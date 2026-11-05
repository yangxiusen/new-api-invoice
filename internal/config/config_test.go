package config

import "testing"

func TestConfigFailsClosed(t *testing.T) {
	env := map[string]string{"DATABASE_URL": "postgres://test:test@127.0.0.1/test", "NEW_API_INTERNAL_URL": "http://new-api:3000", "PUBLIC_ORIGIN": "http://localhost:3000", "PAYMENT_CURRENCY_RULES": "[{\"provider\":\"epay\",\"method\":\"alipay\",\"currency\":\"CNY\"}]"}
	get := func(s string) string { return env[s] }
	configured, err := Load(get)
	if err != nil || !configured.AutoMigrate {
		t.Fatal("automatic schema setup must default on", err)
	}
	env["AUTO_MIGRATE"] = "false"
	configured, err = Load(get)
	if err != nil || configured.AutoMigrate {
		t.Fatal("explicit migration mode", err)
	}
	delete(env, "AUTO_MIGRATE")
	if _, err := Load(get); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"DATABASE_URL", "NEW_API_INTERNAL_URL", "PUBLIC_ORIGIN", "PAYMENT_CURRENCY_RULES"} {
		old := env[key]
		env[key] = ""
		if _, err := Load(get); err == nil {
			t.Errorf("accepted missing %s", key)
		}
		env[key] = old
	}
	env["PUBLIC_ORIGIN"] = "https://example.com/invoice"
	if _, err := Load(get); err == nil {
		t.Fatal("accepted non-origin")
	}
}
