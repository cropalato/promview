package auth

import (
	"context"
)

// DirectoryIdentity is who a directory says somebody is.
//
// OIDC, LDAP and local accounts all produce one; everything downstream of here
// - the user upsert, the group replacement, the grant resolution, the session -
// is the same code for all three. Nothing about a particular protocol belongs
// in it, which is why the OIDC nonce is returned beside it rather than in it.
type DirectoryIdentity struct {
	// Issuer names the directory, and is what a group binding is written
	// against: an OIDC issuer URL, or the canonical URL of an LDAP server.
	Issuer      string
	Subject     string
	Username    string
	Email       string
	DisplayName string
	Groups      []string
}

// DirectoryIdentityRepository turns an identity into a principal, creating or
// updating the user and its group memberships on the way through.
type DirectoryIdentityRepository interface {
	ResolveDirectoryIdentity(context.Context, DirectoryIdentity) (Principal, error)
}
