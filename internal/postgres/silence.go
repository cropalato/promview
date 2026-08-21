package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
	rows, err := store.pool.Query(ctx, `
		SELECT DISTINCT alert.source_slug, source.alertmanager_url, source.alertmanager_token
		FROM alerts AS alert
		JOIN alert_sources AS source ON source.slug = alert.source_slug
		WHERE `+strings.Join(conditions, " AND ")+`
		  AND alert.source_status = 'firing'
		  AND (`+access+`)
		ORDER BY alert.source_slug
	`, args...)
	if err != nil {
		return alerts.SilenceScope{}, fmt.Errorf("read silence scope for group: %w", err)
	}
	defer rows.Close()

	targets := make([]alerts.SilenceTarget, 0, 2)
	members := 0
	for rows.Next() {
		var target alerts.SilenceTarget
		if err := rows.Scan(&target.Source, &target.AlertmanagerURL, &target.AlertmanagerToken); err != nil {
			return alerts.SilenceScope{}, fmt.Errorf("scan silence target: %w", err)
		}
		members++
		if target.AlertmanagerURL == "" {
			// A source with no Alertmanager is not reconciled either; skip it
			// rather than failing the whole group for one unconfigured source.
			continue
		}
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
	return alerts.SilenceScope{Labels: labels, Targets: targets}, nil
}
