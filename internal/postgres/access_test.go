package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/auth"
)

func TestStoreRoleBindingAdministration(t *testing.T) {
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

	admins := auth.RoleBinding{
		Name: "admins", SubjectKind: auth.SubjectOIDCGroup,
		SubjectIssuer: "https://idp.example", SubjectGroup: "promview-admins",
		Role: auth.RoleAdministrator,
	}
	if err := store.SetRoleBinding(ctx, admins); err != nil {
		t.Fatalf("SetRoleBinding() error = %v", err)
	}

	scoped := auth.RoleBinding{
		Name: "platform", SubjectKind: auth.SubjectOIDCGroup,
		SubjectIssuer: "https://idp.example", SubjectGroup: "platform",
		Role: auth.RoleOperator,
		Matchers: []auth.LabelMatcher{
			{Name: "team", Operator: "=", Value: "platform"},
			{Name: "environment", Operator: "!=", Value: "development"},
		},
	}
	if err := store.SetRoleBinding(ctx, scoped); err != nil {
		t.Fatalf("SetRoleBinding(scoped) error = %v", err)
	}

	bindings, err := store.RoleBindings(ctx)
	if err != nil {
		t.Fatalf("RoleBindings() error = %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("bindings = %d, want 2: %#v", len(bindings), bindings)
	}
	// A scope is half of what a binding means; listing the role without it
	// would describe a different binding than the one in force.
	var platform auth.RoleBinding
	for _, binding := range bindings {
		if binding.Name == "platform" {
			platform = binding
		}
	}
	if len(platform.Matchers) != 2 {
		t.Fatalf("matchers = %#v, want both in order", platform.Matchers)
	}
	if platform.Matchers[0].Name != "team" || platform.Matchers[1].Operator != "!=" {
		t.Errorf("matchers = %#v, want them in the order they were written", platform.Matchers)
	}

	// The guard: with one administrator binding left, neither deleting it nor
	// demoting it may succeed, or nobody can undo the change.
	if err := store.DeleteRoleBinding(ctx, "admins"); !errors.Is(err, auth.ErrLastAdministrator) {
		t.Fatalf("delete last administrator error = %v, want ErrLastAdministrator", err)
	}
	demoted := admins
	demoted.Role = auth.RoleViewer
	if err := store.SetRoleBinding(ctx, demoted); !errors.Is(err, auth.ErrLastAdministrator) {
		t.Fatalf("demote last administrator error = %v, want ErrLastAdministrator", err)
	}
	// And it really is still an administrator afterwards.
	bindings, err = store.RoleBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if binding.Name == "admins" && binding.Role != auth.RoleAdministrator {
			t.Fatalf("admins role = %q after a refused demotion", binding.Role)
		}
	}

	// A second administrator makes the first removable: the rule is about the
	// last one, not about any one.
	second := admins
	second.Name = "admins-backup"
	second.SubjectGroup = "promview-admins-backup"
	if err := store.SetRoleBinding(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRoleBinding(ctx, "admins"); err != nil {
		t.Fatalf("delete with a second administrator error = %v", err)
	}

	// A binding that is not an administrator is never held back by the guard.
	if err := store.DeleteRoleBinding(ctx, "platform"); err != nil {
		t.Fatalf("delete operator binding error = %v", err)
	}

	// Validation still refuses nonsense, and says so as something the caller
	// can fix rather than as a server failure.
	if err := store.SetRoleBinding(ctx, auth.RoleBinding{Name: "Bad Name", Role: auth.RoleViewer}); !errors.Is(err, auth.ErrInvalidRoleBinding) {
		t.Errorf("invalid binding error = %v, want ErrInvalidRoleBinding", err)
	}
}

// A deployment with no administrator binding at all has to be able to create
// the first one; open mode is the usual case.
func TestStoreCreatesTheFirstAdministrator(t *testing.T) {
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
		t.Fatal(err)
	}
	store := New(pool)
	if err := store.SetRoleBinding(ctx, auth.RoleBinding{
		Name: "first", SubjectKind: auth.SubjectOIDCGroup,
		SubjectIssuer: "https://idp.example", SubjectGroup: "admins",
		Role: auth.RoleAdministrator,
	}); err != nil {
		t.Fatalf("first administrator error = %v", err)
	}
}
