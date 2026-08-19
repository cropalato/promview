package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
	"github.com/cropalato/promview/internal/sources"
)

type Store struct {
	pool *pgxpool.Pool
}

type alertSort struct {
	expression string
	cursorType string
}

// severityRank orders severities for sorting and for picking a group's worst
// member. Anything outside the three known buckets ranks below info, matching
// how the console renders unknown severities.
const severityRank = "CASE COALESCE(alert.labels->>'severity', 'warning') " +
	"WHEN 'critical' THEN 3 WHEN 'warning' THEN 2 WHEN 'info' THEN 1 ELSE 0 END"

var alertSorts = map[string]alertSort{
	"lastSeen":  {expression: "alert.last_seen", cursorType: "timestamptz"},
	"startsAt":  {expression: "alert.starts_at", cursorType: "timestamptz"},
	"severity":  {expression: severityRank, cursorType: "integer"},
	"alertname": {expression: "COALESCE(alert.labels->>'alertname', '')", cursorType: "text"},
	"summary":   {expression: "COALESCE(alert.annotations->>'summary', '')", cursorType: "text"},
	"status":    {expression: "alert.source_status", cursorType: "text"},
	"team":      {expression: "COALESCE(alert.labels->>'team', '')", cursorType: "text"},
	"instance":  {expression: "COALESCE(alert.labels->>'instance', '')", cursorType: "text"},
	"source":    {expression: "alert.source_slug", cursorType: "text"},
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (store *Store) Ping(ctx context.Context) error {
	return store.pool.Ping(ctx)
}

func (store *Store) SetSource(ctx context.Context, source sources.Source, rawToken string) error {
	if err := sources.Validate(source, rawToken); err != nil {
		return err
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO alert_sources (slug, name, token_hash, enabled, stale_after, alertmanager_url)
		VALUES ($1, $2, $3, true, $4, COALESCE($5, ''))
		ON CONFLICT (slug) DO UPDATE SET
			name = EXCLUDED.name,
			token_hash = EXCLUDED.token_hash,
			enabled = true,
			stale_after = COALESCE(EXCLUDED.stale_after, alert_sources.stale_after),
			alertmanager_url = COALESCE($5, alert_sources.alertmanager_url),
			updated_at = now()
	`, source.Slug, source.Name, sources.HashToken(rawToken), staleAfterInterval(source.StaleAfter), source.AlertmanagerURL)
	if err != nil {
		return fmt.Errorf("set alert source %s: %w", source.Slug, err)
	}
	return nil
}

// UpdateSource changes how an existing source is read without touching its
// credentials, so adding an Alertmanager URL never risks the token the source
// authenticates its deliveries with.
func (store *Store) UpdateSource(ctx context.Context, slug string, patch sources.Patch) error {
	if err := sources.ValidatePatch(patch); err != nil {
		return err
	}
	tag, err := store.pool.Exec(ctx, `
		UPDATE alert_sources SET
			name = COALESCE($2, name),
			stale_after = COALESCE($3, stale_after),
			alertmanager_url = COALESCE($4, alertmanager_url),
			updated_at = now()
		WHERE slug = $1
	`, slug, patch.Name, staleAfterInterval(patch.StaleAfter), patch.AlertmanagerURL)
	if err != nil {
		return fmt.Errorf("update alert source %s: %w", slug, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("alert source %s does not exist", slug)
	}
	return nil
}

func (store *Store) BootstrapSource(ctx context.Context, source sources.Source, rawToken string) error {
	if err := sources.Validate(source, rawToken); err != nil {
		return err
	}
	_, err := store.pool.Exec(ctx, `
		INSERT INTO alert_sources (slug, name, token_hash, enabled)
		VALUES ($1, $2, $3, true)
		ON CONFLICT (slug) DO UPDATE SET
			name = EXCLUDED.name,
			token_hash = EXCLUDED.token_hash,
			enabled = true,
			updated_at = now()
		WHERE alert_sources.token_hash IS NULL
	`, source.Slug, source.Name, sources.HashToken(rawToken))
	if err != nil {
		return fmt.Errorf("bootstrap alert source %s: %w", source.Slug, err)
	}
	return nil
}

func (store *Store) AuthenticateSource(ctx context.Context, slug, rawToken string) (bool, error) {
	var expected []byte
	err := store.pool.QueryRow(ctx, `
		SELECT token_hash
		FROM alert_sources
		WHERE slug = $1 AND enabled AND token_hash IS NOT NULL
	`, slug).Scan(&expected)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("authenticate alert source %s: %w", slug, err)
	}
	provided := sources.HashToken(rawToken)
	return subtle.ConstantTimeCompare(provided, expected) == 1, nil
}

func (store *Store) StoreSession(ctx context.Context, session auth.Session) error {
	_, err := store.pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES ($1, $2, $3)
	`, session.TokenHash, session.UserID, session.ExpiresAt)
	if err != nil {
		return fmt.Errorf("store session: %w", err)
	}
	return nil
}

func (store *Store) FindSession(ctx context.Context, tokenHash []byte, now time.Time) (auth.Session, error) {
	var session auth.Session
	err := store.pool.QueryRow(ctx, `
		UPDATE sessions SET last_seen_at = $2
		WHERE token_hash = $1 AND expires_at > $2
		RETURNING token_hash, user_id, expires_at
	`, tokenHash, now).Scan(
		&session.TokenHash, &session.UserID, &session.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Session{}, auth.ErrUnauthenticated
	}
	if err != nil {
		return auth.Session{}, fmt.Errorf("find session: %w", err)
	}
	session.Principal, err = resolvePrincipal(ctx, store.pool, session.UserID)
	if errors.Is(err, auth.ErrAccessDenied) {
		return auth.Session{}, auth.ErrUnauthenticated
	}
	if err != nil {
		return auth.Session{}, fmt.Errorf("resolve session principal: %w", err)
	}
	return session, nil
}

func (store *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := store.pool.Exec(ctx, "DELETE FROM sessions WHERE token_hash = $1", tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (store *Store) StoreOIDCTransaction(ctx context.Context, transaction auth.OIDCTransaction) error {
	err := pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM oidc_login_transactions WHERE expires_at <= now()"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO oidc_login_transactions (state_hash, nonce, code_verifier, expires_at)
			VALUES ($1, $2, $3, $4)
		`, transaction.StateHash, transaction.Nonce, transaction.CodeVerifier, transaction.ExpiresAt)
		return err
	})
	if err != nil {
		return fmt.Errorf("store OIDC login transaction: %w", err)
	}
	return nil
}

func (store *Store) ConsumeOIDCTransaction(ctx context.Context, stateHash []byte, now time.Time) (auth.OIDCTransaction, error) {
	var transaction auth.OIDCTransaction
	err := store.pool.QueryRow(ctx, `
		DELETE FROM oidc_login_transactions
		WHERE state_hash = $1 AND expires_at > $2
		RETURNING state_hash, nonce, code_verifier, expires_at
	`, stateHash, now).Scan(
		&transaction.StateHash, &transaction.Nonce, &transaction.CodeVerifier, &transaction.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.OIDCTransaction{}, auth.ErrInvalidOIDCTransaction
	}
	if err != nil {
		return auth.OIDCTransaction{}, fmt.Errorf("consume OIDC login transaction: %w", err)
	}
	return transaction, nil
}

func (store *Store) ResolveOIDCIdentity(ctx context.Context, identity auth.OIDCIdentity) (auth.Principal, error) {
	if identity.Issuer == "" || identity.Subject == "" {
		return auth.Principal{}, errors.New("OIDC issuer and subject are required")
	}
	displayName := identity.DisplayName
	if displayName == "" {
		displayName = identity.Username
	}
	if displayName == "" {
		displayName = identity.Email
	}
	if displayName == "" {
		displayName = identity.Subject
	}
	var userID int64
	err := pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		identityKey := identity.Issuer + "\x1f" + identity.Subject
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", identityKey); err != nil {
			return fmt.Errorf("lock OIDC identity: %w", err)
		}

		var identityID int64
		err := tx.QueryRow(ctx, `
			SELECT id, user_id
			FROM auth_identities
			WHERE issuer = $1 AND subject = $2
			FOR UPDATE
		`, identity.Issuer, identity.Subject).Scan(&identityID, &userID)
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.QueryRow(ctx, `
				INSERT INTO users (email, display_name, last_login_at)
				VALUES ($1, $2, now())
				RETURNING id
			`, identity.Email, displayName).Scan(&userID); err != nil {
				return fmt.Errorf("create OIDC user: %w", err)
			}
			if err := tx.QueryRow(ctx, `
				INSERT INTO auth_identities (user_id, issuer, subject, username, email, display_name)
				VALUES ($1, $2, $3, $4, $5, $6)
				RETURNING id
			`, userID, identity.Issuer, identity.Subject, identity.Username, identity.Email, displayName).Scan(&identityID); err != nil {
				return fmt.Errorf("create OIDC identity: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("find OIDC identity: %w", err)
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE users
				SET email = $2, display_name = $3, last_login_at = now(), updated_at = now()
				WHERE id = $1
			`, userID, identity.Email, displayName); err != nil {
				return fmt.Errorf("update OIDC user: %w", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE auth_identities
				SET username = $2, email = $3, display_name = $4, last_seen_at = now()
				WHERE id = $1
			`, identityID, identity.Username, identity.Email, displayName); err != nil {
				return fmt.Errorf("update OIDC identity: %w", err)
			}
		}

		if _, err := tx.Exec(ctx, "DELETE FROM auth_identity_groups WHERE identity_id = $1", identityID); err != nil {
			return fmt.Errorf("replace OIDC groups: %w", err)
		}
		for _, group := range identity.Groups {
			if group == "" {
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO auth_identity_groups (identity_id, group_name)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, identityID, group); err != nil {
				return fmt.Errorf("store OIDC group: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return auth.Principal{}, err
	}
	return resolvePrincipal(ctx, store.pool, userID)
}

func (store *Store) SetRoleBinding(ctx context.Context, binding auth.RoleBinding) error {
	if err := auth.ValidateRoleBinding(binding); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		for _, matcher := range binding.Matchers {
			if matcher.Operator == "=~" || matcher.Operator == "!~" {
				if _, err := tx.Exec(ctx, "SELECT '' ~ $1", matcher.Value); err != nil {
					return fmt.Errorf("validate PostgreSQL selector expression: %w", err)
				}
			}
		}
		var bindingID int64
		err := tx.QueryRow(ctx, `
			INSERT INTO role_bindings (name, subject_kind, user_id, oidc_issuer, oidc_group, role)
			VALUES ($1, $2, NULLIF($3, 0), NULLIF($4, ''), NULLIF($5, ''), $6)
			ON CONFLICT (name) DO UPDATE SET
				subject_kind = EXCLUDED.subject_kind,
				user_id = EXCLUDED.user_id,
				oidc_issuer = EXCLUDED.oidc_issuer,
				oidc_group = EXCLUDED.oidc_group,
				role = EXCLUDED.role,
				updated_at = now()
			RETURNING id
		`, binding.Name, binding.SubjectKind, binding.UserID, binding.OIDCIssuer, binding.OIDCGroup, binding.Role).Scan(&bindingID)
		if err != nil {
			return fmt.Errorf("set role binding %s: %w", binding.Name, err)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM role_binding_matchers WHERE role_binding_id = $1", bindingID); err != nil {
			return fmt.Errorf("replace role binding matchers: %w", err)
		}
		for ordinal, matcher := range binding.Matchers {
			if _, err := tx.Exec(ctx, `
				INSERT INTO role_binding_matchers (role_binding_id, ordinal, label_name, operator, value)
				VALUES ($1, $2, $3, $4, $5)
			`, bindingID, ordinal, matcher.Name, matcher.Operator, matcher.Value); err != nil {
				return fmt.Errorf("store role binding matcher: %w", err)
			}
		}
		return nil
	})
}

func (store *Store) DeleteRoleBinding(ctx context.Context, name string) error {
	if _, err := store.pool.Exec(ctx, "DELETE FROM role_bindings WHERE name = $1", name); err != nil {
		return fmt.Errorf("delete role binding %s: %w", name, err)
	}
	return nil
}

func (store *Store) AuthorizationDiagnostics(ctx context.Context) (auth.AuthorizationDiagnostics, error) {
	diagnostics := auth.AuthorizationDiagnostics{}
	rows, err := store.pool.Query(ctx, `
		SELECT identity.id, identity.user_id, identity.issuer, identity.subject, identity.username, identity.email,
			identity.display_name, identity.last_seen_at, membership.group_name
		FROM auth_identities AS identity
		LEFT JOIN auth_identity_groups AS membership ON membership.identity_id = identity.id
		ORDER BY identity.user_id, identity.id, membership.group_name
	`)
	if err != nil {
		return diagnostics, fmt.Errorf("query OIDC identities: %w", err)
	}
	defer rows.Close()
	var currentIdentityID int64
	for rows.Next() {
		var identityID int64
		var identity auth.OIDCIdentityDiagnostic
		var group *string
		if err := rows.Scan(&identityID, &identity.UserID, &identity.Issuer, &identity.Subject, &identity.Username, &identity.Email, &identity.DisplayName, &identity.LastSeenAt, &group); err != nil {
			return diagnostics, fmt.Errorf("scan OIDC identity: %w", err)
		}
		if identityID != currentIdentityID {
			diagnostics.Identities = append(diagnostics.Identities, identity)
			currentIdentityID = identityID
		}
		if group != nil {
			last := len(diagnostics.Identities) - 1
			diagnostics.Identities[last].Groups = append(diagnostics.Identities[last].Groups, *group)
		}
	}
	if err := rows.Err(); err != nil {
		return diagnostics, fmt.Errorf("iterate OIDC identities: %w", err)
	}

	rows, err = store.pool.Query(ctx, `
		SELECT name, subject_kind, COALESCE(user_id, 0), COALESCE(oidc_issuer, ''), COALESCE(oidc_group, ''), role
		FROM role_bindings
		ORDER BY name
	`)
	if err != nil {
		return diagnostics, fmt.Errorf("query role bindings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var binding auth.RoleBinding
		if err := rows.Scan(&binding.Name, &binding.SubjectKind, &binding.UserID, &binding.OIDCIssuer, &binding.OIDCGroup, &binding.Role); err != nil {
			return diagnostics, fmt.Errorf("scan role binding: %w", err)
		}
		diagnostics.Bindings = append(diagnostics.Bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return diagnostics, fmt.Errorf("iterate role bindings: %w", err)
	}
	return diagnostics, nil
}

type principalQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func resolvePrincipal(ctx context.Context, database principalQuerier, userID int64) (auth.Principal, error) {
	var principal auth.Principal
	var enabled bool
	err := database.QueryRow(ctx, `
		SELECT u.id, COALESCE(identity.issuer || '|' || identity.subject, ''), u.email, u.display_name, u.enabled
		FROM users AS u
		LEFT JOIN LATERAL (
			SELECT issuer, subject
			FROM auth_identities
			WHERE user_id = u.id
			ORDER BY last_seen_at DESC, id DESC
			LIMIT 1
		) AS identity ON true
		WHERE u.id = $1
	`, userID).Scan(&principal.UserID, &principal.Subject, &principal.Email, &principal.DisplayName, &enabled)
	if errors.Is(err, pgx.ErrNoRows) || !enabled {
		return auth.Principal{}, auth.ErrAccessDenied
	}
	if err != nil {
		return auth.Principal{}, fmt.Errorf("read principal: %w", err)
	}

	rows, err := database.Query(ctx, `
		SELECT binding.id, binding.role, matcher.label_name, matcher.operator, matcher.value
		FROM role_bindings AS binding
		LEFT JOIN role_binding_matchers AS matcher ON matcher.role_binding_id = binding.id
		WHERE binding.user_id = $1
			OR EXISTS (
				SELECT 1
				FROM auth_identities AS identity
				JOIN auth_identity_groups AS membership ON membership.identity_id = identity.id
				WHERE identity.user_id = $1
					AND identity.issuer = binding.oidc_issuer
					AND membership.group_name = binding.oidc_group
			)
		ORDER BY binding.id, matcher.ordinal
	`, userID)
	if err != nil {
		return auth.Principal{}, fmt.Errorf("query principal grants: %w", err)
	}
	defer rows.Close()
	var currentID int64
	for rows.Next() {
		var bindingID int64
		var role auth.Role
		var name, operator, value *string
		if err := rows.Scan(&bindingID, &role, &name, &operator, &value); err != nil {
			return auth.Principal{}, fmt.Errorf("scan principal grant: %w", err)
		}
		if bindingID != currentID {
			principal.Grants = append(principal.Grants, auth.Grant{Role: role})
			currentID = bindingID
		}
		if name != nil {
			last := len(principal.Grants) - 1
			principal.Grants[last].Matchers = append(principal.Grants[last].Matchers, auth.LabelMatcher{
				Name: *name, Operator: *operator, Value: *value,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return auth.Principal{}, fmt.Errorf("iterate principal grants: %w", err)
	}
	principal.Roles = auth.RolesFromGrants(principal.Grants)
	if !principal.CanRead() {
		return auth.Principal{}, auth.ErrAccessDenied
	}
	return principal, nil
}

func (store *Store) Ingest(ctx context.Context, alerts []alertmanager.IncomingAlert) error {
	return pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		for _, alert := range alerts {
			labels, err := json.Marshal(alert.Labels)
			if err != nil {
				return fmt.Errorf("marshal labels: %w", err)
			}
			annotations, err := json.Marshal(alert.Annotations)
			if err != nil {
				return fmt.Errorf("marshal annotations: %w", err)
			}
			rawData := alert.RawData
			if len(rawData) == 0 {
				rawData = json.RawMessage(`{}`)
			}

			identity := alert.SourceSlug + "/" + alert.Fingerprint
			if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", identity); err != nil {
				return fmt.Errorf("lock alert %s/%s: %w", alert.SourceSlug, alert.Fingerprint, err)
			}

			var id int64
			var previousStatus string
			var previousLabelsJSON []byte
			var previousAnnotationsJSON []byte
			var previousOccurrence int
			err = tx.QueryRow(ctx, `
				SELECT id, source_status, labels, annotations, occurrence
				FROM alerts
				WHERE source_slug = $1 AND fingerprint = $2
				FOR UPDATE
			`, alert.SourceSlug, alert.Fingerprint).Scan(
				&id, &previousStatus, &previousLabelsJSON, &previousAnnotationsJSON, &previousOccurrence,
			)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				err = tx.QueryRow(ctx, `
					INSERT INTO alerts (
						source_slug, fingerprint, source_status, labels, annotations,
						starts_at, ends_at, generator_url, external_url, first_seen, last_seen, raw_data
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10, $11)
					RETURNING id
				`, alert.SourceSlug, alert.Fingerprint, alert.Status, labels, annotations,
					alert.StartsAt, nullableTime(alert.EndsAt), alert.GeneratorURL, alert.ExternalURL, alert.ReceivedAt, rawData,
				).Scan(&id)
				if err != nil {
					return fmt.Errorf("insert alert %s/%s: %w", alert.SourceSlug, alert.Fingerprint, err)
				}
				if err := insertStreamEvent(ctx, tx, "alert.created", id, alert, nil); err != nil {
					return err
				}
				if err := insertHistoryEvent(ctx, tx, id, 1, "alert.created", alert.Status, alert.ReceivedAt); err != nil {
					return err
				}
			case err != nil:
				return fmt.Errorf("read alert %s/%s: %w", alert.SourceSlug, alert.Fingerprint, err)
			default:
				occurrence := previousOccurrence
				historyType := "alert.updated"
				if previousStatus == "resolved" && alert.Status == "firing" {
					occurrence++
					historyType = "alert.reopened"
				} else if alert.Status == "resolved" && previousStatus != "resolved" {
					historyType = "alert.resolved"
				}
				_, err = tx.Exec(ctx, `
					UPDATE alerts SET
						source_status = $3,
						labels = $4,
						annotations = $5,
						starts_at = $6,
						ends_at = $7,
						generator_url = $8,
						external_url = $9,
						last_seen = $10,
						repeat_count = repeat_count + 1,
						occurrence = $11,
						raw_data = $12,
						acknowledged = CASE WHEN source_status = 'resolved' AND $3 = 'firing' THEN false ELSE acknowledged END,
						acknowledged_at = CASE WHEN source_status = 'resolved' AND $3 = 'firing' THEN NULL ELSE acknowledged_at END,
						acknowledged_by = CASE WHEN source_status = 'resolved' AND $3 = 'firing' THEN '' ELSE acknowledged_by END
					WHERE source_slug = $1 AND fingerprint = $2
				`, alert.SourceSlug, alert.Fingerprint, alert.Status, labels, annotations,
					alert.StartsAt, nullableTime(alert.EndsAt), alert.GeneratorURL, alert.ExternalURL, alert.ReceivedAt,
					occurrence, rawData)
				if err != nil {
					return fmt.Errorf("update alert %s/%s: %w", alert.SourceSlug, alert.Fingerprint, err)
				}

				changed, err := alertMateriallyChanged(
					previousStatus, previousLabelsJSON, previousAnnotationsJSON, alert,
				)
				if err != nil {
					return fmt.Errorf("compare alert %s/%s: %w", alert.SourceSlug, alert.Fingerprint, err)
				}
				if changed {
					streamType := "alert.updated"
					if historyType == "alert.resolved" {
						streamType = "alert.resolved"
					}
					if err := insertStreamEvent(ctx, tx, streamType, id, alert, previousLabelsJSON); err != nil {
						return err
					}
					if err := insertHistoryEvent(ctx, tx, id, occurrence, historyType, alert.Status, alert.ReceivedAt); err != nil {
						return err
					}
				}
			}
		}
		if len(alerts) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE alert_sources
				SET last_delivery_at = $2, updated_at = now()
				WHERE slug = $1
			`, alerts[0].SourceSlug, alerts[0].ReceivedAt); err != nil {
				return fmt.Errorf("update source delivery time: %w", err)
			}
		}
		return nil
	})
}

func (store *Store) ListAlerts(ctx context.Context, principal auth.Principal, query alerts.Query) (alerts.ListResult, error) {
	if query.Sort == "" {
		query.Sort = alerts.DefaultSort
	}
	if query.Order == "" {
		query.Order = alerts.DefaultOrder
	}
	sort, ok := alertSorts[query.Sort]
	if !ok || (query.Order != "asc" && query.Order != "desc") {
		return alerts.ListResult{}, errors.New("invalid alert sort")
	}
	streamCursor, err := store.readStreamCursor(ctx, principal)
	if err != nil {
		return alerts.ListResult{}, err
	}
	where, args := alertFilters(principal, query, "alert")
	counts, total, err := store.readSeverityCounts(ctx, where, args)
	if err != nil {
		return alerts.ListResult{}, err
	}

	listWhere := where
	listArgs := append([]any(nil), args...)
	if query.Cursor != nil {
		listArgs = append(listArgs, query.Cursor.Value, query.Cursor.ID)
		comparison := ">"
		if query.Order == "desc" {
			comparison = "<"
		}
		listWhere = appendCondition(listWhere, fmt.Sprintf("(%s, alert.id) %s ($%d::%s, $%d)", sort.expression, comparison, len(listArgs)-1, sort.cursorType, len(listArgs)))
	}
	listArgs = append(listArgs, query.Limit+1)
	listSQL := `
		SELECT alert.id, alert.source_slug, alert.fingerprint, alert.source_status, alert.labels, alert.annotations,
		       alert.starts_at, alert.ends_at, alert.generator_url, alert.external_url, alert.first_seen, alert.last_seen, alert.repeat_count,
		       alert.occurrence, alert.acknowledged, alert.suppressed, alert.acknowledged_at, alert.acknowledged_by, alert.raw_data
		FROM alerts AS alert` + listWhere + fmt.Sprintf(`
		ORDER BY `+sort.expression+" "+strings.ToUpper(query.Order)+`, alert.id `+strings.ToUpper(query.Order)+`
		LIMIT $%d`, len(listArgs))

	rows, err := store.pool.Query(ctx, listSQL, listArgs...)
	if err != nil {
		return alerts.ListResult{}, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()

	items := make([]alerts.Alert, 0, query.Limit+1)
	for rows.Next() {
		var item alerts.Alert
		var labelsJSON []byte
		var annotationsJSON []byte
		if err := rows.Scan(
			&item.ID, &item.SourceSlug, &item.Fingerprint, &item.SourceStatus,
			&labelsJSON, &annotationsJSON, &item.StartsAt, &item.EndsAt,
			&item.GeneratorURL, &item.ExternalURL, &item.FirstSeen, &item.LastSeen, &item.RepeatCount,
			&item.Occurrence, &item.Acknowledged, &item.Suppressed, &item.AcknowledgedAt, &item.AcknowledgedBy, &item.RawData,
		); err != nil {
			return alerts.ListResult{}, fmt.Errorf("scan alert: %w", err)
		}
		if err := json.Unmarshal(labelsJSON, &item.Labels); err != nil {
			return alerts.ListResult{}, fmt.Errorf("decode labels for alert %d: %w", item.ID, err)
		}
		if err := json.Unmarshal(annotationsJSON, &item.Annotations); err != nil {
			return alerts.ListResult{}, fmt.Errorf("decode annotations for alert %d: %w", item.ID, err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return alerts.ListResult{}, fmt.Errorf("iterate alerts: %w", err)
	}

	var next *alerts.Cursor
	if len(items) > query.Limit {
		items = items[:query.Limit]
		last := items[len(items)-1]
		next = &alerts.Cursor{
			LastSeen: last.LastSeen,
			ID:       last.ID,
			Sort:     query.Sort,
			Order:    query.Order,
			Query:    query.CursorIdentity(),
			Value:    alertCursorValue(last, query.Sort),
		}
	}

	return alerts.ListResult{
		Alerts:         items,
		NextCursor:     next,
		SeverityCounts: counts,
		Total:          total,
		StreamCursor:   streamCursor,
	}, nil
}

func (store *Store) GetAlertDetail(ctx context.Context, principal auth.Principal, id int64) (alerts.Detail, error) {
	var item alerts.Alert
	var labelsJSON []byte
	var annotationsJSON []byte
	access, args := readAccessCondition(principal, "alert.labels", []any{id})
	err := store.pool.QueryRow(ctx, `
		SELECT alert.id, alert.source_slug, alert.fingerprint, alert.source_status, alert.labels, alert.annotations,
		       alert.starts_at, alert.ends_at, alert.generator_url, alert.external_url, alert.first_seen, alert.last_seen,
		       alert.repeat_count, alert.occurrence, alert.acknowledged, alert.suppressed, alert.acknowledged_at, alert.acknowledged_by, alert.raw_data
		FROM alerts AS alert
		WHERE alert.id = $1 AND (`+access+`)
	`, args...).Scan(
		&item.ID, &item.SourceSlug, &item.Fingerprint, &item.SourceStatus,
		&labelsJSON, &annotationsJSON, &item.StartsAt, &item.EndsAt,
		&item.GeneratorURL, &item.ExternalURL, &item.FirstSeen, &item.LastSeen,
		&item.RepeatCount, &item.Occurrence, &item.Acknowledged, &item.Suppressed, &item.AcknowledgedAt, &item.AcknowledgedBy, &item.RawData,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return alerts.Detail{}, alerts.ErrNotFound
	}
	if err != nil {
		return alerts.Detail{}, fmt.Errorf("get alert %d: %w", id, err)
	}
	if err := json.Unmarshal(labelsJSON, &item.Labels); err != nil {
		return alerts.Detail{}, fmt.Errorf("decode labels for alert %d: %w", id, err)
	}
	if err := json.Unmarshal(annotationsJSON, &item.Annotations); err != nil {
		return alerts.Detail{}, fmt.Errorf("decode annotations for alert %d: %w", id, err)
	}

	historyAccess, historyArgs := readAccessCondition(principal, "alert.labels", []any{id})
	rows, err := store.pool.Query(ctx, `
		SELECT history.id, history.occurrence, history.event_type, history.source_status,
		       history.actor, history.message, history.occurred_at
		FROM alert_history AS history
		JOIN alerts AS alert ON alert.id = history.alert_id
		WHERE history.alert_id = $1 AND (`+historyAccess+`)
		ORDER BY history.id DESC
	`, historyArgs...)
	if err != nil {
		return alerts.Detail{}, fmt.Errorf("query history for alert %d: %w", id, err)
	}
	defer rows.Close()
	history := make([]alerts.HistoryEvent, 0)
	for rows.Next() {
		var event alerts.HistoryEvent
		if err := rows.Scan(
			&event.ID, &event.Occurrence, &event.Type, &event.SourceStatus,
			&event.Actor, &event.Message, &event.OccurredAt,
		); err != nil {
			return alerts.Detail{}, fmt.Errorf("scan history for alert %d: %w", id, err)
		}
		history = append(history, event)
	}
	if err := rows.Err(); err != nil {
		return alerts.Detail{}, fmt.Errorf("iterate history for alert %d: %w", id, err)
	}
	return alerts.Detail{Alert: item, History: history}, nil
}

func (store *Store) AcknowledgeAlert(ctx context.Context, principal auth.Principal, id int64, acknowledged bool) (alerts.Detail, error) {
	if !principal.CanOperate() {
		return alerts.Detail{}, alerts.ErrNotFound
	}
	actor := principal.Subject
	if actor == "" {
		actor = principal.DisplayName
	}
	if actor == "" {
		actor = "unknown"
	}
	err := pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		access, args := operateAccessCondition(principal, "alert.labels", []any{id})
		var alert alerts.Alert
		var labelsJSON, annotationsJSON []byte
		err := tx.QueryRow(ctx, `
			SELECT alert.id, alert.source_slug, alert.fingerprint, alert.source_status, alert.labels, alert.annotations,
			       alert.starts_at, alert.ends_at, alert.generator_url, alert.external_url, alert.first_seen, alert.last_seen,
			       alert.repeat_count, alert.occurrence, alert.acknowledged
			FROM alerts AS alert
			WHERE alert.id = $1 AND (`+access+`)
			FOR UPDATE
		`, args...).Scan(
			&alert.ID, &alert.SourceSlug, &alert.Fingerprint, &alert.SourceStatus, &labelsJSON, &annotationsJSON,
			&alert.StartsAt, &alert.EndsAt, &alert.GeneratorURL, &alert.ExternalURL, &alert.FirstSeen, &alert.LastSeen,
			&alert.RepeatCount, &alert.Occurrence, &alert.Acknowledged,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return alerts.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get alert %d for acknowledgement: %w", id, err)
		}
		if alert.Acknowledged == acknowledged {
			return nil
		}
		if err := json.Unmarshal(labelsJSON, &alert.Labels); err != nil {
			return fmt.Errorf("decode labels for alert %d: %w", id, err)
		}
		if err := json.Unmarshal(annotationsJSON, &alert.Annotations); err != nil {
			return fmt.Errorf("decode annotations for alert %d: %w", id, err)
		}
		now := time.Now().UTC()
		if acknowledged {
			_, err = tx.Exec(ctx, "UPDATE alerts SET acknowledged = true, acknowledged_at = $2, acknowledged_by = $3 WHERE id = $1", id, now, actor)
		} else {
			_, err = tx.Exec(ctx, "UPDATE alerts SET acknowledged = false, acknowledged_at = NULL, acknowledged_by = '' WHERE id = $1", id)
		}
		if err != nil {
			return fmt.Errorf("update acknowledgement for alert %d: %w", id, err)
		}
		eventType, message := "alert.acknowledged", "Alert acknowledged"
		if !acknowledged {
			eventType, message = "alert.unacknowledged", "Alert unacknowledged"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO alert_history (alert_id, occurrence, event_type, source_status, actor, message, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, id, alert.Occurrence, eventType, alert.SourceStatus, actor, message, now); err != nil {
			return fmt.Errorf("insert acknowledgement history for alert %d: %w", id, err)
		}
		return insertStreamEvent(ctx, tx, "alert.updated", id, alertmanager.IncomingAlert{
			SourceSlug: alert.SourceSlug, Fingerprint: alert.Fingerprint, Status: alert.SourceStatus,
			Labels: alert.Labels, Annotations: alert.Annotations, ReceivedAt: now,
		}, nil)
	})
	if err != nil {
		return alerts.Detail{}, err
	}
	return store.GetAlertDetail(ctx, principal, id)
}

func (store *Store) StreamEvents(ctx context.Context, principal auth.Principal, afterID int64, limit int) (alerts.StreamBatch, error) {
	var scannedThrough int64
	if err := store.pool.QueryRow(ctx, `
		SELECT COALESCE(max(candidate.id), $1)
		FROM (
			SELECT id FROM stream_events WHERE id > $1 ORDER BY id LIMIT $2
		) AS candidate
	`, afterID, limit).Scan(&scannedThrough); err != nil {
		return alerts.StreamBatch{}, fmt.Errorf("scan stream event window: %w", err)
	}
	if scannedThrough == afterID {
		return alerts.StreamBatch{ScannedThrough: afterID}, nil
	}
	if !principal.Anonymous {
		var err error
		principal, err = resolvePrincipal(ctx, store.pool, principal.UserID)
		if err != nil {
			return alerts.StreamBatch{}, err
		}
	}
	access, args := readAccessCondition(principal, "event.labels", []any{afterID, scannedThrough})
	previousAccess, args := readAccessCondition(principal, "event.previous_labels", args)
	rows, err := store.pool.Query(ctx, `
		SELECT event.id, event.event_type, event.alert_id, event.occurred_at,
		       event.severity, event.alert_name, event.summary, event.source_slug, event.team,
		       event.labels, event.previous_labels
		FROM stream_events AS event
		WHERE event.id > $1 AND event.id <= $2
			AND ((`+access+`) OR (event.previous_labels IS NOT NULL AND (`+previousAccess+`)))
		ORDER BY event.id
	`, args...)
	if err != nil {
		return alerts.StreamBatch{}, fmt.Errorf("query stream events: %w", err)
	}
	defer rows.Close()

	events := make([]alerts.StreamEvent, 0, limit)
	for rows.Next() {
		var event alerts.StreamEvent
		var labelsJSON []byte
		var previousLabelsJSON []byte
		if err := rows.Scan(
			&event.ID, &event.Type, &event.AlertID, &event.OccurredAt,
			&event.Severity, &event.AlertName, &event.Summary, &event.SourceSlug, &event.Team,
			&labelsJSON, &previousLabelsJSON,
		); err != nil {
			return alerts.StreamBatch{}, fmt.Errorf("scan stream event: %w", err)
		}
		if err := json.Unmarshal(labelsJSON, &event.Labels); err != nil {
			return alerts.StreamBatch{}, fmt.Errorf("decode stream event labels: %w", err)
		}
		if len(previousLabelsJSON) > 0 {
			if err := json.Unmarshal(previousLabelsJSON, &event.PreviousLabels); err != nil {
				return alerts.StreamBatch{}, fmt.Errorf("decode previous stream event labels: %w", err)
			}
		}
		if !auth.CanReadLabels(principal, event.Labels) && auth.CanReadLabels(principal, event.PreviousLabels) {
			event.Type = "alert.removed"
			event.Redacted = true
			event.Severity = ""
			event.AlertName = ""
			event.Summary = ""
			event.SourceSlug = ""
			event.Team = ""
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return alerts.StreamBatch{}, fmt.Errorf("iterate stream events: %w", err)
	}
	return alerts.StreamBatch{Events: events, ScannedThrough: scannedThrough}, nil
}

func insertStreamEvent(
	ctx context.Context,
	tx pgx.Tx,
	eventType string,
	alertID int64,
	alert alertmanager.IncomingAlert,
	previousLabelsJSON []byte,
) error {
	severity := alert.Labels["severity"]
	if severity == "" {
		severity = "warning"
	}
	alertName := alert.Labels["alertname"]
	if alertName == "" {
		alertName = alert.Fingerprint
	}
	labelsJSON, err := json.Marshal(alert.Labels)
	if err != nil {
		return fmt.Errorf("marshal stream event labels: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO stream_events (
			event_type, alert_id, occurred_at, severity, alert_name, summary, source_slug, team,
			labels, previous_labels
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, eventType, alertID, alert.ReceivedAt, severity, alertName,
		alert.Annotations["summary"], alert.SourceSlug, alert.Labels["team"], labelsJSON, previousLabelsJSON); err != nil {
		return fmt.Errorf("insert stream event for alert %d: %w", alertID, err)
	}
	return nil
}

func insertHistoryEvent(
	ctx context.Context,
	tx pgx.Tx,
	alertID int64,
	occurrence int,
	eventType string,
	sourceStatus string,
	occurredAt time.Time,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO alert_history (alert_id, occurrence, event_type, source_status, occurred_at)
		VALUES ($1, $2, $3, $4, $5)
	`, alertID, occurrence, eventType, sourceStatus, occurredAt); err != nil {
		return fmt.Errorf("insert history for alert %d: %w", alertID, err)
	}
	return nil
}

func alertMateriallyChanged(
	previousStatus string,
	previousLabelsJSON []byte,
	previousAnnotationsJSON []byte,
	incoming alertmanager.IncomingAlert,
) (bool, error) {
	var previousLabels map[string]string
	if err := json.Unmarshal(previousLabelsJSON, &previousLabels); err != nil {
		return false, fmt.Errorf("decode previous labels: %w", err)
	}
	var previousAnnotations map[string]string
	if err := json.Unmarshal(previousAnnotationsJSON, &previousAnnotations); err != nil {
		return false, fmt.Errorf("decode previous annotations: %w", err)
	}
	return previousStatus != incoming.Status ||
		!reflect.DeepEqual(previousLabels, incoming.Labels) ||
		!reflect.DeepEqual(previousAnnotations, incoming.Annotations), nil
}

func alertFilters(principal auth.Principal, query alerts.Query, alias string) (string, []any) {
	access, args := readAccessCondition(principal, alias+".labels", nil)
	conditions := []string{"(" + access + ")"}
	add := func(expression string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(expression, len(args)))
	}
	if query.Source != "" {
		add(alias+".source_slug = $%d", query.Source)
	}
	if query.Status != "" {
		add(alias+".source_status = $%d", query.Status)
	}
	if query.Severity != "" {
		add("COALESCE("+alias+".labels->>'severity', 'warning') = $%d", query.Severity)
	}
	if query.Team != "" {
		add(alias+".labels->>'team' = $%d", query.Team)
	}
	for _, matcher := range query.Matches {
		args = append(args, matcher.Name, matcher.Value)
		namePosition := len(args) - 1
		valuePosition := len(args)
		if matcher.Operator == "!=" {
			// Like Prometheus, a negative matcher also includes absent labels.
			conditions = append(conditions, fmt.Sprintf("(NOT (%s ? $%d) OR %s->>$%d <> $%d)", alias+".labels", namePosition, alias+".labels", namePosition, valuePosition))
			continue
		}
		conditions = append(conditions, fmt.Sprintf("(%s ? $%d AND %s->>$%d = $%d)", alias+".labels", namePosition, alias+".labels", namePosition, valuePosition))
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func alertCursorValue(alert alerts.Alert, sort string) string {
	switch sort {
	case "lastSeen":
		return alert.LastSeen.UTC().Format(time.RFC3339Nano)
	case "startsAt":
		return alert.StartsAt.UTC().Format(time.RFC3339Nano)
	case "severity":
		switch alert.Labels["severity"] {
		case "critical":
			return "3"
		case "info":
			return "1"
		case "warning", "":
			return "2"
		default:
			return "0"
		}
	case "alertname":
		return alert.Labels["alertname"]
	case "summary":
		return alert.Annotations["summary"]
	case "status":
		return alert.SourceStatus
	case "team":
		return alert.Labels["team"]
	case "instance":
		return alert.Labels["instance"]
	case "source":
		return alert.SourceSlug
	default:
		return ""
	}
}

func readAccessCondition(principal auth.Principal, labelsExpression string, args []any) (string, []any) {
	if principal.Anonymous {
		return "TRUE", args
	}
	grants := make([]string, 0, len(principal.Grants))
	for _, grant := range principal.Grants {
		switch grant.Role {
		case auth.RoleViewer, auth.RoleOperator:
		case auth.RoleAdministrator:
			return "TRUE", args
		default:
			continue
		}
		if len(grant.Matchers) == 0 {
			return "TRUE", args
		}
		matchers := make([]string, 0, len(grant.Matchers))
		for _, matcher := range grant.Matchers {
			args = append(args, matcher.Name, matcher.Value)
			namePosition := len(args) - 1
			valuePosition := len(args)
			operator := map[string]string{"=": "=", "!=": "<>", "=~": "~", "!~": "!~"}[matcher.Operator]
			matchers = append(matchers, fmt.Sprintf(
				"(%s ? $%d AND %s->>$%d %s $%d)",
				labelsExpression, namePosition, labelsExpression, namePosition, operator, valuePosition,
			))
		}
		grants = append(grants, "("+strings.Join(matchers, " AND ")+")")
	}
	if len(grants) == 0 {
		return "FALSE", args
	}
	return strings.Join(grants, " OR "), args
}

func operateAccessCondition(principal auth.Principal, labelsExpression string, args []any) (string, []any) {
	grants := make([]string, 0, len(principal.Grants))
	for _, grant := range principal.Grants {
		switch grant.Role {
		case auth.RoleAdministrator:
			return "TRUE", args
		case auth.RoleOperator:
		default:
			continue
		}
		if len(grant.Matchers) == 0 {
			return "TRUE", args
		}
		matchers := make([]string, 0, len(grant.Matchers))
		for _, matcher := range grant.Matchers {
			args = append(args, matcher.Name, matcher.Value)
			namePosition := len(args) - 1
			valuePosition := len(args)
			operator := map[string]string{"=": "=", "!=": "<>", "=~": "~", "!~": "!~"}[matcher.Operator]
			matchers = append(matchers, fmt.Sprintf(
				"(%s ? $%d AND %s->>$%d %s $%d)",
				labelsExpression, namePosition, labelsExpression, namePosition, operator, valuePosition,
			))
		}
		grants = append(grants, "("+strings.Join(matchers, " AND ")+")")
	}
	if len(grants) == 0 {
		return "FALSE", args
	}
	return strings.Join(grants, " OR "), args
}

func appendCondition(where, condition string) string {
	if where == "" {
		return " WHERE " + condition
	}
	return where + " AND " + condition
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
