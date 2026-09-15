package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cropalato/promview/internal/alertmanager"
	"github.com/cropalato/promview/internal/alerts"
)

/*
Syncing the silence inventory with what one Alertmanager actually reports.

The stored rows started life as promview's own provenance diary. What the sync
adds is the other half of the truth: silences created straight on the
Alertmanager get a record with their real author instead of an invented blank,
and a silence that vanished — expired, or deleted by someone promview never
heard from — stops reading as live.

Every listed silence is kept, matched to alerts or not. A preventive silence
written ahead of a maintenance window matches nothing yet; one whose alerts
already cleared matches nothing anymore. Both are real, and the sync never
joins against alerts to decide what to store.
*/

// SyncSilences upserts the silences one Alertmanager reports and marks the
// stored ones it no longer lists as expired. An Alertmanager keeps expired
// silences visible for days before garbage-collecting them, so a silence
// missing from the listing entirely is one that ended a while ago or was
// deleted; either way it is not suppressing anything.
func (store *Store) SyncSilences(
	ctx context.Context,
	sourceSlug string,
	listed []alertmanager.ListedSilence,
	now time.Time,
) error {
	return pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		ids := make([]string, 0, len(listed))
		for _, silence := range listed {
			ids = append(ids, silence.ID)
			equality := equalityMatcherMap(silence.Matchers)
			matchersJSON, err := json.Marshal(equality)
			if err != nil {
				return fmt.Errorf("encode silence matchers for %s: %w", silence.ID, err)
			}
			listJSON, err := json.Marshal(matcherList(silence.Matchers))
			if err != nil {
				return fmt.Errorf("encode silence matcher list for %s: %w", silence.ID, err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO alertmanager_silences
					(source_slug, silence_id, matchers, created_by, comment, starts_at, ends_at, state, matcher_list)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				ON CONFLICT (source_slug, silence_id) DO UPDATE SET
					matchers = EXCLUDED.matchers,
					created_by = EXCLUDED.created_by,
					comment = EXCLUDED.comment,
					starts_at = EXCLUDED.starts_at,
					ends_at = EXCLUDED.ends_at,
					state = EXCLUDED.state,
					matcher_list = EXCLUDED.matcher_list
			`, sourceSlug, silence.ID, matchersJSON, silence.CreatedBy, silence.Comment,
				silence.StartsAt.UTC(), silence.EndsAt.UTC(), silence.State, listJSON); err != nil {
				return fmt.Errorf("sync silence %s: %w", silence.ID, err)
			}
		}
		// Ends_at is brought forward for a silence that was deleted early, so a
		// record read later does not claim it ran its full course. A silence the
		// listing merely omitted this pass and still lists the next is put back
		// by the same upsert above.
		if _, err := tx.Exec(ctx, `
			UPDATE alertmanager_silences SET
				state = 'expired',
				ends_at = LEAST(ends_at, $3)
			WHERE source_slug = $1 AND state <> 'expired' AND NOT (silence_id = ANY($2))
		`, sourceSlug, ids, now.UTC()); err != nil {
			return fmt.Errorf("expire vanished silences for %s: %w", sourceSlug, err)
		}
		return nil
	})
}

// equalityMatcherMap keeps only the matchers the legacy map can say honestly:
// plain equality. A regex or a negation flattened into `name=value` would claim
// a match the silence does not make.
func equalityMatcherMap(matchers []alertmanager.Matcher) map[string]string {
	equality := map[string]string{}
	for _, matcher := range matchers {
		if matcher.IsEqual && !matcher.IsRegex {
			equality[matcher.Name] = matcher.Value
		}
	}
	return equality
}

// matcherList restates the client's matchers in the alerts package's shape,
// which exists only because the import runs the other way.
func matcherList(matchers []alertmanager.Matcher) []alerts.SilenceMatcher {
	list := make([]alerts.SilenceMatcher, 0, len(matchers))
	for _, matcher := range matchers {
		list = append(list, alerts.SilenceMatcher{
			Name:    matcher.Name,
			Value:   matcher.Value,
			IsRegex: matcher.IsRegex,
			IsEqual: matcher.IsEqual,
		})
	}
	return list
}

// equalityMatcherList derives the full-fidelity list from an equality map, in a
// stable order, for the one writer that only has the map: promview recording a
// silence it just created.
func equalityMatcherList(matchers map[string]string) []alerts.SilenceMatcher {
	names := make([]string, 0, len(matchers))
	for name := range matchers {
		names = append(names, name)
	}
	sort.Strings(names)
	list := make([]alerts.SilenceMatcher, 0, len(names))
	for _, name := range names {
		list = append(list, alerts.SilenceMatcher{
			Name: name, Value: matchers[name], IsEqual: true,
		})
	}
	return list
}
