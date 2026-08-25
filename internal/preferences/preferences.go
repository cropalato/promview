// Package preferences holds the console layout choices that follow an operator
// between machines: which columns they keep, in what order and width, how dense
// the table is, whether alerts arrive grouped, and which palette the console
// renders in.
//
// These are stored per user, which means they exist only where there is a user
// to key on. In open mode every reader is the same anonymous principal, so the
// server has nothing to store and the console keeps its choices in the browser
// instead.
package preferences

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cropalato/promview/internal/alerts"
)

// FixedColumns are the console's built-in columns, keyed by the ids the table
// already uses.
var FixedColumns = []string{
	"severity", "state", "alert", "summary", "team",
	"instance", "lastSeen", "source", "age", "assignee", "notes",
}

// LabelColumnPrefix marks a column bound to an arbitrary alert label, which is
// what lets an operator surface a dimension the built-in columns do not cover
// without the label having to be given console-wide meaning.
const LabelColumnPrefix = "label:"

// Densities are the row-height choices. A NOC wall display wants larger type; a
// laptop wants more rows on screen. "auto" defers the choice to the console,
// which resolves it from the area the table actually has - the same operator
// moves between a laptop and a wall display, and the right answer differs per
// screen rather than per person.
var Densities = []string{"auto", "compact", "normal", "comfortable"}

// Themes are the palette choices. "system" is the default and keeps the
// console following the operating system's light/dark setting, which is what
// the console did before a palette could be picked at all. The named themes
// pin a palette instead: an operator whose machine is set light but who works
// a dark room should not have to change the machine to change the console.
// Beyond taste, "high-contrast" is for wall displays and low vision, and
// "colorblind-safe" separates the severity ramp by lightness as well as hue.
var Themes = []string{
	"system", "dark", "light", "nord", "gruvbox",
	"solarized-light", "high-contrast", "colorblind-safe",
}

// NotificationFields are what a notification selector may match on.
//
// The vocabulary is bounded by the transport, not by taste: a stream event
// carries only the handful of fields the server denormalized into the stream
// record, so a selector naming anything else could never fire. Refusing it at
// write time is the difference between "no notifications yet" and "notifications
// that will never arrive and nobody can explain".
var NotificationFields = []string{"severity", "alertname", "source", "team"}

// NotificationOperators mirror the filter bar's, minus the regular-expression
// forms: a selector is evaluated per event on a hot path, and an operator
// debugging why a page never arrived should not also be debugging a regex.
var NotificationOperators = []string{"=", "!="}

// maxNotificationMatchers bounds a selector. Past a handful the operator wants
// a different alerting rule, not a longer client-side filter.
const maxNotificationMatchers = 8

// NotificationMatcher constrains one field of a stream event.
type NotificationMatcher struct {
	Name  string `json:"name"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// Notifications is who gets told about what, stored per user so the policy
// follows an operator to whatever client they are using rather than living in
// one browser's local storage.
type Notifications struct {
	Enabled bool `json:"enabled"`
	// Matchers are ANDed. An empty list matches nothing rather than
	// everything: the vacuous-truth reading would turn switching notifications
	// on into a page for every alert in the deployment.
	Matchers []NotificationMatcher `json:"matchers"`
}

const (
	maxColumns     = 24
	minColumnWidth = 40
	maxColumnWidth = 1200
)

var labelNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ErrNoSubject is returned when the caller has no user to key preferences
// against, which is every caller in open mode. It is not an authorization
// failure: there is simply nothing to store against.
var ErrNoSubject = errors.New("preferences require an authenticated user")

type Column struct {
	ID string `json:"id"`
	// Width in pixels; zero means the table decides.
	Width int `json:"width,omitempty"`
}

type Grouping struct {
	Enabled bool     `json:"enabled"`
	Keys    []string `json:"keys"`
}

// SilencedVisibilities are what an operator can do about alerts a silence or
// inhibition is holding back. "hide" is offered but is not the default: an
// alert disappearing because somebody else silenced it is the failure mode
// silencing is supposed to replace, not cause.
var SilencedVisibilities = []string{"show", "hide", "only"}

type Preferences struct {
	Columns  []Column `json:"columns"`
	Density  string   `json:"density"`
	Grouping Grouping `json:"grouping"`
	Theme    string   `json:"theme"`
	// SilencedVisibility decides whether suppressed alerts appear in the list.
	SilencedVisibility string `json:"silencedVisibility"`

	Notifications Notifications `json:"notifications"`
}

// Default is what a user who has never saved anything gets, and what the
// console falls back to when the stored value cannot be read.
func Default() Preferences {
	columns := make([]Column, 0, len(FixedColumns))
	for _, id := range FixedColumns {
		columns = append(columns, Column{ID: id})
	}
	return Preferences{
		Columns: columns,
		// Auto by default: a fixed density is right for one screen size and
		// wrong for the next, and an operator should not have to set it per
		// machine when the layout otherwise follows them.
		Density: "auto",
		// Grouping is on by default: the fan-out it collapses is what makes a
		// busy console unreadable, and a single-member group renders as an
		// ordinary row, so grouping costs nothing when there is nothing to
		// collapse.
		Grouping: Grouping{Enabled: true, Keys: []string{"alertname", "source"}},
		// System by default: the console has always followed the OS setting,
		// and a user who never opens the picker should see no change.
		Theme: "system",
		// Shown by default, dimmed and chipped rather than hidden. An operator
		// has to be able to see that an alert is firing and being held back;
		// those are different facts from it not being there.
		SilencedVisibility: "show",
		// Off, with the selector the console hardcoded before this was
		// configurable. Opting in should do what it always did; widening it is
		// then the operator's explicit choice.
		Notifications: Notifications{
			Enabled:  false,
			Matchers: []NotificationMatcher{{Name: "severity", Op: "=", Value: "critical"}},
		},
	}
}

// Validate rejects anything the console could not render, so a bad write is
// refused at the door rather than breaking every later read.
func Validate(value Preferences) error {
	if len(value.Columns) == 0 {
		return errors.New("at least one column is required")
	}
	if len(value.Columns) > maxColumns {
		return fmt.Errorf("at most %d columns are allowed", maxColumns)
	}
	seen := make(map[string]bool, len(value.Columns))
	for _, column := range value.Columns {
		if err := validateColumnID(column.ID); err != nil {
			return err
		}
		if seen[column.ID] {
			return fmt.Errorf("duplicate column %q", column.ID)
		}
		seen[column.ID] = true
		if column.Width != 0 && (column.Width < minColumnWidth || column.Width > maxColumnWidth) {
			return fmt.Errorf("column %q width must be between %d and %d", column.ID, minColumnWidth, maxColumnWidth)
		}
	}
	if !isDensity(value.Density) {
		return fmt.Errorf("density must be one of %s", strings.Join(Densities, ", "))
	}
	if !isTheme(value.Theme) {
		return fmt.Errorf("theme must be one of %s", strings.Join(Themes, ", "))
	}
	if !isSilencedVisibility(value.SilencedVisibility) {
		return fmt.Errorf("silencedVisibility must be one of %s", strings.Join(SilencedVisibilities, ", "))
	}
	if err := validateNotifications(value.Notifications); err != nil {
		return err
	}
	if value.Grouping.Enabled {
		if err := alerts.ValidateGroupBy(value.Grouping.Keys); err != nil {
			return err
		}
	}
	return nil
}

func validateColumnID(id string) error {
	if label, ok := strings.CutPrefix(id, LabelColumnPrefix); ok {
		if !labelNamePattern.MatchString(label) {
			return fmt.Errorf("column %q does not name a valid label", id)
		}
		return nil
	}
	for _, fixed := range FixedColumns {
		if fixed == id {
			return nil
		}
	}
	return fmt.Errorf("unknown column %q", id)
}

func isDensity(value string) bool {
	for _, density := range Densities {
		if density == value {
			return true
		}
	}
	return false
}

func validateNotifications(value Notifications) error {
	if len(value.Matchers) > maxNotificationMatchers {
		return fmt.Errorf("at most %d notification matchers are allowed", maxNotificationMatchers)
	}
	seen := make(map[string]bool, len(value.Matchers))
	for _, matcher := range value.Matchers {
		if !isNotificationField(matcher.Name) {
			return fmt.Errorf(
				"notifications can only match on %s, not %q",
				strings.Join(NotificationFields, ", "), matcher.Name,
			)
		}
		if matcher.Op != "=" && matcher.Op != "!=" {
			return fmt.Errorf("notification matcher operator must be = or !=, not %q", matcher.Op)
		}
		if matcher.Value == "" {
			return fmt.Errorf("notification matcher on %q needs a value", matcher.Name)
		}
		// Two constraints on one field are either contradictory (severity is
		// both critical and warning) or redundant, and neither is what the
		// operator meant.
		if seen[matcher.Name] {
			return fmt.Errorf("duplicate notification matcher on %q", matcher.Name)
		}
		seen[matcher.Name] = true
	}
	return nil
}

func isNotificationField(name string) bool {
	for _, field := range NotificationFields {
		if field == name {
			return true
		}
	}
	return false
}

func isSilencedVisibility(value string) bool {
	for _, candidate := range SilencedVisibilities {
		if candidate == value {
			return true
		}
	}
	return false
}

func isTheme(value string) bool {
	for _, theme := range Themes {
		if theme == value {
			return true
		}
	}
	return false
}
