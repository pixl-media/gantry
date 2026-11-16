package api

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/live"
)

// readSSE sends the lines of an event stream (events and comments) to the
// returned channel; an event is "event|data", a comment ":text".
func readSSE(r io.Reader) <-chan string {
	ch := make(chan string, 64)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(r)
		var ev, data string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, ":"):
				ch <- line
			case strings.HasPrefix(line, "event: "):
				ev = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && ev != "":
				ch <- ev + "|" + data
				ev, data = "", ""
			}
		}
	}()
	return ch
}

func expect(t *testing.T, ch <-chan string, want func(string) bool, what string) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got, ok := <-ch:
			if !ok {
				t.Fatalf("stream ended, want %s", what)
			}
			if want(got) {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func openEvents(t *testing.T, srv *httptest.Server, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/events", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	} else {
		req.Header.Set("Authorization", "Bearer t")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return resp
}

func TestStreamEvents(t *testing.T) {
	hub := live.New(nil, live.Options{})
	h := New(Config{Token: "t", Stacks: fakeStacks{}, Live: hub, EventsPing: 50 * time.Millisecond})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close) // after the stream's body is closed (cleanups run in reverse)

	events := readSSE(openEvents(t, srv, nil).Body)
	expect(t, events, func(s string) bool { return s == "ready|{}" }, "ready")

	// A burst of changes arrives as one event.
	hub.Notify("web")
	hub.Notify("db")
	hub.Notify("web")
	got := expect(t, events, func(s string) bool { return strings.HasPrefix(s, "changed|") }, "changed")
	var c StackChanges
	if err := json.Unmarshal([]byte(strings.TrimPrefix(got, "changed|")), &c); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Stacks, []string{"db", "web"}) || c.All {
		t.Errorf("changes = %+v", c)
	}

	// Idle streams get keepalive comments.
	expect(t, events, func(s string) bool { return s == ": ping" }, "a ping")
}

func TestStreamEventsWithoutHub(t *testing.T) {
	h := New(Config{Token: "t", Stacks: fakeStacks{}, EventsPing: 20 * time.Millisecond})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close) // after the stream's body is closed (cleanups run in reverse)
	events := readSSE(openEvents(t, srv, nil).Body)
	expect(t, events, func(s string) bool { return s == "ready|{}" }, "ready")
	expect(t, events, func(s string) bool { return s == ": ping" }, "a ping")
}

func TestStreamEventsNeedsAuth(t *testing.T) {
	h := New(Config{Token: "t", Stacks: fakeStacks{}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(http.MethodGet, "/api/v1/events", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("without credentials = %d", rec.Code)
	}
}

func TestEventStreamEndsWithSession(t *testing.T) {
	hub := live.New(nil, live.Options{})
	h := New(Config{Token: "t", Stacks: fakeStacks{}, Live: hub})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close) // after the stream's body is closed (cleanups run in reverse)
	c := sessionCookieFrom(t, h)

	events := readSSE(openEvents(t, srv, c).Body)
	expect(t, events, func(s string) bool { return s == "ready|{}" }, "ready")

	logout, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/session", nil)
	logout.AddCookie(c)
	if lr, err := http.DefaultClient.Do(logout); err != nil || lr.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %v %v", lr, err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the stream outlived the logout")
		}
	}
}

// recordingLive records Notify calls.
type recordingLive struct {
	*live.Hub
	mu       sync.Mutex
	notified []string
}

func (r *recordingLive) Notify(stack string) {
	r.mu.Lock()
	r.notified = append(r.notified, stack)
	r.mu.Unlock()
	r.Hub.Notify(stack)
}

func (r *recordingLive) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.notified)
}

var _ LiveService = (*recordingLive)(nil)
