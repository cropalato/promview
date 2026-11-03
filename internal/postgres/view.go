package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/cropalato/promview/internal/alerts"
	"github.com/cropalato/promview/internal/auth"
)

// resolvedView is a client's view options joined to the selectors derived from
// the caller's grants. The selectors are derived here, from the principal the
// request was already authorized as, rather than carried by the client: a
// binding changed in the database applies to the next request, and a client
// cannot supply a selector of its own to be trusted.
type resolvedView struct {
	groups  [][]alerts.LabelMatcher
	options alerts.ViewOptions
	// requested is false when the caller asked for no view at all.
	requested bool
}

// active reports whether the caller has selectors to view through. Without
// any, the view is no filter: an empty selector list must never read as
// "nothing matches".
func (view resolvedView) active() bool {
	return view.requested && len(view.groups) > 0
}

// filtering reports whether the view narrows the result. Showing everything
// still classifies, but removes nothing.
func (view resolvedView) filtering() bool {
	return view.active() && !view.options.ShowAll
}

func resolveView(principal auth.Principal, options *alerts.ViewOptions) resolvedView {
	if options == nil {
		return resolvedView{}
	}
	view := resolvedView{options: *options, requested: true}
	for _, group := range auth.ViewMatchers(principal) {
		converted := make([]alerts.LabelMatcher, 0, len(group))
		for _, matcher := range group {
			converted = append(converted, alerts.LabelMatcher{
				Name: matcher.Name, Operator: matcher.Operator, Value: matcher.Value,
			})
		}
		view.groups = append(view.groups, converted)
	}
	return view
}

// classify places an alert's labels relative to the view, or returns the empty
// match when the caller has none.
func (view resolvedView) classify(labels map[string]string) alerts.ViewMatch {
	if !view.active() {
		return ""
	}
	return alerts.ClassifyView(view.groups, view.options, labels)
}

// includes reports whether the view lets these labels through. It is the Go
// twin of visibleCondition and must agree with it, because the stream decides
// per event in Go what the snapshot decided per row in SQL.
func (view resolvedView) includes(labels map[string]string) bool {
	if !view.filtering() {
		return true
	}
	return view.classify(labels) != alerts.ViewMatchOutside
}

// visibleCondition is the SQL for "this row is in the view, or is critical and
// critical alerts ignore the view". It is only ever ANDed with the scope
// condition, never used in place of it.
func (view resolvedView) visibleCondition(labelsExpression string, args []any) (string, []any) {
	groups := make([]string, 0, len(view.groups))
	for _, group := range view.groups {
		matchers := make([]string, 0, len(group))
		for _, matcher := range group {
			args = append(args, matcher.Name, matcher.Value)
			namePosition := len(args) - 1
			valuePosition := len(args)
			operator := map[string]string{"=": "=", "!=": "<>", "=~": "~", "!~": "!~"}[matcher.Operator]
			matchers = append(matchers, fmt.Sprintf(
				"(%s ? $%d AND %s->>$%d %s $%d)",
				labelsExpression, namePosition, labelsExpression, namePosition, operator, valuePosition,
			))
		}
		groups = append(groups, "("+strings.Join(matchers, " AND ")+")")
	}
	condition := "(" + strings.Join(groups, " OR ") + ")"
	if !view.options.FilterCritical {
		condition = "(" + condition + " OR COALESCE(" + labelsExpression + "->>'severity', 'warning') = 'critical')"
	}
	return condition, args
}

// summarizeView reports what the view did to a result, counting what it hides
// under the same filters as the result itself. It returns nil when the caller
// asked for no view.
func (store *Store) summarizeView(
	ctx context.Context, principal auth.Principal, query alerts.Query, view resolvedView,
) (*alerts.ViewSummary, error) {
	if !view.requested {
		return nil, nil
	}
	summary := &alerts.ViewSummary{
		Active:         view.active(),
		ShowingAll:     view.options.ShowAll,
		CriticalBypass: !view.options.FilterCritical,
	}
	if !view.active() {
		return summary, nil
	}
	where, args := alertFiltersWithView(principal, query, "alert", view, viewInvert)
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM alerts AS alert`+where, args...).Scan(&summary.Hidden); err != nil {
		return nil, fmt.Errorf("count alerts hidden by view: %w", err)
	}
	return summary, nil
}
