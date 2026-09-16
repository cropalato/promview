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

func TestStoreBulkActions(t *testing.T) {
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
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	if err := store.SetSource(ctx, sources.Source{Slug: "yul", Name: "YUL"}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}

	// Two alerts on the platform team, one on payments. The scoped operator
	// below may only act on platform.
	platform := staleAlert("yul", "a", now)
	second := staleAlert("yul", "b", now)
	payments := staleAlert("yul", "c", now)
	payments.Labels = map[string]string{"alertname": "StaleAlert", "severity": "critical", "team": "payments"}
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{platform, second, payments}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	rows, err := pool.Query(ctx, "SELECT fingerprint, id FROM alerts")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var fingerprint string
		var id int64
		if err := rows.Scan(&fingerprint, &id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ids[fingerprint] = id
	}
	rows.Close()

	global := auth.Principal{UserID: 1, Subject: "oncall", Grants: []auth.Grant{{Role: auth.RoleOperator}}}
	scoped := auth.Principal{UserID: 2, Subject: "platform-only", Grants: []auth.Grant{{
		Role:     auth.RoleOperator,
		Matchers: []auth.LabelMatcher{{Name: "team", Operator: "=", Value: "platform"}},
	}}}
	viewer := auth.Principal{UserID: 3, Subject: "reader", Grants: []auth.Grant{{Role: auth.RoleViewer}}}

	selection := []int64{ids["a"], ids["b"], ids["c"]}

	if _, err := store.BulkClose(ctx, viewer, selection, true); !errors.Is(err, alerts.ErrNotFound) {
		t.Fatalf("viewer bulk error = %v, want ErrNotFound", err)
	}
	if _, err := store.BulkClose(ctx, global, nil, true); !errors.Is(err, alerts.ErrInvalid) {
		t.Errorf("empty selection error = %v, want ErrInvalid", err)
	}
	tooMany := make([]int64, maxBulkAlerts+1)
	if _, err := store.BulkClose(ctx, global, tooMany, true); !errors.Is(err, alerts.ErrInvalid) {
		t.Errorf("oversized selection error = %v, want ErrInvalid", err)
	}

	// The scoped operator acts on the two alerts in scope. The third comes back
	// as not found rather than forbidden, so the reply cannot be read to learn
	// that an alert exists outside the scope.
	outcomes, err := store.BulkAcknowledge(ctx, scoped, selection, true)
	if err != nil {
		t.Fatalf("BulkAcknowledge() error = %v", err)
	}
	byID := map[int64]string{}
	for _, outcome := range outcomes {
		byID[outcome.ID] = outcome.Status
	}
	if byID[ids["a"]] != alerts.BulkApplied || byID[ids["b"]] != alerts.BulkApplied {
		t.Errorf("in-scope outcomes = %v, want both applied", byID)
	}
	if byID[ids["c"]] != alerts.BulkNotFound {
		t.Errorf("out-of-scope outcome = %q, want notFound", byID[ids["c"]])
	}
	// And the out-of-scope alert was genuinely left alone.
	var acknowledged bool
	if err := pool.QueryRow(ctx, "SELECT acknowledged FROM alerts WHERE id = $1", ids["c"]).Scan(&acknowledged); err != nil {
		t.Fatal(err)
	}
	if acknowledged {
		t.Error("a bulk action reached an alert outside the operator's scope")
	}

	// Re-running reports unchanged rather than applied, so an operator can tell
	// what they actually did from what was already done.
	outcomes, err = store.BulkAcknowledge(ctx, scoped, []int64{ids["a"], ids["b"]}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range outcomes {
		if outcome.Status != alerts.BulkUnchanged {
			t.Errorf("repeat outcome for %d = %q, want unchanged", outcome.ID, outcome.Status)
		}
	}

	// A selection naming the same alert twice is one decision, not two.
	outcomes, err = store.BulkAssign(ctx, global, []int64{ids["a"], ids["a"]}, "platform-rota")
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 {
		t.Errorf("outcomes for a duplicated id = %d, want 1", len(outcomes))
	}

	// One note across a selection, which is the case where a single finding
	// explains all of them.
	if _, err := store.BulkNote(ctx, global, selection, "same root cause: bad disk"); err != nil {
		t.Fatal(err)
	}
	var notes int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM alert_notes").Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if notes != 3 {
		t.Errorf("notes written = %d, want one per alert", notes)
	}
	if _, err := store.BulkNote(ctx, global, selection, "   "); !errors.Is(err, alerts.ErrInvalid) {
		t.Errorf("empty bulk note error = %v, want ErrInvalid: the single-alert rules apply", err)
	}

	// Closing takes them out of the default list, exactly as the single-alert
	// path does.
	if _, err := store.BulkClose(ctx, global, selection, true); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListAlerts(ctx, global, alerts.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Alerts) != 0 {
		t.Errorf("default list after closing everything = %d alerts, want 0", len(page.Alerts))
	}
}
