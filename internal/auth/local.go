package auth

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// LocalIssuer names this deployment's own account store, and is what a local
// identity's rows in auth_identities are keyed by. A constant rather than a
// setting: two deployments' local accounts never meet.
const LocalIssuer = "local"

// LocalCredential is a stored local account as the directory needs to see it.
type LocalCredential struct {
	UserID       int64
	Username     string
	Email        string
	DisplayName  string
	PasswordHash string
	Enabled      bool
	// LockedUntil is zero when the account is not in backoff.
	LockedUntil time.Time
}

// LocalCredentialRepository is the storage a local sign-in needs.
//
// FindLocalCredential must report a missing account as ErrInvalidCredentials
// and nothing more specific; the caller cannot make a distinction it is not
// given, and one made here would reach the wire.
type LocalCredentialRepository interface {
	FindLocalCredential(context.Context, string) (LocalCredential, error)
	RecordLocalLoginFailure(context.Context, int64) error
	RecordLocalLoginSuccess(context.Context, int64, string) error
}

// LocalDirectory verifies a username and password against local_credentials.
type LocalDirectory struct {
	credentials LocalCredentialRepository
	now         func() time.Time
	// verify and hash are fields so a test can observe that a derivation
	// happened without measuring how long it took. Timing assertions are
	// flaky in CI, and the property that matters is "it ran", not "it was
	// slow".
	verify func(encoded, password string) bool
	hash   func(password string) (string, error)
	// decoy is verified against when no account matches, so an unknown username
	// costs the same as a wrong password. Generated from random bytes at
	// construction: a constant in the source would be recognisable by its
	// timing being exactly the decoy path, and would eventually be somebody's
	// password.
	decoy string
}

func NewLocalDirectory(credentials LocalCredentialRepository) (*LocalDirectory, error) {
	decoy, err := HashPassword(randomDecoyPassword())
	if err != nil {
		return nil, err
	}
	return &LocalDirectory{
		credentials: credentials,
		now:         func() time.Time { return time.Now().UTC() },
		verify:      VerifyPassword,
		hash:        HashPassword,
		decoy:       decoy,
	}, nil
}

// NormalizeUsername is how a local handle is compared. Matching the partial
// unique index on auth_identities, so the lookup and the uniqueness rule cannot
// disagree about whether two spellings are the same account.
func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// Verify signs a local account in.
//
// Every refusal is ErrInvalidCredentials and every refusal costs one password
// derivation, whether or not the account exists. A missing account that
// answered faster than a wrong password would be an oracle for which usernames
// are real, and the answer is free to collect.
func (directory *LocalDirectory) Verify(
	ctx context.Context,
	username, password string,
) (DirectoryIdentity, error) {
	credential, err := directory.credentials.FindLocalCredential(ctx, NormalizeUsername(username))
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			directory.verify(directory.decoy, password)
			return DirectoryIdentity{}, ErrInvalidCredentials
		}
		return DirectoryIdentity{}, err
	}

	matched := directory.verify(credential.PasswordHash, password)

	// Checked after the derivation, not instead of it. A disabled or locked
	// account that answered before doing the work would take measurably less
	// time than a live one, which is the same oracle by another route.
	if !credential.Enabled || directory.lockedOut(credential) {
		return DirectoryIdentity{}, ErrInvalidCredentials
	}
	if !matched {
		if err := directory.credentials.RecordLocalLoginFailure(ctx, credential.UserID); err != nil {
			return DirectoryIdentity{}, err
		}
		return DirectoryIdentity{}, ErrInvalidCredentials
	}

	// Raising the iteration count only helps if stored hashes actually move, and
	// a sign-in is the one moment the plaintext is in hand to move them with.
	rehashed := ""
	if NeedsRehash(credential.PasswordHash) {
		if upgraded, err := directory.hash(password); err == nil {
			rehashed = upgraded
		}
	}
	if err := directory.credentials.RecordLocalLoginSuccess(ctx, credential.UserID, rehashed); err != nil {
		return DirectoryIdentity{}, err
	}

	displayName := credential.DisplayName
	if displayName == "" {
		displayName = credential.Username
	}
	return DirectoryIdentity{
		Issuer: LocalIssuer,
		// The user ID, not the username: a renamed account has to stay the same
		// identity, and a handle-keyed subject would silently become a second
		// user with none of the first one's bindings.
		Subject:     localSubject(credential.UserID),
		Username:    credential.Username,
		Email:       credential.Email,
		DisplayName: displayName,
	}, nil
}

func (directory *LocalDirectory) lockedOut(credential LocalCredential) bool {
	return !credential.LockedUntil.IsZero() && credential.LockedUntil.After(directory.now())
}

func localSubject(userID int64) string {
	return strconv.FormatInt(userID, 10)
}

// randomDecoyPassword is never stored and never compared against anything but
// itself; it exists only so the decoy hash is a real one.
func randomDecoyPassword() string {
	token, err := randomToken()
	if err != nil {
		// randomToken fails only if the system source of randomness does, in
		// which case nothing here can be trusted anyway. A fixed fallback would
		// be a known decoy; panicking at construction is louder and safer.
		panic("promview: system randomness unavailable: " + err.Error())
	}
	return token
}
