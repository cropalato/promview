package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
)

/*
Resolving what a silence will match, and where it has to be created.

Both directions are answered inside the operator's authorization scope: an
operator who may only act on their own team's alerts must not be able to silence
another team's by naming a group that spans both. The scope query therefore
carries the same operateAccessCondition the acknowledge path uses, and a group
resolves to the sources whose in-scope members it actually has.
*/

// SilenceScopeForAlert resolves one alert into its full label set and the single
// Alertmanager that delivered it.
func (store *Store) SilenceScopeForAlert(
	ctx context.Context,
	principal auth.Principal,
	id int64,
) (alerts.SilenceScope, error) {
	if !principal.CanOperate() {
		return alerts.SilenceScope{}, alerts.ErrNotFound
	}
	access, args := operateAccessCondition(principal, "alert.labels", []any{id})
	var labelsJSON []byte
	var target alerts.SilenceTarget
	err := store.pool.QueryRow(ctx, `
		SELECT alert.labels, alert.source_slug, source.alertmanager_url, source.alertmanager_token
		FROM alerts AS alert
		JOIN alert_sources AS source ON source.slug = alert.source_slug
		WHERE alert.id = $1 AND (`+access+`)
	`, args...).Scan(&labelsJSON, &target.Source, &target.AlertmanagerURL, &target.AlertmanagerToken)
	if errors.Is(err, pgx.ErrNoRows) {
		// Out of scope reads the same as absent, so a silence attempt cannot be
		// used to probe for alerts the operator may not act on.
		return alerts.SilenceScope{}, alerts.ErrNotFound
	}
	if err != nil {
		return alerts.SilenceScope{}, fmt.Errorf("read silence scope for alert %d: %w", id, err)
	}
	labels := map[string]string{}
	if err := json.Unmarshal(labelsJSON, &labels); err != nil {
		return alerts.SilenceScope{}, fmt.Errorf("decode labels for alert %d: %w", id, err)
	}
	if len(labels) == 0 {
		return alerts.SilenceScope{}, errors.New("alert has no labels to silence on")
	}
	if target.AlertmanagerURL == "" {
		return alerts.SilenceScope{}, alerts.ErrNoSilenceTarget
	}
	target.Labels = labels
	target.Members = 1
	return alerts.SilenceScope{Labels: labels, Targets: []alerts.SilenceTarget{target}}, nil
}

// SilenceScopeForGroup resolves a grouping key into the labels a silence matches
// on and every Alertmanager holding an in-scope member of that group.
func (store *Store) SilenceScopeForGroup(
	ctx context.Context,
	principal auth.Principal,
	groupBy []string,
	key map[string]string,
) (alerts.SilenceScope, error) {
	if !principal.CanOperate() {
		return alerts.SilenceScope{}, alerts.ErrNotFound
	}
	if err := alerts.ValidateGroupBy(groupBy); err != nil {
		return alerts.SilenceScope{}, err
	}

	labels := map[string]string{}
	conditions := make([]string, 0, len(groupBy))
	args := make([]any, 0, len(groupBy))
	for _, name := range groupBy {
		value, ok := key[name]
		if !ok {
			return alerts.SilenceScope{}, fmt.Errorf("group key is missing %q", name)
		}
		args = append(args, value)
		if name == "source" {
			// A promview source slug is not an alert label; Alertmanager has
			// never heard of it. It narrows which Alertmanager to write to
			// instead of narrowing what the silence matches.
			conditions = append(conditions, fmt.Sprintf("alert.source_slug = $%d", len(args)))
			continue
		}
		// ValidateGroupBy permits only ASCII Prometheus label identifiers, so
		// embedding the key in this quoted literal cannot alter the SQL.
		conditions = append(conditions, fmt.Sprintf("COALESCE(alert.labels->>'%s', '') = $%d", name, len(args)))
		labels[name] = value
	}
	if len(labels) == 0 {
		// Grouping by source alone leaves nothing to match on, and a silence
		// with no matchers silences the whole Alertmanager.
		return alerts.SilenceScope{}, errors.New("a group keyed only by source has no labels to silence on")
	}

	access, args := operateAccessCondition(principal, "alert.labels", args)
	// The grouping key is the coarsest match that covers the group, not the
	// narrowest. Members almost always agree on far more than the two or three
	// keys they were grouped by, and a silence written on the key alone hides
	// every future alert sharing it - including ones nobody has seen yet. So
	// the match is widened back out to every label the members actually agree
	// on, folded per source for the reason SilenceTarget.Labels explains.
	rows, err := store.pool.Query(ctx, `
		WITH scoped AS (
			SELECT alert.id, alert.source_slug, alert.labels
			FROM alerts AS alert
			WHERE `+strings.Join(conditions, " AND ")+`
			  AND alert.source_status = 'firing'
			  AND (`+access+`)
		), totals AS (
			SELECT source_slug, count(*) AS members
			FROM scoped
			GROUP BY source_slug
		), common AS (
			SELECT scoped.source_slug, pair.key, min(pair.value) AS value
			FROM scoped, jsonb_each_text(scoped.labels) AS pair
			GROUP BY scoped.source_slug, pair.key
			HAVING count(*) = (SELECT members FROM totals WHERE totals.source_slug = scoped.source_slug)
			   AND min(pair.value) = max(pair.value)
		)
		SELECT totals.source_slug,
		       source.alertmanager_url,
		       source.alertmanager_token,
		       totals.members,
		       COALESCE(jsonb_object_agg(common.key, common.value)
		                FILTER (WHERE common.key IS NOT NULL), '{}'::jsonb)
		FROM totals
		JOIN alert_sources AS source ON source.slug = totals.source_slug
		LEFT JOIN common ON common.source_slug = totals.source_slug
		GROUP BY totals.source_slug, source.alertmanager_url, source.alertmanager_token, totals.members
		ORDER BY totals.source_slug
	`, args...)
	if err != nil {
		return alerts.SilenceScope{}, fmt.Errorf("read silence scope for group: %w", err)
	}
	defer rows.Close()

	targets := make([]alerts.SilenceTarget, 0, 2)
	members := 0
	for rows.Next() {
		var target alerts.SilenceTarget
		var commonJSON []byte
		if err := rows.Scan(
			&target.Source, &target.AlertmanagerURL, &target.AlertmanagerToken,
			&target.Members, &commonJSON,
		); err != nil {
			return alerts.SilenceScope{}, fmt.Errorf("scan silence target: %w", err)
		}
		members += target.Members
		if target.AlertmanagerURL == "" {
			// A source with no Alertmanager is not reconciled either; skip it
			// rather than failing the whole group for one unconfigured source.
			continue
		}
		common := map[string]string{}
		if err := json.Unmarshal(commonJSON, &common); err != nil {
			return alerts.SilenceScope{}, fmt.Errorf("decode common labels for %s: %w", target.Source, err)
		}
		// The key always survives, even where the fold could not see it: a key
		// whose value is the empty string matches members that carry no such
		// label at all, so jsonb_each_text never emitted a pair for it.
		for name, value := range labels {
			common[name] = value
		}
		target.Labels = common
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return alerts.SilenceScope{}, fmt.Errorf("read silence targets: %w", err)
	}
	if members == 0 {
		return alerts.SilenceScope{}, alerts.ErrNotFound
	}
	if len(targets) == 0 {
		return alerts.SilenceScope{}, alerts.ErrNoSilenceTarget
	}
	return alerts.SilenceScope{Labels: alerts.CommonLabels(targets), Targets: targets}, nil
}

// SilenceRemovalScope resolves which Alertmanager to expire a silence on, and
// proves the operator is entitled to.
//
// Entitlement is anchored on an alert rather than on the silence: a silence id
// is an opaque token an operator could name without having any right to it, and
// a bare `DELETE /silences/{id}` would let anyone who may operate on anything
// un-hide anything. Requiring the silence to be one currently holding back an
// in-scope alert reuses the authorization that already exists and keeps the
// blast radius to what the operator can see.
//
// The consequence is deliberate: a preventive silence matching no alert cannot
// be reached this way. It has no row to act from, and inventing one would mean
// inventing an authorization rule too.
func (store *Store) SilenceRemovalScope(
	ctx context.Context,
	principal auth.Principal,
	alertID int64,
	silenceID string,
) (alerts.SilenceTarget, error) {
	if !principal.CanOperate() {
		return alerts.SilenceTarget{}, alerts.ErrNotFound
	}
	if strings.TrimSpace(silenceID) == "" {
		return alerts.SilenceTarget{}, errors.New("a silence id is required")
	}
	access, args := operateAccessCondition(principal, "alert.labels", []any{alertID})
	var target alerts.SilenceTarget
	var silencedBy []string
	var labelsJSON []byte
	err := store.pool.QueryRow(ctx, `
		SELECT alert.labels, alert.source_slug, alert.silenced_by,
		       source.alertmanager_url, source.alertmanager_token
		FROM alerts AS alert
		JOIN alert_sources AS source ON source.slug = alert.source_slug
		WHERE alert.id = $1 AND (`+access+`)
	`, args...).Scan(&labelsJSON, &target.Source, &silencedBy,
		&target.AlertmanagerURL, &target.AlertmanagerToken)
	if errors.Is(err, pgx.ErrNoRows) {
		// Out of scope reads the same as absent, so a removal attempt cannot be
		// used to probe for alerts the operator may not act on.
		return alerts.SilenceTarget{}, alerts.ErrNotFound
	}
	if err != nil {
		return alerts.SilenceTarget{}, fmt.Errorf("read silence removal scope for alert %d: %w", alertID, err)
	}
	if !slices.Contains(silencedBy, silenceID) {
		// Either the id is not this alert's, or the suppression has already
		// lifted. Both are "there is nothing here to remove", and saying which
		// would leak whether the id exists elsewhere.
		return alerts.SilenceTarget{}, alerts.ErrNotFound
	}
	if target.AlertmanagerURL == "" {
		return alerts.SilenceTarget{}, alerts.ErrNoSilenceTarget
	}
	labels := map[string]string{}
	if err := json.Unmarshal(labelsJSON, &labels); err != nil {
		return alerts.SilenceTarget{}, fmt.Errorf("decode labels for alert %d: %w", alertID, err)
	}
	target.Labels = labels
	target.Members = 1
	return target, nil
}

// ExpireSilenceRecord marks a stored silence as no longer suppressing, right
// after promview expired it on the Alertmanager.
//
// The next sync would do this on its own. Doing it here means the drawer stops
// calling the silence live the moment the removal returns, including in a
// deployment where reconciliation is switched off and no sync is coming.
func (store *Store) ExpireSilenceRecord(ctx context.Context, sourceSlug, silenceID string, now time.Time) error {
	if _, err := store.pool.Exec(ctx, `
		UPDATE alertmanager_silences SET
			state = 'expired',
			ends_at = LEAST(ends_at, $3)
		WHERE source_slug = $1 AND silence_id = $2
	`, sourceSlug, silenceID, now.UTC()); err != nil {
		return fmt.Errorf("expire silence record %s: %w", silenceID, err)
	}
	return nil
}

// RecordSilence remembers a silence promview created. Alertmanager owns the
// live state; the sync re-reads it every reconcile pass, and this row written
// at creation is what still answers "who silenced this, and why" after the
// silence itself is gone.
//
// A repeated (source, id) is an upsert rather than an error: the id comes from
// Alertmanager, and a retry that lands twice should not fail a silence that
// actually worked.
func (store *Store) RecordSilence(ctx context.Context, record alerts.SilenceRecord) error {
	matchersJSON, err := json.Marshal(record.Matchers)
	if err != nil {
		return fmt.Errorf("encode silence matchers: %w", err)
	}
	// Promview only writes equality matchers, so the full-fidelity list is
	// derived rather than asked for.
	listJSON, err := json.Marshal(equalityMatcherList(record.Matchers))
	if err != nil {
		return fmt.Errorf("encode silence matcher list: %w", err)
	}
	_, err = store.pool.Exec(ctx, `
		INSERT INTO alertmanager_silences
			(source_slug, silence_id, matchers, created_by, comment, starts_at, ends_at, state, matcher_list)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8)
		ON CONFLICT (source_slug, silence_id) DO UPDATE SET
			matchers = EXCLUDED.matchers,
			created_by = EXCLUDED.created_by,
			comment = EXCLUDED.comment,
			starts_at = EXCLUDED.starts_at,
			ends_at = EXCLUDED.ends_at,
			state = EXCLUDED.state,
			matcher_list = EXCLUDED.matcher_list
	`, record.Source, record.SilenceID, matchersJSON, record.CreatedBy, record.Comment,
		record.StartsAt.UTC(), record.EndsAt.UTC(), listJSON)
	if err != nil {
		return fmt.Errorf("record silence %s: %w", record.SilenceID, err)
	}
	return nil
}

// silenceRecords reads back the stored silences with these ids. An id with no
// record — a silence made straight on the Alertmanager that no sync has read
// yet — simply does not come back: it is still real and still suppressing, and
// the console says so without inventing an author for it.
func (store *Store) silenceRecords(ctx context.Context, sourceSlug string, ids []string) ([]alerts.SilenceRecord, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := store.pool.Query(ctx, `
		SELECT source_slug, silence_id, matchers, created_by, comment, starts_at, ends_at, state, matcher_list
		FROM alertmanager_silences
		WHERE source_slug = $1 AND silence_id = ANY($2)
		ORDER BY ends_at DESC
	`, sourceSlug, ids)
	if err != nil {
		return nil, fmt.Errorf("read silence records: %w", err)
	}
	defer rows.Close()
	records := make([]alerts.SilenceRecord, 0, len(ids))
	for rows.Next() {
		var record alerts.SilenceRecord
		var matchersJSON, listJSON []byte
		var startsAt, endsAt time.Time
		if err := rows.Scan(&record.Source, &record.SilenceID, &matchersJSON,
			&record.CreatedBy, &record.Comment, &startsAt, &endsAt,
			&record.State, &listJSON); err != nil {
			return nil, fmt.Errorf("scan silence record: %w", err)
		}
		if err := json.Unmarshal(matchersJSON, &record.Matchers); err != nil {
			return nil, fmt.Errorf("decode silence matchers for %s: %w", record.SilenceID, err)
		}
		if err := json.Unmarshal(listJSON, &record.MatcherList); err != nil {
			return nil, fmt.Errorf("decode silence matcher list for %s: %w", record.SilenceID, err)
		}
		record.StartsAt = startsAt.UTC()
		record.EndsAt = endsAt.UTC()
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate silence records: %w", err)
	}
	return records, nil
}
