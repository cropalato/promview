package postgres

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/sources"
)

/*
The project plan commits to a scale - up to 50,000 active alerts and 100
received alerts per second - that nothing had ever measured. This replaces the
guess with a number.

It is gated on PROMVIEW_LOAD_TEST because it writes tens of thousands of rows
and takes minutes: run it deliberately, not on every change. The assertions are
deliberately generous. A load test that fails on a slow laptop teaches people to
ignore it, so these catch an order-of-magnitude regression and nothing finer;
the numbers it prints are the actual output, and the thresholds only stop a
catastrophic one passing silently.
*/

// loadAlertCount is the active-alert figure the plan commits to. Override with
// PROMVIEW_LOAD_ALERTS to measure a different shape of deployment.
const loadAlertCount = 50_000

// loadIngestBatch is how many alerts one webhook delivery carries. Alertmanager
// groups notifications, so a delivery is a batch rather than one alert, and
// measuring one-at-a-time would measure something nobody runs.
const loadIngestBatch = 100

func TestLoadAtCommittedScale(t *testing.T) {
	if os.Getenv("PROMVIEW_LOAD_TEST") == "" {
		t.Skip("PROMVIEW_LOAD_TEST is not set; this writes tens of thousands of rows")
	}
	databaseURL := os.Getenv("PROMVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PROMVIEW_TEST_DATABASE_URL is not set")
	}
	total := loadAlertCount
	if raw := os.Getenv("PROMVIEW_LOAD_ALERTS"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			t.Fatalf("PROMVIEW_LOAD_ALERTS = %q, want a positive count", raw)
		}
		total = parsed
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
	if err := store.SetSource(ctx, sources.Source{Slug: "load", Name: "Load"}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	principal := auth.Principal{Anonymous: true}
	now := time.Now().UTC()

	// Ingestion, in the batches a real delivery arrives as.
	start := time.Now()
	for sent := 0; sent < total; sent += loadIngestBatch {
		size := loadIngestBatch
		if remaining := total - sent; remaining < size {
			size = remaining
		}
		batch := make([]alertmanager.IncomingAlert, 0, size)
		for i := 0; i < size; i++ {
			batch = append(batch, loadAlert(sent+i, now))
		}
		if err := store.Ingest(ctx, batch); err != nil {
			t.Fatalf("Ingest at %d: %v", sent, err)
		}
	}
	ingestElapsed := time.Since(start)
	perSecond := float64(total) / ingestElapsed.Seconds()
	t.Logf("ingest: %d alerts in %s (%.0f/s, batches of %d)",
		total, ingestElapsed.Round(time.Millisecond), perSecond, loadIngestBatch)

	// The plan commits to 100 per second. Anything at or below that is a
	// regression worth failing over; the margin above it is the headroom.
	if perSecond < 100 {
		t.Errorf("ingest rate %.0f/s is below the 100/s the project plan commits to", perSecond)
	}

	var stored int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM alerts").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != total {
		t.Fatalf("stored %d alerts, want %d: the generator is reusing fingerprints", stored, total)
	}

	// Reads, with the whole set loaded. These are what an operator waits on:
	// the first page carries the severity counts, which touch every row.
	measure := func(name string, run func() error) time.Duration {
		t.Helper()
		samples := make([]time.Duration, 0, 5)
		for i := 0; i < 5; i++ {
			began := time.Now()
			if err := run(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			samples = append(samples, time.Since(began))
		}
		sort.Slice(samples, func(a, b int) bool { return samples[a] < samples[b] })
		median := samples[len(samples)/2]
		t.Logf("%-28s median %s (worst %s)", name, median.Round(time.Millisecond), samples[len(samples)-1].Round(time.Millisecond))
		return median
	}

	firstPage := measure("first page + counts", func() error {
		_, err := store.ListAlerts(ctx, principal, alerts.Query{Limit: 100})
		return err
	})
	filtered := measure("label matcher", func() error {
		_, err := store.ListAlerts(ctx, principal, alerts.Query{
			Limit:   100,
			Matches: []alerts.LabelMatcher{{Name: "team", Operator: "=", Value: "team-3"}},
		})
		return err
	})
	grouped := measure("grouped by alertname+source", func() error {
		_, err := store.GroupAlerts(ctx, principal, alerts.Query{
			Limit: 100, GroupBy: []string{"alertname", "source"},
		})
		return err
	})

	// Two seconds is not a target, it is a cliff: the console is unusable well
	// before this, and a query that crosses it has stopped using an index.
	for name, elapsed := range map[string]time.Duration{
		"first page + counts": firstPage,
		"label matcher":       filtered,
		"grouped":             grouped,
	} {
		if elapsed > 2*time.Second {
			t.Errorf("%s took %s with %d alerts; something has stopped using an index", name, elapsed, total)
		}
	}
}

// loadAlert builds one alert with a distinct fingerprint and a realistic spread
// of labels, so the queries above exercise the indexes rather than one value.
func loadAlert(index int, now time.Time) alertmanager.IncomingAlert {
	severities := []string{"critical", "warning", "info"}
	return alertmanager.IncomingAlert{
		SourceSlug:  "load",
		Fingerprint: fmt.Sprintf("load-%06d", index),
		Status:      alerts.StatusFiring,
		Labels: map[string]string{
			"alertname": fmt.Sprintf("LoadAlert%d", index%25),
			"severity":  severities[index%len(severities)],
			"team":      fmt.Sprintf("team-%d", index%8),
			"instance":  fmt.Sprintf("host-%04d:9100", index%500),
			"cluster":   fmt.Sprintf("cluster-%d", index%4),
		},
		Annotations: map[string]string{"summary": fmt.Sprintf("synthetic alert %d", index)},
		StartsAt:    now.Add(-time.Duration(index%3600) * time.Second),
		ReceivedAt:  now,
	}
}
