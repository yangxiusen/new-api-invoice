package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"invoice/internal/fault"
)

type Claims struct {
	Issuer   string          `json:"iss"`
	Audience json.RawMessage `json:"aud"`
	Use      string          `json:"token_use"`
	Sub      string          `json:"sub"`
	SID      string          `json:"sid"`
	UV       int64           `json:"uv"`
	SV       int64           `json:"sv"`
	Exp      int64           `json:"exp"`
	Nbf      int64           `json:"nbf"`
}

// Classify restricts credential class only. These unverified claims NEVER grant identity.
func Classify(raw string) (Claims, error) {
	var c Claims
	bad := fault.New(401, "CREDENTIAL_UNSUPPORTED", "请使用原站面板登录")
	if len(raw) > 8192 {
		return c, bad
	}
	p := strings.Split(raw, ".")
	if len(p) != 3 {
		return c, bad
	}
	h, e := base64.RawURLEncoding.DecodeString(p[0])
	if e != nil {
		return c, bad
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(h, &header) != nil || header.Alg != "HS256" {
		return c, bad
	}
	b, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil || json.Unmarshal(b, &c) != nil {
		return c, bad
	}
	var aud []string
	if json.Unmarshal(c.Audience, &aud) != nil {
		var s string
		if json.Unmarshal(c.Audience, &s) != nil {
			return c, bad
		}
		aud = []string{s}
	}
	found := false
	for _, a := range aud {
		if a == "new-api-dashboard" {
			found = true
		}
	}
	id, e := strconv.ParseInt(c.Sub, 10, 64)
	if !found || c.Issuer != "new-api" || c.Use != "access" || e != nil || id <= 0 || c.SID == "" || len(c.SID) > 255 || c.UV < 1 || c.SV < 1 {
		return c, bad
	}
	if c.Exp <= time.Now().Unix() || c.Nbf > time.Now().Add(5*time.Second).Unix() {
		return c, fault.New(401, "AUTH_REQUIRED", "登录已过期，请重新登录")
	}
	return c, nil
}

type HostUser struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        int    `json:"role"`
	Status      int    `json:"status"`
}
type Client struct {
	base string
	http *http.Client
}

func NewClient(base string, timeout time.Duration) *Client {
	return &Client{base, &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Verify(ctx context.Context, raw string) (HostUser, error) {
	var user HostUser
	if _, e := Classify(raw); e != nil {
		return user, e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/user/self", nil)
	if e != nil {
		return user, e
	}
	req.Header.Set("Authorization", "Bearer "+raw)
	req.Header.Set("Accept", "application/json")
	res, e := c.http.Do(req)
	unavailable := fault.New(503, "AUTH_SERVICE_UNAVAILABLE", "原站身份服务暂不可用")
	if e != nil {
		return user, unavailable
	}
	defer res.Body.Close()
	if res.StatusCode == 401 {
		return user, fault.New(401, "AUTH_REQUIRED", "请重新登录原站")
	}
	if res.StatusCode == 429 {
		return user, fault.New(429, "RATE_LIMITED", "请求频繁，请稍后重试")
	}
	if res.StatusCode != 200 {
		return user, unavailable
	}
	rawBody, e := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if e != nil {
		return user, unavailable
	}
	var body struct {
		Success bool      `json:"success"`
		Data    *HostUser `json:"data"`
	}
	if json.Unmarshal(rawBody, &body) != nil || !body.Success || body.Data == nil || body.Data.ID <= 0 || body.Data.Username == "" || body.Data.Status == 0 || body.Data.Role == 0 {
		return user, unavailable
	}
	return *body.Data, nil
}
