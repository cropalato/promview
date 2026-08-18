package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
)

/*
Expiry closes the gap left when a source stops reporting an alert without ever
sending a resolved notification. Alertmanager suppresses resolved notifications
for silenced alerts, so an alert that clears inside a maintenance window is never
announced; a delivery outage has the same effect. Without a sweep those alerts
stay firing forever and the console's counts drift away from reality.

The window is resolved per alert rather than globally, because it has to exceed
the source Alertmanager's repeat_interval — a shorter window would expire a live
alert and let the next repeat notification resurrect it.
*/

// expiryWindow resolves how long an alert may go unreported before it expires:
// an explicit numeric timeout label wins, then the source's own window, then the
// server default in $1. Alerta's Prometheus webhook honours a timeout label the
// same way, so rules that already carry one behave consistently across both.
const expiryWindow = `COALESCE(
			CASE WHEN alert.labels->>'timeout' ~ '^[0-9]+$'
			     THEN (alert.labels->>'timeout')::bigint * interval '1 second' END,
			source.stale_after,
			$1::interval
		)`

// expiryBatchSize bounds one sweep transaction so a large backlog is worked
// through in steady chunks instead of one long-running lock-heavy statement.
const expiryBatchSize = 500

// ExpireStaleAlerts marks firing alerts whose source went quiet past its window
// as expired, recording an alert.expired history entry and an alert.resolved
// stream event for each. It returns how many alerts were expired.
//
// A zero window (server default or per-source) disables expiry for the alerts it
// covers, which is the escape hatch for a source whose repeat_interval is unknown.
func (store *Store) ExpireStaleAlerts(ctx context.Context, defaultStaleAfter time.Duration, now time.Time) (int, error) {
	if defaultStaleAfter < 0 {
		return 0, errors.New("default stale-after must not be negative")
	}
	total := 0
	for {
		expired, err := store.expireStaleBatch(ctx, defaultStaleAfter, now)
		total += expired
		if err != nil {
			return total, err
		}
		if expired < expiryBatchSize {
			return total, nil
		}
	}
}

func (store *Store) expireStaleBatch(ctx context.Context, defaultStaleAfter time.Duration, now time.Time) (int, error) {
	expired := 0
	err := pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		// SKIP LOCKED keeps the sweep and concurrent ingestion out of each
		// other's way: an alert being written right now is simply left for the
		// next pass instead of blocking either side.
		rows, err := tx.Query(ctx, `
			SELECT alert.id, alert.source_slug, alert.fingerprint, alert.labels, alert.annotations, alert.occurrence
			FROM alerts AS alert
			JOIN alert_sources AS source ON source.slug = alert.source_slug
			WHERE alert.source_status = 'firing'
			  AND `+expiryWindow+` > interval '0'
			  AND alert.last_seen < $2::timestamptz - `+expiryWindow+`
			ORDER BY alert.last_seen
			LIMIT $3
			FOR UPDATE OF alert SKIP LOCKED
		`, intervalFromDuration(defaultStaleAfter), now, expiryBatchSize)
		if err != nil {
			return fmt.Errorf("select stale alerts: %w", err)
		}

		type staleAlert struct {
			id          int64
			sourceSlug  string
			fingerprint string
			labels      map[string]string
			annotations map[string]string
			occurrence  int
		}
		var stale []staleAlert
		for rows.Next() {
			var item staleAlert
			var labelsJSON, annotationsJSON []byte
			if err := rows.Scan(&item.id, &item.sourceSlug, &item.fingerprint, &labelsJSON, &annotationsJSON, &item.occurrence); err != nil {
				rows.Close()
				return fmt.Errorf("scan stale alert: %w", err)
			}
			if err := json.Unmarshal(labelsJSON, &item.labels); err != nil {
				rows.Close()
				return fmt.Errorf("decode labels for alert %d: %w", item.id, err)
			}
			if err := json.Unmarshal(annotationsJSON, &item.annotations); err != nil {
				rows.Close()
				return fmt.Errorf("decode annotations for alert %d: %w", item.id, err)
			}
			stale = append(stale, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate stale alerts: %w", err)
		}
		rows.Close()
		if len(stale) == 0 {
			return nil
		}

		ids := make([]int64, 0, len(stale))
		for _, item := range stale {
			ids = append(ids, item.id)
		}
		// ends_at is preserved when the source already supplied one; otherwise
		// the sweep time is the best available answer for when it stopped.
		if _, err := tx.Exec(ctx, `
			UPDATE alerts SET
				source_status = $1,
				ends_at = COALESCE(ends_at, $2)
			WHERE id = ANY($3)
		`, alerts.StatusExpired, now, ids); err != nil {
			return fmt.Errorf("expire alerts: %w", err)
		}

		for _, item := range stale {
			incoming := alertmanager.IncomingAlert{
				SourceSlug:  item.sourceSlug,
				Fingerprint: item.fingerprint,
				Status:      alerts.StatusExpired,
				Labels:      item.labels,
				Annotations: item.annotations,
				ReceivedAt:  now,
			}
			// The stream carries alert.resolved because expiry means the alert
			// left the firing view, which is what every consumer needs to react
			// to. History carries alert.expired, where the distinction between
			// "the source said so" and "the source went quiet" is preserved.
			if err := insertStreamEvent(ctx, tx, "alert.resolved", item.id, incoming, nil); err != nil {
				return err
			}
			if err := insertHistoryEvent(ctx, tx, item.id, item.occurrence, "alert.expired", alerts.StatusExpired, now); err != nil {
				return err
			}
		}
		expired = len(stale)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return expired, nil
}

func intervalFromDuration(value time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: value.Microseconds(), Valid: true}
}

func staleAfterInterval(value *time.Duration) *pgtype.Interval {
	if value == nil {
		return nil
	}
	interval := intervalFromDuration(*value)
	return &interval
}
