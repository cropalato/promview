package auth

// Password hashing for local accounts.
//
// PBKDF2-HMAC-SHA-256, from the standard library's crypto/pbkdf2.
//
// Argon2id would be the better choice on the merits: it is memory-hard, and
// PBKDF2 is not, so a GPU or an ASIC recovers a PBKDF2 password far faster per
// dollar. That matters if local_credentials is ever stolen.
//
// The standard library has no memory-hard KDF, so Argon2id would mean taking
// golang.org/x/crypto as a direct dependency and compiling it in. It is in the
// module graph already, as something a dependency depends on, but no package
// this binary builds imports it today.
//
// That trade is not obviously worth making at this scale. These are a handful
// of operator accounts, and the attacker holding the credentials table also
// holds every Alertmanager bearer token in `sources` and every live session
// hash from the same database, so cracking a password buys access they already
// have. If that calculus changes, the stored format carries its own algorithm
// name and adding $argon2id$ is an addition rather than a migration.

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// pbkdf2Iterations follows OWASP's 2023 guidance for PBKDF2-HMAC-SHA-256.
	// Raising it is safe at any time: the cost is recorded in each hash and read
	// back when verifying, so existing passwords keep working and are re-hashed
	// at the stronger setting on their owner's next sign-in.
	pbkdf2Iterations = 600_000
	pbkdf2SaltBytes  = 16
	pbkdf2KeyBytes   = 32

	pbkdf2Scheme = "pbkdf2-sha256"

	// passwordMinLength is length only. Composition rules - a digit, a symbol,
	// a capital - reliably produce worse passwords than a longer minimum does.
	passwordMinLength = 12
	// passwordMaxLength bounds work an unauthenticated caller controls. PBKDF2
	// has no bcrypt-style truncation, so without a cap the HMAC cost is theirs
	// to set.
	passwordMaxLength = 1024
)

// ErrInvalidCredentials is the single answer to every failed sign-in.
//
// Unknown username, wrong password, disabled account and locked account all
// return it, and the handler answers all four identically. Distinguishing them
// would tell an unauthenticated caller which accounts exist.
var ErrInvalidCredentials = errors.New("invalid credentials")

// HashPassword derives a password hash in a self-describing encoding.
//
// The iteration count travels with the hash because it is a number that will
// need raising, and a format without it forces a flag-day rehash that cannot be
// done: nobody keeps the plaintexts.
func HashPassword(password string) (string, error) {
	salt := make([]byte, pbkdf2SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyBytes)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"$%s$i=%d$%s$%s",
		pbkdf2Scheme, pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password produces encoded.
//
// An encoding this build does not recognise fails closed rather than being
// treated as a match, and the comparison is constant-time so a near-miss is not
// distinguishable from a wrong first byte.
func VerifyPassword(encoded, password string) bool {
	_, iterations, salt, want, err := parsePasswordHash(encoded)
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NeedsRehash reports whether encoded was produced with weaker settings than
// the current ones, so a successful sign-in can quietly upgrade it.
func NeedsRehash(encoded string) bool {
	scheme, iterations, salt, key, err := parsePasswordHash(encoded)
	if err != nil {
		// Unreadable is not "needs rehashing": nothing can be rehashed without
		// a password that verifies against it first, and VerifyPassword has
		// already refused this one.
		return false
	}
	return scheme != pbkdf2Scheme ||
		iterations < pbkdf2Iterations ||
		len(salt) < pbkdf2SaltBytes ||
		len(key) < pbkdf2KeyBytes
}

// ValidatePassword reports whether a password may be set.
//
// Checked where passwords are chosen, never where they are verified: an
// existing password that no longer meets the policy must still sign its owner
// in, or a tightened rule locks out everyone it applies to at once.
func ValidatePassword(username, password string) error {
	if len(password) < passwordMinLength {
		return fmt.Errorf("password must be at least %d characters", passwordMinLength)
	}
	if len(password) > passwordMaxLength {
		return fmt.Errorf("password must not exceed %d characters", passwordMaxLength)
	}
	if username != "" && strings.EqualFold(username, password) {
		return errors.New("password must not be the username")
	}
	return nil
}

func parsePasswordHash(encoded string) (scheme string, iterations int, salt, key []byte, err error) {
	// Leading empty field from the opening "$", then scheme, cost, salt, key.
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "" {
		return "", 0, nil, nil, errors.New("password hash is malformed")
	}
	scheme = parts[1]
	if scheme != pbkdf2Scheme {
		return "", 0, nil, nil, fmt.Errorf("unsupported password hash scheme %q", scheme)
	}
	cost, found := strings.CutPrefix(parts[2], "i=")
	if !found {
		return "", 0, nil, nil, errors.New("password hash has no iteration count")
	}
	iterations, err = strconv.Atoi(cost)
	if err != nil || iterations < 1 {
		return "", 0, nil, nil, errors.New("password hash iteration count is invalid")
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[3]); err != nil || len(salt) == 0 {
		return "", 0, nil, nil, errors.New("password hash salt is invalid")
	}
	if key, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(key) == 0 {
		return "", 0, nil, nil, errors.New("password hash key is invalid")
	}
	return scheme, iterations, salt, key, nil
}
