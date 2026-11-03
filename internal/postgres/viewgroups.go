package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cropalato/promview/internal/auth"
)

/*
View groups narrow what a user sees by default. They are a convenience, never a
permission: nothing here is read when authorizing, and the selectors a user
belongs to only ever reach the view, which is intersected with scope.
*/

// pinnedToUser is the SQL for "this view group is forced on user $n". It is the
// same subject match role bindings use, evaluated per request, so a directory
// group membership change applies at the next login like authorization does.
func pinnedToUser(groupID, userParam string) string {
	return `EXISTS (
		SELECT 1 FROM view_group_pins AS pin
		WHERE pin.view_group_id = ` + groupID + `
			AND (
				(pin.subject_kind = 'user' AND pin.user_id = ` + userParam + `)
				OR (
					pin.subject_kind IN ('oidc_group', 'ldap_group')
					AND EXISTS (
						SELECT 1
						FROM auth_identities AS identity
						JOIN auth_identity_groups AS membership ON membership.identity_id = identity.id
						WHERE identity.user_id = ` + userParam + `
							AND identity.issuer = pin.subject_issuer
							AND membership.group_name = pin.subject_group
					)
				)
			)
	)`
}

// viewGroupMatchers reads the selectors of the view groups a user belongs to,
// joined or pinned, one conjunction per group.
func viewGroupMatchers(ctx context.Context, database principalQuerier, userID int64) ([][]auth.LabelMatcher, error) {
	rows, err := database.Query(ctx, `
		SELECT view_group.id, matcher.label_name, matcher.operator, matcher.value
		FROM view_groups AS view_group
		JOIN view_group_matchers AS matcher ON matcher.view_group_id = view_group.id
		WHERE EXISTS (
				SELECT 1 FROM view_group_members AS member
				WHERE member.view_group_id = view_group.id AND member.user_id = $1
			)
			OR `+pinnedToUser("view_group.id", "$1")+`
		ORDER BY view_group.id, matcher.ordinal
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query view groups: %w", err)
	}
	defer rows.Close()
	var groups [][]auth.LabelMatcher
	var current int64
	for rows.Next() {
		var id int64
		var matcher auth.LabelMatcher
		if err := rows.Scan(&id, &matcher.Name, &matcher.Operator, &matcher.Value); err != nil {
			return nil, fmt.Errorf("scan view group matcher: %w", err)
		}
		if id != current {
			groups = append(groups, nil)
			current = id
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], matcher)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate view groups: %w", err)
	}
	return groups, nil
}

// ViewGroups lists every view group with the caller's membership state.
// Selectors, pins and member counts are returned to administrators only: a
// selector can name teams or labels the reader has no access to.
func (store *Store) ViewGroups(ctx context.Context, principal auth.Principal) ([]auth.ViewGroupStatus, error) {
	administrator := principal.CanAdminister()
	rows, err := store.pool.Query(ctx, `
		SELECT view_group.id, view_group.name, view_group.description,
			EXISTS (SELECT 1 FROM view_group_members AS member
				WHERE member.view_group_id = view_group.id AND member.user_id = $1),
			`+pinnedToUser("view_group.id", "$1")+`,
			(SELECT count(*) FROM view_group_members AS member WHERE member.view_group_id = view_group.id)
		FROM view_groups AS view_group
		ORDER BY view_group.name
	`, principal.UserID)
	if err != nil {
		return nil, fmt.Errorf("list view groups: %w", err)
	}
	defer rows.Close()
	statuses := []auth.ViewGroupStatus{}
	ids := []int64{}
	for rows.Next() {
		var id int64
		var status auth.ViewGroupStatus
		var members int
		if err := rows.Scan(&id, &status.Name, &status.Description, &status.Joined, &status.Pinned, &members); err != nil {
			return nil, fmt.Errorf("scan view group: %w", err)
		}
		if administrator {
			status.Members = members
		}
		statuses = append(statuses, status)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate view groups: %w", err)
	}
	rows.Close()
	if !administrator || len(statuses) == 0 {
		return statuses, nil
	}

	index := make(map[int64]int, len(ids))
	for position, id := range ids {
		index[id] = position
	}
	matcherRows, err := store.pool.Query(ctx, `
		SELECT view_group_id, label_name, operator, value
		FROM view_group_matchers ORDER BY view_group_id, ordinal
	`)
	if err != nil {
		return nil, fmt.Errorf("list view group matchers: %w", err)
	}
	defer matcherRows.Close()
	for matcherRows.Next() {
		var id int64
		var matcher auth.LabelMatcher
		if err := matcherRows.Scan(&id, &matcher.Name, &matcher.Operator, &matcher.Value); err != nil {
			return nil, fmt.Errorf("scan view group matcher: %w", err)
		}
		statuses[index[id]].Matchers = append(statuses[index[id]].Matchers, matcher)
	}
	if err := matcherRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate view group matchers: %w", err)
	}
	matcherRows.Close()

	pinRows, err := store.pool.Query(ctx, `
		SELECT view_group_id, subject_kind, COALESCE(user_id, 0), COALESCE(subject_issuer, ''), COALESCE(subject_group, '')
		FROM view_group_pins ORDER BY view_group_id, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list view group pins: %w", err)
	}
	defer pinRows.Close()
	for pinRows.Next() {
		var id int64
		var pin auth.ViewGroupPin
		if err := pinRows.Scan(&id, &pin.SubjectKind, &pin.UserID, &pin.SubjectIssuer, &pin.SubjectGroup); err != nil {
			return nil, fmt.Errorf("scan view group pin: %w", err)
		}
		statuses[index[id]].Pins = append(statuses[index[id]].Pins, pin)
	}
	if err := pinRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate view group pins: %w", err)
	}
	return statuses, nil
}

func (store *Store) SetViewGroup(ctx context.Context, group auth.ViewGroup) error {
	if err := auth.ValidateViewGroup(group); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		for _, matcher := range group.Matchers {
			if matcher.Operator == "=~" || matcher.Operator == "!~" {
				if _, err := tx.Exec(ctx, "SELECT '' ~ $1", matcher.Value); err != nil {
					return fmt.Errorf("%w: selector expression is not valid for PostgreSQL", auth.ErrInvalidViewGroup)
				}
			}
		}
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO view_groups (name, description) VALUES ($1, $2)
			ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description, updated_at = now()
			RETURNING id
		`, group.Name, group.Description).Scan(&id); err != nil {
			return fmt.Errorf("set view group %s: %w", group.Name, err)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM view_group_matchers WHERE view_group_id = $1", id); err != nil {
			return fmt.Errorf("replace view group matchers: %w", err)
		}
		for ordinal, matcher := range group.Matchers {
			if _, err := tx.Exec(ctx, `
				INSERT INTO view_group_matchers (view_group_id, ordinal, label_name, operator, value)
				VALUES ($1, $2, $3, $4, $5)
			`, id, ordinal, matcher.Name, matcher.Operator, matcher.Value); err != nil {
				return fmt.Errorf("store view group matcher: %w", err)
			}
		}
		return nil
	})
}

func (store *Store) DeleteViewGroup(ctx context.Context, name string) error {
	tag, err := store.pool.Exec(ctx, "DELETE FROM view_groups WHERE name = $1", name)
	if err != nil {
		return fmt.Errorf("delete view group %s: %w", name, err)
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrViewGroupNotFound
	}
	return nil
}

func viewGroupID(ctx context.Context, tx pgx.Tx, name string) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, "SELECT id FROM view_groups WHERE name = $1", name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, auth.ErrViewGroupNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read view group %s: %w", name, err)
	}
	return id, nil
}

// JoinViewGroup adds the caller to a group. Joining is allowed even when the
// selector falls outside the caller's scope: it then yields nothing, and
// rejecting it would let a user probe which selectors overlap scope they
// cannot see.
func (store *Store) JoinViewGroup(ctx context.Context, userID int64, name string) error {
	return pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		id, err := viewGroupID(ctx, tx, name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO view_group_members (view_group_id, user_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, id, userID); err != nil {
			return fmt.Errorf("join view group %s: %w", name, err)
		}
		return nil
	})
}

// LeaveViewGroup removes only the caller's own membership row. A group pinned
// on the caller cannot be left; the global switch is how a user sees past it.
func (store *Store) LeaveViewGroup(ctx context.Context, userID int64, name string) error {
	return pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		id, err := viewGroupID(ctx, tx, name)
		if err != nil {
			return err
		}
		var pinned bool
		if err := tx.QueryRow(ctx, "SELECT "+pinnedToUser("$1::bigint", "$2::bigint"), id, userID).Scan(&pinned); err != nil {
			return fmt.Errorf("check view group pin: %w", err)
		}
		if pinned {
			return auth.ErrViewGroupPinned
		}
		if _, err := tx.Exec(ctx,
			"DELETE FROM view_group_members WHERE view_group_id = $1 AND user_id = $2", id, userID,
		); err != nil {
			return fmt.Errorf("leave view group %s: %w", name, err)
		}
		return nil
	})
}

func (store *Store) PinViewGroup(ctx context.Context, name string, pin auth.ViewGroupPin) error {
	if err := auth.ValidateViewGroupPin(pin); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		id, err := viewGroupID(ctx, tx, name)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO view_group_pins (view_group_id, subject_kind, user_id, subject_issuer, subject_group)
			VALUES ($1, $2, NULLIF($3, 0), NULLIF($4, ''), NULLIF($5, ''))
			ON CONFLICT DO NOTHING
		`, id, pin.SubjectKind, pin.UserID, pin.SubjectIssuer, pin.SubjectGroup)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return fmt.Errorf("%w: user %d does not exist", auth.ErrInvalidViewGroup, pin.UserID)
		}
		if err != nil {
			return fmt.Errorf("pin view group %s: %w", name, err)
		}
		return nil
	})
}

func (store *Store) UnpinViewGroup(ctx context.Context, name string, pin auth.ViewGroupPin) error {
	if err := auth.ValidateViewGroupPin(pin); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		id, err := viewGroupID(ctx, tx, name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM view_group_pins
			WHERE view_group_id = $1 AND subject_kind = $2
				AND user_id IS NOT DISTINCT FROM NULLIF($3, 0)
				AND subject_issuer IS NOT DISTINCT FROM NULLIF($4, '')
				AND subject_group IS NOT DISTINCT FROM NULLIF($5, '')
		`, id, pin.SubjectKind, pin.UserID, pin.SubjectIssuer, pin.SubjectGroup); err != nil {
			return fmt.Errorf("unpin view group %s: %w", name, err)
		}
		return nil
	})
}
