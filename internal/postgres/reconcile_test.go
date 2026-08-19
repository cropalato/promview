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
		{Fingerprint: "silenced", Suppressed: true},
	}
	result, err := store.ReconcileSource(ctx, "yul", live, map[string]bool{"gone": true}, now)
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
	}, nil, now)
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
	}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if quiet != (ReconcileResult{}) {
		t.Errorf("quiet pass = %#v, want no changes", quiet)
	}
}
