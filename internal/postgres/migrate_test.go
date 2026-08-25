package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPendingMigrations(t *testing.T) {
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

	// A database with no ledger at all has applied nothing. Reading that as an
	// error rather than a full pending set would let the case this exists to
	// catch through untouched.
	pending, err := PendingMigrations(ctx, pool, "../../migrations")
	if err != nil {
		t.Fatalf("PendingMigrations() on a bare database error = %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("a database with no schema reported nothing pending")
	}
	if pending[0] != "000001_initial.up.sql" {
		t.Errorf("pending[0] = %q, want the first migration", pending[0])
	}

	if err := ApplyMigrations(ctx, pool, "../../migrations"); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}
	pending, err = PendingMigrations(ctx, pool, "../../migrations")
	if err != nil {
		t.Fatalf("PendingMigrations() after applying error = %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %v, want none once every migration is applied", pending)
	}

	// The case that took the console down: the binary carries a migration the
	// database has not run, so its queries name columns that do not exist.
	if _, err := pool.Exec(ctx,
		"DELETE FROM schema_migrations WHERE name = '000015_silence_provenance.up.sql'"); err != nil {
		t.Fatal(err)
	}
	pending, err = PendingMigrations(ctx, pool, "../../migrations")
	if err != nil {
		t.Fatalf("PendingMigrations() with one rolled back error = %v", err)
	}
	if len(pending) != 1 || pending[0] != "000015_silence_provenance.up.sql" {
		t.Errorf("pending = %v, want exactly the unapplied migration named", pending)
	}
}
