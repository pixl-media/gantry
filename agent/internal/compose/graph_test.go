package compose

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGraph(t *testing.T) {
	// The shape of `docker compose config --format json` output.
	out := `{"name":"app","services":{
		"db":{"image":"postgres","environment":{"POSTGRES_PASSWORD":"secret"}},
		"app":{"depends_on":{"db":{"condition":"service_started","required":true}},"volumes_from":["db:ro","container:legacy"]},
		"web":{"depends_on":{"app":{"condition":"service_healthy"}},"network_mode":"service:app"},
		"side":{"network_mode":"host","volumes_from":["gone"]}
	}}`
	bin, argsFile := fakeDocker(t, "cat <<'EOF'\n"+out+"\nEOF")
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")

	g, err := CLI{Docker: bin}.Graph(context.Background(), Project{Name: "app", Dir: dir, File: file})
	if err != nil {
		t.Fatal(err)
	}
	want := Graph{"db": nil, "app": {"db"}, "web": {"app"}, "side": nil}
	for k, v := range want {
		if !slices.Equal(g[k], v) {
			t.Errorf("%s needs %v, want %v", k, g[k], v)
		}
	}
	if len(g) != len(want) {
		t.Errorf("graph = %v", g)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"compose", "-p", "app", "-f", file, "--project-directory", dir, "config", "--format", "json", "--no-interpolate"}
	if args := strings.Fields(string(raw)); !slices.Equal(args, wantArgs) {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
}

func TestParseGraphErrors(t *testing.T) {
	if _, err := parseGraph([]byte("not json")); err == nil {
		t.Error("garbage parsed")
	}
	g, err := parseGraph([]byte(`{"services":{"a":{"depends_on":{"a":{}}}}}`))
	if err != nil || len(g) != 1 || len(g["a"]) != 0 {
		t.Errorf("self-dependency: %v, %v", g, err)
	}
}
