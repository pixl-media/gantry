package oidc

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/oidc/fakeidp"
)

const redirectURL = "https://gantry.example.com/api/v1/session/oidc/callback"

func newClient(t *testing.T, p *fakeidp.Provider, mod func(*Config)) *Client {
	t.Helper()
	cfg := Config{
		Issuer:        p.Issuer(),
		ClientID:      p.ClientID,
		ClientSecret:  p.ClientSecret,
		RedirectURL:   redirectURL,
		Scopes:        []string{"profile", "email"},
		AllowedGroups: []string{"admins"},
		HTTPClient:    p.Client(),
	}
	if mod != nil {
		mod(&cfg)
	}
	return New(cfg)
}

// login runs a whole login: Start, the provider, Finish.
func login(t *testing.T, c *Client, p *fakeidp.Provider) (User, string, error) {
	t.Helper()
	authURL, cookie, err := c.Start(context.Background(), "/stacks/whoami")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return c.Finish(context.Background(), cookie, p.Login(t, authURL))
}

func TestLogin(t *testing.T) {
	p := fakeidp.New(t)
	p.Claims = map[string]any{"groups": []string{"users", "admins"}, "email": "Alice@Example.com", "email_verified": true}
	c := newClient(t, p, nil)

	u, returnTo, err := login(t, c, p)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	want := User{Subject: "user-1", Email: "alice@example.com", Name: "alice@example.com"}
	if u != want {
		t.Errorf("user = %+v, want %+v", u, want)
	}
	if returnTo != "/stacks/whoami" {
		t.Errorf("returnTo = %q", returnTo)
	}
}

func TestAuthURL(t *testing.T) {
	p := fakeidp.New(t)
	c := newClient(t, p, nil)
	authURL, _, err := c.Start(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if q.Get("scope") != "openid profile email" {
		t.Errorf("scope = %q, want openid first", q.Get("scope"))
	}
	if q.Get("redirect_uri") != redirectURL || q.Get("code_challenge_method") != "S256" ||
		q.Get("nonce") == "" || len(q.Get("state")) < 43 {
		t.Errorf("auth URL lacks PKCE, nonce or state: %s", authURL)
	}
}

func TestAllowList(t *testing.T) {
	for _, tc := range []struct {
		name   string
		claims map[string]any
		groups []string
		emails []string
		want   error
		user   string
	}{
		{name: "group", claims: map[string]any{"groups": []string{"admins"}, "preferred_username": "alice"}, groups: []string{"admins"}, user: "alice"},
		{name: "group as one string", claims: map[string]any{"groups": "admins"}, groups: []string{"admins"}, user: "user-1"},
		{name: "other group", claims: map[string]any{"groups": []string{"users"}}, groups: []string{"admins"}, want: ErrDenied},
		{name: "group names are exact", claims: map[string]any{"groups": []string{"Admins"}}, groups: []string{"admins"}, want: ErrDenied},
		{name: "no groups", claims: map[string]any{}, groups: []string{"admins"}, want: ErrDenied},
		{name: "groups null", claims: map[string]any{"groups": nil}, groups: []string{"admins"}, want: ErrDenied},
		{name: "verified email", claims: map[string]any{"email": "Bob@example.com", "email_verified": true}, emails: []string{"bob@example.com"}, user: "bob@example.com"},
		{name: "verified as a string", claims: map[string]any{"email": "bob@example.com", "email_verified": "true"}, emails: []string{"bob@example.com"}, user: "bob@example.com"},
		{name: "unverified email", claims: map[string]any{"email": "bob@example.com", "email_verified": false, "preferred_username": "bob"}, emails: []string{"bob@example.com"}, want: ErrDenied, user: "bob"},
		{name: "email without verified", claims: map[string]any{"email": "bob@example.com"}, emails: []string{"bob@example.com"}, want: ErrDenied},
		{name: "other email", claims: map[string]any{"email": "eve@example.com", "email_verified": true}, emails: []string{"bob@example.com"}, want: ErrDenied},
		{name: "groups or email", claims: map[string]any{"email": "bob@example.com", "email_verified": true}, groups: []string{"admins"}, emails: []string{"bob@example.com"}, user: "bob@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fakeidp.New(t)
			p.Claims = tc.claims
			c := newClient(t, p, func(cfg *Config) { cfg.AllowedGroups, cfg.AllowedEmails = tc.groups, tc.emails })
			u, _, err := login(t, c, p)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.user != "" && u.Name != tc.user {
				t.Errorf("user = %q, want %q", u.Name, tc.user)
			}
		})
	}
}

func TestFlowCookie(t *testing.T) {
	p := fakeidp.New(t)
	p.Claims = map[string]any{"groups": []string{"admins"}}
	c := newClient(t, p, nil)
	ctx := context.Background()

	authURL, cookie, err := c.Start(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	q := p.Login(t, authURL)
	payload, sig, _ := strings.Cut(cookie, ".")
	alt := "A"
	if payload[0] == 'A' {
		alt = "B"
	}
	tampered := alt + payload[1:] + "." + sig

	other := newClient(t, p, nil) // another start of the agent: another key
	for name, try := range map[string]func() error{
		"missing":  func() error { _, _, err := c.Finish(ctx, "", q); return err },
		"garbage":  func() error { _, _, err := c.Finish(ctx, "x.y", q); return err },
		"tampered": func() error { _, _, err := c.Finish(ctx, tampered, q); return err },
		"other key": func() error {
			_, _, err := other.Finish(ctx, cookie, q)
			return err
		},
	} {
		if err := try(); !errors.Is(err, ErrNoLogin) {
			t.Errorf("%s: err = %v, want ErrNoLogin", name, err)
		}
	}
	if p.Exchanges != 0 {
		t.Errorf("%d code exchanges without a valid cookie", p.Exchanges)
	}

	// Too late.
	c.now = func() time.Time { return time.Now().Add(FlowTTL + time.Second) }
	if _, _, err := c.Finish(ctx, cookie, q); !errors.Is(err, ErrNoLogin) {
		t.Errorf("expired: err = %v, want ErrNoLogin", err)
	}
	c.now = time.Now
	if _, _, err := c.Finish(ctx, cookie, q); err != nil {
		t.Errorf("in time: %v", err)
	}
}

func TestCallbackChecks(t *testing.T) {
	p := fakeidp.New(t)
	p.Claims = map[string]any{"groups": []string{"admins"}}
	c := newClient(t, p, nil)
	ctx := context.Background()

	start := func() (string, url.Values) {
		authURL, cookie, err := c.Start(ctx, "/")
		if err != nil {
			t.Fatal(err)
		}
		return cookie, p.Login(t, authURL)
	}

	cookie, q := start()
	q.Set("state", "someone-elses-state")
	if _, _, err := c.Finish(ctx, cookie, q); !errors.Is(err, ErrFailed) {
		t.Errorf("wrong state: err = %v", err)
	}

	cookie, _ = start()
	if _, _, err := c.Finish(ctx, cookie, url.Values{"error": {"access_denied"}}); !errors.Is(err, ErrRefused) {
		t.Errorf("provider error: err = %v", err)
	}

	// A code works once; the provider refuses it the second time.
	cookie, q = start()
	if _, _, err := c.Finish(ctx, cookie, q); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Finish(ctx, cookie, q); !errors.Is(err, ErrFailed) {
		t.Errorf("reused code: err = %v, want ErrFailed", err)
	}
}

func TestIDTokenChecks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tweak func(map[string]any)
		ok    bool
	}{
		{name: "other audience", tweak: func(c map[string]any) { c["aud"] = "other-app" }},
		{name: "other issuer", tweak: func(c map[string]any) { c["iss"] = "https://evil.example.com" }},
		{name: "expired", tweak: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{name: "other nonce", tweak: func(c map[string]any) { c["nonce"] = "replayed" }},
		{name: "no nonce", tweak: func(c map[string]any) { delete(c, "nonce") }},
		{name: "several audiences, issued to another", tweak: func(c map[string]any) { c["aud"] = []string{"gantry", "other-app"}; c["azp"] = "other-app" }},
		{name: "several audiences, no azp", tweak: func(c map[string]any) { c["aud"] = []string{"gantry", "other-app"} }},
		{name: "issued to another", tweak: func(c map[string]any) { c["azp"] = "other-app" }},
		{name: "issued to gantry", tweak: func(c map[string]any) { c["azp"] = "gantry" }, ok: true},
		{name: "several audiences, issued to gantry", tweak: func(c map[string]any) { c["aud"] = []string{"gantry", "other-app"}; c["azp"] = "gantry" }, ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fakeidp.New(t)
			p.Claims = map[string]any{"groups": []string{"admins"}}
			p.Tweak = tc.tweak
			_, _, err := login(t, newClient(t, p, nil), p)
			if tc.ok && err != nil {
				t.Fatalf("err = %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrFailed) {
				t.Fatalf("err = %v, want ErrFailed", err)
			}
		})
	}
}

func TestOnlyKeyPairSignatures(t *testing.T) {
	p := fakeidp.New(t)
	p.Claims = map[string]any{"groups": []string{"admins"}}
	p.Alg = "HS256" // signed with the client secret, which gantry knows too
	_, _, err := login(t, newClient(t, p, nil), p)
	if !errors.Is(err, ErrFailed) || !strings.Contains(err.Error(), "key pair") {
		t.Fatalf("err = %v, want ErrFailed with a hint", err)
	}
}

func TestProviderUnavailable(t *testing.T) {
	p := fakeidp.New(t)
	p.Claims = map[string]any{"groups": []string{"admins"}}
	c := newClient(t, p, nil)
	ctx := context.Background()

	p.Set(func(p *fakeidp.Provider) { p.Down = true })
	if _, _, err := c.Start(ctx, "/"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("down: err = %v, want ErrUnavailable", err)
	}
	// Discovery is tried again at the next login.
	p.Set(func(p *fakeidp.Provider) { p.Down = false })
	authURL, cookie, err := c.Start(ctx, "/")
	if err != nil {
		t.Fatalf("up again: %v", err)
	}
	q := p.Login(t, authURL)

	p.Set(func(p *fakeidp.Provider) { p.Down = true })
	if _, _, err := c.Finish(ctx, cookie, q); !errors.Is(err, ErrUnavailable) {
		t.Errorf("down at the exchange: err = %v, want ErrUnavailable", err)
	}
}

func TestIssuerMismatch(t *testing.T) {
	p := fakeidp.New(t)
	c := newClient(t, p, func(cfg *Config) { cfg.Issuer += "/" })
	_, _, err := c.Start(context.Background(), "/")
	if !errors.Is(err, ErrFailed) || !strings.Contains(err.Error(), "GANTRY_OIDC_ISSUER") {
		t.Fatalf("err = %v, want ErrFailed naming GANTRY_OIDC_ISSUER", err)
	}
}

func TestSafeReturn(t *testing.T) {
	for in, want := range map[string]string{
		"":                                     "/",
		"/":                                    "/",
		"/stacks/whoami":                       "/stacks/whoami",
		"/stacks/a?tab=logs":                   "/stacks/a?tab=logs",
		"stacks":                               "/",
		"//evil.example.com":                   "/",
		"/\\evil.example.com":                  "/",
		"/x\\y":                                "/",
		"https://evil.example":                 "/",
		"/api/v1/session":                      "/",
		"/api":                                 "/",
		"/stacks/a\r\nSet-Cookie":              "/",
		"/" + strings.Repeat("a", maxReturnTo): "/",
	} {
		if got := SafeReturn(in); got != want {
			t.Errorf("SafeReturn(%q) = %q, want %q", in, got, want)
		}
	}
}
