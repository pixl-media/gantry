package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"
)

func TestEvents(t *testing.T) {
	release := make(chan struct{})
	c := serveUnix(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var filters map[string][]string
		if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters); err != nil {
			t.Errorf("filters: %v", err)
		}
		if !slices.Equal(filters["type"], []string{"container"}) || !slices.Equal(filters["label"], []string{LabelProject}) {
			t.Errorf("filters = %v", filters)
		}
		w.(http.Flusher).Flush()
		for _, line := range []string{
			`{"Type":"container","Action":"start","Actor":{"Attributes":{"com.docker.compose.project":"web","com.docker.compose.service":"app"}}}`,
			`{"Type":"container","Action":"exec_start: sh","Actor":{"Attributes":{"com.docker.compose.project":"web"}}}`,
			`{"Type":"container","Action":"health_status: healthy","Actor":{"Attributes":{"com.docker.compose.project":"db"}}}`,
			`{"Type":"container","Action":"die","Actor":{"Attributes":{}}}`,
			`{"Type":"network","Action":"connect","Actor":{"Attributes":{"com.docker.compose.project":"web"}}}`,
		} {
			w.Write([]byte(line + "\n"))
			w.(http.Flusher).Flush()
		}
		<-release // keep the stream open like Docker does
	})

	ctx, cancel := context.WithCancel(context.Background())
	connected := false
	got := make(chan Event, 10)
	done := make(chan error, 1)
	go func() {
		done <- c.Events(ctx, func() { connected = true }, func(e Event) { got <- e })
	}()

	want := []Event{
		{Action: "start", Project: "web", Service: "app"},
		{Action: "health_status: healthy", Project: "db"},
	}
	for _, w := range want {
		select {
		case e := <-got:
			if e != w {
				t.Errorf("event = %+v, want %+v", e, w)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no event, want %+v", w)
		}
	}
	cancel()
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if !connected {
		t.Error("connected wasn't called")
	}
	select {
	case e := <-got:
		t.Errorf("unexpected event %+v", e)
	default:
	}
}

func TestEventsStreamEnds(t *testing.T) {
	c := serveUnix(t, func(w http.ResponseWriter, r *http.Request) {})
	err := c.Events(context.Background(), func() {}, func(Event) {})
	if err == nil || errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want stream ended", err)
	}
}

func TestEventsRefused(t *testing.T) {
	c := serveUnix(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"nope"}`, http.StatusInternalServerError)
	})
	called := false
	if err := c.Events(context.Background(), func() { called = true }, func(Event) {}); err == nil || called {
		t.Errorf("err = %v, connected = %v", err, called)
	}
}
