package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/preferences"
)

// ReadPreferences returns the user's stored console layout, or the defaults
// when they have never saved one.
func (store *Store) ReadPreferences(ctx context.Context, principal auth.Principal) (preferences.Preferences, error) {
	if principal.Anonymous || principal.UserID == 0 {
		return preferences.Preferences{}, preferences.ErrNoSubject
	}
	var stored []byte
	err := store.pool.QueryRow(ctx, `
		SELECT preferences FROM user_preferences WHERE user_id = $1
	`, principal.UserID).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return preferences.Default(), nil
	}
	if err != nil {
		return preferences.Preferences{}, fmt.Errorf("read preferences for user %d: %w", principal.UserID, err)
	}
	var value preferences.Preferences
	if err := json.Unmarshal(stored, &value); err != nil {
		return preferences.Preferences{}, fmt.Errorf("decode preferences for user %d: %w", principal.UserID, err)
	}
	// A row written by an older console can be missing pieces this one needs;
	// falling back per field keeps the table rendering instead of failing.
	if len(value.Columns) == 0 {
		value.Columns = preferences.Default().Columns
	}
	if value.Density == "" {
		value.Density = preferences.Default().Density
	}
	if value.Theme == "" {
		value.Theme = preferences.Default().Theme
	}
	if value.Notifications.Matchers == nil {
		// A row written before notifications were configurable has no key at
		// all. Falling back to the default selector keeps opting in doing what
		// it did then, rather than silently matching nothing.
		value.Notifications = preferences.Default().Notifications
	}
	return value, nil
}

// WritePreferences replaces the user's stored layout.
func (store *Store) WritePreferences(ctx context.Context, principal auth.Principal, value preferences.Preferences) error {
	if principal.Anonymous || principal.UserID == 0 {
		return preferences.ErrNoSubject
	}
	if err := preferences.Validate(value); err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode preferences: %w", err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO user_preferences (user_id, preferences)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET
			preferences = EXCLUDED.preferences,
			updated_at = now()
	`, principal.UserID, encoded); err != nil {
		return fmt.Errorf("write preferences for user %d: %w", principal.UserID, err)
	}
	return nil
}
