package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrationAndInitialization(t *testing.T) {
	dsn := os.Getenv("INVOICE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated test database required")
	}
	ctx := context.Background()
	isolatedDSN(t, dsn)
	db, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;
 CREATE TABLE users(id bigint PRIMARY KEY,username text,display_name text,email text,role bigint,status bigint,deleted_at timestamptz,auth_version bigint NOT NULL DEFAULT 1,invoice_enabled boolean);
 CREATE TABLE top_ups(id bigint PRIMARY KEY,user_id bigint,trade_no varchar(255),money numeric,amount bigint,payment_method varchar(50),payment_provider varchar(50),create_time bigint,complete_time bigint,status text);
 CREATE TABLE options(key text PRIMARY KEY,value text);
 INSERT INTO users(id,username,role,status,invoice_enabled) VALUES(1,'root',100,1,false),(2,'user',1,1,true),(3,'new',1,1,NULL);
 INSERT INTO options VALUES('SidebarModulesAdmin','{"personal":{"invoice":false}}');`); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(ctx, db); e != nil {
		t.Fatal(e)
	}
	if e = Migrate(ctx, db); e != nil {
		t.Fatal("repeat migration", e)
	}
	if e = Ready(ctx, db); e == nil {
		t.Fatal("ready without initialization")
	}
	if e = Bootstrap(ctx, db, db, ""); e == nil {
		t.Fatal("existing install silently skipped legacy initialization")
	}
	stats, e := Initialize(ctx, db, nil, true)
	if e != nil || stats.Inserted != 3 || stats.Enabled != 1 {
		t.Fatalf("dry run %+v %v", stats, e)
	}
	var n int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM invoice_user_permissions").Scan(&n)
	if n != 0 {
		t.Fatal("dry run wrote data")
	}
	if _, e = Initialize(ctx, db, nil, false); e != nil {
		t.Fatal(e)
	}
	if e = Ready(ctx, db); e != nil {
		t.Fatal(e)
	}
	// Simulate a legacy-only populated database: remove only new support structures,
	// then upgrade twice while preserving rows and sequences.
	_, e = db.Exec(ctx, `INSERT INTO invoice_titles(user_id,company_name) VALUES(2,'legacy company');
 INSERT INTO invoice_applications(id,request_no,user_id,status,total_amount_cents,currency,created_at) VALUES(50,'legacy-50',2,'pending',100,'CNY',1700000000);
 SELECT setval(pg_get_serial_sequence('invoice_applications','id'),50);
 INSERT INTO invoice_application_items(application_id,user_id,top_up_id,amount_cents,active,active_key) VALUES(50,2,10,100,true,'active');
 DROP TABLE invoice_user_permissions,invoice_write_requests,invoice_operation_logs,invoice_initializations,invoice_schema_migrations;`)
	if e != nil {
		t.Fatal(e)
	}
	if e = Migrate(ctx, db); e != nil {
		t.Fatal("legacy upgrade", e)
	}
	if e = Migrate(ctx, db); e != nil {
		t.Fatal("legacy repeat", e)
	}
	var legacy string
	var nextID int64
	if e = db.QueryRow(ctx, "SELECT company_name FROM invoice_titles WHERE user_id=2").Scan(&legacy); e != nil || legacy != "legacy company" {
		t.Fatal("legacy row changed")
	}
	_ = db.QueryRow(ctx, "SELECT nextval(pg_get_serial_sequence('invoice_applications','id'))").Scan(&nextID)
	if nextID <= 50 {
		t.Fatal("legacy sequence reset")
	}
	if _, e = Initialize(ctx, db, nil, false); e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(ctx, "INSERT INTO users(id,status,role) VALUES(4,1,1)")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Initialize(ctx, db, nil, false); e != nil {
		t.Fatal(e)
	}
	_ = db.QueryRow(ctx, "SELECT count(*) FROM invoice_user_permissions").Scan(&n)
	if n != 3 {
		t.Fatal("repeat imported later user")
	}

	for _, global := range []bool{false, true} {
		_, e = db.Exec(ctx, "DELETE FROM invoice_initializations; DELETE FROM invoice_user_permissions; DELETE FROM invoice_operation_logs")
		if e != nil {
			t.Fatal(e)
		}
		stats, e := Initialize(ctx, db, &global, false)
		expected := int64(1)
		if global {
			expected = 4
		}
		if e != nil || stats.Enabled != expected || stats.Inserted != 4 {
			t.Fatalf("global matrix %+v %v", stats, e)
		}
	}
	_, _ = db.Exec(ctx, "DELETE FROM invoice_initializations; DELETE FROM invoice_user_permissions; DELETE FROM invoice_operation_logs; INSERT INTO invoice_user_permissions(user_id,enabled,version,source,created_at,updated_at) VALUES(2,false,7,'manual',1,1)")
	global := true
	stats, e = Initialize(ctx, db, &global, false)
	if e != nil || stats.Skipped != 1 {
		t.Fatal("manual permission overwritten", stats, e)
	}
	var version int64
	var enabled bool
	_ = db.QueryRow(ctx, "SELECT version,enabled FROM invoice_user_permissions WHERE user_id=2").Scan(&version, &enabled)
	if version != 7 || enabled {
		t.Fatal("manual revocation not preserved")
	}
	_, _ = db.Exec(ctx, "DELETE FROM invoice_initializations; DELETE FROM invoice_user_permissions; DELETE FROM invoice_operation_logs; UPDATE options SET value='invalid' WHERE key='SidebarModulesAdmin'")
	if _, e = Initialize(ctx, db, nil, true); e == nil {
		t.Fatal("accepted ambiguous legacy option")
	}
	_, e = db.Exec(ctx, `CREATE FUNCTION fail_import() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.user_id=2 THEN RAISE EXCEPTION 'test interruption'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_import BEFORE INSERT ON invoice_user_permissions FOR EACH ROW EXECUTE FUNCTION fail_import()`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Initialize(ctx, db, &global, false); e == nil {
		t.Fatal("injected import failure not reported")
	}
	_ = db.QueryRow(ctx, "SELECT (SELECT count(*) FROM invoice_user_permissions)+(SELECT count(*) FROM invoice_operation_logs)+(SELECT count(*) FROM invoice_initializations)").Scan(&n)
	if n != 0 {
		t.Fatal("initialization partly committed", n)
	}
	_, _ = db.Exec(ctx, "DROP TRIGGER fail_import ON invoice_user_permissions; DROP FUNCTION fail_import()")
	_, e = db.Exec(ctx, "UPDATE invoice_schema_migrations SET checksum='bad' WHERE version=1")
	if e != nil {
		t.Fatal(e)
	}
	if e = Migrate(ctx, db); e == nil {
		t.Fatal("accepted changed checksum")
	}
}
