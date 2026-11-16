package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBodyLimits(t *testing.T) {
	h := New(Config{Token: "secret-token", Stacks: fakeStacks{}})
	tests := []struct {
		name, method, path, body string
		bearer                   bool
		wantStatus               int
		wantCode                 ErrorCode
	}{
		{
			name: "oversized login", method: http.MethodPost, path: "/api/v1/session",
			body:       `{"token":"` + strings.Repeat("A", maxLoginBody) + `"}`,
			wantStatus: http.StatusRequestEntityTooLarge, wantCode: ErrorCodeRequestTooLarge,
		},
		{
			name: "normal login is still checked", method: http.MethodPost, path: "/api/v1/session",
			body:       `{"token":"wrong"}`,
			wantStatus: http.StatusUnauthorized, wantCode: ErrorCodeInvalidToken,
		},
		{
			name: "oversized compose file", method: http.MethodPut, path: "/api/v1/stacks/app/compose", bearer: true,
			body:       `{"version":"v1","content":"` + strings.Repeat("a", maxBody) + `"}`,
			wantStatus: http.StatusRequestEntityTooLarge, wantCode: ErrorCodeRequestTooLarge,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "http://example.com")
			if tt.bearer {
				req.Header.Set("Authorization", "Bearer secret-token")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			var e Error
			if err := json.NewDecoder(rec.Body).Decode(&e); err != nil {
				t.Fatal(err)
			}
			if e.Code == nil || *e.Code != tt.wantCode {
				t.Errorf("code = %v, want %s (%s)", e.Code, tt.wantCode, e.Error)
			}
		})
	}
}

func TestNewServerLimits(t *testing.T) {
	srv := NewServer(":0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout == 0 || srv.ReadTimeout == 0 || srv.IdleTimeout == 0 {
		t.Errorf("timeouts not set: %+v", srv)
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, would cut event streams", srv.WriteTimeout)
	}
	if srv.MaxHeaderBytes == 0 || srv.MaxHeaderBytes > 1<<20 {
		t.Errorf("MaxHeaderBytes = %d", srv.MaxHeaderBytes)
	}
}

// Event streams run far longer than the server's ReadTimeout. They must keep
// running past it (net/http lifts the read deadline once the request has
// been read; this guards against that changing, or the timeout moving).
func TestEventStreamOutlivesReadTimeout(t *testing.T) {
	const readTimeout = 50 * time.Millisecond
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc, err := startEventStream(w)
		if err != nil {
			t.Error(err)
			return
		}
		state := "alive"
		select {
		case <-r.Context().Done():
			state = "canceled"
		case <-time.After(10 * readTimeout):
		}
		fmt.Fprintf(w, "data: %s\n\n", state)
		_ = rc.Flush()
	})
	srv := httptest.NewUnstartedServer(handler)
	srv.Config = NewServer("", handler)
	srv.Config.ReadTimeout = readTimeout
	srv.Start()
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store (streams may hold secrets)", cc)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "data: alive") {
		t.Errorf("stream = %q, want it alive past the read timeout", body)
	}
}

// Connections beyond the per-host limit are closed at once; closing one
// frees its slot.
func TestConnectionLimits(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := newLimitListener(inner, nil, 10, 2)
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(io.Discard, c); c.Close() }()
		}
	}()
	dial := func() net.Conn {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	// closedWithin reports whether the server closes c within d. Waiting
	// long for a close that is expected keeps the test free of timing
	// assumptions; a kept connection simply never sees EOF.
	closedWithin := func(c net.Conn, d time.Duration) bool {
		c.SetReadDeadline(time.Now().Add(d))
		_, err := c.Read(make([]byte, 1))
		return err == io.EOF
	}
	a, b := dial(), dial()
	if closedWithin(a, 200*time.Millisecond) || closedWithin(b, 200*time.Millisecond) {
		t.Fatal("connections within the limit were closed")
	}
	if c := dial(); !closedWithin(c, 5*time.Second) {
		t.Error("the third connection from one host was kept")
	}
	a.Close()
	waitFor(t, func() bool {
		ln.mu.Lock()
		defer ln.mu.Unlock()
		return ln.open == 1
	})
	if d := dial(); closedWithin(d, 200*time.Millisecond) {
		t.Error("a freed slot wasn't reused")
	}
	b.Close()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
