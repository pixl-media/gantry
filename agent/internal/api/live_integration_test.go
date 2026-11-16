//go:build integration

package api_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/api"
	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/docker"
	"github.com/pixl-media/gantry/agent/internal/live"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

// A container stopped outside gantry shows up on the event stream.
func TestLiveEventsAgainstDocker(t *testing.T) {
	b := make([]byte, 4)
	rand.Read(b)
	name := "gantry-test-" + hex.EncodeToString(b) + "-live"
	stacksDir := tempDir(t)
	file := writeStack(t, filepath.Join(stacksDir, name), "app")
	up(t, name, file)

	dc, err := docker.New(os.Getenv("DOCKER_HOST"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := stacks.New(stacksDir, dc, compose.CLI{})
	if err != nil {
		t.Fatal(err)
	}
	h := api.New(api.Config{Token: token, Stacks: manager, Live: live.New(dc, live.Options{})})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	events := readEvents(resp.Body)

	// next returns the next event, or fails after a deadline.
	next := func(what string) sseEvent {
		t.Helper()
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("stream ended, want %s", what)
			}
			return ev
		case <-time.After(10 * time.Second):
			t.Fatalf("no event, want %s", what)
		}
		return sseEvent{}
	}
	if ev := next("ready"); ev.name != "ready" {
		t.Fatalf("first event = %+v", ev)
	}
	// The watcher connecting to Docker reports "all"; wait for it so that
	// it isn't mistaken for the change below.
	if ev := next("all"); ev.name != "changed" {
		t.Fatalf("second event = %+v", ev)
	}

	if out, err := exec.Command("docker", "stop", "-t", "1", name+"-app-1").CombinedOutput(); err != nil {
		t.Fatalf("docker stop: %v\n%s", err, out)
	}
	for {
		ev := next("a change of " + name)
		if ev.name != "changed" {
			continue
		}
		var c api.StackChanges
		if err := json.Unmarshal([]byte(ev.data), &c); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(c.Stacks, name) {
			return
		}
	}
}
