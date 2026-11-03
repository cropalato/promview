package alerts

import (
	"fmt"
	"regexp"
)

// ViewMatch says why an alert is in front of the caller, relative to their view.
type ViewMatch string

const (
	// ViewMatchView means the alert matches one of the caller's view selectors.
	ViewMatchView ViewMatch = "view"
	// ViewMatchCritical means the alert is shown only because critical alerts
	// ignore the view.
	ViewMatchCritical ViewMatch = "critical"
	// ViewMatchOutside means the alert is outside the view and is present only
	// because the caller asked to see everything in their scope.
	ViewMatchOutside ViewMatch = "outside"
)

// ViewOptions is what a client asks for. The selectors themselves are not here:
// they are derived from the caller's grants by the store, on each request, so
// a binding change applies without the client being told.
//
// A nil *ViewOptions on a Query means no view at all, which is what every
// internal caller that is not rendering a console list wants.
type ViewOptions struct {
	// ShowAll returns everything in scope and only classifies it against the
	// view. It never widens scope.
	ShowAll bool
	// FilterCritical makes critical alerts subject to the view like any other.
	// The zero value lets them through, which is the default on purpose: a
	// filter that can hide a critical alert is something a user chooses.
	FilterCritical bool
}

// Signature identifies the options inside a cursor, so a page token issued
// under one view is rejected under another. It is empty for a nil receiver,
// which keeps the cursors of queries without a view byte-for-byte unchanged.
func (options *ViewOptions) Signature() string {
	if options == nil {
		return ""
	}
	return fmt.Sprintf("all=%t,filterCritical=%t", options.ShowAll, options.FilterCritical)
}

// ViewSummary is reported alongside a result so the console can say what the
// view did instead of leaving the user to wonder why a list looks short.
type ViewSummary struct {
	// Active is true when the caller has view selectors at all.
	Active bool `json:"active"`
	// ShowingAll is true when the result was not narrowed by the view.
	ShowingAll bool `json:"showingAll"`
	// Hidden counts alerts in scope that the view excludes from this result,
	// under the same other filters. When ShowingAll it is what the view would
	// have hidden.
	Hidden         int64 `json:"hidden"`
	CriticalBypass bool  `json:"criticalBypass"`
}

// MatchViewGroups reports whether labels satisfy any group of selectors, each
// group being a conjunction. It mirrors the SQL the store builds for the same
// selectors: a label that is absent matches no operator, negative ones
// included, so a Go-side classification never disagrees with the query.
func MatchViewGroups(groups [][]LabelMatcher, labels map[string]string) bool {
	for _, group := range groups {
		matched := len(group) > 0
		for _, matcher := range group {
			value, exists := labels[matcher.Name]
			if !exists {
				matched = false
				break
			}
			switch matcher.Operator {
			case "=":
				matched = value == matcher.Value
			case "!=":
				matched = value != matcher.Value
			case "=~":
				matched, _ = regexp.MatchString(matcher.Value, value)
			case "!~":
				found, _ := regexp.MatchString(matcher.Value, value)
				matched = !found
			default:
				matched = false
			}
			if !matched {
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// IsCritical applies the same default the list filter does: an alert with no
// severity label is a warning.
func IsCritical(labels map[string]string) bool {
	severity, exists := labels["severity"]
	if !exists || severity == "" {
		return false
	}
	return severity == "critical"
}

// ClassifyView places an alert relative to a view. groups must be non-empty.
func ClassifyView(groups [][]LabelMatcher, options ViewOptions, labels map[string]string) ViewMatch {
	if MatchViewGroups(groups, labels) {
		return ViewMatchView
	}
	if !options.FilterCritical && IsCritical(labels) {
		return ViewMatchCritical
	}
	return ViewMatchOutside
}
