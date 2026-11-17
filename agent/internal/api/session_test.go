package api

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pixl-media/gantry/agent/internal/session"
)

// httptest.NewRequest uses this host; a matching Origin is same-origin.
const sameOriginHeader = "http://example.com"

// testUIToken is the web login token of the handlers under test.
const testUIToken = "the-ui-token"

func login(t *testing.T, h http.Handler, token string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest(http.MethodPost, "/api/v1/session", strings.NewReader(`{"token":"`+token+`"}`))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// sessionCookieFrom logs in and returns the session cookie.
func sessionCookieFrom(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	rec := login(t, h, testUIToken, map[string]string{"Origin": sameOriginHeader})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName {
			return c
		}
	}
	t.Fatal("no session cookie set")
	return nil
}

func withCookie(method, path string, c *http.Cookie, origin string) *http.Request {
	req := newRequest(method, path, nil)
	req.AddCookie(c)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLoginSetsSecureCookie(t *testing.T) {
	h, _ := newOpsServer(t, runner{})
	rec := login(t, h, testUIToken, map[string]string{"Origin": sameOriginHeader})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	set := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"gantry_session=", "HttpOnly", "SameSite=Strict", "Path=/", "Max-Age=43200"} {
		if !strings.Contains(set, want) {
			t.Errorf("Set-Cookie = %q, missing %q", set, want)
		}
	}
	if strings.Contains(set, "Secure") {
		t.Errorf("Set-Cookie = %q: Secure over plain HTTP would make browsers drop it", set)
	}

	// Behind a TLS-terminating proxy the cookie is Secure.
	rec = login(t, h, testUIToken, map[string]string{"X-Forwarded-Proto": "https"})
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "Secure") {
		t.Errorf("Set-Cookie behind HTTPS proxy = %q, want Secure", rec.Header().Get("Set-Cookie"))
	}
}

func TestCookieAuthentication(t *testing.T) {
	h, _ := newOpsServer(t, runner{})
	c := sessionCookieFrom(t, h)

	if rec := serve(h, withCookie(http.MethodGet, "/api/v1/stacks", c, "")); rec.Code != http.StatusOK {
		t.Errorf("GET with cookie = %d", rec.Code)
	}
	s := decode[Session](t, serve(h, withCookie(http.MethodGet, "/api/v1/session", c, "")))
	if s.Method != SessionMethodCookie || s.ExpiresAt == nil {
		t.Errorf("session = %+v", s)
	}
	if s := decode[Session](t, request(t, h, http.MethodGet, "/api/v1/session")); s.Method != SessionMethodToken || s.ExpiresAt != nil {
		t.Errorf("session with bearer = %+v", s)
	}

	// An unknown or altered cookie is rejected.
	bad := *c
	bad.Value = c.Value + "x"
	if rec := serve(h, withCookie(http.MethodGet, "/api/v1/stacks", &bad, "")); rec.Code != http.StatusUnauthorized {
		t.Errorf("tampered cookie = %d", rec.Code)
	}

	// A wrong bearer token doesn't fall back to a valid cookie.
	req := withCookie(http.MethodGet, "/api/v1/stacks", c, "")
	req.Header.Set("Authorization", "Bearer wrong")
	if rec := serve(h, req); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong bearer + cookie = %d", rec.Code)
	}
}

func TestCSRFProtection(t *testing.T) {
	h, ops := newOpsServer(t, runner{})
	c := sessionCookieFrom(t, h)
	path := "/api/v1/stacks/app/actions/stop"

	tests := []struct {
		name   string
		origin string
		want   int
	}{
		{"no Origin", "", http.StatusForbidden},
		{"other site", "https://evil.example", http.StatusForbidden},
		{"same origin", sameOriginHeader, http.StatusAccepted},
	}
	for _, tt := range tests {
		rec := serve(h, withCookie(http.MethodPost, path, c, tt.origin))
		if rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d: %s", tt.name, rec.Code, tt.want, rec.Body)
		}
		if rec.Code == http.StatusAccepted {
			waitDone(t, ops, decode[Operation](t, rec).Id)
		}
	}

	// API clients with a bearer token don't send Origin; that's fine.
	if rec := request(t, h, http.MethodPost, path); rec.Code != http.StatusAccepted {
		t.Errorf("bearer without Origin = %d", rec.Code)
	} else {
		waitDone(t, ops, decode[Operation](t, rec).Id)
	}
}

func TestLoginFailures(t *testing.T) {
	h, _ := newOpsServer(t, runner{})

	rec := login(t, h, "wrong", nil)
	if e := decode[Error](t, rec); rec.Code != http.StatusUnauthorized || *e.Code != ErrorCodeInvalidToken {
		t.Errorf("wrong token = %d %+v", rec.Code, e)
	}
	if rec := login(t, h, testUIToken, map[string]string{"Origin": "https://evil.example"}); rec.Code != http.StatusForbidden {
		t.Errorf("login from another site = %d", rec.Code)
	}

	req := newRequest(http.MethodPost, "/api/v1/session", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	if rec := serve(h, req); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d", rec.Code)
	}

	// The wrong attempt above counts; after 10 in total the client is blocked,
	// even with the right token.
	for range maxFailedAttempts - 1 {
		login(t, h, "wrong", nil)
	}
	rec = login(t, h, testUIToken, nil)
	if e := decode[Error](t, rec); rec.Code != http.StatusTooManyRequests || *e.Code != ErrorCodeTooManyAttempts || rec.Header().Get("Retry-After") == "" {
		t.Errorf("after %d failures = %d %+v, Retry-After %q", maxFailedAttempts, rec.Code, e, rec.Header().Get("Retry-After"))
	}
}

// Guessing the token with bearer headers is limited like logins, and shares
// the limit with them.
func TestBearerGuessingIsLimited(t *testing.T) {
	var logs bytes.Buffer
	h := New(Config{Token: "the-token", UIToken: testUIToken, Stacks: fakeStacks{}, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	get := func(token, remote string) *httptest.ResponseRecorder {
		req := newRequest(http.MethodGet, "/api/v1/version", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if remote != "" {
			req.RemoteAddr = remote
		}
		return serve(h, req)
	}

	for i := range maxFailedAttempts {
		if rec := get(fmt.Sprintf("guess-%d", i), ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d = %d", i, rec.Code)
		}
	}
	rec := get("the-token", "")
	if e := decode[Error](t, rec); rec.Code != http.StatusTooManyRequests || *e.Code != ErrorCodeTooManyAttempts || rec.Header().Get("Retry-After") == "" {
		t.Errorf("right token while blocked = %d %+v, Retry-After %q", rec.Code, e, rec.Header().Get("Retry-After"))
	}
	if rec := login(t, h, testUIToken, nil); rec.Code != http.StatusTooManyRequests {
		t.Errorf("login while blocked = %d, want 429 (shared limit)", rec.Code)
	}
	if rec := get("the-token", "10.0.0.7:4000"); rec.Code != http.StatusOK {
		t.Errorf("another client = %d, want 200", rec.Code)
	}
	if !strings.Contains(logs.String(), "failed authentication") || strings.Contains(logs.String(), "guess-") {
		t.Errorf("logs must record failures without the token sent:\n%s", logs.String())
	}
}

func TestParsePublicURL(t *testing.T) {
	good := map[string]string{
		"":                                "",
		"https://gantry.example.com":      "https://gantry.example.com",
		"https://Gantry.Example.com:443/": "https://gantry.example.com",
		"http://nas.local:8080":           "http://nas.local:8080",
	}
	for in, want := range good {
		if got, err := ParsePublicURL(in); err != nil || got != want {
			t.Errorf("ParsePublicURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"ftp://x", "gantry.example.com", "https://", "https://x/sub", "https://x?a=1", "https://user@x"} {
		if _, err := ParsePublicURL(bad); err == nil {
			t.Errorf("ParsePublicURL(%q) accepted", bad)
		}
	}
}

// With an https public URL, the cookie gets the __Host- prefix and Secure,
// and cookie requests must come from exactly that origin.
func TestPublicOrigin(t *testing.T) {
	const origin = "https://gantry.example.com"
	h := New(Config{Token: "the-token", UIToken: testUIToken, Stacks: fakeStacks{}, PublicOrigin: origin})

	rec := login(t, h, testUIToken, map[string]string{"Origin": origin})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login = %d: %s", rec.Code, rec.Body)
	}
	set := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"__Host-gantry_session=", "Secure", "Path=/"} {
		if !strings.Contains(set, want) {
			t.Errorf("Set-Cookie = %q, missing %q", set, want)
		}
	}
	if strings.Contains(set, "Domain=") {
		t.Errorf("Set-Cookie = %q: __Host- cookies must not have a Domain", set)
	}
	c := rec.Result().Cookies()[0]

	if rec := login(t, h, testUIToken, map[string]string{"Origin": "http://gantry.example.com"}); rec.Code != http.StatusForbidden {
		t.Errorf("login from the http origin = %d, want 403", rec.Code)
	}
	if rec := serve(h, withCookie(http.MethodGet, "/api/v1/stacks", c, "")); rec.Code != http.StatusOK {
		t.Errorf("GET with cookie = %d", rec.Code)
	}
	// Changing requests: the host alone isn't enough, the scheme must match.
	for o, want := range map[string]int{origin: http.StatusBadRequest, "http://gantry.example.com": http.StatusForbidden} {
		rec := serve(h, withCookie(http.MethodPost, "/api/v1/stacks/app/actions/explode", c, o))
		if rec.Code != want {
			t.Errorf("POST from %s = %d, want %d", o, rec.Code, want)
		}
	}
	// The same value under the unprefixed name isn't a session.
	plain := &http.Cookie{Name: "gantry_session", Value: c.Value}
	if rec := serve(h, withCookie(http.MethodGet, "/api/v1/stacks", plain, "")); rec.Code != http.StatusUnauthorized {
		t.Errorf("unprefixed cookie = %d, want 401", rec.Code)
	}
}

func TestLogout(t *testing.T) {
	h, _ := newOpsServer(t, runner{})
	// Public, so it works without (or with an expired) session.
	rec := serve(h, newRequest(http.MethodDelete, "/api/v1/session", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rec.Code)
	}
	if set := rec.Header().Get("Set-Cookie"); !strings.Contains(set, "gantry_session=;") || !strings.Contains(set, "Max-Age=0") {
		t.Errorf("Set-Cookie = %q, want an expired cookie", set)
	}

	// Logging out ends the session in the agent too: a copy of the cookie
	// (say, captured by someone else) stops working at once.
	c := sessionCookieFrom(t, h)
	other := sessionCookieFrom(t, h)
	if rec := serve(h, withCookie(http.MethodDelete, "/api/v1/session", c, sameOriginHeader)); rec.Code != http.StatusNoContent {
		t.Fatalf("logout with cookie = %d", rec.Code)
	}
	if rec := serve(h, withCookie(http.MethodGet, "/api/v1/stacks", c, "")); rec.Code != http.StatusUnauthorized {
		t.Errorf("cookie after logout = %d, want 401", rec.Code)
	}
	if rec := serve(h, withCookie(http.MethodGet, "/api/v1/stacks", other, "")); rec.Code != http.StatusOK {
		t.Errorf("another session after logout = %d, want 200", rec.Code)
	}
}

func TestSessionNeedsAuthentication(t *testing.T) {
	h, _ := newOpsServer(t, runner{})
	if rec := serve(h, newRequest(http.MethodGet, "/api/v1/session", nil)); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/session without auth = %d", rec.Code)
	}
}

// Each token works only in its own place (docs/decisions.md, decision 26), and using the other
// one counts as a failed attempt.
func TestTokensAreSeparate(t *testing.T) {
	h := New(Config{Token: "the-api-token", UIToken: testUIToken, Stacks: fakeStacks{}})

	rec := login(t, h, "the-api-token", nil)
	if e := decode[Error](t, rec); rec.Code != http.StatusUnauthorized || *e.Code != ErrorCodeInvalidToken {
		t.Errorf("login with the API token = %d %+v, want 401 invalid_token", rec.Code, e)
	}
	if rec := do(t, h, "/api/v1/stacks", testUIToken); rec.Code != http.StatusUnauthorized {
		t.Errorf("bearer with the web login token = %d, want 401", rec.Code)
	}
	if rec := login(t, h, testUIToken, nil); rec.Code != http.StatusNoContent {
		t.Errorf("login with the web login token = %d, want 204", rec.Code)
	}
	if rec := do(t, h, "/api/v1/stacks", "the-api-token"); rec.Code != http.StatusOK {
		t.Errorf("bearer with the API token = %d, want 200", rec.Code)
	}

	// Mix-ups are failed attempts: enough of them block the client.
	h = New(Config{Token: "the-api-token", UIToken: testUIToken, Stacks: fakeStacks{}})
	for i := range maxFailedAttempts {
		if i%2 == 0 {
			login(t, h, "the-api-token", nil)
		} else {
			do(t, h, "/api/v1/stacks", testUIToken)
		}
	}
	if rec := do(t, h, "/api/v1/stacks", "the-api-token"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("after %d mix-ups = %d, want 429", maxFailedAttempts, rec.Code)
	}
}

// Without a web login token, the login is off and no cookie is accepted.
func TestWebLoginOff(t *testing.T) {
	h := New(Config{Token: "the-api-token", Stacks: fakeStacks{}})
	for _, token := range []string{"the-api-token", testUIToken, ""} {
		rec := login(t, h, token, nil)
		if e := decode[Error](t, rec); rec.Code != http.StatusForbidden || *e.Code != ErrorCodeLoginDisabled {
			t.Errorf("login with %q = %d %+v, want 403 login_disabled", token, rec.Code, e)
		}
	}
	// A cookie that looks like a session gets nowhere.
	req := withCookie(http.MethodGet, "/api/v1/stacks", &http.Cookie{Name: session.CookieName, Value: "forged"}, "")
	if rec := serve(h, req); rec.Code != http.StatusUnauthorized {
		t.Errorf("cookie without a web login = %d, want 401", rec.Code)
	}
	if rec := do(t, h, "/api/v1/stacks", "the-api-token"); rec.Code != http.StatusOK {
		t.Errorf("bearer = %d, want 200", rec.Code)
	}
}

// Without an API token, bearer headers never work; the web login does.
func TestAPITokenOff(t *testing.T) {
	h := New(Config{UIToken: testUIToken, Stacks: fakeStacks{}})
	for _, token := range []string{testUIToken, "anything"} {
		if rec := do(t, h, "/api/v1/stacks", token); rec.Code != http.StatusUnauthorized {
			t.Errorf("bearer %q = %d, want 401", token, rec.Code)
		}
	}
	c := sessionCookieFrom(t, h)
	if rec := serve(h, withCookie(http.MethodGet, "/api/v1/stacks", c, "")); rec.Code != http.StatusOK {
		t.Errorf("with the session = %d, want 200", rec.Code)
	}
}

// Changing requests are logged with how they were authenticated, never with
// their bodies; reading requests aren't.
func TestAuditLog(t *testing.T) {
	var logs bytes.Buffer
	h := New(Config{Token: "the-api-token", UIToken: testUIToken, Stacks: fakeStacks{},
		Logger: slog.New(slog.NewTextHandler(&logs, nil))})

	if rec := do(t, h, "/api/v1/stacks", "the-api-token"); rec.Code != http.StatusOK {
		t.Fatalf("get = %d", rec.Code)
	}
	if strings.Contains(logs.String(), "api request") {
		t.Errorf("a GET was logged: %s", logs.String())
	}

	// Invalid JSON: the handler never runs, the request is still logged.
	req := newRequest(http.MethodPost, "/api/v1/stacks", strings.NewReader(`{"name":"body-secret"`))
	req.Header.Set("Authorization", "Bearer the-api-token")
	req.Header.Set("Content-Type", "application/json")
	serve(h, req)
	c := sessionCookieFrom(t, h)
	req = withCookie(http.MethodPost, "/api/v1/stacks", c, sameOriginHeader)
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(strings.NewReader(`{"name":"body-secret"`))
	serve(h, req)

	got := logs.String()
	for _, want := range []string{
		`msg="api request" method=POST path=/api/v1/stacks client=127.0.0.1 via=token`,
		`msg="api request" method=POST path=/api/v1/stacks client=127.0.0.1 via=session`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
	for _, secret := range []string{"body-secret", "the-api-token", testUIToken} {
		if strings.Contains(got, secret) {
			t.Errorf("log contains %q:\n%s", secret, got)
		}
	}
}
