package auth

// ViewMatchers returns the selectors a caller's automatic view is built from:
// one conjunction per scoped viewer or operator grant, to be ORed together.
//
// A view only means something where it can be narrower than scope. A caller
// whose every grant is scoped already sees exactly the union of those
// scopes, so a view derived from them would restrict nothing and cost a hidden
// count on every request; that case returns nil. The view exists for the
// caller who has an unrestricted grant and also belongs to a team: scope is
// everything, and the team is what they normally mean to look at.
//
// Anonymous callers and callers with no scoped grant get nil, which the
// caller must read as "no filter", never as "nothing matches".
func ViewMatchers(principal Principal) [][]LabelMatcher {
	if principal.Anonymous {
		return nil
	}
	unrestricted := false
	var groups [][]LabelMatcher
	for _, grant := range principal.Grants {
		switch grant.Role {
		case RoleAdministrator:
			unrestricted = true
		case RoleViewer, RoleOperator:
			if len(grant.Matchers) == 0 {
				unrestricted = true
				continue
			}
			groups = append(groups, append([]LabelMatcher(nil), grant.Matchers...))
		}
	}
	if !unrestricted {
		return nil
	}
	return groups
}
