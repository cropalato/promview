package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
)

/*
Reconciliation compares what promview holds against what the source Alertmanager
still has, which is the only way to learn two things webhooks never report: an
alert that ended while silenced, and an alert that is currently being suppressed.

An alert the Alertmanager no longer lists is recorded as resolved rather than
expired. Expiry is promview's own inference from silence; here the source itself
is authoritative, and flattening the two would throw away the difference between
a confirmed ending and a guess.
*/

// ReconcileResult reports what one pass changed, so the caller can log
// something meaningful without re-querying.
type ReconcileResult struct {
	Resolved   int
	Suppressed int
	Released   int
}

// ReconcileSource aligns one source's firing alerts with the Alertmanager's own
// view. Fingerprints in `missing` are resolved; every other stored alert has its
// suppression flag brought in line with `live`.
//
// The caller decides what counts as missing rather than this method deriving it,
// because a single absent reading is not evidence: an Alertmanager restarting
// briefly holds no alerts at all, and resolving everything on that basis would
// be worse than the staleness reconciliation exists to fix.
func (store *Store) ReconcileSource(
	ctx context.Context,
	sourceSlug string,
	live []alertmanager.LiveAlert,
	missing map[string]bool,
	now time.Time,
) (ReconcileResult, error) {
	suppressedByFingerprint := make(map[string]bool, len(live))
	for _, alert := range live {
		suppressedByFingerprint[alert.Fingerprint] = alert.Suppressed
	}

	var result ReconcileResult
	err := pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, fingerprint, labels, annotations, occurrence, suppressed
			FROM alerts
			WHERE source_slug = $1 AND source_status = $2
			FOR UPDATE SKIP LOCKED
		`, sourceSlug, alerts.StatusFiring)
		if err != nil {
			return fmt.Errorf("select alerts for reconciliation: %w", err)
		}
		type storedAlert struct {
			id          int64
			fingerprint string
			labels      map[string]string
			annotations map[string]string
			occurrence  int
			suppressed  bool
		}
		var stored []storedAlert
		for rows.Next() {
			var item storedAlert
			var labelsJSON, annotationsJSON []byte
			if err := rows.Scan(&item.id, &item.fingerprint, &labelsJSON, &annotationsJSON, &item.occurrence, &item.suppressed); err != nil {
				rows.Close()
				return fmt.Errorf("scan alert for reconciliation: %w", err)
			}
			if err := json.Unmarshal(labelsJSON, &item.labels); err != nil {
				rows.Close()
				return fmt.Errorf("decode labels for alert %d: %w", item.id, err)
			}
			if err := json.Unmarshal(annotationsJSON, &item.annotations); err != nil {
				rows.Close()
				return fmt.Errorf("decode annotations for alert %d: %w", item.id, err)
			}
			stored = append(stored, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate alerts for reconciliation: %w", err)
		}
		rows.Close()

		occurredAt := now.UTC()
		for _, item := range stored {
			incoming := alertmanager.IncomingAlert{
				SourceSlug:  sourceSlug,
				Fingerprint: item.fingerprint,
				Labels:      item.labels,
				Annotations: item.annotations,
				ReceivedAt:  occurredAt,
			}

			if missing[item.fingerprint] {
				incoming.Status = alerts.StatusResolved
				if _, err := tx.Exec(ctx, `
					UPDATE alerts SET
						source_status = $1,
						ends_at = COALESCE(ends_at, $2),
						suppressed = false
					WHERE id = $3
				`, alerts.StatusResolved, occurredAt, item.id); err != nil {
					return fmt.Errorf("resolve alert %d: %w", item.id, err)
				}
				if err := insertStreamEvent(ctx, tx, "alert.resolved", item.id, incoming, nil); err != nil {
					return err
				}
				// History says how it ended: the Alertmanager no longer had it,
				// which is a different claim from a delivered resolution.
				if err := insertHistoryEvent(ctx, tx, item.id, item.occurrence, "alert.reconciled", alerts.StatusResolved, occurredAt); err != nil {
					return err
				}
				result.Resolved++
				continue
			}

			suppressed, present := suppressedByFingerprint[item.fingerprint]
			if !present || suppressed == item.suppressed {
				continue
			}
			incoming.Status = alerts.StatusFiring
			if _, err := tx.Exec(ctx, "UPDATE alerts SET suppressed = $1 WHERE id = $2", suppressed, item.id); err != nil {
				return fmt.Errorf("update suppression for alert %d: %w", item.id, err)
			}
			if err := insertStreamEvent(ctx, tx, "alert.updated", item.id, incoming, nil); err != nil {
				return err
			}
			if suppressed {
				result.Suppressed++
			} else {
				result.Released++
			}
		}
		return nil
	})
	if err != nil {
		return ReconcileResult{}, err
	}
	return result, nil
}

// ReconcilableSources lists the enabled sources that carry an Alertmanager URL.
func (store *Store) ReconcilableSources(ctx context.Context) (map[string]string, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT slug, alertmanager_url FROM alert_sources
		WHERE enabled AND alertmanager_url <> ''
	`)
	if err != nil {
		return nil, fmt.Errorf("list reconcilable sources: %w", err)
	}
	defer rows.Close()
	urls := make(map[string]string)
	for rows.Next() {
		var slug, url string
		if err := rows.Scan(&slug, &url); err != nil {
			return nil, fmt.Errorf("scan reconcilable source: %w", err)
		}
		urls[slug] = url
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reconcilable sources: %w", err)
	}
	return urls, nil
}

// FiringFingerprints returns the fingerprints a source currently holds as
// firing, which is what a caller diffs against the Alertmanager's own list.
func (store *Store) FiringFingerprints(ctx context.Context, sourceSlug string) ([]string, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT fingerprint FROM alerts WHERE source_slug = $1 AND source_status = $2
	`, sourceSlug, alerts.StatusFiring)
	if err != nil {
		return nil, fmt.Errorf("list firing fingerprints: %w", err)
	}
	defer rows.Close()
	var fingerprints []string
	for rows.Next() {
		var fingerprint string
		if err := rows.Scan(&fingerprint); err != nil {
			return nil, fmt.Errorf("scan firing fingerprint: %w", err)
		}
		fingerprints = append(fingerprints, fingerprint)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate firing fingerprints: %w", err)
	}
	return fingerprints, nil
}
