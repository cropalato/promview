package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/alertmanager"
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
		case "access":
			return runAccessCommand(ctx, postgres.New(pool), os.Args[2:])
		default:
			return errors.New("usage: promview [migrate|source set|access set|access delete|access inspect]")
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
	if cfg.AuthMode == "oidc" {
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
				store, store, sessionManager, provider,
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

	sweepDone := make(chan struct{})
	go func() {
		defer close(sweepDone)
		runExpirySweeps(ctx, store, cfg.AlertStaleAfter, cfg.AlertExpiryInterval)
	}()

	reconcileDone := make(chan struct{})
	go func() {
		defer close(reconcileDone)
		runReconciliation(ctx, store, alertmanager.NewClient(cfg.ReconcileTimeout), cfg.ReconcileInterval)
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
		err := server.Shutdown(shutdownCtx)
		<-sweepDone
		<-reconcileDone
		return err
	}
}

// expiryStore is the slice of the store the sweep loop needs, kept narrow so the
// loop can be tested without a database.
type expiryStore interface {
	ExpireStaleAlerts(ctx context.Context, defaultStaleAfter time.Duration, now time.Time) (int, error)
}

// runExpirySweeps marks alerts whose source went quiet as expired, on a ticker,
// until the context is cancelled. A zero window disables expiry entirely; a
// failing sweep is logged and retried on the next tick rather than taking the
// server down, since a stale console still serves every other request.
func runExpirySweeps(ctx context.Context, store expiryStore, staleAfter, interval time.Duration) {
	if staleAfter == 0 {
		slog.Info("alert expiry disabled", "reason", "PROMVIEW_ALERT_STALE_AFTER is zero")
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expired, err := store.ExpireStaleAlerts(ctx, staleAfter, time.Now().UTC())
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Error("alert expiry sweep failed", "error", err)
				continue
			}
			if expired > 0 {
				slog.Info("expired stale alerts", "count", expired, "staleAfter", staleAfter)
			}
		}
	}
}

type accessStore interface {
	SetRoleBinding(context.Context, auth.RoleBinding) error
	DeleteRoleBinding(context.Context, string) error
	AuthorizationDiagnostics(context.Context) (auth.AuthorizationDiagnostics, error)
}

type repeatedStrings []string

func (values *repeatedStrings) String() string { return strings.Join(*values, ",") }
func (values *repeatedStrings) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func runAccessCommand(ctx context.Context, store accessStore, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: promview access [set|delete|inspect]")
	}
	switch args[0] {
	case "inspect":
		if len(args) != 1 {
			return errors.New("usage: promview access inspect")
		}
		diagnostics, err := store.AuthorizationDiagnostics(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(diagnostics)
	case "delete":
		flags := flag.NewFlagSet("promview access delete", flag.ContinueOnError)
		name := flags.String("name", "", "binding name")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return errors.New("--name is required")
		}
		return store.DeleteRoleBinding(ctx, *name)
	case "set":
		flags := flag.NewFlagSet("promview access set", flag.ContinueOnError)
		name := flags.String("name", "", "binding name")
		role := flags.String("role", "", "viewer, operator, or administrator")
		userID := flags.Int64("user-id", 0, "Promview user ID")
		issuer := flags.String("oidc-issuer", "", "OIDC issuer URL")
		group := flags.String("oidc-group", "", "OIDC group name")
		var selectors repeatedStrings
		flags.Var(&selectors, "selector", "label selector; repeat for AND semantics")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		binding := auth.RoleBinding{Name: *name, Role: auth.Role(*role)}
		switch {
		case *userID > 0 && *issuer == "" && *group == "":
			binding.SubjectKind = auth.SubjectUser
			binding.UserID = *userID
		case *userID == 0 && *issuer != "" && *group != "":
			binding.SubjectKind = auth.SubjectOIDCGroup
			binding.OIDCIssuer = *issuer
			binding.OIDCGroup = *group
		default:
			return errors.New("set exactly one subject with --user-id or --oidc-issuer and --oidc-group")
		}
		for _, raw := range selectors {
			matcher, err := auth.ParseLabelMatcher(raw)
			if err != nil {
				return err
			}
			binding.Matchers = append(binding.Matchers, matcher)
		}
		if err := auth.ValidateRoleBinding(binding); err != nil {
			return err
		}
		return store.SetRoleBinding(ctx, binding)
	default:
		return errors.New("usage: promview access [set|delete|inspect]")
	}
}

// runSourceUpdate changes an existing source's settings without its token.
// Requiring credentials to adjust something like an Alertmanager URL would mean
// handling a live secret to make an unrelated change, and rewriting the token
// by accident breaks the source's deliveries.
func runSourceUpdate(ctx context.Context, store sourceSetter, args []string) error {
	flags := flag.NewFlagSet("promview source update", flag.ContinueOnError)
	slug := flags.String("slug", "", "slug of the source to update")
	name := flags.String("name", "", "source display name")
	staleAfter := flags.String("stale-after", "", "how long an alert may go unreported before it expires; must exceed this source's repeat_interval (0 disables expiry)")
	alertmanagerURL := flags.String("alertmanager-url", "", "base URL of this source's Alertmanager, read to confirm what is still firing (empty clears it)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *slug == "" {
		return errors.New("usage: promview source update --slug <slug> [--name <name>] [--stale-after <duration>] [--alertmanager-url <url>]")
	}

	var patch sources.Patch
	if isFlagSet(flags, "name") {
		patch.Name = name
	}
	if isFlagSet(flags, "alertmanager-url") {
		patch.AlertmanagerURL = alertmanagerURL
	}
	if isFlagSet(flags, "stale-after") {
		window, err := time.ParseDuration(*staleAfter)
		if err != nil {
			return fmt.Errorf("parse --stale-after: %w", err)
		}
		patch.StaleAfter = &window
	}
	return store.UpdateSource(ctx, *slug, patch)
}

type sourceSetter interface {
	SetSource(context.Context, sources.Source, string) error
	UpdateSource(context.Context, string, sources.Patch) error
}

// isFlagSet reports whether a flag was given on the command line, which is what
// separates "clear this value" from "leave it alone" for optional settings.
func isFlagSet(flags *flag.FlagSet, name string) bool {
	found := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func runSourceCommand(ctx context.Context, store sourceSetter, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: promview source [set|update]")
	}
	if args[0] == "update" {
		return runSourceUpdate(ctx, store, args)
	}
	if args[0] != "set" {
		return errors.New("usage: promview source set --slug <slug> --name <name> --token <token> [--stale-after <duration>] [--alertmanager-url <url>]")
	}
	flags := flag.NewFlagSet("promview source set", flag.ContinueOnError)
	slug := flags.String("slug", "", "stable source slug")
	name := flags.String("name", "", "source display name")
	token := flags.String("token", "", "source bearer token")
	staleAfter := flags.String("stale-after", "", "how long an alert may go unreported before it expires; must exceed this source's repeat_interval (0 disables expiry, empty keeps the stored value)")
	alertmanagerURL := flags.String("alertmanager-url", "", "base URL of this source's Alertmanager, read to confirm what is still firing (empty keeps the stored value)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	source := sources.Source{Slug: *slug, Name: *name}
	if isFlagSet(flags, "alertmanager-url") {
		source.AlertmanagerURL = alertmanagerURL
	}
	if *staleAfter != "" {
		window, err := time.ParseDuration(*staleAfter)
		if err != nil {
			return fmt.Errorf("parse --stale-after: %w", err)
		}
		source.StaleAfter = &window
	}
	return store.SetSource(ctx, source, *token)
}
