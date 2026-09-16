package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/sources"
)

func TestStoreCloseAlert(t *testing.T) {
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

	if _, err := store.CloseAlert(ctx, viewer, id, true); !errors.Is(err, alerts.ErrNotFound) {
		t.Fatalf("viewer close error = %v, want ErrNotFound", err)
	}

	detail, err := store.CloseAlert(ctx, operator, id, true)
	if err != nil {
		t.Fatalf("CloseAlert() error = %v", err)
	}
	if !detail.Alert.Closed || detail.Alert.ClosedBy != "oncall" || detail.Alert.ClosedAt == nil {
		t.Fatalf("after closing: closed=%v by=%q at=%v", detail.Alert.Closed, detail.Alert.ClosedBy, detail.Alert.ClosedAt)
	}
	// Closing is promview-local: the source still says this alert is firing.
	if detail.Alert.SourceStatus != alerts.StatusFiring {
		t.Errorf("source status after closing = %q, want it untouched at firing", detail.Alert.SourceStatus)
	}

	// Out of the default list, which is what makes closing visible at all.
	page, err := store.ListAlerts(ctx, operator, alerts.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Alerts) != 0 {
		t.Errorf("default list returned %d alerts, want the closed one hidden", len(page.Alerts))
	}
	closedOnly := true
	page, err = store.ListAlerts(ctx, operator, alerts.Query{Limit: 10, Closed: &closedOnly})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Alerts) != 1 {
		t.Errorf("closed=true returned %d alerts, want the closed one", len(page.Alerts))
	}

	// An identical repeat carries no new information. Reopening on one would
	// mean a close never outlived the next repeat_interval.
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{staleAlert("yul", "one", now.Add(time.Minute))}); err != nil {
		t.Fatal(err)
	}
	var closed bool
	if err := pool.QueryRow(ctx, "SELECT closed FROM alerts WHERE id = $1", id).Scan(&closed); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Error("an identical repeat reopened a closed alert")
	}

	// A delivery that materially changes the alert is not the alert that was
	// closed, so it comes back.
	changed := staleAlert("yul", "one", now.Add(2*time.Minute))
	changed.Annotations = map[string]string{"summary": "disk now completely full"}
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{changed}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT closed FROM alerts WHERE id = $1", id).Scan(&closed); err != nil {
		t.Fatal(err)
	}
	if closed {
		t.Error("a materially changed delivery did not reopen a closed alert")
	}

	// Reopening by hand is the same endpoint with the other value.
	if _, err := store.CloseAlert(ctx, operator, id, true); err != nil {
		t.Fatal(err)
	}
	detail, err = store.CloseAlert(ctx, operator, id, false)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	if detail.Alert.Closed || detail.Alert.ClosedBy != "" || detail.Alert.ClosedAt != nil {
		t.Errorf("after reopening: closed=%v by=%q at=%v, want all cleared",
			detail.Alert.Closed, detail.Alert.ClosedBy, detail.Alert.ClosedAt)
	}

	// Closing something already closed is not news and must not stream.
	if _, err := store.CloseAlert(ctx, operator, id, false); err != nil {
		t.Fatal(err)
	}
	var closedHistory, reopenedHistory int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE event_type = 'alert.closed'),
		       count(*) FILTER (WHERE event_type = 'alert.reopened.local')
		FROM alert_history
	`).Scan(&closedHistory, &reopenedHistory); err != nil {
		t.Fatal(err)
	}
	if closedHistory != 2 || reopenedHistory != 1 {
		t.Errorf("history = %d closed, %d reopened; want 2 and 1", closedHistory, reopenedHistory)
	}
}
