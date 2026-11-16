package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pixl-media/gantry/agent/internal/session"
	"github.com/pixl-media/gantry/agent/internal/stackfiles"
)

// publicRoutes are served without authentication, as "METHOD /path".
// Everything else requires it, so new routes are protected by default.
var publicRoutes = map[string]bool{
	"GET /healthz":           true,
	"HEAD /healthz":          true,
	"POST /api/v1/session":   true, // log in
	"DELETE /api/v1/session": true, // log out, also with an expired session
}

type authMethod string

const (
	authToken  authMethod = "token"  // Authorization: Bearer <token>
	authCookie authMethod = "cookie" // session cookie from the web UI
)

type authInfo struct {
	method  authMethod
	expires time.Time // cookie only
}

type authKey struct{}

type authenticator struct {
	token  string
	signer *session.Signer
}

// authenticate checks the bearer token, or else the session cookie. An empty
// configured token rejects everything. A wrong Authorization header doesn't
// fall back to the cookie.
func (a *authenticator) authenticate(r *http.Request) (authInfo, bool) {
	if a.token == "" {
		return authInfo{}, false
	}
	if header := r.Header.Get("Authorization"); header != "" {
		got, ok := strings.CutPrefix(header, "Bearer ")
		if ok && subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) == 1 {
			return authInfo{method: authToken}, true
		}
		return authInfo{}, false
	}
	if c, err := r.Cookie(session.CookieName); err == nil {
		if exp, ok := a.signer.Verify(c.Value); ok {
			return authInfo{method: authCookie, expires: exp}, true
		}
	}
	return authInfo{}, false
}

// Request body limits. Bodies are small JSON documents; the largest holds a
// Compose file (at most stackfiles.MaxSize) as a JSON string, which escaping
// can make a few times larger. The login is public, so its limit is tight:
// the body is read before any check runs.
const (
	maxLoginBody = 4 << 10
	maxBody      = 4 * stackfiles.MaxSize
)

// middleware enforces authentication on everything but publicRoutes. With
// cookie authentication, changing requests must come from the same origin:
// browsers attach cookies automatically, so this blocks cross-site request
// forgery in addition to SameSite=Strict. It also limits request bodies.
func (a *authenticator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(maxBody)
		if r.URL.Path == "/api/v1/session" {
			limit = maxLoginBody
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			// API data is live and may include secrets (env reveal): never
			// cache it. Event streams set their own value.
			w.Header().Set("Cache-Control", "no-store")
		}
		if publicRoutes[r.Method+" "+r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		info, ok := a.authenticate(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, apiError(errors.New("unauthorized"), ErrorCodeUnauthorized))
			return
		}
		if info.method == authCookie && !safeMethod(r.Method) && !sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, apiError(errors.New("cross-origin request rejected"), ErrorCodeOriginRejected))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authKey{}, info)))
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin reports whether the Origin header names the host the request
// was sent to. Browsers always send Origin with POST and DELETE requests.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}
