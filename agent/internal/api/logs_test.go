package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/logs"
)

// fakeLogs emits fixed lines and records the options it was called with.
type fakeLogs struct {
	mu    sync.Mutex
	lines []logs.Line
	err   error
	opts  logs.Options
	stack string
}

func (f *fakeLogs) Stream(ctx context.Context, project string, opts logs.Options, emit func(logs.Line) error, flush func() error) error {
	f.mu.Lock()
	f.opts, f.stack = opts, project
	f.mu.Unlock()
	for _, l := range f.lines {
		if err := emit(l); err != nil {
			return err
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if f.err != nil {
		return f.err
	}
	if opts.Follow {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func newLogsServer(f *fakeLogs) http.Handler {
	return New(Config{Token: "t", Stacks: fakeStacks{unmanaged: []string{"ext"}}, Logs: f})
}

// sseEvents parses a Server-Sent Events body into (event, data) pairs.
func sseEvents(body string) [][2]string {
	var out [][2]string
	for block := range strings.SplitSeq(strings.TrimSpace(body), "\n\n") {
		var ev, data string
		for line := range strings.SplitSeq(block, "\n") {
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				ev = v
			}
			if v, ok := strings.CutPrefix(line, "data: "); ok {
				data = v
			}
		}
		out = append(out, [2]string{ev, data})
	}
	return out
}

func TestStackLogs(t *testing.T) {
	f := &fakeLogs{lines: []logs.Line{
		{Time: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC), Service: "web", Container: "app-web-1", Stream: "stdout", Text: "\x1b[32mready\x1b[0m"},
		{Time: time.Date(2026, 11, 12, 12, 0, 1, 0, time.UTC), Service: "db", Container: "app-db-1", Stream: "stderr", Text: "warning"},
	}}
	h := newLogsServer(f)

	rec := request(t, h, http.MethodGet, "/api/v1/stacks/app/logs?follow=false&tail=50&service=web&container=app-web-1")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, Content-Type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	events := sseEvents(rec.Body.String())
	if len(events) != 3 || events[0][0] != "log" || events[1][0] != "log" || events[2][0] != "end" {
		t.Fatalf("events = %v", events)
	}
	var first, second LogLine
	json.Unmarshal([]byte(events[0][1]), &first)
	json.Unmarshal([]byte(events[1][1]), &second)
	if first.Service != "web" || first.Line != "\x1b[32mready\x1b[0m" || first.Stream != LogLineStreamStdout {
		t.Errorf("first = %+v", first)
	}
	if second.Stream != LogLineStreamStderr || second.Container != "app-db-1" {
		t.Errorf("second = %+v", second)
	}
	want := logs.Options{Tail: 50, Follow: false, Service: "web", Container: "app-web-1"}
	if f.opts != want || f.stack != "app" {
		t.Errorf("options = %+v for %q, want %+v", f.opts, f.stack, want)
	}
}

func TestStackLogsDefaults(t *testing.T) {
	f := &fakeLogs{}
	srv := httptest.NewServer(newLogsServer(f))
	defer srv.Close()

	// Following by default: the stream stays open until the client leaves.
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/stacks/ext/logs", nil)
	req.Header.Set("Authorization", "Bearer t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unmanaged stack logs = %d, want 200 (read-only is allowed)", resp.StatusCode)
	}
	cancel()
	resp.Body.Close()

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opts.Tail != defaultLogTail || !f.opts.Follow {
		t.Errorf("default options = %+v", f.opts)
	}
}

func TestStackLogsError(t *testing.T) {
	h := newLogsServer(&fakeLogs{err: errors.New("docker unavailable")})
	rec := request(t, h, http.MethodGet, "/api/v1/stacks/app/logs?follow=false")
	events := sseEvents(rec.Body.String())
	if len(events) != 1 || events[0][0] != "failed" || !strings.Contains(events[0][1], "docker unavailable") {
		t.Errorf("events = %v", events)
	}
}

func TestStackLogsErrors(t *testing.T) {
	h := newLogsServer(&fakeLogs{})
	if rec := request(t, h, http.MethodGet, "/api/v1/stacks/nope/logs"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown stack = %d", rec.Code)
	}
	if rec := request(t, h, http.MethodGet, "/api/v1/stacks/Bad.Name/logs"); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid name = %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/stacks/app/logs", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("without token = %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if strings.Contains(string(body), "event:") {
		t.Error("unauthorized request got a stream")
	}
}
