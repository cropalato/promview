package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/config"
	"github.com/cropalato/promview/internal/httpapi"
	"github.com/cropalato/promview/internal/postgres"
	"github.com/cropalato/promview/internal/sources"
)

func main() {
	if err := run(); err != nil {
		slog.Error("promview stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return err
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate":
			return postgres.ApplyMigrations(ctx, pool, cfg.MigrationsDir)
		case "source":
			return runSourceCommand(ctx, postgres.New(pool), os.Args[2:])
		default:
			return errors.New("usage: promview [migrate|source set]")
		}
	}

	store := postgres.New(pool)
	if cfg.BootstrapSourceSlug != "" {
		if err := store.BootstrapSource(ctx, sources.Source{
			Slug: cfg.BootstrapSourceSlug,
			Name: cfg.BootstrapSourceName,
		}, cfg.BootstrapSourceToken); err != nil {
			return fmt.Errorf("bootstrap source: %w", err)
		}
	}
	var authenticator auth.Authenticator = auth.OpenAuthenticator{}
	var authenticationHandler http.Handler
	if cfg.AuthMode != "open" {
		const sessionTTL = 12 * time.Hour
		sessionManager := auth.NewSessionManager(store, sessionTTL)
		authenticator = sessionManager
		if cfg.AuthMode == "oidc" {
			discoveryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			provider, err := auth.NewDiscoveredOIDCProvider(discoveryCtx, auth.OIDCProviderConfig{
				IssuerURL: cfg.OIDCIssuerURL, ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
				RedirectURL: cfg.OIDCRedirectURL, Scopes: cfg.OIDCScopes,
				UsernameClaim: cfg.OIDCUsernameClaim, EmailClaim: cfg.OIDCEmailClaim,
				DisplayNameClaim: cfg.OIDCDisplayNameClaim, GroupsClaim: cfg.OIDCGroupsClaim,
			})
			cancel()
			if err != nil {
				return err
			}
			authenticationHandler = auth.NewOIDCHandler(
				store, sessionManager, provider,
				auth.OIDCRoleMapping{
					ViewerGroups: cfg.OIDCViewerGroups, OperatorGroups: cfg.OIDCOperatorGroups,
					AdminGroups: cfg.OIDCAdminGroups,
				},
				cfg.OIDCCookieSecure, sessionTTL,
			)
		}
	}
	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           httpapi.New(cfg, store, authenticator, authenticationHandler),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("promview listening", "address", cfg.ListenAddress)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

type sourceSetter interface {
	SetSource(context.Context, sources.Source, string) error
}

func runSourceCommand(ctx context.Context, store sourceSetter, args []string) error {
	if len(args) == 0 || args[0] != "set" {
		return errors.New("usage: promview source set --slug <slug> --name <name> --token <token>")
	}
	flags := flag.NewFlagSet("promview source set", flag.ContinueOnError)
	slug := flags.String("slug", "", "stable source slug")
	name := flags.String("name", "", "source display name")
	token := flags.String("token", "", "source bearer token")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	return store.SetSource(ctx, sources.Source{Slug: *slug, Name: *name}, *token)
}
