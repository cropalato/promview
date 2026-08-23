package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cropalato/promview/internal/auth"
)

// StoreDesktopCode records a one-time sign-in code, sweeping expired ones on
// the way past so the table cannot grow on abandoned sign-ins.
func (store *Store) StoreDesktopCode(ctx context.Context, code auth.DesktopCode) error {
	err := pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM desktop_auth_codes WHERE expires_at <= now()"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO desktop_auth_codes (code_hash, user_id, expires_at)
			VALUES ($1, $2, $3)
		`, code.CodeHash, code.UserID, code.ExpiresAt)
		return err
	})
	if err != nil {
		return fmt.Errorf("store desktop auth code: %w", err)
	}
	return nil
}

// ConsumeDesktopCode redeems a code and returns whose it was.
//
// The delete is the read: two requests racing to redeem the same code cannot
// both come back with a user, because only one DELETE can match the row.
func (store *Store) ConsumeDesktopCode(ctx context.Context, codeHash []byte, now time.Time) (int64, error) {
	var userID int64
	err := store.pool.QueryRow(ctx, `
		DELETE FROM desktop_auth_codes
		WHERE code_hash = $1 AND expires_at > $2
		RETURNING user_id
	`, codeHash, now).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Unknown, expired and already-redeemed are one answer on purpose;
		// telling them apart would let a caller probe for codes that existed.
		return 0, auth.ErrDesktopCodeInvalid
	}
	if err != nil {
		return 0, fmt.Errorf("consume desktop auth code: %w", err)
	}
	return userID, nil
}
