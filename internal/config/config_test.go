package config

import "testing"

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
