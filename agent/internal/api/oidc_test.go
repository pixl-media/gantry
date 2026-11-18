package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pixl-media/gantry/agent/internal/oidc"
	"github.com/pixl-media/gantry/agent/internal/oidc/fakeidp"
)

const ssoOrigin = "https://gantry.example.com"

// ssoSetup returns handlers with single sign-on against a fake provider
// (no web login token), the provider, and the agent's log.
func ssoSetup(t *testing.T, mod func(*Config)) (http.Handler, *fakeidp.Provider, *bytes.Buffer) {
	t.Helper()
	p := fakeidp.New(t)
	p.Claims = map[string]any{"groups": []string{"admins"}, "email": "alice@example.com", "email_verified": true}
	logs := &bytes.Buffer{}
	cfg := Config{
		Token: "the-api-token",
		OIDC: oidc.New(oidc.Config{
			Issuer: p.Issuer(), ClientID: p.ClientID, ClientSecret: p.ClientSecret,
			RedirectURL:   ssoOrigin + "/api/v1/session/oidc/callback",
			AllowedGroups: []string{"admins"},
			HTTPClient:    p.Client(),
		}),
		Stacks:       fakeStacks{},
		Logger:       slog.New(slog.NewTextHandler(logs, nil)),
		PublicOrigin: ssoOrigin,
	}
	if mod != nil {
		mod(&cfg)
	}
	return New(cfg), p, logs
}

// ssoStart opens the login route and returns the response.
func ssoStart(h http.Handler, returnTo string) *httptest.ResponseRecorder {
	return serve(h, newRequest(http.MethodGet, "/api/v1/session/oidc?returnTo="+url.QueryEscape(returnTo), nil))
}

// ssoCallback comes back from the provider with query and the cookies of
// start (the flow cookie).
func ssoCallback(h http.Handler, query url.Values, start *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	req := newRequest(http.MethodGet, "/api/v1/session/oidc/callback?"+query.Encode(), nil)
	if start != nil {
		for _, c := range start.Result().Cookies() {
			req.AddCookie(c)
		}
	}
	return serve(h, req)
}

// ssoLogin runs a whole login and returns the callback's response.
func ssoLogin(t *testing.T, h http.Handler, p *fakeidp.Provider, returnTo string) *httptest.ResponseRecorder {
	t.Helper()
	start := ssoStart(h, returnTo)
	if start.Code != http.StatusFound || !strings.HasPrefix(start.Header().Get("Location"), p.URL+"/auth?") {
		t.Fatalf("start = %d to %q", start.Code, start.Header().Get("Location"))
	}
	return ssoCallback(h, p.Login(t, start.Header().Get("Location")), start)
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestSSOLogin(t *testing.T) {
	h, p, logs := ssoSetup(t, nil)

	start := ssoStart(h, "/stacks/whoami")
	flow := cookieNamed(start, "__Host-gantry_oidc")
	if start.Code != http.StatusFound || flow == nil {
		t.Fatalf("start = %d, cookies %v", start.Code, start.Result().Cookies())
	}
	if !flow.HttpOnly || !flow.Secure || flow.SameSite != http.SameSiteLaxMode || flow.Path != "/" || flow.MaxAge != 600 {
		t.Errorf("flow cookie = %+v, want HttpOnly, Secure, Lax, path /, 10 minutes", flow)
	}

	back := ssoCallback(h, p.Login(t, start.Header().Get("Location")), start)
	if back.Code != http.StatusFound || back.Header().Get("Location") != "/stacks/whoami" {
		t.Fatalf("callback = %d to %q", back.Code, back.Header().Get("Location"))
	}
	if back.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("callback passes its URL on as referrer")
	}
	s := cookieNamed(back, "__Host-gantry_session")
	if s == nil || s.Value == "" || !s.HttpOnly || !s.Secure || s.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %+v, want HttpOnly, Secure, Strict", s)
	}
	if cleared := cookieNamed(back, "__Host-gantry_oidc"); cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("flow cookie not cleared: %+v", cleared)
	}

	// The session works, without a web login token, and knows the user.
	rec := serve(h, withCookie(http.MethodGet, "/api/v1/session", s, ""))
	got := decode[Session](t, rec)
	if rec.Code != http.StatusOK || got.Method != SessionMethodCookie || got.User == nil || *got.User != "alice@example.com" {
		t.Fatalf("session = %d %+v", rec.Code, got)
	}

	// Changes are logged with the user.
	req := withCookie(http.MethodPost, "/api/v1/stacks", s, ssoOrigin)
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(strings.NewReader(`{`))
	serve(h, req)
	for _, want := range []string{
		`msg="single sign-on login" client=127.0.0.1 user=alice@example.com subject=user-1`,
		`msg="api request" method=POST path=/api/v1/stacks client=127.0.0.1 via=session user=alice@example.com`,
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, logs)
		}
	}
}

func TestLoginMethods(t *testing.T) {
	sso := oidc.New(oidc.Config{Issuer: "https://idp.example.com", ClientID: "gantry", AllowedGroups: []string{"admins"}})
	for _, tc := range []struct {
		cfg  Config
		want LoginMethods
	}{
		{Config{Token: "the-api-token"}, LoginMethods{}},
		{Config{UIToken: testUIToken}, LoginMethods{Token: true}},
		{Config{OIDC: sso}, LoginMethods{Oidc: true}},
		{Config{UIToken: testUIToken, OIDC: sso}, LoginMethods{Token: true, Oidc: true}},
	} {
		tc.cfg.Stacks = fakeStacks{}
		rec := serve(New(tc.cfg), newRequest(http.MethodGet, "/api/v1/session/methods", nil))
		if got := decode[LoginMethods](t, rec); rec.Code != http.StatusOK || got != tc.want {
			t.Errorf("methods = %d %+v, want %+v", rec.Code, got, tc.want)
		}
	}
}

// Without a web login token, the token login stays off next to single
// sign-on.
func TestSSOWithoutUIToken(t *testing.T) {
	h, _, _ := ssoSetup(t, nil)
	for _, token := range []string{testUIToken, "the-api-token"} {
		rec := login(t, h, token, nil)
		if e := decode[Error](t, rec); rec.Code != http.StatusForbidden || *e.Code != ErrorCodeLoginDisabled {
			t.Errorf("token login = %d %+v, want 403 login_disabled", rec.Code, e)
		}
	}
}

func TestSSOErrors(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		h := New(Config{UIToken: testUIToken, Stacks: fakeStacks{}})
		for _, path := range []string{"/api/v1/session/oidc", "/api/v1/session/oidc/callback?code=x&state=y"} {
			rec := serve(h, newRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login?error=oidc_disabled" {
				t.Errorf("%s = %d to %q", path, rec.Code, rec.Header().Get("Location"))
			}
		}
	})
	t.Run("not on the allow list", func(t *testing.T) {
		h, p, logs := ssoSetup(t, nil)
		p.Claims = map[string]any{"groups": []string{"users"}, "preferred_username": "mallory"}
		rec := ssoLogin(t, h, p, "/")
		if rec.Header().Get("Location") != "/login?error=oidc_denied" || cookieNamed(rec, "__Host-gantry_session") != nil {
			t.Fatalf("callback to %q, cookies %v", rec.Header().Get("Location"), rec.Result().Cookies())
		}
		if !strings.Contains(logs.String(), "not on the allow list") || !strings.Contains(logs.String(), "user=mallory") {
			t.Errorf("log lacks the denied user:\n%s", logs)
		}
	})
	t.Run("provider down", func(t *testing.T) {
		h, p, _ := ssoSetup(t, nil)
		p.Set(func(p *fakeidp.Provider) { p.Down = true })
		rec := ssoStart(h, "/")
		if rec.Header().Get("Location") != "/login?error=oidc_unavailable" {
			t.Errorf("start to %q", rec.Header().Get("Location"))
		}
	})
	t.Run("not started in this browser", func(t *testing.T) {
		h, p, _ := ssoSetup(t, nil)
		start := ssoStart(h, "/")
		q := p.Login(t, start.Header().Get("Location"))
		rec := ssoCallback(h, q, nil) // no flow cookie: e.g. a link from an attacker
		if rec.Header().Get("Location") != "/login?error=oidc_failed" || cookieNamed(rec, "__Host-gantry_session") != nil {
			t.Fatalf("callback to %q, cookies %v", rec.Header().Get("Location"), rec.Result().Cookies())
		}
		if p.Exchanges != 0 {
			t.Errorf("%d code exchanges without the flow cookie", p.Exchanges)
		}
	})
	t.Run("only paths of this site", func(t *testing.T) {
		h, p, _ := ssoSetup(t, nil)
		for _, to := range []string{"//evil.example.com", "https://evil.example.com", "/api/v1/stacks"} {
			if rec := ssoLogin(t, h, p, to); rec.Header().Get("Location") != "/" {
				t.Errorf("returnTo %q: callback to %q, want /", to, rec.Header().Get("Location"))
			}
		}
	})
	t.Run("plain HTTP from the internet", func(t *testing.T) {
		h, _, _ := ssoSetup(t, nil)
		for _, path := range []string{"/api/v1/session/oidc", "/api/v1/session/oidc/callback?code=x&state=y"} {
			req := newRequest(http.MethodGet, path, nil)
			req.RemoteAddr = "203.0.113.7:1234"
			rec := serve(h, req)
			if rec.Header().Get("Location") != "/login?error=https_required" {
				t.Errorf("%s to %q, want https_required", path, rec.Header().Get("Location"))
			}
		}
	})
}

// Failed callbacks count like wrong tokens; once a client is blocked, the
// agent doesn't call the provider for it anymore.
func TestSSOFailuresAreLimited(t *testing.T) {
	h, p, _ := ssoSetup(t, nil)

	// Without the flow cookie nothing reaches the provider: not counted.
	for range maxFailedAttempts + 1 {
		ssoCallback(h, url.Values{"code": {"x"}, "state": {"y"}}, nil)
	}
	for range maxFailedAttempts {
		start := ssoStart(h, "/")
		q := p.Login(t, start.Header().Get("Location"))
		q.Set("code", "guessed")
		if rec := ssoCallback(h, q, start); rec.Header().Get("Location") != "/login?error=oidc_failed" {
			t.Fatalf("wrong code: callback to %q", rec.Header().Get("Location"))
		}
	}
	rec := ssoLogin(t, h, p, "/")
	if rec.Header().Get("Location") != "/login?error=too_many_attempts" || cookieNamed(rec, "__Host-gantry_session") != nil {
		t.Fatalf("blocked client: callback to %q", rec.Header().Get("Location"))
	}
	if p.Exchanges != maxFailedAttempts {
		t.Errorf("%d code exchanges, want %d: a blocked client reached the provider", p.Exchanges, maxFailedAttempts)
	}
}

// Accounts that aren't allowed and logins ended at the provider don't
// count: they ask nothing extra of the provider, and behind a shared proxy
// address they would lock everyone out.
func TestSSODenialsAreNotCounted(t *testing.T) {
	h, p, _ := ssoSetup(t, nil)
	allowed := p.Claims
	p.Claims = map[string]any{"groups": []string{"users"}}
	for range maxFailedAttempts + 1 {
		if rec := ssoLogin(t, h, p, "/"); rec.Header().Get("Location") != "/login?error=oidc_denied" {
			t.Fatalf("denied: callback to %q", rec.Header().Get("Location"))
		}
		start := ssoStart(h, "/")
		rec := ssoCallback(h, url.Values{"error": {"access_denied"}}, start)
		if rec.Header().Get("Location") != "/login?error=oidc_failed" {
			t.Fatalf("canceled: callback to %q", rec.Header().Get("Location"))
		}
	}
	p.Claims = allowed
	if rec := ssoLogin(t, h, p, "/"); rec.Header().Get("Location") != "/" {
		t.Errorf("allowed after denials: callback to %q", rec.Header().Get("Location"))
	}
}
