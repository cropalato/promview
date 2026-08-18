package config

import (
	"testing"
	"time"
)

func TestLoadBootstrapSource(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_BOOTSTRAP_SOURCE_SLUG", "primary")
	t.Setenv("PROMVIEW_BOOTSTRAP_SOURCE_TOKEN", "0123456789abcdef")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BootstrapSourceName != "primary" {
		t.Fatalf("bootstrap name = %q, want primary", cfg.BootstrapSourceName)
	}
}

func TestLoadRejectsPartialBootstrapSource(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_BOOTSTRAP_SOURCE_SLUG", "primary")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadOIDCConfiguration(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_AUTH_MODE", "oidc")
	t.Setenv("PROMVIEW_OIDC_ISSUER_URL", "http://localhost:9000")
	t.Setenv("PROMVIEW_OIDC_CLIENT_ID", "promview")
	t.Setenv("PROMVIEW_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("PROMVIEW_OIDC_REDIRECT_URL", "http://localhost:8080/api/v1/auth/oidc/callback")
	t.Setenv("PROMVIEW_OIDC_COOKIE_SECURE", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OIDCCookieSecure || cfg.OIDCGroupsClaim != "groups" {
		t.Fatalf("OIDC config = %#v", cfg)
	}
}

func TestLoadRejectsIncompleteOIDCConfiguration(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_AUTH_MODE", "oidc")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadRejectsInsecureRemoteOIDCConfiguration(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_AUTH_MODE", "oidc")
	t.Setenv("PROMVIEW_OIDC_ISSUER_URL", "http://identity.example.com")
	t.Setenv("PROMVIEW_OIDC_CLIENT_ID", "promview")
	t.Setenv("PROMVIEW_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("PROMVIEW_OIDC_REDIRECT_URL", "https://promview.example.com/api/v1/auth/oidc/callback")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadRejectsLDAPMode(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_AUTH_MODE", "ldap")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadAlertExpiryDefaults(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// The default has to stay above Alertmanager's own 4h repeat_interval
	// default, otherwise a live alert expires between repeat notifications and
	// flaps back on the next one.
	if cfg.AlertStaleAfter != 12*time.Hour {
		t.Fatalf("stale after = %v, want 12h", cfg.AlertStaleAfter)
	}
	if cfg.AlertExpiryInterval != time.Minute {
		t.Fatalf("expiry interval = %v, want 1m", cfg.AlertExpiryInterval)
	}
}

func TestLoadAlertExpiryOverrides(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_ALERT_STALE_AFTER", "0")
	t.Setenv("PROMVIEW_ALERT_EXPIRY_INTERVAL", "30s")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AlertStaleAfter != 0 {
		t.Fatalf("stale after = %v, want 0 (expiry disabled)", cfg.AlertStaleAfter)
	}
	if cfg.AlertExpiryInterval != 30*time.Second {
		t.Fatalf("expiry interval = %v, want 30s", cfg.AlertExpiryInterval)
	}
}

func TestLoadRejectsInvalidAlertExpiry(t *testing.T) {
	for _, test := range []struct{ key, value string }{
		{key: "PROMVIEW_ALERT_STALE_AFTER", value: "-1h"},
		{key: "PROMVIEW_ALERT_STALE_AFTER", value: "soon"},
		{key: "PROMVIEW_ALERT_EXPIRY_INTERVAL", value: "0"},
		{key: "PROMVIEW_ALERT_EXPIRY_INTERVAL", value: "-1m"},
	} {
		t.Run(test.key+"="+test.value, func(t *testing.T) {
			t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load() error = nil, want error for %s=%s", test.key, test.value)
			}
		})
	}
}
