package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/auth"
)

func TestStoreDesktopAuthCodes(t *testing.T) {
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

	var userID int64
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email, display_name) VALUES ('ada@example.com', 'Ada') RETURNING id",
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	code := auth.DesktopCode{
		CodeHash: auth.HashSessionToken("one-time"), UserID: userID,
		ExpiresAt: time.Now().UTC().Add(auth.DesktopCodeTTL),
	}
	if err := store.StoreDesktopCode(ctx, code); err != nil {
		t.Fatalf("StoreDesktopCode() error = %v", err)
	}

	got, err := store.ConsumeDesktopCode(ctx, code.CodeHash, time.Now().UTC())
	if err != nil {
		t.Fatalf("ConsumeDesktopCode() error = %v", err)
	}
	if got != userID {
		t.Errorf("user = %d, want %d", got, userID)
	}

	// The delete is the read, so a second attempt finds nothing. This is what
	// makes a code recovered from browser history worthless.
	if _, err := store.ConsumeDesktopCode(ctx, code.CodeHash, time.Now().UTC()); !errors.Is(err, auth.ErrDesktopCodeInvalid) {
		t.Errorf("second redemption error = %v, want ErrDesktopCodeInvalid", err)
	}

	// An expired code is refused and indistinguishable from an unknown one.
	stale := auth.DesktopCode{
		CodeHash: auth.HashSessionToken("stale"), UserID: userID,
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}
	if err := store.StoreDesktopCode(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeDesktopCode(ctx, stale.CodeHash, time.Now().UTC()); !errors.Is(err, auth.ErrDesktopCodeInvalid) {
		t.Errorf("expired redemption error = %v, want ErrDesktopCodeInvalid", err)
	}
	if _, err := store.ConsumeDesktopCode(ctx, auth.HashSessionToken("never"), time.Now().UTC()); !errors.Is(err, auth.ErrDesktopCodeInvalid) {
		t.Errorf("unknown redemption error = %v, want ErrDesktopCodeInvalid", err)
	}

	// Storing sweeps expired rows, so an abandoned sign-in cannot accumulate.
	if err := store.StoreDesktopCode(ctx, auth.DesktopCode{
		CodeHash: auth.HashSessionToken("fresh"), UserID: userID,
		ExpiresAt: time.Now().UTC().Add(auth.DesktopCodeTTL),
	}); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM desktop_auth_codes").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Errorf("rows = %d, want only the fresh code", remaining)
	}

	// Deleting the user takes their pending codes with them.
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM desktop_auth_codes").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Errorf("rows after deleting the user = %d, want 0", remaining)
	}
}
