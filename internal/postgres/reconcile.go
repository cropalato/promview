package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
)

/*
Reconciliation compares what promview holds against what the source Alertmanager
still has, which is the only way to learn things webhooks never report: an
alert that ended while silenced, an alert that is currently being suppressed,
and a silence that is no longer holding anything back.

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
	// Revived counts alerts expiry had retired that the Alertmanager turned out
	// to still be holding. A non-zero count is not an error: it is expiry's
	// guess being corrected, which is the whole point of reading the source.
	Revived int
}

// ReconcileSource aligns one source's firing alerts with the Alertmanager's own
// view. Fingerprints in `missing` are resolved; every other stored alert has its
// suppression flag brought in line with `live`.
//
// It also reads the source's expired alerts, and returns to firing any the
// Alertmanager still holds. Expiry infers an ending from a source going quiet,
// and a source that is still reporting the alert is the evidence that the
// inference was wrong. Alerts the live view does not carry are left expired, and
// resolved alerts are not examined at all: that status came from the source.
// An Alertmanager reporting nothing revives nothing, since an alert absent from
// an empty reading is absent like any other.
//
// The caller decides what counts as missing rather than this method deriving it,
// because a single absent reading is not evidence: an Alertmanager restarting
// briefly holds no alerts at all, and resolving everything on that basis would
// be worse than the staleness reconciliation exists to fix.
//
// `activeSilences` is the ids of the silences the Alertmanager reports as
// active, or nil when that listing failed. A stored alert the live view no
// longer carries cannot have its suppression synced from live data, so its
// silenced_by is checked against this set instead: a silence that is gone is
// not holding anything back, and the row must stop reading as silenced even
// while the alert's own ending is still unconfirmed. Nil skips the check —
// no listing is not the same claim as an empty one. An alert suppressed with
// no silence ids is inhibited, and the silence listing says nothing about it.
func (store *Store) ReconcileSource(
	ctx context.Context,
	sourceSlug string,
	live []alertmanager.LiveAlert,
	missing map[string]bool,
	activeSilences map[string]bool,
	now time.Time,
) (ReconcileResult, error) {
	liveByFingerprint := make(map[string]alertmanager.LiveAlert, len(live))
	for _, alert := range live {
		alert.SilencedBy = sortedCopy(alert.SilencedBy)
		liveByFingerprint[alert.Fingerprint] = alert
	}

	var result ReconcileResult
	err := pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		// Expired alerts are read alongside firing ones because expiry is an
		// inference and this is the reading that can contradict it. A resolved
		// alert is deliberately not read: that status came from the source
		// itself, and the source is not something to second-guess.
		//
		// The expired half is restricted to fingerprints the live view actually
		// carries. Expired alerts accumulate - nothing retires them - so
		// selecting all of them would make every pass scan a set that only ever
		// grows, to decide almost every time that there is nothing to do. Only
		// an alert the Alertmanager is reporting can be revived, and that set is
		// bounded by the reading itself and reached through the unique index on
		// (source_slug, fingerprint).
		liveFingerprints := make([]string, 0, len(liveByFingerprint))
		for fingerprint := range liveByFingerprint {
			liveFingerprints = append(liveFingerprints, fingerprint)
		}
		rows, err := tx.Query(ctx, `
			SELECT id, fingerprint, labels, annotations, occurrence, suppressed, silenced_by, source_status
			FROM alerts
			WHERE source_slug = $1
			  AND (
				source_status = $2
				OR (source_status = $3 AND fingerprint = ANY($4))
			  )
			FOR UPDATE SKIP LOCKED
		`, sourceSlug, alerts.StatusFiring, alerts.StatusExpired, liveFingerprints)
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
			silencedBy  []string
			status      string
		}
		var stored []storedAlert
		for rows.Next() {
			var item storedAlert
			var labelsJSON, annotationsJSON []byte
			if err := rows.Scan(&item.id, &item.fingerprint, &labelsJSON, &annotationsJSON, &item.occurrence, &item.suppressed, &item.silencedBy, &item.status); err != nil {
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

			if item.status == alerts.StatusExpired {
				current, present := liveByFingerprint[item.fingerprint]
				if !present {
					// Still absent, so the guess still stands. Nothing here
					// promotes an expired alert to resolved: expiry never
					// observed an ending, and neither has this pass.
					continue
				}
				// The source is holding an alert promview had retired. Expiry
				// inferred an ending from silence and was wrong, so the row goes
				// back to firing and ends_at is cleared - it never ended.
				//
				// The occurrence is deliberately not advanced and the
				// acknowledgement is deliberately kept. A new occurrence is what
				// a firing alert after a *resolved* one gets, because the source
				// said that one ended. Here nothing ended, so treating this as a
				// new occurrence would discard an acknowledgement for an alert
				// that never stopped firing. Ingestion already takes this view:
				// a webhook arriving for an expired alert is an ordinary update.
				if _, err := tx.Exec(ctx, `
					UPDATE alerts SET
						source_status = $1,
						ends_at = NULL,
						suppressed = $2,
						silenced_by = $3
					WHERE id = $4
				`, alerts.StatusFiring, current.Suppressed, current.SilencedBy, item.id); err != nil {
					return fmt.Errorf("revive alert %d: %w", item.id, err)
				}
				incoming.Status = alerts.StatusFiring
				// alert.updated rather than alert.created: the console treats
				// alert.created as a genuinely new alert and raises a desktop
				// notification for a critical one. Reviving is promview
				// correcting its own bookkeeping, and a deployment that had
				// wrongly expired thirty alerts would otherwise announce all
				// thirty the moment this shipped.
				if err := insertStreamEvent(ctx, tx, "alert.updated", item.id, incoming, nil); err != nil {
					return err
				}
				if err := insertHistoryEvent(ctx, tx, item.id, item.occurrence, "alert.revived", alerts.StatusFiring, occurredAt); err != nil {
					return err
				}
				result.Revived++
				continue
			}

			if missing[item.fingerprint] {
				incoming.Status = alerts.StatusResolved
				if _, err := tx.Exec(ctx, `
					UPDATE alerts SET
						source_status = $1,
						ends_at = COALESCE(ends_at, $2),
						suppressed = false,
						silenced_by = '{}'
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

			current, present := liveByFingerprint[item.fingerprint]
			if !present {
				// Not in the live view, not yet confirmed missing. Its silences
				// can still be checked: every one gone from the active set means
				// nothing is suppressing it any more, whatever its ending turns
				// out to be. This is the one signal the alert list cannot carry
				// for an alert it no longer lists — an alert that cleared inside
				// a silence takes the evidence of that silence with it.
				if activeSilences == nil || !item.suppressed || len(item.silencedBy) == 0 {
					continue
				}
				if anySilenceActive(item.silencedBy, activeSilences) {
					continue
				}
				if _, err := tx.Exec(ctx,
					"UPDATE alerts SET suppressed = false, silenced_by = '{}' WHERE id = $1",
					item.id,
				); err != nil {
					return fmt.Errorf("release suppression for alert %d: %w", item.id, err)
				}
				incoming.Status = alerts.StatusFiring
				if err := insertStreamEvent(ctx, tx, "alert.updated", item.id, incoming, nil); err != nil {
					return err
				}
				result.Released++
				continue
			}
			stateChanged := current.Suppressed != item.suppressed
			if !stateChanged && sameStrings(current.SilencedBy, item.silencedBy) {
				continue
			}
			if _, err := tx.Exec(ctx,
				"UPDATE alerts SET suppressed = $1, silenced_by = $2 WHERE id = $3",
				current.Suppressed, current.SilencedBy, item.id,
			); err != nil {
				return fmt.Errorf("update suppression for alert %d: %w", item.id, err)
			}
			if !stateChanged {
				// The alert was already suppressed and stays suppressed; only
				// which silence covers it moved. That is bookkeeping, not news,
				// and pushing it down the stream would wake every open console
				// every time an operator re-silences a maintenance window.
				continue
			}
			incoming.Status = alerts.StatusFiring
			if err := insertStreamEvent(ctx, tx, "alert.updated", item.id, incoming, nil); err != nil {
				return err
			}
			if current.Suppressed {
				result.Suppressed++
			} else {
				result.Released++
			}
		}

		// Record that the source vouched for these alerts. Expiry measures
		// staleness from the later of last_seen and reconciled_at, so this is
		// what stops a silenced alert - one Alertmanager never sends a
		// notification for - being retired as stale while it is demonstrably
		// still firing. Reviving it afterwards would work, but only after it had
		// already vanished from the console for as long as the sweep took to
		// notice, and it would repeat every window.
		//
		// This is deliberately not written to last_seen, which means one
		// specific thing - when a webhook last delivered this alert - and is
		// also the console's default sort key and its pagination cursor.
		// Restamping it every pass would reshuffle the table under a reader and
		// carry rows across cursor boundaries.
		//
		// One statement for the whole source. It writes a row per live alert per
		// pass, which is the cost of the guarantee; a deployment holding far more
		// alerts than it can afford to restamp should lengthen
		// PROMVIEW_RECONCILE_INTERVAL rather than lose the evidence.
		if len(liveFingerprints) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE alerts SET reconciled_at = $1
				WHERE source_slug = $2 AND source_status = $3 AND fingerprint = ANY($4)
			`, occurredAt, sourceSlug, alerts.StatusFiring, liveFingerprints); err != nil {
				return fmt.Errorf("record reconciliation evidence for %s: %w", sourceSlug, err)
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

// sortedCopy returns the ids in a stable order so a stored set and a freshly
// read one compare by value rather than by whatever order the Alertmanager
// happened to serialise them in.
func sortedCopy(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted
}

func sameStrings(left, right []string) bool {
	return slices.Equal(left, right)
}

func anySilenceActive(ids []string, active map[string]bool) bool {
	for _, id := range ids {
		if active[id] {
			return true
		}
	}
	return false
}
