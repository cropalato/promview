package auth

import (
	"context"
	"errors"
	"net/url"
	"time"
)

/*
Signing in from a client that cannot hold a cookie.

The browser flow ends with a Set-Cookie and a redirect to the console. A
desktop shell gets neither, so it starts the same flow with a loopback redirect
and receives a one-time code there instead. It then exchanges that code for a
session over POST.

The desktop never speaks to the identity provider and never sees a token issued
by one; it only ever holds a Promview session, which an administrator can
revoke like any other. The code exists so the credential itself never travels
in a URL, where it would land in browser history, a proxy log, or a Referer
header.
*/

// DesktopCodeTTL is deliberately short. The desktop redeems the code the moment
// its loopback listener receives it, so the only thing a longer window buys is
// a larger replay opportunity.
const DesktopCodeTTL = 60 * time.Second

var (
	// ErrDesktopCodeInvalid covers unknown, expired, and already-redeemed
	// codes alike. Telling them apart would let a caller probe for which codes
	// have existed.
	ErrDesktopCodeInvalid = errors.New("desktop authorization code is invalid")
	// ErrDesktopRedirectInvalid is returned for a redirect that is not a
	// loopback address.
	ErrDesktopRedirectInvalid = errors.New("desktop redirect must be a loopback http address")
)

// DesktopCode is a one-time authorization code bound to a user.
//
// Only the hash is stored, for the same reason session tokens are: a database
// copy must not be replayable.
type DesktopCode struct {
	CodeHash  []byte
	UserID    int64
	ExpiresAt time.Time
}

type DesktopCodeRepository interface {
	StoreDesktopCode(context.Context, DesktopCode) error
	// ConsumeDesktopCode deletes the code and returns whose it was. Deleting
	// as part of the read is what makes it single-use even when two requests
	// race.
	ConsumeDesktopCode(ctx context.Context, codeHash []byte, now time.Time) (int64, error)
}

// ValidateDesktopRedirect accepts only a loopback address the operating system
// can route to the client that asked.
//
// This is the open-redirect boundary of the whole flow. The server will send a
// freshly minted credential to whatever this returns, so anything that is not
// unmistakably the local machine is refused: a hostname that merely resolves to
// 127.0.0.1 today is not the same promise, and neither is a URL carrying its
// own query the callback would have to merge with.
func ValidateDesktopRedirect(raw string) (string, error) {
	if raw == "" {
		return "", ErrDesktopRedirectInvalid
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", ErrDesktopRedirectInvalid
	}
	// Plain http only: a loopback listener cannot present a certificate anyone
	// would trust, and https here would be a lie rather than protection.
	if parsed.Scheme != "http" {
		return "", ErrDesktopRedirectInvalid
	}
	if !isLoopbackHost(parsed.Hostname()) {
		return "", ErrDesktopRedirectInvalid
	}
	if parsed.Port() == "" {
		// The port is how the desktop's listener is found; without one this
		// would go to port 80 on the user's machine.
		return "", ErrDesktopRedirectInvalid
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		// The callback appends the code as a query. Merging with one the caller
		// supplied is an ambiguity nobody needs.
		return "", ErrDesktopRedirectInvalid
	}
	if parsed.User != nil {
		return "", ErrDesktopRedirectInvalid
	}
	return parsed.String(), nil
}

// isLoopbackHost matches only the literal loopback addresses. "localhost" is
// included because every platform resolves it locally and the OAuth native-app
// guidance names it; anything else is a name whose meaning can change.
func isLoopbackHost(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	default:
		return false
	}
}

// DesktopRedirectWithCode appends the one-time code to a validated redirect.
func DesktopRedirectWithCode(redirect string, code string) (string, error) {
	parsed, err := url.Parse(redirect)
	if err != nil {
		return "", ErrDesktopRedirectInvalid
	}
	query := parsed.Query()
	query.Set("code", code)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
