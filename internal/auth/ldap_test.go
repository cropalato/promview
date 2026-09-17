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
	entries       []*ldap.Entry
	searchErr     error
	closed        bool
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
	if fake.searchErr != nil {
		return nil, fake.searchErr
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
