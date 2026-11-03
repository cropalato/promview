package alerts

import "testing"

func TestMatchViewGroups(t *testing.T) {
	team := []LabelMatcher{{Name: "team", Operator: "=", Value: "platform"}}
	notDB := []LabelMatcher{{Name: "team", Operator: "!=", Value: "db"}}
	regex := []LabelMatcher{{Name: "team", Operator: "=~", Value: "^plat"}}
	both := []LabelMatcher{{Name: "team", Operator: "=", Value: "platform"}, {Name: "env", Operator: "=", Value: "prod"}}
	tests := []struct {
		name   string
		groups [][]LabelMatcher
		labels map[string]string
		want   bool
	}{
		{"equal", [][]LabelMatcher{team}, map[string]string{"team": "platform"}, true},
		{"unequal", [][]LabelMatcher{team}, map[string]string{"team": "payments"}, false},
		{"absent label matches nothing", [][]LabelMatcher{team}, map[string]string{}, false},
		{"absent label does not satisfy a negative matcher", [][]LabelMatcher{notDB}, map[string]string{}, false},
		{"negative matcher", [][]LabelMatcher{notDB}, map[string]string{"team": "platform"}, true},
		{"regex", [][]LabelMatcher{regex}, map[string]string{"team": "platform"}, true},
		{"conjunction needs both", [][]LabelMatcher{both}, map[string]string{"team": "platform"}, false},
		{"groups are ORed", [][]LabelMatcher{both, team}, map[string]string{"team": "platform"}, true},
		{"no groups match nothing", nil, map[string]string{"team": "platform"}, false},
		{"an empty group matches nothing", [][]LabelMatcher{{}}, map[string]string{"team": "platform"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := MatchViewGroups(test.groups, test.labels); got != test.want {
				t.Fatalf("MatchViewGroups() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestClassifyView(t *testing.T) {
	groups := [][]LabelMatcher{{{Name: "team", Operator: "=", Value: "platform"}}}
	tests := []struct {
		name    string
		options ViewOptions
		labels  map[string]string
		want    ViewMatch
	}{
		{"in the view", ViewOptions{}, map[string]string{"team": "platform"}, ViewMatchView},
		{"outside", ViewOptions{}, map[string]string{"team": "db", "severity": "warning"}, ViewMatchOutside},
		{"critical bypasses", ViewOptions{}, map[string]string{"team": "db", "severity": "critical"}, ViewMatchCritical},
		{"critical filtered on request", ViewOptions{FilterCritical: true}, map[string]string{"team": "db", "severity": "critical"}, ViewMatchOutside},
		{"no severity is a warning, not critical", ViewOptions{}, map[string]string{"team": "db"}, ViewMatchOutside},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyView(groups, test.options, test.labels); got != test.want {
				t.Fatalf("ClassifyView() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCursorIdentityBindsTheView(t *testing.T) {
	plain := Query{Limit: 10}.CursorIdentity()
	withView := Query{Limit: 10, View: &ViewOptions{}}.CursorIdentity()
	showAll := Query{Limit: 10, View: &ViewOptions{ShowAll: true}}.CursorIdentity()
	if plain == withView || withView == showAll {
		t.Fatal("a cursor issued under one view must be rejected under another")
	}
	if plain != (Query{Limit: 99}).CursorIdentity() {
		t.Fatal("a query with no view must hash as it did before views existed")
	}
}
