package store

import (
	"net/url"
	"testing"
)

func isolatedDSN(t *testing.T, dsn string) {
	t.Helper()
	u, e := url.Parse(dsn)
	if e != nil || u.Path != "/invoice_test" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "host.docker.internal") {
		t.Fatal("test refuses a non-isolated database: use loopback invoice_test only")
	}
}
