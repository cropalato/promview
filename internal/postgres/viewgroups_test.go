package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/sources"
)

func teamGroup(name, team string) auth.ViewGroup {
	return auth.ViewGroup{Name: name, Description: name + " team", Matchers: []auth.LabelMatcher{
		{Name: "team", Operator: "=", Value: team},
	}}
}

func TestStoreViewGroupsNarrowTheViewAndNeverScope(t *testing.T) {
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
	for _, group := range []auth.ViewGroup{teamGroup("payments", "payments"), teamGroup("db", "db")} {
		if err := store.SetViewGroup(ctx, group); err != nil {
			t.Fatal(err)
		}
	}

	// Everything in scope, and no team of their own: no view until they join one.
	everything := viewTestUser(t, ctx, store, pool, "all@example.com", auth.Grant{Role: auth.RoleViewer})
	platform := []auth.LabelMatcher{{Name: "team", Operator: "=", Value: "platform"}}
	scoped := viewTestUser(t, ctx, store, pool, "scoped@example.com", auth.Grant{Role: auth.RoleViewer, Matchers: platform})
	admin := viewTestUser(t, ctx, store, pool, "admin@example.com", auth.Grant{Role: auth.RoleAdministrator})

	refresh := func(principal auth.Principal) auth.Principal {
		t.Helper()
		resolved, err := resolvePrincipal(ctx, pool, principal.UserID)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	mine := &alerts.ViewOptions{}
	list := func(principal auth.Principal) alerts.ListResult {
		t.Helper()
		result, err := store.ListAlerts(ctx, principal, alerts.Query{Limit: 50, View: mine})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	if result := list(everything); len(result.Alerts) != 5 || result.View == nil || result.View.Active {
		t.Fatalf("no group: %d alerts, view %+v; want all 5 and an inactive view", len(result.Alerts), result.View)
	}

	if err := store.JoinViewGroup(ctx, everything.UserID, "payments"); err != nil {
		t.Fatal(err)
	}
	// Joining twice is a no-op.
	if err := store.JoinViewGroup(ctx, everything.UserID, "payments"); err != nil {
		t.Fatal(err)
	}
	everything = refresh(everything)
	result := list(everything)
	if got, want := fingerprints(result.Alerts), []string{"pay-crit", "pay-warn", "plat-crit"}; !equalStrings(got, want) {
		t.Fatalf("after joining payments: %v, want %v (the group, plus critical through the bypass)", got, want)
	}
	if result.View.Hidden != 2 {
		t.Fatalf("hidden = %d, want 2", result.View.Hidden)
	}

	// A group outside the caller's scope yields nothing of it: scope still rules.
	if err := store.JoinViewGroup(ctx, scoped.UserID, "payments"); err != nil {
		t.Fatalf("joining outside scope must be allowed: %v", err)
	}
	scoped = refresh(scoped)
	result = list(scoped)
	if got, want := fingerprints(result.Alerts), []string{"plat-crit"}; !equalStrings(got, want) {
		t.Fatalf("scoped caller in an outside group: %v, want only the in-scope critical %v", got, want)
	}
	if result.View.Hidden != 1 {
		t.Fatalf("hidden = %d, want 1 (the in-scope warning)", result.View.Hidden)
	}

	// Leaving removes only the caller's own row and restores the full view.
	if err := store.LeaveViewGroup(ctx, everything.UserID, "payments"); err != nil {
		t.Fatal(err)
	}
	if got := len(list(refresh(everything)).Alerts); got != 5 {
		t.Fatalf("after leaving: %d alerts, want 5", got)
	}
	if got := len(list(refresh(scoped)).Alerts); got != 1 {
		t.Fatalf("leaving must not touch another user's membership; got %d alerts", got)
	}

	// Pinned: cannot be left, can be joined, and unpinning releases it.
	pin := auth.ViewGroupPin{SubjectKind: auth.SubjectUser, UserID: everything.UserID}
	if err := store.PinViewGroup(ctx, "db", pin); err != nil {
		t.Fatal(err)
	}
	if err := store.PinViewGroup(ctx, "db", pin); err != nil {
		t.Fatalf("pinning twice must be a no-op: %v", err)
	}
	everything = refresh(everything)
	if got, want := fingerprints(list(everything).Alerts), []string{"db-warn", "pay-crit", "plat-crit"}; !equalStrings(got, want) {
		t.Fatalf("pinned: %v, want %v", got, want)
	}
	if err := store.LeaveViewGroup(ctx, everything.UserID, "db"); !errors.Is(err, auth.ErrViewGroupPinned) {
		t.Fatalf("leaving a pinned group = %v, want ErrViewGroupPinned", err)
	}
	if err := store.UnpinViewGroup(ctx, "db", pin); err != nil {
		t.Fatal(err)
	}
	if err := store.LeaveViewGroup(ctx, everything.UserID, "db"); err != nil {
		t.Fatalf("leaving after unpinning: %v", err)
	}

	// The global switch still shows everything in scope past a pinned group.
	if err := store.PinViewGroup(ctx, "db", pin); err != nil {
		t.Fatal(err)
	}
	everything = refresh(everything)
	showAll, err := store.ListAlerts(ctx, everything, alerts.Query{Limit: 50, View: &alerts.ViewOptions{ShowAll: true}})
	if err != nil || len(showAll.Alerts) != 5 {
		t.Fatalf("show all past a pin: %d alerts, err %v; want 5", len(showAll.Alerts), err)
	}

	// Pinning a directory group reaches a member by the identity's groups.
	issuer := "https://idp.example"
	var identityID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth_identities (user_id, issuer, subject) VALUES ($1, $2, 'sub-1') RETURNING id
	`, admin.UserID, issuer).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO auth_identity_groups (identity_id, group_name) VALUES ($1, 'sre')", identityID); err != nil {
		t.Fatal(err)
	}
	if err := store.PinViewGroup(ctx, "payments", auth.ViewGroupPin{
		SubjectKind: auth.SubjectOIDCGroup, SubjectIssuer: issuer, SubjectGroup: "sre",
	}); err != nil {
		t.Fatal(err)
	}
	admin = refresh(admin)
	if got, want := fingerprints(list(admin).Alerts), []string{"pay-crit", "pay-warn", "plat-crit"}; !equalStrings(got, want) {
		t.Fatalf("directory group pin: %v, want %v", got, want)
	}
	if err := store.LeaveViewGroup(ctx, admin.UserID, "payments"); !errors.Is(err, auth.ErrViewGroupPinned) {
		t.Fatalf("leaving a group pinned through a directory group = %v, want ErrViewGroupPinned", err)
	}

	// Deleting a group removes it from every view.
	if err := store.DeleteViewGroup(ctx, "payments"); err != nil {
		t.Fatal(err)
	}
	if got := len(list(refresh(admin)).Alerts); got != 5 {
		t.Fatalf("after deleting the group: %d alerts, want 5", got)
	}
}

func TestStoreViewGroupsListingHidesSelectorsFromNonAdministrators(t *testing.T) {
	ctx, store, pool := viewTestStore(t)
	if err := store.SetViewGroup(ctx, teamGroup("secret", "stealth")); err != nil {
		t.Fatal(err)
	}
	viewer := viewTestUser(t, ctx, store, pool, "viewer@example.com", auth.Grant{Role: auth.RoleViewer})
	admin := viewTestUser(t, ctx, store, pool, "admin@example.com", auth.Grant{Role: auth.RoleAdministrator})
	if err := store.JoinViewGroup(ctx, viewer.UserID, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := store.PinViewGroup(ctx, "secret", auth.ViewGroupPin{SubjectKind: auth.SubjectUser, UserID: admin.UserID}); err != nil {
		t.Fatal(err)
	}

	asViewer, err := store.ViewGroups(ctx, viewer)
	if err != nil || len(asViewer) != 1 {
		t.Fatalf("viewer listing = %v, %v", asViewer, err)
	}
	if got := asViewer[0]; got.Name != "secret" || !got.Joined || got.Pinned || len(got.Matchers) != 0 || len(got.Pins) != 0 || got.Members != 0 {
		t.Fatalf("viewer saw %+v: only name, description and own state are allowed", got)
	}
	asAdmin, err := store.ViewGroups(ctx, admin)
	if err != nil || len(asAdmin) != 1 {
		t.Fatalf("admin listing = %v, %v", asAdmin, err)
	}
	if got := asAdmin[0]; !got.Pinned || got.Joined || len(got.Matchers) != 1 || len(got.Pins) != 1 || got.Members != 1 {
		t.Fatalf("admin saw %+v", got)
	}
}

func TestStoreViewGroupsRefuseWhatTheCallerMustFix(t *testing.T) {
	ctx, store, _ := viewTestStore(t)
	if err := store.JoinViewGroup(ctx, 1, "missing"); !errors.Is(err, auth.ErrViewGroupNotFound) {
		t.Errorf("join missing = %v", err)
	}
	if err := store.LeaveViewGroup(ctx, 1, "missing"); !errors.Is(err, auth.ErrViewGroupNotFound) {
		t.Errorf("leave missing = %v", err)
	}
	if err := store.DeleteViewGroup(ctx, "missing"); !errors.Is(err, auth.ErrViewGroupNotFound) {
		t.Errorf("delete missing = %v", err)
	}
	if err := store.SetViewGroup(ctx, auth.ViewGroup{Name: "x"}); !errors.Is(err, auth.ErrInvalidViewGroup) {
		t.Errorf("a group with no selector = %v", err)
	}
	bad := auth.ViewGroup{Name: "x", Matchers: []auth.LabelMatcher{{Name: "a", Operator: "=~", Value: "[[:bogus:]]"}}}
	if err := store.SetViewGroup(ctx, bad); !errors.Is(err, auth.ErrInvalidViewGroup) {
		t.Errorf("a regexp PostgreSQL rejects = %v", err)
	}
	if err := store.SetViewGroup(ctx, teamGroup("x", "x")); err != nil {
		t.Fatal(err)
	}
	if err := store.PinViewGroup(ctx, "x", auth.ViewGroupPin{SubjectKind: auth.SubjectUser, UserID: 9999}); !errors.Is(err, auth.ErrInvalidViewGroup) {
		t.Errorf("pinning a user that does not exist = %v", err)
	}
	// Redefining replaces the selectors rather than appending.
	redefined := auth.ViewGroup{Name: "x", Matchers: []auth.LabelMatcher{{Name: "team", Operator: "=", Value: "y"}}}
	if err := store.SetViewGroup(ctx, redefined); err != nil {
		t.Fatal(err)
	}
	admin := auth.Principal{Roles: []string{"administrator"}, Grants: []auth.Grant{{Role: auth.RoleAdministrator}}}
	groups, err := store.ViewGroups(ctx, admin)
	if err != nil || len(groups) != 1 || len(groups[0].Matchers) != 1 || groups[0].Matchers[0].Value != "y" {
		t.Fatalf("after redefining: %+v, %v", groups, err)
	}
}
