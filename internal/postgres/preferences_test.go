package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/preferences"
)

func TestStorePreferences(t *testing.T) {
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
	if err := pool.QueryRow(ctx, "INSERT INTO users (email, display_name) VALUES ('ada@example.com', 'Ada') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	user := auth.Principal{UserID: userID, Subject: "ada"}

	// A user who has never saved anything gets defaults rather than an error,
	// so the console has something to render on first sign-in.
	initial, err := store.ReadPreferences(ctx, user)
	if err != nil {
		t.Fatalf("ReadPreferences() error = %v", err)
	}
	if initial.Density != "auto" || len(initial.Columns) == 0 || !initial.Grouping.Enabled {
		t.Fatalf("initial preferences = %#v, want the defaults", initial)
	}
	if initial.Theme != "system" {
		t.Errorf("initial theme = %q, want system", initial.Theme)
	}

	saved := preferences.Default()
	saved.Density = "compact"
	saved.Columns = []preferences.Column{{ID: "severity"}, {ID: "alert"}, {ID: "label:prometheus_cluster", Width: 180}}
	saved.Grouping = preferences.Grouping{Enabled: true, Keys: []string{"alertname"}}
	saved.Theme = "nord"
	if err := store.WritePreferences(ctx, user, saved); err != nil {
		t.Fatalf("WritePreferences() error = %v", err)
	}
	read, err := store.ReadPreferences(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if read.Density != "compact" || len(read.Columns) != 3 || read.Columns[2].Width != 180 {
		t.Fatalf("read preferences = %#v, want the saved layout", read)
	}
	// The palette follows the operator between machines just as the layout
	// does; that is the whole reason it is stored per user rather than per
	// browser.
	if read.Theme != "nord" {
		t.Errorf("read theme = %q, want nord", read.Theme)
	}

	// Saving again replaces rather than accumulating.
	saved.Density = "comfortable"
	if err := store.WritePreferences(ctx, user, saved); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM user_preferences WHERE user_id = $1", userID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("preference rows = %d, want 1", rows)
	}

	// A layout the console could not render must not reach storage.
	invalid := preferences.Default()
	invalid.Density = "tiny"
	if err := store.WritePreferences(ctx, user, invalid); err == nil {
		t.Error("WritePreferences() with an invalid density error = nil, want error")
	}
	unknownTheme := preferences.Default()
	unknownTheme.Theme = "neon"
	if err := store.WritePreferences(ctx, user, unknownTheme); err == nil {
		t.Error("WritePreferences() with an unknown theme error = nil, want error")
	}

	// A row written by an older console can lack fields this one needs; the
	// read fills them in instead of failing and leaving the table blank.
	if _, err := pool.Exec(ctx, `UPDATE user_preferences SET preferences = '{"grouping":{"enabled":false}}'::jsonb WHERE user_id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	partial, err := store.ReadPreferences(ctx, user)
	if err != nil {
		t.Fatalf("ReadPreferences() on a partial row error = %v", err)
	}
	if partial.Density != "auto" || len(partial.Columns) == 0 {
		t.Errorf("partial preferences = %#v, want defaults filled in", partial)
	}
	// Every row written before the palette existed lacks the key entirely, and
	// an empty theme would be refused by the next write.
	if partial.Theme != "system" {
		t.Errorf("partial theme = %q, want system", partial.Theme)
	}

	// Open mode has no user to key against; both directions say so plainly.
	anonymous := auth.Principal{Anonymous: true}
	if _, err := store.ReadPreferences(ctx, anonymous); !errors.Is(err, preferences.ErrNoSubject) {
		t.Errorf("anonymous read error = %v, want ErrNoSubject", err)
	}
	if err := store.WritePreferences(ctx, anonymous, preferences.Default()); !errors.Is(err, preferences.ErrNoSubject) {
		t.Errorf("anonymous write error = %v, want ErrNoSubject", err)
	}

	// Deleting the user takes their layout with it.
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM user_preferences").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("preference rows after deleting the user = %d, want 0", rows)
	}
}
