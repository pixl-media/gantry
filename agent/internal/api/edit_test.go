package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/docker"
	"github.com/pixl-media/gantry/agent/internal/live"
	"github.com/pixl-media/gantry/agent/internal/operations"
	"github.com/pixl-media/gantry/agent/internal/stackfiles"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

// dockerStub knows a fixed set of containers and no volumes.
type dockerStub struct{ containers []docker.Container }

func (d dockerStub) ListComposeContainers(context.Context) ([]docker.Container, error) {
	return d.containers, nil
}

type servicesStub struct{}

func (servicesStub) Services(context.Context, compose.Project) ([]string, error) {
	return []string{"app"}, nil
}

// validatorStub rejects Compose files containing "invalid".
type validatorStub struct{}

func (validatorStub) Validate(_ context.Context, p compose.Project, displayName string) error {
	data, err := os.ReadFile(p.File)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), "invalid") {
		return &compose.InvalidError{Message: displayName + ": services must be a mapping"}
	}
	return nil
}

// recordingRunner records Compose commands and succeeds.
type recordingRunner struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingRunner) Run(_ context.Context, _ compose.Project, args []string, out io.Writer) (int, error) {
	r.mu.Lock()
	r.calls = append(r.calls, strings.Join(args, " "))
	r.mu.Unlock()
	return 0, nil
}

type editEnv struct {
	h      http.Handler
	dir    string
	ops    *operations.Manager
	runner *recordingRunner
	audit  *bytes.Buffer
	live   *recordingLive
}

// newEditEnv runs the real stacks, stackfiles and operations packages on a
// temporary stacks directory with a managed stack "app". Docker knows an
// unmanaged project "ext".
func newEditEnv(t *testing.T) editEnv {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "app"), 0o755)
	os.WriteFile(filepath.Join(dir, "app", "compose.yaml"), []byte("services: {}\n"), 0o644)

	ext := docker.Container{ID: "e", Name: "ext-x-1", State: "running", Labels: map[string]string{
		docker.LabelProject: "ext", docker.LabelService: "x", docker.LabelWorkingDir: "/elsewhere",
	}}
	manager, err := stacks.New(dir, dockerStub{containers: []docker.Container{ext}}, servicesStub{})
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	ops := operations.NewManager(context.Background(), runner, operations.Options{})
	var audit bytes.Buffer
	rl := &recordingLive{Hub: live.New(nil, live.Options{})}
	h := New(Config{
		Token:      "t",
		UIToken:    testUIToken,
		Stacks:     manager,
		Operations: ops,
		Files:      stackfiles.New(validatorStub{}),
		Live:       rl,
		Logger:     slog.New(slog.NewTextHandler(&audit, nil)),
	})
	return editEnv{h: h, dir: manager.Dir(), ops: ops, runner: runner, audit: &audit, live: rl}
}

func send(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req := newRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func code(t *testing.T, rec *httptest.ResponseRecorder) ErrorCode {
	t.Helper()
	e := decode[Error](t, rec)
	if e.Code == nil {
		t.Fatalf("no error code in %s", rec.Body)
	}
	return *e.Code
}

func TestUpdateCompose(t *testing.T) {
	env := newEditEnv(t)
	detail := decode[StackDetail](t, send(t, env.h, http.MethodGet, "/api/v1/stacks/app", nil))
	version := *detail.ComposeFile.Version

	rec := send(t, env.h, http.MethodPut, "/api/v1/stacks/app/compose", ComposeUpdate{Content: "services:\n  web: {}\n", Version: version})
	if rec.Code != http.StatusOK {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	newVersion := decode[FileVersion](t, rec).Version
	if got, _ := os.ReadFile(filepath.Join(env.dir, "app", "compose.yaml")); string(got) != "services:\n  web: {}\n" {
		t.Errorf("file = %q", got)
	}

	tests := []struct {
		name, path string
		body       ComposeUpdate
		status     int
		code       ErrorCode
	}{
		{"stale version", "app", ComposeUpdate{Content: "x: 1\n", Version: version}, http.StatusConflict, ErrorCodeVersionConflict},
		{"invalid", "app", ComposeUpdate{Content: "invalid\n", Version: newVersion}, http.StatusBadRequest, ErrorCodeInvalidCompose},
		{"unmanaged", "ext", ComposeUpdate{Content: "services: {}\n", Version: ""}, http.StatusConflict, ErrorCodeStackUnmanaged},
		{"unknown", "nope", ComposeUpdate{Content: "services: {}\n", Version: ""}, http.StatusNotFound, ErrorCodeNotFound},
	}
	for _, tt := range tests {
		rec := send(t, env.h, http.MethodPut, "/api/v1/stacks/"+tt.path+"/compose", tt.body)
		if rec.Code != tt.status || code(t, rec) != tt.code {
			t.Errorf("%s: %d %s", tt.name, rec.Code, rec.Body)
		}
	}
	// Compose's message names the real file, not the temporary copy.
	rec = send(t, env.h, http.MethodPut, "/api/v1/stacks/app/compose", ComposeUpdate{Content: "invalid\n", Version: newVersion})
	if msg := decode[Error](t, rec).Error; !strings.HasPrefix(msg, "compose.yaml:") {
		t.Errorf("message = %q", msg)
	}
}

func TestEnvNeverLeaksValues(t *testing.T) {
	env := newEditEnv(t)
	const secret = "s3cr3t-Pw!"

	info := decode[EnvFile](t, send(t, env.h, http.MethodGet, "/api/v1/stacks/app/env", nil))
	if info.Exists || info.Version != "" || len(info.Variables) != 0 {
		t.Fatalf("no file yet: %+v", info)
	}
	rec := send(t, env.h, http.MethodPatch, "/api/v1/stacks/app/env", EnvUpdate{
		Version: "", Set: &map[string]string{"DB_PASSWORD": secret, "DB_USER": "admin"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("set = %d: %s", rec.Code, rec.Body)
	}
	info = decode[EnvFile](t, rec)
	if !info.Exists || len(info.Variables) != 2 {
		t.Errorf("after set: %+v", info)
	}

	// Values appear nowhere except in the explicit reveal.
	for _, path := range []string{"/api/v1/stacks/app/env", "/api/v1/stacks/app", "/api/v1/stacks"} {
		if body := send(t, env.h, http.MethodGet, path, nil).Body.String(); strings.Contains(body, secret) {
			t.Errorf("GET %s leaks the value", path)
		}
	}
	rec = send(t, env.h, http.MethodPost, "/api/v1/stacks/app/env/reveal", EnvRevealRequest{Key: "DB_PASSWORD"})
	if got := decode[EnvValue](t, rec); rec.Code != http.StatusOK || got.Value != secret {
		t.Errorf("reveal = %d %+v", rec.Code, got)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("reveal must not be cacheable")
	}
	log := env.audit.String()
	if !strings.Contains(log, "env value revealed") || !strings.Contains(log, "DB_PASSWORD") {
		t.Errorf("audit log lacks the reveal: %s", log)
	}
	if strings.Contains(log, secret) {
		t.Error("the audit log contains the secret value")
	}

	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   any
		status int
		code   ErrorCode
	}{
		{"reveal unknown", http.MethodPost, "env/reveal", EnvRevealRequest{Key: "NOPE"}, http.StatusNotFound, ErrorCodeNotFound},
		{"reveal bad key", http.MethodPost, "env/reveal", EnvRevealRequest{Key: "../x"}, http.StatusBadRequest, ErrorCodeInvalidVariable},
		{"stale version", http.MethodPatch, "env", EnvUpdate{Version: "", Delete: &[]string{"DB_USER"}}, http.StatusConflict, ErrorCodeVersionConflict},
		{"bad key", http.MethodPatch, "env", EnvUpdate{Version: info.Version, Set: &map[string]string{"BAD-KEY": "x"}}, http.StatusBadRequest, ErrorCodeInvalidVariable},
	} {
		rec := send(t, env.h, tt.method, "/api/v1/stacks/app/"+tt.path, tt.body)
		if rec.Code != tt.status || code(t, rec) != tt.code {
			t.Errorf("%s: %d %s", tt.name, rec.Code, rec.Body)
		}
	}
	if rec := send(t, env.h, http.MethodGet, "/api/v1/stacks/ext/env", nil); rec.Code != http.StatusConflict {
		t.Errorf("env of an unmanaged stack = %d", rec.Code)
	}
}

func TestCreateStack(t *testing.T) {
	env := newEditEnv(t)
	rec := send(t, env.h, http.MethodPost, "/api/v1/stacks", StackCreate{
		Name: "fresh", Compose: "services: {}\n", Env: &map[string]string{"TOKEN": "abc"},
	})
	if rec.Code != http.StatusCreated || rec.Header().Get("Location") != "/api/v1/stacks/fresh" {
		t.Fatalf("create = %d %s, Location %q", rec.Code, rec.Body, rec.Header().Get("Location"))
	}
	if _, err := os.Stat(filepath.Join(env.dir, "fresh", ".env")); err != nil {
		t.Error("no .env written")
	}
	if strings.Contains(env.audit.String(), "abc") {
		t.Error("the audit log contains a value")
	}

	for _, tt := range []struct {
		name   string
		body   StackCreate
		status int
		code   ErrorCode
	}{
		{"existing directory", StackCreate{Name: "fresh", Compose: "services: {}\n"}, http.StatusConflict, ErrorCodeStackExists},
		{"name of an unmanaged project", StackCreate{Name: "ext", Compose: "services: {}\n"}, http.StatusConflict, ErrorCodeStackExists},
		{"invalid name", StackCreate{Name: "Bad.Name", Compose: "services: {}\n"}, http.StatusBadRequest, ErrorCodeInvalidName},
		{"invalid compose", StackCreate{Name: "broken", Compose: "invalid\n"}, http.StatusBadRequest, ErrorCodeInvalidCompose},
		{"invalid variable", StackCreate{Name: "badvar", Compose: "services: {}\n", Env: &map[string]string{"1X": "y"}}, http.StatusBadRequest, ErrorCodeInvalidVariable},
	} {
		rec := send(t, env.h, http.MethodPost, "/api/v1/stacks", tt.body)
		if rec.Code != tt.status || code(t, rec) != tt.code {
			t.Errorf("%s: %d %s", tt.name, rec.Code, rec.Body)
		}
	}
	for _, name := range []string{"broken", "badvar"} {
		if _, err := os.Stat(filepath.Join(env.dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("failed create left %s behind", name)
		}
	}
}

func TestDeleteStack(t *testing.T) {
	env := newEditEnv(t)

	if rec := send(t, env.h, http.MethodDelete, "/api/v1/stacks/app?confirm=wrong", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("wrong confirm = %d", rec.Code)
	}
	if rec := send(t, env.h, http.MethodDelete, "/api/v1/stacks/ext?confirm=ext", nil); rec.Code != http.StatusConflict {
		t.Errorf("unmanaged = %d", rec.Code)
	}

	rec := send(t, env.h, http.MethodDelete, "/api/v1/stacks/app?confirm=app&volumes=true", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body)
	}
	op := decode[Operation](t, rec)
	if op.Action != OperationActionDelete {
		t.Errorf("action = %s", op.Action)
	}
	if s := waitDone(t, env.ops, op.Id); s.Status != operations.StatusSucceeded {
		t.Fatalf("delete operation = %+v", s)
	}
	if _, err := os.Stat(filepath.Join(env.dir, "app")); !errors.Is(err, os.ErrNotExist) {
		t.Error("stack directory still exists")
	}
	if !slices.Contains(env.runner.calls, "down --remove-orphans --volumes") {
		t.Errorf("calls = %v, want down with --volumes", env.runner.calls)
	}
	if rec := send(t, env.h, http.MethodGet, "/api/v1/stacks/app", nil); rec.Code != http.StatusNotFound {
		t.Errorf("deleted stack still found: %d", rec.Code)
	}
}

type downDocker struct{}

func (downDocker) ListComposeContainers(context.Context) ([]docker.Container, error) {
	return nil, errors.New("connection refused")
}

func TestCreateStackDockerUnavailable(t *testing.T) {
	dir := t.TempDir()
	manager, err := stacks.New(dir, downDocker{}, servicesStub{})
	if err != nil {
		t.Fatal(err)
	}
	h := New(Config{Token: "t", UIToken: testUIToken, Stacks: manager, Files: stackfiles.New(validatorStub{})})
	rec := send(t, h, http.MethodPost, "/api/v1/stacks", StackCreate{Name: "fresh", Compose: "services: {}\n"})
	if e := decode[Error](t, rec); rec.Code != http.StatusServiceUnavailable || *e.Code != ErrorCodeDockerUnavailable {
		t.Errorf("create = %d %+v", rec.Code, e)
	}
	if _, err := os.Stat(filepath.Join(dir, "fresh")); err == nil {
		t.Error("the stack was created although the name couldn't be checked")
	}
}

// Saving files through the API tells live clients at once; Docker sends no
// event for file changes.
func TestFileChangesNotify(t *testing.T) {
	env := newEditEnv(t)
	detail := decode[StackDetail](t, send(t, env.h, http.MethodGet, "/api/v1/stacks/app", nil))
	if rec := send(t, env.h, http.MethodPut, "/api/v1/stacks/app/compose", ComposeUpdate{Content: "services:\n  web: {}\n", Version: *detail.ComposeFile.Version}); rec.Code != http.StatusOK {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	if rec := send(t, env.h, http.MethodPatch, "/api/v1/stacks/app/env", EnvUpdate{Set: &map[string]string{"A": "1"}}); rec.Code != http.StatusOK {
		t.Fatalf("env = %d: %s", rec.Code, rec.Body)
	}
	if rec := send(t, env.h, http.MethodPost, "/api/v1/stacks", StackCreate{Name: "fresh", Compose: "services: {}\n"}); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body)
	}
	// A failed save changes nothing and tells nobody.
	send(t, env.h, http.MethodPut, "/api/v1/stacks/app/compose", ComposeUpdate{Content: "invalid", Version: "stale"})
	if got := env.live.calls(); !slices.Equal(got, []string{"app", "app", "fresh"}) {
		t.Errorf("notified %v", got)
	}
}
