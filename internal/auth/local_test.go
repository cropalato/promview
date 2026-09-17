package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeLocalCredentials struct {
	credential LocalCredential
	findErr    error

	failures  int
	successes int
	rehashed  string
	writeErr  error
}

func (fake *fakeLocalCredentials) FindLocalCredential(_ context.Context, username string) (LocalCredential, error) {
	if fake.findErr != nil {
		return LocalCredential{}, fake.findErr
	}
	if !strings.EqualFold(username, fake.credential.Username) {
		return LocalCredential{}, ErrInvalidCredentials
	}
	return fake.credential, nil
}

func (fake *fakeLocalCredentials) RecordLocalLoginFailure(context.Context, int64) error {
	fake.failures++
	return fake.writeErr
}

func (fake *fakeLocalCredentials) RecordLocalLoginSuccess(_ context.Context, _ int64, rehashed string) error {
	fake.successes++
	fake.rehashed = rehashed
	return fake.writeErr
}

// testDirectory replaces the real derivation with a counted stub. These are
// tests of the flow, not of PBKDF2, and 600,000 iterations per case would make
// the package's tests minutes long for nothing.
func testDirectory(t *testing.T, credentials LocalCredentialRepository) (*LocalDirectory, *int) {
	t.Helper()
	derivations := 0
	// Built directly rather than through NewLocalDirectory, whose decoy hash
	// costs a real 600,000-iteration derivation per call. That constructor has
	// its own test; everything here is about the flow around the derivation,
	// which is stubbed anyway.
	directory := &LocalDirectory{
		credentials: credentials,
		decoy:       "hash-of:nothing anybody will type",
		verify: func(encoded, password string) bool {
			derivations++
			return encoded == "hash-of:"+password
		},
		hash: func(password string) (string, error) { return "hash-of:" + password, nil },
		now:  func() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) },
	}
	return directory, &derivations
}

// The decoy has to be a real hash, and a different one every time: a constant
// in the source would be recognisable by its timing being exactly the decoy
// path, and would eventually be somebody's password.
func TestNewLocalDirectoryBuildsAVerifiableDecoy(t *testing.T) {
	first, err := NewLocalDirectory(&fakeLocalCredentials{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewLocalDirectory(&fakeLocalCredentials{})
	if err != nil {
		t.Fatal(err)
	}
	if first.decoy == "" || first.decoy == second.decoy {
		t.Fatalf("decoys = %q and %q", first.decoy, second.decoy)
	}
	if NeedsRehash(first.decoy) {
		t.Fatal("the decoy is not a hash at the current settings, so it does not cost what a real one does")
	}
}

func liveCredential() LocalCredential {
	return LocalCredential{
		UserID: 42, Username: "operator", Email: "operator@example.com",
		DisplayName: "The Operator", PasswordHash: "hash-of:correct horse battery staple",
		Enabled: true,
	}
}

func TestLocalVerifySignsInAKnownAccount(t *testing.T) {
	credentials := &fakeLocalCredentials{credential: liveCredential()}
	directory, _ := testDirectory(t, credentials)
	identity, err := directory.Verify(context.Background(), "Operator", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Issuer != LocalIssuer || identity.Subject != "42" {
		t.Fatalf("identity = %#v", identity)
	}
	// The subject is the user ID, so renaming an account keeps its bindings.
	if identity.Username != "operator" || identity.DisplayName != "The Operator" {
		t.Fatalf("identity = %#v", identity)
	}
	if credentials.successes != 1 || credentials.failures != 0 {
		t.Fatalf("successes = %d, failures = %d", credentials.successes, credentials.failures)
	}
}

// An unknown username must cost the same as a wrong password, or the response
// time is a free list of which accounts exist. Asserted by counting the
// derivation rather than timing it: timing assertions are flaky in CI, and
// "it ran" is the property that matters.
func TestLocalVerifyDerivesForAnUnknownUsername(t *testing.T) {
	credentials := &fakeLocalCredentials{credential: liveCredential()}
	directory, derivations := testDirectory(t, credentials)
	_, err := directory.Verify(context.Background(), "nobody", "correct horse battery staple")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error = %v, want ErrInvalidCredentials", err)
	}
	if *derivations != 1 {
		t.Fatalf("derivations = %d, want exactly 1 against the decoy", *derivations)
	}
	// Nothing to record against: there is no account.
	if credentials.failures != 0 {
		t.Fatalf("failures = %d, want 0", credentials.failures)
	}
}

func TestLocalVerifyRefusesAWrongPassword(t *testing.T) {
	credentials := &fakeLocalCredentials{credential: liveCredential()}
	directory, derivations := testDirectory(t, credentials)
	_, err := directory.Verify(context.Background(), "operator", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error = %v, want ErrInvalidCredentials", err)
	}
	if *derivations != 1 || credentials.failures != 1 {
		t.Fatalf("derivations = %d, failures = %d", *derivations, credentials.failures)
	}
}

// Disabled and locked accounts answer identically to a wrong password, and
// still pay for the derivation. An early return would be the same oracle by
// another route.
func TestLocalVerifyRefusesDisabledAndLockedAccountsAtTheSameCost(t *testing.T) {
	locked := liveCredential()
	locked.LockedUntil = time.Date(2026, 9, 17, 12, 5, 0, 0, time.UTC)
	disabled := liveCredential()
	disabled.Enabled = false
	expired := liveCredential()
	expired.LockedUntil = time.Date(2026, 9, 17, 11, 55, 0, 0, time.UTC)

	for name, test := range map[string]struct {
		credential LocalCredential
		wantErr    bool
	}{
		"locked":          {credential: locked, wantErr: true},
		"disabled":        {credential: disabled, wantErr: true},
		"lock has lapsed": {credential: expired},
	} {
		t.Run(name, func(t *testing.T) {
			credentials := &fakeLocalCredentials{credential: test.credential}
			directory, derivations := testDirectory(t, credentials)
			_, err := directory.Verify(context.Background(), "operator", "correct horse battery staple")
			if test.wantErr && !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("error = %v, want ErrInvalidCredentials", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			if *derivations != 1 {
				t.Fatalf("derivations = %d, want 1", *derivations)
			}
		})
	}
}

// A lockout must not be extendable by continuing to guess, so an attempt made
// while locked records nothing.
func TestLocalVerifyDoesNotCountFailuresDuringALockout(t *testing.T) {
	locked := liveCredential()
	locked.LockedUntil = time.Date(2026, 9, 17, 12, 5, 0, 0, time.UTC)
	credentials := &fakeLocalCredentials{credential: locked}
	directory, _ := testDirectory(t, credentials)
	if _, err := directory.Verify(context.Background(), "operator", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error = %v", err)
	}
	if credentials.failures != 0 {
		t.Fatalf("failures = %d, want 0 while locked", credentials.failures)
	}
}

// Raising the iteration count only helps if stored hashes move, and a sign-in
// is the one moment the plaintext is in hand to move them with.
func TestLocalVerifyUpgradesAWeakStoredHash(t *testing.T) {
	credential := liveCredential()
	credential.PasswordHash = "$pbkdf2-sha256$i=1000$c2FsdHNhbHRzYWx0c2FsdA$" +
		"31ltvuXrbs8POP2F4tw9851NSNQCQpm0sCwu2eEvId8"
	credentials := &fakeLocalCredentials{credential: credential}
	directory, _ := testDirectory(t, credentials)
	directory.verify = func(string, string) bool { return true }
	if _, err := directory.Verify(context.Background(), "operator", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if credentials.rehashed == "" {
		t.Fatal("a hash stored at a weaker cost was not upgraded on sign-in")
	}
}

func TestLocalVerifyDoesNotRehashACurrentHash(t *testing.T) {
	credential := liveCredential()
	current, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	credential.PasswordHash = current
	credentials := &fakeLocalCredentials{credential: credential}
	directory, _ := testDirectory(t, credentials)
	directory.verify = func(string, string) bool { return true }
	if _, err := directory.Verify(context.Background(), "operator", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if credentials.rehashed != "" {
		t.Fatalf("rehashed = %q, want empty", credentials.rehashed)
	}
}

// A database that is down is not a wrong password. Reporting it as one would
// send an operator hunting for a typo during an outage.
func TestLocalVerifyReportsStorageFailuresAsThemselves(t *testing.T) {
	broken := errors.New("database is down")
	credentials := &fakeLocalCredentials{findErr: broken}
	directory, _ := testDirectory(t, credentials)
	_, err := directory.Verify(context.Background(), "operator", "correct horse battery staple")
	if !errors.Is(err, broken) {
		t.Fatalf("error = %v, want the storage failure", err)
	}
	if errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("a storage failure was reported as invalid credentials")
	}
}

func TestNormalizeUsername(t *testing.T) {
	for raw, want := range map[string]string{
		"operator":     "operator",
		"Operator":     "operator",
		"  OPERATOR  ": "operator",
		"":             "",
	} {
		if got := NormalizeUsername(raw); got != want {
			t.Fatalf("NormalizeUsername(%q) = %q, want %q", raw, got, want)
		}
	}
}
