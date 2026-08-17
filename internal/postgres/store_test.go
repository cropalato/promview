package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/sources"
)

func TestStoreIngestAndList(t *testing.T) {
	databaseURL := os.Getenv("PROMVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PROMVIEW_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := ApplyMigrations(ctx, pool, "../../migrations"); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}
	if err := ApplyMigrations(ctx, pool, "../../migrations"); err != nil {
		t.Fatalf("second ApplyMigrations() error = %v", err)
	}
	var migrationCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 7 {
		t.Fatalf("migration count = %d, want 7", migrationCount)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE oidc_login_transactions, sessions, role_binding_matchers, role_bindings, auth_identity_groups, auth_identities, users, stream_events, alert_history, alerts RESTART IDENTITY"); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	store := New(pool)
	principal, err := auth.OpenAuthenticator{}.Authenticate(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	streamEvents := func(afterID int64) ([]alerts.StreamEvent, error) {
		batch, err := store.StreamEvents(ctx, principal, afterID, 100)
		return batch.Events, err
	}
	const sourceToken = "0123456789abcdef"
	if err := store.SetSource(ctx, sources.Source{Slug: "primary", Name: "Primary"}, sourceToken); err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapSource(ctx, sources.Source{Slug: "primary", Name: "Bootstrap"}, "fedcba9876543210"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		token string
		want  bool
	}{
		{token: sourceToken, want: true},
		{token: "wrong-token-value", want: false},
		{token: "fedcba9876543210", want: false},
	} {
		got, err := store.AuthenticateSource(ctx, "primary", test.token)
		if err != nil || got != test.want {
			t.Fatalf("AuthenticateSource(%q) = %v, %v, want %v", test.token, got, err, test.want)
		}
	}
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{
		incoming("newest", "critical", "platform", base.Add(3*time.Minute)),
		incoming("middle", "warning", "payments", base.Add(2*time.Minute)),
		incoming("oldest", "info", "platform", base.Add(time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}

	first, err := store.ListAlerts(ctx, principal, alerts.Query{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 3 || len(first.Alerts) != 2 || first.NextCursor == nil {
		t.Fatalf("first page = %#v, want total 3, two alerts, and cursor", first)
	}
	if first.StreamCursor != 3 {
		t.Fatalf("stream cursor = %d, want 3", first.StreamCursor)
	}
	if first.Alerts[0].Fingerprint != "newest" || first.Alerts[1].Fingerprint != "middle" {
		t.Fatalf("first page order = %q, %q", first.Alerts[0].Fingerprint, first.Alerts[1].Fingerprint)
	}
	if first.SeverityCounts["critical"] != 1 || first.SeverityCounts["warning"] != 1 || first.SeverityCounts["info"] != 1 {
		t.Fatalf("severity counts = %#v", first.SeverityCounts)
	}

	second, err := store.ListAlerts(ctx, principal, alerts.Query{Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != 3 || len(second.Alerts) != 1 || second.Alerts[0].Fingerprint != "oldest" || second.NextCursor != nil {
		t.Fatalf("second page = %#v, want final oldest alert", second)
	}

	filtered, err := store.ListAlerts(ctx, principal, alerts.Query{Limit: 10, Team: "platform", Severity: "critical"})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || len(filtered.Alerts) != 1 || filtered.Alerts[0].Fingerprint != "newest" {
		t.Fatalf("filtered result = %#v, want newest alert", filtered)
	}

	events, err := streamEvents(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Type != "alert.created" || events[2].ID != 3 {
		t.Fatalf("initial events = %#v, want three created events", events)
	}
	if events[0].Severity != "critical" || events[0].AlertName != "ExampleAlert" || events[0].SourceSlug != "primary" || events[0].Team != "platform" {
		t.Fatalf("stream notification metadata = %#v", events[0])
	}

	repeated := incoming("middle", "warning", "payments", base.Add(4*time.Minute))
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{repeated}); err != nil {
		t.Fatal(err)
	}
	events, err = streamEvents(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("repeat events = %#v, want none for unchanged alert", events)
	}

	repeated.Annotations["summary"] = "material change"
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{repeated}); err != nil {
		t.Fatal(err)
	}
	events, err = streamEvents(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "alert.updated" || events[0].ID != 4 {
		t.Fatalf("changed events = %#v, want one updated event", events)
	}

	repeated.Status = "resolved"
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{repeated}); err != nil {
		t.Fatal(err)
	}
	events, err = streamEvents(4)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "alert.resolved" || events[0].ID != 5 {
		t.Fatalf("resolved events = %#v, want one resolved event", events)
	}

	detail, err := store.GetAlertDetail(ctx, principal, 2)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Alert.Occurrence != 1 || detail.Alert.SourceStatus != "resolved" || len(detail.History) != 3 {
		t.Fatalf("resolved detail = %#v, want occurrence 1 with three history events", detail)
	}
	if detail.History[0].Type != "alert.resolved" || detail.History[1].Type != "alert.updated" || detail.History[2].Type != "alert.created" {
		t.Fatalf("history order = %#v", detail.History)
	}

	repeated.Status = "firing"
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{repeated}); err != nil {
		t.Fatal(err)
	}
	detail, err = store.GetAlertDetail(ctx, principal, 2)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Alert.Occurrence != 2 || len(detail.History) != 4 || detail.History[0].Type != "alert.reopened" || detail.History[0].Occurrence != 2 {
		t.Fatalf("reopened detail = %#v, want occurrence 2", detail)
	}

	var delivered bool
	if err := pool.QueryRow(ctx, "SELECT last_delivery_at IS NOT NULL FROM alert_sources WHERE slug = 'primary'").Scan(&delivered); err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("source last_delivery_at was not updated")
	}

	binding := auth.RoleBinding{
		Name: "platform-viewers", SubjectKind: auth.SubjectOIDCGroup,
		OIDCIssuer: "https://identity.example.com", OIDCGroup: "platform-viewers", Role: auth.RoleViewer,
		Matchers: []auth.LabelMatcher{{Name: "team", Operator: "=", Value: "platform"}},
	}
	if err := store.SetRoleBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	oidcPrincipal, err := store.ResolveOIDCIdentity(ctx, auth.OIDCIdentity{
		Issuer: "https://identity.example.com", Subject: "user-1", Email: "user@example.com",
		DisplayName: "User One", Groups: []string{"platform-viewers"},
	})
	if err != nil {
		t.Fatal(err)
	}
	paymentsBinding := auth.RoleBinding{
		Name: "payments-viewers", SubjectKind: auth.SubjectOIDCGroup,
		OIDCIssuer: "https://identity.example.com", OIDCGroup: "payments-viewers", Role: auth.RoleViewer,
		Matchers: []auth.LabelMatcher{
			{Name: "team", Operator: "=~", Value: "pay.*"},
			{Name: "severity", Operator: "!=", Value: "critical"},
		},
	}
	if err := store.SetRoleBinding(ctx, paymentsBinding); err != nil {
		t.Fatal(err)
	}
	paymentsPrincipal, err := store.ResolveOIDCIdentity(ctx, auth.OIDCIdentity{
		Issuer: "https://identity.example.com", Subject: "user-2", DisplayName: "Payments User",
		Groups: []string{"payments-viewers"},
	})
	if err != nil {
		t.Fatal(err)
	}
	paymentsAlerts, err := store.ListAlerts(ctx, paymentsPrincipal, alerts.Query{Limit: 10})
	if err != nil || paymentsAlerts.Total != 1 || len(paymentsAlerts.Alerts) != 1 || paymentsAlerts.Alerts[0].Labels["team"] != "payments" {
		t.Fatalf("regex-scoped alerts = %#v, error = %v", paymentsAlerts, err)
	}
	scoped, err := store.ListAlerts(ctx, oidcPrincipal, alerts.Query{Limit: 10})
	if err != nil || scoped.Total != 2 || len(scoped.Alerts) != 2 {
		t.Fatalf("scoped alerts = %#v, error = %v, want two platform alerts", scoped, err)
	}
	if scoped.SeverityCounts["warning"] != 0 || scoped.SeverityCounts["critical"] != 1 || scoped.SeverityCounts["info"] != 1 {
		t.Fatalf("scoped severity counts = %#v", scoped.SeverityCounts)
	}
	broadened, err := store.ListAlerts(ctx, oidcPrincipal, alerts.Query{Limit: 10, Team: "payments"})
	if err != nil || broadened.Total != 0 || len(broadened.Alerts) != 0 {
		t.Fatalf("out-of-scope client filter = %#v, error = %v", broadened, err)
	}
	scopedFirst, err := store.ListAlerts(ctx, oidcPrincipal, alerts.Query{Limit: 1})
	if err != nil || len(scopedFirst.Alerts) != 1 || scopedFirst.NextCursor == nil {
		t.Fatalf("first scoped page = %#v, error = %v", scopedFirst, err)
	}
	scopedSecond, err := store.ListAlerts(ctx, oidcPrincipal, alerts.Query{Limit: 1, Cursor: scopedFirst.NextCursor})
	if err != nil || len(scopedSecond.Alerts) != 1 || scopedSecond.Alerts[0].Labels["team"] != "platform" {
		t.Fatalf("second scoped page = %#v, error = %v", scopedSecond, err)
	}
	if _, err := store.GetAlertDetail(ctx, oidcPrincipal, 2); !errors.Is(err, alerts.ErrNotFound) {
		t.Fatalf("out-of-scope detail error = %v, want not found", err)
	}
	scopedBatch, err := store.StreamEvents(ctx, oidcPrincipal, 0, 100)
	if err != nil || len(scopedBatch.Events) != 2 {
		t.Fatalf("scoped stream = %#v, error = %v, want two platform events", scopedBatch, err)
	}
	transition := incoming("newest", "critical", "security", base.Add(10*time.Minute))
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{transition}); err != nil {
		t.Fatal(err)
	}
	transitionBatch, err := store.StreamEvents(ctx, oidcPrincipal, 6, 100)
	if err != nil || len(transitionBatch.Events) != 1 || transitionBatch.Events[0].Type != "alert.removed" || !transitionBatch.Events[0].Redacted {
		t.Fatalf("scope transition stream = %#v, error = %v", transitionBatch, err)
	}

	manager := auth.NewSessionManager(store, time.Hour)
	token, err := manager.NewSession(ctx, oidcPrincipal)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.FindSession(ctx, auth.HashSessionToken(token), time.Now().UTC())
	if err != nil || session.Principal.Subject != oidcPrincipal.Subject || !session.Principal.CanRead() {
		t.Fatalf("session = %#v, error = %v", session, err)
	}
	if _, err := store.ResolveOIDCIdentity(ctx, auth.OIDCIdentity{
		Issuer: "https://identity.example.com", Subject: "user-1", DisplayName: "User One",
		Groups: []string{"unmapped"},
	}); !errors.Is(err, auth.ErrAccessDenied) {
		t.Fatalf("identity after group removal error = %v, want access denied", err)
	}
	if _, err := store.FindSession(ctx, auth.HashSessionToken(token), time.Now().UTC()); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("session after group removal error = %v, want unauthenticated", err)
	}
	if oidcPrincipal, err = store.ResolveOIDCIdentity(ctx, auth.OIDCIdentity{
		Issuer: "https://identity.example.com", Subject: "user-1", DisplayName: "User One",
		Groups: []string{"platform-viewers"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRoleBinding(ctx, binding.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindSession(ctx, auth.HashSessionToken(token), time.Now().UTC()); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("session after binding revocation error = %v, want unauthenticated", err)
	}
	transaction := auth.OIDCTransaction{
		StateHash: auth.HashSessionToken("state"), Nonce: "nonce", CodeVerifier: "verifier", ExpiresAt: base.Add(time.Hour),
	}
	if err := store.StoreOIDCTransaction(ctx, transaction); err != nil {
		t.Fatal(err)
	}
	consumed, err := store.ConsumeOIDCTransaction(ctx, transaction.StateHash, base)
	if err != nil || consumed.Nonce != transaction.Nonce {
		t.Fatalf("OIDC transaction = %#v, error = %v", consumed, err)
	}
	if _, err := store.ConsumeOIDCTransaction(ctx, transaction.StateHash, base); !errors.Is(err, auth.ErrInvalidOIDCTransaction) {
		t.Fatalf("replayed OIDC transaction error = %v, want invalid transaction", err)
	}
}

func incoming(fingerprint, severity, team string, receivedAt time.Time) alertmanager.IncomingAlert {
	return alertmanager.IncomingAlert{
		SourceSlug:  "primary",
		Fingerprint: fingerprint,
		Status:      "firing",
		Labels: map[string]string{
			"alertname": "ExampleAlert",
			"severity":  severity,
			"team":      team,
		},
		Annotations: map[string]string{"summary": fingerprint},
		StartsAt:    receivedAt.Add(-time.Minute),
		ReceivedAt:  receivedAt,
	}
}
