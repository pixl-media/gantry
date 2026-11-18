package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pixl-media/gantry/agent/internal/oidc"
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
	// The login page asks which ways in exist; single sign-on starts and
	// returns before there is a session.
	"GET /api/v1/session/methods":       true,
	"GET /api/v1/session/oidc":          true,
	"GET /api/v1/session/oidc/callback": true,
}

type authMethod string

const (
	authToken  authMethod = "token"  // Authorization: Bearer <token>
	authCookie authMethod = "cookie" // session cookie from the web UI
)

type authInfo struct {
	method  authMethod
	expires time.Time // cookie only
	session string    // cookie only: the session, to end streams with it
	user    string    // cookie only: who logged in by single sign-on
}

type authKey struct{}

type authenticator struct {
	// apiToken is accepted only as "Authorization: Bearer", uiToken only by
	// the login (docs/decisions.md, decision 26). Either may be empty: that way is off.
	apiToken string
	uiToken  string
	// sso logs in through an identity provider (docs/decisions.md,
	// decision 30); nil means off.
	sso      *oidc.Client
	sessions *session.Store
	// limiter counts wrong tokens per client, from logins and bearer
	// headers alike.
	limiter  *session.Limiter
	failures *failureLog
	logger   *slog.Logger
	net      network

	// publicOrigin is the web UI's origin from GANTRY_PUBLIC_URL, e.g.
	// "https://gantry.example.com", or "". When set, cookie requests must
	// come from exactly this origin, scheme included.
	publicOrigin string
	// cookieName is session.CookieName, with the __Host- prefix for an
	// https public URL: browsers then accept the cookie only if it is
	// Secure, for this host, and set by this host. flowCookieName is the
	// same for single sign-on's flow cookie.
	cookieName     string
	flowCookieName string
}

// webLogin reports whether the web UI can log in at all, so that sessions
// exist.
func (a *authenticator) webLogin() bool { return a.uiToken != "" || a.sso != nil }

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
		Secure:   strings.HasPrefix(a.publicOrigin, "https://") || a.net.secureTransport(r),
	}
}

// validAPIToken reports whether got is the API token.
func (a *authenticator) validAPIToken(got string) bool { return tokenMatches(a.apiToken, got) }

// validUIToken reports whether got is the web login token.
func (a *authenticator) validUIToken(got string) bool { return tokenMatches(a.uiToken, got) }

// tokenMatches compares got with want in constant time. It compares SHA-256
// digests, so the time doesn't depend on the lengths either. An empty want
// matches nothing.
func tokenMatches(want, got string) bool {
	w := sha256.Sum256([]byte(want))
	g := sha256.Sum256([]byte(got))
	return want != "" && subtle.ConstantTimeCompare(g[:], w[:]) == 1
}

// limitKey is the key r's client is counted under by the login limit.
func (a *authenticator) limitKey(r *http.Request) string {
	return limitKey(a.net.client(r))
}

// errTooManyAttempts carries how long a client has to wait.
type errTooManyAttempts struct{ wait time.Duration }

func (e errTooManyAttempts) Error() string { return "too many failed attempts" }

// bearerToken returns the token of an "Authorization: Bearer" header. Other
// schemes (such as Basic auth from a proxy in front) aren't gantry's.
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(token), true
}

// authenticate checks the bearer token, or else the session cookie. A wrong
// bearer token doesn't fall back to the cookie, and counts as a failed
// attempt: guessing the token over the API is limited like logins. Only the
// API token works here; the web login token gets a session at the login.
func (a *authenticator) authenticate(r *http.Request) (authInfo, error) {
	if got, ok := bearerToken(r); ok {
		correct := a.validAPIToken(got)
		client := a.limitKey(r)
		if ok, wait := a.limiter.Attempt(client, correct); !ok {
			return authInfo{}, errTooManyAttempts{wait}
		}
		if !correct {
			a.failures.record(client, string(authToken))
			return authInfo{}, errUnauthorized
		}
		return authInfo{method: authToken}, nil
	}
	if !a.webLogin() {
		return authInfo{}, errUnauthorized // no web login, so no sessions
	}
	if c, err := r.Cookie(a.cookieName); err == nil {
		if s, ok := a.sessions.Verify(c.Value); ok {
			return authInfo{method: authCookie, expires: s.Expires, session: c.Value, user: s.User}, nil
		}
	}
	return authInfo{}, errUnauthorized
}

// sessionBound returns ctx, canceled when the session that authenticated the
// request ends: logout, expiry, or eviction. Event streams run under it, so
// they don't outlive a logout. Requests with a bearer token are unaffected.
func (a *authenticator) sessionBound(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	info, _ := ctx.Value(authKey{}).(authInfo)
	if info.method != authCookie {
		return ctx, cancel
	}
	done, exp, ok := a.sessions.Done(info.session)
	if !ok {
		cancel()
		return ctx, cancel
	}
	go func() {
		expired := time.NewTimer(time.Until(exp))
		defer expired.Stop()
		select {
		case <-ctx.Done():
		case <-done:
			cancel()
		case <-expired.C:
			cancel()
		}
	}()
	return ctx, cancel
}

// errInsecure refuses credentials sent over plain HTTP from the internet.
var errInsecure = errors.New("refusing credentials sent over plain HTTP from an internet address: " +
	"put gantry behind HTTPS (see the README), or set GANTRY_ALLOW_INSECURE_HTTP=true at your own risk")

// refuseInsecure answers 403 https_required if r must not be served over
// its transport.
func (a *authenticator) refuseInsecure(w http.ResponseWriter, r *http.Request) bool {
	if !a.net.insecure(r) {
		return false
	}
	writeJSON(w, http.StatusForbidden, apiError(errInsecure, ErrorCodeHttpsRequired))
	return true
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
		// Responses are JSON or event streams; never let a browser guess
		// another type.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			// API data is live and may include secrets (env reveal): never
			// cache it.
			w.Header().Set("Cache-Control", "no-store")
		}
		if strings.HasPrefix(a.publicOrigin, "https://") {
			// Browsers that have seen this once only use HTTPS from then on.
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if publicRoutes[r.Method+" "+r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		// Before checking credentials, so plain HTTP from the internet
		// never works, not even for a correct token.
		if a.refuseInsecure(w, r) {
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
		if !safeMethod(r.Method) {
			// Who changed something, and whether from a browser (session)
			// or a script (token). Never bodies: they can hold secrets.
			args := []any{"method", r.Method, "path", r.URL.Path,
				"client", a.net.client(r).String(), "via", auditVia(info.method)}
			if info.user != "" {
				args = append(args, "user", info.user)
			}
			a.logger.Info("api request", args...)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authKey{}, info)))
	})
}

// auditVia names an authentication method in audit logs.
func auditVia(m authMethod) string {
	if m == authCookie {
		return "session"
	}
	return "token"
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin reports whether the Origin header names the configured public
// origin, or else the host the request was sent to; for a request that came
// over HTTPS, with the https scheme. Browsers always send Origin with POST,
// PUT, PATCH and DELETE requests.
func (a *authenticator) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	if a.publicOrigin != "" {
		return strings.EqualFold(origin, a.publicOrigin)
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host != r.Host {
		return false
	}
	// A page served over plain HTTP from the same host name (e.g. by someone
	// on the network) is another origin than the HTTPS one.
	return u.Scheme == "https" || !a.net.secureTransport(r)
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
