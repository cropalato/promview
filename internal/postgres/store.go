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
		INSERT INTO alert_sources (slug, name, token_hash, enabled)
		VALUES ($1, $2, $3, true)
		ON CONFLICT (slug) DO UPDATE SET
			name = EXCLUDED.name,
			token_hash = EXCLUDED.token_hash,
			enabled = true,
			updated_at = now()
	`, source.Slug, source.Name, sources.HashToken(rawToken))
	if err != nil {
		return fmt.Errorf("set alert source %s: %w", source.Slug, err)
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
		INSERT INTO sessions (token_hash, subject, email, display_name, roles, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, session.TokenHash, session.Subject, session.Email, session.DisplayName, session.Roles, session.ExpiresAt)
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
		RETURNING token_hash, subject, email, display_name, roles, expires_at
	`, tokenHash, now).Scan(
		&session.TokenHash, &session.Subject, &session.Email, &session.DisplayName, &session.Roles, &session.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Session{}, auth.ErrUnauthenticated
	}
	if err != nil {
		return auth.Session{}, fmt.Errorf("find session: %w", err)
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
				if err := insertStreamEvent(ctx, tx, "alert.created", id, alert); err != nil {
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
						raw_data = $12
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
					if err := insertStreamEvent(ctx, tx, streamType, id, alert); err != nil {
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

func (store *Store) ListAlerts(ctx context.Context, query alerts.Query) (alerts.ListResult, error) {
	var streamCursor int64
	if err := store.pool.QueryRow(ctx, "SELECT COALESCE(max(id), 0) FROM stream_events").Scan(&streamCursor); err != nil {
		return alerts.ListResult{}, fmt.Errorf("read stream cursor: %w", err)
	}
	where, args := alertFilters(query)
	countSQL := `
		SELECT COALESCE(labels->>'severity', 'warning'), count(*)
		FROM alerts` + where + `
		GROUP BY COALESCE(labels->>'severity', 'warning')`

	rows, err := store.pool.Query(ctx, countSQL, args...)
	if err != nil {
		return alerts.ListResult{}, fmt.Errorf("count alerts: %w", err)
	}
	counts := make(map[string]int64)
	var total int64
	for rows.Next() {
		var severity string
		var count int64
		if err := rows.Scan(&severity, &count); err != nil {
			rows.Close()
			return alerts.ListResult{}, fmt.Errorf("scan alert count: %w", err)
		}
		counts[severity] = count
		total += count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return alerts.ListResult{}, fmt.Errorf("iterate alert counts: %w", err)
	}
	rows.Close()

	listWhere := where
	listArgs := append([]any(nil), args...)
	if query.Cursor != nil {
		listArgs = append(listArgs, query.Cursor.LastSeen, query.Cursor.ID)
		listWhere = appendCondition(listWhere, fmt.Sprintf("(last_seen, id) < ($%d, $%d)", len(listArgs)-1, len(listArgs)))
	}
	listArgs = append(listArgs, query.Limit+1)
	listSQL := `
		SELECT id, source_slug, fingerprint, source_status, labels, annotations,
		       starts_at, ends_at, generator_url, external_url, first_seen, last_seen, repeat_count,
		       occurrence, raw_data
		FROM alerts` + listWhere + fmt.Sprintf(`
		ORDER BY last_seen DESC, id DESC
		LIMIT $%d`, len(listArgs))

	rows, err = store.pool.Query(ctx, listSQL, listArgs...)
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
			&item.Occurrence, &item.RawData,
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
		next = &alerts.Cursor{LastSeen: last.LastSeen, ID: last.ID}
	}

	return alerts.ListResult{
		Alerts:         items,
		NextCursor:     next,
		SeverityCounts: counts,
		Total:          total,
		StreamCursor:   streamCursor,
	}, nil
}

func (store *Store) GetAlertDetail(ctx context.Context, id int64) (alerts.Detail, error) {
	var item alerts.Alert
	var labelsJSON []byte
	var annotationsJSON []byte
	err := store.pool.QueryRow(ctx, `
		SELECT id, source_slug, fingerprint, source_status, labels, annotations,
		       starts_at, ends_at, generator_url, external_url, first_seen, last_seen,
		       repeat_count, occurrence, raw_data
		FROM alerts
		WHERE id = $1
	`, id).Scan(
		&item.ID, &item.SourceSlug, &item.Fingerprint, &item.SourceStatus,
		&labelsJSON, &annotationsJSON, &item.StartsAt, &item.EndsAt,
		&item.GeneratorURL, &item.ExternalURL, &item.FirstSeen, &item.LastSeen,
		&item.RepeatCount, &item.Occurrence, &item.RawData,
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

	rows, err := store.pool.Query(ctx, `
		SELECT id, occurrence, event_type, source_status, actor, message, occurred_at
		FROM alert_history
		WHERE alert_id = $1
		ORDER BY id DESC
	`, id)
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

func (store *Store) StreamEvents(ctx context.Context, afterID int64, limit int) ([]alerts.StreamEvent, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT id, event_type, alert_id, occurred_at, severity, alert_name, summary, source_slug, team
		FROM stream_events
		WHERE id > $1
		ORDER BY id
		LIMIT $2
	`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("query stream events: %w", err)
	}
	defer rows.Close()

	events := make([]alerts.StreamEvent, 0, limit)
	for rows.Next() {
		var event alerts.StreamEvent
		if err := rows.Scan(
			&event.ID, &event.Type, &event.AlertID, &event.OccurredAt,
			&event.Severity, &event.AlertName, &event.Summary, &event.SourceSlug, &event.Team,
		); err != nil {
			return nil, fmt.Errorf("scan stream event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stream events: %w", err)
	}
	return events, nil
}

func insertStreamEvent(
	ctx context.Context,
	tx pgx.Tx,
	eventType string,
	alertID int64,
	alert alertmanager.IncomingAlert,
) error {
	severity := alert.Labels["severity"]
	if severity == "" {
		severity = "warning"
	}
	alertName := alert.Labels["alertname"]
	if alertName == "" {
		alertName = alert.Fingerprint
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO stream_events (
			event_type, alert_id, occurred_at, severity, alert_name, summary, source_slug, team
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, eventType, alertID, alert.ReceivedAt, severity, alertName,
		alert.Annotations["summary"], alert.SourceSlug, alert.Labels["team"]); err != nil {
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

func alertFilters(query alerts.Query) (string, []any) {
	conditions := make([]string, 0, 4)
	args := make([]any, 0, 4)
	add := func(expression string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(expression, len(args)))
	}
	if query.Source != "" {
		add("source_slug = $%d", query.Source)
	}
	if query.Status != "" {
		add("source_status = $%d", query.Status)
	}
	if query.Severity != "" {
		add("COALESCE(labels->>'severity', 'warning') = $%d", query.Severity)
	}
	if query.Team != "" {
		add("labels->>'team' = $%d", query.Team)
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
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
