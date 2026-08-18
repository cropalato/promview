package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/sources"
)

func TestGroupKeyExpressionsRejectsUnusableKeys(t *testing.T) {
	if _, err := groupKeyExpressions(nil); err == nil {
		t.Error("groupKeyExpressions(nil) error = nil, want error")
	}
	// An arbitrary label would be an unindexed GROUP BY with unbounded
	// cardinality, so the vocabulary is closed.
	if _, err := groupKeyExpressions([]string{"labels->>'x'; DROP TABLE alerts"}); err == nil {
		t.Error("groupKeyExpressions(injection) error = nil, want error")
	}
	if _, err := groupKeyExpressions([]string{"alertname", "alertname"}); err == nil {
		t.Error("groupKeyExpressions(duplicate) error = nil, want error")
	}
	if _, err := groupKeyExpressions([]string{"alertname", "source", "team", "severity"}); err == nil {
		t.Error("groupKeyExpressions(too many) error = nil, want error")
	}
	got, err := groupKeyExpressions([]string{"alertname", "source"})
	if err != nil || len(got) != 2 {
		t.Fatalf("groupKeyExpressions(alertname, source) = %v, %v", got, err)
	}
}

func TestStoreGroupAlerts(t *testing.T) {
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
	anonymous := auth.Principal{Anonymous: true}
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	for _, slug := range []string{"yul", "dsm"} {
		if err := store.SetSource(ctx, sources.Source{Slug: slug, Name: slug}, "0123456789abcdef"); err != nil {
			t.Fatal(err)
		}
	}

	// A fan-out under one alertname, mirroring the case that motivated grouping:
	// many members, mixed severity, spread across two sources and two teams.
	batch := []alertmanager.IncomingAlert{
		groupedAlert("yul", "card-1", "Cardinality", "critical", "platform", base.Add(5*time.Minute)),
		groupedAlert("yul", "card-2", "Cardinality", "warning", "platform", base.Add(4*time.Minute)),
		groupedAlert("yul", "card-3", "Cardinality", "info", "payments", base.Add(3*time.Minute)),
		groupedAlert("yul", "card-4", "Cardinality", "novel", "platform", base.Add(2*time.Minute)),
		groupedAlert("dsm", "card-5", "Cardinality", "warning", "platform", base.Add(time.Minute)),
		groupedAlert("yul", "lonely-1", "Lonely", "warning", "payments", base),
	}
	if err := store.Ingest(ctx, batch); err != nil {
		t.Fatal(err)
	}

	result, err := store.GroupAlerts(ctx, anonymous, alerts.Query{
		Limit:   10,
		GroupBy: []string{"alertname", "source"},
	})
	if err != nil {
		t.Fatalf("GroupAlerts() error = %v", err)
	}
	if result.TotalGroups != 3 {
		t.Fatalf("total groups = %d, want 3", result.TotalGroups)
	}
	if result.TotalAlerts != 6 {
		t.Fatalf("total alerts = %d, want 6", result.TotalAlerts)
	}
	if len(result.Groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(result.Groups))
	}

	// Ordered worst severity first: the critical group leads regardless of age.
	first := result.Groups[0]
	if first.Key["alertname"] != "Cardinality" || first.Key["source"] != "yul" {
		t.Fatalf("first group key = %v, want Cardinality/yul", first.Key)
	}
	if first.Total != 4 {
		t.Errorf("first group total = %d, want 4", first.Total)
	}
	if first.WorstSeverity != "critical" {
		t.Errorf("first group worst severity = %q, want critical", first.WorstSeverity)
	}
	// A severity outside the three known buckets is counted, not dropped.
	wantCounts := map[string]int64{"critical": 1, "warning": 1, "info": 1, "other": 1}
	for severity, want := range wantCounts {
		if first.SeverityCounts[severity] != want {
			t.Errorf("first group %s count = %d, want %d", severity, first.SeverityCounts[severity], want)
		}
	}
	if !first.LatestLastSeen.Equal(base.Add(5 * time.Minute)) {
		t.Errorf("first group latest last seen = %v, want %v", first.LatestLastSeen, base.Add(5*time.Minute))
	}
	if first.SampleAlertID == 0 {
		t.Error("first group sample alert id = 0, want the newest member")
	}

	// A group of one still reports its member, which is what lets the console
	// render it as a plain row that opens the alert directly.
	var lonely alerts.Group
	for _, group := range result.Groups {
		if group.Key["alertname"] == "Lonely" {
			lonely = group
		}
	}
	if lonely.Total != 1 || lonely.SampleAlertID == 0 {
		t.Errorf("single-member group = %#v, want one member with a sample id", lonely)
	}

	// Acknowledgement coverage is what an operator scans a collapsed row for.
	operator := auth.Principal{Subject: "operator-1", Grants: []auth.Grant{{Role: auth.RoleOperator}}}
	if _, err := store.AcknowledgeAlert(ctx, operator, first.SampleAlertID, true); err != nil {
		t.Fatal(err)
	}
	acked, err := store.GroupAlerts(ctx, anonymous, alerts.Query{Limit: 10, GroupBy: []string{"alertname", "source"}})
	if err != nil {
		t.Fatal(err)
	}
	if acked.Groups[0].Acknowledged != 1 {
		t.Errorf("acknowledged in first group = %d, want 1", acked.Groups[0].Acknowledged)
	}

	// Read restrictions must reach the aggregate. A reader scoped to payments
	// may not learn how many platform alerts hide inside a group.
	restricted := auth.Principal{Subject: "viewer-1", Grants: []auth.Grant{{
		Role:     auth.RoleViewer,
		Matchers: []auth.LabelMatcher{{Name: "team", Operator: "=", Value: "payments"}},
	}}}
	scoped, err := store.GroupAlerts(ctx, restricted, alerts.Query{Limit: 10, GroupBy: []string{"alertname", "source"}})
	if err != nil {
		t.Fatalf("GroupAlerts(restricted) error = %v", err)
	}
	if scoped.TotalAlerts != 2 {
		t.Fatalf("restricted total alerts = %d, want 2", scoped.TotalAlerts)
	}
	for _, group := range scoped.Groups {
		if group.Total != 1 {
			t.Errorf("restricted group %v total = %d, want 1", group.Key, group.Total)
		}
		if group.Key["alertname"] == "Cardinality" && group.WorstSeverity != "info" {
			t.Errorf("restricted worst severity = %q, want info (the critical member is out of scope)",
				group.WorstSeverity)
		}
	}

	// Filters compose with grouping the same way they do with the flat list.
	filtered, err := store.GroupAlerts(ctx, anonymous, alerts.Query{
		Limit:    10,
		GroupBy:  []string{"alertname"},
		Severity: "warning",
	})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.TotalAlerts != 3 || filtered.TotalGroups != 2 {
		t.Fatalf("filtered result = %d alerts in %d groups, want 3 in 2", filtered.TotalAlerts, filtered.TotalGroups)
	}

	// Keyset pagination must not repeat or skip a group.
	seen := map[string]bool{}
	var cursor *alerts.GroupCursor
	for page := 0; page < 5; page++ {
		query := alerts.Query{Limit: 1, GroupBy: []string{"alertname", "source"}, GroupCursor: cursor}
		paged, err := store.GroupAlerts(ctx, anonymous, query)
		if err != nil {
			t.Fatalf("paged GroupAlerts() error = %v", err)
		}
		if len(paged.Groups) != 1 {
			t.Fatalf("page %d returned %d groups, want 1", page, len(paged.Groups))
		}
		key := paged.Groups[0].Key["alertname"] + "/" + paged.Groups[0].Key["source"]
		if seen[key] {
			t.Fatalf("page %d repeated group %q", page, key)
		}
		seen[key] = true
		cursor = paged.NextCursor
		if cursor == nil {
			break
		}
	}
	if len(seen) != 3 {
		t.Errorf("paged through %d groups, want 3", len(seen))
	}

	// A cursor is bound to the query that produced it; reusing it under a
	// different grouping would silently return the wrong page.
	stale := &alerts.GroupCursor{SeverityRank: 3, LatestLastSeen: base, Key: []string{"Cardinality", "yul"}, Query: "mismatched"}
	if _, err := store.GroupAlerts(ctx, anonymous, alerts.Query{Limit: 10, GroupBy: []string{"alertname", "source"}, GroupCursor: stale}); err == nil {
		t.Error("GroupAlerts() with a foreign cursor error = nil, want error")
	}

	if _, err := store.GroupAlerts(ctx, anonymous, alerts.Query{Limit: 10, GroupBy: []string{"nonsense"}}); err == nil {
		t.Error("GroupAlerts() with an unknown key error = nil, want error")
	}
	if _, err := store.GroupAlerts(ctx, anonymous, alerts.Query{Limit: 0, GroupBy: []string{"alertname"}}); err == nil {
		t.Error("GroupAlerts() with a zero limit error = nil, want error")
	}
}

func groupedAlert(sourceSlug, fingerprint, alertname, severity, team string, lastSeen time.Time) alertmanager.IncomingAlert {
	return alertmanager.IncomingAlert{
		SourceSlug:  sourceSlug,
		Fingerprint: fingerprint,
		Status:      "firing",
		Labels: map[string]string{
			"alertname": alertname,
			"severity":  severity,
			"team":      team,
			"instance":  fingerprint,
		},
		Annotations: map[string]string{"summary": fingerprint},
		StartsAt:    lastSeen.Add(-time.Minute),
		ReceivedAt:  lastSeen,
	}
}
