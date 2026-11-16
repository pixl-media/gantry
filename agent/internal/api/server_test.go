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
	"slices"
	"strings"
	"testing"

	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/docker"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

type fakeStacks struct {
	list      stacks.List
	detail    stacks.Detail
	err       error
	unmanaged []string                      // names Target reports as unmanaged
	plans     map[string]stacks.ServicePlan // by service, for PlanService
}

// PlanService returns the plan in plans for service, with action filled in.
func (f fakeStacks) PlanService(ctx context.Context, name, service string, action stacks.ServiceAction) (compose.Project, stacks.ServicePlan, error) {
	p, err := f.Target(ctx, name)
	switch {
	case err != nil:
		return compose.Project{}, stacks.ServicePlan{}, err
	case !stacks.ValidServiceName(service):
		return compose.Project{}, stacks.ServicePlan{}, stacks.ErrInvalidService
	}
	plan, ok := f.plans[service]
	if !ok {
		return compose.Project{}, stacks.ServicePlan{}, stacks.ErrServiceNotFound
	}
	plan.Action = action
	return p, plan, nil
}

func (f fakeStacks) Dir() string         { return "/s" }
func (f fakeStacks) Remove(string) error { return nil }

// Target treats "app" as managed, names in unmanaged as unmanaged, and
// everything else as unknown.
func (f fakeStacks) Target(_ context.Context, name string) (compose.Project, error) {
	switch {
	case !stacks.ValidName(name):
		return compose.Project{}, stacks.ErrInvalidName
	case name == "app":
		return compose.Project{Name: "app", Dir: "/s/app", File: "/s/app/compose.yaml"}, nil
	case slices.Contains(f.unmanaged, name):
		return compose.Project{}, stacks.ErrUnmanaged
	}
	return compose.Project{}, stacks.ErrNotFound
}

func (f fakeStacks) List(context.Context) (stacks.List, error) { return f.list, f.err }

func (f fakeStacks) Get(_ context.Context, name string) (stacks.Detail, error) {
	if f.err != nil {
		return stacks.Detail{}, f.err
	}
	if name != f.detail.Name {
		return stacks.Detail{}, stacks.ErrNotFound
	}
	return f.detail, nil
}

func do(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthzIsPublic(t *testing.T) {
	h := New(Config{Token: "secret", UIToken: testUIToken, Version: "test"})
	if rec := do(t, h, "/healthz", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("GET /healthz = %d %s", rec.Code, rec.Body)
	}
}

func TestAuth(t *testing.T) {
	tests := []struct {
		name   string
		token  string
		path   string
		header string
		want   int
	}{
		{"no header", "secret", "/api/v1/version", "", http.StatusUnauthorized},
		{"wrong token", "secret", "/api/v1/version", "Bearer nope", http.StatusUnauthorized},
		{"missing Bearer prefix", "secret", "/api/v1/version", "secret", http.StatusUnauthorized},
		{"valid token", "secret", "/api/v1/version", "Bearer secret", http.StatusOK},
		{"empty configured token denies all", "", "/api/v1/version", "Bearer ", http.StatusUnauthorized},
		{"stacks need a token", "secret", "/api/v1/stacks", "", http.StatusUnauthorized},
		{"unknown paths need a token too", "secret", "/debug", "", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(Config{Token: tt.token, UIToken: testUIToken, Version: "test", Stacks: fakeStacks{}})
			req := newRequest(http.MethodGet, tt.path, nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestListStacks(t *testing.T) {
	h := New(Config{Token: "t", UIToken: testUIToken, Stacks: fakeStacks{list: stacks.List{
		DockerAvailable: true,
		Stacks: []stacks.Summary{
			{Name: "whoami", Managed: true, Status: stacks.StatusRunning, ServicesTotal: 1, ServicesRunning: 1, Path: "/s/whoami"},
		},
	}}})
	rec := do(t, h, "/api/v1/stacks", "t")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	// Arrays must be [] rather than null for clients.
	if !strings.Contains(rec.Body.String(), `"warnings":[]`) {
		t.Errorf("body = %s, want empty warnings array", rec.Body)
	}
	var got StackList
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := StackSummary{Name: "whoami", Managed: true, Status: StackStatusRunning, ServicesTotal: 1, ServicesRunning: 1, Path: "/s/whoami"}
	if !got.DockerAvailable || len(got.Stacks) != 1 || got.Stacks[0] != want {
		t.Errorf("got %+v", got)
	}
}

func TestGetStack(t *testing.T) {
	content := "services: {}\n"
	detail := stacks.Detail{
		Summary:         stacks.Summary{Name: "whoami", Managed: true, Status: stacks.StatusRunning, ServicesTotal: 1, ServicesRunning: 1},
		DockerAvailable: true,
		ComposeFile:     &stacks.ComposeFile{Path: "/s/whoami/compose.yaml", Content: &content},
		Services: []stacks.Service{{Name: "whoami", Containers: []docker.Container{{
			ID: "abc", Name: "whoami-whoami-1", State: "running", Health: "healthy",
			Ports: []docker.Port{{IP: "127.0.0.1", PrivatePort: 80, PublicPort: 8081, Type: "tcp"}, {PrivatePort: 443, Type: "tcp"}},
		}}}},
	}
	h := New(Config{Token: "t", UIToken: testUIToken, Stacks: fakeStacks{detail: detail}})

	rec := do(t, h, "/api/v1/stacks/whoami", "t")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var got StackDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ComposeFile == nil || got.ComposeFile.Content == nil || *got.ComposeFile.Content != content {
		t.Errorf("compose file = %+v", got.ComposeFile)
	}
	c := got.Services[0].Containers[0]
	if c.Health != ContainerHealthHealthy || len(c.Ports) != 2 || *c.Ports[0].PublicPort != 8081 || c.Ports[1].PublicPort != nil {
		t.Errorf("container = %+v", c)
	}

	if rec := do(t, h, "/api/v1/stacks/nope", "t"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown stack: status = %d, want 404", rec.Code)
	}
}

func TestGetStackInvalidName(t *testing.T) {
	h := New(Config{Token: "t", UIToken: testUIToken, Stacks: fakeStacks{err: stacks.ErrInvalidName}})
	if rec := do(t, h, "/api/v1/stacks/Bad.Name", "t"); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// Unexpected errors go to the log; the client gets no paths or commands.
func TestInternalError(t *testing.T) {
	var logs bytes.Buffer
	h := New(Config{
		Token:   "t",
		UIToken: testUIToken,
		Stacks:  fakeStacks{err: errors.New("reading stacks directory /opt/stacks: permission denied")},
		Logger:  slog.New(slog.NewTextHandler(&logs, nil)),
	})
	rec := do(t, h, "/api/v1/stacks", "t")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "/opt/stacks") {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(logs.String(), "permission denied") {
		t.Errorf("the log lacks the details:\n%s", logs.String())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options: nosniff missing")
	}
}

// newRequest is httptest.NewRequest from a local address. httptest's default
// (192.0.2.1) is an internet address, from which plain HTTP with credentials
// is refused; TestPlainHTTPFromTheInternet covers that.
func newRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.RemoteAddr = "127.0.0.1:1234"
	return req
}
