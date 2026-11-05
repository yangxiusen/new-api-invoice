package server

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"

	"invoice/internal/config"
)

func TestStaticHistoryDoesNotSwallowMissingAssetsOrAPI(t *testing.T) {
	h, e := New(nil, config.Config{FileRoot: t.TempDir()}, fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Invoice</title>")}, "static/js/app.js": {Data: []byte("const ok=true;")}})
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	apiCount := 0
	for _, route := range h.Handler.(*gin.Engine).Routes() {
		if strings.HasPrefix(route.Path, "/invoice-api/v1/") {
			apiCount++
		}
	}
	if apiCount != 24 {
		t.Fatal("business route count", apiCount)
	}
	for _, c := range []struct {
		path   string
		status int
		mime   string
	}{{"/invoice/orders", 200, "text/html; charset=utf-8"}, {"/invoice/admin/users", 200, "text/html; charset=utf-8"}, {"/invoice/static/js/app.js", 200, "text/javascript; charset=utf-8"}, {"/invoice/missing.js", 404, "application/json; charset=utf-8"}, {"/invoice-api/unknown", 404, "application/json; charset=utf-8"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", c.path, nil))
		if w.Code != c.status {
			t.Errorf("%s status %d", c.path, w.Code)
		}
		if c.path != "/invoice/static/js/app.js" && w.Header().Get("Content-Type") != c.mime {
			t.Errorf("%s MIME %s", c.path, w.Header().Get("Content-Type"))
		}
	}
}
