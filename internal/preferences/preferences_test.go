package preferences

import "testing"

func TestDefaultIsValid(t *testing.T) {
	if err := Validate(Default()); err != nil {
		t.Fatalf("Validate(Default()) error = %v", err)
	}
	if !Default().Grouping.Enabled {
		t.Error("grouping is off by default, want on")
	}
}

func TestValidateAcceptsEveryDensityIncludingAuto(t *testing.T) {
	for _, density := range Densities {
		value := Default()
		value.Density = density
		if err := Validate(value); err != nil {
			t.Errorf("Validate() with density %q error = %v", density, err)
		}
	}
	// Auto is the default because the right row height depends on the screen in
	// front of the operator, not on the operator.
	if Default().Density != "auto" {
		t.Errorf("default density = %q, want auto", Default().Density)
	}
}

func TestValidateAcceptsEveryThemeIncludingSystem(t *testing.T) {
	for _, theme := range Themes {
		value := Default()
		value.Theme = theme
		if err := Validate(value); err != nil {
			t.Errorf("Validate() with theme %q error = %v", theme, err)
		}
	}
	// System is the default so an operator who never opens the picker keeps the
	// behaviour the console had before there was one.
	if Default().Theme != "system" {
		t.Errorf("default theme = %q, want system", Default().Theme)
	}
}

func TestNotificationDefaultsPreserveTheHardcodedRule(t *testing.T) {
	value := Default().Notifications
	// Off, but pre-loaded with what the console matched on before this was
	// configurable: opting in should do what it always did.
	if value.Enabled {
		t.Error("notifications default to on, want off")
	}
	if len(value.Matchers) != 1 {
		t.Fatalf("default matchers = %#v, want the critical selector", value.Matchers)
	}
	got := value.Matchers[0]
	if got.Name != "severity" || got.Op != "=" || got.Value != "critical" {
		t.Errorf("default matcher = %#v, want severity=critical", got)
	}
}

func TestValidateAcceptsEveryMatchableField(t *testing.T) {
	for _, field := range NotificationFields {
		value := Default()
		value.Notifications.Matchers = []NotificationMatcher{{Name: field, Op: "=", Value: "x"}}
		if err := Validate(value); err != nil {
			t.Errorf("Validate() with a %q matcher error = %v", field, err)
		}
	}
	// Negation is offered too; a team that pages on everything except its own
	// noisy source is the case this exists for.
	value := Default()
	value.Notifications.Matchers = []NotificationMatcher{{Name: "source", Op: "!=", Value: "lab"}}
	if err := Validate(value); err != nil {
		t.Errorf("Validate() with a negated matcher error = %v", err)
	}
}

func TestValidateRejectsSelectorsThatCouldNeverFire(t *testing.T) {
	for _, test := range []struct {
		name     string
		matchers []NotificationMatcher
	}{
		{
			// A stream event carries only the denormalized fields; a selector
			// on anything else would never match, and silence is the worst
			// possible way to report that.
			name:     "field the stream event does not carry",
			matchers: []NotificationMatcher{{Name: "instance", Op: "=", Value: "web-01"}},
		},
		{
			name:     "regex operator",
			matchers: []NotificationMatcher{{Name: "severity", Op: "=~", Value: "crit.*"}},
		},
		{
			name:     "empty value",
			matchers: []NotificationMatcher{{Name: "severity", Op: "=", Value: ""}},
		},
		{
			// Contradictory or redundant; neither is what the operator meant.
			name: "two constraints on one field",
			matchers: []NotificationMatcher{
				{Name: "severity", Op: "=", Value: "critical"},
				{Name: "severity", Op: "=", Value: "warning"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := Default()
			value.Notifications.Matchers = test.matchers
			if err := Validate(value); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestValidateBoundsTheSelector(t *testing.T) {
	value := Default()
	matchers := make([]NotificationMatcher, 0, maxNotificationMatchers+1)
	for i := 0; i <= maxNotificationMatchers; i++ {
		matchers = append(matchers, NotificationMatcher{
			Name: NotificationFields[i%len(NotificationFields)], Op: "=", Value: "x",
		})
	}
	value.Notifications.Matchers = matchers
	if err := Validate(value); err == nil {
		t.Fatal("Validate() with an unbounded selector error = nil, want error")
	}
}

func TestValidateAcceptsAnEmptySelector(t *testing.T) {
	// Allowed, and documented to match nothing rather than everything. The
	// vacuous-truth reading would turn opting in into a page per alert.
	value := Default()
	value.Notifications = Notifications{Enabled: true, Matchers: []NotificationMatcher{}}
	if err := Validate(value); err != nil {
		t.Fatalf("Validate() with an empty selector error = %v", err)
	}
}

func TestValidateAcceptsLabelColumns(t *testing.T) {
	// A label column is the whole point of the registry: surface a dimension
	// the built-in columns do not cover without giving the label console-wide
	// meaning.
	value := Default()
	value.Columns = append(value.Columns, Column{ID: "label:prometheus_cluster", Width: 180})
	if err := Validate(value); err != nil {
		t.Fatalf("Validate() with a label column error = %v", err)
	}
}

func TestValidateAcceptsCustomGroupingLabel(t *testing.T) {
	value := Default()
	value.Grouping.Keys = []string{"prometheus_cluster", "source"}
	if err := Validate(value); err != nil {
		t.Fatalf("Validate() with a custom grouping label error = %v", err)
	}
}

func TestValidateRejectsUnusableLayouts(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Preferences)
	}{
		{name: "no columns", mutate: func(p *Preferences) { p.Columns = nil }},
		{name: "unknown column", mutate: func(p *Preferences) { p.Columns = []Column{{ID: "nonsense"}} }},
		{name: "malformed label column", mutate: func(p *Preferences) { p.Columns = []Column{{ID: "label:not a label"}} }},
		{name: "empty label column", mutate: func(p *Preferences) { p.Columns = []Column{{ID: "label:"}} }},
		{name: "duplicate column", mutate: func(p *Preferences) {
			p.Columns = []Column{{ID: "severity"}, {ID: "severity"}}
		}},
		{name: "absurd width", mutate: func(p *Preferences) { p.Columns = []Column{{ID: "severity", Width: 5}} }},
		{name: "unknown density", mutate: func(p *Preferences) { p.Density = "tiny" }},
		{name: "unknown theme", mutate: func(p *Preferences) { p.Theme = "neon" }},
		{name: "empty theme", mutate: func(p *Preferences) { p.Theme = "" }},
		{name: "grouping without keys", mutate: func(p *Preferences) {
			p.Grouping = Grouping{Enabled: true}
		}},
		{name: "grouping by a malformed label", mutate: func(p *Preferences) {
			p.Grouping = Grouping{Enabled: true, Keys: []string{"not a label"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := Default()
			test.mutate(&value)
			if err := Validate(value); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestValidateIgnoresGroupingKeysWhenDisabled(t *testing.T) {
	// Turning grouping off should not require clearing the keys first; the
	// console keeps them so re-enabling restores the previous choice.
	value := Default()
	value.Grouping = Grouping{Enabled: false, Keys: nil}
	if err := Validate(value); err != nil {
		t.Fatalf("Validate() with grouping off error = %v", err)
	}
}
