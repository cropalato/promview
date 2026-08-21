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

func TestLoadSilenceWindowDefaults(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// Two hours is the documented default; the console reads it from the config
	// endpoint rather than hardcoding its own.
	if cfg.SilenceDefaultDuration != 2*time.Hour {
		t.Errorf("default silence = %s, want 2h", cfg.SilenceDefaultDuration)
	}
	if cfg.SilenceMaxDuration != 30*24*time.Hour {
		t.Errorf("max silence = %s, want 720h", cfg.SilenceMaxDuration)
	}
}

func TestLoadSilenceWindowOverrides(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_SILENCE_DEFAULT_DURATION", "45m")
	t.Setenv("PROMVIEW_SILENCE_MAX_DURATION", "8h")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SilenceDefaultDuration != 45*time.Minute {
		t.Errorf("default silence = %s, want 45m", cfg.SilenceDefaultDuration)
	}
	if cfg.SilenceMaxDuration != 8*time.Hour {
		t.Errorf("max silence = %s, want 8h", cfg.SilenceMaxDuration)
	}
}

func TestLoadRejectsUnusableSilenceWindows(t *testing.T) {
	for _, test := range []struct{ name, key, value string }{
		{"zero default", "PROMVIEW_SILENCE_DEFAULT_DURATION", "0"},
		{"negative default", "PROMVIEW_SILENCE_DEFAULT_DURATION", "-1h"},
		{"unparseable default", "PROMVIEW_SILENCE_DEFAULT_DURATION", "soon"},
		{"zero maximum", "PROMVIEW_SILENCE_MAX_DURATION", "0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want error")
			}
		})
	}
}

func TestLoadRejectsADefaultSilencePastTheMaximum(t *testing.T) {
	// A default the server would refuse on every request is a deployment that
	// can never silence anything.
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_SILENCE_DEFAULT_DURATION", "12h")
	t.Setenv("PROMVIEW_SILENCE_MAX_DURATION", "1h")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}
