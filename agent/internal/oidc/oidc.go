// Package oidc logs web UI users in through an identity provider (OpenID
// Connect), for the agent's single sign-on (docs/decisions.md, decision 30).
//
// The login is the authorization code flow with PKCE, state and nonce. What
// a login needs between leaving for the provider and coming back travels in
// a cookie the agent signs, so nothing is stored; the agent turns a finished
// login into an ordinary session. Only accounts on an allow list (groups or
// verified email addresses) get in.
package oidc

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	goidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// FlowTTL is how long a login may take, from leaving for the identity
// provider to coming back.
const FlowTTL = 10 * time.Minute

// requestTimeout bounds each call to the identity provider.
const requestTimeout = 10 * time.Second

// Config is the single sign-on setup.
type Config struct {
	// Issuer is the provider's issuer URL, exactly as it announces it.
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL is where the provider sends the browser back to: the web
	// UI's public URL plus the callback path.
	RedirectURL string
	// Scopes to ask for; "openid" is always among them.
	Scopes []string
	// AllowedGroups are compared with the ID token's "groups" claim,
	// AllowedEmails (lower case) with its "email" claim if the provider
	// verified the address. At least one is needed.
	AllowedGroups []string
	AllowedEmails []string
	// HTTPClient talks to the provider; nil means one with a timeout.
	HTTPClient *http.Client
}

// User is who logged in.
type User struct {
	Subject string
	// Email is set if the provider verified it.
	Email string
	// Name identifies the user in logs and the web UI: the verified email,
	// or else the user name, or else the subject. A user name may be one
	// the user chose at the provider; Subject is what stays the same.
	Name string
}

// Kinds of failed logins, for errors.Is. Every error from Start and Finish
// is one of them.
var (
	// ErrNoLogin: no login started in this browser, or it took too long.
	ErrNoLogin = errors.New("no login in progress in this browser, or it took longer than 10 minutes")
	// ErrDenied: the account isn't on the allow list.
	ErrDenied = errors.New("account not allowed")
	// ErrRefused: the identity provider sent the browser back with an
	// error, e.g. a canceled login; nothing was asked of the provider.
	ErrRefused = errors.New("the identity provider ended the login")
	// ErrUnavailable: the identity provider can't be reached.
	ErrUnavailable = errors.New("identity provider unavailable")
	// ErrFailed: anything else.
	ErrFailed = errors.New("login failed")
)

// Client runs logins against one identity provider.
type Client struct {
	cfg  Config
	http *http.Client
	now  func() time.Time
	// key signs the flow cookies. It is made at start, so a restart ends
	// logins in progress, like it ends sessions.
	key []byte

	mu       sync.Mutex
	oauth    *oauth2.Config
	verifier *goidc.IDTokenVerifier
}

// New returns a Client for cfg. It doesn't contact the provider: that
// happens at the first login, so the agent starts without it.
func New(cfg Config) *Client {
	if !slices.Contains(cfg.Scopes, goidc.ScopeOpenID) {
		cfg.Scopes = append([]string{goidc.ScopeOpenID}, cfg.Scopes...)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: requestTimeout}
	}
	key := make([]byte, 32)
	rand.Read(key) // never fails (crypto/rand)
	return &Client{cfg: cfg, http: hc, now: time.Now, key: key}
}

// discover returns the provider's endpoints and a verifier for its ID
// tokens, fetching its discovery document until that succeeds once. A
// failure is not kept, so the next login tries again. The lock isn't held
// while fetching: with the provider unreachable, parallel logins each wait
// for their own attempt, not for each other's.
func (c *Client) discover(ctx context.Context) (*oauth2.Config, *goidc.IDTokenVerifier, error) {
	c.mu.Lock()
	oauth, verifier := c.oauth, c.verifier
	c.mu.Unlock()
	if oauth != nil {
		return oauth, verifier, nil
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	p, err := goidc.NewProvider(goidc.ClientContext(ctx, c.http), c.cfg.Issuer)
	if err != nil {
		if _, mismatch := errors.AsType[*goidc.IssuerMismatchError](err); mismatch {
			return nil, nil, fmt.Errorf("%w: %v (set GANTRY_OIDC_ISSUER exactly as the provider announces it)", ErrFailed, err)
		}
		return nil, nil, fmt.Errorf("%w: discovery: %v", ErrUnavailable, err)
	}
	// How the client authenticates at the token endpoint, from discovery.
	// oauth2 would otherwise guess, trying the other way after any failure,
	// which doubles the calls for every rejected code.
	endpoint := p.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInHeader // client_secret_basic, the default
	var meta struct {
		AuthMethods []string `json:"token_endpoint_auth_methods_supported"`
	}
	if p.Claims(&meta) == nil && len(meta.AuthMethods) > 0 &&
		!slices.Contains(meta.AuthMethods, "client_secret_basic") && slices.Contains(meta.AuthMethods, "client_secret_post") {
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	}
	oauth = &oauth2.Config{
		ClientID:     c.cfg.ClientID,
		ClientSecret: c.cfg.ClientSecret,
		Endpoint:     endpoint,
		RedirectURL:  c.cfg.RedirectURL,
		Scopes:       c.cfg.Scopes,
	}
	// Only signatures with a key pair: go-oidc leaves out HS256 and "none"
	// even if the provider offers them. Keys are fetched when needed; the
	// context only carries the HTTP client.
	verifier = p.VerifierContext(goidc.ClientContext(context.Background(), c.http),
		&goidc.Config{ClientID: c.cfg.ClientID, Now: func() time.Time { return c.now() }})

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.oauth == nil { // the first success wins; later ones are the same
		c.oauth, c.verifier = oauth, verifier
	}
	return c.oauth, c.verifier, nil
}

// flow is what a login carries from Start to Finish, in the signed cookie.
type flow struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"` // PKCE
	ReturnTo string `json:"r"`
	Expires  int64  `json:"e"` // Unix seconds
}

// Start begins a login. It returns the provider's URL to send the browser
// to and the value of the flow cookie, which must come back with the
// callback. returnTo is where the browser goes after the login; it should
// already be checked with SafeReturn.
func (c *Client) Start(ctx context.Context, returnTo string) (authURL, cookie string, err error) {
	oauth, _, err := c.discover(ctx)
	if err != nil {
		return "", "", err
	}
	f := flow{
		State:    randomString(),
		Nonce:    randomString(),
		Verifier: oauth2.GenerateVerifier(),
		ReturnTo: returnTo,
		Expires:  c.now().Add(FlowTTL).Unix(),
	}
	authURL = oauth.AuthCodeURL(f.State, goidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier))
	return authURL, c.sign(f), nil
}

// Finish completes a login from the callback's query and the flow cookie,
// and returns who logged in and where the browser goes next. Errors other
// than ErrNoLogin come after the cookie proved that this browser started
// the login.
func (c *Client) Finish(ctx context.Context, cookie string, query url.Values) (User, string, error) {
	f, ok := c.open(cookie)
	if !ok {
		return User{}, "", ErrNoLogin
	}
	if e := query.Get("error"); e != "" {
		return User{}, "", fmt.Errorf("%w: %q: %q", ErrRefused, e, query.Get("error_description"))
	}
	if !equal(query.Get("state"), f.State) {
		return User{}, "", fmt.Errorf("%w: state doesn't match the login started in this browser", ErrFailed)
	}
	code := query.Get("code")
	if code == "" {
		return User{}, "", fmt.Errorf("%w: no code from the identity provider", ErrFailed)
	}
	oauth, verifier, err := c.discover(ctx)
	if err != nil {
		return User{}, "", err
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	ctx = goidc.ClientContext(ctx, c.http)
	token, err := oauth.Exchange(ctx, code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		// An answer below 500 refuses this login; anything else is the
		// provider's trouble.
		if re, answered := errors.AsType[*oauth2.RetrieveError](err); answered &&
			re.Response != nil && re.Response.StatusCode < 500 {
			return User{}, "", fmt.Errorf("%w: code exchange: %v", ErrFailed, err)
		}
		return User{}, "", fmt.Errorf("%w: code exchange: %v", ErrUnavailable, err)
	}
	raw, _ := token.Extra("id_token").(string)
	if raw == "" {
		return User{}, "", fmt.Errorf("%w: no ID token from the identity provider", ErrFailed)
	}
	idToken, err := verifier.Verify(ctx, raw)
	if err != nil {
		return User{}, "", fmt.Errorf("%w: ID token: %v%s", ErrFailed, err, algorithmHint(err))
	}
	if !equal(idToken.Nonce, f.Nonce) {
		return User{}, "", fmt.Errorf("%w: ID token: nonce doesn't match", ErrFailed)
	}
	var claims idClaims
	if err := idToken.Claims(&claims); err != nil {
		return User{}, "", fmt.Errorf("%w: ID token claims: %v", ErrFailed, err)
	}
	// The token must have been issued to gantry: with several audiences
	// azp says so, and if azp is there, it must name gantry (OpenID
	// Connect Core 3.1.3.7).
	if (len(idToken.Audience) > 1 || claims.AuthorizedParty != "") && claims.AuthorizedParty != c.cfg.ClientID {
		return User{}, "", fmt.Errorf("%w: ID token issued to %q, not this client", ErrFailed, claims.AuthorizedParty)
	}

	u := claims.user(idToken.Subject)
	if !c.allowed(u, claims.Groups) {
		return u, "", fmt.Errorf("%w: %s", ErrDenied, u.Name)
	}
	return u, f.ReturnTo, nil
}

// algorithmHint explains a signature the verifier doesn't accept, which is
// usually a provider signing with the client secret (HS256).
func algorithmHint(err error) string {
	if strings.Contains(err.Error(), "unexpected signature algorithm") ||
		strings.Contains(err.Error(), "unsupported signing algorithm") {
		return " (the identity provider must sign ID tokens with a key pair, such as RS256 or ES256; HS256 isn't accepted)"
	}
	return ""
}

// allowed reports whether u, a member of groups, is on the allow list.
func (c *Client) allowed(u User, groups []string) bool {
	for _, g := range groups {
		if slices.Contains(c.cfg.AllowedGroups, g) {
			return true
		}
	}
	return u.Email != "" && slices.Contains(c.cfg.AllowedEmails, u.Email)
}

// idClaims are the ID token's claims gantry reads.
type idClaims struct {
	Email             string      `json:"email"`
	EmailVerified     flexBool    `json:"email_verified"`
	PreferredUsername string      `json:"preferred_username"`
	Groups            flexStrings `json:"groups"`
	AuthorizedParty   string      `json:"azp"`
}

func (c idClaims) user(subject string) User {
	u := User{Subject: subject}
	if c.EmailVerified {
		u.Email = strings.ToLower(c.Email)
	}
	switch {
	case u.Email != "":
		u.Name = u.Email
	case c.PreferredUsername != "":
		u.Name = c.PreferredUsername
	default:
		u.Name = subject
	}
	return u
}

// flexBool is a JSON boolean that some providers send as a string.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case bool:
		*b = flexBool(v)
	case string:
		*b = flexBool(v == "true")
	default:
		*b = false
	}
	return nil
}

// flexStrings is a list of strings that some providers send as one string.
type flexStrings []string

func (s *flexStrings) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*s = nil
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*s = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("groups: want a list of strings: %w", err)
	}
	*s = many
	return nil
}

// sign encodes f as a cookie value: the JSON in base64, a dot, and its
// HMAC-SHA256. The browser may read it (its own state, verifier and
// nonce), but can't change it.
func (c *Client) sign(f flow) string {
	data, _ := json.Marshal(f) // can't fail for this struct
	payload := base64.RawURLEncoding.EncodeToString(data)
	return payload + "." + base64.RawURLEncoding.EncodeToString(c.mac(payload))
}

// open checks a cookie value from sign and returns its flow, if it is
// genuine and not expired.
func (c *Client) open(cookie string) (flow, bool) {
	payload, sig, ok := strings.Cut(cookie, ".")
	if !ok {
		return flow{}, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, c.mac(payload)) {
		return flow{}, false
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return flow{}, false
	}
	var f flow
	if err := json.Unmarshal(data, &f); err != nil || c.now().Unix() >= f.Expires {
		return flow{}, false
	}
	return f, true
}

func (c *Client) mac(payload string) []byte {
	m := hmac.New(sha256.New, c.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// randomString returns 256 random bits, URL-safe.
func randomString() string {
	b := make([]byte, 32)
	rand.Read(b) // never fails (crypto/rand)
	return base64.RawURLEncoding.EncodeToString(b)
}

// equal compares two strings in constant time; empty matches nothing.
func equal(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// maxReturnTo is the longest path SafeReturn keeps; the flow cookie must
// stay well below 4 KiB.
const maxReturnTo = 1024

// SafeReturn returns p if it is a path of the web UI to return to after a
// login, and "/" otherwise. Only plain paths on this site count: nothing
// that a browser could read as another host ("//host", "/\host"), no
// control characters, nothing under /api/.
func SafeReturn(p string) string {
	if p == "" || len(p) > maxReturnTo || p[0] != '/' || strings.HasPrefix(p, "//") ||
		strings.ContainsAny(p, "\\") || strings.HasPrefix(p, "/api/") || p == "/api" {
		return "/"
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "/"
		}
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return p
}
