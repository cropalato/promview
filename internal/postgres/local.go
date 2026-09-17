package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cropalato/promview/internal/auth"
)

// lockoutBase and lockoutCeiling shape the backoff after repeated failures.
//
// Self-expiring, never permanent. A lockout that has to be cleared by hand is a
// denial of service against a named on-call operator, triggerable by anyone who
// knows their username, at the moment they need to acknowledge a page - which
// is a worse outcome than the brute force it prevents.
const (
	lockoutAfterAttempts = 5
	lockoutBase          = 30 * time.Second
	lockoutCeiling       = 15 * time.Minute
)

// ErrLocalAccountExists is returned when a username is already taken. Reported
// to an administrator at a terminal, never to an unauthenticated caller.
var ErrLocalAccountExists = errors.New("a local account with that username already exists")

// ErrLocalAccountNotFound is returned by the administrative commands. Sign-in
// never sees it: FindLocalCredential answers ErrInvalidCredentials instead, so
// no unauthenticated caller can tell a missing account from a wrong password.
var ErrLocalAccountNotFound = errors.New("no local account with that username")

// LocalAccount is a local account as the CLI creates and lists it.
type LocalAccount struct {
	UserID      int64
	Username    string
	Email       string
	DisplayName string
	Enabled     bool
	LockedUntil time.Time
	LastLoginAt time.Time
}

// CreateLocalAccount writes the user, its identity and its password together.
//
// The auth_identities row is not optional bookkeeping: resolvePrincipal builds
// a principal's subject from it, and an account without one resolves to an
// empty subject that the console rejects as a malformed session - a failure
// that points nowhere near its cause.
func (store *Store) CreateLocalAccount(ctx context.Context, account LocalAccount, password string) (int64, error) {
	username := auth.NormalizeUsername(account.Username)
	if username == "" {
		return 0, errors.New("username is required")
	}
	if err := auth.ValidatePassword(username, password); err != nil {
		return 0, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, err
	}
	displayName := account.DisplayName
	if displayName == "" {
		displayName = username
	}

	var userID int64
	err = pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		var existing int64
		err := tx.QueryRow(ctx, `
			SELECT user_id FROM auth_identities
			WHERE issuer = $1 AND lower(username) = $2
		`, auth.LocalIssuer, username).Scan(&existing)
		if err == nil {
			return ErrLocalAccountExists
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check local account: %w", err)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (email, display_name)
			VALUES ($1, $2)
			RETURNING id
		`, account.Email, displayName).Scan(&userID); err != nil {
			return fmt.Errorf("create local user: %w", err)
		}
		// The subject is the user ID as text, matching what LocalDirectory
		// reports on sign-in. Not the username: renaming an account must not
		// silently produce a second identity with none of the first's bindings.
		if _, err := tx.Exec(ctx, `
			INSERT INTO auth_identities (user_id, issuer, subject, username, email, display_name)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, userID, auth.LocalIssuer, strconv.FormatInt(userID, 10), username, account.Email, displayName); err != nil {
			return fmt.Errorf("create local identity: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO local_credentials (user_id, password_hash)
			VALUES ($1, $2)
		`, userID, hash); err != nil {
			return fmt.Errorf("create local credential: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return userID, nil
}

// FindLocalCredential reads an account for a sign-in attempt.
//
// A missing account is ErrInvalidCredentials, never a distinct "no such user".
// The caller cannot leak a distinction it is never given.
func (store *Store) FindLocalCredential(ctx context.Context, username string) (auth.LocalCredential, error) {
	var credential auth.LocalCredential
	var lockedUntil *time.Time
	err := store.pool.QueryRow(ctx, `
		SELECT identity.user_id, identity.username, identity.email, identity.display_name,
		       credential.password_hash, account.enabled, credential.locked_until
		FROM auth_identities AS identity
		JOIN local_credentials AS credential ON credential.user_id = identity.user_id
		JOIN users AS account ON account.id = identity.user_id
		WHERE identity.issuer = $1 AND lower(identity.username) = $2
	`, auth.LocalIssuer, auth.NormalizeUsername(username)).Scan(
		&credential.UserID, &credential.Username, &credential.Email, &credential.DisplayName,
		&credential.PasswordHash, &credential.Enabled, &lockedUntil,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.LocalCredential{}, auth.ErrInvalidCredentials
	}
	if err != nil {
		return auth.LocalCredential{}, fmt.Errorf("find local credential: %w", err)
	}
	if lockedUntil != nil {
		credential.LockedUntil = lockedUntil.UTC()
	}
	return credential, nil
}

// RecordLocalLoginFailure counts the attempt and extends the backoff.
//
// The lock is only ever extended from its own count, and only while the account
// is not already locked: counting attempts made during a lockout would let
// somebody lengthen an operator's lockout indefinitely by continuing to guess.
func (store *Store) RecordLocalLoginFailure(ctx context.Context, userID int64) error {
	_, err := store.pool.Exec(ctx, `
		UPDATE local_credentials
		SET failed_attempts = failed_attempts + 1,
		    locked_until = CASE
		        WHEN failed_attempts + 1 >= $2
		        THEN now() + LEAST($3 * power(2, failed_attempts + 1 - $2), $4) * interval '1 second'
		        ELSE locked_until
		    END,
		    updated_at = now()
		WHERE user_id = $1 AND (locked_until IS NULL OR locked_until <= now())
	`, userID, lockoutAfterAttempts, int64(lockoutBase/time.Second), int64(lockoutCeiling/time.Second))
	if err != nil {
		return fmt.Errorf("record local login failure: %w", err)
	}
	return nil
}

// RecordLocalLoginSuccess clears the backoff and, when the stored hash was
// produced at weaker settings, replaces it. A rehash that fails to write is not
// a failed sign-in: the password was correct, and the old hash still verifies.
func (store *Store) RecordLocalLoginSuccess(ctx context.Context, userID int64, rehashed string) error {
	return pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE local_credentials
			SET failed_attempts = 0, locked_until = NULL,
			    password_hash = COALESCE(NULLIF($2, ''), password_hash),
			    password_changed_at = CASE WHEN $2 = '' THEN password_changed_at ELSE now() END,
			    updated_at = now()
			WHERE user_id = $1
		`, userID, rehashed); err != nil {
			return fmt.Errorf("record local login success: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE users SET last_login_at = now(), updated_at = now() WHERE id = $1
		`, userID); err != nil {
			return fmt.Errorf("record local login time: %w", err)
		}
		return nil
	})
}

// SetLocalPassword replaces an account's password and clears any backoff, so an
// administrator resetting a password does not hand back a locked account.
func (store *Store) SetLocalPassword(ctx context.Context, username, password string) error {
	normalized := auth.NormalizeUsername(username)
	if err := auth.ValidatePassword(normalized, password); err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	tag, err := store.pool.Exec(ctx, `
		UPDATE local_credentials AS credential
		SET password_hash = $2, password_changed_at = now(),
		    failed_attempts = 0, locked_until = NULL, updated_at = now()
		FROM auth_identities AS identity
		WHERE identity.user_id = credential.user_id
		  AND identity.issuer = $3 AND lower(identity.username) = $1
	`, normalized, hash, auth.LocalIssuer)
	if err != nil {
		return fmt.Errorf("set local password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLocalAccountNotFound
	}
	return nil
}

// UnlockLocalAccount ends a backoff early. The backoff expires on its own, so
// this is for the case where it bit somebody who needed in now.
func (store *Store) UnlockLocalAccount(ctx context.Context, username string) error {
	tag, err := store.pool.Exec(ctx, `
		UPDATE local_credentials AS credential
		SET failed_attempts = 0, locked_until = NULL, updated_at = now()
		FROM auth_identities AS identity
		WHERE identity.user_id = credential.user_id
		  AND identity.issuer = $2 AND lower(identity.username) = $1
	`, auth.NormalizeUsername(username), auth.LocalIssuer)
	if err != nil {
		return fmt.Errorf("unlock local account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLocalAccountNotFound
	}
	return nil
}

// SetLocalAccountEnabled disables or re-enables an account. Disabling is the
// reversible half of removal, and leaves the bindings and the audit trail that
// deleting the user would take with it.
func (store *Store) SetLocalAccountEnabled(ctx context.Context, username string, enabled bool) error {
	tag, err := store.pool.Exec(ctx, `
		UPDATE users AS account
		SET enabled = $2, updated_at = now()
		FROM auth_identities AS identity
		WHERE identity.user_id = account.id
		  AND identity.issuer = $3 AND lower(identity.username) = $1
	`, auth.NormalizeUsername(username), enabled, auth.LocalIssuer)
	if err != nil {
		return fmt.Errorf("set local account enabled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLocalAccountNotFound
	}
	return nil
}

// ListLocalAccounts reports every local account, including the disabled and
// locked ones: an administrator listing accounts is usually looking for exactly
// those.
func (store *Store) ListLocalAccounts(ctx context.Context) ([]LocalAccount, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT identity.user_id, identity.username, identity.email, identity.display_name,
		       account.enabled, credential.locked_until, account.last_login_at
		FROM auth_identities AS identity
		JOIN local_credentials AS credential ON credential.user_id = identity.user_id
		JOIN users AS account ON account.id = identity.user_id
		WHERE identity.issuer = $1
		ORDER BY lower(identity.username)
	`, auth.LocalIssuer)
	if err != nil {
		return nil, fmt.Errorf("list local accounts: %w", err)
	}
	defer rows.Close()
	accounts := make([]LocalAccount, 0)
	for rows.Next() {
		var account LocalAccount
		var lockedUntil, lastLoginAt *time.Time
		if err := rows.Scan(
			&account.UserID, &account.Username, &account.Email, &account.DisplayName,
			&account.Enabled, &lockedUntil, &lastLoginAt,
		); err != nil {
			return nil, fmt.Errorf("scan local account: %w", err)
		}
		if lockedUntil != nil {
			account.LockedUntil = lockedUntil.UTC()
		}
		if lastLoginAt != nil {
			account.LastLoginAt = lastLoginAt.UTC()
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}
