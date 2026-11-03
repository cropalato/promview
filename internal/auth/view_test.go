package auth

import "testing"

func TestViewMatchers(t *testing.T) {
	team := []LabelMatcher{{Name: "team", Operator: "=", Value: "platform"}}
	db := []LabelMatcher{{Name: "team", Operator: "=", Value: "db"}}
	tests := []struct {
		name      string
		principal Principal
		want      int
	}{
		{"anonymous has no view", Principal{Anonymous: true}, 0},
		{"scoped only: view would equal scope", Principal{Grants: []Grant{{Role: RoleViewer, Matchers: team}}}, 0},
		{"unrestricted only: nothing to view through", Principal{Grants: []Grant{{Role: RoleViewer}}}, 0},
		{"administrator only", Principal{Grants: []Grant{{Role: RoleAdministrator}}}, 0},
		{"unrestricted plus a team", Principal{Grants: []Grant{{Role: RoleViewer}, {Role: RoleViewer, Matchers: team}}}, 1},
		{"administrator plus two teams unions them", Principal{Grants: []Grant{
			{Role: RoleAdministrator},
			{Role: RoleOperator, Matchers: team},
			{Role: RoleViewer, Matchers: db},
		}}, 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ViewMatchers(test.principal); len(got) != test.want {
				t.Fatalf("ViewMatchers() = %v, want %d group(s)", got, test.want)
			}
		})
	}
}
