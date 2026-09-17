package auth

// LDAP sign-in, search-then-bind.
//
// A service account binds and searches for the user, then the connection binds
// again as that user with the password they supplied. The directory itself is
// what verifies the password; promview never sees a stored hash and never
// stores one.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// ldapConn is the slice of the client this package uses.
//
// Narrow on purpose: it makes the flow testable against a fake, so the tests
// that matter - an empty password, an escaped filter, a search matching two
// people - do not need a directory to run against.
type ldapConn interface {
	Bind(username, password string) error
	Search(*ldap.SearchRequest) (*ldap.SearchResult, error)
	Close() error
}

// LDAPConfig is what a directory sign-in needs.
type LDAPConfig struct {
	// URL is the directory, as ldaps://host:636 or ldap://host:389.
	URL string
	// Issuer is what group bindings are written against. Defaults to URL, and
	// is separate so a deployment can move or rename a server without every
	// binding ceasing to match.
	Issuer string
	// BindDN and BindPassword are the service account that performs the search.
	BindDN       string
	BindPassword string
	BaseDN       string
	// UserFilter contains one %s, replaced by the escaped username.
	UserFilter string
	// GroupAttribute names the attribute on the user entry listing its groups,
	// usually memberOf.
	GroupAttribute string
	// GroupFormat is "cn" to store a group's common name or "dn" to store its
	// full distinguished name. CN is what an operator reads off a directory
	// browser; DN is for a deployment whose CNs collide across OUs.
	GroupFormat     string
	UsernameAttr    string
	EmailAttr       string
	DisplayNameAttr string
	StartTLS        bool
	InsecureSkipTLS bool
	Timeout         time.Duration
	dial            func(context.Context) (ldapConn, error)
}

const (
	LDAPGroupFormatCN = "cn"
	LDAPGroupFormatDN = "dn"
)

// ErrLDAPUnavailable marks a directory that could not answer, as opposed to one
// that answered no.
//
// The distinction is the whole point: a service bind that fails because the
// directory is down must not read as a bad password, or the operator spends the
// outage hunting for a typo in somebody's credentials.
var ErrLDAPUnavailable = errors.New("directory is unavailable")

// LDAPDirectory verifies a username and password against an LDAP server.
type LDAPDirectory struct {
	config LDAPConfig
}

func NewLDAPDirectory(config LDAPConfig) (*LDAPDirectory, error) {
	if config.URL == "" || config.BaseDN == "" || config.UserFilter == "" {
		return nil, errors.New("LDAP URL, base DN and user filter are required")
	}
	if !strings.Contains(config.UserFilter, "%s") {
		return nil, errors.New("LDAP user filter must contain %s for the username")
	}
	if config.Issuer == "" {
		config.Issuer = config.URL
	}
	if config.GroupAttribute == "" {
		config.GroupAttribute = "memberOf"
	}
	if config.GroupFormat == "" {
		config.GroupFormat = LDAPGroupFormatCN
	}
	if config.UsernameAttr == "" {
		config.UsernameAttr = "uid"
	}
	if config.EmailAttr == "" {
		config.EmailAttr = "mail"
	}
	if config.DisplayNameAttr == "" {
		config.DisplayNameAttr = "cn"
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	if config.dial == nil {
		config.dial = func(context.Context) (ldapConn, error) { return dialLDAP(config) }
	}
	return &LDAPDirectory{config: config}, nil
}

// Verify signs somebody in against the directory.
func (directory *LDAPDirectory) Verify(
	ctx context.Context,
	username, password string,
) (DirectoryIdentity, error) {
	// Refused before anything is dialled. An LDAP simple bind with an empty
	// password succeeds as an anonymous bind on most servers, which would turn
	// "leave the password blank" into a valid sign-in as anybody. This is the
	// single most common way an LDAP integration is wrong.
	if password == "" || strings.TrimSpace(username) == "" {
		return DirectoryIdentity{}, ErrInvalidCredentials
	}

	conn, err := directory.config.dial(ctx)
	if err != nil {
		return DirectoryIdentity{}, fmt.Errorf("%w: %w", ErrLDAPUnavailable, err)
	}
	defer conn.Close()

	if err := conn.Bind(directory.config.BindDN, directory.config.BindPassword); err != nil {
		// The service account's own failure is ours, not the caller's.
		return DirectoryIdentity{}, fmt.Errorf("%w: service bind failed", ErrLDAPUnavailable)
	}

	entry, err := directory.searchUser(conn, username)
	if err != nil {
		return DirectoryIdentity{}, err
	}

	// The second bind is the actual password check: the directory decides,
	// and promview never learns the password's hash.
	if err := conn.Bind(entry.DN, password); err != nil {
		return DirectoryIdentity{}, ErrInvalidCredentials
	}

	return directory.identityFrom(entry), nil
}

func (directory *LDAPDirectory) searchUser(conn ldapConn, username string) (*ldap.Entry, error) {
	// EscapeFilter is not optional politeness: an unescaped `*` matches every
	// user, and `)(uid=*` rewrites the filter into one of the caller's
	// choosing. Either turns a login form into a directory dump.
	filter := fmt.Sprintf(directory.config.UserFilter, ldap.EscapeFilter(username))
	result, err := conn.Search(&ldap.SearchRequest{
		BaseDN:       directory.config.BaseDN,
		Scope:        ldap.ScopeWholeSubtree,
		DerefAliases: ldap.NeverDerefAliases,
		// Two is enough to detect an ambiguous match, and asking for more would
		// pull entries only to discard them.
		SizeLimit:  2,
		TimeLimit:  int(directory.config.Timeout.Seconds()),
		Filter:     filter,
		Attributes: directory.attributes(),
	})
	if err != nil {
		// The filter contains the username, so the directory's error text never
		// reaches the caller.
		return nil, fmt.Errorf("%w: user search failed", ErrLDAPUnavailable)
	}
	switch len(result.Entries) {
	case 1:
		return result.Entries[0], nil
	case 0:
		return nil, ErrInvalidCredentials
	default:
		// Two matches means the filter is wrong for this directory. Picking one
		// would sign somebody in as whichever entry happened to sort first.
		return nil, fmt.Errorf("%w: user filter matched more than one entry", ErrLDAPUnavailable)
	}
}

func (directory *LDAPDirectory) attributes() []string {
	seen := map[string]bool{}
	attributes := make([]string, 0, 6)
	for _, attribute := range []string{
		directory.config.UsernameAttr, directory.config.EmailAttr,
		directory.config.DisplayNameAttr, directory.config.GroupAttribute,
		// Preferred over the DN as a subject: a DN changes when somebody is
		// moved between OUs, and a DN-keyed identity would silently become a
		// second user with none of the first one's bindings.
		"entryUUID", "objectGUID",
	} {
		if attribute == "" || seen[attribute] {
			continue
		}
		seen[attribute] = true
		attributes = append(attributes, attribute)
	}
	return attributes
}

func (directory *LDAPDirectory) identityFrom(entry *ldap.Entry) DirectoryIdentity {
	subject := entry.GetAttributeValue("entryUUID")
	if subject == "" {
		subject = entry.GetAttributeValue("objectGUID")
	}
	if subject == "" {
		subject = entry.DN
	}
	displayName := entry.GetAttributeValue(directory.config.DisplayNameAttr)
	username := entry.GetAttributeValue(directory.config.UsernameAttr)
	if displayName == "" {
		displayName = username
	}
	return DirectoryIdentity{
		Issuer:      directory.config.Issuer,
		Subject:     subject,
		Username:    username,
		Email:       entry.GetAttributeValue(directory.config.EmailAttr),
		DisplayName: displayName,
		Groups:      directory.groupsFrom(entry),
	}
}

// groupsFrom reduces group memberships to the form bindings are written in.
//
// Lowercased either way, so a binding does not stop matching because somebody
// retyped a group name with different capitalisation in the directory.
func (directory *LDAPDirectory) groupsFrom(entry *ldap.Entry) []string {
	values := entry.GetAttributeValues(directory.config.GroupAttribute)
	groups := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		name := value
		if directory.config.GroupFormat == LDAPGroupFormatCN {
			name = commonNameOf(value)
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		groups = append(groups, name)
	}
	return groups
}

// commonNameOf takes the CN out of a distinguished name. A value that is not a
// DN at all is already a bare group name and is returned as it stands, which is
// what a directory reporting plain names gives.
func commonNameOf(value string) string {
	parsed, err := ldap.ParseDN(value)
	if err != nil || len(parsed.RDNs) == 0 {
		return value
	}
	for _, attribute := range parsed.RDNs[0].Attributes {
		if strings.EqualFold(attribute.Type, "cn") {
			return attribute.Value
		}
	}
	return value
}

// dialLDAP opens the connection.
//
// The library's dialler takes no context, so SetTimeout is what bounds a
// directory that accepts the connection and then says nothing. The caller's
// context still cancels the request around it.
func dialLDAP(config LDAPConfig) (ldapConn, error) {
	parsed, err := url.Parse(config.URL)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{
		ServerName:         parsed.Hostname(),
		InsecureSkipVerify: config.InsecureSkipTLS,
		MinVersion:         tls.VersionTLS12,
	}
	conn, err := ldap.DialURL(config.URL, ldap.DialWithTLSConfig(tlsConfig))
	if err != nil {
		return nil, err
	}
	conn.SetTimeout(config.Timeout)
	if config.StartTLS {
		if err := conn.StartTLS(tlsConfig); err != nil {
			conn.Close()
			// Never fall back to cleartext. A StartTLS that silently degrades
			// puts the user's password on the wire, which is the one outcome
			// this whole path exists to avoid.
			return nil, fmt.Errorf("StartTLS failed: %w", err)
		}
	}
	return conn, nil
}
