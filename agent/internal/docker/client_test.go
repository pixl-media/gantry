package docker

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serveUnix starts an HTTP server on a Unix socket and returns a client for it.
func serveUnix(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	// Short path: Unix socket paths are limited to ~104 bytes on macOS, and
	// t.TempDir() paths can exceed that.
	dir, err := os.MkdirTemp("", "gd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "d.sock")

	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	c, err := New("unix://" + socket)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestListComposeContainers(t *testing.T) {
	c := serveUnix(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/json" {
			t.Errorf("path = %q, want /containers/json", r.URL.Path)
		}
		if got := r.URL.Query().Get("all"); got != "1" {
			t.Errorf("all = %q, want 1", got)
		}
		var filters map[string][]string
		if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters); err != nil {
			t.Errorf("filters: %v", err)
		}
		if got := filters["label"]; len(got) != 1 || got[0] != LabelProject {
			t.Errorf("label filter = %v, want [%s]", got, LabelProject)
		}
		w.Write([]byte(`[
		  {"Id":"a1","Names":["/web-1"],"Image":"nginx","State":"running","Status":"Up 1 hour (healthy)",
		   "Labels":{"com.docker.compose.project":"web"},
		   "Ports":[{"IP":"127.0.0.1","PrivatePort":80,"PublicPort":8080,"Type":"tcp"}],
		   "Health":{"Status":"healthy","FailingStreak":0}},
		  {"Id":"b2","Names":["/db-1"],"Image":"postgres","State":"running","Status":"Up 1 hour (unhealthy)",
		   "Labels":{"com.docker.compose.project":"web"},"Ports":[]},
		  {"Id":"c3","Names":["/job-1"],"Image":"alpine","State":"exited","Status":"Exited (0) 1 minute ago",
		   "Labels":{"com.docker.compose.project":"web"}}
		]`))
	})

	got, err := c.ListComposeContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d containers, want 3", len(got))
	}
	web := got[0]
	if web.ID != "a1" || web.Name != "web-1" || web.State != "running" || web.Health != "healthy" {
		t.Errorf("web = %+v", web)
	}
	if len(web.Ports) != 1 || web.Ports[0] != (Port{IP: "127.0.0.1", PrivatePort: 80, PublicPort: 8080, Type: "tcp"}) {
		t.Errorf("web ports = %+v", web.Ports)
	}
	if web.Labels[LabelProject] != "web" {
		t.Errorf("web labels = %v", web.Labels)
	}
	// Without a Health field, health comes from the status text.
	if got[1].Health != "unhealthy" {
		t.Errorf("db health = %q, want unhealthy", got[1].Health)
	}
	if got[2].Health != "none" {
		t.Errorf("job health = %q, want none", got[2].Health)
	}
}

func TestListComposeContainersError(t *testing.T) {
	c := serveUnix(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
	})
	_, err := c.ListComposeContainers(context.Background())
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want 500 with message", err)
	}
}

func TestListComposeContainersUnreachable(t *testing.T) {
	c, err := New("unix:///nonexistent/gantry-test.sock")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ListComposeContainers(context.Background())
	if err == nil {
		t.Fatal("expected an error for a missing socket")
	}
	if strings.Contains(err.Error(), "http://") {
		t.Errorf("err = %q, should not contain the internal request URL", err)
	}
}

func TestListProjectVolumes(t *testing.T) {
	c := serveUnix(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/volumes" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var filters map[string][]string
		json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters)
		if got := filters["label"]; len(got) != 1 || got[0] != LabelProject+"=app" {
			t.Errorf("label filter = %v", got)
		}
		w.Write([]byte(`{"Volumes":[{"Name":"app_db"},{"Name":"app_cache"}],"Warnings":null}`))
	})
	got, err := c.ListProjectVolumes(context.Background(), "app")
	if err != nil || len(got) != 2 || got[0] != "app_cache" || got[1] != "app_db" {
		t.Errorf("volumes = %v, %v", got, err)
	}
}

func TestNewRejectsUnsupportedHosts(t *testing.T) {
	for _, host := range []string{"tcp://127.0.0.1:2375", "ssh://host", "unix://"} {
		if _, err := New(host); err == nil {
			t.Errorf("New(%q) = nil error, want error", host)
		}
	}
	if _, err := New(""); err != nil {
		t.Errorf("New(\"\") = %v, want default host", err)
	}
}
