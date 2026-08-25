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
	// Not just the grouping key: every member also carries team=platform, and a
	// silence on alertname alone would hide the same alert for every other team
	// too, including alerts nobody has seen yet.
	if len(scope.Labels) != 2 || scope.Labels["alertname"] != "HighCPU" || scope.Labels["team"] != "platform" {
		t.Errorf("labels = %v, want every label the members agree on", scope.Labels)
	}
	// The members disagree on instance across sources, but each source holds
	// exactly one member, so each target narrows to that member's own labels.
	for _, target := range scope.Targets {
		want := map[string]string{"demo": "web-01", "edge": "edge-01"}[target.Source]
		if target.Labels["instance"] != want {
			t.Errorf("%s matched on instance=%q, want %q", target.Source, target.Labels["instance"], want)
		}
		if target.Members != 1 {
			t.Errorf("%s members = %d, want 1", target.Source, target.Members)
		}
	}

	// Members of one source that disagree on a label drop it, and keep what
	// they still share. Without this the fold would either miss members or
	// pin the silence to one of them.
	ingest("demo", "f4", map[string]string{"alertname": "HighCPU", "instance": "web-02", "team": "platform"})
	scope, err = store.SilenceScopeForGroup(ctx, operator, []string{"alertname"}, map[string]string{"alertname": "HighCPU"})
	if err != nil {
		t.Fatalf("SilenceScopeForGroup() after a second member error = %v", err)
	}
	for _, target := range scope.Targets {
		if target.Source != "demo" {
			continue
		}
		if _, ok := target.Labels["instance"]; ok {
			t.Errorf("demo matched on instance=%q; its members disagree and one of them would escape",
				target.Labels["instance"])
		}
		if target.Labels["team"] != "platform" || target.Labels["alertname"] != "HighCPU" {
			t.Errorf("demo labels = %v, want what its members still agree on", target.Labels)
		}
		if target.Members != 2 {
			t.Errorf("demo members = %d, want 2", target.Members)
		}
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

func TestStoreSilenceVisibility(t *testing.T) {
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

	amURL := "http://am:9093"
	if err := store.SetSource(ctx, sources.Source{Slug: "demo", Name: "Demo", AlertmanagerURL: &amURL}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	incoming := make([]alertmanager.IncomingAlert, 0, 3)
	for _, fingerprint := range []string{"quiet", "loud-1", "loud-2"} {
		incoming = append(incoming, alertmanager.IncomingAlert{
			SourceSlug: "demo", Fingerprint: fingerprint, Status: "firing",
			Labels:      map[string]string{"alertname": "HighCPU", "instance": fingerprint},
			Annotations: map[string]string{}, StartsAt: now, ReceivedAt: now,
		})
	}
	if err := store.Ingest(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReconcileSource(ctx, "demo", []alertmanager.LiveAlert{
		{Fingerprint: "quiet", Suppressed: true, SilencedBy: []string{"sil-1"}},
		{Fingerprint: "loud-1"},
		{Fingerprint: "loud-2"},
	}, nil, now); err != nil {
		t.Fatal(err)
	}

	admin := auth.Principal{UserID: 1, Subject: "ada", Grants: []auth.Grant{{Role: auth.RoleAdministrator}}}
	yes, no := true, false

	// Absent leaves them in. Hiding by default would make an alert disappear
	// because somebody else silenced it, which is what silencing replaces.
	for _, test := range []struct {
		name  string
		value *bool
		want  int
	}{
		{name: "unset", value: nil, want: 3},
		{name: "only silenced", value: &yes, want: 1},
		{name: "only unsilenced", value: &no, want: 2},
	} {
		result, err := store.ListAlerts(ctx, admin, alerts.Query{Limit: 50, Suppressed: test.value})
		if err != nil {
			t.Fatalf("ListAlerts(%s) error = %v", test.name, err)
		}
		if len(result.Alerts) != test.want {
			t.Errorf("ListAlerts(%s) = %d alerts, want %d", test.name, len(result.Alerts), test.want)
		}
	}

	// A group counts what is held back, or a fully silenced group is
	// indistinguishable from a fully firing one.
	groups, err := store.GroupAlerts(ctx, admin, alerts.Query{Limit: 50, GroupBy: []string{"alertname"}})
	if err != nil {
		t.Fatalf("GroupAlerts() error = %v", err)
	}
	if len(groups.Groups) != 1 {
		t.Fatalf("groups = %#v, want one", groups.Groups)
	}
	if groups.Groups[0].Total != 3 || groups.Groups[0].Silenced != 1 {
		t.Errorf("group = total %d silenced %d, want 3 and 1",
			groups.Groups[0].Total, groups.Groups[0].Silenced)
	}

	// Provenance survives the silence: alertmanager expires and forgets it,
	// promview still answers who asked and why.
	if err := store.RecordSilence(ctx, alerts.SilenceRecord{
		Source: "demo", SilenceID: "sil-1",
		Matchers:  map[string]string{"alertname": "HighCPU", "instance": "quiet"},
		CreatedBy: "ada@example.com", Comment: "disk swap",
		StartsAt: now, EndsAt: now.Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("RecordSilence() error = %v", err)
	}
	// Repeating it is an upsert, not an error: the id comes from alertmanager,
	// and a retry that lands twice must not fail a silence that worked.
	if err := store.RecordSilence(ctx, alerts.SilenceRecord{
		Source: "demo", SilenceID: "sil-1",
		Matchers:  map[string]string{"alertname": "HighCPU", "instance": "quiet"},
		CreatedBy: "ada@example.com", Comment: "disk swap, again",
		StartsAt: now, EndsAt: now.Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("RecordSilence() repeated error = %v", err)
	}

	var quietID int64
	if err := pool.QueryRow(ctx, "SELECT id FROM alerts WHERE fingerprint = 'quiet'").Scan(&quietID); err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetAlertDetail(ctx, admin, quietID)
	if err != nil {
		t.Fatalf("GetAlertDetail() error = %v", err)
	}
	if len(detail.Alert.SilencedBy) != 1 || detail.Alert.SilencedBy[0] != "sil-1" {
		t.Errorf("silencedBy = %v, want the id reconciliation stored", detail.Alert.SilencedBy)
	}
	if len(detail.Silences) != 1 || detail.Silences[0].CreatedBy != "ada@example.com" {
		t.Fatalf("silences = %#v, want the recorded provenance", detail.Silences)
	}
	if detail.Silences[0].Comment != "disk swap, again" {
		t.Errorf("comment = %q, want the upserted value", detail.Silences[0].Comment)
	}

	// A silence made straight on the alertmanager is just as real; promview
	// reports the suppression and stays quiet about an author it never had.
	var loudID int64
	if err := pool.QueryRow(ctx, "SELECT id FROM alerts WHERE fingerprint = 'loud-1'").Scan(&loudID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		"UPDATE alerts SET suppressed = true, silenced_by = $1 WHERE id = $2",
		[]string{"made-elsewhere"}, loudID); err != nil {
		t.Fatal(err)
	}
	detail, err = store.GetAlertDetail(ctx, admin, loudID)
	if err != nil {
		t.Fatalf("GetAlertDetail() for a foreign silence error = %v", err)
	}
	if len(detail.Silences) != 0 {
		t.Errorf("silences = %#v, want none invented for a silence promview did not create", detail.Silences)
	}
}
