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

func newLocalTestStore(t *testing.T) (*Store, context.Context) {
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
	return New(pool), ctx
}

const testPassword = "correct horse battery staple"

func TestStoreLocalAccounts(t *testing.T) {
	store, ctx := newLocalTestStore(t)

	userID, err := store.CreateLocalAccount(ctx, LocalAccount{
		Username: "Operator", Email: "operator@example.com", DisplayName: "The Operator",
	}, testPassword)
	if err != nil {
		t.Fatalf("CreateLocalAccount() error = %v", err)
	}

	// The identity row is not bookkeeping: resolvePrincipal builds a subject
	// from it, and an account without one resolves to an empty subject that the
	// console rejects as a malformed session, pointing nowhere near the cause.
	credential, err := store.FindLocalCredential(ctx, "operator")
	if err != nil {
		t.Fatalf("FindLocalCredential() error = %v", err)
	}
	if credential.UserID != userID || credential.Username != "operator" || !credential.Enabled {
		t.Fatalf("credential = %#v", credential)
	}
	if !auth.VerifyPassword(credential.PasswordHash, testPassword) {
		t.Fatal("the stored hash does not verify against the password it was created with")
	}

	// Case-insensitive lookup, matching the partial unique index: Operator and
	// operator must be the same account at both ends.
	if _, err := store.FindLocalCredential(ctx, "OPERATOR"); err != nil {
		t.Fatalf("FindLocalCredential(uppercase) error = %v", err)
	}

	if _, err := store.CreateLocalAccount(ctx, LocalAccount{Username: "operator"}, testPassword); !errors.Is(err, ErrLocalAccountExists) {
		t.Fatalf("duplicate username error = %v, want ErrLocalAccountExists", err)
	}
	if _, err := store.CreateLocalAccount(ctx, LocalAccount{Username: "OPERATOR"}, testPassword); !errors.Is(err, ErrLocalAccountExists) {
		t.Fatalf("duplicate username differing only in case error = %v, want ErrLocalAccountExists", err)
	}

	// A missing account is indistinguishable from a wrong password, so no
	// unauthenticated caller can enumerate who exists.
	if _, err := store.FindLocalCredential(ctx, "nobody"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("unknown username error = %v, want ErrInvalidCredentials", err)
	}
}

func TestStoreLocalAccountPrincipalHasASubject(t *testing.T) {
	store, ctx := newLocalTestStore(t)
	userID, err := store.CreateLocalAccount(ctx, LocalAccount{Username: "operator"}, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRoleBinding(ctx, auth.RoleBinding{
		Name: "operators", SubjectKind: auth.SubjectUser, UserID: userID, Role: auth.RoleOperator,
	}); err != nil {
		t.Fatal(err)
	}
	principal, err := store.ResolveDirectoryIdentity(ctx, auth.DirectoryIdentity{
		Issuer: auth.LocalIssuer, Subject: "1", Username: "operator",
	})
	if err != nil {
		t.Fatalf("ResolveDirectoryIdentity() error = %v", err)
	}
	// An empty subject reaches the console as "Session response was malformed",
	// which is a message pointing nowhere near a missing identity row.
	if principal.Subject == "" {
		t.Fatalf("principal has no subject: %#v", principal)
	}
}

func TestStoreLocalLockoutIsSelfExpiring(t *testing.T) {
	store, ctx := newLocalTestStore(t)
	userID, err := store.CreateLocalAccount(ctx, LocalAccount{Username: "operator"}, testPassword)
	if err != nil {
		t.Fatal(err)
	}

	for attempt := 1; attempt < 5; attempt++ {
		if err := store.RecordLocalLoginFailure(ctx, userID); err != nil {
			t.Fatal(err)
		}
		credential, err := store.FindLocalCredential(ctx, "operator")
		if err != nil {
			t.Fatal(err)
		}
		if !credential.LockedUntil.IsZero() {
			t.Fatalf("locked after %d failures, want a lock only from the fifth", attempt)
		}
	}
	if err := store.RecordLocalLoginFailure(ctx, userID); err != nil {
		t.Fatal(err)
	}
	locked, err := store.FindLocalCredential(ctx, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if locked.LockedUntil.IsZero() || !locked.LockedUntil.After(time.Now().UTC()) {
		t.Fatalf("lockedUntil = %v, want a future time", locked.LockedUntil)
	}
	// Never permanent: a lock that has to be cleared by hand is a denial of
	// service against a named on-call operator, triggerable by anyone who knows
	// their username.
	if wait := time.Until(locked.LockedUntil); wait > lockoutCeiling {
		t.Fatalf("lock lasts %v, want no more than %v", wait, lockoutCeiling)
	}

	// Guessing during a lockout must not extend it, or the backoff is a weapon.
	before := locked.LockedUntil
	if err := store.RecordLocalLoginFailure(ctx, userID); err != nil {
		t.Fatal(err)
	}
	after, err := store.FindLocalCredential(ctx, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if !after.LockedUntil.Equal(before) {
		t.Fatalf("lock moved from %v to %v while already locked", before, after.LockedUntil)
	}

	if err := store.UnlockLocalAccount(ctx, "Operator"); err != nil {
		t.Fatalf("UnlockLocalAccount() error = %v", err)
	}
	unlocked, err := store.FindLocalCredential(ctx, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if !unlocked.LockedUntil.IsZero() {
		t.Fatalf("lockedUntil = %v after an unlock", unlocked.LockedUntil)
	}
}

func TestStoreLocalLoginSuccessClearsBackoffAndRehashes(t *testing.T) {
	store, ctx := newLocalTestStore(t)
	userID, err := store.CreateLocalAccount(ctx, LocalAccount{Username: "operator"}, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	for range 6 {
		if err := store.RecordLocalLoginFailure(ctx, userID); err != nil {
			t.Fatal(err)
		}
	}
	upgraded, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLocalLoginSuccess(ctx, userID, upgraded); err != nil {
		t.Fatalf("RecordLocalLoginSuccess() error = %v", err)
	}
	credential, err := store.FindLocalCredential(ctx, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if !credential.LockedUntil.IsZero() {
		t.Fatalf("lockedUntil = %v after a success", credential.LockedUntil)
	}
	if credential.PasswordHash != upgraded {
		t.Fatal("a rehashed password was not stored")
	}

	// An empty rehash means "no upgrade needed" and must leave the hash alone,
	// not blank it.
	if err := store.RecordLocalLoginSuccess(ctx, userID, ""); err != nil {
		t.Fatal(err)
	}
	unchanged, err := store.FindLocalCredential(ctx, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.PasswordHash != upgraded {
		t.Fatalf("hash = %q after an empty rehash, want it unchanged", unchanged.PasswordHash)
	}
}

func TestStoreLocalAccountAdministration(t *testing.T) {
	store, ctx := newLocalTestStore(t)
	if _, err := store.CreateLocalAccount(ctx, LocalAccount{Username: "operator"}, testPassword); err != nil {
		t.Fatal(err)
	}

	if err := store.SetLocalPassword(ctx, "Operator", "a different long password"); err != nil {
		t.Fatalf("SetLocalPassword() error = %v", err)
	}
	credential, err := store.FindLocalCredential(ctx, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.VerifyPassword(credential.PasswordHash, "a different long password") {
		t.Fatal("the new password does not verify")
	}
	if auth.VerifyPassword(credential.PasswordHash, testPassword) {
		t.Fatal("the old password still verifies")
	}

	// The policy is enforced where passwords are chosen.
	if err := store.SetLocalPassword(ctx, "operator", "short"); err == nil {
		t.Fatal("a password below the minimum was accepted")
	}

	if err := store.SetLocalAccountEnabled(ctx, "operator", false); err != nil {
		t.Fatalf("SetLocalAccountEnabled() error = %v", err)
	}
	disabled, err := store.FindLocalCredential(ctx, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled {
		t.Fatal("the account is still enabled")
	}

	accounts, err := store.ListLocalAccounts(ctx)
	if err != nil {
		t.Fatalf("ListLocalAccounts() error = %v", err)
	}
	// Disabled accounts are listed: an administrator listing accounts is
	// usually looking for exactly those.
	if len(accounts) != 1 || accounts[0].Username != "operator" || accounts[0].Enabled {
		t.Fatalf("accounts = %#v", accounts)
	}

	for name, err := range map[string]error{
		"set password": store.SetLocalPassword(ctx, "nobody", testPassword),
		"unlock":       store.UnlockLocalAccount(ctx, "nobody"),
		"disable":      store.SetLocalAccountEnabled(ctx, "nobody", false),
	} {
		if !errors.Is(err, ErrLocalAccountNotFound) {
			t.Fatalf("%s on a missing account error = %v, want ErrLocalAccountNotFound", name, err)
		}
	}
}

// An LDAP identity flows through the same upsert OIDC uses and binds through
// the same two columns. This is the check that the grants query actually
// matches on the new subject kind rather than only appearing to.
func TestStoreLDAPGroupBindings(t *testing.T) {
	store, ctx := newLocalTestStore(t)

	const directory = "ldaps://dc.example.com:636"
	if err := store.SetRoleBinding(ctx, auth.RoleBinding{
		Name: "directory-operators", SubjectKind: auth.SubjectLDAPGroup,
		SubjectIssuer: directory, SubjectGroup: "promview-operators",
		Role: auth.RoleOperator,
	}); err != nil {
		t.Fatalf("SetRoleBinding() error = %v", err)
	}

	principal, err := store.ResolveDirectoryIdentity(ctx, auth.DirectoryIdentity{
		Issuer: directory, Subject: "0f8fad5b-d9cb-469f-a165-70867728950e",
		Username: "ada", Email: "ada@example.com", DisplayName: "Ada Lovelace",
		Groups: []string{"promview-operators"},
	})
	if err != nil {
		t.Fatalf("ResolveDirectoryIdentity() error = %v", err)
	}
	if !principal.CanOperate() {
		t.Fatalf("an LDAP group binding did not grant its role: %#v", principal)
	}

	// An OIDC issuer string and an LDAP one cannot collide, but the query keys
	// on the subject kind rather than relying on that, so a member of an
	// identically named group at a different directory gets nothing.
	other, err := store.ResolveDirectoryIdentity(ctx, auth.DirectoryIdentity{
		Issuer: "ldaps://other.example.com:636", Subject: "someone-else",
		Username: "bob", Groups: []string{"promview-operators"},
	})
	if !errors.Is(err, auth.ErrAccessDenied) {
		t.Fatalf("a group at another directory resolved to %#v (error %v)", other, err)
	}
}
