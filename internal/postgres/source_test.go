package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/sources"
)

func TestStoreUpdateSource(t *testing.T) {
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

	const token = "0123456789abcdef"
	if err := store.SetSource(ctx, sources.Source{Slug: "yul", Name: "YUL"}, token); err != nil {
		t.Fatal(err)
	}

	url := "http://alertmanager:9093"
	if err := store.UpdateSource(ctx, "yul", sources.Patch{AlertmanagerURL: &url}); err != nil {
		t.Fatalf("UpdateSource() error = %v", err)
	}

	// The whole reason this command exists: adding a URL must not disturb the
	// credentials the source authenticates its deliveries with.
	authenticated, err := store.AuthenticateSource(ctx, "yul", token)
	if err != nil || !authenticated {
		t.Fatalf("AuthenticateSource() = %v, %v; want the original token to still work", authenticated, err)
	}

	var name, storedURL string
	var staleAfter *time.Duration
	if err := pool.QueryRow(ctx, "SELECT name, alertmanager_url, stale_after FROM alert_sources WHERE slug = 'yul'").
		Scan(&name, &storedURL, &staleAfter); err != nil {
		t.Fatal(err)
	}
	if storedURL != url {
		t.Errorf("alertmanager URL = %q, want %q", storedURL, url)
	}
	// Fields the patch did not name keep their values.
	if name != "YUL" || staleAfter != nil {
		t.Errorf("update touched unnamed fields: name = %q, stale_after = %v", name, staleAfter)
	}

	window := 6 * time.Hour
	newName := "YUL primary"
	if err := store.UpdateSource(ctx, "yul", sources.Patch{Name: &newName, StaleAfter: &window}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT name, alertmanager_url FROM alert_sources WHERE slug = 'yul'").
		Scan(&name, &storedURL); err != nil {
		t.Fatal(err)
	}
	if name != newName || storedURL != url {
		t.Errorf("after second update name = %q, url = %q; want the URL preserved", name, storedURL)
	}

	// An empty URL clears the setting, leaving the source to expiry alone.
	empty := ""
	if err := store.UpdateSource(ctx, "yul", sources.Patch{AlertmanagerURL: &empty}); err != nil {
		t.Fatal(err)
	}
	reconcilable, err := store.ReconcilableSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reconcilable) != 0 {
		t.Errorf("reconcilable sources = %v, want none after clearing the URL", reconcilable)
	}

	if err := store.UpdateSource(ctx, "missing", sources.Patch{AlertmanagerURL: &url}); err == nil {
		t.Error("UpdateSource() on an unknown source error = nil, want error")
	}
	if err := store.UpdateSource(ctx, "yul", sources.Patch{}); err == nil {
		t.Error("UpdateSource() with an empty patch error = nil, want error")
	}
	bad := "alertmanager:9093"
	if err := store.UpdateSource(ctx, "yul", sources.Patch{AlertmanagerURL: &bad}); err == nil {
		t.Error("UpdateSource() with a schemeless URL error = nil, want error")
	}
}
