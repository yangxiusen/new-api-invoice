package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testToken(use string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"iss":"new-api","aud":["new-api-dashboard"],"token_use":%q,"sub":"2","sid":"test-session","uv":1,"sv":1,"exp":%d}`, use, time.Now().Add(time.Minute).Unix()))) + ".signature"
}

func TestClassAndHostValidation(t *testing.T) {
	for _, s := range []string{"sk-model", "opaque", testToken("security_proof")} {
		if _, e := Classify(s); e == nil {
			t.Fatal("accepted unsupported token")
		}
	}
	token := testToken("access")
	if _, e := Classify(token); e != nil {
		t.Fatal(e)
	}
	status := 200
	body := `{"success":true,"data":{"id":2,"username":"user","role":1,"status":1}}`
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("wrong forwarding")
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer host.Close()
	client := NewClient(host.URL, time.Second)
	if _, e := client.Verify(context.Background(), token); e != nil {
		t.Fatal(e)
	}
	body = `{"success":false}`
	if _, e := client.Verify(context.Background(), token); e == nil {
		t.Fatal("accepted ambiguous failure")
	}
	status = 302
	if _, e := client.Verify(context.Background(), token); e == nil {
		t.Fatal("followed redirect")
	}
}
