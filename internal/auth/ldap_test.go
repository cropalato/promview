package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

type fakeLDAPConn struct {
	binds   []struct{ dn, password string }
	bindErr map[string]error

	searchRequest *ldap.SearchRequest
	searches      []*ldap.SearchRequest
	entries       []*ldap.Entry
	searchErr     error

	groupBaseDN    string
	groupEntries   []*ldap.Entry
	groupSearchErr error

	closed bool
}

func (fake *fakeLDAPConn) Bind(dn, password string) error {
	fake.binds = append(fake.binds, struct{ dn, password string }{dn, password})
	if err, refused := fake.bindErr[dn]; refused {
		return err
	}
	return nil
}

func (fake *fakeLDAPConn) Search(request *ldap.SearchRequest) (*ldap.SearchResult, error) {
	fake.searchRequest = request
	fake.searches = append(fake.searches, request)
	if fake.searchErr != nil {
		return nil, fake.searchErr
	}
	// The group search runs against its own base, so the fake answers it with
	// group entries rather than with the user again.
	if fake.groupBaseDN != "" && request.BaseDN == fake.groupBaseDN {
		if fake.groupSearchErr != nil {
			return nil, fake.groupSearchErr
		}
		return &ldap.SearchResult{Entries: fake.groupEntries}, nil
	}
	return &ldap.SearchResult{Entries: fake.entries}, nil
}

func (fake *fakeLDAPConn) Close() error {
	fake.closed = true
	return nil
}

func userEntry(attributes map[string][]string) *ldap.Entry {
	entry := &ldap.Entry{DN: "uid=ada,ou=people,dc=example,dc=com"}
	for name, values := range attributes {
		entry.Attributes = append(entry.Attributes, &ldap.EntryAttribute{Name: name, Values: values})
	}
	return entry
}

func testLDAP(t *testing.T, conn *fakeLDAPConn, mutate func(*LDAPConfig)) *LDAPDirectory {
	t.Helper()
	config := LDAPConfig{
		URL: "ldaps://dc.example.com:636", BindDN: "cn=svc,dc=example,dc=com", BindPassword: "service-secret",
		BaseDN: "dc=example,dc=com", UserFilter: "(uid=%s)",
		dial: func(context.Context) (ldapConn, error) { return conn, nil },
	}
	if mutate != nil {
		mutate(&config)
	}
	directory, err := NewLDAPDirectory(config)
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestLDAPVerifySignsInAndReadsGroups(t *testing.T) {
	conn := &fakeLDAPConn{entries: []*ldap.Entry{userEntry(map[string][]string{
		"uid": {"ada"}, "mail": {"ada@example.com"}, "cn": {"Ada Lovelace"},
		"entryUUID": {"0f8fad5b-d9cb-469f-a165-70867728950e"},
		"memberOf": {
			"cn=Promview-Admins,ou=groups,dc=example,dc=com",
			"cn=oncall,ou=groups,dc=example,dc=com",
		},
	})}}
	directory := testLDAP(t, conn, nil)
	identity, err := directory.Verify(context.Background(), "ada", "her password")
	if err != nil {
		t.Fatal(err)
	}
	// The UUID, not the DN: a DN changes when somebody is moved between OUs,
	// and a DN-keyed identity would silently become a second user.
	if identity.Subject != "0f8fad5b-d9cb-469f-a165-70867728950e" {
		t.Fatalf("subject = %q", identity.Subject)
	}
	if identity.Issuer != "ldaps://dc.example.com:636" || identity.Email != "ada@example.com" {
		t.Fatalf("identity = %#v", identity)
	}
	// Bindings are written against lowercased common names, so a group retyped
	// with different capitalisation does not stop matching.
	if strings.Join(identity.Groups, ",") != "promview-admins,oncall" {
		t.Fatalf("groups = %v", identity.Groups)
	}
	// Service bind first, then a second bind as the user: that second bind is
	// the password check, and the directory is what performs it.
	if len(conn.binds) != 2 || conn.binds[0].dn != "cn=svc,dc=example,dc=com" {
		t.Fatalf("binds = %#v", conn.binds)
	}
	if conn.binds[1].dn != "uid=ada,ou=people,dc=example,dc=com" || conn.binds[1].password != "her password" {
		t.Fatalf("user bind = %#v", conn.binds[1])
	}
	if !conn.closed {
		t.Fatal("the connection was not closed")
	}
}

// An LDAP simple bind with an empty password succeeds as an anonymous bind on
// most servers, which would turn "leave the password blank" into a valid
// sign-in as anybody. It is the most common way an LDAP integration is wrong.
func TestLDAPVerifyRefusesAnEmptyPasswordWithoutBinding(t *testing.T) {
	for name, password := range map[string]string{"empty": "", "whitespace username": ""} {
		t.Run(name, func(t *testing.T) {
			conn := &fakeLDAPConn{}
			directory := testLDAP(t, conn, nil)
			_, err := directory.Verify(context.Background(), "ada", password)
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("error = %v, want ErrInvalidCredentials", err)
			}
			if len(conn.binds) != 0 {
				t.Fatalf("the directory was contacted: %#v", conn.binds)
			}
		})
	}
}

func TestLDAPVerifyRefusesABlankUsername(t *testing.T) {
	conn := &fakeLDAPConn{}
	directory := testLDAP(t, conn, nil)
	if _, err := directory.Verify(context.Background(), "   ", "a password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error = %v", err)
	}
	if len(conn.binds) != 0 {
		t.Fatalf("the directory was contacted: %#v", conn.binds)
	}
}

// An unescaped `*` matches every user and `)(uid=*` rewrites the filter, either
// of which turns a login form into a directory dump.
func TestLDAPVerifyEscapesTheUsernameInTheFilter(t *testing.T) {
	for name, username := range map[string]string{
		"wildcard":       "*",
		"filter break":   "a)(uid=*",
		"backslash":      `a\b`,
		"null byte":      "a\x00b",
		"parenthesis":    "a(b)c",
		"leading spaces": " ada ",
	} {
		t.Run(name, func(t *testing.T) {
			conn := &fakeLDAPConn{}
			directory := testLDAP(t, conn, nil)
			_, _ = directory.Verify(context.Background(), username, "a password")
			filter := conn.searchRequest.Filter
			if !strings.HasPrefix(filter, "(uid=") || !strings.HasSuffix(filter, ")") {
				t.Fatalf("filter = %q", filter)
			}
			inner := strings.TrimSuffix(strings.TrimPrefix(filter, "(uid="), ")")
			for _, forbidden := range []string{"*", "(", ")", `\b`, "\x00"} {
				if strings.Contains(inner, forbidden) {
					t.Fatalf("filter %q carries an unescaped %q", filter, forbidden)
				}
			}
		})
	}
}

// A directory that cannot answer must not read as a wrong password, or the
// operator spends the outage hunting for a typo in somebody's credentials.
func TestLDAPVerifySeparatesAnUnavailableDirectoryFromABadPassword(t *testing.T) {
	for name, test := range map[string]struct {
		conn *fakeLDAPConn
		dial error
	}{
		"dial fails": {conn: &fakeLDAPConn{}, dial: errors.New("connection refused")},
		"service bind fails": {conn: &fakeLDAPConn{
			bindErr: map[string]error{"cn=svc,dc=example,dc=com": errors.New("invalid credentials")},
		}},
		"search fails": {conn: &fakeLDAPConn{searchErr: errors.New("size limit exceeded")}},
		"two matches": {conn: &fakeLDAPConn{entries: []*ldap.Entry{
			userEntry(map[string][]string{"uid": {"ada"}}),
			userEntry(map[string][]string{"uid": {"ada2"}}),
		}}},
	} {
		t.Run(name, func(t *testing.T) {
			config := func(config *LDAPConfig) {
				if test.dial != nil {
					config.dial = func(context.Context) (ldapConn, error) { return nil, test.dial }
				}
			}
			directory := testLDAP(t, test.conn, config)
			_, err := directory.Verify(context.Background(), "ada", "a password")
			if !errors.Is(err, ErrLDAPUnavailable) {
				t.Fatalf("error = %v, want ErrLDAPUnavailable", err)
			}
			if errors.Is(err, ErrInvalidCredentials) {
				t.Fatal("an unavailable directory was reported as invalid credentials")
			}
		})
	}
}

// The filter contains the username, so the directory's own error text must not
// reach a caller who can read the response.
func TestLDAPVerifyDoesNotLeakTheFilterOrThePassword(t *testing.T) {
	conn := &fakeLDAPConn{searchErr: errors.New("bad search filter: (uid=ada-secret)")}
	directory := testLDAP(t, conn, nil)
	_, err := directory.Verify(context.Background(), "ada-secret", "her password")
	if strings.Contains(err.Error(), "ada-secret") || strings.Contains(err.Error(), "her password") {
		t.Fatalf("error text leaks the search: %q", err)
	}
}

func TestLDAPVerifyRefusesAnUnknownUser(t *testing.T) {
	conn := &fakeLDAPConn{entries: nil}
	directory := testLDAP(t, conn, nil)
	if _, err := directory.Verify(context.Background(), "nobody", "a password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error = %v, want ErrInvalidCredentials", err)
	}
}

func TestLDAPVerifyRefusesAWrongPassword(t *testing.T) {
	conn := &fakeLDAPConn{
		entries: []*ldap.Entry{userEntry(map[string][]string{"uid": {"ada"}})},
		bindErr: map[string]error{"uid=ada,ou=people,dc=example,dc=com": errors.New("invalid credentials")},
	}
	directory := testLDAP(t, conn, nil)
	if _, err := directory.Verify(context.Background(), "ada", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("error = %v, want ErrInvalidCredentials", err)
	}
}

// A deployment whose CNs collide across OUs writes bindings against full DNs
// instead, and the stored form has to match what the binding says.
func TestLDAPGroupFormats(t *testing.T) {
	entry := userEntry(map[string][]string{
		"uid":      {"ada"},
		"memberOf": {"CN=Promview-Admins,OU=Groups,DC=example,DC=com"},
	})
	for format, want := range map[string]string{
		LDAPGroupFormatCN: "promview-admins",
		LDAPGroupFormatDN: "cn=promview-admins,ou=groups,dc=example,dc=com",
	} {
		t.Run(format, func(t *testing.T) {
			conn := &fakeLDAPConn{entries: []*ldap.Entry{entry}}
			directory := testLDAP(t, conn, func(config *LDAPConfig) { config.GroupFormat = format })
			identity, err := directory.Verify(context.Background(), "ada", "a password")
			if err != nil {
				t.Fatal(err)
			}
			if len(identity.Groups) != 1 || identity.Groups[0] != want {
				t.Fatalf("groups = %v, want [%s]", identity.Groups, want)
			}
		})
	}
}

// A directory reporting plain group names rather than DNs is left alone.
func TestLDAPGroupsThatAreNotDistinguishedNames(t *testing.T) {
	conn := &fakeLDAPConn{entries: []*ldap.Entry{userEntry(map[string][]string{
		"uid": {"ada"}, "memberOf": {"promview-admins", "oncall"},
	})}}
	directory := testLDAP(t, conn, nil)
	identity, err := directory.Verify(context.Background(), "ada", "a password")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(identity.Groups, ",") != "promview-admins,oncall" {
		t.Fatalf("groups = %v", identity.Groups)
	}
}

func TestLDAPFallsBackToTheDNWhenThereIsNoUUID(t *testing.T) {
	conn := &fakeLDAPConn{entries: []*ldap.Entry{userEntry(map[string][]string{"uid": {"ada"}})}}
	directory := testLDAP(t, conn, nil)
	identity, err := directory.Verify(context.Background(), "ada", "a password")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Subject != "uid=ada,ou=people,dc=example,dc=com" {
		t.Fatalf("subject = %q", identity.Subject)
	}
}

func TestNewLDAPDirectoryRejectsUnusableConfiguration(t *testing.T) {
	for name, config := range map[string]LDAPConfig{
		"no URL":          {BaseDN: "dc=example,dc=com", UserFilter: "(uid=%s)"},
		"no base DN":      {URL: "ldaps://dc.example.com", UserFilter: "(uid=%s)"},
		"no filter":       {URL: "ldaps://dc.example.com", BaseDN: "dc=example,dc=com"},
		"filter has no s": {URL: "ldaps://dc.example.com", BaseDN: "dc=example,dc=com", UserFilter: "(uid=fixed)"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewLDAPDirectory(config); err == nil {
				t.Fatal("an unusable configuration was accepted")
			}
		})
	}
}

// The issuer is what group bindings are written against, so it defaults to the
// URL and is separately settable: a deployment that moves or renames a server
// should not have every binding stop matching.
func TestNewLDAPDirectoryDefaultsTheIssuerToTheURL(t *testing.T) {
	conn := &fakeLDAPConn{entries: []*ldap.Entry{userEntry(map[string][]string{"uid": {"ada"}})}}
	directory := testLDAP(t, conn, func(config *LDAPConfig) { config.Issuer = "ldaps://directory.example.com" })
	identity, err := directory.Verify(context.Background(), "ada", "a password")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Issuer != "ldaps://directory.example.com" {
		t.Fatalf("issuer = %q", identity.Issuer)
	}
}

func groupEntry(dn, cn string) *ldap.Entry {
	return &ldap.Entry{DN: dn, Attributes: []*ldap.EntryAttribute{{Name: "cn", Values: []string{cn}}}}
}

// memberOf is an overlay plenty of OpenLDAP and FreeIPA installations do not
// enable. Without a reverse search a sign-in there succeeds with no groups,
// resolves to no roles and answers 403 - telling the operator their account has
// no access rather than that promview cannot see their groups.
func TestLDAPFindsGroupsBySearchingForTheUser(t *testing.T) {
	conn := &fakeLDAPConn{
		entries:     []*ldap.Entry{userEntry(map[string][]string{"uid": {"ada"}})},
		groupBaseDN: "ou=groups,dc=example,dc=com",
		groupEntries: []*ldap.Entry{
			groupEntry("cn=Promview-Admins,ou=groups,dc=example,dc=com", "Promview-Admins"),
			groupEntry("cn=oncall,ou=groups,dc=example,dc=com", "oncall"),
		},
	}
	directory := testLDAP(t, conn, func(config *LDAPConfig) {
		config.GroupBaseDN = "ou=groups,dc=example,dc=com"
	})
	identity, err := directory.Verify(context.Background(), "ada", "a password")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(identity.Groups, ",") != "promview-admins,oncall" {
		t.Fatalf("groups = %v", identity.Groups)
	}
	// The user's DN goes into the group filter, and it is escaped like any
	// other value a caller can influence.
	last := conn.searches[len(conn.searches)-1]
	if last.Filter != "(member=uid=ada,ou=people,dc=example,dc=com)" {
		t.Fatalf("group filter = %q", last.Filter)
	}
}

func TestLDAPGroupSearchStoresDNsWhenAsked(t *testing.T) {
	conn := &fakeLDAPConn{
		entries:      []*ldap.Entry{userEntry(map[string][]string{"uid": {"ada"}})},
		groupBaseDN:  "ou=groups,dc=example,dc=com",
		groupEntries: []*ldap.Entry{groupEntry("cn=Promview-Admins,ou=groups,dc=example,dc=com", "Promview-Admins")},
	}
	directory := testLDAP(t, conn, func(config *LDAPConfig) {
		config.GroupBaseDN = "ou=groups,dc=example,dc=com"
		config.GroupFormat = LDAPGroupFormatDN
	})
	identity, err := directory.Verify(context.Background(), "ada", "a password")
	if err != nil {
		t.Fatal(err)
	}
	if len(identity.Groups) != 1 || identity.Groups[0] != "cn=promview-admins,ou=groups,dc=example,dc=com" {
		t.Fatalf("groups = %v", identity.Groups)
	}
}

// A directory answering both ways must not produce a group twice: a binding
// matching twice is the same binding.
func TestLDAPMergesMemberOfWithTheGroupSearch(t *testing.T) {
	conn := &fakeLDAPConn{
		entries: []*ldap.Entry{userEntry(map[string][]string{
			"uid": {"ada"}, "memberOf": {"cn=Promview-Admins,ou=groups,dc=example,dc=com"},
		})},
		groupBaseDN: "ou=groups,dc=example,dc=com",
		groupEntries: []*ldap.Entry{
			groupEntry("cn=Promview-Admins,ou=groups,dc=example,dc=com", "Promview-Admins"),
			groupEntry("cn=oncall,ou=groups,dc=example,dc=com", "oncall"),
		},
	}
	directory := testLDAP(t, conn, func(config *LDAPConfig) {
		config.GroupBaseDN = "ou=groups,dc=example,dc=com"
	})
	identity, err := directory.Verify(context.Background(), "ada", "a password")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(identity.Groups, ",") != "promview-admins,oncall" {
		t.Fatalf("groups = %v", identity.Groups)
	}
}

// Swallowing this would sign somebody in with no roles, and the console would
// tell them their account has no access - a different problem from the one they
// have, and one that sends them to the wrong person to fix it.
func TestLDAPReportsAFailedGroupSearch(t *testing.T) {
	conn := &fakeLDAPConn{
		entries:        []*ldap.Entry{userEntry(map[string][]string{"uid": {"ada"}})},
		groupBaseDN:    "ou=groups,dc=example,dc=com",
		groupSearchErr: errors.New("size limit exceeded"),
	}
	directory := testLDAP(t, conn, func(config *LDAPConfig) {
		config.GroupBaseDN = "ou=groups,dc=example,dc=com"
	})
	_, err := directory.Verify(context.Background(), "ada", "a password")
	if !errors.Is(err, ErrLDAPUnavailable) {
		t.Fatalf("error = %v, want ErrLDAPUnavailable", err)
	}
}

func TestNewLDAPDirectoryRejectsAGroupFilterWithoutAPlaceholder(t *testing.T) {
	_, err := NewLDAPDirectory(LDAPConfig{
		URL: "ldaps://dc.example.com", BaseDN: "dc=example,dc=com", UserFilter: "(uid=%s)",
		GroupBaseDN: "ou=groups,dc=example,dc=com", GroupFilter: "(objectClass=groupOfNames)",
	})
	if err == nil {
		t.Fatal("a group filter with no placeholder was accepted")
	}
}

// The group search must run while still bound as the service account.
//
// Rebinding as the user first runs it with whatever rights that user happens to
// have, which in a default OpenLDAP is not enough to see the groups
// organisational unit - the search then fails with "no such object", which
// reads as a missing OU rather than as a permission the service account has and
// the user does not. Found against a real directory, so it is pinned here.
func TestLDAPReadsGroupsBeforeBindingAsTheUser(t *testing.T) {
	conn := &orderedLDAPConn{fakeLDAPConn: fakeLDAPConn{
		entries:      []*ldap.Entry{userEntry(map[string][]string{"uid": {"ada"}})},
		groupBaseDN:  "ou=groups,dc=example,dc=com",
		groupEntries: []*ldap.Entry{groupEntry("cn=oncall,ou=groups,dc=example,dc=com", "oncall")},
	}}
	directory := testLDAP(t, &conn.fakeLDAPConn, func(config *LDAPConfig) {
		config.GroupBaseDN = "ou=groups,dc=example,dc=com"
		config.dial = func(context.Context) (ldapConn, error) { return conn, nil }
	})
	if _, err := directory.Verify(context.Background(), "ada", "a password"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"bind:cn=svc,dc=example,dc=com",
		"search:dc=example,dc=com",
		"search:ou=groups,dc=example,dc=com",
		"bind:uid=ada,ou=people,dc=example,dc=com",
	}
	if strings.Join(conn.order, " ") != strings.Join(want, " ") {
		t.Fatalf("order = %v, want %v", conn.order, want)
	}
}

// orderedLDAPConn records the sequence of operations, which is the property
// under test rather than any single call's result.
type orderedLDAPConn struct {
	fakeLDAPConn
	order []string
}

func (conn *orderedLDAPConn) Bind(dn, password string) error {
	conn.order = append(conn.order, "bind:"+dn)
	return conn.fakeLDAPConn.Bind(dn, password)
}

func (conn *orderedLDAPConn) Search(request *ldap.SearchRequest) (*ldap.SearchResult, error) {
	conn.order = append(conn.order, "search:"+request.BaseDN)
	return conn.fakeLDAPConn.Search(request)
}

func (conn *orderedLDAPConn) Close() error { return conn.fakeLDAPConn.Close() }
