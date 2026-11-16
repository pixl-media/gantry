package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
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
	token    string
	sessions *session.Store
	// limiter counts wrong tokens per client, from logins and bearer
	// headers alike.
	limiter *session.Limiter
	logger  *slog.Logger

	// publicOrigin is the web UI's origin from GANTRY_PUBLIC_URL, e.g.
	// "https://gantry.example.com", or "". When set, cookie requests must
	// come from exactly this origin, scheme included.
	publicOrigin string
	// cookieName is session.CookieName, with the __Host- prefix for an
	// https public URL: browsers then accept the cookie only if it is
	// Secure, for this host, and set by this host.
	cookieName string
}

// cookie returns the session cookie with its security attributes. maxAge
// -1 deletes it.
func (a *authenticator) cookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     a.cookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,                    // not readable by JavaScript
		SameSite: http.SameSiteStrictMode, // not sent with requests from other sites
		Secure:   strings.HasPrefix(a.publicOrigin, "https://") || isHTTPS(r),
	}
}

// validToken compares got with the configured token in constant time. An
// empty configured token matches nothing.
func (a *authenticator) validToken(got string) bool {
	return a.token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) == 1
}

// errTooManyAttempts carries how long a client has to wait.
type errTooManyAttempts struct{ wait time.Duration }

func (e errTooManyAttempts) Error() string { return "too many failed attempts" }

// authenticate checks the bearer token, or else the session cookie. A wrong
// Authorization header doesn't fall back to the cookie, and counts as a
// failed attempt: guessing the token over the API is limited like logins.
func (a *authenticator) authenticate(r *http.Request) (authInfo, error) {
	if header := r.Header.Get("Authorization"); header != "" {
		got, isBearer := strings.CutPrefix(header, "Bearer ")
		correct := isBearer && a.validToken(got)
		client := clientAddr(r)
		if ok, wait := a.limiter.Attempt(client, correct); !ok {
			return authInfo{}, errTooManyAttempts{wait}
		}
		if !correct {
			a.logger.Warn("failed authentication", "client", client, "method", authToken)
			return authInfo{}, errUnauthorized
		}
		return authInfo{method: authToken}, nil
	}
	if a.token == "" {
		return authInfo{}, errUnauthorized
	}
	if c, err := r.Cookie(a.cookieName); err == nil {
		if exp, ok := a.sessions.Verify(c.Value); ok {
			return authInfo{method: authCookie, expires: exp}, nil
		}
	}
	return authInfo{}, errUnauthorized
}

var errUnauthorized = errors.New("unauthorized")

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
		info, err := a.authenticate(r)
		if limited, ok := errors.AsType[errTooManyAttempts](err); ok {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter(limited.wait)))
			writeJSON(w, http.StatusTooManyRequests, apiError(limited, ErrorCodeTooManyAttempts))
			return
		}
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, apiError(err, ErrorCodeUnauthorized))
			return
		}
		if info.method == authCookie && !safeMethod(r.Method) && !a.sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, apiError(errors.New("cross-origin request rejected"), ErrorCodeOriginRejected))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authKey{}, info)))
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin reports whether the Origin header names the configured public
// origin, or else the host the request was sent to. Browsers always send
// Origin with POST, PUT, PATCH and DELETE requests.
func (a *authenticator) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	if a.publicOrigin != "" {
		return strings.EqualFold(origin, a.publicOrigin)
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

// ParsePublicURL checks GANTRY_PUBLIC_URL, the address the web UI is
// reached at, and returns its origin: scheme and host, without a default
// port. An empty value returns "".
func ParsePublicURL(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("GANTRY_PUBLIC_URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("GANTRY_PUBLIC_URL %q: want scheme and host only, like https://gantry.example.com", s)
	}
	host := strings.ToLower(u.Host)
	if port := u.Port(); (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		host = strings.ToLower(u.Hostname())
	}
	return u.Scheme + "://" + host, nil
}
