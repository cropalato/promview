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

func TestStoreReconcileSource(t *testing.T) {
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
	principal := auth.Principal{Anonymous: true}
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

	amURL := "http://alertmanager.example:9093"
	if err := store.SetSource(ctx, sources.Source{Slug: "yul", Name: "YUL", AlertmanagerURL: &amURL}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSource(ctx, sources.Source{Slug: "dsm", Name: "DSM"}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}

	// Only sources that carry a URL can be reconciled; the rest keep expiry as
	// their backstop.
	reconcilable, err := store.ReconcilableSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reconcilable) != 1 || reconcilable["yul"] != amURL {
		t.Fatalf("reconcilable sources = %v, want only yul", reconcilable)
	}

	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{
		staleAlert("yul", "still-firing", now.Add(-time.Minute)),
		staleAlert("yul", "gone", now.Add(-time.Minute)),
		staleAlert("yul", "silenced", now.Add(-time.Minute)),
		staleAlert("dsm", "untouched", now.Add(-time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}

	firing, err := store.FiringFingerprints(ctx, "yul")
	if err != nil {
		t.Fatal(err)
	}
	if len(firing) != 3 {
		t.Fatalf("firing fingerprints = %v, want three", firing)
	}

	live := []alertmanager.LiveAlert{
		{Fingerprint: "still-firing"},
		{Fingerprint: "silenced", Suppressed: true, SilencedBy: []string{"sil-2", "sil-1"}},
	}
	result, err := store.ReconcileSource(ctx, "yul", live, map[string]bool{"gone": true}, nil, now)
	if err != nil {
		t.Fatalf("ReconcileSource() error = %v", err)
	}
	if result.Resolved != 1 || result.Suppressed != 1 || result.Released != 0 {
		t.Fatalf("result = %#v, want one resolved and one suppressed", result)
	}

	statuses := map[string]string{}
	suppressed := map[string]bool{}
	rows, err := pool.Query(ctx, "SELECT fingerprint, source_status, suppressed, ends_at IS NOT NULL FROM alerts")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var fingerprint, status string
		var isSuppressed, hasEndsAt bool
		if err := rows.Scan(&fingerprint, &status, &isSuppressed, &hasEndsAt); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		statuses[fingerprint] = status
		suppressed[fingerprint] = isSuppressed
		if fingerprint == "gone" && !hasEndsAt {
			t.Error("a reconciled alert left ends_at null")
		}
	}
	rows.Close()

	// The alertmanager is authoritative about an alert it no longer holds, so
	// this is resolved rather than the weaker "expired".
	if statuses["gone"] != alerts.StatusResolved {
		t.Errorf("vanished alert status = %q, want resolved", statuses["gone"])
	}
	if statuses["still-firing"] != alerts.StatusFiring || statuses["silenced"] != alerts.StatusFiring {
		t.Errorf("statuses = %v, want the live alerts still firing", statuses)
	}
	// Suppressed is a flag, not a status: a silenced alert is still firing.
	if !suppressed["silenced"] || suppressed["still-firing"] {
		t.Errorf("suppression = %v, want only the silenced alert flagged", suppressed)
	}

	// Which silence is holding it back, stored in a stable order so a later
	// pass reading the same set does not rewrite the row and wake every console.
	var silencedBy []string
	if err := pool.QueryRow(ctx,
		"SELECT silenced_by FROM alerts WHERE fingerprint = 'silenced'").Scan(&silencedBy); err != nil {
		t.Fatal(err)
	}
	if len(silencedBy) != 2 || silencedBy[0] != "sil-1" || silencedBy[1] != "sil-2" {
		t.Errorf("silenced_by = %v, want the ids sorted", silencedBy)
	}
	// Another source's alerts are never touched by this source's reconciliation.
	if statuses["untouched"] != alerts.StatusFiring {
		t.Errorf("other source's alert status = %q, want firing", statuses["untouched"])
	}

	var reconciled int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM alert_history WHERE event_type = 'alert.reconciled'").Scan(&reconciled); err != nil {
		t.Fatal(err)
	}
	if reconciled != 1 {
		t.Errorf("alert.reconciled history rows = %d, want 1", reconciled)
	}

	// The console only reacts to stream events, so both kinds of change emit one.
	batch, err := store.StreamEvents(ctx, principal, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var resolvedEvents, updatedEvents int
	for _, event := range batch.Events {
		switch event.Type {
		case "alert.resolved":
			resolvedEvents++
		case "alert.updated":
			updatedEvents++
		}
	}
	if resolvedEvents != 1 || updatedEvents != 1 {
		t.Errorf("stream events = %d resolved, %d updated; want 1 and 1", resolvedEvents, updatedEvents)
	}

	// A silence ending is as much a change as one starting.
	released, err := store.ReconcileSource(ctx, "yul", []alertmanager.LiveAlert{
		{Fingerprint: "still-firing"},
		{Fingerprint: "silenced", Suppressed: false},
	}, nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if released.Released != 1 || released.Suppressed != 0 {
		t.Fatalf("release result = %#v, want one released", released)
	}

	// Reconciling with no changes must write nothing.
	quiet, err := store.ReconcileSource(ctx, "yul", []alertmanager.LiveAlert{
		{Fingerprint: "still-firing"},
		{Fingerprint: "silenced"},
	}, nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if quiet != (ReconcileResult{}) {
		t.Errorf("quiet pass = %#v, want no changes", quiet)
	}
}

// TestStoreReviveExpiredAlerts covers the gap that made expiry's guesses
// permanent: an alert silenced for a maintenance window sends no notifications,
// so the sweep retired it as stale while the Alertmanager was still holding it,
// and nothing ever brought it back.
func TestStoreReviveExpiredAlerts(t *testing.T) {
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
	principal := auth.Principal{Anonymous: true}
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	amURL := "http://alertmanager.example:9093"
	if err := store.SetSource(ctx, sources.Source{Slug: "yul", Name: "YUL", AlertmanagerURL: &amURL}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}

	// Long enough ago that the 12h window has passed for all three.
	stale := now.Add(-24 * time.Hour)
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{
		staleAlert("yul", "silenced-live", stale),
		staleAlert("yul", "really-gone", stale),
		staleAlert("yul", "ended", stale),
	}); err != nil {
		t.Fatal(err)
	}
	// "ended" is closed by the source itself, which is the one status
	// reconciliation must never overrule.
	resolved := staleAlert("yul", "ended", stale)
	resolved.Status = alerts.StatusResolved
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{resolved}); err != nil {
		t.Fatal(err)
	}
	// An operator acknowledged the silenced one before it was wrongly retired.
	if _, err := pool.Exec(ctx,
		"UPDATE alerts SET acknowledged = true, acknowledged_at = $1, acknowledged_by = 'oncall' WHERE fingerprint = 'silenced-live'", now,
	); err != nil {
		t.Fatal(err)
	}

	expired, err := store.ExpireStaleAlerts(ctx, 12*time.Hour, now)
	if err != nil {
		t.Fatalf("ExpireStaleAlerts() error = %v", err)
	}
	if expired != 2 {
		t.Fatalf("expired = %d, want the two firing alerts", expired)
	}

	var occurrenceBefore int
	if err := pool.QueryRow(ctx,
		"SELECT occurrence FROM alerts WHERE fingerprint = 'silenced-live'").Scan(&occurrenceBefore); err != nil {
		t.Fatal(err)
	}

	// The Alertmanager still holds the silenced alert, and still holds the one
	// it already told us had ended. Only the first is promview's to correct.
	result, err := store.ReconcileSource(ctx, "yul", []alertmanager.LiveAlert{
		{Fingerprint: "silenced-live", Suppressed: true, SilencedBy: []string{"sil-1"}},
		{Fingerprint: "ended"},
	}, nil, nil, now)
	if err != nil {
		t.Fatalf("ReconcileSource() error = %v", err)
	}
	if result.Revived != 1 {
		t.Fatalf("result = %#v, want exactly one revived", result)
	}

	var status string
	var hasEndsAt, suppressed, acknowledged bool
	var occurrenceAfter int
	if err := pool.QueryRow(ctx, `
		SELECT source_status, ends_at IS NOT NULL, suppressed, acknowledged, occurrence
		FROM alerts WHERE fingerprint = 'silenced-live'
	`).Scan(&status, &hasEndsAt, &suppressed, &acknowledged, &occurrenceAfter); err != nil {
		t.Fatal(err)
	}
	if status != alerts.StatusFiring {
		t.Errorf("revived status = %q, want firing", status)
	}
	if hasEndsAt {
		// It never ended, so the ending expiry invented has to go with it.
		t.Error("revived alert kept the ends_at expiry gave it")
	}
	if !suppressed {
		t.Error("revived alert lost the suppression the live view reported")
	}
	if !acknowledged {
		t.Error("revived alert lost its acknowledgement; expiry was a guess, not an ending")
	}
	if occurrenceAfter != occurrenceBefore {
		t.Errorf("occurrence = %d, want %d unchanged: nothing ended, so nothing reopened",
			occurrenceAfter, occurrenceBefore)
	}

	if err := pool.QueryRow(ctx,
		"SELECT source_status FROM alerts WHERE fingerprint = 'really-gone'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != alerts.StatusExpired {
		t.Errorf("absent alert status = %q, want it left expired", status)
	}
	if err := pool.QueryRow(ctx,
		"SELECT source_status FROM alerts WHERE fingerprint = 'ended'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != alerts.StatusResolved {
		t.Errorf("resolved alert status = %q, want resolved: the source said it ended", status)
	}

	var revivedHistory int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM alert_history WHERE event_type = 'alert.revived'").Scan(&revivedHistory); err != nil {
		t.Fatal(err)
	}
	if revivedHistory != 1 {
		t.Errorf("alert.revived history rows = %d, want 1", revivedHistory)
	}

	// alert.created would make the console announce a revived critical as a new
	// one, so a deployment correcting thirty wrong expiries would fire thirty
	// desktop notifications.
	batch, err := store.StreamEvents(ctx, principal, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var createdAfterExpiry int
	for _, event := range batch.Events {
		if event.Type == "alert.created" && event.Summary == "silenced-live" {
			createdAfterExpiry++
		}
	}
	// One at ingestion, and none from the revival.
	if createdAfterExpiry != 1 {
		t.Errorf("alert.created events for the revived alert = %d, want only the original", createdAfterExpiry)
	}

	// The flap this would otherwise cause: last_seen is still 24h old, so
	// without reconciliation evidence the very next sweep retires it again, and
	// the pass after that revives it, forever.
	reExpired, err := store.ExpireStaleAlerts(ctx, 12*time.Hour, now)
	if err != nil {
		t.Fatalf("ExpireStaleAlerts() error = %v", err)
	}
	if reExpired != 0 {
		t.Fatalf("second sweep expired %d alerts, want 0: reconciliation vouched for it", reExpired)
	}

	// And the backstop still works: once reconciliation stops vouching, the
	// alert goes stale from that evidence like any other.
	later := now.Add(13 * time.Hour)
	staleAgain, err := store.ExpireStaleAlerts(ctx, 12*time.Hour, later)
	if err != nil {
		t.Fatalf("ExpireStaleAlerts() error = %v", err)
	}
	if staleAgain != 1 {
		t.Errorf("sweep past the window expired %d alerts, want 1: expiry is still the backstop", staleAgain)
	}
}
