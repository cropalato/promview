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

func TestCanOperateLabelsExcludesViewersAndHonorsOperatorScope(t *testing.T) {
	labels := map[string]string{"team": "platform"}
	viewer := Principal{Grants: []Grant{{Role: RoleViewer}}}
	if viewer.CanOperate() || CanOperateLabels(viewer, labels) {
		t.Fatal("viewer can operate")
	}
	operator := Principal{Grants: []Grant{{
		Role: RoleOperator, Matchers: []LabelMatcher{{Name: "team", Operator: "=", Value: "platform"}},
	}}}
	if !operator.CanOperate() || !CanOperateLabels(operator, labels) {
		t.Fatal("scoped operator cannot operate in scope")
	}
	if CanOperateLabels(operator, map[string]string{"team": "payments"}) {
		t.Fatal("scoped operator can operate out of scope")
	}
}

func TestValidateRoleBinding(t *testing.T) {
	valid := RoleBinding{
		Name: "platform-operators", SubjectKind: SubjectOIDCGroup,
		SubjectIssuer: "https://identity.example.com", SubjectGroup: "platform", Role: RoleOperator,
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

// The scheme is what keeps the two group kinds from being written against each
// other's directories. A binding whose issuer scheme contradicts its kind can
// never match anything, and would sit in the list looking like access somebody
// has.
func TestValidateRoleBindingMatchesTheIssuerSchemeToTheSubjectKind(t *testing.T) {
	for name, test := range map[string]struct {
		kind, issuer string
		wantErr      bool
	}{
		"oidc over https":  {kind: SubjectOIDCGroup, issuer: "https://identity.example.com"},
		"oidc over http":   {kind: SubjectOIDCGroup, issuer: "http://localhost:9000"},
		"oidc over ldaps":  {kind: SubjectOIDCGroup, issuer: "ldaps://dc.example.com", wantErr: true},
		"ldap over ldaps":  {kind: SubjectLDAPGroup, issuer: "ldaps://dc.example.com:636"},
		"ldap over ldap":   {kind: SubjectLDAPGroup, issuer: "ldap://127.0.0.1:389"},
		"ldap over https":  {kind: SubjectLDAPGroup, issuer: "https://identity.example.com", wantErr: true},
		"not a URL at all": {kind: SubjectLDAPGroup, issuer: "dc.example.com", wantErr: true},
		"unknown subject":  {kind: "kerberos_realm", issuer: "https://identity.example.com", wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateRoleBinding(RoleBinding{
				Name: "platform", SubjectKind: test.kind,
				SubjectIssuer: test.issuer, SubjectGroup: "platform", Role: RoleOperator,
			})
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, test.wantErr)
			}
		})
	}
}
