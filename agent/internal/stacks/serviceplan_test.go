package stacks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/docker"
)

// chain is web → app → db (web needs app, app needs db), plus an unrelated
// solo service.
var chain = compose.Graph{"web": {"app"}, "app": {"db"}, "db": nil, "solo": nil}

func running(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// affected formats a plan's affected services as "name:effect:reason".
func affected(p ServicePlan) []string {
	var out []string
	for _, a := range p.Affected {
		out = append(out, fmt.Sprintf("%s:%s:%s", a.Service, a.Effect, a.Reason))
	}
	return out
}

func TestPlanServiceAction(t *testing.T) {
	tests := []struct {
		name     string
		running  map[string]bool
		service  string
		action   ServiceAction
		affected []string
		steps    [][]string
	}{
		{
			name: "start starts stopped dependencies", running: running(), service: "web", action: ServiceStart,
			affected: []string{"web:start:target", "app:start:dependency", "db:start:dependency"},
			steps:    [][]string{{"up", "-d", "--no-deps", "app", "db", "web"}},
		},
		{
			name: "start leaves running dependencies alone", running: running("db"), service: "web", action: ServiceStart,
			affected: []string{"web:start:target", "app:start:dependency"},
			steps:    [][]string{{"up", "-d", "--no-deps", "app", "web"}},
		},
		{
			name: "stop stops running dependents", running: running("web", "app", "db"), service: "db", action: ServiceStop,
			affected: []string{"db:stop:target", "app:stop:dependent", "web:stop:dependent"},
			steps:    [][]string{{"stop", "db", "app", "web"}},
		},
		{
			name: "stop ignores stopped dependents", running: running("app", "db"), service: "db", action: ServiceStop,
			affected: []string{"db:stop:target", "app:stop:dependent"},
			steps:    [][]string{{"stop", "db", "app"}},
		},
		{
			name: "restart restarts the chain", running: running("web", "app", "db"), service: "db", action: ServiceRestart,
			affected: []string{"db:restart:target", "app:restart:dependent", "web:restart:dependent"},
			steps:    [][]string{{"stop", "db", "app", "web"}, {"start", "db", "app", "web"}},
		},
		{
			name: "restart starts stopped dependencies first", running: running("web", "app"), service: "app", action: ServiceRestart,
			affected: []string{"app:restart:target", "db:start:dependency", "web:restart:dependent"},
			steps:    [][]string{{"stop", "app", "web"}, {"up", "-d", "--no-deps", "db"}, {"start", "app", "web"}},
		},
		{
			name: "update recreates and restarts dependents", running: running("web", "app", "db"), service: "app", action: ServiceUpdate,
			affected: []string{"app:recreate:target", "web:restart:dependent"},
			steps: [][]string{
				{"pull", "--ignore-buildable", "app"},
				{"up", "-d", "--no-deps", "app"},
				{"stop", "web"},
				{"up", "-d", "--no-deps", "web"},
			},
		},
		{
			name: "update without dependents", running: running(), service: "web", action: ServiceUpdate,
			affected: []string{"web:recreate:target", "app:start:dependency", "db:start:dependency"},
			steps:    [][]string{{"pull", "--ignore-buildable", "web"}, {"up", "-d", "--no-deps", "app", "db", "web"}},
		},
		{
			name: "a service without dependencies affects only itself", running: running("solo", "db"), service: "solo", action: ServiceRestart,
			affected: []string{"solo:restart:target"},
			steps:    [][]string{{"stop", "solo"}, {"start", "solo"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := planServiceAction(chain, tt.running, tt.service, tt.action)
			if got := affected(p); !slices.Equal(got, tt.affected) {
				t.Errorf("affected = %v, want %v", got, tt.affected)
			}
			if !reflect.DeepEqual(p.Steps, tt.steps) {
				t.Errorf("steps = %v, want %v", p.Steps, tt.steps)
			}
			if p.Services()[0] != tt.service || p.Service != tt.service || p.Action != tt.action {
				t.Errorf("plan = %+v", p)
			}
		})
	}
}

func TestPlanNeverRemovesAnything(t *testing.T) {
	all := running("web", "app", "db", "solo")
	for _, svc := range []string{"web", "app", "db", "solo"} {
		for _, a := range []ServiceAction{ServiceStart, ServiceStop, ServiceRestart, ServiceUpdate} {
			for _, r := range []map[string]bool{running(), all} {
				for _, step := range planServiceAction(chain, r, svc, a).Steps {
					for _, arg := range step {
						if arg == "-v" || arg == "--volumes" || arg == "--remove-orphans" || arg == "down" || arg == "rm" {
							t.Errorf("%s %s: step %v", a, svc, step)
						}
					}
				}
			}
		}
	}
}

func TestPlanSurvivesCycles(t *testing.T) {
	g := compose.Graph{"a": {"b"}, "b": {"a"}}
	p := planServiceAction(g, running(), "a", ServiceStart)
	if got := affected(p); !slices.Equal(got, []string{"a:start:target", "b:start:dependency"}) {
		t.Errorf("affected = %v", got)
	}
}

func TestValidServiceName(t *testing.T) {
	for name, want := range map[string]bool{
		"web": true, "Web_1": true, "my.app": true, "a-b": true, "0": true,
		"": false, "-v": false, ".hidden": false, "a b": false, "a/b": false, "_x": false,
	} {
		if got := ValidServiceName(name); got != want {
			t.Errorf("ValidServiceName(%q) = %v, want %v", name, got, want)
		}
	}
}

// graphCompose is a fakeCompose that also reads dependency graphs.
type graphCompose struct {
	fakeCompose
	graph compose.Graph
	err   error
}

func (g graphCompose) Graph(context.Context, compose.Project) (compose.Graph, error) {
	return g.graph, g.err
}

func TestPlanService(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	containers := []docker.Container{
		{Name: "app-db-1", State: "running", Labels: map[string]string{docker.LabelProject: "app", docker.LabelService: "db"}},
		{Name: "app-app-1", State: "running", Labels: map[string]string{docker.LabelProject: "app", docker.LabelService: "app"}},
		// A one-off container (`docker compose run`) doesn't count as running.
		{Name: "app-web-run-1", State: "running", Labels: map[string]string{docker.LabelProject: "app", docker.LabelService: "web", docker.LabelOneoff: "True"}},
		// Another project's service of the same name doesn't count either.
		{Name: "other-web-1", State: "running", Labels: map[string]string{docker.LabelProject: "other", docker.LabelService: "web"}},
	}
	m, err := New(dir, fakeDocker{containers: containers}, graphCompose{graph: chain})
	if err != nil {
		t.Fatal(err)
	}

	p, plan, err := m.PlanService(context.Background(), "app", "db", ServiceStop)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "app" || !slices.Equal(affected(plan), []string{"db:stop:target", "app:stop:dependent"}) {
		t.Errorf("project = %+v, plan = %v", p, affected(plan))
	}

	for _, tt := range []struct {
		name, service string
		want          error
	}{
		{"Bad", "db", ErrInvalidName},
		{"nope", "db", ErrNotFound},
		{"other", "web", ErrUnmanaged},
		{"app", "-v", ErrInvalidService},
		{"app", "cache", ErrServiceNotFound},
	} {
		if _, _, err := m.PlanService(context.Background(), tt.name, tt.service, ServiceStart); !errors.Is(err, tt.want) {
			t.Errorf("PlanService(%s, %s) = %v, want %v", tt.name, tt.service, err, tt.want)
		}
	}

	// Docker down: the running state is unknown.
	m, _ = New(dir, fakeDocker{err: errors.New("no socket")}, graphCompose{graph: chain})
	if _, _, err := m.PlanService(context.Background(), "app", "db", ServiceStop); !errors.Is(err, ErrDockerUnavailable) {
		t.Errorf("without Docker: %v", err)
	}
	// Compose's message stays out of the error's text shown to clients;
	// the caller only logs it.
	m, _ = New(dir, fakeDocker{}, graphCompose{err: errors.New("yaml: line 3: SECRET=x")})
	if _, _, err := m.PlanService(context.Background(), "app", "db", ServiceStop); err == nil || !strings.Contains(err.Error(), "reading services") {
		t.Errorf("broken Compose file: %v", err)
	}
}
