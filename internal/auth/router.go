package auth

import (
	"net/http"
)

// Router owns /api/v1/auth/*.
//
// It exists because logout is not an OIDC concept. Every mode that issues a
// session has to be able to end one, and the only route that could was owned by
// the OIDC handler - so adding a second mode that signs people in would have
// added one they could not sign out of.
type Router struct {
	sessions     *SessionManager
	cookieSecure bool
	// oidc is nil in every mode but oidc. A path with no handler behind it
	// answers 404, which is what a server that predates the mode would do, and
	// is a truer answer than a 501 the console has no way to act on.
	oidc *OIDCHandler
}

type RouterConfig struct {
	Sessions     *SessionManager
	CookieSecure bool
	OIDC         *OIDCHandler
}

func NewRouter(config RouterConfig) *Router {
	return &Router{
		sessions:     config.Sessions,
		cookieSecure: config.CookieSecure,
		oidc:         config.OIDC,
	}
}

func (router *Router) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/api/v1/auth/logout" {
		if request.Method != http.MethodPost {
			response.Header().Set("Allow", http.MethodPost)
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		router.logout(response, request)
		return
	}
	if router.oidc != nil {
		router.oidc.ServeHTTP(response, request)
		return
	}
	http.NotFound(response, request)
}

// logout revokes the session and expires its cookie.
//
// A revoke that finds no session is not an error: the caller wanted to be
// signed out, and they are. Only a storage failure is reported, because there
// the session is still live and saying otherwise would be a lie the operator
// acts on.
func (router *Router) logout(response http.ResponseWriter, request *http.Request) {
	if err := router.sessions.Revoke(request.Context(), request); err != nil {
		http.Error(response, "could not end session", http.StatusInternalServerError)
		return
	}
	clearCookie(response, SessionCookieName, "/", router.cookieSecure)
	response.WriteHeader(http.StatusNoContent)
}
