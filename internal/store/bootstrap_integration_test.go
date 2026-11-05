package store

import (
	"context"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBootstrapNewHost(t *testing.T) {
	dsn := os.Getenv("INVOICE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated database required")
	}
	isolatedDSN(t, dsn)
	ctx := context.Background()
	db, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, e = db.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;
 CREATE TABLE users(id bigint PRIMARY KEY,username text,display_name text,email text,role bigint,status bigint,deleted_at timestamptz,auth_version bigint NOT NULL DEFAULT 1);
 CREATE TABLE top_ups(id bigint PRIMARY KEY,user_id bigint,trade_no varchar(255),money numeric,amount bigint,payment_method varchar(50),payment_provider varchar(50),create_time bigint,complete_time bigint,status text);
 INSERT INTO users(id,role,status) VALUES(1,100,1),(2,1,1);`)
	if e != nil {
		t.Fatal(e)
	}
	if e = Bootstrap(ctx, db, db, "missing_role_"+uuid.NewString()[:8]); e == nil {
		t.Fatal("expected grant failure")
	}
	var tables int
	if e = db.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=ANY($1::text[])`, ownedTables).Scan(&tables); e != nil || tables != 0 {
		t.Fatal("failed bootstrap left partial tables", tables, e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- Bootstrap(ctx, db, db, "") }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = Ready(ctx, db); e != nil {
		t.Fatal(e)
	}
	var enabled int
	var source string
	if e = db.QueryRow(ctx, "SELECT count(*) FROM invoice_user_permissions WHERE enabled").Scan(&enabled); e != nil || enabled != 0 {
		t.Fatal("fresh users granted", e)
	}
	if e = db.QueryRow(ctx, "SELECT source_kind FROM invoice_initializations").Scan(&source); e != nil || source != "fresh_install" {
		t.Fatal("fresh marker", source, e)
	}
	if _, e = db.Exec(ctx, "INSERT INTO invoice_user_permissions(user_id,enabled,version,source,created_at,updated_at) VALUES(2,true,1,'manual',1,1)"); e != nil {
		t.Fatal(e)
	}
	if e = Bootstrap(ctx, db, db, ""); e != nil {
		t.Fatal(e)
	}
	_ = db.QueryRow(ctx, "SELECT count(*) FROM invoice_user_permissions WHERE enabled").Scan(&enabled)
	if enabled != 1 {
		t.Fatal("existing grant changed")
	}
	role := "invoice_test_boot_" + uuid.NewString()[:8]
	if _, e = db.Exec(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD 'invoice_test_only'"); e != nil {
		t.Fatal(e)
	}
	defer func() { _, _ = db.Exec(ctx, "DROP OWNED BY "+role); _, _ = db.Exec(ctx, "DROP ROLE "+role) }()
	u, _ := url.Parse(dsn)
	u.User = url.UserPassword(role, "invoice_test_only")
	runtimeDB, e := pgxpool.New(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	defer runtimeDB.Close()
	if e = Bootstrap(ctx, runtimeDB, db, role); e != nil {
		t.Fatal("separate migration role", e)
	}
	if e = Preflight(ctx, runtimeDB); e != nil {
		t.Fatal("runtime grants incomplete", e)
	}
	if _, e = runtimeDB.Exec(ctx, "UPDATE users SET status=1"); e == nil {
		t.Fatal("host write granted")
	}
	if _, e = runtimeDB.Exec(ctx, "DELETE FROM invoice_operation_logs"); e == nil {
		t.Fatal("audit delete granted")
	}
	if e = Ensure(ctx, runtimeDB, "postgres://unreachable:5432/other"); e != nil {
		t.Fatal("ready runtime must not open migration connection", e)
	}
	if _, e = db.Exec(ctx, `DROP TABLE invoice_titles,invoice_applications,invoice_application_items,invoice_user_permissions,invoice_operation_logs,invoice_write_requests,invoice_schema_migrations,invoice_initializations`); e != nil {
		t.Fatal(e)
	}
	if e = Ensure(ctx, runtimeDB, "postgres://unreachable:5432/other"); e == nil {
		t.Fatal("cross-database migration allowed")
	}
	multiCfg := runtimeDB.Config()
	multiCfg.ConnConfig.Fallbacks = append(multiCfg.ConnConfig.Fallbacks, &pgconn.FallbackConfig{Host: "other-database", Port: multiCfg.ConnConfig.Port})
	multiDB, e := pgxpool.NewWithConfig(ctx, multiCfg)
	if e != nil {
		t.Fatal(e)
	}
	defer multiDB.Close()
	if e = Ensure(ctx, multiDB, dsn); e == nil {
		t.Fatal("runtime cross-endpoint fallback permitted automatic migration")
	}
	if e = Ensure(ctx, runtimeDB, dsn); e != nil {
		t.Fatal("fresh bootstrap via separate DSN", e)
	}
	if e = Ready(ctx, runtimeDB); e != nil {
		t.Fatal("fresh runtime not ready", e)
	}
}
