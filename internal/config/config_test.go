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
	t.Setenv("PROMVIEW_SESSION_COOKIE_SECURE", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionCookieSecure || cfg.OIDCGroupsClaim != "groups" {
		t.Fatalf("OIDC config = %#v", cfg)
	}
}

func TestLoadHonoursTheOldCookieSecureName(t *testing.T) {
	// An upgrade that quietly ignored the old name would turn Secure back on
	// for a loopback deployment that had deliberately turned it off, and the
	// only symptom is a sign-in that never sticks.
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_OIDC_COOKIE_SECURE", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionCookieSecure {
		t.Fatal("SessionCookieSecure = true, want the deprecated name to be honoured")
	}
}

func TestLoadPrefersTheCurrentCookieSecureName(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_OIDC_COOKIE_SECURE", "false")
	t.Setenv("PROMVIEW_SESSION_COOKIE_SECURE", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SessionCookieSecure {
		t.Fatal("SessionCookieSecure = false, want the current name to win")
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

func TestLoadRejectsUnknownAuthMode(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_AUTH_MODE", "kerberos")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func ldapEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_AUTH_MODE", "ldap")
	t.Setenv("PROMVIEW_LDAP_URL", "ldaps://dc.example.com:636")
	t.Setenv("PROMVIEW_LDAP_BASE_DN", "dc=example,dc=com")
	t.Setenv("PROMVIEW_LDAP_BIND_DN", "cn=svc,dc=example,dc=com")
	t.Setenv("PROMVIEW_LDAP_BIND_PASSWORD", "service-secret")
}

func TestLoadLDAPConfiguration(t *testing.T) {
	ldapEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LDAPUserFilter != "(uid=%s)" || cfg.LDAPGroupAttribute != "memberOf" || cfg.LDAPGroupFormat != "cn" {
		t.Fatalf("LDAP config = %#v", cfg)
	}
}

func TestLoadRequiresLDAPSettings(t *testing.T) {
	for _, missing := range []string{
		"PROMVIEW_LDAP_URL",
		"PROMVIEW_LDAP_BASE_DN",
		"PROMVIEW_LDAP_BIND_DN",
		// A blank service password is far more likely to be an unset variable
		// than a directory that allows an anonymous search.
		"PROMVIEW_LDAP_BIND_PASSWORD",
	} {
		t.Run(missing, func(t *testing.T) {
			ldapEnvironment(t)
			t.Setenv(missing, "")
			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted a configuration with %s unset", missing)
			}
		})
	}
}

// Search-then-bind sends the user's own password to the directory. Over
// cleartext that password is on the wire, which is the one outcome the whole
// flow exists to avoid.
func TestLoadRejectsCleartextLDAPToARemoteHost(t *testing.T) {
	for name, test := range map[string]struct {
		url      string
		startTLS bool
		wantErr  bool
	}{
		"ldaps":                   {url: "ldaps://dc.example.com:636"},
		"cleartext remote":        {url: "ldap://dc.example.com:389", wantErr: true},
		"cleartext with StartTLS": {url: "ldap://dc.example.com:389", startTLS: true},
		"cleartext loopback":      {url: "ldap://127.0.0.1:389"},
		"not a URL":               {url: "dc.example.com", wantErr: true},
		"wrong scheme":            {url: "https://dc.example.com", wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			ldapEnvironment(t)
			t.Setenv("PROMVIEW_LDAP_URL", test.url)
			if test.startTLS {
				t.Setenv("PROMVIEW_LDAP_START_TLS", "true")
			}
			_, err := Load()
			if (err != nil) != test.wantErr {
				t.Fatalf("Load() error = %v, wantErr = %v", err, test.wantErr)
			}
		})
	}
}

func TestLoadRejectsUnusableLDAPSettings(t *testing.T) {
	for name, setting := range map[string][2]string{
		"filter without a placeholder": {"PROMVIEW_LDAP_USER_FILTER", "(uid=fixed)"},
		"unknown group format":         {"PROMVIEW_LDAP_GROUP_FORMAT", "uuid"},
		"issuer that is not a URL":     {"PROMVIEW_LDAP_ISSUER", "dc.example.com"},
		"issuer with an HTTP scheme":   {"PROMVIEW_LDAP_ISSUER", "https://identity.example.com"},
	} {
		t.Run(name, func(t *testing.T) {
			ldapEnvironment(t)
			t.Setenv(setting[0], setting[1])
			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted %s=%q", setting[0], setting[1])
			}
		})
	}
}

func TestLoadAcceptsLocalMode(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_AUTH_MODE", "local")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SessionCookieSecure {
		t.Fatal("SessionCookieSecure = false by default")
	}
}

// An insecure cookie on a published listener is a sign-in that appears to work
// and then does not stick, which reads as a broken console rather than as the
// misconfiguration it is.
func TestLoadRejectsAnInsecureCookieOnAPublishedListener(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_AUTH_MODE", "local")
	t.Setenv("PROMVIEW_SESSION_COOKIE_SECURE", "false")
	for _, listen := range []string{":8080", "0.0.0.0:8080", "promview.example.com:8080"} {
		t.Setenv("PROMVIEW_LISTEN_ADDRESS", listen)
		if _, err := Load(); err == nil {
			t.Fatalf("listening on %q with an insecure cookie was accepted", listen)
		}
	}
	for _, listen := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080"} {
		t.Setenv("PROMVIEW_LISTEN_ADDRESS", listen)
		if _, err := Load(); err != nil {
			t.Fatalf("listening on %q with an insecure cookie was refused: %v", listen, err)
		}
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

func TestLoadOpenModeRole(t *testing.T) {
	for role, wantErr := range map[string]bool{
		"viewer": false, "operator": false, "administrator": false,
		"superuser": true, "Operator": true,
	} {
		t.Run(role, func(t *testing.T) {
			t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
			t.Setenv("PROMVIEW_OPEN_MODE_ROLE", role)
			cfg, err := Load()
			if (err != nil) != wantErr {
				t.Fatalf("Load() error = %v, wantErr = %v", err, wantErr)
			}
			if err == nil && cfg.OpenModeRole != role {
				t.Fatalf("OpenModeRole = %q, want %q", cfg.OpenModeRole, role)
			}
		})
	}
}

func TestLoadDefaultsOpenModeToViewer(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// The default has to be the safe one: a deployment that did not ask for
	// elevation must not have it.
	if cfg.OpenModeRole != "viewer" || cfg.OpenModeAuthor == "" {
		t.Fatalf("open mode defaults = %q / %q", cfg.OpenModeRole, cfg.OpenModeAuthor)
	}
}

// Silently ignoring these outside open mode would leave somebody believing they
// had widened or restricted access when they had not.
func TestLoadRejectsOpenModeSettingsInOtherModes(t *testing.T) {
	for _, setting := range []string{"PROMVIEW_OPEN_MODE_ROLE", "PROMVIEW_OPEN_MODE_AUTHOR"} {
		t.Run(setting, func(t *testing.T) {
			t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
			t.Setenv("PROMVIEW_AUTH_MODE", "local")
			t.Setenv(setting, "operator")
			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted %s outside open mode", setting)
			}
		})
	}
}

// Alertmanager refuses an unnamed silence, so an elevated open mode with a
// blank author would be missing the thing it was turned on for.
func TestLoadRejectsABlankOpenModeAuthor(t *testing.T) {
	t.Setenv("PROMVIEW_DATABASE_URL", "postgres://example")
	t.Setenv("PROMVIEW_OPEN_MODE_AUTHOR", "   ")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a blank author")
	}
}
