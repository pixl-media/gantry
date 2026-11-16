package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Graph maps every service of a Compose project to the services it needs
// directly, sorted: those in depends_on, `network_mode: service:…` and
// volumes_from, the dependencies Compose itself starts first.
type Graph map[string][]string

// Graph returns the services of project p and their dependencies. Like
// Services it doesn't interpolate variables; of the configuration it keeps
// only service names and dependencies.
func (c CLI) Graph(ctx context.Context, p Project) (Graph, error) {
	out, err := c.output(ctx, p, "config", "--format", "json", "--no-interpolate")
	if err != nil {
		return nil, err
	}
	return parseGraph(out)
}

// parseGraph reads the dependencies from `docker compose config --format
// json` output. Everything else in it is ignored.
func parseGraph(data []byte) (Graph, error) {
	var cfg struct {
		Services map[string]struct {
			DependsOn   map[string]json.RawMessage `json:"depends_on"`
			NetworkMode string                     `json:"network_mode"`
			VolumesFrom []string                   `json:"volumes_from"`
		} `json:"services"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("reading Compose configuration: %w", err)
	}
	g := make(Graph, len(cfg.Services))
	for name, svc := range cfg.Services {
		var deps []string
		for dep := range svc.DependsOn {
			deps = append(deps, dep)
		}
		if dep, ok := strings.CutPrefix(svc.NetworkMode, "service:"); ok {
			deps = append(deps, dep)
		}
		for _, v := range svc.VolumesFrom {
			// "service[:mode]" or "container:name[:mode]"; containers
			// aren't services.
			if !strings.HasPrefix(v, "container:") {
				dep, _, _ := strings.Cut(v, ":")
				deps = append(deps, dep)
			}
		}
		// Only services of this project count (profiles can leave some out).
		deps = slices.DeleteFunc(deps, func(d string) bool {
			_, ok := cfg.Services[d]
			return !ok || d == name
		})
		slices.Sort(deps)
		g[name] = slices.Compact(deps)
	}
	return g, nil
}
