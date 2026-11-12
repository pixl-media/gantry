package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Method + " " + r.URL.Path + " auth=" + r.Header.Get("Authorization")))
	}))
	defer agent.Close()
	target, _ := url.Parse(agent.URL)

	spec := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(spec, []byte("openapi: 3.0.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newHandler(spec, target))
	defer srv.Close()

	tests := []struct {
		path, auth, want string
	}{
		{"/", "", "SwaggerUIBundle"},
		{"/openapi.yaml", "", "openapi: 3.0.3"},
		{"/api/v1/stacks", "Bearer dev", "GET /api/v1/stacks auth=Bearer dev"},
		{"/healthz", "", "GET /healthz"},
	}
	for _, tt := range tests {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+tt.path, nil)
		if tt.auth != "" {
			req.Header.Set("Authorization", tt.auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), tt.want) {
			t.Errorf("GET %s = %d %q, want %q", tt.path, resp.StatusCode, body, tt.want)
		}
	}
}

func TestAgentDown(t *testing.T) {
	target, _ := url.Parse("http://127.0.0.1:1") // nothing listens here
	srv := httptest.NewServer(newHandler("unused", target))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/stacks")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "make run") {
		t.Errorf("status = %d, body = %s", resp.StatusCode, body)
	}
}
