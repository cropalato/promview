package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cropalato/promview/internal/postgres"
)

type fakeUserStore struct {
	account  postgres.LocalAccount
	password string
	unlocked string
	enabled  *bool
	accounts []postgres.LocalAccount
	err      error
}

func (fake *fakeUserStore) CreateLocalAccount(_ context.Context, account postgres.LocalAccount, password string) (int64, error) {
	fake.account, fake.password = account, password
	return 7, fake.err
}

func (fake *fakeUserStore) SetLocalPassword(_ context.Context, username, password string) error {
	fake.account.Username, fake.password = username, password
	return fake.err
}

func (fake *fakeUserStore) UnlockLocalAccount(_ context.Context, username string) error {
	fake.unlocked = username
	return fake.err
}

func (fake *fakeUserStore) SetLocalAccountEnabled(_ context.Context, username string, enabled bool) error {
	fake.account.Username, fake.enabled = username, &enabled
	return fake.err
}

func (fake *fakeUserStore) ListLocalAccounts(context.Context) ([]postgres.LocalAccount, error) {
	return fake.accounts, fake.err
}

func runUser(t *testing.T, store userStore, stdin string, args ...string) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	err := runUserCommand(context.Background(), store, strings.NewReader(stdin), &stdout, args)
	return stdout.String(), err
}

func TestUserCreateReadsThePasswordFromStdin(t *testing.T) {
	store := &fakeUserStore{}
	stdout, err := runUser(t, store, "correct horse battery staple\n",
		"create", "--username", "operator", "--email", "operator@example.com", "--password-stdin")
	if err != nil {
		t.Fatal(err)
	}
	if store.account.Username != "operator" || store.password != "correct horse battery staple" {
		t.Fatalf("account = %#v, password = %q", store.account, store.password)
	}
	// A new account can read nothing until it is bound, and an administrator
	// who is not told that reports the account as broken.
	if !strings.Contains(stdout, "--user-id 7") {
		t.Fatalf("stdout = %q, want the binding command", stdout)
	}
}

// Only the trailing newline is stripped. A password may legitimately begin or
// end with a space, and trimming it would set something other than what was
// typed while reporting success.
func TestUserCreateKeepsSignificantWhitespace(t *testing.T) {
	store := &fakeUserStore{}
	if _, err := runUser(t, store, "  a password with spaces  \r\n",
		"create", "--username", "operator", "--password-stdin"); err != nil {
		t.Fatal(err)
	}
	if store.password != "  a password with spaces  " {
		t.Fatalf("password = %q", store.password)
	}
}

// argv is world-readable through /proc and lands in shell history, so there is
// no flag that takes a password.
func TestUserCreateHasNoPasswordFlag(t *testing.T) {
	store := &fakeUserStore{}
	_, err := runUser(t, store, "", "create", "--username", "operator", "--password", "correct horse battery staple")
	if err == nil {
		t.Fatal("--password was accepted")
	}
	if store.password != "" {
		t.Fatalf("a password reached the store as %q", store.password)
	}
}

func TestUserCreateRefusesWithNoPasswordSource(t *testing.T) {
	if _, err := runUser(t, &fakeUserStore{}, "", "create", "--username", "operator"); err == nil {
		t.Fatal("an account was created with no password source")
	}
}

func TestUserCreateReadsThePasswordFromTheEnvironment(t *testing.T) {
	t.Setenv("PROMVIEW_LOCAL_PASSWORD", "correct horse battery staple")
	store := &fakeUserStore{}
	if _, err := runUser(t, store, "", "create", "--username", "operator"); err != nil {
		t.Fatal(err)
	}
	if store.password != "correct horse battery staple" {
		t.Fatalf("password = %q", store.password)
	}
}

func TestUserEnableAndDisable(t *testing.T) {
	for command, want := range map[string]bool{"enable": true, "disable": false} {
		store := &fakeUserStore{}
		if _, err := runUser(t, store, "", command, "--username", "operator"); err != nil {
			t.Fatal(err)
		}
		if store.enabled == nil || *store.enabled != want {
			t.Fatalf("%s set enabled = %v, want %v", command, store.enabled, want)
		}
	}
}

func TestUserUnlock(t *testing.T) {
	store := &fakeUserStore{}
	if _, err := runUser(t, store, "", "unlock", "--username", "Operator"); err != nil {
		t.Fatal(err)
	}
	if store.unlocked != "Operator" {
		t.Fatalf("unlocked = %q", store.unlocked)
	}
}

// Disabled and locked are the two reasons somebody cannot sign in, and finding
// out which is usually why this command is being run.
func TestUserListShowsWhyAnAccountCannotSignIn(t *testing.T) {
	store := &fakeUserStore{accounts: []postgres.LocalAccount{
		{UserID: 1, Username: "live", Enabled: true, LastLoginAt: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)},
		{UserID: 2, Username: "off", Enabled: false},
		{UserID: 3, Username: "locked", Enabled: true, LockedUntil: time.Now().UTC().Add(time.Hour)},
	}}
	stdout, err := runUser(t, store, "", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"enabled", "disabled", "locked until", "never", "2026-09-17T12:00:00Z"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestUserRejectsAnUnknownCommand(t *testing.T) {
	if _, err := runUser(t, &fakeUserStore{}, "", "delete", "--username", "operator"); err == nil {
		t.Fatal("an unknown command was accepted")
	}
	if _, err := runUser(t, &fakeUserStore{}, ""); err == nil {
		t.Fatal("no command was accepted")
	}
}

func TestUserReportsStoreFailures(t *testing.T) {
	broken := errors.New("database is down")
	store := &fakeUserStore{err: broken}
	if _, err := runUser(t, store, "", "unlock", "--username", "operator"); !errors.Is(err, broken) {
		t.Fatalf("error = %v, want the store's", err)
	}
}
