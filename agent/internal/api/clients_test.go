package api

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies("10.0.0.0/8, 192.168.1.5 fd00::/8,::ffff:172.16.0.1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "192.168.1.5/32", "fd00::/8", "172.16.0.1/32"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i, p := range got {
		if p.String() != want[i] {
			t.Errorf("prefix %d = %s, want %s", i, p, want[i])
		}
	}
	if p, err := ParseTrustedProxies(""); err != nil || p != nil {
		t.Errorf("empty = %v, %v", p, err)
	}
	if _, err := ParseTrustedProxies("gantry-web"); err == nil {
		t.Error("a host name was accepted")
	}
}

func req(remote string, headers ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stacks", nil)
	r.RemoteAddr = remote
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Add(headers[i], headers[i+1])
	}
	return r
}

func TestClientAddress(t *testing.T) {
	trusted, _ := ParseTrustedProxies("10.0.0.0/8")
	n := network{trusted: trusted}
	tests := []struct {
		name string
		r    *http.Request
		want string
	}{
		{"direct", req("203.0.113.9:5000"), "203.0.113.9"},
		{"untrusted peer can't claim another address", req("203.0.113.9:5000", "X-Forwarded-For", "10.1.1.1"), "203.0.113.9"},
		{"trusted proxy", req("10.0.0.2:5000", "X-Forwarded-For", "198.51.100.4"), "198.51.100.4"},
		{"forged entries left of the client are ignored", req("10.0.0.2:5000", "X-Forwarded-For", "1.1.1.1, 198.51.100.4"), "198.51.100.4"},
		{"chain of trusted proxies", req("10.0.0.2:5000", "X-Forwarded-For", "198.51.100.4, 10.0.0.3"), "198.51.100.4"},
		{"trusted proxy without header", req("10.0.0.2:5000"), "10.0.0.2"},
		{"garbage in the header", req("10.0.0.2:5000", "X-Forwarded-For", "not-an-ip"), "10.0.0.2"},
		{"IPv4-mapped", req("[::ffff:203.0.113.9]:5000"), "203.0.113.9"},
	}
	for _, tt := range tests {
		if got := n.client(tt.r).String(); got != tt.want {
			t.Errorf("%s: client = %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestLimitKeyGroupsIPv6Networks(t *testing.T) {
	a := limitKey(netip.MustParseAddr("2001:db8:1:2::1"))
	b := limitKey(netip.MustParseAddr("2001:db8:1:2:ffff::9"))
	c := limitKey(netip.MustParseAddr("2001:db8:1:3::1"))
	if a != b || a == c {
		t.Errorf("keys: %s, %s, %s; want the same /64 together, another apart", a, b, c)
	}
	if k := limitKey(netip.MustParseAddr("203.0.113.9")); k != "203.0.113.9" {
		t.Errorf("IPv4 key = %s", k)
	}
}

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"203.0.113.9": true, "8.8.8.8": true, "2001:db8::1": true,
		"127.0.0.1": false, "::1": false, "10.1.2.3": false, "172.20.0.1": false, "192.168.0.10": false,
		"fd12::1": false, "fe80::1": false, "169.254.1.1": false, "100.101.102.103": false,
	} {
		if got := public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("public(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestInsecure(t *testing.T) {
	trusted, _ := ParseTrustedProxies("10.0.0.2")
	n := network{trusted: trusted}
	withTLS := req("203.0.113.9:5000")
	withTLS.TLS = &tls.ConnectionState{}
	tests := []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"plain HTTP from the internet", req("203.0.113.9:5000"), true},
		{"plain HTTP from the LAN", req("192.168.1.20:5000"), false},
		{"plain HTTP from a VPN", req("100.64.1.2:5000"), false},
		{"TLS", withTLS, false},
		{"forwarded HTTPS claimed by an internet client", req("203.0.113.9:5000", "X-Forwarded-Proto", "https"), true},
		{"HTTPS terminated by a proxy in the LAN", req("192.168.1.1:5000", "X-Forwarded-Proto", "https"), false},
		{"internet client behind a trusted plain-HTTP proxy", req("10.0.0.2:5000", "X-Forwarded-For", "203.0.113.9", "X-Forwarded-Proto", "http"), true},
		{"internet client behind a trusted HTTPS proxy", req("10.0.0.2:5000", "X-Forwarded-For", "203.0.113.9", "X-Forwarded-Proto", "https"), false},
	}
	for _, tt := range tests {
		if got := n.insecure(tt.r); got != tt.want {
			t.Errorf("%s: insecure = %v, want %v", tt.name, got, tt.want)
		}
	}
	n.allowInsecureHTTP = true
	if n.insecure(req("203.0.113.9:5000")) {
		t.Error("GANTRY_ALLOW_INSECURE_HTTP doesn't allow it")
	}
}

// Credentials over plain HTTP from the internet are refused before they are
// checked: no answer tells whether they were right, and nothing is counted.
func TestPlainHTTPFromTheInternet(t *testing.T) {
	h := New(Config{Token: "the-token", Stacks: fakeStacks{}})
	for range maxFailedAttempts + 2 {
		r := req("203.0.113.9:5000", "Authorization", "Bearer the-token")
		rec := serve(h, r)
		if e := decode[Error](t, rec); rec.Code != http.StatusForbidden || *e.Code != ErrorCodeHttpsRequired {
			t.Fatalf("bearer over plain HTTP = %d %+v", rec.Code, e)
		}
	}
	login := httptest.NewRequest(http.MethodPost, "/api/v1/session", strings.NewReader(`{"token":"the-token"}`))
	login.RemoteAddr = "203.0.113.9:5000"
	login.Header.Set("Content-Type", "application/json")
	if rec := serve(h, login); rec.Code != http.StatusForbidden {
		t.Errorf("login over plain HTTP = %d, want 403", rec.Code)
	}
	// The guard runs before authentication, for any protected route...
	if rec := serve(h, req("203.0.113.9:5000")); rec.Code != http.StatusForbidden {
		t.Errorf("GET without credentials = %d, want 403", rec.Code)
	}
	// ...but the health check needs no credentials and stays reachable.
	health := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	health.RemoteAddr = "203.0.113.9:5000"
	if rec := serve(h, health); rec.Code != http.StatusOK {
		t.Errorf("healthz = %d", rec.Code)
	}

	allowed := New(Config{Token: "the-token", Stacks: fakeStacks{}, AllowInsecureHTTP: true})
	if rec := serve(allowed, req("203.0.113.9:5000", "Authorization", "Bearer the-token")); rec.Code != http.StatusOK {
		t.Errorf("with GANTRY_ALLOW_INSECURE_HTTP = %d, want 200", rec.Code)
	}
}

// Behind a trusted proxy, every client has its own limit.
func TestLimitPerClientBehindTrustedProxy(t *testing.T) {
	trusted, _ := ParseTrustedProxies("10.0.0.2")
	h := New(Config{Token: "the-token", Stacks: fakeStacks{}, TrustedProxies: trusted})
	via := func(client, token string) int {
		return serve(h, req("10.0.0.2:5000", "X-Forwarded-For", client, "Authorization", "Bearer "+token)).Code
	}
	for range maxFailedAttempts {
		via("192.168.1.66", "wrong")
	}
	if code := via("192.168.1.66", "the-token"); code != http.StatusTooManyRequests {
		t.Errorf("blocked client = %d, want 429", code)
	}
	if code := via("192.168.1.20", "the-token"); code != http.StatusOK {
		t.Errorf("another client behind the same proxy = %d, want 200", code)
	}
}

// Addresses of one IPv6 network share the limit.
func TestIPv6NetworkSharesLimit(t *testing.T) {
	h := New(Config{Token: "the-token", Stacks: fakeStacks{}})
	for i := range maxFailedAttempts {
		serve(h, req(fmt.Sprintf("[fd12:3456:789a:1::%x]:5000", i+1), "Authorization", "Bearer wrong"))
	}
	if rec := serve(h, req("[fd12:3456:789a:1::ffff]:5000", "Authorization", "Bearer the-token")); rec.Code != http.StatusTooManyRequests {
		t.Errorf("new address in the same /64 = %d, want 429", rec.Code)
	}
}

// Other Authorization schemes, such as Basic auth from a proxy in front,
// aren't token attempts: the cookie still works and nothing is counted.
func TestOtherAuthorizationSchemes(t *testing.T) {
	h, _ := newOpsServer(t, runner{})
	c := sessionCookieFrom(t, h)
	for range maxFailedAttempts + 2 {
		r := withCookie(http.MethodGet, "/api/v1/stacks", c, "")
		r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		if rec := serve(h, r); rec.Code != http.StatusOK {
			t.Fatalf("cookie with a Basic header = %d, want 200", rec.Code)
		}
	}
	// The scheme name is case-insensitive.
	r := newRequest(http.MethodGet, "/api/v1/stacks", nil)
	r.Header.Set("Authorization", "bearer t")
	if rec := serve(h, r); rec.Code != http.StatusOK {
		t.Errorf("lowercase bearer = %d", rec.Code)
	}
}

func TestOriginSchemeAndHSTS(t *testing.T) {
	h, _ := newOpsServer(t, runner{})
	c := sessionCookieFrom(t, h)
	// Over HTTPS (here: a proxy in the LAN says so), an http Origin of the
	// same host is another origin.
	for origin, want := range map[string]int{"http://example.com": http.StatusForbidden, "https://example.com": http.StatusBadRequest} {
		r := withCookie(http.MethodPost, "/api/v1/stacks/app/actions/explode", c, origin)
		r.Header.Set("X-Forwarded-Proto", "https")
		if rec := serve(h, r); rec.Code != want {
			t.Errorf("Origin %s over HTTPS = %d, want %d", origin, rec.Code, want)
		}
	}

	secure := New(Config{Token: "the-token", Stacks: fakeStacks{}, PublicOrigin: "https://gantry.example.com"})
	r := newRequest(http.MethodGet, "/api/v1/version", nil)
	r.Header.Set("Authorization", "Bearer the-token")
	if hsts := serve(secure, r).Header().Get("Strict-Transport-Security"); !strings.HasPrefix(hsts, "max-age=") {
		t.Errorf("Strict-Transport-Security = %q", hsts)
	}
	if hsts := serve(h, newRequest(http.MethodGet, "/healthz", nil)).Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("HSTS without an https public URL: %q", hsts)
	}
}

func TestFailureLogIsBounded(t *testing.T) {
	var logs bytes.Buffer
	f := newFailureLog(slog.New(slog.NewTextHandler(&logs, nil)))
	for i := range maxFailureLines + 5 {
		f.record(string(rune('a'+i)), "token")
	}
	if n := strings.Count(logs.String(), `msg="failed authentication"`); n != maxFailureLines {
		t.Errorf("%d lines logged one by one, want %d", n, maxFailureLines)
	}
	f.flush()
	if !strings.Contains(logs.String(), "attempts=5 clients=5") {
		t.Errorf("summary missing:\n%s", logs.String())
	}
}
