package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"invoice/internal/auth"
	"invoice/internal/config"
	"invoice/internal/store"
)

func token(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"iss":"new-api","aud":["new-api-dashboard"],"token_use":"access","sub":%q,"sid":"test-session","uv":1,"sv":1,"exp":%d}`, id, time.Now().Add(time.Hour).Unix()))) + ".signature"
}

func TestRealAPILifecycle(t *testing.T) {
	dsn := os.Getenv("INVOICE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated test database required")
	}
	ctx := context.Background()
	safeURL, parseError := url.Parse(dsn)
	if parseError != nil || safeURL.Path != "/invoice_test" || (safeURL.Hostname() != "127.0.0.1" && safeURL.Hostname() != "localhost" && safeURL.Hostname() != "host.docker.internal") {
		t.Fatal("tests require isolated loopback invoice_test")
	}
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	name := "invoice_test_api_" + uuid.NewString()[:8]
	if _, e = admin.Exec(ctx, "CREATE DATABASE "+name); e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(dsn)
	u.Path = "/" + name
	db, e := pgxpool.New(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { db.Close(); _, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)") }()
	_, e = db.Exec(ctx, `CREATE TABLE users(id bigint PRIMARY KEY,username text,display_name text,email text,role bigint,status bigint,deleted_at timestamptz,auth_version bigint NOT NULL DEFAULT 1,invoice_enabled boolean);
 CREATE TABLE top_ups(id bigint PRIMARY KEY,user_id bigint,trade_no varchar(255),money numeric,amount bigint,payment_method varchar(50),payment_provider varchar(50),create_time bigint,complete_time bigint,status text);
 INSERT INTO users VALUES(1,'root','管理员','root@example.test',100,1,NULL,1,false),(2,'user','用户','user@example.test',1,1,NULL,1,true),(3,'other','他人','other@example.test',1,1,NULL,1,false);
 INSERT INTO top_ups VALUES(10,2,'order-a',12.005,999999,'alipay','epay',1700000000,0,'success'),(11,2,'order-b',3.50,1,'alipay','epay',1700000000,1700000010,'success'),(12,3,'other',10,1,'alipay','epay',1700000000,0,'success');`)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Migrate(ctx, db); e != nil {
		t.Fatal(e)
	}
	f := false
	if _, e = store.Initialize(ctx, db, &f, false); e != nil {
		t.Fatal(e)
	}
	roleName := "invoice_test_runtime_" + uuid.NewString()[:8]
	if _, e = db.Exec(ctx, "CREATE ROLE "+roleName+" LOGIN PASSWORD 'invoice_test_only'"); e != nil {
		t.Fatal(e)
	}
	defer func() { _, _ = db.Exec(ctx, "DROP OWNED BY "+roleName); _, _ = admin.Exec(ctx, "DROP ROLE "+roleName) }()
	_, e = db.Exec(ctx, `GRANT USAGE ON SCHEMA public TO `+roleName+`;
 GRANT SELECT(id,username,display_name,email,role,status,deleted_at,auth_version) ON users TO `+roleName+`;
 GRANT SELECT(id,user_id,trade_no,money,amount,payment_method,payment_provider,create_time,complete_time,status) ON top_ups TO `+roleName+`;
 GRANT SELECT,INSERT,UPDATE,DELETE ON invoice_titles,invoice_applications,invoice_application_items,invoice_user_permissions,invoice_write_requests TO `+roleName+`;
 GRANT SELECT,INSERT ON invoice_operation_logs TO `+roleName+`;
 GRANT SELECT ON invoice_schema_migrations,invoice_initializations TO `+roleName+`;
 GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO `+roleName)
	if e != nil {
		t.Fatal(e)
	}
	runtimeURL := *u
	runtimeURL.User = url.UserPassword(roleName, "invoice_test_only")
	runtimeDB, e := pgxpool.New(ctx, runtimeURL.String())
	if e != nil {
		t.Fatal(e)
	}
	defer runtimeDB.Close()
	if e = store.Preflight(ctx, runtimeDB); e != nil {
		t.Fatal("restricted preflight", e)
	}
	for _, sql := range []string{"UPDATE users SET status=1 WHERE id=2", "SELECT id FROM top_ups FOR UPDATE", "UPDATE invoice_operation_logs SET remark='x'", "DELETE FROM invoice_operation_logs"} {
		if _, e = runtimeDB.Exec(ctx, sql); e == nil {
			t.Fatal("runtime allowed forbidden SQL", sql)
		}
	}
	var hostDown atomic.Bool
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hostDown.Load() {
			w.WriteHeader(503)
			return
		}
		claims, err := auth.Classify(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if err != nil {
			w.WriteHeader(401)
			return
		}
		id := claims.Sub
		role := 1
		if id == "1" {
			id = "1"
			role = 100
		}
		fmt.Fprintf(w, `{"success":true,"data":{"id":%s,"username":"u","role":%d,"status":1}}`, id, role)
	}))
	defer host.Close()
	cfg := config.Config{HostURL: host.URL, Origin: "http://localhost:3000", FileRoot: t.TempDir(), AuthTimeout: time.Second, CurrencyRules: []config.CurrencyRule{{Provider: "epay", Method: "alipay", Currency: "CNY"}}}
	handler, e := New(runtimeDB, cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer handler.Close()
	request := func(actor, method, path string, body any, key string) (int, map[string]any) {
		b, _ := json.Marshal(body)
		if method == "DELETE" {
			b = nil
		}
		r := httptest.NewRequest(method, "http://localhost:3000/invoice-api/v1"+path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token(actor))
		r.Header.Set("Origin", cfg.Origin)
		r.Header.Set("Content-Type", "application/json")
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var out map[string]any
		if json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Errorf("not JSON %s", w.Body.String())
		}
		return w.Code, out
	}
	must := func(actor, method, path string, b any, key string) map[string]any {
		status, out := request(actor, method, path, b, key)
		if status != 200 {
			t.Fatalf("%s %s: %d %+v", method, path, status, out)
		}
		return out["data"].(map[string]any)
	}
	if code, _ := request("2", "GET", "/admin/users", nil, ""); code != 403 {
		t.Fatal("non-admin", code)
	}
	title := map[string]any{"company_name": "企业", "tax_no": "001122", "email": "invoice@example.test"}
	key := uuid.NewString()
	result := must("2", "POST", "/titles", title, key)
	titleID := result["resource_id"].(string)
	if got := must("2", "POST", "/titles", title, key); got["resource_id"] != titleID {
		t.Fatal("idempotency mismatch")
	}
	title["company_name"] = "变更"
	if code, _ := request("2", "POST", "/titles", title, key); code != 409 {
		t.Fatal("same key changed body", code)
	}

	must("2", "GET", "/titles", nil, "")
	must("1", "GET", "/admin/users/2/titles", nil, "")
	must("2", "GET", "/orders/eligible", nil, "")
	must("1", "GET", "/admin/users/2/orders/eligible", nil, "")
	disposable := must("2", "POST", "/titles", map[string]any{"company_name": "可删除抬头", "tax_no": "delete", "email": "delete@example.test"}, uuid.NewString())["resource_id"].(string)
	deleteKey := uuid.NewString()
	must("2", "DELETE", "/titles/"+disposable, nil, deleteKey)
	must("2", "DELETE", "/titles/"+disposable, nil, deleteKey)
	create := map[string]any{"title_id": titleID, "topup_ids": []string{"10", "11", "10"}, "user_remark": "合并"}
	key = uuid.NewString()
	created := must("2", "POST", "/applications", create, key)
	id := created["resource_id"].(string)
	detail := must("2", "GET", "/applications/"+id, nil, "")
	must("2", "GET", "/applications", nil, "")
	must("1", "GET", "/admin/applications?user_id=2", nil, "")
	must("1", "GET", "/admin/applications/"+id, nil, "")

	if detail["total_amount_cents"] != "1551" || detail["item_count"] != float64(2) {
		t.Fatalf("wrong money %+v", detail)
	}
	if detail["items"].([]any)[0].(map[string]any)["payment_time_source"] != "create_fallback" {
		t.Fatal("fallback payment time provenance lost")
	}
	updated := map[string]any{"company_name": "新企业名", "tax_no": "001122", "email": "invoice@example.test"}
	must("2", "PUT", "/titles/"+titleID, updated, uuid.NewString())
	if snapshot := must("2", "GET", "/applications/"+id, nil, ""); snapshot["company_name"] != "企业" {
		t.Fatal("history snapshot rewritten")
	}
	must("2", "GET", "/write-requests/"+key, nil, "")
	if code, _ := request("3", "GET", "/applications/"+id, nil, ""); code != 404 {
		t.Fatal("cross-user detail", code)
	}
	if code, _ := request("2", "POST", "/applications", create, uuid.NewString()); code != 409 {
		t.Fatal("duplicate occupied order", code)
	}
	must("1", "PUT", "/admin/users/2/permission", map[string]any{"enabled": false, "expected_version": "0"}, uuid.NewString())
	if code, _ := request("2", "POST", "/applications", create, uuid.NewString()); code != 403 {
		t.Fatal("revoked can submit", code)
	}
	must("2", "GET", "/applications/"+id, nil, "")
	must("2", "POST", "/applications/"+id+"/cancel", map[string]any{}, uuid.NewString())
	if code, _ := request("2", "POST", "/applications/"+id+"/cancel", map[string]any{}, uuid.NewString()); code != 409 {
		t.Fatal("terminal state", code)
	}
	// Administrators can submit for a denied user, without implicitly granting permission.
	create["user_id"] = "2"
	created = must("1", "POST", "/admin/applications", create, uuid.NewString())
	id = created["resource_id"].(string)
	must("1", "POST", "/admin/applications/"+id+"/reject", map[string]any{"admin_remark": "税号需核对"}, uuid.NewString())
	// Two connections racing for the same orders must produce one application only.
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := request("1", "POST", "/admin/applications", create, uuid.NewString())
			codes <- code
		}()
	}
	wg.Wait()
	close(codes)
	success, conflict := 0, 0
	for code := range codes {
		if code == 200 {
			success++
		}
		if code == 409 {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("race: success=%d conflict=%d", success, conflict)
	}
	var enabled bool
	_ = db.QueryRow(ctx, "SELECT enabled FROM invoice_user_permissions WHERE user_id=2").Scan(&enabled)
	if enabled {
		t.Fatal("admin creation granted permission")
	}
	must("1", "GET", "/admin/operations", nil, "")
	hostDown.Store(true)
	if code, _ := request("2", "GET", "/me", nil, ""); code != 503 {
		t.Fatal("auth failed open", code)
	}
	hostDown.Store(false)
	_, _ = db.Exec(ctx, "UPDATE users SET role=1 WHERE id=1")
	if code, _ := request("1", "GET", "/admin/users", nil, ""); code != 403 {
		t.Fatal("current role ignored", code)
	}
	_, _ = db.Exec(ctx, "UPDATE users SET role=100 WHERE id=1")
	for _, raw := range []string{`{"company_name":"x","company_name":"y","tax_no":"x","email":"x@e.test"}`, `{"company_name":"x","tax_no":"x","email":"x@e.test","role":100}`} {
		r := httptest.NewRequest("POST", "http://localhost:3000/invoice-api/v1/admin/users/2/titles", strings.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token("1"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", cfg.Origin)
		r.Header.Set("Idempotency-Key", uuid.NewString())
		wr := httptest.NewRecorder()
		handler.ServeHTTP(wr, r)
		if wr.Code != 400 {
			t.Fatal("strict JSON", wr.Code)
		}
	}
	var appID string
	_ = db.QueryRow(ctx, "SELECT id::text FROM invoice_applications WHERE status='pending' LIMIT 1").Scan(&appID)
	var upload bytes.Buffer
	multi := multipart.NewWriter(&upload)
	head := textproto.MIMEHeader{}
	head.Set("Content-Disposition", `form-data; name="file"; filename="invoice.pdf"`)
	head.Set("Content-Type", "application/pdf")
	part, _ := multi.CreatePart(head)
	pdf := []byte("%PDF-1.4\n%%EOF\n")
	_, _ = part.Write(pdf)
	_ = multi.Close()
	uploadKey := uuid.NewString()
	r := httptest.NewRequest("POST", "http://localhost:3000/invoice-api/v1/admin/applications/"+appID+"/complete", bytes.NewReader(upload.Bytes()))
	r.Header.Set("Authorization", "Bearer "+token("1"))
	r.Header.Set("Origin", cfg.Origin)
	r.Header.Set("Idempotency-Key", uploadKey)
	r.Header.Set("Content-Type", multi.FormDataContentType())
	wr := httptest.NewRecorder()
	handler.ServeHTTP(wr, r)
	if wr.Code != 200 {
		t.Fatalf("complete: %d %s", wr.Code, wr.Body.String())
	}
	r = httptest.NewRequest("GET", "http://localhost:3000/invoice-api/v1/applications/"+appID+"/file", nil)
	r.Header.Set("Authorization", "Bearer "+token("2"))
	wr = httptest.NewRecorder()
	handler.ServeHTTP(wr, r)
	got, _ := io.ReadAll(wr.Body)
	if wr.Code != 200 || !bytes.Equal(got, pdf) || wr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("download %d %q", wr.Code, got)
	}

	r = httptest.NewRequest("GET", "http://localhost:3000/invoice-api/v1/admin/applications/"+appID+"/file", nil)
	r.Header.Set("Authorization", "Bearer "+token("1"))
	wr = httptest.NewRecorder()
	handler.ServeHTTP(wr, r)
	if wr.Code != 200 || !bytes.Equal(wr.Body.Bytes(), pdf) {
		t.Fatal("admin file download failed", wr.Code)
	}
	var originalPath string
	_ = db.QueryRow(ctx, "SELECT invoice_file_path FROM invoice_applications WHERE id=$1", appID).Scan(&originalPath)
	_, _ = db.Exec(ctx, "UPDATE invoice_applications SET invoice_file_path='missing.pdf' WHERE id=$1", appID)
	if code, _ := request("2", "GET", "/applications/"+appID+"/file", nil, ""); code != 410 {
		t.Fatal("missing file status", code)
	}
	_, _ = db.Exec(ctx, "UPDATE invoice_applications SET invoice_file_path=$1 WHERE id=$2", originalPath, appID)
	// A failed audit insert must roll back the title and idempotency registration.
	_, e = db.Exec(ctx, `CREATE FUNCTION reject_audit_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type='title_create_admin' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_audit_test BEFORE INSERT ON invoice_operation_logs FOR EACH ROW EXECUTE FUNCTION reject_audit_test()`)
	if e != nil {
		t.Fatal(e)
	}
	var before, after int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM invoice_titles").Scan(&before)
	failedKey := uuid.NewString()
	if code, _ := request("1", "POST", "/admin/users/2/titles", updated, failedKey); code != 500 {
		t.Fatal("audit failure not reported", code)
	}
	_ = db.QueryRow(ctx, "SELECT count(*) FROM invoice_titles").Scan(&after)
	if after != before {
		t.Fatal("audit rollback lost")
	}
	if code, _ := request("1", "GET", "/write-requests/"+failedKey, nil, ""); code != 404 {
		t.Fatal("failed request committed")
	}
	_, _ = db.Exec(ctx, "DROP TRIGGER reject_audit_test ON invoice_operation_logs; DROP FUNCTION reject_audit_test()")
	// Same actor/key on two connections produces exactly one resource and one audit.
	dupeKey := uuid.NewString()
	dupeIDs := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, out := request("1", "POST", "/admin/users/2/titles", updated, dupeKey)
			if code != 200 {
				t.Errorf("same-key race %d", code)
				return
			}
			dupeIDs <- out["data"].(map[string]any)["resource_id"].(string)
		}()
	}
	wg.Wait()
	close(dupeIDs)
	last := ""
	for got := range dupeIDs {
		if last != "" && last != got {
			t.Fatal("two resources for same key")
		}
		last = got
	}
	var auditCount int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM invoice_operation_logs l JOIN invoice_write_requests w ON w.id=l.write_request_id WHERE w.idempotency_key=$1", dupeKey).Scan(&auditCount)
	if auditCount != 1 {
		t.Fatal("same-key audit duplicated", auditCount)
	}
	// Two administrators confirm the same old version: only one state change wins.
	must("1", "PUT", "/admin/users/2/permission", map[string]any{"enabled": true, "expected_version": "1"}, uuid.NewString())
	versionCodes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := request("1", "PUT", "/admin/users/2/permission", map[string]any{"enabled": false, "expected_version": "2"}, uuid.NewString())
			versionCodes <- code
		}()
	}
	wg.Wait()
	close(versionCodes)
	ok, stale := 0, 0
	for code := range versionCodes {
		if code == 200 {
			ok++
		}
		if code == 409 {
			stale++
		}
	}
	if ok != 1 || stale != 1 {
		t.Fatal("permission version race", ok, stale)
	}
	// Prepare a second application and lose its real PostgreSQL COMMIT response.
	_, e = db.Exec(ctx, "INSERT INTO top_ups VALUES(13,2,'commit-lost',20,1,'alipay','epay',1700000000,0,'success')")
	if e != nil {
		t.Fatal(e)
	}
	special := must("1", "POST", "/admin/applications", map[string]any{"user_id": "2", "title_id": titleID, "topup_ids": []string{"13"}}, uuid.NewString())["resource_id"].(string)
	proxyAddress, armed := commitProxy(t, u.Host)
	proxyURL := runtimeURL
	proxyURL.Host = proxyAddress
	proxyDB, e := pgxpool.New(ctx, proxyURL.String())
	if e != nil {
		t.Fatal(e)
	}
	defer proxyDB.Close()
	proxyHandler, e := New(proxyDB, cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer proxyHandler.Close()
	if e = proxyDB.Ping(ctx); e != nil {
		t.Fatal(e)
	}
	armed.Store(true)
	lostKey := uuid.NewString()
	r = httptest.NewRequest("POST", "http://localhost:3000/invoice-api/v1/admin/applications/"+special+"/complete", bytes.NewReader(upload.Bytes()))
	r.Header.Set("Authorization", "Bearer "+token("1"))
	r.Header.Set("Origin", cfg.Origin)
	r.Header.Set("Idempotency-Key", lostKey)
	r.Header.Set("Content-Type", multi.FormDataContentType())
	wr = httptest.NewRecorder()
	proxyHandler.ServeHTTP(wr, r)
	if wr.Code != 503 {
		t.Fatalf("expected unknown commit, got %d %s", wr.Code, wr.Body.String())
	}
	recovered := must("1", "GET", "/write-requests/"+lostKey, nil, "")
	if recovered["committed"] != true {
		t.Fatal("lost COMMIT not recovered")
	}
	var savedPath string
	if e = db.QueryRow(ctx, "SELECT invoice_file_path FROM invoice_applications WHERE id=$1", special).Scan(&savedPath); e != nil {
		t.Fatal(e)
	}
	retained, e := handler.Files.Open(savedPath)
	if e != nil {
		t.Fatal("committed file deleted after unknown result", e)
	}
	retained.Close()

	_, e = db.Exec(ctx, `INSERT INTO top_ups VALUES(14,2,'recheck',20,1,'alipay','epay',1700000000,0,'success'); CREATE FUNCTION change_order_during_create() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$ BEGIN IF NEW.top_up_id=14 THEN UPDATE top_ups SET status='refunded' WHERE id=14; END IF; RETURN NEW; END $$; CREATE TRIGGER change_order_during_create AFTER INSERT ON invoice_application_items FOR EACH ROW EXECUTE FUNCTION change_order_during_create()`)
	if e != nil {
		t.Fatal(e)
	}
	if code, _ := request("1", "POST", "/admin/applications", map[string]any{"user_id": "2", "title_id": titleID, "topup_ids": []string{"14"}}, uuid.NewString()); code != 409 {
		t.Fatal("order not rechecked before commit", code)
	}
	_, _ = db.Exec(ctx, "DROP TRIGGER change_order_during_create ON invoice_application_items; DROP FUNCTION change_order_during_create()")

	if code, _ := request("1", "POST", "/admin/applications", map[string]any{"user_id": "2", "title_id": titleID, "topup_ids": []string{"14"}, "expected_order_amounts": map[string]string{"14": "1000"}}, uuid.NewString()); code != 409 {
		t.Fatal("confirmed stale amount accepted", code)
	}
	for range 241 {
		r := httptest.NewRequest("GET", "http://localhost:3000/invoice-api/v1/me", nil)
		wr := httptest.NewRecorder()
		handler.ServeHTTP(wr, r)
	}
	if code, _ := request("2", "GET", "/me", nil, ""); code != 200 {
		t.Fatal("unauthenticated flood blocked authenticated user behind same proxy", code)
	}

	filtered := must("1", "GET", "/admin/users/3/orders/eligible?topup_ids=12&page_size=100", nil, "")
	if filtered["total"] != float64(1) || filtered["items"].([]any)[0].(map[string]any)["id"] != "12" {
		t.Fatal("selected order filter ignored")
	}
	_, _ = db.Exec(ctx, "UPDATE users SET status=2 WHERE id=3")
	if code, _ := request("3", "GET", "/me", nil, ""); code != 403 {
		t.Fatal("disabled user accepted", code)
	}
	_, _ = db.Exec(ctx, "UPDATE users SET status=1,auth_version=2 WHERE id=3")
	if code, _ := request("3", "GET", "/me", nil, ""); code != 401 {
		t.Fatal("stale auth version accepted", code)
	}
	_, _ = db.Exec(ctx, "UPDATE users SET auth_version=1 WHERE id=3")
	// Deterministically hold the permission row until revocation commits.
	must("1", "PUT", "/admin/users/2/permission", map[string]any{"enabled": true, "expected_version": "3"}, uuid.NewString())
	_, _ = db.Exec(ctx, "INSERT INTO top_ups VALUES(15,2,'revoke-race',30,1,'alipay','epay',1700000000,0,'success')")
	revokeTx, e := db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer revokeTx.Rollback(ctx)
	if _, e = revokeTx.Exec(ctx, "SELECT user_id FROM invoice_user_permissions WHERE user_id=2 FOR UPDATE"); e != nil {
		t.Fatal(e)
	}
	pendingCreate := make(chan int, 1)
	go func() {
		code, _ := request("2", "POST", "/applications", map[string]any{"title_id": titleID, "topup_ids": []string{"15"}}, uuid.NewString())
		pendingCreate <- code
	}()
	deadline := time.Now().Add(3 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		_ = db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock' AND query LIKE '%invoice_user_permissions%')", roleName).Scan(&waiting)
		if waiting {
			break
		}
		timer := time.NewTimer(10 * time.Millisecond)
		<-timer.C
	}
	if !waiting {
		t.Fatal("self application never acquired permission wait")
	}
	if _, e = revokeTx.Exec(ctx, "UPDATE invoice_user_permissions SET enabled=false,version=version+1 WHERE user_id=2"); e != nil {
		t.Fatal(e)
	}
	if e = revokeTx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if code := <-pendingCreate; code != 403 {
		t.Fatal("create bypassed committed revocation", code)
	}
	// Complete versus cancel races on the application row, preserving occupation.
	racing := must("1", "POST", "/admin/applications", map[string]any{"user_id": "2", "title_id": titleID, "topup_ids": []string{"15"}}, uuid.NewString())["resource_id"].(string)
	terminal := make(chan int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		code, _ := request("2", "POST", "/applications/"+racing+"/cancel", map[string]any{}, uuid.NewString())
		terminal <- code
	}()
	go func() {
		defer wg.Done()
		r := httptest.NewRequest("POST", "http://localhost:3000/invoice-api/v1/admin/applications/"+racing+"/complete", bytes.NewReader(upload.Bytes()))
		r.Header.Set("Authorization", "Bearer "+token("1"))
		r.Header.Set("Origin", cfg.Origin)
		r.Header.Set("Idempotency-Key", uuid.NewString())
		r.Header.Set("Content-Type", multi.FormDataContentType())
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		terminal <- w.Code
	}()
	wg.Wait()
	close(terminal)
	win, lose := 0, 0
	for code := range terminal {
		if code == 200 {
			win++
		}
		if code == 409 {
			lose++
		}
	}
	if win != 1 || lose != 1 {
		t.Fatal("terminal transition race", win, lose)
	}
	var consistent bool
	if e = db.QueryRow(ctx, "SELECT i.active=(a.status='invoiced') FROM invoice_application_items i JOIN invoice_applications a ON a.id=i.application_id WHERE a.id=$1", racing).Scan(&consistent); e != nil || !consistent {
		t.Fatal("terminal occupation inconsistent", e)
	}

}
