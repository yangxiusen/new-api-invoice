package config

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

type CurrencyRule struct {
	Provider string `json:"provider"`
	Method   string `json:"method"`
	Currency string `json:"currency"`
}
type Config struct {
	AutoMigrate                                    bool
	MigrationURL                                   string
	DatabaseURL, HostURL, Origin, FileRoot, Listen string
	AuthTimeout                                    time.Duration
	ReadOnly                                       bool
	CurrencyRules                                  []CurrencyRule
}

func Load(get func(string) string) (Config, error) {
	c := Config{DatabaseURL: get("DATABASE_URL"), HostURL: get("NEW_API_INTERNAL_URL"), Origin: get("PUBLIC_ORIGIN"), FileRoot: get("INVOICE_FILE_ROOT"), Listen: get("LISTEN_ADDR"), AuthTimeout: 3 * time.Second}
	c.AutoMigrate = true
	c.MigrationURL = get("MIGRATION_DATABASE_URL")
	switch get("AUTO_MIGRATE") {
	case "", "true":
	case "false":
		c.AutoMigrate = false
	default:
		return c, errors.New("invalid AUTO_MIGRATE")
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL required")
	}
	for name, raw := range map[string]string{"NEW_API_INTERNAL_URL": c.HostURL, "PUBLIC_ORIGIN": c.Origin} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return c, errors.New(name + " must be an HTTP origin")
		}
		if name == "PUBLIC_ORIGIN" && u.Scheme != "https" && u.Hostname() != "localhost" && !net.ParseIP(u.Hostname()).IsLoopback() {
			return c, errors.New("PUBLIC_ORIGIN requires HTTPS except loopback")
		}
	}
	c.Origin = strings.TrimSuffix(c.Origin, "/")
	c.HostURL = strings.TrimSuffix(c.HostURL, "/")
	if c.FileRoot == "" {
		c.FileRoot = "/data/invoices"
	}
	if c.Listen == "" {
		c.Listen = ":3001"
	}
	if raw := get("AUTH_TIMEOUT"); raw != "" {
		d, e := time.ParseDuration(raw)
		if e != nil || d < 100*time.Millisecond || d > 30*time.Second {
			return c, errors.New("invalid AUTH_TIMEOUT")
		}
		c.AuthTimeout = d
	}
	switch get("READ_ONLY_MODE") {
	case "true":
		c.ReadOnly = true
	case "", "false":
	default:
		return c, errors.New("invalid READ_ONLY_MODE")
	}
	dec := json.NewDecoder(strings.NewReader(get("PAYMENT_CURRENCY_RULES")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c.CurrencyRules); err != nil || len(c.CurrencyRules) == 0 {
		return c, errors.New("PAYMENT_CURRENCY_RULES requires nonempty JSON array")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return c, errors.New("trailing currency rules content")
	}
	seen := map[string]bool{}
	for _, r := range c.CurrencyRules {
		key := r.Provider + "\x00" + r.Method
		if r.Provider == "" || r.Method == "" || len(r.Currency) != 3 || seen[key] {
			return c, errors.New("invalid/duplicate currency rule")
		}
		seen[key] = true
	}
	return c, nil
}

func (c Config) Currency(provider, method string) string {
	for _, r := range c.CurrencyRules {
		if r.Provider == provider && r.Method == method {
			return r.Currency
		}
	}
	return ""
}
