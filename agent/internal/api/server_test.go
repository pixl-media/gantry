package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pixl-media/gantry/agent/internal/docker"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

type fakeStacks struct {
	list   stacks.List
	detail stacks.Detail
	err    error
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
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthzIsPublic(t *testing.T) {
	h := New(Config{Token: "secret", Version: "test"})
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
			h := New(Config{Token: tt.token, Version: "test", Stacks: fakeStacks{}})
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
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
	h := New(Config{Token: "t", Stacks: fakeStacks{list: stacks.List{
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
	h := New(Config{Token: "t", Stacks: fakeStacks{detail: detail}})

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
	h := New(Config{Token: "t", Stacks: fakeStacks{err: stacks.ErrInvalidName}})
	if rec := do(t, h, "/api/v1/stacks/Bad.Name", "t"); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestInternalError(t *testing.T) {
	h := New(Config{Token: "t", Stacks: fakeStacks{err: errors.New("reading stacks directory: permission denied")}})
	rec := do(t, h, "/api/v1/stacks", "t")
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "permission denied") {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body)
	}
}
