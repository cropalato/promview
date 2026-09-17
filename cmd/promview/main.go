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
	"github.com/cropalato/promview/internal/metrics"
	"github.com/cropalato/promview/internal/postgres"
	"github.com/cropalato/promview/internal/sources"
)

// version is stamped at build time with -X main.version. A binary that cannot
// say what it is makes "did the rollout land" a question nobody can answer from
// outside, which is exactly the question an upgrade raises.
var version = "dev"

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
		case "user":
			return runUserCommand(ctx, postgres.New(pool), os.Stdin, os.Stdout, os.Args[2:])
		default:
			return errors.New("usage: promview [migrate|source set|access set|access delete|access inspect|user create|user set-password|user unlock|user enable|user disable|user list]")
		}
	}

	// A binary newer than its schema does not half-work: the alert queries name
	// columns the database does not have, so every read answers 500 and the
	// console is down. Serving that is worse than not starting, because a
	// process that refuses to boot names its own cause and a 500 does not.
	pending, err := postgres.PendingMigrations(ctx, pool, cfg.MigrationsDir)
	if err != nil {
		return fmt.Errorf("check applied migrations: %w", err)
	}
	if len(pending) > 0 {
		return fmt.Errorf(
			"database schema is behind this binary; run `promview migrate` first (unapplied: %s)",
			strings.Join(pending, ", "),
		)
	}

	// Promview is what an operator looks at when something else breaks, so its
	// own failures are the ones most likely to go unseen. See internal/metrics.
	instruments := metrics.New(version)
	// Every open console polls the database on a timer, so the pool is the
	// ceiling this scales against long before anything else is.
	instruments.WatchPool(poolSnapshot(pool))

	store := countingStore{Store: postgres.New(pool), metrics: instruments}
	if cfg.BootstrapSourceSlug != "" {
		if err := store.BootstrapSource(ctx, sources.Source{
			Slug: cfg.BootstrapSourceSlug,
			Name: cfg.BootstrapSourceName,
		}, cfg.BootstrapSourceToken); err != nil {
			return fmt.Errorf("bootstrap source: %w", err)
		}
	}
	// Open mode issues no session, so it needs neither a session manager nor
	// any of /api/v1/auth/*. Every other mode needs both, which is why the
	// manager is built here rather than inside the branch that picks how people
	// prove who they are.
	var authenticator auth.Authenticator = auth.OpenAuthenticator{}
	var authenticationHandler http.Handler
	// Non-nil only in the modes that check credentials. Its buckets are keyed
	// by attacker-chosen usernames, so it has to be swept or it is a way to
	// spend this process's memory from outside it.
	var loginLimiter *auth.LoginLimiter
	if cfg.AuthMode != "open" {
		const sessionTTL = 12 * time.Hour
		sessionManager := auth.NewSessionManager(store, sessionTTL)
		authenticator = sessionManager
		routes := auth.RouterConfig{Sessions: sessionManager, CookieSecure: cfg.SessionCookieSecure}
		switch cfg.AuthMode {
		case "oidc":
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
			routes.OIDC = auth.NewOIDCHandler(
				store, store, sessionManager, provider,
				cfg.SessionCookieSecure, sessionTTL, store,
			)
		case "local", "ldap":
			var verifier auth.CredentialVerifier
			if cfg.AuthMode == "local" {
				directory, err := auth.NewLocalDirectory(store)
				if err != nil {
					return err
				}
				verifier = directory
			} else {
				directory, err := auth.NewLDAPDirectory(auth.LDAPConfig{
					URL: cfg.LDAPURL, Issuer: cfg.LDAPIssuer,
					BindDN: cfg.LDAPBindDN, BindPassword: cfg.LDAPBindPassword,
					BaseDN: cfg.LDAPBaseDN, UserFilter: cfg.LDAPUserFilter,
					GroupAttribute: cfg.LDAPGroupAttribute, GroupFormat: cfg.LDAPGroupFormat,
					GroupBaseDN: cfg.LDAPGroupBaseDN, GroupFilter: cfg.LDAPGroupFilter,
					GroupNameAttribute: cfg.LDAPGroupNameAttr,
					UsernameAttr:       cfg.LDAPUsernameAttr, EmailAttr: cfg.LDAPEmailAttr,
					DisplayNameAttr: cfg.LDAPDisplayNameAttr,
					StartTLS:        cfg.LDAPStartTLS, Timeout: cfg.LDAPTimeout,
				})
				if err != nil {
					return err
				}
				verifier = directory
			}
			loginLimiter = auth.NewLoginLimiter(auth.LoginLimiterConfig{})
			routes.Credentials = auth.NewCredentialHandler(auth.CredentialHandlerConfig{
				Mode: cfg.AuthMode, Verifier: verifier, Identities: store,
				Sessions: sessionManager, Limiter: loginLimiter,
				CookieSecure: cfg.SessionCookieSecure, SessionTTL: sessionTTL,
				DesktopCodes: store, Observe: instruments.LoginAttempted,
			})
		}
		authenticationHandler = auth.NewRouter(routes)
	}
	// The same client reconciliation reads with; its timeout already bounds one
	// Alertmanager request, which is the property a silence write needs too.
	silencer := alertmanager.NewClient(cfg.ReconcileTimeout)
	// A silence that lands is invisible in the console until something re-reads
	// the source, which would otherwise be the reconcile ticker up to a whole
	// interval later. An operator who silences an alert and sees no change
	// assumes it did not work.
	//
	// Gated on reconciliation being enabled at all: with it off, promview never
	// reads suppression from anywhere, and refreshing one source on one write
	// would leave the console half-informed rather than consistently uninformed.
	var apiSilencer httpapi.Silencer = countingSilencer{inner: silencer, metrics: instruments}
	var refresher *silenceRefresher
	if cfg.ReconcileInterval != 0 {
		refresher = newSilenceRefresher(store, alertmanager.NewClient(cfg.ReconcileTimeout), instruments)
		apiSilencer = refreshingSilencer{inner: apiSilencer, refresher: refresher, ctx: ctx}
	}
	server := &http.Server{
		Addr: cfg.ListenAddress,
		Handler: httpapi.NewObserved(
			httpapi.Observers{
				Request:      instruments.ObserveRequest,
				StreamOpened: instruments.StreamOpened,
				StreamClosed: instruments.StreamClosed,
				StreamPolled: instruments.StreamPolled,
				StreamGapped: instruments.StreamGapped,
			},
			cfg, store, authenticator, apiSilencer, authenticationHandler,
		),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("promview listening", "address", cfg.ListenAddress, "version", version)
		errCh <- server.ListenAndServe()
	}()

	// A listener of its own rather than a route on the public one. The labels
	// name sources and teams, and this deployment is commonly reachable from
	// the internet; a port the ingress never publishes cannot leak them by
	// being forgotten.
	var metricsServer *http.Server
	if cfg.MetricsAddress != "" {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("GET /metrics", instruments.Handler())
		metricsServer = &http.Server{
			Addr:              cfg.MetricsAddress,
			Handler:           metricsMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			slog.Info("promview metrics listening", "address", cfg.MetricsAddress)
			// Never fed into errCh: losing the metrics endpoint is not a reason
			// to take the console down with it.
			if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("metrics listener stopped", "error", err)
			}
		}()
	}

	sweepDone := make(chan struct{})
	go func() {
		defer close(sweepDone)
		runExpirySweeps(ctx, store, cfg.AlertStaleAfter, cfg.AlertExpiryInterval)
	}()

	pruneDone := make(chan struct{})
	go func() {
		defer close(pruneDone)
		runStreamPruning(ctx, store, instruments, cfg.StreamRetention, cfg.AlertExpiryInterval)
	}()

	limiterDone := make(chan struct{})
	go func() {
		defer close(limiterDone)
		runLoginLimiterSweeps(ctx, loginLimiter, cfg.AlertExpiryInterval)
	}()

	reconcileDone := make(chan struct{})
	go func() {
		defer close(reconcileDone)
		runReconciliation(ctx, store, alertmanager.NewClient(cfg.ReconcileTimeout), instruments, cfg.ReconcileInterval)
	}()

	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		if refresher != nil {
			refresher.run(ctx)
		}
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
		if metricsServer != nil {
			_ = metricsServer.Shutdown(shutdownCtx)
		}
		<-sweepDone
		<-pruneDone
		<-limiterDone
		<-reconcileDone
		<-refreshDone
		return err
	}
}

// streamPruneStore is the slice of the store the retention loop needs.
type streamPruneStore interface {
	PruneStreamEvents(ctx context.Context, retention time.Duration, now time.Time) (int, error)
}

// runStreamPruning deletes stream events past the retention window on a ticker.
// It shares the expiry sweep's interval rather than introducing a knob of its
// own: both are housekeeping against the same clock, and a retention window is
// measured in hours while the interval that enforces it is measured in minutes,
// so the exact interval never mattered.
//
// A zero window disables pruning, and the table grows without bound in exchange
// for never asking a client to re-snapshot.
func runStreamPruning(
	ctx context.Context,
	store streamPruneStore,
	instruments *metrics.Metrics,
	retention, interval time.Duration,
) {
	if retention == 0 {
		slog.Info("stream event retention disabled", "reason", "PROMVIEW_STREAM_RETENTION is zero")
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pruned, err := store.PruneStreamEvents(ctx, retention, time.Now().UTC())
			instruments.StreamPruned(pruned)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Error("stream event retention sweep failed", "error", err)
				continue
			}
			if pruned > 0 {
				slog.Info("pruned stream events", "count", pruned, "retention", retention)
			}
		}
	}
}

// expiryStore is the slice of the store the sweep loop needs, kept narrow so the
// loop can be tested without a database.
type expiryStore interface {
	ExpireStaleAlerts(ctx context.Context, defaultStaleAfter time.Duration, now time.Time) (int, error)
}

// runLoginLimiterSweeps drops rate-limit buckets nobody has touched.
//
// Not housekeeping: the buckets are keyed by usernames an unauthenticated
// caller chooses, so a limiter that is never swept is a way to spend this
// process's memory from outside it. A nil limiter means a mode that checks no
// credentials, and there is nothing to sweep.
func runLoginLimiterSweeps(ctx context.Context, limiter *auth.LoginLimiter, interval time.Duration) {
	if limiter == nil || interval <= 0 {
		return
	}
	// Idle for an hour is far past any budget's refill, so a swept bucket is
	// always one that had refilled to full anyway and carried no state worth
	// keeping.
	const idle = time.Hour
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			limiter.Sweep(idle)
		}
	}
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
		issuer := flags.String("issuer", "", "directory issuer URL")
		group := flags.String("group", "", "group name within the issuer")
		// Deprecated aliases, removed in 0.2.0. The chart invokes this command
		// from a post-install Job built out of a user-held values file, so
		// dropping the old spellings outright would break an upgrade at the
		// point where the only symptom is a failed Job.
		deprecatedIssuer := flags.String("oidc-issuer", "", "deprecated alias for --issuer")
		deprecatedGroup := flags.String("oidc-group", "", "deprecated alias for --group")
		var selectors repeatedStrings
		flags.Var(&selectors, "selector", "label selector; repeat for AND semantics")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *issuer != "" && *deprecatedIssuer != "" && *issuer != *deprecatedIssuer {
			return errors.New("--issuer and --oidc-issuer disagree; set one")
		}
		if *group != "" && *deprecatedGroup != "" && *group != *deprecatedGroup {
			return errors.New("--group and --oidc-group disagree; set one")
		}
		subjectIssuer := firstNonEmptyFlag(*issuer, *deprecatedIssuer)
		subjectGroup := firstNonEmptyFlag(*group, *deprecatedGroup)
		binding := auth.RoleBinding{Name: *name, Role: auth.Role(*role)}
		switch {
		case *userID > 0 && subjectIssuer == "" && subjectGroup == "":
			binding.SubjectKind = auth.SubjectUser
			binding.UserID = *userID
		case *userID == 0 && subjectIssuer != "" && subjectGroup != "":
			// The scheme names the directory, so the kind follows from the
			// issuer rather than needing a flag of its own - and a binding whose
			// kind disagreed with its issuer could never match anything while
			// looking exactly like access somebody has.
			binding.SubjectKind = auth.SubjectOIDCGroup
			if scheme, _, found := strings.Cut(subjectIssuer, "://"); found &&
				(scheme == "ldap" || scheme == "ldaps") {
				binding.SubjectKind = auth.SubjectLDAPGroup
			}
			binding.SubjectIssuer = subjectIssuer
			binding.SubjectGroup = subjectGroup
		default:
			return errors.New("set exactly one subject with --user-id or --issuer and --group")
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
	alertmanagerToken := flags.String("alertmanager-token", "", "bearer credential for writing silences to this source's Alertmanager (empty clears it)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *slug == "" {
		return errors.New("usage: promview source update --slug <slug> [--name <name>] [--stale-after <duration>] [--alertmanager-url <url>] [--alertmanager-token <token>]")
	}

	var patch sources.Patch
	if isFlagSet(flags, "name") {
		patch.Name = name
	}
	if isFlagSet(flags, "alertmanager-url") {
		patch.AlertmanagerURL = alertmanagerURL
	}
	if isFlagSet(flags, "alertmanager-token") {
		patch.AlertmanagerToken = alertmanagerToken
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
		return errors.New("usage: promview source set --slug <slug> --name <name> --token <token> [--stale-after <duration>] [--alertmanager-url <url>] [--alertmanager-token <token>]")
	}
	flags := flag.NewFlagSet("promview source set", flag.ContinueOnError)
	slug := flags.String("slug", "", "stable source slug")
	name := flags.String("name", "", "source display name")
	token := flags.String("token", "", "source bearer token")
	staleAfter := flags.String("stale-after", "", "how long an alert may go unreported before it expires; must exceed this source's repeat_interval (0 disables expiry, empty keeps the stored value)")
	alertmanagerURL := flags.String("alertmanager-url", "", "base URL of this source's Alertmanager, read to confirm what is still firing (empty keeps the stored value)")
	alertmanagerToken := flags.String("alertmanager-token", "", "bearer credential for writing silences to this source's Alertmanager (empty keeps the stored value)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	source := sources.Source{Slug: *slug, Name: *name}
	if isFlagSet(flags, "alertmanager-url") {
		source.AlertmanagerURL = alertmanagerURL
	}
	if isFlagSet(flags, "alertmanager-token") {
		source.AlertmanagerToken = alertmanagerToken
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

func firstNonEmptyFlag(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
