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

// maxNoteLength bounds one note. Long enough for a paragraph of handover,
// short enough that the table cannot be used as a document store.
const maxNoteLength = 4000

// AddNote records one operator's note against an alert's current occurrence.
//
// Notes are append-only. There is no edit and no delete, because a note is what
// somebody relied on at the time, and a handover that can be quietly rewritten
// afterwards is worth less than none.
func (store *Store) AddNote(ctx context.Context, principal auth.Principal, id int64, body string) (alerts.Detail, error) {
	if !principal.CanOperate() {
		return alerts.Detail{}, alerts.ErrNotFound
	}
	body, err := validNoteBody(body)
	if err != nil {
		return alerts.Detail{}, err
	}
	author := operatorName(principal)
	err = pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		access, args := operateAccessCondition(principal, "alert.labels", []any{id})
		var alert alerts.Alert
		var labelsJSON, annotationsJSON []byte
		err := tx.QueryRow(ctx, `
			SELECT alert.id, alert.source_slug, alert.fingerprint, alert.source_status,
			       alert.labels, alert.annotations, alert.occurrence
			FROM alerts AS alert
			WHERE alert.id = $1 AND (`+access+`)
			FOR UPDATE
		`, args...).Scan(
			&alert.ID, &alert.SourceSlug, &alert.Fingerprint, &alert.SourceStatus,
			&labelsJSON, &annotationsJSON, &alert.Occurrence,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return alerts.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get alert %d for note: %w", id, err)
		}
		if err := json.Unmarshal(labelsJSON, &alert.Labels); err != nil {
			return fmt.Errorf("decode labels for alert %d: %w", id, err)
		}
		if err := json.Unmarshal(annotationsJSON, &alert.Annotations); err != nil {
			return fmt.Errorf("decode annotations for alert %d: %w", id, err)
		}
		now := time.Now().UTC()
		if _, err := tx.Exec(ctx, `
			INSERT INTO alert_notes (alert_id, occurrence, author, body, created_at)
			VALUES ($1, $2, $3, $4, $5)
		`, id, alert.Occurrence, author, body, now); err != nil {
			return fmt.Errorf("insert note for alert %d: %w", id, err)
		}
		// History records that a note was added, not what it said. The body
		// lives in one place, so redacting or exporting it later is one table
		// rather than two that must agree.
		if _, err := tx.Exec(ctx, `
			INSERT INTO alert_history (alert_id, occurrence, event_type, source_status, actor, message, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, id, alert.Occurrence, "alert.note", alert.SourceStatus, author, "Note added", now); err != nil {
			return fmt.Errorf("insert note history for alert %d: %w", id, err)
		}
		// The list carries a note count, so a new note changes a row every open
		// console is showing.
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

// listNotes reads an alert's notes oldest first. Access is the caller's own
// read scope, checked by whoever loaded the alert.
func (store *Store) listNotes(ctx context.Context, alertID int64) ([]alerts.Note, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT id, occurrence, author, body, created_at
		FROM alert_notes WHERE alert_id = $1 ORDER BY id
	`, alertID)
	if err != nil {
		return nil, fmt.Errorf("list notes for alert %d: %w", alertID, err)
	}
	defer rows.Close()
	notes := []alerts.Note{}
	for rows.Next() {
		var note alerts.Note
		if err := rows.Scan(&note.ID, &note.Occurrence, &note.Author, &note.Body, &note.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan note for alert %d: %w", alertID, err)
		}
		notes = append(notes, note)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notes for alert %d: %w", alertID, err)
	}
	return notes, nil
}

// validNoteBody normalises and bounds a note. Shared with the bulk path so a
// note written to forty alerts is held to the same rules as one written to one.
func validNoteBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("%w: a note cannot be empty", alerts.ErrInvalid)
	}
	if len(body) > maxNoteLength {
		return "", fmt.Errorf("%w: a note cannot exceed %d characters", alerts.ErrInvalid, maxNoteLength)
	}
	return body, nil
}
