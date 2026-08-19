// Package preferences holds the console layout choices that follow an operator
// between machines: which columns they keep, in what order and width, how dense
// the table is, and whether alerts arrive grouped.
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
	"instance", "source", "age", "assignee", "notes",
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

type Preferences struct {
	Columns  []Column `json:"columns"`
	Density  string   `json:"density"`
	Grouping Grouping `json:"grouping"`
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
