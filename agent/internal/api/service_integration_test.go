//go:build integration

package api_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/api"
	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/docker"
	"github.com/pixl-media/gantry/agent/internal/operations"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

// chainStack is web → app → db, where web also shares app's network
// namespace, plus an independent solo service.
const chainStack = `services:
  db:
    image: alpine:3.24
    command: ["sleep", "infinity"]
    init: true
    stop_grace_period: 1s
  app:
    image: alpine:3.24
    command: ["sleep", "infinity"]
    init: true
    stop_grace_period: 1s
    depends_on: [db]
  web:
    image: alpine:3.24
    command: ["sleep", "infinity"]
    init: true
    stop_grace_period: 1s
    depends_on: [app]
    network_mode: service:app
  solo:
    image: alpine:3.24
    command: ["sleep", "infinity"]
    init: true
    stop_grace_period: 1s
`

// runService runs a service action, confirming the plan if asked for one,
// and waits until the operation has finished. It returns the confirmed plan's
// services (only the service itself if there was nothing to confirm).
func runService(t *testing.T, h http.Handler, stack, service string, action api.ServiceAction) []string {
	t.Helper()
	path := "/api/v1/stacks/" + stack + "/services/" + service + "/actions/" + string(action)
	code, body := post(t, h, path)
	affected := []string{service}
	if code == http.StatusConflict {
		var e api.Error
		json.Unmarshal(body, &e)
		if e.Code == nil || *e.Code != api.ErrorCodeConfirmationRequired || e.Plan == nil {
			t.Fatalf("%s %s: %s", action, service, body)
		}
		affected = nil
		for _, a := range e.Plan.Affected {
			affected = append(affected, a.Service)
		}
		confirm, _ := json.Marshal(api.ServiceActionRequest{Confirm: &affected})
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(confirm)))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		code, body = rec.Code, rec.Body.Bytes()
	}
	if code != http.StatusAccepted {
		t.Fatalf("%s %s: status %d: %s", action, service, code, body)
	}
	var op api.Operation
	json.Unmarshal(body, &op)
	deadline := time.Now().Add(3 * time.Minute)
	for {
		var d api.OperationDetail
		get(t, h, "/api/v1/operations/"+op.Id, &d)
		if d.Status != api.OperationStatusRunning {
			if d.Status != api.OperationStatusSucceeded {
				t.Fatalf("%s %s: %s: %v\n%s", action, service, d.Status, d.Error, d.Output)
			}
			return affected
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s %s: still running after 3 minutes", action, service)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// states returns each service's container state.
func states(t *testing.T, h http.Handler, stack string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, s := range stackStatus(t, h, stack).Services {
		if len(s.Containers) > 0 {
			out[s.Name] = s.Containers[0].State
		} else {
			out[s.Name] = "none"
		}
	}
	return out
}

// startedAt returns when a service's container last started.
func startedAt(t *testing.T, project, service string) string {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.StartedAt}}", project+"-"+service+"-1").Output()
	if err != nil {
		t.Fatalf("inspect %s: %v", service, err)
	}
	return strings.TrimSpace(string(out))
}

func TestServiceActionsAgainstDocker(t *testing.T) {
	b := make([]byte, 4)
	rand.Read(b)
	name := "gantry-test-" + hex.EncodeToString(b) + "-services"

	stacksDir := tempDir(t)
	dir := filepath.Join(stacksDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(file, []byte(chainStack), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-p", name, "-f", file, "down", "-v", "--remove-orphans", "-t", "1").Run()
	})

	dc, err := docker.New(os.Getenv("DOCKER_HOST"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := stacks.New(stacksDir, dc, compose.CLI{})
	if err != nil {
		t.Fatal(err)
	}
	ops := operations.NewManager(context.Background(), compose.CLI{}, operations.Options{})
	h := api.New(api.Config{Token: token, Stacks: manager, Operations: ops})

	// Starting web starts what it needs, and nothing else.
	if got := runService(t, h, name, "web", api.ServiceActionStart); !slices.Equal(got, []string{"web", "app", "db"}) {
		t.Errorf("start web affected %v", got)
	}
	want := map[string]string{"db": "running", "app": "running", "web": "running", "solo": "none"}
	if got := states(t, h, name); !maps.Equal(got, want) {
		t.Fatalf("after start web: %v", got)
	}

	// A service without dependencies runs without confirmation.
	runService(t, h, name, "solo", api.ServiceActionStart)

	// Restarting db restarts the whole chain, but not solo.
	before := map[string]string{}
	for _, s := range []string{"db", "app", "web", "solo"} {
		before[s] = startedAt(t, name, s)
	}
	if got := runService(t, h, name, "db", api.ServiceActionRestart); !slices.Equal(got, []string{"db", "app", "web"}) {
		t.Errorf("restart db affected %v", got)
	}
	for _, s := range []string{"db", "app", "web"} {
		if startedAt(t, name, s) == before[s] {
			t.Errorf("%s wasn't restarted", s)
		}
	}
	if startedAt(t, name, "solo") != before["solo"] {
		t.Error("solo was restarted")
	}

	// Stopping app stops web, which needs it, and keeps db running.
	runService(t, h, name, "app", api.ServiceActionStop)
	want = map[string]string{"db": "running", "app": "exited", "web": "exited", "solo": "running"}
	if got := states(t, h, name); !maps.Equal(got, want) {
		t.Errorf("after stop app: %v", got)
	}
	runService(t, h, name, "web", api.ServiceActionStart)

	// Updating app recreates it and brings web back up, even though web
	// shares app's network namespace.
	if got := runService(t, h, name, "app", api.ServiceActionUpdate); !slices.Equal(got, []string{"app", "web"}) {
		t.Errorf("update app affected %v", got)
	}
	want = map[string]string{"db": "running", "app": "running", "web": "running", "solo": "running"}
	if got := states(t, h, name); !maps.Equal(got, want) {
		t.Errorf("after update app: %v", got)
	}
}
