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

func TestStoreAssignAlert(t *testing.T) {
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

	// A viewer must not be able to tell an assignment apart from an alert that
	// is not there, or the error itself leaks which alerts exist.
	if _, err := store.AssignAlert(ctx, viewer, id, "someone"); !errors.Is(err, alerts.ErrNotFound) {
		t.Fatalf("viewer assignment error = %v, want ErrNotFound", err)
	}

	detail, err := store.AssignAlert(ctx, operator, id, "  platform-rota  ")
	if err != nil {
		t.Fatalf("AssignAlert() error = %v", err)
	}
	// Trimmed, because a trailing space is not a different owner.
	if detail.Alert.AssignedTo != "platform-rota" {
		t.Errorf("assignee = %q, want it trimmed to platform-rota", detail.Alert.AssignedTo)
	}
	if detail.Alert.AssignedBy != "oncall" {
		t.Errorf("assignedBy = %q, want the operator who decided, not the owner", detail.Alert.AssignedBy)
	}
	if detail.Alert.AssignedAt == nil {
		t.Error("assignedAt was not recorded")
	}

	// Assigning to the same owner again is not news and must not stream.
	before := streamCount(ctx, t, pool)
	if _, err := store.AssignAlert(ctx, operator, id, "platform-rota"); err != nil {
		t.Fatal(err)
	}
	if after := streamCount(ctx, t, pool); after != before {
		t.Errorf("re-assigning to the same owner wrote %d stream events, want 0", after-before)
	}

	// Clearing is the empty string rather than a second verb.
	detail, err = store.AssignAlert(ctx, operator, id, "")
	if err != nil {
		t.Fatalf("AssignAlert(\"\") error = %v", err)
	}
	if detail.Alert.AssignedTo != "" || detail.Alert.AssignedBy != "" || detail.Alert.AssignedAt != nil {
		t.Errorf("after unassigning: to=%q by=%q at=%v, want all cleared",
			detail.Alert.AssignedTo, detail.Alert.AssignedBy, detail.Alert.AssignedAt)
	}

	if _, err := store.AssignAlert(ctx, operator, id, strings.Repeat("x", maxAssigneeLength+1)); !errors.Is(err, alerts.ErrInvalid) {
		t.Errorf("over-long assignee error = %v, want ErrInvalid", err)
	}

	var assigned, unassigned int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FILTER (WHERE event_type = 'alert.assigned'), count(*) FILTER (WHERE event_type = 'alert.unassigned') FROM alert_history",
	).Scan(&assigned, &unassigned); err != nil {
		t.Fatal(err)
	}
	if assigned != 1 || unassigned != 1 {
		t.Errorf("history = %d assigned, %d unassigned; want 1 and 1", assigned, unassigned)
	}

	// An alert the source resolves and then fires again is a new occurrence, and
	// the previous owner did not agree to own the new one.
	if _, err := store.AssignAlert(ctx, operator, id, "platform-rota"); err != nil {
		t.Fatal(err)
	}
	resolved := staleAlert("yul", "one", now.Add(time.Minute))
	resolved.Status = alerts.StatusResolved
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{resolved}); err != nil {
		t.Fatal(err)
	}
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{staleAlert("yul", "one", now.Add(2*time.Minute))}); err != nil {
		t.Fatal(err)
	}
	var assignee string
	if err := pool.QueryRow(ctx, "SELECT assigned_to FROM alerts WHERE id = $1", id).Scan(&assignee); err != nil {
		t.Fatal(err)
	}
	if assignee != "" {
		t.Errorf("assignee after reopening = %q, want it cleared with the rest of the operator state", assignee)
	}
}

func streamCount(ctx context.Context, t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM stream_events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
