package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddress        string
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
	OIDCViewerGroups     []string
	OIDCOperatorGroups   []string
	OIDCAdminGroups      []string
	OIDCCookieSecure     bool
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress:        envOrDefault("PROMVIEW_LISTEN_ADDRESS", ":8080"),
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
		OIDCViewerGroups:     splitCSV(os.Getenv("PROMVIEW_OIDC_VIEWER_GROUPS")),
		OIDCOperatorGroups:   splitCSV(os.Getenv("PROMVIEW_OIDC_OPERATOR_GROUPS")),
		OIDCAdminGroups:      splitCSV(os.Getenv("PROMVIEW_OIDC_ADMIN_GROUPS")),
		OIDCCookieSecure:     true,
	}
	if raw := os.Getenv("PROMVIEW_OIDC_COOKIE_SECURE"); raw != "" {
		secure, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, errors.New("PROMVIEW_OIDC_COOKIE_SECURE must be true or false")
		}
		cfg.OIDCCookieSecure = secure
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("PROMVIEW_DATABASE_URL is required")
	}
	switch cfg.AuthMode {
	case "open", "ldap", "oidc":
	default:
		return Config{}, errors.New("PROMVIEW_AUTH_MODE must be open, ldap, or oidc")
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
	if cfg.AuthMode == "oidc" {
		if err := validateOIDC(cfg); err != nil {
			return Config{}, err
		}
	}

	return cfg, nil
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
	if !cfg.OIDCCookieSecure && !isLoopbackHost(redirect.Hostname()) {
		return errors.New("PROMVIEW_OIDC_COOKIE_SECURE may be false only on loopback hosts")
	}
	if len(cfg.OIDCViewerGroups)+len(cfg.OIDCOperatorGroups)+len(cfg.OIDCAdminGroups) == 0 {
		return errors.New("at least one OIDC viewer, operator, or administrator group must be configured")
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
