package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ownedTables = []string{"invoice_titles", "invoice_applications", "invoice_application_items", "invoice_user_permissions", "invoice_operation_logs", "invoice_write_requests", "invoice_schema_migrations", "invoice_initializations"}

// Ensure avoids even opening the optional DDL connection on a ready installation.
func Ensure(ctx context.Context, runtimeDB *pgxpool.Pool, migrationURL string) error {
	if Ready(ctx, runtimeDB) == nil {
		return nil
	}
	runtimeCfg := runtimeDB.Config().ConnConfig
	for _, fallback := range runtimeCfg.Fallbacks {
		if fallback.Host != runtimeCfg.Host || fallback.Port != runtimeCfg.Port {
			return fmt.Errorf("automatic migration requires a single runtime database endpoint")
		}
	}
	if migrationURL == "" {
		return Bootstrap(ctx, runtimeDB, runtimeDB, "")
	}
	cfg, e := pgxpool.ParseConfig(migrationURL)
	if e != nil {
		return fmt.Errorf("invalid MIGRATION_DATABASE_URL")
	}
	if cfg.ConnConfig.Host != runtimeCfg.Host || cfg.ConnConfig.Port != runtimeCfg.Port || cfg.ConnConfig.Database != runtimeCfg.Database {
		return fmt.Errorf("MIGRATION_DATABASE_URL must target the same host, port and database as DATABASE_URL")
	}
	for _, fallback := range cfg.ConnConfig.Fallbacks {
		if fallback.Host != runtimeCfg.Host || fallback.Port != runtimeCfg.Port {
			return fmt.Errorf("migration connection cannot fall back to a different database endpoint")
		}
	}
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["search_path"] = "public"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	db, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		return fmt.Errorf("cannot open migration connection")
	}
	defer db.Close()
	var role string
	if e = runtimeDB.QueryRow(ctx, "SELECT current_user").Scan(&role); e != nil {
		return e
	}
	return Bootstrap(ctx, runtimeDB, db, role)
}

// Bootstrap creates only Invoice-owned objects. A populated installation without
// an initialization marker still requires the explicit legacy import command.
func Bootstrap(ctx context.Context, runtimeDB, migrationDB *pgxpool.Pool, grantRole string) error {
	if Ready(ctx, runtimeDB) == nil {
		return nil
	}
	if e := migrate(ctx, migrationDB, true, grantRole); e != nil {
		return fmt.Errorf("automatic Invoice schema initialization failed (check CREATE privileges or MIGRATION_DATABASE_URL): %w", e)
	}
	if e := Ready(ctx, runtimeDB); e != nil {
		return fmt.Errorf("Invoice schema not ready; existing installations require init-permissions, new runtime roles require table grants: %w", e)
	}
	return nil
}

func initializeFreshInstall(ctx context.Context, tx pgx.Tx) error {
	var total, maxID int64
	if e := tx.QueryRow(ctx, "SELECT count(*),coalesce(max(id),0) FROM users").Scan(&total, &maxID); e != nil {
		return e
	}
	h := sha256.Sum256([]byte("invoice-fresh-install-v1:default-denied"))
	// Missing permission rows already mean false/version0. Do not fabricate legacy
	// imports or grant any existing/new host account self-service access.
	_, e := tx.Exec(ctx, `INSERT INTO invoice_initializations(name,batch_id,global_enabled,source_kind,source_fingerprint,snapshot_max_user_id,snapshot_user_count,inserted_count,enabled_count,skipped_count,completed_at) VALUES('legacy-permissions-v1',$1,false,'fresh_install',$2,$3,$4,0,0,0,$5)`, uuid.NewString(), hex.EncodeToString(h[:]), maxID, total, time.Now().Unix())
	return e
}

func grantRuntime(ctx context.Context, tx pgx.Tx, role string) error {
	target := pgx.Identifier{role}.Sanitize()
	for _, statement := range []string{
		"GRANT USAGE ON SCHEMA public TO " + target,
		"GRANT SELECT(id,username,display_name,email,role,status,deleted_at,auth_version) ON public.users TO " + target,
		"GRANT SELECT(id,user_id,trade_no,money,amount,payment_method,payment_provider,create_time,complete_time,status) ON public.top_ups TO " + target,
		"GRANT SELECT,INSERT,UPDATE,DELETE ON public.invoice_titles,public.invoice_applications,public.invoice_application_items,public.invoice_user_permissions,public.invoice_write_requests TO " + target,
		"GRANT SELECT,INSERT ON public.invoice_operation_logs TO " + target,
		"GRANT SELECT ON public.invoice_schema_migrations,public.invoice_initializations TO " + target,
	} {
		if _, e := tx.Exec(ctx, statement); e != nil {
			return e
		}
	}
	for _, table := range []string{"invoice_titles", "invoice_applications", "invoice_application_items", "invoice_operation_logs"} {
		var schema, name string
		if e := tx.QueryRow(ctx, `SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=pg_get_serial_sequence($1,'id')::regclass`, "public."+table).Scan(&schema, &name); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, "GRANT USAGE,SELECT ON SEQUENCE "+pgx.Identifier{schema, name}.Sanitize()+" TO "+target); e != nil {
			return e
		}
	}
	return nil
}
