package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

/*
Stream events exist so a client that lost its connection can resume where it
stopped. Nothing else reads them, so almost all of them are dead weight within
minutes of being written - and until now nothing ever removed one.

Pruning them is straightforward. Doing it safely is not: a client resuming from
a cursor older than what survives would receive the events that remain, miss the
ones that went, and report no error at all. A console that quietly disagrees
with the database about which alerts are firing is a worse outcome than a table
that grows, so the watermark and the gap signal are part of this rather than a
later refinement.
*/

// streamPruneBatchSize bounds one delete so a long-neglected table is worked
// through in steady chunks rather than one statement holding locks over
// everything at once.
const streamPruneBatchSize = 1000

// PruneStreamEvents deletes stream events older than the retention window and
// advances the watermark that tells resuming clients whether they missed
// anything. It returns how many events were removed.
//
// A zero or negative window disables pruning entirely, which keeps the table
// growing but guarantees no client is ever asked to re-snapshot.
func (store *Store) PruneStreamEvents(ctx context.Context, retention time.Duration, now time.Time) (int, error) {
	if retention < 0 {
		return 0, errors.New("stream retention must not be negative")
	}
	if retention == 0 {
		return 0, nil
	}
	cutoff := now.UTC().Add(-retention)
	total := 0
	for {
		pruned, err := store.pruneStreamBatch(ctx, cutoff)
		total += pruned
		if err != nil {
			return total, err
		}
		if pruned < streamPruneBatchSize {
			return total, nil
		}
	}
}

func (store *Store) pruneStreamBatch(ctx context.Context, cutoff time.Time) (int, error) {
	pruned := 0
	err := pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		var highest int64
		var count int
		// Oldest first, so the watermark only ever moves forward over a
		// contiguous run. Deleting an arbitrary middle would leave a hole no
		// watermark can describe, and a client resuming into that hole would be
		// told everything was fine.
		if err := tx.QueryRow(ctx, `
			WITH victims AS (
				SELECT id FROM stream_events
				WHERE occurred_at < $1
				ORDER BY id
				LIMIT $2
				FOR UPDATE SKIP LOCKED
			), removed AS (
				DELETE FROM stream_events
				WHERE id IN (SELECT id FROM victims)
				RETURNING id
			)
			SELECT COALESCE(max(id), 0), count(*) FROM removed
		`, cutoff, streamPruneBatchSize).Scan(&highest, &count); err != nil {
			return fmt.Errorf("prune stream events: %w", err)
		}
		if count == 0 {
			return nil
		}
		// GREATEST rather than assignment: two sweeps racing must not move the
		// watermark backwards, which would tell a client it had missed nothing.
		if _, err := tx.Exec(ctx, `
			UPDATE stream_event_retention
			SET deleted_through = GREATEST(deleted_through, $1)
			WHERE singleton
		`, highest); err != nil {
			return fmt.Errorf("advance stream retention watermark: %w", err)
		}
		pruned = count
		return nil
	})
	if err != nil {
		return 0, err
	}
	return pruned, nil
}
