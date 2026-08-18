package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
)

/*
Grouping collapses a fan-out into one row per distinct key combination. A single
alerting rule commonly fires once per offending series — one production console
had 52 alerts under one alertname — and listing them individually buries
everything else on the page.

The aggregation runs through the same alertFilters() the flat list uses, which is
what makes the counts trustworthy: a reader whose grants restrict them to one
team sees group totals computed over their slice alone, never the true total of
a group they cannot open.
*/

// alertGroupKeys is the vocabulary a caller may group by. It is a whitelist
// rather than an arbitrary label because each key becomes a GROUP BY expression,
// and an unindexed label with unbounded cardinality is a sequential scan that
// produces one group per alert.
var alertGroupKeys = map[string]string{
	"alertname": "COALESCE(alert.labels->>'alertname', '')",
	"source":    "alert.source_slug",
	"team":      "COALESCE(alert.labels->>'team', '')",
	"severity":  "COALESCE(alert.labels->>'severity', 'warning')",
	"instance":  "COALESCE(alert.labels->>'instance', '')",
}

// maxGroupKeys bounds how finely a caller can slice the result. Beyond a few
// keys the grouping stops collapsing anything and just costs an aggregation.
const maxGroupKeys = 3

// severityBuckets are counted per group so the console can render the mix
// without loading the members.
var severityBuckets = []string{"critical", "warning", "info"}

// GroupAlerts summarises alerts into one row per distinct combination of the
// query's GroupBy keys, ordered by worst severity then recency, both descending.
func (store *Store) GroupAlerts(ctx context.Context, principal auth.Principal, query alerts.Query) (alerts.GroupResult, error) {
	keyExpressions, err := groupKeyExpressions(query.GroupBy)
	if err != nil {
		return alerts.GroupResult{}, err
	}
	if query.Limit < 1 {
		return alerts.GroupResult{}, errors.New("limit must be positive")
	}
	if query.GroupCursor != nil && query.GroupCursor.Query != query.CursorIdentity() {
		return alerts.GroupResult{}, errors.New("cursor does not match the current query")
	}

	streamCursor, err := store.readStreamCursor(ctx, principal)
	if err != nil {
		return alerts.GroupResult{}, err
	}
	where, args := alertFilters(principal, query, "alert")
	counts, totalAlerts, err := store.readSeverityCounts(ctx, where, args)
	if err != nil {
		return alerts.GroupResult{}, err
	}

	groupBy := groupByPositions(len(keyExpressions))
	var totalGroups int64
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT 1 FROM alerts AS alert`+where+`
			GROUP BY `+strings.Join(keyExpressions, ", ")+`
		) AS groups
	`, args...).Scan(&totalGroups); err != nil {
		return alerts.GroupResult{}, fmt.Errorf("count alert groups: %w", err)
	}

	listArgs := append([]any(nil), args...)
	having := ""
	if cursor := query.GroupCursor; cursor != nil {
		if len(cursor.Key) != len(keyExpressions) {
			return alerts.GroupResult{}, errors.New("cursor does not match the current grouping")
		}
		// Every ordering column descends, so one row comparison expresses the
		// whole keyset: strictly "after" the last row of the previous page.
		placeholders := make([]string, 0, len(cursor.Key)+2)
		listArgs = append(listArgs, cursor.SeverityRank, cursor.LatestLastSeen)
		placeholders = append(placeholders,
			fmt.Sprintf("$%d::integer", len(listArgs)-1),
			fmt.Sprintf("$%d::timestamptz", len(listArgs)),
		)
		for _, value := range cursor.Key {
			listArgs = append(listArgs, value)
			placeholders = append(placeholders, fmt.Sprintf("$%d::text", len(listArgs)))
		}
		having = fmt.Sprintf(" HAVING (max(%s), max(alert.last_seen), %s) < (%s)",
			severityRank, strings.Join(keyExpressions, ", "), strings.Join(placeholders, ", "))
	}
	listArgs = append(listArgs, query.Limit+1)

	selections := append([]string(nil), keyExpressions...)
	selections = append(selections,
		"count(*)",
		"count(*) FILTER (WHERE alert.acknowledged)",
		"max("+severityRank+")",
		"max(alert.last_seen)",
		"min(alert.starts_at)",
		"(array_agg(alert.id ORDER BY alert.last_seen DESC, alert.id DESC))[1]",
	)
	for _, severity := range severityBuckets {
		selections = append(selections, "count(*) FILTER (WHERE COALESCE(alert.labels->>'severity', 'warning') = '"+severity+"')")
	}
	selections = append(selections, "count(*) FILTER (WHERE COALESCE(alert.labels->>'severity', 'warning') NOT IN ('critical', 'warning', 'info'))")

	ordering := []string{"max(" + severityRank + ") DESC", "max(alert.last_seen) DESC"}
	for _, expression := range keyExpressions {
		ordering = append(ordering, expression+" DESC")
	}

	rows, err := store.pool.Query(ctx, `
		SELECT `+strings.Join(selections, ", ")+`
		FROM alerts AS alert`+where+`
		GROUP BY `+groupBy+having+`
		ORDER BY `+strings.Join(ordering, ", ")+`
		LIMIT $`+strconv.Itoa(len(listArgs)), listArgs...)
	if err != nil {
		return alerts.GroupResult{}, fmt.Errorf("list alert groups: %w", err)
	}
	defer rows.Close()

	groups := make([]alerts.Group, 0, query.Limit+1)
	ranks := make([]int, 0, query.Limit+1)
	keys := make([][]string, 0, query.Limit+1)
	for rows.Next() {
		keyValues := make([]string, len(keyExpressions))
		scanTargets := make([]any, 0, len(selections))
		for i := range keyValues {
			scanTargets = append(scanTargets, &keyValues[i])
		}
		var group alerts.Group
		var rank int
		var other int64
		bucketCounts := make([]int64, len(severityBuckets))
		scanTargets = append(scanTargets,
			&group.Total, &group.Acknowledged, &rank,
			&group.LatestLastSeen, &group.EarliestStartsAt, &group.SampleAlertID,
		)
		for i := range bucketCounts {
			scanTargets = append(scanTargets, &bucketCounts[i])
		}
		scanTargets = append(scanTargets, &other)
		if err := rows.Scan(scanTargets...); err != nil {
			return alerts.GroupResult{}, fmt.Errorf("scan alert group: %w", err)
		}

		group.Key = make(map[string]string, len(keyValues))
		for i, name := range query.GroupBy {
			group.Key[name] = keyValues[i]
		}
		group.SeverityCounts = make(map[string]int64, len(severityBuckets)+1)
		for i, severity := range severityBuckets {
			if bucketCounts[i] > 0 {
				group.SeverityCounts[severity] = bucketCounts[i]
			}
		}
		if other > 0 {
			group.SeverityCounts["other"] = other
		}
		group.WorstSeverity = severityForRank(rank)
		groups = append(groups, group)
		ranks = append(ranks, rank)
		keys = append(keys, keyValues)
	}
	if err := rows.Err(); err != nil {
		return alerts.GroupResult{}, fmt.Errorf("iterate alert groups: %w", err)
	}

	var next *alerts.GroupCursor
	if len(groups) > query.Limit {
		groups = groups[:query.Limit]
		last := groups[len(groups)-1]
		next = &alerts.GroupCursor{
			SeverityRank:   ranks[query.Limit-1],
			LatestLastSeen: last.LatestLastSeen,
			Key:            keys[query.Limit-1],
			Query:          query.CursorIdentity(),
		}
	}

	return alerts.GroupResult{
		Groups:         groups,
		NextCursor:     next,
		TotalGroups:    totalGroups,
		TotalAlerts:    totalAlerts,
		SeverityCounts: counts,
		StreamCursor:   streamCursor,
	}, nil
}

func groupKeyExpressions(groupBy []string) ([]string, error) {
	if len(groupBy) == 0 {
		return nil, errors.New("group by requires at least one key")
	}
	if len(groupBy) > maxGroupKeys {
		return nil, fmt.Errorf("group by accepts at most %d keys", maxGroupKeys)
	}
	seen := make(map[string]bool, len(groupBy))
	expressions := make([]string, 0, len(groupBy))
	for _, key := range groupBy {
		expression, ok := alertGroupKeys[key]
		if !ok {
			return nil, fmt.Errorf("cannot group by %q", key)
		}
		if seen[key] {
			return nil, fmt.Errorf("duplicate group key %q", key)
		}
		seen[key] = true
		expressions = append(expressions, expression)
	}
	return expressions, nil
}

func groupByPositions(count int) string {
	positions := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		positions = append(positions, strconv.Itoa(i))
	}
	return strings.Join(positions, ", ")
}

func severityForRank(rank int) string {
	switch rank {
	case 3:
		return "critical"
	case 2:
		return "warning"
	case 1:
		return "info"
	default:
		return "other"
	}
}

func (store *Store) readStreamCursor(ctx context.Context, principal auth.Principal) (int64, error) {
	streamAccess, streamArgs := readAccessCondition(principal, "event.labels", nil)
	previousAccess, streamArgs := readAccessCondition(principal, "event.previous_labels", streamArgs)
	var cursor int64
	if err := store.pool.QueryRow(ctx, `
		SELECT COALESCE(max(event.id), 0)
		FROM stream_events AS event
		WHERE (`+streamAccess+") OR (event.previous_labels IS NOT NULL AND ("+previousAccess+"))", streamArgs...).Scan(&cursor); err != nil {
		return 0, fmt.Errorf("read stream cursor: %w", err)
	}
	return cursor, nil
}

func (store *Store) readSeverityCounts(ctx context.Context, where string, args []any) (map[string]int64, int64, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT COALESCE(alert.labels->>'severity', 'warning'), count(*)
		FROM alerts AS alert`+where+`
		GROUP BY COALESCE(alert.labels->>'severity', 'warning')`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("count alerts: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int64)
	var total int64
	for rows.Next() {
		var severity string
		var count int64
		if err := rows.Scan(&severity, &count); err != nil {
			return nil, 0, fmt.Errorf("scan alert count: %w", err)
		}
		counts[severity] = count
		total += count
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate alert counts: %w", err)
	}
	return counts, total, nil
}
