package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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

// severityBuckets are counted per group so the console can render the mix
// without loading the members.
var severityBuckets = []string{"critical", "warning", "info"}

// GroupAlerts summarises alerts into one row per distinct combination of the
// query's GroupBy keys. Without an explicit sort it retains the console's
// severity-then-recency ordering; explicit sorts use a group aggregate.
func (store *Store) GroupAlerts(ctx context.Context, principal auth.Principal, query alerts.Query) (alerts.GroupResult, error) {
	keyExpressions, err := groupKeyExpressions(query.GroupBy)
	if err != nil {
		return alerts.GroupResult{}, err
	}
	if query.Limit < 1 {
		return alerts.GroupResult{}, errors.New("limit must be positive")
	}
	if query.Sort != "" {
		if _, ok := alertSorts[query.Sort]; !ok {
			return alerts.GroupResult{}, errors.New("sort is invalid")
		}
		if query.Order == "" {
			query.Order = alerts.DefaultOrder
		}
		if query.Order != "asc" && query.Order != "desc" {
			return alerts.GroupResult{}, errors.New("order must be asc or desc")
		}
	} else if query.Order != "" {
		return alerts.GroupResult{}, errors.New("order requires sort")
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
	ordering := []string{}
	sortExpression := ""
	sort := alertSort{}
	if cursor := query.GroupCursor; cursor != nil {
		if len(cursor.Key) != len(keyExpressions) {
			return alerts.GroupResult{}, errors.New("cursor does not match the current grouping")
		}
		if query.Sort == "" {
			if cursor.Sort != "severity" || cursor.Order != "desc" || cursor.LatestLastSeen.IsZero() {
				return alerts.GroupResult{}, errors.New("cursor does not match the current ordering")
			}
			placeholders := groupCursorPlaceholders(&listArgs, cursor.Key, cursor.SeverityRank, cursor.LatestLastSeen)
			having = fmt.Sprintf(" HAVING (max(%s), max(alert.last_seen), %s) < (%s)", severityRank, strings.Join(keyExpressions, ", "), strings.Join(placeholders, ", "))
		} else {
			sort = alertSorts[query.Sort]
			sortExpression = groupSortExpression(sort, query.Sort)
			if cursor.Sort != query.Sort || cursor.Order != query.Order || !validGroupCursorValue(cursor.Value, sort.cursorType) {
				return alerts.GroupResult{}, errors.New("cursor does not match the current ordering")
			}
			listArgs = append(listArgs, cursor.Value)
			placeholders := []string{fmt.Sprintf("$%d::%s", len(listArgs), sort.cursorType)}
			for _, value := range cursor.Key {
				listArgs = append(listArgs, value)
				placeholders = append(placeholders, fmt.Sprintf("$%d::text", len(listArgs)))
			}
			operator := ">"
			if query.Order == "desc" {
				operator = "<"
			}
			having = fmt.Sprintf(" HAVING (%s, %s) %s (%s)", sortExpression, strings.Join(keyExpressions, ", "), operator, strings.Join(placeholders, ", "))
		}
	}
	if query.Sort == "" {
		ordering = []string{"max(" + severityRank + ") DESC", "max(alert.last_seen) DESC"}
	} else {
		if sortExpression == "" {
			sort = alertSorts[query.Sort]
			sortExpression = groupSortExpression(sort, query.Sort)
		}
		ordering = []string{sortExpression + " " + strings.ToUpper(query.Order)}
	}
	for _, expression := range keyExpressions {
		ordering = append(ordering, expression+" "+strings.ToUpper(groupOrder(query)))
	}
	listArgs = append(listArgs, query.Limit+1)

	selections := append([]string(nil), keyExpressions...)
	selections = append(selections,
		"count(*)",
		"count(*) FILTER (WHERE alert.acknowledged)",
		"count(*) FILTER (WHERE alert.suppressed)",
		"max("+severityRank+")",
		"max(alert.last_seen)",
		"min(alert.starts_at)",
		"(array_agg(alert.id ORDER BY alert.last_seen DESC, alert.id DESC))[1]",
	)
	if query.Sort != "" {
		selections = append(selections, sortExpression)
	}
	for _, severity := range severityBuckets {
		selections = append(selections, "count(*) FILTER (WHERE COALESCE(alert.labels->>'severity', 'warning') = '"+severity+"')")
	}
	selections = append(selections, "count(*) FILTER (WHERE COALESCE(alert.labels->>'severity', 'warning') NOT IN ('critical', 'warning', 'info'))")

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
	values := make([]string, 0, query.Limit+1)
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
			&group.Total, &group.Acknowledged, &group.Silenced, &rank,
			&group.LatestLastSeen, &group.EarliestStartsAt, &group.SampleAlertID,
		)
		var value any
		if query.Sort != "" {
			value = groupCursorScanTarget(sort.cursorType)
			scanTargets = append(scanTargets, value)
		}
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
		if query.Sort != "" {
			values = append(values, groupCursorValue(value))
		}
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
			Sort:           groupSort(query),
			Order:          groupOrder(query),
			Query:          query.CursorIdentity(),
		}
		if query.Sort != "" {
			next.Value = values[query.Limit-1]
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
	if err := alerts.ValidateGroupBy(groupBy); err != nil {
		return nil, err
	}
	expressions := make([]string, 0, len(groupBy))
	for _, key := range groupBy {
		if key == "source" {
			expressions = append(expressions, "alert.source_slug")
			continue
		}
		// ValidateGroupBy permits only ASCII Prometheus label identifiers, so
		// embedding the key in this quoted literal cannot alter the SQL.
		expressions = append(expressions, "COALESCE(alert.labels->>'"+key+"', '')")
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

// Explicit group sorts use one stable value per group: the newest last-seen,
// earliest start, worst severity, or lexicographically greatest text value.
func groupSortExpression(sort alertSort, name string) string {
	if name == "startsAt" {
		return "min(" + sort.expression + ")"
	}
	return "max(" + sort.expression + ")"
}

func groupSort(query alerts.Query) string {
	if query.Sort == "" {
		return "severity"
	}
	return query.Sort
}

func groupOrder(query alerts.Query) string {
	if query.Sort == "" || query.Order == "" {
		return "desc"
	}
	return query.Order
}

func groupCursorPlaceholders(args *[]any, key []string, rank int, lastSeen time.Time) []string {
	*args = append(*args, rank, lastSeen)
	placeholders := []string{
		fmt.Sprintf("$%d::integer", len(*args)-1),
		fmt.Sprintf("$%d::timestamptz", len(*args)),
	}
	for _, value := range key {
		*args = append(*args, value)
		placeholders = append(placeholders, fmt.Sprintf("$%d::text", len(*args)))
	}
	return placeholders
}

func validGroupCursorValue(value, cursorType string) bool {
	switch cursorType {
	case "timestamptz":
		_, err := time.Parse(time.RFC3339Nano, value)
		return err == nil
	case "integer":
		parsed, err := strconv.Atoi(value)
		return err == nil && parsed >= 0 && parsed <= 3
	default:
		return true
	}
}

func groupCursorScanTarget(cursorType string) any {
	switch cursorType {
	case "timestamptz":
		return new(time.Time)
	case "integer":
		return new(int)
	default:
		return new(string)
	}
}

func groupCursorValue(value any) string {
	switch value := value.(type) {
	case *time.Time:
		return value.UTC().Format(time.RFC3339Nano)
	case *int:
		return strconv.Itoa(*value)
	case *string:
		return *value
	default:
		panic("unsupported group cursor value")
	}
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
