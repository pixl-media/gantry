//go:build integration

package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/api"
	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/docker"
	"github.com/pixl-media/gantry/agent/internal/operations"
	"github.com/pixl-media/gantry/agent/internal/stackfiles"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

func sendJSON(t *testing.T, h http.Handler, method, path string, body any) (int, []byte) {
	t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.RemoteAddr = "127.0.0.1:1234" // plain HTTP with credentials is refused from the internet
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// waitOperation polls an operation until it has finished.
func waitOperation(t *testing.T, h http.Handler, id string) api.OperationDetail {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for {
		var d api.OperationDetail
		get(t, h, "/api/v1/operations/"+id, &d)
		if d.Status != api.OperationStatusRunning {
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation %s still running", id)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func printenv(t *testing.T, container, key string) string {
	t.Helper()
	out, err := exec.Command("docker", "exec", container, "printenv", key).Output()
	if err != nil {
		t.Fatalf("printenv %s in %s: %v", key, container, err)
	}
	return strings.TrimSuffix(string(out), "\n")
}

func volumeExists(name string) bool {
	return exec.Command("docker", "volume", "inspect", name).Run() == nil
}

func TestEditAgainstDocker(t *testing.T) {
	b := make([]byte, 4)
	rand.Read(b)
	name := "gantry-test-" + hex.EncodeToString(b) + "-edit"
	volume := name + "_data"
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-p", name, "down", "-v", "--remove-orphans", "-t", "1").Run()
		exec.Command("docker", "volume", "rm", "-f", volume).Run()
	})

	stacksDir := tempDir(t)
	dc, err := docker.New(os.Getenv("DOCKER_HOST"))
	if err != nil {
		t.Fatal(err)
	}
	manager, _ := stacks.New(stacksDir, dc, compose.CLI{})
	ops := operations.NewManager(context.Background(), compose.CLI{}, operations.Options{})
	h := api.New(api.Config{Token: token, Stacks: manager, Operations: ops, Files: stackfiles.New(compose.CLI{})})

	composeV1 := `services:
  app:
    image: alpine:3.24
    command: ["sleep", "infinity"]
    init: true
    stop_grace_period: 1s
    environment:
      GREETING: ${GREETING}
      TRICKY: ${TRICKY:-unset}
    volumes:
      - data:/data
volumes:
  data: {}
`
	// Create with a variable, then start.
	code, body := sendJSON(t, h, http.MethodPost, "/api/v1/stacks", api.StackCreate{
		Name: name, Compose: composeV1, Env: &map[string]string{"GREETING": "hello world"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	runAction(t, h, name, api.StackActionStart)
	container := name + "-app-1"
	if got := printenv(t, container, "GREETING"); got != "hello world" {
		t.Errorf("GREETING in the container = %q", got)
	}

	// Tricky values make it through the .env file into the container
	// unchanged, read by the real docker compose.
	tricky := map[string]string{
		"GREETING": `it's $5 \ "quoted" #not-a-comment ümlaut ✓`,
		"TRICKY":   `pa$$word ${NOT_EXPANDED} 'single' #x`,
	}
	var envFile api.EnvFile
	get(t, h, "/api/v1/stacks/"+name+"/env", &envFile)
	code, body = sendJSON(t, h, http.MethodPatch, "/api/v1/stacks/"+name+"/env", api.EnvUpdate{Version: envFile.Version, Set: &tricky})
	if code != http.StatusOK {
		t.Fatalf("env update: %d %s", code, body)
	}
	runAction(t, h, name, api.StackActionStart) // recreates the container with the new values
	for key, want := range tricky {
		if got := printenv(t, container, key); got != want {
			t.Errorf("%s in the container = %q, want %q", key, got, want)
		}
		code, body := sendJSON(t, h, http.MethodPost, "/api/v1/stacks/"+name+"/env/reveal", api.EnvRevealRequest{Key: key})
		var v api.EnvValue
		json.Unmarshal(body, &v)
		if code != http.StatusOK || v.Value != want {
			t.Errorf("reveal %s = %d %q, want %q", key, code, v.Value, want)
		}
	}

	// An invalid Compose file is rejected and the file stays as it was.
	detail := stackStatus(t, h, name)
	version := *detail.ComposeFile.Version
	code, body = sendJSON(t, h, http.MethodPut, "/api/v1/stacks/"+name+"/compose", api.ComposeUpdate{
		Content: "services:\n  app:\n    ports: not-a-list\n", Version: version,
	})
	var e api.Error
	json.Unmarshal(body, &e)
	if code != http.StatusBadRequest || e.Code == nil || *e.Code != api.ErrorCodeInvalidCompose || !strings.Contains(e.Error, "compose.yaml") {
		t.Errorf("invalid compose: %d %s", code, body)
	}
	if got, _ := os.ReadFile(filepath.Join(stacksDir, name, "compose.yaml")); string(got) != composeV1 {
		t.Error("a rejected save changed the file")
	}

	// A valid change, applied with start, reaches the container.
	composeV2 := strings.Replace(composeV1, "GREETING: ${GREETING}", "RENAMED: ${GREETING}", 1)
	code, body = sendJSON(t, h, http.MethodPut, "/api/v1/stacks/"+name+"/compose", api.ComposeUpdate{Content: composeV2, Version: version})
	if code != http.StatusOK {
		t.Fatalf("save: %d %s", code, body)
	}
	runAction(t, h, name, api.StackActionStart)
	if got := printenv(t, container, "RENAMED"); got != tricky["GREETING"] {
		t.Errorf("RENAMED = %q", got)
	}

	// Delete without volumes: the directory is gone, the volume stays.
	if !volumeExists(volume) {
		t.Fatalf("volume %s missing before delete", volume)
	}
	code, body = sendJSON(t, h, http.MethodDelete, "/api/v1/stacks/"+name+"?confirm="+name, nil)
	if code != http.StatusAccepted {
		t.Fatalf("delete: %d %s", code, body)
	}
	var op api.Operation
	json.Unmarshal(body, &op)
	if d := waitOperation(t, h, op.Id); d.Status != api.OperationStatusSucceeded {
		t.Fatalf("delete: %s\n%s", d.Status, d.Output)
	}
	if _, err := os.Stat(filepath.Join(stacksDir, name)); !errors.Is(err, os.ErrNotExist) {
		t.Error("stack directory still exists")
	}
	if !volumeExists(volume) {
		t.Error("the volume was deleted without volumes=true")
	}
}

func TestDeleteWithVolumesAgainstDocker(t *testing.T) {
	b := make([]byte, 4)
	rand.Read(b)
	name := "gantry-test-" + hex.EncodeToString(b) + "-vol"
	volume := name + "_data"
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-p", name, "down", "-v", "--remove-orphans", "-t", "1").Run()
		exec.Command("docker", "volume", "rm", "-f", volume).Run()
	})

	stacksDir := tempDir(t)
	dc, _ := docker.New(os.Getenv("DOCKER_HOST"))
	manager, _ := stacks.New(stacksDir, dc, compose.CLI{})
	ops := operations.NewManager(context.Background(), compose.CLI{}, operations.Options{})
	h := api.New(api.Config{Token: token, Stacks: manager, Operations: ops, Files: stackfiles.New(compose.CLI{})})

	code, body := sendJSON(t, h, http.MethodPost, "/api/v1/stacks", api.StackCreate{Name: name, Compose: `services:
  app:
    image: alpine:3.24
    command: ["sleep", "infinity"]
    init: true
    stop_grace_period: 1s
    volumes:
      - data:/data
volumes:
  data: {}
`})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	runAction(t, h, name, api.StackActionStart)
	// The details list the volume, as the delete dialog shows it.
	if d := stackStatus(t, h, name); len(d.Volumes) != 1 || d.Volumes[0] != volume {
		t.Errorf("volumes = %v, want [%s]", d.Volumes, volume)
	}

	code, body = sendJSON(t, h, http.MethodDelete, "/api/v1/stacks/"+name+"?confirm="+name+"&volumes=true", nil)
	if code != http.StatusAccepted {
		t.Fatalf("delete: %d %s", code, body)
	}
	var op api.Operation
	json.Unmarshal(body, &op)
	if d := waitOperation(t, h, op.Id); d.Status != api.OperationStatusSucceeded {
		t.Fatalf("delete: %s\n%s", d.Status, d.Output)
	}
	if volumeExists(volume) {
		t.Error("volume still exists after delete with volumes=true")
	}
}
