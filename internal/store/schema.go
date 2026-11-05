package store

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"invoice/migrations"
)

//go:embed legacy_schema.json
var legacySchema []byte

//go:embed host_schema.json
var hostSchema []byte

type Column struct {
	Table  string `json:"table_name"`
	Name   string `json:"column_name"`
	Type   string `json:"data_type"`
	Length *int   `json:"character_maximum_length"`
}
type Queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func HostPreflight(ctx context.Context, q Queryer) error {
	var version int
	if e := q.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); e != nil {
		return e
	}
	if version < 170000 || version >= 190000 {
		return errors.New("PostgreSQL 17/18 required")
	}
	var cols []Column
	if e := json.Unmarshal(hostSchema, &cols); e != nil {
		return e
	}
	for _, col := range cols {
		var typ string
		e := q.QueryRow(ctx, `SELECT data_type FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name=$2`, col.Table, col.Name).Scan(&typ)
		if e != nil || typ != col.Type {
			return fmt.Errorf("incompatible host column: %s.%s", col.Table, col.Name)
		}
	}
	for _, sql := range []string{
		"SELECT id,username,display_name,email,role,status,deleted_at,auth_version FROM public.users LIMIT 0",
		"SELECT id,user_id,trade_no,money::text,amount,payment_method,payment_provider,create_time,complete_time,status FROM public.top_ups LIMIT 0",
	} {
		rows, e := q.Query(ctx, sql)
		if e != nil {
			return errors.New("host schema incompatible")
		}
		rows.Close()
	}
	return nil
}

func Preflight(ctx context.Context, q Queryer) error {
	if e := HostPreflight(ctx, q); e != nil {
		return e
	}
	var cols []Column
	if e := json.Unmarshal(legacySchema, &cols); e != nil {
		return e
	}
	for _, c := range cols {
		var typ string
		var length *int
		var defaultValue *string
		var identity string
		e := q.QueryRow(ctx, `SELECT data_type,character_maximum_length,column_default,is_identity FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name=$2`, c.Table, c.Name).Scan(&typ, &length, &defaultValue, &identity)
		if e != nil || typ != c.Type || (c.Length != nil && (length == nil || *length != *c.Length)) {
			return fmt.Errorf("incompatible invoice column: %s.%s", c.Table, c.Name)
		}
		if c.Name == "id" && identity != "YES" && defaultValue == nil {
			return errors.New("missing invoice id sequence")
		}
		if c.Name == "id" {
			var last, max *int64
			e := q.QueryRow(ctx, "SELECT pg_sequence_last_value(pg_get_serial_sequence($1,'id')::regclass),(SELECT max(id) FROM public."+c.Table+")", "public."+c.Table).Scan(&last, &max)
			if e != nil || max != nil && (last == nil || *last < *max) {
				return fmt.Errorf("invoice sequence behind rows: %s", c.Table)
			}
		}
	}
	for _, check := range []struct {
		table string
		cols  string
	}{{"invoice_application_items", "top_up_id, active_key"}, {"invoice_applications", "request_no"}} {
		var ok bool
		e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname='public' AND tablename=$1 AND indexdef LIKE 'CREATE UNIQUE INDEX%' AND indexdef LIKE $2)`, check.table, "%("+check.cols+")").Scan(&ok)
		if e != nil || !ok {
			return errors.New("required invoice unique index missing")
		}
	}
	var invalid bool
	e := q.QueryRow(ctx, `SELECT EXISTS(
 SELECT 1 FROM invoice_application_items i LEFT JOIN invoice_applications a ON a.id=i.application_id
 WHERE a.id IS NULL OR i.user_id IS DISTINCT FROM a.user_id OR i.top_up_id IS NULL OR i.amount_cents IS NULL OR i.amount_cents<=0
 OR i.active IS DISTINCT FROM (a.status IN ('pending','invoiced')) OR i.active_key IS NULL OR (i.active AND i.active_key<>'active') OR (NOT i.active AND i.active_key='active')
 UNION ALL SELECT 1 FROM invoice_applications a WHERE a.user_id IS NULL OR a.status NOT IN ('pending','invoiced','rejected','cancelled') OR a.status IS NULL
 OR a.total_amount_cents IS NULL OR a.total_amount_cents<=0 OR a.total_amount_cents IS DISTINCT FROM (SELECT sum(i.amount_cents) FROM invoice_application_items i WHERE i.application_id=a.id)
 )`).Scan(&invalid)
	if e != nil {
		return e
	}
	if invalid {
		return errors.New("legacy invoice consistency check failed; repair separately")
	}
	return nil
}

func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	return migrate(ctx, db, false, "")
}

func migrate(ctx context.Context, db *pgxpool.Pool, initializeFresh bool, grantRole string) error {
	tx, e := db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(73190501)"); e != nil {
		return e
	}
	if e = HostPreflight(ctx, tx); e != nil {
		return e
	}
	var existing int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=ANY($1::text[])`, ownedTables).Scan(&existing); e != nil {
		return e
	}
	// Existing legacy tables are validated before any DDL, never repaired implicitly.
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('invoice_titles','invoice_applications','invoice_application_items')`).Scan(&count); e != nil {
		return e
	}
	if count != 0 || existing != 0 {
		if count != 3 {
			return errors.New("partial legacy schema")
		}
		if e = Preflight(ctx, tx); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS invoice_schema_migrations(version bigint PRIMARY KEY,checksum char(64) NOT NULL,applied_at bigint NOT NULL)`); e != nil {
		return e
	}
	for i, name := range []string{"0001_legacy_tables.sql", "0002_support.sql"} {
		raw, e := migrations.SQL.ReadFile(name)
		if e != nil {
			return e
		}
		h := sha256.Sum256(raw)
		hash := hex.EncodeToString(h[:])
		var old string
		e = tx.QueryRow(ctx, "SELECT checksum FROM invoice_schema_migrations WHERE version=$1", i+1).Scan(&old)
		if e == nil {
			if hash != old {
				return errors.New("migration checksum mismatch")
			}
			continue
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if _, e = tx.Exec(ctx, string(raw)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, "INSERT INTO invoice_schema_migrations VALUES($1,$2,$3)", i+1, hash, time.Now().Unix()); e != nil {
			return e
		}
	}
	if e = Preflight(ctx, tx); e != nil {
		return e
	}
	if initializeFresh && existing == 0 {
		if e = initializeFreshInstall(ctx, tx); e != nil {
			return e
		}
	}
	if grantRole != "" {
		if e = grantRuntime(ctx, tx, grantRole); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

func Ready(ctx context.Context, db *pgxpool.Pool) error {
	var checksums map[string]string
	var initialized bool
	var tables int
	e := db.QueryRow(ctx, `SELECT coalesce((SELECT jsonb_object_agg(version::text,checksum) FROM invoice_schema_migrations),'{}'::jsonb),EXISTS(SELECT 1 FROM invoice_initializations WHERE name='legacy-permissions-v1'),(SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('invoice_titles','invoice_applications','invoice_application_items','invoice_user_permissions','invoice_write_requests','invoice_operation_logs','invoice_schema_migrations','invoice_initializations'))`).Scan(&checksums, &initialized, &tables)
	if e != nil {
		return e
	}
	if len(checksums) != 2 || !initialized || tables != 8 {
		return errors.New("schema or initialization incomplete")
	}
	for i, name := range []string{"0001_legacy_tables.sql", "0002_support.sql"} {
		raw, e := migrations.SQL.ReadFile(name)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(raw)
		if checksums[fmt.Sprint(i+1)] != hex.EncodeToString(sum[:]) {
			return errors.New("schema checksum mismatch")
		}
	}
	return nil
}
