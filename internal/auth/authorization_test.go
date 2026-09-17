package auth

import (
	"context"
	"testing"
)

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

// The capability checks stopped short-circuiting on Anonymous, so these are the
// tests that catch a cut that went too far. The distinction that matters is
// between a plain open-mode reader and an elevated one: both are anonymous, and
// only the grants tell them apart.
func TestOpenModeCapabilitiesFollowTheGrantedRole(t *testing.T) {
	labels := map[string]string{"team": "platform"}
	for name, test := range map[string]struct {
		role                                     Role
		read, operate, operateLabels, administer bool
	}{
		"viewer": {role: RoleViewer, read: true},
		"operator": {
			role: RoleOperator, read: true, operate: true, operateLabels: true,
		},
		"administrator": {
			role: RoleAdministrator, read: true, operate: true, operateLabels: true, administer: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			principal, err := OpenAuthenticator{Role: test.role}.Authenticate(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if !principal.Anonymous {
				t.Fatal("an open-mode principal must stay anonymous whatever it is granted")
			}
			if principal.CanRead() != test.read {
				t.Errorf("CanRead() = %v, want %v", principal.CanRead(), test.read)
			}
			if principal.CanOperate() != test.operate {
				t.Errorf("CanOperate() = %v, want %v", principal.CanOperate(), test.operate)
			}
			if CanOperateLabels(principal, labels) != test.operateLabels {
				t.Errorf("CanOperateLabels() = %v, want %v", CanOperateLabels(principal, labels), test.operateLabels)
			}
			if principal.CanAdminister() != test.administer {
				t.Errorf("CanAdminister() = %v, want %v", principal.CanAdminister(), test.administer)
			}
		})
	}
}

// The one that catches an over-broad cut. A default open-mode deployment must
// be able to do nothing but read, and nothing about removing the anonymous
// short-circuits may change that.
func TestPlainOpenModeCanOnlyRead(t *testing.T) {
	principal, err := OpenAuthenticator{}.Authenticate(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !principal.CanRead() || !CanReadLabels(principal, map[string]string{"team": "platform"}) {
		t.Fatal("a plain open-mode reader cannot read")
	}
	if principal.CanOperate() || principal.CanAdminister() {
		t.Fatalf("a plain open-mode reader can act: %#v", principal)
	}
	if CanOperateLabels(principal, map[string]string{"team": "platform"}) {
		t.Fatal("a plain open-mode reader can act on an alert")
	}
}

// A principal that resolved from a directory is never marked anonymous, so
// removing the short-circuits could not have widened what one can do. This
// holds that: a signed-in viewer still cannot operate.
func TestASignedInViewerStillCannotOperate(t *testing.T) {
	principal := Principal{
		UserID: 1, Subject: "local|1", Grants: []Grant{{Role: RoleViewer}},
	}
	if principal.CanOperate() || principal.CanAdminister() {
		t.Fatalf("a signed-in viewer can act: %#v", principal)
	}
	if !principal.CanRead() {
		t.Fatal("a signed-in viewer cannot read")
	}
}

// Elevation grants a matcher-less operator grant, which matches every alert.
// That is what a lab wants, and it is worth stating rather than inferring.
func TestElevatedOpenModeOperatesOnEveryAlert(t *testing.T) {
	principal, err := OpenAuthenticator{Role: RoleOperator}.Authenticate(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, labels := range []map[string]string{
		{"team": "platform"}, {"team": "payments"}, {}, nil,
	} {
		if !CanOperateLabels(principal, labels) {
			t.Fatalf("an elevated open mode could not act on %v", labels)
		}
	}
}

// The author is what lands in an audit trail and in Alertmanager's createdBy,
// so it has to read as a mode rather than as a person.
func TestOpenModeAuthorIsTheRecordedSubject(t *testing.T) {
	principal, err := OpenAuthenticator{Role: RoleOperator, Author: "lab-console"}.
		Authenticate(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject != "lab-console" {
		t.Fatalf("subject = %q, want the configured author", principal.Subject)
	}
	fallback, err := OpenAuthenticator{}.Authenticate(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if fallback.Subject != DefaultOpenModeAuthor {
		t.Fatalf("subject = %q, want %q", fallback.Subject, DefaultOpenModeAuthor)
	}
}
