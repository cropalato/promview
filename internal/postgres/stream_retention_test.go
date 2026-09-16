package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/sources"
)

// TestStorePruneStreamEvents covers the half of retention that is easy to get
// wrong: deleting events is trivial, but a client resuming from a cursor that
// no longer exists must be told so rather than silently handed the remainder.
func TestStorePruneStreamEvents(t *testing.T) {
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
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	if err := store.SetSource(ctx, sources.Source{Slug: "yul", Name: "YUL"}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}

	// Three alerts, so three alert.created events, ids 1..3.
	for _, fingerprint := range []string{"one", "two", "three"} {
		if err := store.Ingest(ctx, []alertmanager.IncomingAlert{
			staleAlert("yul", fingerprint, now),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Age the first two past the window; the third stays inside it.
	if _, err := pool.Exec(ctx,
		"UPDATE stream_events SET occurred_at = $1 WHERE id <= 2", now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Nothing is pruned while retention is disabled, whatever the ages.
	pruned, err := store.PruneStreamEvents(ctx, 0, now)
	if err != nil {
		t.Fatalf("PruneStreamEvents() error = %v", err)
	}
	if pruned != 0 {
		t.Fatalf("pruned %d events with retention disabled, want 0", pruned)
	}

	pruned, err = store.PruneStreamEvents(ctx, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("PruneStreamEvents() error = %v", err)
	}
	if pruned != 2 {
		t.Fatalf("pruned = %d, want the two events past the window", pruned)
	}

	var remaining, watermark int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM stream_events").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Errorf("remaining events = %d, want the one inside the window", remaining)
	}
	if err := pool.QueryRow(ctx,
		"SELECT deleted_through FROM stream_event_retention WHERE singleton").Scan(&watermark); err != nil {
		t.Fatal(err)
	}
	if watermark != 2 {
		t.Fatalf("watermark = %d, want the highest id removed", watermark)
	}

	// A client that had already seen everything pruned has lost nothing, and
	// must not be told to throw away a snapshot that is still correct.
	caughtUp, err := store.StreamEvents(ctx, principal, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	if caughtUp.RetainedFrom != 2 {
		t.Errorf("RetainedFrom = %d, want the watermark reported", caughtUp.RetainedFrom)
	}
	if len(caughtUp.Events) != 1 {
		t.Errorf("events after the watermark = %d, want the surviving one", len(caughtUp.Events))
	}

	// A client resuming from before the watermark has missed events that no
	// longer exist. The batch still reports the watermark so the caller can say
	// so; what it must never do is hand back the remainder as if complete.
	behind, err := store.StreamEvents(ctx, principal, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if behind.RetainedFrom != 2 {
		t.Fatalf("RetainedFrom = %d, want 2 so the gap is detectable", behind.RetainedFrom)
	}
	if behind.RetainedFrom <= 1 {
		t.Error("a cursor below the watermark was not distinguishable from a caught-up one")
	}

	// An empty table still answers the question. This is the case the watermark
	// exists for: min(id) can say nothing once every row is gone.
	if _, err := pool.Exec(ctx, "UPDATE stream_events SET occurred_at = $1", now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PruneStreamEvents(ctx, 24*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM stream_events").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("remaining events = %d, want the table emptied", remaining)
	}
	emptied, err := store.StreamEvents(ctx, principal, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if emptied.RetainedFrom != 3 {
		t.Errorf("RetainedFrom on an empty table = %d, want 3", emptied.RetainedFrom)
	}

	// The watermark never moves backwards, which is what stops a later sweep
	// telling a client it had missed nothing.
	if _, err := store.PruneStreamEvents(ctx, 24*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		"SELECT deleted_through FROM stream_event_retention WHERE singleton").Scan(&watermark); err != nil {
		t.Fatal(err)
	}
	if watermark != 3 {
		t.Errorf("watermark after a sweep that removed nothing = %d, want 3", watermark)
	}
}
