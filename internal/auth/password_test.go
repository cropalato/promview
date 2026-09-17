package auth

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(encoded, "correct horse battery staple") {
		t.Fatalf("a password did not verify against its own hash: %q", encoded)
	}
	if VerifyPassword(encoded, "correct horse battery stapl") {
		t.Fatal("a wrong password verified")
	}
}

// Two hashes of the same password must differ, or the stored table tells an
// attacker which accounts share a password before any cracking starts.
func TestPasswordHashesAreSalted(t *testing.T) {
	first, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two hashes of the same password are identical")
	}
}

// The iteration count travels with the hash precisely so it can be raised. A
// password stored at the old cost has to keep signing its owner in, and be
// reported as due for an upgrade.
func TestPasswordVerifiesAtAnOlderCost(t *testing.T) {
	const weak = "$pbkdf2-sha256$i=1000$c2FsdHNhbHRzYWx0c2FsdA$" +
		"31ltvuXrbs8POP2F4tw9851NSNQCQpm0sCwu2eEvId8"
	if !VerifyPassword(weak, "correct horse battery staple") {
		t.Fatal("a hash at an older iteration count did not verify")
	}
	if !NeedsRehash(weak) {
		t.Fatal("a hash at an older iteration count was not reported for rehashing")
	}
	current, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if NeedsRehash(current) {
		t.Fatal("a hash at the current settings was reported for rehashing")
	}
}

// Anything unreadable must fail closed. A parser that treated a truncated or
// unfamiliar encoding as a match would turn a corrupt row into a skeleton key.
func TestPasswordRejectsMalformedEncodings(t *testing.T) {
	valid, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	for name, encoded := range map[string]string{
		"empty":               "",
		"no leading dollar":   strings.TrimPrefix(valid, "$"),
		"truncated":           valid[:len(valid)-10],
		"missing key":         "$pbkdf2-sha256$i=600000$c2FsdHNhbHRzYWx0c2FsdA",
		"empty key":           "$pbkdf2-sha256$i=600000$c2FsdHNhbHRzYWx0c2FsdA$",
		"empty salt":          "$pbkdf2-sha256$i=600000$$aGVsbG8",
		"no iteration count":  "$pbkdf2-sha256$600000$c2FsdA$aGVsbG8",
		"zero iterations":     "$pbkdf2-sha256$i=0$c2FsdA$aGVsbG8",
		"negative iterations": "$pbkdf2-sha256$i=-1$c2FsdA$aGVsbG8",
		"unknown scheme":      "$argon2id$i=600000$c2FsdA$aGVsbG8",
		"plaintext":           "correct horse battery staple",
		"extra field":         valid + "$extra",
	} {
		t.Run(name, func(t *testing.T) {
			if VerifyPassword(encoded, "correct horse battery staple") {
				t.Fatalf("%q verified", encoded)
			}
			// Nothing unreadable is "due for a rehash" either: a rehash needs a
			// password that verified first, and none of these can produce one.
			if NeedsRehash(encoded) {
				t.Fatalf("%q was reported for rehashing", encoded)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	for name, test := range map[string]struct {
		username, password string
		wantErr            bool
	}{
		"long enough":        {password: "correct horse battery staple"},
		"exactly the floor":  {password: "123456789012"},
		"one short":          {password: "12345678901", wantErr: true},
		"empty":              {wantErr: true},
		"the username":       {username: "operator", password: "operator", wantErr: true},
		"the username cased": {username: "operator", password: "OPERATOR", wantErr: true},
		"contains it":        {username: "operator", password: "operator-and-more"},
		"too long":           {password: strings.Repeat("a", 1025), wantErr: true},
		"at the ceiling":     {password: strings.Repeat("a", 1024)},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidatePassword(test.username, test.password)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidatePassword(%q, len %d) error = %v, wantErr = %v",
					test.username, len(test.password), err, test.wantErr)
			}
		})
	}
}
