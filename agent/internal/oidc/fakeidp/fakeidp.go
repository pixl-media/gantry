// Package fakeidp is an OpenID Connect provider for tests: discovery, keys,
// an authorization endpoint that logs the user in at once, and a token
// endpoint that checks the client, the code and PKCE. Only tests import it.
package fakeidp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// Provider is a running fake identity provider over HTTPS.
type Provider struct {
	*httptest.Server
	ClientID     string
	ClientSecret string

	key *rsa.PrivateKey

	mu sync.Mutex
	// Claims go into every ID token, next to iss, sub, aud, exp, iat and
	// nonce.
	Claims map[string]any
	// Tweak, if set, changes an ID token's claims before signing, e.g. its
	// audience or expiry.
	Tweak func(claims map[string]any)
	// Alg is the signature algorithm: RS256 (default) or HS256, with the
	// client secret as key.
	Alg string
	// Down makes every endpoint answer 503.
	Down bool
	// Exchanges counts calls of the token endpoint.
	Exchanges int

	codes map[string]grant
}

type grant struct {
	nonce, challenge, redirect string
}

// New starts a provider; it stops when the test ends.
func New(t testing.TB) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{
		ClientID:     "gantry",
		ClientSecret: "client-secret-for-tests-only-0123456789abcdef",
		key:          key,
		Claims:       map[string]any{},
		codes:        map[string]grant{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /keys", p.keys)
	mux.HandleFunc("GET /auth", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	p.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		down := p.Down
		p.mu.Unlock()
		if down {
			http.Error(w, "down for tests", http.StatusServiceUnavailable)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(p.Close)
	return p
}

// Set changes the provider's settings under its lock, for tests that run
// logins in parallel.
func (p *Provider) Set(f func(p *Provider)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(p)
}

// Issuer is the provider's issuer URL.
func (p *Provider) Issuer() string { return p.URL }

// Login plays the browser at the provider: it opens authURL (from the
// client's Start) and returns the query the provider sends the browser back
// with, code and state.
func (p *Provider) Login(t testing.TB, authURL string) url.Values {
	t.Helper()
	c := p.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize: status %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return loc.Query()
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	alg := p.alg()
	p.mu.Unlock()
	writeJSON(w, map[string]any{
		"issuer":                                p.URL,
		"authorization_endpoint":                p.URL + "/auth",
		"token_endpoint":                        p.URL + "/token",
		"jwks_uri":                              p.URL + "/keys",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{alg},
	})
}

func (p *Provider) keys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: &p.key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig",
	}}})
}

// authorize logs the user in at once and sends the browser back.
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != p.ClientID || q.Get("response_type") != "code" ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	code := rand.Text()
	p.mu.Lock()
	p.codes[code] = grant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
	p.mu.Unlock()
	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	v := back.Query()
	v.Set("code", code)
	v.Set("state", q.Get("state"))
	back.RawQuery = v.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

// token exchanges a code for tokens, checking the client, the redirect URI
// and PKCE.
func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != p.ClientID || secret != p.ClientSecret {
		tokenError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Exchanges++
	code := r.PostForm.Get("code")
	g, ok := p.codes[code]
	delete(p.codes, code) // codes work once
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || r.PostForm.Get("grant_type") != "authorization_code" ||
		r.PostForm.Get("redirect_uri") != g.redirect ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}

	now := time.Now()
	claims := map[string]any{
		"iss": p.URL, "sub": "user-1", "aud": p.ClientID,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "nonce": g.nonce,
	}
	maps.Copy(claims, p.Claims)
	if p.Tweak != nil {
		p.Tweak(claims)
	}
	idToken, err := p.sign(claims)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"access_token": rand.Text(), "token_type": "Bearer", "expires_in": 300, "id_token": idToken,
	})
}

func (p *Provider) alg() string {
	if p.Alg == "" {
		return "RS256"
	}
	return p.Alg
}

func (p *Provider) sign(claims map[string]any) (string, error) {
	key := jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: p.key, KeyID: "test-key"}}
	if p.alg() == "HS256" {
		key = jose.SigningKey{Algorithm: jose.HS256, Key: []byte(p.ClientSecret)}
	}
	signer, err := jose.NewSigner(key, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		return "", err
	}
	return jws.CompactSerialize()
}

func tokenError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
