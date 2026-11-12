package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pixl-media/gantry/agent/internal/session"
)

// httptest.NewRequest uses this host; a matching Origin is same-origin.
const sameOriginHeader = "http://example.com"

func login(t *testing.T, h http.Handler, token string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/session", strings.NewReader(`{"token":"`+token+`"}`))
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
	rec := login(t, h, "t", map[string]string{"Origin": sameOriginHeader})
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
	req := httptest.NewRequest(method, path, nil)
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
	rec := login(t, h, "t", map[string]string{"Origin": sameOriginHeader})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	set := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"gantry_session=v1.", "HttpOnly", "SameSite=Strict", "Path=/", "Max-Age=43200"} {
		if !strings.Contains(set, want) {
			t.Errorf("Set-Cookie = %q, missing %q", set, want)
		}
	}
	if strings.Contains(set, "Secure") {
		t.Errorf("Set-Cookie = %q: Secure over plain HTTP would make browsers drop it", set)
	}

	// Behind a TLS-terminating proxy the cookie is Secure.
	rec = login(t, h, "t", map[string]string{"X-Forwarded-Proto": "https"})
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

	// A tampered cookie is rejected.
	bad := *c
	bad.Value = strings.Replace(c.Value, "v1.", "v1.9", 1)
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
	if rec := login(t, h, "t", map[string]string{"Origin": "https://evil.example"}); rec.Code != http.StatusForbidden {
		t.Errorf("login from another site = %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/session", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	if rec := serve(h, req); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON = %d", rec.Code)
	}

	// The wrong attempt above counts; after 10 in total the client is blocked,
	// even with the right token.
	for range maxFailedLogins - 1 {
		login(t, h, "wrong", nil)
	}
	rec = login(t, h, "t", nil)
	if e := decode[Error](t, rec); rec.Code != http.StatusTooManyRequests || *e.Code != ErrorCodeTooManyAttempts || rec.Header().Get("Retry-After") == "" {
		t.Errorf("after %d failures = %d %+v, Retry-After %q", maxFailedLogins, rec.Code, e, rec.Header().Get("Retry-After"))
	}
}

func TestLogout(t *testing.T) {
	h, _ := newOpsServer(t, runner{})
	// Public, so it works without (or with an expired) session.
	rec := serve(h, httptest.NewRequest(http.MethodDelete, "/api/v1/session", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rec.Code)
	}
	if set := rec.Header().Get("Set-Cookie"); !strings.Contains(set, "gantry_session=;") || !strings.Contains(set, "Max-Age=0") {
		t.Errorf("Set-Cookie = %q, want an expired cookie", set)
	}
}

func TestSessionNeedsAuthentication(t *testing.T) {
	h, _ := newOpsServer(t, runner{})
	if rec := serve(h, httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/session without auth = %d", rec.Code)
	}
}
