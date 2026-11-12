//go:build integration

// Integration tests against a real Docker daemon (Docker Desktop locally, the
// runner's Docker in CI). Run with `make test-integration`.
//
// Every stack is named gantry-test-<random>-<case> and removed afterwards;
// assertions only look at these stacks, so other containers on the host are
// never touched.
package api_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
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
	"github.com/pixl-media/gantry/agent/internal/logs"
	"github.com/pixl-media/gantry/agent/internal/operations"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

const token = "integration"

// tempDir returns a symlink-free temporary directory, so that paths match
// the working_dir labels Compose writes (/var vs /private/var on macOS).
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gantry-it-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeStack writes a Compose file with sleeping alpine services.
func writeStack(t *testing.T, dir string, services ...string) string {
	t.Helper()
	content := "services:\n"
	for _, s := range services {
		content += "  " + s + ":\n" +
			"    image: alpine:3.24\n" +
			"    command: [\"sleep\", \"infinity\"]\n" +
			"    init: true\n" +
			"    stop_grace_period: 1s\n"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func dockerCompose(t *testing.T, project, file string, args ...string) {
	t.Helper()
	cmd := exec.Command("docker", append([]string{"compose", "-p", project, "-f", file}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker compose %v: %v\n%s", args, err, out)
	}
}

// up starts a project and registers its removal, even if the test fails.
func up(t *testing.T, project, file string) {
	t.Helper()
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-p", project, "-f", file, "down", "-v", "--remove-orphans", "-t", "1").Run()
	})
	dockerCompose(t, project, file, "up", "-d", "--quiet-pull")
}

func get(t *testing.T, h http.Handler, path string, v any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if v != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("decode %s: %v\n%s", path, err, rec.Body)
		}
	}
	return rec.Code
}

func TestStacksAgainstDocker(t *testing.T) {
	b := make([]byte, 4)
	rand.Read(b)
	prefix := "gantry-test-" + hex.EncodeToString(b)
	var (
		running  = prefix + "-running"
		stopped  = prefix + "-stopped"
		partial  = prefix + "-partial"
		external = prefix + "-external"
	)

	stacksDir := tempDir(t)
	externalDir := tempDir(t)

	runningFile := writeStack(t, filepath.Join(stacksDir, running), "app")
	writeStack(t, filepath.Join(stacksDir, stopped), "app") // never started
	partialFile := writeStack(t, filepath.Join(stacksDir, partial), "a", "b")
	externalFile := writeStack(t, externalDir, "x")

	up(t, running, runningFile)
	up(t, partial, partialFile)
	dockerCompose(t, partial, partialFile, "stop", "-t", "1", "b")
	up(t, external, externalFile)

	dc, err := docker.New(os.Getenv("DOCKER_HOST"))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := stacks.New(stacksDir, dc, compose.CLI{})
	if err != nil {
		t.Fatal(err)
	}
	h := api.New(api.Config{Token: token, Version: "it", Stacks: manager})

	var list api.StackList
	if code := get(t, h, "/api/v1/stacks", &list); code != http.StatusOK {
		t.Fatalf("list: status %d", code)
	}
	if !list.DockerAvailable {
		t.Fatalf("dockerAvailable = false, warnings: %v", list.Warnings)
	}
	byName := map[string]api.StackSummary{}
	for _, s := range list.Stacks {
		byName[s.Name] = s
	}

	tests := []api.StackSummary{
		{Name: running, Managed: true, Status: api.StackStatusRunning, ServicesTotal: 1, ServicesRunning: 1, Path: filepath.Join(stacksDir, running)},
		{Name: stopped, Managed: true, Status: api.StackStatusStopped, ServicesTotal: 1, ServicesRunning: 0, Path: filepath.Join(stacksDir, stopped)},
		{Name: partial, Managed: true, Status: api.StackStatusPartial, ServicesTotal: 2, ServicesRunning: 1, Path: filepath.Join(stacksDir, partial)},
		{Name: external, Managed: false, Status: api.StackStatusRunning, ServicesTotal: 1, ServicesRunning: 1, Path: externalDir},
	}
	for _, want := range tests {
		got, ok := byName[want.Name]
		if !ok {
			t.Errorf("%s missing from list", want.Name)
			continue
		}
		if got != want {
			t.Errorf("%s:\n got  %+v\n want %+v", want.Name, got, want)
		}
	}

	var detail api.StackDetail
	if code := get(t, h, "/api/v1/stacks/"+running, &detail); code != http.StatusOK {
		t.Fatalf("detail: status %d", code)
	}
	raw, _ := os.ReadFile(runningFile)
	if detail.ComposeFile == nil || detail.ComposeFile.Content == nil || *detail.ComposeFile.Content != string(raw) {
		t.Errorf("compose file = %+v, want raw content", detail.ComposeFile)
	}
	if len(detail.Services) != 1 || len(detail.Services[0].Containers) != 1 || detail.Services[0].Containers[0].State != "running" {
		t.Errorf("services = %+v", detail.Services)
	}

	var ext api.StackDetail
	if code := get(t, h, "/api/v1/stacks/"+external, &ext); code != http.StatusOK {
		t.Fatalf("external detail: status %d", code)
	}
	if ext.Managed || ext.ComposeFile == nil || ext.ComposeFile.Path != externalFile || ext.ComposeFile.Content == nil {
		t.Errorf("external = %+v, compose file %+v", ext, ext.ComposeFile)
	}

	if code := get(t, h, "/api/v1/stacks/"+prefix+"-missing", nil); code != http.StatusNotFound {
		t.Errorf("missing stack: status %d, want 404", code)
	}
}

func post(t *testing.T, h http.Handler, path string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// runAction starts an action and waits until its operation has finished.
func runAction(t *testing.T, h http.Handler, stack string, action api.StackAction) api.OperationDetail {
	t.Helper()
	code, body := post(t, h, "/api/v1/stacks/"+stack+"/actions/"+string(action))
	if code != http.StatusAccepted {
		t.Fatalf("%s: status %d: %s", action, code, body)
	}
	var op api.Operation
	json.Unmarshal(body, &op)
	deadline := time.Now().Add(3 * time.Minute)
	for {
		var d api.OperationDetail
		get(t, h, "/api/v1/operations/"+op.Id, &d)
		if d.Status != api.OperationStatusRunning {
			if d.Status != api.OperationStatusSucceeded {
				t.Fatalf("%s: %s: %v\n%s", action, d.Status, d.Error, d.Output)
			}
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: still running after 3 minutes", action)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func stackStatus(t *testing.T, h http.Handler, name string) api.StackDetail {
	t.Helper()
	var d api.StackDetail
	if code := get(t, h, "/api/v1/stacks/"+name, &d); code != http.StatusOK {
		t.Fatalf("stack %s: status %d", name, code)
	}
	return d
}

func TestActionsAgainstDocker(t *testing.T) {
	b := make([]byte, 4)
	rand.Read(b)
	prefix := "gantry-test-" + hex.EncodeToString(b)
	name := prefix + "-actions"
	external := prefix + "-external"

	stacksDir := tempDir(t)
	file := writeStack(t, filepath.Join(stacksDir, name), "app")
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-p", name, "-f", file, "down", "-v", "--remove-orphans", "-t", "1").Run()
	})
	externalFile := writeStack(t, tempDir(t), "x")
	up(t, external, externalFile)

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

	// start, followed live over Server-Sent Events.
	srv := httptest.NewServer(h)
	defer srv.Close()
	code, body := post(t, h, "/api/v1/stacks/"+name+"/actions/start")
	if code != http.StatusAccepted {
		t.Fatalf("start: %d %s", code, body)
	}
	var op api.Operation
	json.Unmarshal(body, &op)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/operations/"+op.Id+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if s := string(stream); !strings.Contains(s, "Started") || !strings.Contains(s, "event: done") || !strings.Contains(s, `"status":"succeeded"`) {
		t.Fatalf("event stream:\n%s", s)
	}
	if d := stackStatus(t, h, name); d.Status != api.StackStatusRunning || d.ActiveOperation != nil {
		t.Errorf("after start: %s, active %+v", d.Status, d.ActiveOperation)
	}

	runAction(t, h, name, api.StackActionRestart)
	if d := stackStatus(t, h, name); d.Status != api.StackStatusRunning {
		t.Errorf("after restart: %s", d.Status)
	}

	runAction(t, h, name, api.StackActionStop)
	d := stackStatus(t, h, name)
	if d.Status != api.StackStatusStopped || len(d.Services[0].Containers) != 1 {
		t.Errorf("after stop: %s, containers %+v (stop keeps containers)", d.Status, d.Services[0].Containers)
	}

	update := runAction(t, h, name, api.StackActionUpdate)
	if !strings.Contains(update.Output, "$ docker compose pull --ignore-buildable") {
		t.Errorf("update output:\n%s", update.Output)
	}
	if d := stackStatus(t, h, name); d.Status != api.StackStatusRunning {
		t.Errorf("after update: %s", d.Status)
	}

	runAction(t, h, name, api.StackActionDown)
	d = stackStatus(t, h, name)
	if d.Status != api.StackStatusStopped || len(d.Services[0].Containers) != 0 {
		t.Errorf("after down: %s, containers %+v (down removes containers)", d.Status, d.Services[0].Containers)
	}

	// Stacks outside the stacks directory are never changed.
	code, body = post(t, h, "/api/v1/stacks/"+external+"/actions/stop")
	if code != http.StatusConflict || !strings.Contains(string(body), "stack_unmanaged") {
		t.Errorf("unmanaged stop: %d %s", code, body)
	}
}

// sseEvent is one Server-Sent Event.
type sseEvent struct{ name, data string }

// readEvents sends the events of an SSE stream to the returned channel until
// the stream ends.
func readEvents(r io.Reader) <-chan sseEvent {
	ch := make(chan sseEvent, 64)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			case line == "":
				ch <- ev
				ev = sseEvent{}
			}
		}
	}()
	return ch
}

func TestLogsAgainstDocker(t *testing.T) {
	b := make([]byte, 4)
	rand.Read(b)
	name := "gantry-test-" + hex.EncodeToString(b) + "-logs"
	stacksDir := tempDir(t)
	dir := filepath.Join(stacksDir, name)
	os.MkdirAll(dir, 0o755)
	file := filepath.Join(dir, "compose.yaml")
	os.WriteFile(file, []byte(`services:
  talk:
    image: alpine:3.24
    command: ["sh", "-c", "i=0; while true; do echo tick $$i; i=$$((i+1)); sleep 0.2; done"]
    init: true
    stop_grace_period: 1s
  err:
    image: alpine:3.24
    command: ["sh", "-c", "echo to-stderr >&2; sleep infinity"]
    init: true
    stop_grace_period: 1s
`), 0o644)
	up(t, name, file)
	time.Sleep(1500 * time.Millisecond)

	dc, err := docker.New(os.Getenv("DOCKER_HOST"))
	if err != nil {
		t.Fatal(err)
	}
	manager, _ := stacks.New(stacksDir, dc, compose.CLI{})
	ops := operations.NewManager(context.Background(), compose.CLI{}, operations.Options{})
	h := api.New(api.Config{Token: token, Stacks: manager, Operations: ops, Logs: logs.New(dc)})
	srv := httptest.NewServer(h)
	defer srv.Close()

	open := func(ctx context.Context, query string) <-chan sseEvent {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/stacks/"+name+"/logs?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return readEvents(resp.Body)
	}

	// Past lines only: both services, in time order, stderr marked.
	var lines []api.LogLine
	ended := false
	for ev := range open(context.Background(), "follow=false&tail=5") {
		switch ev.name {
		case "log":
			var l api.LogLine
			json.Unmarshal([]byte(ev.data), &l)
			lines = append(lines, l)
		case "end":
			ended = true
		}
	}
	if !ended {
		t.Error("no end event with follow=false")
	}
	var sawStderr, sawTick bool
	for i, l := range lines {
		if i > 0 && l.Time.Before(lines[i-1].Time) {
			t.Errorf("lines out of order at %d: %v before %v", i, l.Time, lines[i-1].Time)
		}
		if l.Service == "err" && l.Line == "to-stderr" && l.Stream == api.LogLineStreamStderr {
			sawStderr = true
		}
		if l.Service == "talk" && strings.HasPrefix(l.Line, "tick ") && l.Stream == api.LogLineStreamStdout {
			sawTick = true
		}
	}
	if !sawStderr || !sawTick || len(lines) > 10 {
		t.Errorf("past lines (tail=5 per container) = %+v", lines)
	}

	// Following, across a restart of the stack.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	events := open(ctx, "tail=1&service=talk")
	next := func() api.LogLine {
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					t.Fatal("stream ended")
				}
				if ev.name == "log" {
					var l api.LogLine
					json.Unmarshal([]byte(ev.data), &l)
					return l
				}
			case <-ctx.Done():
				t.Fatal("timed out waiting for log lines")
			}
		}
	}
	seen := map[string]bool{}
	record := func(l api.LogLine) {
		key := l.Time.String() + l.Line
		if seen[key] {
			t.Errorf("duplicate line %q at %v", l.Line, l.Time)
		}
		seen[key] = true
		if l.Service != "talk" {
			t.Errorf("service filter: got a line from %q", l.Service)
		}
	}
	for range 5 {
		record(next())
	}

	runAction(t, h, name, api.StackActionRestart)
	// After the restart the counter starts again at 0, in the same stream.
	deadline := time.Now().Add(30 * time.Second)
	for {
		l := next()
		record(l)
		if l.Line == "tick 2" && time.Now().Before(deadline) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no lines from the restarted container")
		}
	}
}
