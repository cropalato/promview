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
Close is an operator saying an alert is handled. It is promview-local and never
reaches Alertmanager: closing is not silencing, and an alert the source keeps
reporting is still firing however it has been filed.

It is a flag rather than a source_status for that reason. firing, resolved and
expired are claims about what the source reports; closed is a claim about what
somebody decided, and an alert can honestly be both still firing and already
dealt with. One field cannot say both.
*/

// CloseAlert marks an alert handled, or reopens one. The alert stays exactly as
// the source reports it; only the operator's own judgement changes.
func (store *Store) CloseAlert(ctx context.Context, principal auth.Principal, id int64, closed bool) (alerts.Detail, error) {
	if !principal.CanOperate() {
		return alerts.Detail{}, alerts.ErrNotFound
	}
	actor := operatorName(principal)
	err := pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		access, args := operateAccessCondition(principal, "alert.labels", []any{id})
		var alert alerts.Alert
		var labelsJSON, annotationsJSON []byte
		err := tx.QueryRow(ctx, `
			SELECT alert.id, alert.source_slug, alert.fingerprint, alert.source_status,
			       alert.labels, alert.annotations, alert.occurrence, alert.closed
			FROM alerts AS alert
			WHERE alert.id = $1 AND (`+access+`)
			FOR UPDATE
		`, args...).Scan(
			&alert.ID, &alert.SourceSlug, &alert.Fingerprint, &alert.SourceStatus,
			&labelsJSON, &annotationsJSON, &alert.Occurrence, &alert.Closed,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return alerts.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get alert %d for close: %w", id, err)
		}
		if alert.Closed == closed {
			return nil
		}
		if err := json.Unmarshal(labelsJSON, &alert.Labels); err != nil {
			return fmt.Errorf("decode labels for alert %d: %w", id, err)
		}
		if err := json.Unmarshal(annotationsJSON, &alert.Annotations); err != nil {
			return fmt.Errorf("decode annotations for alert %d: %w", id, err)
		}
		now := time.Now().UTC()
		if closed {
			_, err = tx.Exec(ctx,
				"UPDATE alerts SET closed = true, closed_at = $2, closed_by = $3 WHERE id = $1", id, now, actor)
		} else {
			_, err = tx.Exec(ctx,
				"UPDATE alerts SET closed = false, closed_at = NULL, closed_by = '' WHERE id = $1", id)
		}
		if err != nil {
			return fmt.Errorf("update close state for alert %d: %w", id, err)
		}
		eventType, message := "alert.closed", "Closed by operator"
		if !closed {
			eventType, message = "alert.reopened.local", "Reopened by operator"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO alert_history (alert_id, occurrence, event_type, source_status, actor, message, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, id, alert.Occurrence, eventType, alert.SourceStatus, actor, message, now); err != nil {
			return fmt.Errorf("insert close history for alert %d: %w", id, err)
		}
		// Closing takes the alert out of the default list, so every open
		// console is showing a row that should no longer be there. The stream
		// says resolved for the same reason expiry does: consumers only need to
		// know it left the firing view, not why.
		streamType := "alert.resolved"
		if !closed {
			// alert.updated rather than alert.created. The console announces a
			// newly created critical with a desktop notification, and an alert
			// somebody reopened is not new - it has been there the whole time.
			// Every open console still refetches on an update, so the row comes
			// back either way.
			streamType = "alert.updated"
		}
		return insertStreamEvent(ctx, tx, streamType, id, alertmanager.IncomingAlert{
			SourceSlug: alert.SourceSlug, Fingerprint: alert.Fingerprint, Status: alert.SourceStatus,
			Labels: alert.Labels, Annotations: alert.Annotations, ReceivedAt: now,
		}, nil)
	})
	if err != nil {
		return alerts.Detail{}, err
	}
	return store.GetAlertDetail(ctx, principal, id)
}
