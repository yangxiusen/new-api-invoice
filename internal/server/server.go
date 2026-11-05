package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"invoice/internal/auth"
	"invoice/internal/billing"
	"invoice/internal/config"
	"invoice/internal/fault"
	"invoice/internal/files"
	"invoice/internal/store"
)

type endpoint func(*gin.Context) (any, error)

func reply(c *gin.Context, data any, e error) {
	c.Header("Cache-Control", "no-store")
	status := 200
	code, msg := "OK", ""
	if e != nil {
		var f *fault.Error
		if errors.As(e, &f) {
			status, code, msg = f.Status, f.Code, f.Message
		} else {
			status, code, msg = 500, "INTERNAL_ERROR", "服务暂时无法完成请求"
		}
		data = nil
		slog.Warn("request failed", "request_id", c.GetString("request_id"), "code", code)
		if status == 429 {
			c.Header("Retry-After", "5")
		}
	}
	c.JSON(status, gin.H{"success": e == nil, "code": code, "message": msg, "data": data, "request_id": c.GetString("request_id")})
}
func wrap(fn endpoint) gin.HandlerFunc {
	return func(c *gin.Context) { data, e := fn(c); reply(c, data, e) }
}
func actor(c *gin.Context) billing.Actor { return c.MustGet("actor").(billing.Actor) }
func write(c *gin.Context) billing.Write {
	return billing.Write{Actor: actor(c), Key: c.GetHeader("Idempotency-Key"), Operation: c.Request.Method + " " + c.Request.URL.Path, RequestID: c.GetString("request_id"), Admin: strings.HasPrefix(c.FullPath(), "/invoice-api/v1/admin/")}
}
func pathID(c *gin.Context) (int64, error) { return billing.ID(c.Param("id")) }
func bad() error                           { return fault.New(400, "INVALID_ARGUMENT", "请求参数或 JSON 格式不正确") }
func filter(c *gin.Context, allowed ...string) (billing.Filter, error) {
	f := billing.Filter{Page: 1, Size: 20}
	known := map[string]bool{"page": true, "page_size": true}
	for _, s := range allowed {
		known[s] = true
	}
	for k, v := range c.Request.URL.Query() {
		if !known[k] || len(v) != 1 {
			return f, bad()
		}
	}
	for _, pair := range []struct {
		key string
		v   *int
		max int
	}{{"page", &f.Page, 1000000}, {"page_size", &f.Size, 100}} {
		if raw := c.Query(pair.key); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 || n > pair.max {
				return f, bad()
			}
			*pair.v = n
		}
	}
	var e error
	f.Keyword, e = billing.Text(c.Query("keyword"), 128, false)
	if e != nil {
		return f, e
	}
	if raw := c.Query("topup_ids"); raw != "" {
		parts := strings.Split(raw, ",")
		if len(parts) > 100 {
			return f, bad()
		}
		for _, part := range parts {
			id, e := billing.ID(part)
			if e != nil {
				return f, e
			}
			f.Topups = append(f.Topups, id)
		}
	}
	f.Status = c.Query("status")
	if f.Status != "" && f.Status != "pending" && f.Status != "invoiced" && f.Status != "rejected" && f.Status != "cancelled" {
		return f, bad()
	}
	f.UserID = c.Query("user_id")
	f.ActorID = c.Query("actor_user_id")
	for _, raw := range []string{f.UserID, f.ActorID} {
		if raw != "" {
			if _, e = billing.ID(raw); e != nil {
				return f, e
			}
		}
	}
	f.TargetID = c.Query("target_user_id")
	if f.TargetID != "" {
		if _, e = billing.ID(f.TargetID); e != nil {
			return f, e
		}
	}
	f.Enabled = c.Query("invoice_enabled")
	if f.Enabled != "" && f.Enabled != "true" && f.Enabled != "false" {
		return f, bad()
	}
	f.Account = c.Query("account_status")
	if f.Account != "" && f.Account != "enabled" && f.Account != "disabled" {
		return f, bad()
	}
	f.Operation = c.Query("operation_type")
	if f.Operation != "" && !billing.Operations[f.Operation] {
		return f, bad()
	}
	return f, nil
}
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 32 {
		return bad()
	}
	tok, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			key, ok := k.(string)
			if !ok || seen[key] {
				return bad()
			}
			seen[key] = true
			if e = uniqueJSON(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = uniqueJSON(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return bad()
	}
	_, e = d.Token()
	return e
}
func body(c *gin.Context, dst any) error {
	media, _, e := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if e != nil || media != "application/json" {
		return fault.New(415, "UNSUPPORTED_MEDIA_TYPE", "请使用 application/json")
	}
	raw, e := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 65536))
	if e != nil {
		return fault.New(413, "FILE_TOO_LARGE", "请求内容过大")
	}
	if !utf8.Valid(raw) || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return bad()
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if e = uniqueJSON(dec, 0); e != nil {
		return bad()
	}
	if _, e = dec.Token(); !errors.Is(e, io.EOF) {
		return bad()
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e = dec.Decode(dst); e != nil {
		return bad()
	}
	return nil
}

type Handler struct {
	http.Handler
	Files *files.Store
}

func (h *Handler) Close() error { return h.Files.Close() }
func New(db *pgxpool.Pool, cfg config.Config, assets fs.FS) (*Handler, error) {
	fileStore, e := files.New(cfg.FileRoot)
	if e != nil {
		return nil, e
	}
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(func(c *gin.Context) {
		c.Set("request_id", uuid.NewString())
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "same-origin")
		c.Next()
	})
	r.Use(gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		reply(c, nil, fault.New(500, "INTERNAL_ERROR", "服务暂时无法完成请求"))
		c.Abort()
	}))
	svc := &billing.Service{DB: db, Config: cfg}
	host := auth.NewClient(cfg.HostURL, cfg.AuthTimeout)
	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if store.Ready(ctx, db) != nil || fileStore.Writable() != nil {
			c.JSON(503, gin.H{"status": "not_ready"})
			return
		}
		c.JSON(200, gin.H{"status": "ready"})
	})
	api := r.Group("/invoice-api/v1")
	// Bounded in-memory admission control; no caller-controlled forwarding headers.
	var mu sync.Mutex
	type bucket struct {
		start time.Time
		count int
	}
	buckets := map[string]bucket{}
	allow := func(key string) bool {
		now := time.Now()
		mu.Lock()
		b := buckets[key]
		if now.Sub(b.start) > time.Minute {
			b = bucket{start: now}
		}
		b.count++
		denied := b.count > 240
		if len(buckets) >= 10000 {
			for k, v := range buckets {
				if now.Sub(v.start) > time.Minute {
					delete(buckets, k)
				}
			}
			if _, exists := buckets[key]; !exists && len(buckets) >= 10000 {
				denied = true
			}
		}
		if _, exists := buckets[key]; exists || len(buckets) < 10000 {
			buckets[key] = b
		}
		mu.Unlock()
		return !denied
	}
	authSlots := make(chan struct{}, 32)
	api.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if e := store.Ready(c.Request.Context(), db); e != nil {
			reply(c, nil, fault.New(503, "NOT_READY", "发票服务尚未就绪"))
			c.Abort()
			return
		}
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			if !allow("anonymous:" + c.ClientIP()) {
				reply(c, nil, fault.New(429, "RATE_LIMITED", "请求频繁，请稍后重试"))
				c.Abort()
				return
			}
			reply(c, nil, fault.New(401, "AUTH_REQUIRED", "请先登录原站"))
			c.Abort()
			return
		}
		raw := strings.TrimPrefix(header, "Bearer ")
		claims, e := auth.Classify(raw)
		if e != nil {
			if !allow("anonymous:" + c.ClientIP()) {
				reply(c, nil, fault.New(429, "RATE_LIMITED", "请求频繁，请稍后重试"))
				c.Abort()
				return
			}
			reply(c, nil, e)
			c.Abort()
			return
		}
		select {
		case authSlots <- struct{}{}:
		default:
			reply(c, nil, fault.New(429, "RATE_LIMITED", "身份校验繁忙，请稍后重试"))
			c.Abort()
			return
		}
		user, e := host.Verify(c.Request.Context(), raw)
		<-authSlots
		if e != nil {
			reply(c, nil, e)
			c.Abort()
			return
		}
		if strconv.FormatInt(user.ID, 10) != claims.Sub {
			reply(c, nil, fault.New(401, "AUTH_REQUIRED", "身份不一致，请重新登录"))
			c.Abort()
			return
		}
		a := billing.Actor{}
		var deleted *time.Time
		e = db.QueryRow(c.Request.Context(), `SELECT id,coalesce(username,''),coalesce(display_name,''),role,status,auth_version,deleted_at FROM users WHERE id=$1`, user.ID).Scan(&a.ID, &a.Username, &a.Name, &a.Role, &a.Status, &a.AuthVersion, &deleted)
		if e != nil {
			reply(c, nil, fault.New(503, "AUTH_SERVICE_UNAVAILABLE", "身份数据暂不可用"))
			c.Abort()
			return
		}
		if a.AuthVersion != claims.UV {
			reply(c, nil, fault.New(401, "AUTH_REQUIRED", "登录状态已更新"))
			c.Abort()
			return
		}
		if deleted != nil || a.Status != 1 || (a.Role != 1 && a.Role != 10 && a.Role != 100) {
			reply(c, nil, fault.New(403, "ACCOUNT_UNAVAILABLE", "账号不可用"))
			c.Abort()
			return
		}
		if !allow("actor:" + strconv.FormatInt(a.ID, 10)) {
			reply(c, nil, fault.New(429, "RATE_LIMITED", "请求频繁，请稍后重试"))
			c.Abort()
			return
		}
		c.Set("actor", a)
		if c.Request.Method != "GET" {
			if cfg.ReadOnly {
				reply(c, nil, fault.New(503, "WRITE_TEMPORARY_FAILURE", "维护期间暂不接受变更"))
				c.Abort()
				return
			}
			origin := c.GetHeader("Origin")
			if origin == "" {
				u, e := url.Parse(c.GetHeader("Referer"))
				if e == nil && u.Host != "" {
					origin = u.Scheme + "://" + u.Host
				}
			}
			if origin != cfg.Origin || c.GetHeader("Sec-Fetch-Site") == "cross-site" {
				reply(c, nil, fault.New(403, "ORIGIN_FORBIDDEN", "请求来源不允许"))
				c.Abort()
				return
			}
			if len(c.Request.URL.Query()) > 0 {
				reply(c, nil, bad())
				c.Abort()
				return
			}
		}
		c.Next()
	})
	api.GET("/me", wrap(func(c *gin.Context) (any, error) {
		if _, e := filter(c); e != nil {
			return nil, e
		}
		return svc.Me(c.Request.Context(), actor(c))
	}))
	api.GET("/write-requests/:key", wrap(func(c *gin.Context) (any, error) {
		if _, e := filter(c); e != nil {
			return nil, e
		}
		return svc.WriteResult(c.Request.Context(), actor(c), c.Param("key"))
	}))
	registerTitles(api, svc, false)
	registerApplications(api, svc, false)
	admin := api.Group("/admin", func(c *gin.Context) {
		if !actor(c).Admin() {
			reply(c, nil, fault.New(403, "ADMIN_REQUIRED", "仅管理员可访问"))
			c.Abort()
			return
		}
		c.Next()
	})
	registerTitles(admin, svc, true)
	registerApplications(admin, svc, true)
	registerFiles(api, admin, svc, fileStore)
	admin.GET("/users", wrap(func(c *gin.Context) (any, error) {
		f, e := filter(c, "keyword", "user_id", "invoice_enabled", "account_status")
		if e != nil {
			return nil, e
		}
		return svc.Users(c.Request.Context(), f)
	}))
	admin.PUT("/users/:id/permission", wrap(func(c *gin.Context) (any, error) {
		id, e := pathID(c)
		if e != nil {
			return nil, e
		}
		var in billing.PermissionInput
		if e = body(c, &in); e != nil {
			return nil, e
		}
		return svc.ChangePermission(c.Request.Context(), write(c), id, in)
	}))
	admin.GET("/operations", wrap(func(c *gin.Context) (any, error) {
		f, e := filter(c, "target_user_id", "actor_user_id", "operation_type", "keyword")
		if e != nil {
			return nil, e
		}
		return svc.Operations(c.Request.Context(), f)
	}))
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if !strings.HasPrefix(p, "/invoice/") || c.Request.Method != "GET" || assets == nil {
			reply(c, nil, billing.NotFound())
			return
		}
		name := strings.TrimPrefix(p, "/invoice/")
		if name == "" {
			name = "index.html"
		}
		raw, e := fs.ReadFile(assets, name)
		if e != nil {
			if path.Ext(name) != "" || strings.HasPrefix(name, "static/") {
				reply(c, nil, billing.NotFound())
				return
			}
			raw, e = fs.ReadFile(assets, "index.html")
		}
		if e != nil {
			reply(c, nil, billing.NotFound())
			return
		}
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		typ := mime.TypeByExtension(path.Ext(name))
		if typ == "" {
			typ = "text/html; charset=utf-8"
		}
		c.Data(200, typ, raw)
	})
	return &Handler{Handler: r, Files: fileStore}, nil
}
