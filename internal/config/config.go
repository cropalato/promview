package config

import (
	"errors"
	"os"
)

type Config struct {
	ListenAddress string
	DatabaseURL   string
	AuthMode      string
	IngestToken   string
	WebDirectory  string
	MigrationsDir string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress: envOrDefault("PROMVIEW_LISTEN_ADDRESS", ":8080"),
		DatabaseURL:   os.Getenv("PROMVIEW_DATABASE_URL"),
		AuthMode:      envOrDefault("PROMVIEW_AUTH_MODE", "open"),
		IngestToken:   os.Getenv("PROMVIEW_INGEST_TOKEN"),
		WebDirectory:  envOrDefault("PROMVIEW_WEB_DIRECTORY", "web/dist"),
		MigrationsDir: envOrDefault("PROMVIEW_MIGRATIONS_DIRECTORY", "migrations"),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("PROMVIEW_DATABASE_URL is required")
	}
	if cfg.IngestToken == "" {
		return Config{}, errors.New("PROMVIEW_INGEST_TOKEN is required")
	}
	switch cfg.AuthMode {
	case "open", "ldap", "oidc":
	default:
		return Config{}, errors.New("PROMVIEW_AUTH_MODE must be open, ldap, or oidc")
	}

	return cfg, nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
