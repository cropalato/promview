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

func TestStoreSilenceScope(t *testing.T) {
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
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(ctx, pool, "../../migrations"); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}
	store := New(pool)

	amA, amB := "http://am-a:9093", "http://am-b:9093"
	tokenA := "am-a-secret"
	for _, source := range []sources.Source{
		{Slug: "demo", Name: "Demo", AlertmanagerURL: &amA, AlertmanagerToken: &tokenA},
		{Slug: "edge", Name: "Edge", AlertmanagerURL: &amB},
		{Slug: "orphan", Name: "Orphan"},
	} {
		if err := store.SetSource(ctx, source, "0123456789abcdef"); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC()
	ingest := func(slug, fingerprint string, labels map[string]string) {
		if err := store.Ingest(ctx, []alertmanager.IncomingAlert{{
			SourceSlug: slug, Fingerprint: fingerprint, Status: "firing",
			Labels: labels, Annotations: map[string]string{}, StartsAt: now, ReceivedAt: now,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	ingest("demo", "f1", map[string]string{"alertname": "HighCPU", "instance": "web-01", "team": "platform"})
	ingest("edge", "f2", map[string]string{"alertname": "HighCPU", "instance": "edge-01", "team": "platform"})
	ingest("orphan", "f3", map[string]string{"alertname": "HighCPU", "instance": "orph-01", "team": "platform"})

	var demoAlertID int64
	if err := pool.QueryRow(ctx, "SELECT id FROM alerts WHERE fingerprint = 'f1'").Scan(&demoAlertID); err != nil {
		t.Fatal(err)
	}
	operator := auth.Principal{UserID: 1, Subject: "ada", Grants: []auth.Grant{{Role: auth.RoleAdministrator}}}

	// A single alert silences on its own full label set, against its own source.
	scope, err := store.SilenceScopeForAlert(ctx, operator, demoAlertID)
	if err != nil {
		t.Fatalf("SilenceScopeForAlert() error = %v", err)
	}
	if scope.Labels["instance"] != "web-01" || len(scope.Labels) != 3 {
		t.Errorf("labels = %v, want the alert's full label set", scope.Labels)
	}
	if len(scope.Targets) != 1 || scope.Targets[0].AlertmanagerURL != amA {
		t.Fatalf("targets = %#v, want the alert's own alertmanager", scope.Targets)
	}
	if scope.Targets[0].AlertmanagerToken != tokenA {
		t.Errorf("token = %q, want the source's credential", scope.Targets[0].AlertmanagerToken)
	}

	// A group spanning two Alertmanagers resolves to both. The third source has
	// no URL, so it is skipped rather than failing the whole group.
	scope, err = store.SilenceScopeForGroup(ctx, operator, []string{"alertname"}, map[string]string{"alertname": "HighCPU"})
	if err != nil {
		t.Fatalf("SilenceScopeForGroup() error = %v", err)
	}
	if len(scope.Targets) != 2 {
		t.Fatalf("targets = %#v, want one per configured alertmanager", scope.Targets)
	}
	// The matchers are the grouping key, not every member's labels: the members
	// disagree on instance, and matching on one would miss the others.
	if len(scope.Labels) != 1 || scope.Labels["alertname"] != "HighCPU" {
		t.Errorf("labels = %v, want just the grouping key", scope.Labels)
	}

	// Source names a promview source, not an alert label, so it selects the
	// target instead of becoming a matcher.
	scope, err = store.SilenceScopeForGroup(ctx, operator,
		[]string{"alertname", "source"},
		map[string]string{"alertname": "HighCPU", "source": "demo"})
	if err != nil {
		t.Fatalf("SilenceScopeForGroup() with source error = %v", err)
	}
	if len(scope.Targets) != 1 || scope.Targets[0].Source != "demo" {
		t.Errorf("targets = %#v, want only demo", scope.Targets)
	}
	if _, ok := scope.Labels["source"]; ok {
		t.Error("source leaked into the silence matchers; alertmanager has never heard of it")
	}

	// Grouping by source alone leaves nothing to match on, which would silence
	// the entire Alertmanager.
	if _, err := store.SilenceScopeForGroup(ctx, operator,
		[]string{"source"}, map[string]string{"source": "demo"}); err == nil {
		t.Error("a group keyed only by source produced a matcher-less silence")
	}

	// A source with no Alertmanager cannot be silenced at all.
	var orphanID int64
	if err := pool.QueryRow(ctx, "SELECT id FROM alerts WHERE fingerprint = 'f3'").Scan(&orphanID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SilenceScopeForAlert(ctx, operator, orphanID); !errors.Is(err, alerts.ErrNoSilenceTarget) {
		t.Errorf("orphan source error = %v, want ErrNoSilenceTarget", err)
	}

	// Scope is enforced in SQL: an operator bound to one team must not reach
	// another team's alerts by naming them.
	scoped := auth.Principal{UserID: 2, Subject: "bob", Grants: []auth.Grant{{
		Role:     auth.RoleOperator,
		Matchers: []auth.LabelMatcher{{Name: "team", Operator: "=", Value: "payments"}},
	}}}
	if _, err := store.SilenceScopeForAlert(ctx, scoped, demoAlertID); !errors.Is(err, alerts.ErrNotFound) {
		t.Errorf("out-of-scope alert error = %v, want ErrNotFound", err)
	}
	if _, err := store.SilenceScopeForGroup(ctx, scoped,
		[]string{"alertname"}, map[string]string{"alertname": "HighCPU"}); !errors.Is(err, alerts.ErrNotFound) {
		t.Errorf("out-of-scope group error = %v, want ErrNotFound", err)
	}

	// A viewer cannot silence anything.
	viewer := auth.Principal{UserID: 3, Subject: "vic", Grants: []auth.Grant{{Role: auth.RoleViewer}}}
	if _, err := store.SilenceScopeForAlert(ctx, viewer, demoAlertID); !errors.Is(err, alerts.ErrNotFound) {
		t.Errorf("viewer error = %v, want ErrNotFound", err)
	}

	// The token round-trips through an update without disturbing the URL.
	rotated := "rotated-secret"
	if err := store.UpdateSource(ctx, "demo", sources.Patch{AlertmanagerToken: &rotated}); err != nil {
		t.Fatal(err)
	}
	scope, err = store.SilenceScopeForAlert(ctx, operator, demoAlertID)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Targets[0].AlertmanagerToken != rotated || scope.Targets[0].AlertmanagerURL != amA {
		t.Errorf("after rotation target = %#v, want the new token and the same url", scope.Targets[0])
	}

}
