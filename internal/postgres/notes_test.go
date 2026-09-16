package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/sources"
)

func TestStoreAlertNotes(t *testing.T) {
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
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{staleAlert("yul", "one", now)}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := pool.QueryRow(ctx, "SELECT id FROM alerts WHERE fingerprint = 'one'").Scan(&id); err != nil {
		t.Fatal(err)
	}

	operator := auth.Principal{UserID: 1, Subject: "oncall", Grants: []auth.Grant{{Role: auth.RoleOperator}}}
	viewer := auth.Principal{UserID: 2, Subject: "reader", Grants: []auth.Grant{{Role: auth.RoleViewer}}}

	if _, err := store.AddNote(ctx, viewer, id, "not mine to write"); !errors.Is(err, alerts.ErrNotFound) {
		t.Fatalf("viewer note error = %v, want ErrNotFound", err)
	}
	if _, err := store.AddNote(ctx, operator, id, "   "); !errors.Is(err, alerts.ErrInvalid) {
		t.Errorf("whitespace-only note error = %v, want ErrInvalid", err)
	}
	if _, err := store.AddNote(ctx, operator, id, strings.Repeat("x", maxNoteLength+1)); !errors.Is(err, alerts.ErrInvalid) {
		t.Errorf("over-long note error = %v, want ErrInvalid", err)
	}

	detail, err := store.AddNote(ctx, operator, id, "  paged the vendor, awaiting callback  ")
	if err != nil {
		t.Fatalf("AddNote() error = %v", err)
	}
	if len(detail.Notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(detail.Notes))
	}
	note := detail.Notes[0]
	if note.Body != "paged the vendor, awaiting callback" {
		t.Errorf("note body = %q, want it trimmed", note.Body)
	}
	if note.Author != "oncall" {
		t.Errorf("note author = %q, want oncall", note.Author)
	}
	if note.Occurrence != 1 {
		t.Errorf("note occurrence = %d, want 1", note.Occurrence)
	}

	if _, err := store.AddNote(ctx, operator, id, "vendor confirmed a bad disk"); err != nil {
		t.Fatal(err)
	}

	// The list carries a count so an operator scanning it can see there is
	// something written without opening every row.
	page, err := store.ListAlerts(ctx, operator, alerts.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Alerts) != 1 {
		t.Fatalf("listed %d alerts, want 1", len(page.Alerts))
	}
	if page.Alerts[0].NoteCount != 2 {
		t.Errorf("note count on the summary = %d, want 2", page.Alerts[0].NoteCount)
	}

	// Oldest first: a handover is read in the order it was written.
	detail, err = store.GetAlertDetail(ctx, operator, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Notes) != 2 || !strings.HasPrefix(detail.Notes[0].Body, "paged the vendor") {
		t.Errorf("notes = %v, want them oldest first", detail.Notes)
	}

	// Notes survive the occurrence they were written against, carrying it with
	// them, so a note about the previous incident is still attributable.
	resolved := staleAlert("yul", "one", now.Add(time.Minute))
	resolved.Status = alerts.StatusResolved
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{resolved}); err != nil {
		t.Fatal(err)
	}
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{staleAlert("yul", "one", now.Add(2*time.Minute))}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddNote(ctx, operator, id, "second incident, unrelated"); err != nil {
		t.Fatal(err)
	}
	detail, err = store.GetAlertDetail(ctx, operator, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Notes) != 3 {
		t.Fatalf("notes after reopening = %d, want all three kept", len(detail.Notes))
	}
	if detail.Notes[0].Occurrence != 1 || detail.Notes[2].Occurrence != 2 {
		t.Errorf("note occurrences = %d and %d, want 1 and 2",
			detail.Notes[0].Occurrence, detail.Notes[2].Occurrence)
	}

	var noteHistory int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM alert_history WHERE event_type = 'alert.note'").Scan(&noteHistory); err != nil {
		t.Fatal(err)
	}
	if noteHistory != 3 {
		t.Errorf("alert.note history rows = %d, want 3", noteHistory)
	}

	// Deleting the alert takes its notes with it rather than orphaning them.
	if _, err := pool.Exec(ctx, "DELETE FROM alerts WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	var orphans int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM alert_notes").Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("notes left after deleting the alert = %d, want 0", orphans)
	}
}
