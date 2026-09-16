package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
)

/*
Bulk actions apply one operator decision to a selection of alerts. They exist
because the alternative is an operator clicking through forty rows after one
incident, which is how alerts stop being acknowledged at all.

Selection is by explicit id, never by filter. "Close everything matching this
query" reads the same whether it matches four alerts or four thousand, and the
operator cannot see which until it has happened. A console that selects rows
already knows their ids.

Every alert is judged on its own. One outside the operator's scope does not fail
the rest; it comes back reported as not found, the same answer the single-alert
endpoints give, so a bulk request cannot be used to discover which alerts exist
outside a scope by watching what fails.
*/

// maxBulkAlerts bounds one request. The same ceiling as a page of alerts,
// because a selection is made from a page.
const maxBulkAlerts = 500

// alertChange is what one bulk action does to a single locked alert. It returns
// whether anything changed, plus how to record it; returning false records the
// alert as unchanged and writes nothing.
type alertChange func(ctx context.Context, tx pgx.Tx, alert alerts.Alert, actor string, now time.Time) (changed bool, historyType, message, streamType string, err error)

// applyToAlerts runs one change across a selection in a single transaction.
//
// One transaction, not one per alert: a bulk action is one operator decision,
// and half of it surviving a failure is a state nobody asked for. The whole
// request is bounded at maxBulkAlerts so that transaction stays short.
func (store *Store) applyToAlerts(
	ctx context.Context,
	principal auth.Principal,
	ids []int64,
	change alertChange,
) ([]alerts.BulkOutcome, error) {
	if !principal.CanOperate() {
		return nil, alerts.ErrNotFound
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: no alerts selected", alerts.ErrInvalid)
	}
	if len(ids) > maxBulkAlerts {
		return nil, fmt.Errorf("%w: at most %d alerts in one request", alerts.ErrInvalid, maxBulkAlerts)
	}
	actor := operatorName(principal)
	outcomes := make([]alerts.BulkOutcome, 0, len(ids))
	err := pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		now := time.Now().UTC()
		seen := make(map[int64]bool, len(ids))
		for _, id := range ids {
			// A selection that names the same alert twice is the console's
			// mistake, not two decisions; acting once is the honest reading.
			if seen[id] {
				continue
			}
			seen[id] = true

			access, args := operateAccessCondition(principal, "alert.labels", []any{id})
			var alert alerts.Alert
			var labelsJSON, annotationsJSON []byte
			err := tx.QueryRow(ctx, `
				SELECT alert.id, alert.source_slug, alert.fingerprint, alert.source_status,
				       alert.labels, alert.annotations, alert.occurrence,
				       alert.acknowledged, alert.assigned_to, alert.closed
				FROM alerts AS alert
				WHERE alert.id = $1 AND (`+access+`)
				FOR UPDATE
			`, args...).Scan(
				&alert.ID, &alert.SourceSlug, &alert.Fingerprint, &alert.SourceStatus,
				&labelsJSON, &annotationsJSON, &alert.Occurrence,
				&alert.Acknowledged, &alert.AssignedTo, &alert.Closed,
			)
			if errors.Is(err, pgx.ErrNoRows) {
				outcomes = append(outcomes, alerts.BulkOutcome{ID: id, Status: alerts.BulkNotFound})
				continue
			}
			if err != nil {
				return fmt.Errorf("get alert %d for bulk action: %w", id, err)
			}
			if err := json.Unmarshal(labelsJSON, &alert.Labels); err != nil {
				return fmt.Errorf("decode labels for alert %d: %w", id, err)
			}
			if err := json.Unmarshal(annotationsJSON, &alert.Annotations); err != nil {
				return fmt.Errorf("decode annotations for alert %d: %w", id, err)
			}

			changed, historyType, message, streamType, err := change(ctx, tx, alert, actor, now)
			if err != nil {
				return err
			}
			if !changed {
				outcomes = append(outcomes, alerts.BulkOutcome{ID: id, Status: alerts.BulkUnchanged})
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO alert_history (alert_id, occurrence, event_type, source_status, actor, message, occurred_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, id, alert.Occurrence, historyType, alert.SourceStatus, actor, message, now); err != nil {
				return fmt.Errorf("insert bulk history for alert %d: %w", id, err)
			}
			if err := insertStreamEvent(ctx, tx, streamType, id, alertmanager.IncomingAlert{
				SourceSlug: alert.SourceSlug, Fingerprint: alert.Fingerprint, Status: alert.SourceStatus,
				Labels: alert.Labels, Annotations: alert.Annotations, ReceivedAt: now,
			}, nil); err != nil {
				return err
			}
			outcomes = append(outcomes, alerts.BulkOutcome{ID: id, Status: alerts.BulkApplied})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return outcomes, nil
}

// BulkAcknowledge acknowledges or unacknowledges a selection.
func (store *Store) BulkAcknowledge(ctx context.Context, principal auth.Principal, ids []int64, acknowledged bool) ([]alerts.BulkOutcome, error) {
	return store.applyToAlerts(ctx, principal, ids, func(ctx context.Context, tx pgx.Tx, alert alerts.Alert, actor string, now time.Time) (bool, string, string, string, error) {
		if alert.Acknowledged == acknowledged {
			return false, "", "", "", nil
		}
		var err error
		if acknowledged {
			_, err = tx.Exec(ctx,
				"UPDATE alerts SET acknowledged = true, acknowledged_at = $2, acknowledged_by = $3 WHERE id = $1", alert.ID, now, actor)
		} else {
			_, err = tx.Exec(ctx,
				"UPDATE alerts SET acknowledged = false, acknowledged_at = NULL, acknowledged_by = '' WHERE id = $1", alert.ID)
		}
		if err != nil {
			return false, "", "", "", fmt.Errorf("update acknowledgement for alert %d: %w", alert.ID, err)
		}
		if acknowledged {
			return true, "alert.acknowledged", "Alert acknowledged", "alert.updated", nil
		}
		return true, "alert.unacknowledged", "Alert unacknowledged", "alert.updated", nil
	})
}

// BulkAssign sets or clears the owner of a selection.
func (store *Store) BulkAssign(ctx context.Context, principal auth.Principal, ids []int64, assignee string) ([]alerts.BulkOutcome, error) {
	assignee, err := validAssignee(assignee)
	if err != nil {
		return nil, err
	}
	return store.applyToAlerts(ctx, principal, ids, func(ctx context.Context, tx pgx.Tx, alert alerts.Alert, actor string, now time.Time) (bool, string, string, string, error) {
		if alert.AssignedTo == assignee {
			return false, "", "", "", nil
		}
		var err error
		if assignee == "" {
			_, err = tx.Exec(ctx,
				"UPDATE alerts SET assigned_to = '', assigned_at = NULL, assigned_by = '' WHERE id = $1", alert.ID)
		} else {
			_, err = tx.Exec(ctx,
				"UPDATE alerts SET assigned_to = $2, assigned_at = $3, assigned_by = $4 WHERE id = $1",
				alert.ID, assignee, now, actor)
		}
		if err != nil {
			return false, "", "", "", fmt.Errorf("update assignment for alert %d: %w", alert.ID, err)
		}
		if assignee == "" {
			return true, "alert.unassigned", "Assignment cleared", "alert.updated", nil
		}
		return true, "alert.assigned", "Assigned to " + assignee, "alert.updated", nil
	})
}

// BulkClose files a selection as handled, or reopens it.
func (store *Store) BulkClose(ctx context.Context, principal auth.Principal, ids []int64, closed bool) ([]alerts.BulkOutcome, error) {
	return store.applyToAlerts(ctx, principal, ids, func(ctx context.Context, tx pgx.Tx, alert alerts.Alert, actor string, now time.Time) (bool, string, string, string, error) {
		if alert.Closed == closed {
			return false, "", "", "", nil
		}
		var err error
		if closed {
			_, err = tx.Exec(ctx,
				"UPDATE alerts SET closed = true, closed_at = $2, closed_by = $3 WHERE id = $1", alert.ID, now, actor)
		} else {
			_, err = tx.Exec(ctx,
				"UPDATE alerts SET closed = false, closed_at = NULL, closed_by = '' WHERE id = $1", alert.ID)
		}
		if err != nil {
			return false, "", "", "", fmt.Errorf("update close state for alert %d: %w", alert.ID, err)
		}
		if closed {
			// Same as the single-alert close: the alert left the firing view,
			// which is all a stream consumer needs to know.
			return true, "alert.closed", "Closed by operator", "alert.resolved", nil
		}
		return true, "alert.reopened.local", "Reopened by operator", "alert.updated", nil
	})
}

// BulkNote writes the same note against a selection, which is the case where
// one finding explains every alert in it.
func (store *Store) BulkNote(ctx context.Context, principal auth.Principal, ids []int64, body string) ([]alerts.BulkOutcome, error) {
	body, err := validNoteBody(body)
	if err != nil {
		return nil, err
	}
	return store.applyToAlerts(ctx, principal, ids, func(ctx context.Context, tx pgx.Tx, alert alerts.Alert, actor string, now time.Time) (bool, string, string, string, error) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO alert_notes (alert_id, occurrence, author, body, created_at)
			VALUES ($1, $2, $3, $4, $5)
		`, alert.ID, alert.Occurrence, actor, body, now); err != nil {
			return false, "", "", "", fmt.Errorf("insert note for alert %d: %w", alert.ID, err)
		}
		// A note always applies: there is no state it could already be in.
		return true, "alert.note", "Note added", "alert.updated", nil
	})
}
