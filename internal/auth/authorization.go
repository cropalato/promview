package auth

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Role string

const (
	RoleViewer        Role = "viewer"
	RoleOperator      Role = "operator"
	RoleAdministrator Role = "administrator"

	SubjectUser      = "user"
	SubjectOIDCGroup = "oidc_group"
)

var (
	ErrAccessDenied     = errors.New("read access denied")
	labelNamePattern    = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	bindingNamePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	supportedMatcherOps = []string{"!=", "=~", "!~", "="}
)

type LabelMatcher struct {
	Name     string `json:"name"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

type Grant struct {
	Role     Role           `json:"role"`
	Matchers []LabelMatcher `json:"matchers,omitempty"`
}

type RoleBinding struct {
	Name        string         `json:"name"`
	SubjectKind string         `json:"subjectKind"`
	UserID      int64          `json:"userID,omitempty"`
	OIDCIssuer  string         `json:"oidcIssuer,omitempty"`
	OIDCGroup   string         `json:"oidcGroup,omitempty"`
	Role        Role           `json:"role"`
	Matchers    []LabelMatcher `json:"matchers,omitempty"`
}

// AuthorizationDiagnostics contains persisted OIDC identity and binding data for administrators.
// It deliberately excludes provider tokens and Promview sessions.
type AuthorizationDiagnostics struct {
	Identities []OIDCIdentityDiagnostic `json:"identities"`
	Bindings   []RoleBinding            `json:"bindings"`
}

type OIDCIdentityDiagnostic struct {
	UserID      int64     `json:"userID"`
	Issuer      string    `json:"issuer"`
	Subject     string    `json:"subject"`
	Username    string    `json:"username"`
	Email       string    `json:"email"`
	DisplayName string    `json:"displayName"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
	Groups      []string  `json:"groups"`
}

func (principal Principal) CanRead() bool {
	if principal.Anonymous {
		return true
	}
	for _, grant := range principal.Grants {
		switch grant.Role {
		case RoleViewer, RoleOperator, RoleAdministrator:
			return true
		}
	}
	return false
}

func (principal Principal) CanOperate() bool {
	if principal.Anonymous {
		return false
	}
	for _, grant := range principal.Grants {
		if grant.Role == RoleOperator || grant.Role == RoleAdministrator {
			return true
		}
	}
	return false
}

func CanOperateLabels(principal Principal, labels map[string]string) bool {
	if principal.Anonymous {
		return false
	}
	for _, grant := range principal.Grants {
		if grant.Role == RoleAdministrator {
			return true
		}
		if grant.Role != RoleOperator {
			continue
		}
		matched := true
		for _, matcher := range grant.Matchers {
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
				regexMatched, _ := regexp.MatchString(matcher.Value, value)
				matched = !regexMatched
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

func RolesFromGrants(grants []Grant) []string {
	seen := make(map[Role]bool)
	roles := make([]string, 0, 3)
	for _, role := range []Role{RoleViewer, RoleOperator, RoleAdministrator} {
		for _, grant := range grants {
			if grant.Role == role && !seen[role] {
				roles = append(roles, string(role))
				seen[role] = true
			}
		}
	}
	return roles
}

func CanReadLabels(principal Principal, labels map[string]string) bool {
	if principal.Anonymous {
		return true
	}
	for _, grant := range principal.Grants {
		if grant.Role == RoleAdministrator {
			return true
		}
		if grant.Role != RoleViewer && grant.Role != RoleOperator {
			continue
		}
		matched := true
		for _, matcher := range grant.Matchers {
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
				regexMatched, _ := regexp.MatchString(matcher.Value, value)
				matched = !regexMatched
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

func ParseLabelMatcher(raw string) (LabelMatcher, error) {
	for _, operator := range supportedMatcherOps {
		parts := strings.SplitN(raw, operator, 2)
		if len(parts) != 2 {
			continue
		}
		matcher := LabelMatcher{Name: strings.TrimSpace(parts[0]), Operator: operator, Value: strings.TrimSpace(parts[1])}
		if !labelNamePattern.MatchString(matcher.Name) {
			return LabelMatcher{}, errors.New("selector label name is invalid")
		}
		if matcher.Value == "" {
			return LabelMatcher{}, errors.New("selector value is required")
		}
		if len(matcher.Value) > 256 {
			return LabelMatcher{}, errors.New("selector value must not exceed 256 characters")
		}
		if operator == "=~" || operator == "!~" {
			if _, err := regexp.Compile(matcher.Value); err != nil {
				return LabelMatcher{}, fmt.Errorf("selector regular expression is invalid: %w", err)
			}
		}
		return matcher, nil
	}
	return LabelMatcher{}, errors.New("selector must use =, !=, =~, or !~")
}

func ValidateRoleBinding(binding RoleBinding) error {
	if !bindingNamePattern.MatchString(binding.Name) {
		return errors.New("binding name must use lowercase letters, digits, underscores, or hyphens")
	}
	switch binding.Role {
	case RoleViewer, RoleOperator, RoleAdministrator:
	default:
		return errors.New("role must be viewer, operator, or administrator")
	}
	switch binding.SubjectKind {
	case SubjectUser:
		if binding.UserID < 1 || binding.OIDCIssuer != "" || binding.OIDCGroup != "" {
			return errors.New("user binding requires only a positive user ID")
		}
	case SubjectOIDCGroup:
		if binding.UserID != 0 || binding.OIDCIssuer == "" || binding.OIDCGroup == "" {
			return errors.New("OIDC group binding requires only issuer and group")
		}
		issuer, err := url.Parse(binding.OIDCIssuer)
		if err != nil || issuer.Scheme == "" || issuer.Host == "" {
			return errors.New("OIDC group binding issuer must be an absolute URL")
		}
		if len(binding.OIDCGroup) > 256 {
			return errors.New("OIDC group name must not exceed 256 characters")
		}
	default:
		return errors.New("binding subject must be user or OIDC group")
	}
	if len(binding.Matchers) > 16 {
		return errors.New("binding must not contain more than 16 selectors")
	}
	if binding.Role == RoleAdministrator && len(binding.Matchers) > 0 {
		return errors.New("administrator bindings must be global")
	}
	return nil
}
