package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateViewGroup(t *testing.T) {
	team := []LabelMatcher{{Name: "team", Operator: "=", Value: "platform"}}
	tests := []struct {
		name  string
		group ViewGroup
		ok    bool
	}{
		{"valid", ViewGroup{Name: "platform", Matchers: team}, true},
		{"bad name", ViewGroup{Name: "Platform Team", Matchers: team}, false},
		{"no selector would mean everything", ViewGroup{Name: "all"}, false},
		{"bad label", ViewGroup{Name: "x", Matchers: []LabelMatcher{{Name: "1bad", Operator: "=", Value: "x"}}}, false},
		{"bad operator", ViewGroup{Name: "x", Matchers: []LabelMatcher{{Name: "a", Operator: "~", Value: "x"}}}, false},
		{"bad regexp", ViewGroup{Name: "x", Matchers: []LabelMatcher{{Name: "a", Operator: "=~", Value: "("}}}, false},
		{"empty value", ViewGroup{Name: "x", Matchers: []LabelMatcher{{Name: "a", Operator: "=", Value: ""}}}, false},
		{"long description", ViewGroup{Name: "x", Matchers: team, Description: strings.Repeat("a", 513)}, false},
	}
	for _, test := range tests {
		err := ValidateViewGroup(test.group)
		if (err == nil) != test.ok {
			t.Errorf("%s: err = %v, want ok=%t", test.name, err, test.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalidViewGroup) {
			t.Errorf("%s: error does not wrap ErrInvalidViewGroup: %v", test.name, err)
		}
	}
}

func TestValidateViewGroupPin(t *testing.T) {
	for name, pin := range map[string]ViewGroupPin{
		"user": {SubjectKind: SubjectUser, UserID: 3},
		"oidc": {SubjectKind: SubjectOIDCGroup, SubjectIssuer: "https://idp.example", SubjectGroup: "g"},
		"ldap": {SubjectKind: SubjectLDAPGroup, SubjectIssuer: "ldaps://ldap.example", SubjectGroup: "g"},
	} {
		if err := ValidateViewGroupPin(pin); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, pin := range map[string]ViewGroupPin{
		"no kind":          {},
		"user without id":  {SubjectKind: SubjectUser},
		"group no issuer":  {SubjectKind: SubjectOIDCGroup, SubjectGroup: "g"},
		"oidc on ldap url": {SubjectKind: SubjectOIDCGroup, SubjectIssuer: "ldaps://x", SubjectGroup: "g"},
	} {
		if err := ValidateViewGroupPin(pin); !errors.Is(err, ErrInvalidViewGroup) {
			t.Errorf("%s: err = %v, want ErrInvalidViewGroup", name, err)
		}
	}
}
