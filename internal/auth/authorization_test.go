package auth

import "testing"

func TestParseAndMatchLabelSelectors(t *testing.T) {
	for _, raw := range []string{"team=platform", "environment!=development", "service=~api-.*", "region!~test-.*"} {
		matcher, err := ParseLabelMatcher(raw)
		if err != nil {
			t.Fatalf("ParseLabelMatcher(%q) error = %v", raw, err)
		}
		principal := Principal{Grants: []Grant{{Role: RoleViewer, Matchers: []LabelMatcher{matcher}}}}
		labels := map[string]string{"team": "platform", "environment": "production", "service": "api-server", "region": "us-east"}
		if !CanReadLabels(principal, labels) {
			t.Fatalf("selector %q did not match %#v", raw, labels)
		}
		if CanReadLabels(principal, map[string]string{}) {
			t.Fatalf("selector %q matched an absent label", raw)
		}
	}
}

func TestCanReadLabelsUsesOrAcrossGrants(t *testing.T) {
	principal := Principal{Grants: []Grant{
		{Role: RoleViewer, Matchers: []LabelMatcher{{Name: "team", Operator: "=", Value: "platform"}}},
		{Role: RoleOperator, Matchers: []LabelMatcher{{Name: "team", Operator: "=", Value: "payments"}}},
	}}
	if !CanReadLabels(principal, map[string]string{"team": "payments"}) {
		t.Fatal("payments grant did not match")
	}
	if CanReadLabels(principal, map[string]string{"team": "security"}) {
		t.Fatal("unbound team matched")
	}
}

func TestValidateRoleBinding(t *testing.T) {
	valid := RoleBinding{
		Name: "platform-operators", SubjectKind: SubjectOIDCGroup,
		OIDCIssuer: "https://identity.example.com", OIDCGroup: "platform", Role: RoleOperator,
		Matchers: []LabelMatcher{{Name: "team", Operator: "=", Value: "platform"}},
	}
	if err := ValidateRoleBinding(valid); err != nil {
		t.Fatal(err)
	}
	valid.Role = RoleAdministrator
	if err := ValidateRoleBinding(valid); err == nil {
		t.Fatal("scoped administrator binding was accepted")
	}
}

func TestParseLabelMatcherRejectsInvalidValues(t *testing.T) {
	for _, raw := range []string{"team", "bad-name=value", "team=", "team=~["} {
		if _, err := ParseLabelMatcher(raw); err == nil {
			t.Errorf("ParseLabelMatcher(%q) error = nil", raw)
		}
	}
}
