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

func TestStoreExpireStaleAlerts(t *testing.T) {
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
	now := time.Date(2026, 8, 18, 18, 0, 0, 0, time.UTC)

	if err := store.SetSource(ctx, sources.Source{Slug: "primary", Name: "Primary"}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	shortWindow := time.Hour
	if err := store.SetSource(ctx, sources.Source{Slug: "impatient", Name: "Impatient", StaleAfter: &shortWindow}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	disabled := time.Duration(0)
	if err := store.SetSource(ctx, sources.Source{Slug: "never", Name: "Never", StaleAfter: &disabled}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}

	stale := staleAlert("primary", "stale", now.Add(-13*time.Hour))
	fresh := staleAlert("primary", "fresh", now.Add(-11*time.Hour))
	perSource := staleAlert("impatient", "per-source", now.Add(-2*time.Hour))
	exempt := staleAlert("never", "exempt", now.Add(-40*time.Hour))
	labelled := staleAlert("primary", "labelled", now.Add(-90*time.Minute))
	labelled.Labels["timeout"] = "3600" // one hour, shorter than the 12h default
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{stale, fresh, perSource, exempt, labelled}); err != nil {
		t.Fatal(err)
	}

	before, err := store.StreamEvents(ctx, principal, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	streamBefore := len(before.Events)

	expired, err := store.ExpireStaleAlerts(ctx, 12*time.Hour, now)
	if err != nil {
		t.Fatalf("ExpireStaleAlerts() error = %v", err)
	}
	if expired != 3 {
		t.Fatalf("expired = %d, want 3 (default window, per-source window, timeout label)", expired)
	}

	statuses := map[string]string{}
	rows, err := pool.Query(ctx, "SELECT fingerprint, source_status, ends_at IS NOT NULL FROM alerts")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var fingerprint, status string
		var hasEndsAt bool
		if err := rows.Scan(&fingerprint, &status, &hasEndsAt); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		statuses[fingerprint] = status
		if status == alerts.StatusExpired && !hasEndsAt {
			t.Errorf("expired alert %q left ends_at null", fingerprint)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	for fingerprint, want := range map[string]string{
		"stale":      alerts.StatusExpired,
		"per-source": alerts.StatusExpired,
		"labelled":   alerts.StatusExpired,
		"fresh":      alerts.StatusFiring,
		"exempt":     alerts.StatusFiring,
	} {
		if got := statuses[fingerprint]; got != want {
			t.Errorf("alert %q status = %q, want %q", fingerprint, got, want)
		}
	}

	// The console only reacts to stream events, so expiry has to emit one per
	// alert or the table keeps showing rows that are no longer firing.
	after, err := store.StreamEvents(ctx, principal, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	resolvedEvents := 0
	for _, event := range after.Events[streamBefore:] {
		if event.Type == "alert.resolved" {
			resolvedEvents++
		}
	}
	if resolvedEvents != 3 {
		t.Errorf("stream resolved events = %d, want 3", resolvedEvents)
	}

	// History keeps the distinction the stream deliberately flattens: these
	// alerts were never reported as resolved, the source just went quiet.
	var expiredHistory int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM alert_history WHERE event_type = 'alert.expired'").Scan(&expiredHistory); err != nil {
		t.Fatal(err)
	}
	if expiredHistory != 3 {
		t.Errorf("alert.expired history rows = %d, want 3", expiredHistory)
	}

	// A second sweep must be a no-op: expired alerts are no longer firing.
	again, err := store.ExpireStaleAlerts(ctx, 12*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Errorf("second sweep expired = %d, want 0", again)
	}

	// A zero default window disables expiry for every source that has no window
	// of its own, which is the documented escape hatch.
	if _, err := pool.Exec(ctx, "UPDATE alerts SET source_status = 'firing'"); err != nil {
		t.Fatal(err)
	}
	disabledSweep, err := store.ExpireStaleAlerts(ctx, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if disabledSweep != 2 {
		t.Errorf("sweep with zero default expired = %d, want 2 (only the per-source and label windows apply)", disabledSweep)
	}

	if _, err := store.ExpireStaleAlerts(ctx, -time.Hour, now); err == nil {
		t.Error("ExpireStaleAlerts() with a negative window error = nil, want error")
	}

	// An alert that starts reporting again reopens rather than staying expired.
	reingested := staleAlert("primary", "stale", now)
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{reingested}); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, "SELECT source_status FROM alerts WHERE fingerprint = 'stale'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != alerts.StatusFiring {
		t.Errorf("re-ingested alert status = %q, want firing", status)
	}
}

func staleAlert(sourceSlug, fingerprint string, lastSeen time.Time) alertmanager.IncomingAlert {
	return alertmanager.IncomingAlert{
		SourceSlug:  sourceSlug,
		Fingerprint: fingerprint,
		Status:      "firing",
		Labels: map[string]string{
			"alertname": "StaleAlert",
			"severity":  "critical",
			"team":      "platform",
		},
		Annotations: map[string]string{"summary": fingerprint},
		StartsAt:    lastSeen.Add(-time.Minute),
		ReceivedAt:  lastSeen,
	}
}
