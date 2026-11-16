package api

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/live"
	"github.com/pixl-media/gantry/agent/internal/session"
	"github.com/pixl-media/gantry/agent/internal/tlscert"
	"github.com/pixl-media/gantry/agent/internal/tlscert/tlscerttest"
)

// The agent served over TLS, the way main sets it up: the limiting
// listener, ServeTLS and the reloading certificate (decision 27).
func TestTLS(t *testing.T) {
	certFile, keyFile, cert := tlscerttest.Write(t, t.TempDir(), 1, time.Now().AddDate(1, 0, 0))
	certs, err := tlscert.New(certFile, keyFile, nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen("127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ln.Addr().String(), New(Config{
		Token: "the-api-token", UIToken: testUIToken, Stacks: fakeStacks{},
		Live: live.New(nil, live.Options{}),
	}))
	srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certs.GetCertificate}
	srv.ErrorLog = log.New(io.Discard, "", 0) // the plain HTTP request below
	go srv.ServeTLS(ln, "", "")
	t.Cleanup(func() { srv.Close() })

	roots := x509.NewCertPool()
	roots.AddCert(cert)
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, ForceAttemptHTTP2: true},
	}
	addr := ln.Addr().String()
	do := func(method, path, body string, headers ...string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, "https://"+addr+path, strings.NewReader(body))
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	resp := do(http.MethodGet, "/healthz", "")
	if resp.StatusCode != http.StatusOK || resp.ProtoMajor != 2 {
		t.Errorf("healthz = %d over %s, want 200 over HTTP/2", resp.StatusCode, resp.Proto)
	}

	secure := func(resp *http.Response) bool {
		t.Helper()
		for _, c := range resp.Cookies() {
			if c.Name == session.CookieName {
				return c.Secure
			}
		}
		t.Fatalf("no session cookie (%d)", resp.StatusCode)
		return false
	}
	login := `{"token":"` + testUIToken + `"}`
	if r := do(http.MethodPost, "/api/v1/session", login, "Content-Type", "application/json"); !secure(r) {
		t.Error("the session cookie over TLS isn't Secure")
	}
	// A proxy in the LAN (here on loopback) talks TLS to the agent, but the
	// browser reached it over plain HTTP: a Secure cookie would never come
	// back.
	if r := do(http.MethodPost, "/api/v1/session", login, "Content-Type", "application/json", "X-Forwarded-Proto", "http"); secure(r) {
		t.Error("the session cookie for a browser on plain HTTP is Secure")
	}

	resp = do(http.MethodGet, "/api/v1/events", "", "Authorization", "Bearer the-api-token")
	ready := false
	for s := bufio.NewScanner(resp.Body); s.Scan(); {
		if s.Text() == "event: ready" {
			ready = true
			break
		}
	}
	if !ready {
		t.Error("the event stream over TLS sent no ready event")
	}

	plain, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	plain.Body.Close()
	if plain.StatusCode != http.StatusBadRequest {
		t.Errorf("plain HTTP to the TLS port = %d, want 400", plain.StatusCode)
	}
}
