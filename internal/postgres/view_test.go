package postgres

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/preferences"
	"github.com/cropalato/promview/internal/sources"
)

func viewTestStore(t *testing.T) (context.Context, *Store, *pgxpool.Pool) {
	t.Helper()
	databaseURL := os.Getenv("PROMVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PROMVIEW_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(ctx, pool, "../../migrations"); err != nil {
		t.Fatalf("ApplyMigrations() error = %v", err)
	}
	return ctx, New(pool), pool
}

// viewTestUser creates a user with the given grants as real role bindings,
// because the stream re-reads a principal's grants from the database.
func viewTestUser(t *testing.T, ctx context.Context, store *Store, pool *pgxpool.Pool, email string, grants ...auth.Grant) auth.Principal {
	t.Helper()
	var userID int64
	if err := pool.QueryRow(ctx, "INSERT INTO users (email, display_name) VALUES ($1, $1) RETURNING id", email).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	for index, grant := range grants {
		binding := auth.RoleBinding{
			Name:        fmt.Sprintf("view-test-%d-%d", userID, index),
			SubjectKind: "user", UserID: userID, Role: grant.Role, Matchers: grant.Matchers,
		}
		if err := store.SetRoleBinding(ctx, binding); err != nil {
			t.Fatal(err)
		}
	}
	principal, err := resolvePrincipal(ctx, pool, userID)
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func fingerprints(items []alerts.Alert) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Fingerprint)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestStoreViewNarrowsInsideScope(t *testing.T) {
	ctx, store, pool := viewTestStore(t)
	if err := store.SetSource(ctx, sources.Source{Slug: "yul", Name: "yul"}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{
		groupedAlert("yul", "plat-warn", "A", "warning", "platform", base.Add(5*time.Minute)),
		groupedAlert("yul", "plat-crit", "B", "critical", "platform", base.Add(4*time.Minute)),
		groupedAlert("yul", "pay-warn", "C", "warning", "payments", base.Add(3*time.Minute)),
		groupedAlert("yul", "pay-crit", "D", "critical", "payments", base.Add(2*time.Minute)),
		groupedAlert("yul", "db-warn", "E", "warning", "db", base.Add(time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}

	platform := []auth.LabelMatcher{{Name: "team", Operator: "=", Value: "platform"}}
	// Everything in scope, and a team they normally look at.
	mixed := viewTestUser(t, ctx, store, pool, "mixed@example.com",
		auth.Grant{Role: auth.RoleViewer},
		auth.Grant{Role: auth.RoleViewer, Matchers: platform},
	)
	scopedOnly := viewTestUser(t, ctx, store, pool, "scoped@example.com", auth.Grant{Role: auth.RoleViewer, Matchers: platform})
	list := func(principal auth.Principal, view *alerts.ViewOptions) alerts.ListResult {
		t.Helper()
		result, err := store.ListAlerts(ctx, principal, alerts.Query{Limit: 50, View: view})
		if err != nil {
			t.Fatalf("ListAlerts() error = %v", err)
		}
		return result
	}

	t.Run("no view applies nothing", func(t *testing.T) {
		result := list(mixed, nil)
		if len(result.Alerts) != 5 || result.View != nil {
			t.Fatalf("got %d alerts, view %+v; want 5 alerts and no view", len(result.Alerts), result.View)
		}
	})

	t.Run("view keeps the team and critical alerts", func(t *testing.T) {
		result := list(mixed, &alerts.ViewOptions{})
		got := fingerprints(result.Alerts)
		want := []string{"pay-crit", "plat-crit", "plat-warn"}
		if !equalStrings(got, want) {
			t.Fatalf("alerts = %v, want %v", got, want)
		}
		if result.Total != 3 {
			t.Errorf("total = %d, want 3: counts must follow the view", result.Total)
		}
		if result.View == nil || !result.View.Active || result.View.Hidden != 2 || !result.View.CriticalBypass {
			t.Errorf("view summary = %+v, want active, 2 hidden, bypass on", result.View)
		}
		matches := map[string]alerts.ViewMatch{}
		for _, item := range result.Alerts {
			matches[item.Fingerprint] = item.ViewMatch
		}
		if matches["plat-warn"] != alerts.ViewMatchView || matches["pay-crit"] != alerts.ViewMatchCritical {
			t.Errorf("view matches = %v", matches)
		}
	})

	t.Run("critical filtered when the user says so", func(t *testing.T) {
		result := list(mixed, &alerts.ViewOptions{FilterCritical: true})
		got := fingerprints(result.Alerts)
		if want := []string{"plat-crit", "plat-warn"}; !equalStrings(got, want) {
			t.Fatalf("alerts = %v, want %v", got, want)
		}
		if result.View.Hidden != 3 || result.View.CriticalBypass {
			t.Errorf("view summary = %+v, want 3 hidden, bypass off", result.View)
		}
	})

	t.Run("show all returns scope and classifies it", func(t *testing.T) {
		result := list(mixed, &alerts.ViewOptions{ShowAll: true})
		if len(result.Alerts) != 5 {
			t.Fatalf("alerts = %d, want 5", len(result.Alerts))
		}
		matches := map[string]alerts.ViewMatch{}
		for _, item := range result.Alerts {
			matches[item.Fingerprint] = item.ViewMatch
		}
		if matches["pay-warn"] != alerts.ViewMatchOutside || matches["db-warn"] != alerts.ViewMatchOutside || matches["pay-crit"] != alerts.ViewMatchCritical {
			t.Errorf("view matches = %v", matches)
		}
		if result.View.Hidden != 2 || !result.View.ShowingAll {
			t.Errorf("view summary = %+v, want 2 that the view would hide", result.View)
		}
	})

	t.Run("a scoped-only caller has no view to apply", func(t *testing.T) {
		result := list(scopedOnly, &alerts.ViewOptions{FilterCritical: true})
		if got := fingerprints(result.Alerts); !equalStrings(got, []string{"plat-crit", "plat-warn"}) {
			t.Fatalf("alerts = %v, want only the platform alerts", got)
		}
		if result.View == nil || result.View.Active || result.View.Hidden != 0 {
			t.Errorf("view summary = %+v, want inactive", result.View)
		}
	})

	t.Run("the critical bypass cannot leave scope", func(t *testing.T) {
		// pay-crit is critical but outside platform. No view option may show it.
		for _, options := range []*alerts.ViewOptions{nil, {}, {ShowAll: true}, {FilterCritical: true}} {
			result := list(scopedOnly, options)
			for _, item := range result.Alerts {
				if item.Fingerprint == "pay-crit" || item.Fingerprint == "db-warn" {
					t.Fatalf("view %+v leaked %s outside scope", options, item.Fingerprint)
				}
			}
		}
	})

	t.Run("an administrator without a team has no view", func(t *testing.T) {
		admin := viewTestUser(t, ctx, store, pool, "admin@example.com", auth.Grant{Role: auth.RoleAdministrator})
		result := list(admin, &alerts.ViewOptions{})
		if len(result.Alerts) != 5 || result.View.Active {
			t.Fatalf("got %d alerts, view %+v; want all 5 and an inactive view", len(result.Alerts), result.View)
		}
	})

	t.Run("grouping agrees with the list", func(t *testing.T) {
		grouped, err := store.GroupAlerts(ctx, mixed, alerts.Query{
			Limit: 10, GroupBy: []string{"alertname"}, View: &alerts.ViewOptions{},
		})
		if err != nil {
			t.Fatalf("GroupAlerts() error = %v", err)
		}
		if grouped.TotalAlerts != 3 || grouped.View == nil || grouped.View.Hidden != 2 {
			t.Fatalf("grouped total = %d, view = %+v; want 3 alerts and 2 hidden", grouped.TotalAlerts, grouped.View)
		}
	})

	t.Run("stream agrees with the snapshot", func(t *testing.T) {
		batch, err := store.StreamEvents(ctx, mixed, &alerts.ViewOptions{}, 0, 100)
		if err != nil {
			t.Fatalf("StreamEvents() error = %v", err)
		}
		if len(batch.Events) != 3 {
			t.Fatalf("events = %d, want 3 (platform x2 and the critical one)", len(batch.Events))
		}
		all, err := store.StreamEvents(ctx, mixed, &alerts.ViewOptions{ShowAll: true}, 0, 100)
		if err != nil {
			t.Fatalf("StreamEvents(all) error = %v", err)
		}
		if len(all.Events) != 5 {
			t.Fatalf("events with everything = %d, want 5", len(all.Events))
		}
		outside := 0
		for _, event := range all.Events {
			if event.ViewMatch == alerts.ViewMatchOutside {
				outside++
			}
		}
		if outside != 2 {
			t.Errorf("events outside the view = %d, want 2", outside)
		}
		scoped, err := store.StreamEvents(ctx, scopedOnly, &alerts.ViewOptions{ShowAll: true}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(scoped.Events) != 2 {
			t.Fatalf("scoped events = %d, want only the 2 platform ones", len(scoped.Events))
		}
	})
}

func TestStoreViewMovesOutOfView(t *testing.T) {
	ctx, store, pool := viewTestStore(t)
	if err := store.SetSource(ctx, sources.Source{Slug: "yul", Name: "yul"}, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	mixed := viewTestUser(t, ctx, store, pool, "mixed@example.com",
		auth.Grant{Role: auth.RoleViewer},
		auth.Grant{Role: auth.RoleViewer, Matchers: []auth.LabelMatcher{{Name: "team", Operator: "=", Value: "platform"}}},
	)

	first := groupedAlert("yul", "mover", "A", "warning", "platform", base)
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{first}); err != nil {
		t.Fatal(err)
	}
	moved := groupedAlert("yul", "mover", "A", "warning", "payments", base.Add(time.Minute))
	if err := store.Ingest(ctx, []alertmanager.IncomingAlert{moved}); err != nil {
		t.Fatal(err)
	}

	batch, err := store.StreamEvents(ctx, mixed, &alerts.ViewOptions{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Events) == 0 {
		t.Fatal("no events")
	}
	last := batch.Events[len(batch.Events)-1]
	if last.Type != "alert.removed" || !last.Redacted {
		t.Fatalf("last event = %+v, want a removal: the alert left the view", last)
	}
}

func TestStoreViewPreferences(t *testing.T) {
	ctx, store, pool := viewTestStore(t)
	var userID int64
	if err := pool.QueryRow(ctx, "INSERT INTO users (email, display_name) VALUES ('ada@example.com', 'Ada') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	user := auth.Principal{UserID: userID, Subject: "ada"}

	// A row written before views existed has no `view` key.
	legacy := preferences.Default()
	legacy.View = nil
	if err := store.WritePreferences(ctx, user, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE user_preferences SET preferences = preferences - 'view' WHERE user_id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	read, err := store.ReadPreferences(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if read.View == nil || !read.View.Enabled || !read.View.CriticalBypass {
		t.Fatalf("legacy view = %+v, want both on: a missing key must not read as off", read.View)
	}

	choice := preferences.Default()
	choice.View = &preferences.ViewPreferences{Enabled: false, CriticalBypass: false}
	if err := store.WritePreferences(ctx, user, choice); err != nil {
		t.Fatal(err)
	}

	// An older client saves its layout without knowing views exist.
	older := preferences.Default()
	older.View = nil
	older.Density = "compact"
	if err := store.WritePreferences(ctx, user, older); err != nil {
		t.Fatal(err)
	}
	read, err = store.ReadPreferences(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if read.Density != "compact" {
		t.Errorf("density = %q, want the older client's layout saved", read.Density)
	}
	if read.View == nil || read.View.Enabled || read.View.CriticalBypass {
		t.Fatalf("view = %+v, want the stored choice preserved", read.View)
	}
}

func TestStoreViewClosedVisibilityPersistsAndSurvivesOlderClients(t *testing.T) {
	ctx, store, pool := viewTestStore(t)
	user := viewTestUser(t, ctx, store, pool, "ada@example.com", auth.Grant{Role: auth.RoleViewer})

	initial, err := store.ReadPreferences(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if initial.ClosedVisibility != "open" {
		t.Fatalf("closed visibility = %q, want open by default", initial.ClosedVisibility)
	}

	chosen := preferences.Default()
	chosen.ClosedVisibility = "closed"
	if err := store.WritePreferences(ctx, user, chosen); err != nil {
		t.Fatal(err)
	}
	// A client that predates the field saves its layout without it.
	older := preferences.Default()
	older.ClosedVisibility = ""
	older.Density = "compact"
	if err := store.WritePreferences(ctx, user, older); err != nil {
		t.Fatal(err)
	}
	read, err := store.ReadPreferences(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if read.ClosedVisibility != "closed" || read.Density != "compact" {
		t.Fatalf("preferences = closed %q density %q, want the stored choice kept and the layout saved", read.ClosedVisibility, read.Density)
	}
}
