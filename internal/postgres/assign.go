package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
)

// maxAssigneeLength bounds what can be stored as an owner. Generous enough for
// a rota address, short enough that the column cannot be used as free storage.
const maxAssigneeLength = 128

// AssignAlert sets or clears who owns an alert. An empty assignee unassigns it.
//
// The assignee is whoever the operator names, not a promview user: an alert is
// routinely handed to somebody who has never signed in here. The actor - the
// operator who made the assignment - is recorded separately, because "who owns
// this" and "who decided that" are different questions and the second one is
// the accountability trail.
func (store *Store) AssignAlert(ctx context.Context, principal auth.Principal, id int64, assignee string) (alerts.Detail, error) {
	if !principal.CanOperate() {
		return alerts.Detail{}, alerts.ErrNotFound
	}
	assignee = strings.TrimSpace(assignee)
	if len(assignee) > maxAssigneeLength {
		return alerts.Detail{}, fmt.Errorf("%w: assignee is longer than %d characters", alerts.ErrInvalid, maxAssigneeLength)
	}
	actor := operatorName(principal)
	err := pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		access, args := operateAccessCondition(principal, "alert.labels", []any{id})
		var alert alerts.Alert
		var labelsJSON, annotationsJSON []byte
		err := tx.QueryRow(ctx, `
			SELECT alert.id, alert.source_slug, alert.fingerprint, alert.source_status,
			       alert.labels, alert.annotations, alert.occurrence, alert.assigned_to
			FROM alerts AS alert
			WHERE alert.id = $1 AND (`+access+`)
			FOR UPDATE
		`, args...).Scan(
			&alert.ID, &alert.SourceSlug, &alert.Fingerprint, &alert.SourceStatus,
			&labelsJSON, &annotationsJSON, &alert.Occurrence, &alert.AssignedTo,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return alerts.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get alert %d for assignment: %w", id, err)
		}
		// Assigning to whoever already owns it is not news, and streaming it
		// would wake every open console for nothing.
		if alert.AssignedTo == assignee {
			return nil
		}
		if err := json.Unmarshal(labelsJSON, &alert.Labels); err != nil {
			return fmt.Errorf("decode labels for alert %d: %w", id, err)
		}
		if err := json.Unmarshal(annotationsJSON, &alert.Annotations); err != nil {
			return fmt.Errorf("decode annotations for alert %d: %w", id, err)
		}
		now := time.Now().UTC()
		if assignee == "" {
			_, err = tx.Exec(ctx,
				"UPDATE alerts SET assigned_to = '', assigned_at = NULL, assigned_by = '' WHERE id = $1", id)
		} else {
			_, err = tx.Exec(ctx,
				"UPDATE alerts SET assigned_to = $2, assigned_at = $3, assigned_by = $4 WHERE id = $1",
				id, assignee, now, actor)
		}
		if err != nil {
			return fmt.Errorf("update assignment for alert %d: %w", id, err)
		}
		eventType, message := "alert.assigned", "Assigned to "+assignee
		if assignee == "" {
			eventType, message = "alert.unassigned", "Assignment cleared"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO alert_history (alert_id, occurrence, event_type, source_status, actor, message, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, id, alert.Occurrence, eventType, alert.SourceStatus, actor, message, now); err != nil {
			return fmt.Errorf("insert assignment history for alert %d: %w", id, err)
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

// operatorName is who to record for an operator action. The subject is stable
// across a display-name change, which is what an audit trail wants; the display
// name is the fallback for a deployment whose provider supplies no subject.
func operatorName(principal auth.Principal) string {
	if principal.Subject != "" {
		return principal.Subject
	}
	if principal.DisplayName != "" {
		return principal.DisplayName
	}
	return "unknown"
}
