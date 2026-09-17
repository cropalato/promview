package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddress string
	// MetricsAddress is a listener of its own, never a route on the public one.
	// The labels name sources and teams, and promview is commonly reachable
	// from the internet; a port the ingress does not publish cannot leak them
	// by being forgotten. Empty disables the endpoint.
	MetricsAddress       string
	DatabaseURL          string
	AuthMode             string
	WebDirectory         string
	MigrationsDir        string
	BootstrapSourceSlug  string
	BootstrapSourceName  string
	BootstrapSourceToken string
	OIDCIssuerURL        string
	OIDCClientID         string
	OIDCClientSecret     string
	OIDCRedirectURL      string
	OIDCScopes           []string
	OIDCUsernameClaim    string
	OIDCEmailClaim       string
	OIDCDisplayNameClaim string
	OIDCGroupsClaim      string
	LDAPURL              string
	LDAPIssuer           string
	LDAPBindDN           string
	LDAPBindPassword     string
	LDAPBaseDN           string
	LDAPUserFilter       string
	LDAPGroupAttribute   string
	LDAPGroupFormat      string
	LDAPUsernameAttr     string
	LDAPEmailAttr        string
	LDAPDisplayNameAttr  string
	LDAPStartTLS         bool
	LDAPTimeout          time.Duration
	// SessionCookieSecure marks the session cookie Secure. It is not an OIDC
	// setting: every mode that issues a session writes the same cookie, and a
	// deployment that had to set one flag per mode would eventually set one and
	// not the other.
	SessionCookieSecure bool
	// AlertStaleAfter is the default window an alert may go unreported before it
	// expires, used for sources that do not set their own. Zero disables expiry.
	AlertStaleAfter time.Duration
	// AlertExpiryInterval is how often the expiry sweep runs.
	AlertExpiryInterval time.Duration
	// ReconcileInterval is how often each source's Alertmanager is read to
	// confirm what is still firing. Zero disables reconciliation.
	ReconcileInterval time.Duration
	// ReconcileTimeout bounds one Alertmanager request.
	ReconcileTimeout time.Duration
	// SilenceDefaultDuration is how long a silence lasts when the operator does
	// not say. It is a deployment choice: the right length is however long the
	// team's usual maintenance window runs.
	SilenceDefaultDuration time.Duration
	// StreamRetention is how long a stream event is kept so a disconnected
	// client can resume through it. Zero disables pruning, which is the escape
	// hatch for a deployment that would rather grow the table than ever ask a
	// client to re-snapshot.
	StreamRetention time.Duration
	// SilenceMaxDuration bounds what an operator may ask for. A silence is the
	// one action here that hides alerts rather than surfacing them, and an
	// unbounded one is indistinguishable from deleting the rule.
	SilenceMaxDuration time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress:        envOrDefault("PROMVIEW_LISTEN_ADDRESS", ":8080"),
		MetricsAddress:       envOrDefault("PROMVIEW_METRICS_ADDRESS", ":9090"),
		DatabaseURL:          os.Getenv("PROMVIEW_DATABASE_URL"),
		AuthMode:             envOrDefault("PROMVIEW_AUTH_MODE", "open"),
		WebDirectory:         envOrDefault("PROMVIEW_WEB_DIRECTORY", "web/dist"),
		MigrationsDir:        envOrDefault("PROMVIEW_MIGRATIONS_DIRECTORY", "migrations"),
		BootstrapSourceSlug:  os.Getenv("PROMVIEW_BOOTSTRAP_SOURCE_SLUG"),
		BootstrapSourceName:  os.Getenv("PROMVIEW_BOOTSTRAP_SOURCE_NAME"),
		BootstrapSourceToken: os.Getenv("PROMVIEW_BOOTSTRAP_SOURCE_TOKEN"),
		OIDCIssuerURL:        os.Getenv("PROMVIEW_OIDC_ISSUER_URL"),
		OIDCClientID:         os.Getenv("PROMVIEW_OIDC_CLIENT_ID"),
		OIDCClientSecret:     os.Getenv("PROMVIEW_OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:      os.Getenv("PROMVIEW_OIDC_REDIRECT_URL"),
		OIDCScopes:           splitCSV(envOrDefault("PROMVIEW_OIDC_SCOPES", "openid,profile,email,groups")),
		OIDCUsernameClaim:    envOrDefault("PROMVIEW_OIDC_USERNAME_CLAIM", "preferred_username"),
		OIDCEmailClaim:       envOrDefault("PROMVIEW_OIDC_EMAIL_CLAIM", "email"),
		OIDCDisplayNameClaim: envOrDefault("PROMVIEW_OIDC_DISPLAY_NAME_CLAIM", "name"),
		OIDCGroupsClaim:      envOrDefault("PROMVIEW_OIDC_GROUPS_CLAIM", "groups"),
		LDAPURL:              os.Getenv("PROMVIEW_LDAP_URL"),
		LDAPIssuer:           os.Getenv("PROMVIEW_LDAP_ISSUER"),
		LDAPBindDN:           os.Getenv("PROMVIEW_LDAP_BIND_DN"),
		LDAPBindPassword:     os.Getenv("PROMVIEW_LDAP_BIND_PASSWORD"),
		LDAPBaseDN:           os.Getenv("PROMVIEW_LDAP_BASE_DN"),
		LDAPUserFilter:       envOrDefault("PROMVIEW_LDAP_USER_FILTER", "(uid=%s)"),
		LDAPGroupAttribute:   envOrDefault("PROMVIEW_LDAP_GROUP_ATTRIBUTE", "memberOf"),
		LDAPGroupFormat:      envOrDefault("PROMVIEW_LDAP_GROUP_FORMAT", "cn"),
		LDAPUsernameAttr:     envOrDefault("PROMVIEW_LDAP_USERNAME_ATTRIBUTE", "uid"),
		LDAPEmailAttr:        envOrDefault("PROMVIEW_LDAP_EMAIL_ATTRIBUTE", "mail"),
		LDAPDisplayNameAttr:  envOrDefault("PROMVIEW_LDAP_DISPLAY_NAME_ATTRIBUTE", "cn"),
		LDAPTimeout:          10 * time.Second,
		SessionCookieSecure:  true,
		// Three times Alertmanager's default repeat_interval of 4h: long enough
		// that a live alert is always re-reported before its window closes, so
		// expiry never fights a repeat notification.
		AlertStaleAfter:     12 * time.Hour,
		AlertExpiryInterval: time.Minute,
		ReconcileInterval:   time.Minute,
		ReconcileTimeout:    10 * time.Second,
		// Two hours covers the maintenance window a silence is usually reaching
		// for, and is short enough that forgetting to clear one is survivable.
		SilenceDefaultDuration: 2 * time.Hour,
		// Thirty days: past that an operator is not silencing an alert, they are
		// declining to fix it, and the rule is the thing to change.
		SilenceMaxDuration: 30 * 24 * time.Hour,
		// A day covers a laptop closed over a weekend night, a rolling deploy,
		// or a proxy that dropped every connection at once, which are the
		// disconnections a resume is actually for. Past that a client is better
		// served by a fresh snapshot than by replaying a day of history.
		StreamRetention: 24 * time.Hour,
	}
	// PROMVIEW_OIDC_COOKIE_SECURE is the name this setting shipped under while
	// OIDC was the only mode that issued a session. Honoured so an upgrade does
	// not silently re-enable Secure on a deployment that had turned it off.
	cookieSecureName := "PROMVIEW_SESSION_COOKIE_SECURE"
	raw := os.Getenv(cookieSecureName)
	if raw == "" {
		cookieSecureName = "PROMVIEW_OIDC_COOKIE_SECURE"
		raw = os.Getenv(cookieSecureName)
	}
	if raw != "" {
		secure, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("%s must be true or false", cookieSecureName)
		}
		cfg.SessionCookieSecure = secure
	}

	if raw := os.Getenv("PROMVIEW_LDAP_START_TLS"); raw != "" {
		startTLS, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, errors.New("PROMVIEW_LDAP_START_TLS must be true or false")
		}
		cfg.LDAPStartTLS = startTLS
	}
	if raw := os.Getenv("PROMVIEW_LDAP_TIMEOUT"); raw != "" {
		timeout, err := time.ParseDuration(raw)
		if err != nil || timeout <= 0 {
			return Config{}, errors.New("PROMVIEW_LDAP_TIMEOUT must be a positive duration such as 10s")
		}
		cfg.LDAPTimeout = timeout
	}

	if raw := os.Getenv("PROMVIEW_ALERT_STALE_AFTER"); raw != "" {
		window, err := time.ParseDuration(raw)
		if err != nil || window < 0 {
			return Config{}, errors.New("PROMVIEW_ALERT_STALE_AFTER must be a non-negative duration such as 12h")
		}
		cfg.AlertStaleAfter = window
	}
	if raw := os.Getenv("PROMVIEW_ALERT_EXPIRY_INTERVAL"); raw != "" {
		interval, err := time.ParseDuration(raw)
		if err != nil || interval <= 0 {
			return Config{}, errors.New("PROMVIEW_ALERT_EXPIRY_INTERVAL must be a positive duration such as 1m")
		}
		cfg.AlertExpiryInterval = interval
	}

	if raw := os.Getenv("PROMVIEW_SILENCE_DEFAULT_DURATION"); raw != "" {
		window, err := time.ParseDuration(raw)
		if err != nil || window <= 0 {
			return Config{}, errors.New("PROMVIEW_SILENCE_DEFAULT_DURATION must be a positive duration such as 2h")
		}
		cfg.SilenceDefaultDuration = window
	}
	if raw := os.Getenv("PROMVIEW_SILENCE_MAX_DURATION"); raw != "" {
		window, err := time.ParseDuration(raw)
		if err != nil || window <= 0 {
			return Config{}, errors.New("PROMVIEW_SILENCE_MAX_DURATION must be a positive duration such as 720h")
		}
		cfg.SilenceMaxDuration = window
	}

	if raw := os.Getenv("PROMVIEW_STREAM_RETENTION"); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value < 0 {
			return Config{}, errors.New("PROMVIEW_STREAM_RETENTION must be a non-negative duration such as 24h")
		}
		cfg.StreamRetention = value
	}

	if raw := os.Getenv("PROMVIEW_RECONCILE_INTERVAL"); raw != "" {
		interval, err := time.ParseDuration(raw)
		if err != nil || interval < 0 {
			return Config{}, errors.New("PROMVIEW_RECONCILE_INTERVAL must be a non-negative duration such as 1m")
		}
		cfg.ReconcileInterval = interval
	}
	if raw := os.Getenv("PROMVIEW_RECONCILE_TIMEOUT"); raw != "" {
		timeout, err := time.ParseDuration(raw)
		if err != nil || timeout <= 0 {
			return Config{}, errors.New("PROMVIEW_RECONCILE_TIMEOUT must be a positive duration such as 10s")
		}
		cfg.ReconcileTimeout = timeout
	}

	if cfg.SilenceDefaultDuration > cfg.SilenceMaxDuration {
		return Config{}, errors.New("PROMVIEW_SILENCE_DEFAULT_DURATION must not exceed PROMVIEW_SILENCE_MAX_DURATION")
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("PROMVIEW_DATABASE_URL is required")
	}
	bootstrapValues := 0
	for _, value := range []string{cfg.BootstrapSourceSlug, cfg.BootstrapSourceToken} {
		if value != "" {
			bootstrapValues++
		}
	}
	if bootstrapValues == 1 {
		return Config{}, errors.New("PROMVIEW_BOOTSTRAP_SOURCE_SLUG and PROMVIEW_BOOTSTRAP_SOURCE_TOKEN must be set together")
	}
	if cfg.BootstrapSourceSlug != "" && cfg.BootstrapSourceName == "" {
		cfg.BootstrapSourceName = cfg.BootstrapSourceSlug
	}
	// One case per mode, each validating its own settings. A mode is listed
	// here only once the binary can actually serve it: admitting one early
	// starts a server that authenticates nobody and explains nothing, which is
	// a worse answer than refusing to boot.
	switch cfg.AuthMode {
	case "open":
	case "local":
		if err := validateLocal(cfg); err != nil {
			return Config{}, err
		}
	case "ldap":
		if err := validateLDAP(cfg); err != nil {
			return Config{}, err
		}
	case "oidc":
		if err := validateOIDC(cfg); err != nil {
			return Config{}, err
		}
	default:
		return Config{}, fmt.Errorf("PROMVIEW_AUTH_MODE must be one of %s", strings.Join(SupportedAuthModes, ", "))
	}

	return cfg, nil
}

// SupportedAuthModes is what PROMVIEW_AUTH_MODE accepts, in the order the error
// message lists them.
var SupportedAuthModes = []string{"open", "oidc", "local", "ldap"}

// validateLocal checks the settings a password sign-in depends on.
//
// Local mode has no external service to point at, so the only thing to get
// wrong is the session cookie - and getting it wrong is a sign-in that appears
// to work and then does not stick, which reads as a broken console rather than
// a misconfiguration.
func validateLocal(cfg Config) error {
	if cfg.SessionCookieSecure {
		return nil
	}
	host, _, err := net.SplitHostPort(cfg.ListenAddress)
	if err != nil {
		host = cfg.ListenAddress
	}
	// An empty host means every interface, which is not loopback-only.
	if host == "" || !isLoopbackHost(host) {
		return errors.New("PROMVIEW_SESSION_COOKIE_SECURE may be false only when listening on loopback")
	}
	return nil
}

func validateLDAP(cfg Config) error {
	required := map[string]string{
		"PROMVIEW_LDAP_URL":     cfg.LDAPURL,
		"PROMVIEW_LDAP_BASE_DN": cfg.LDAPBaseDN,
		"PROMVIEW_LDAP_BIND_DN": cfg.LDAPBindDN,
	}
	for name, value := range required {
		if value == "" {
			return fmt.Errorf("%s is required in LDAP mode", name)
		}
	}
	// The service account's password may legitimately be empty only if the
	// directory allows an anonymous search, which is rare enough that a blank
	// one is far more likely to be an unset environment variable.
	if cfg.LDAPBindPassword == "" {
		return errors.New("PROMVIEW_LDAP_BIND_PASSWORD is required in LDAP mode")
	}
	directory, err := url.Parse(cfg.LDAPURL)
	if err != nil || directory.Scheme == "" || directory.Host == "" {
		return errors.New("PROMVIEW_LDAP_URL must be an absolute URL such as ldaps://directory.example.com:636")
	}
	switch directory.Scheme {
	case "ldaps":
	case "ldap":
		// Search-then-bind sends the user's own password to the directory. Over
		// cleartext that password is on the wire, which is the one outcome the
		// whole flow exists to avoid, so plain ldap:// needs StartTLS or a
		// loopback host.
		if !cfg.LDAPStartTLS && !isLoopbackHost(directory.Hostname()) {
			return errors.New("PROMVIEW_LDAP_URL must use ldaps://, set PROMVIEW_LDAP_START_TLS=true, or point at a loopback host")
		}
	default:
		return errors.New("PROMVIEW_LDAP_URL must use the ldap or ldaps scheme")
	}
	if cfg.LDAPIssuer != "" {
		issuer, err := url.Parse(cfg.LDAPIssuer)
		if err != nil || (issuer.Scheme != "ldap" && issuer.Scheme != "ldaps") || issuer.Host == "" {
			return errors.New("PROMVIEW_LDAP_ISSUER must be an ldap:// or ldaps:// URL")
		}
	}
	if !strings.Contains(cfg.LDAPUserFilter, "%s") {
		return errors.New("PROMVIEW_LDAP_USER_FILTER must contain %s for the username")
	}
	switch cfg.LDAPGroupFormat {
	case "cn", "dn":
	default:
		return errors.New("PROMVIEW_LDAP_GROUP_FORMAT must be cn or dn")
	}
	if err := validateLocal(cfg); err != nil {
		return err
	}
	return nil
}

func validateOIDC(cfg Config) error {
	required := map[string]string{
		"PROMVIEW_OIDC_ISSUER_URL":    cfg.OIDCIssuerURL,
		"PROMVIEW_OIDC_CLIENT_ID":     cfg.OIDCClientID,
		"PROMVIEW_OIDC_CLIENT_SECRET": cfg.OIDCClientSecret,
		"PROMVIEW_OIDC_REDIRECT_URL":  cfg.OIDCRedirectURL,
	}
	for name, value := range required {
		if value == "" {
			return fmt.Errorf("%s is required in OIDC mode", name)
		}
	}
	issuer, err := url.Parse(cfg.OIDCIssuerURL)
	if err != nil || issuer.Scheme == "" || issuer.Host == "" {
		return errors.New("PROMVIEW_OIDC_ISSUER_URL must be an absolute URL")
	}
	if issuer.Scheme != "https" && !isLoopbackHost(issuer.Hostname()) {
		return errors.New("PROMVIEW_OIDC_ISSUER_URL must use HTTPS except on loopback hosts")
	}
	redirect, err := url.Parse(cfg.OIDCRedirectURL)
	if err != nil || redirect.Scheme == "" || redirect.Host == "" {
		return errors.New("PROMVIEW_OIDC_REDIRECT_URL must be an absolute URL")
	}
	if redirect.Scheme != "https" && !isLoopbackHost(redirect.Hostname()) {
		return errors.New("PROMVIEW_OIDC_REDIRECT_URL must use HTTPS except on loopback hosts")
	}
	if redirect.Path != "/api/v1/auth/oidc/callback" || redirect.RawQuery != "" || redirect.Fragment != "" {
		return errors.New("PROMVIEW_OIDC_REDIRECT_URL must end at /api/v1/auth/oidc/callback without a query or fragment")
	}
	if !cfg.SessionCookieSecure && !isLoopbackHost(redirect.Hostname()) {
		return errors.New("PROMVIEW_SESSION_COOKIE_SECURE may be false only on loopback hosts")
	}
	if !contains(cfg.OIDCScopes, "openid") {
		return errors.New("PROMVIEW_OIDC_SCOPES must include openid")
	}
	return nil
}

func splitCSV(raw string) []string {
	var values []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
