package stacks

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/docker"
)

var (
	// ErrInvalidService means the service name can't be a Compose service.
	ErrInvalidService = errors.New("invalid service name")
	// ErrServiceNotFound means the stack's Compose file has no such service.
	ErrServiceNotFound = errors.New("service not found")
)

var servicePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidServiceName reports whether name can be a Compose service name. It
// can't start with "-", so Compose never reads it as a flag.
func ValidServiceName(name string) bool { return servicePattern.MatchString(name) }

// ServiceAction is something that can be done to one service.
type ServiceAction string

const (
	ServiceStart   ServiceAction = "start"
	ServiceStop    ServiceAction = "stop"
	ServiceRestart ServiceAction = "restart"
	ServiceUpdate  ServiceAction = "update"
)

// Valid reports whether a is a service action.
func (a ServiceAction) Valid() bool {
	switch a {
	case ServiceStart, ServiceStop, ServiceRestart, ServiceUpdate:
		return true
	}
	return false
}

// Effect is what a service action does to one affected service.
type Effect string

const (
	EffectStart    Effect = "start"    // started if it isn't running
	EffectStop     Effect = "stop"     // stopped
	EffectRestart  Effect = "restart"  // stopped and started again
	EffectRecreate Effect = "recreate" // image pulled, container recreated if changed
)

// Reason is why a service is affected.
type Reason string

const (
	ReasonTarget     Reason = "target"     // the chosen service
	ReasonDependency Reason = "dependency" // the chosen service needs it
	ReasonDependent  Reason = "dependent"  // it needs the chosen service
)

// Affected is one service a service action changes.
type Affected struct {
	Service string
	Effect  Effect
	Reason  Reason
}

// ServicePlan is what a service action changes and the Compose commands that
// do it. Every command names its services explicitly, with --no-deps where
// Compose would otherwise pull in more, so exactly Affected is changed.
type ServicePlan struct {
	Service  string
	Action   ServiceAction
	Affected []Affected // the chosen service first
	Steps    [][]string
}

// Services returns the names of all affected services.
func (p ServicePlan) Services() []string {
	names := make([]string, len(p.Affected))
	for i, a := range p.Affected {
		names[i] = a.Service
	}
	return names
}

// GraphLoader reads a Compose project's services and dependencies;
// compose.CLI implements it.
type GraphLoader interface {
	Graph(ctx context.Context, p compose.Project) (compose.Graph, error)
}

// PlanService works out what action on service of the managed stack name
// would change, from the Compose file's dependencies and which services run
// right now. Besides Target's errors it returns ErrInvalidService,
// ErrServiceNotFound and ErrDockerUnavailable.
func (m *Manager) PlanService(ctx context.Context, name, service string, action ServiceAction) (compose.Project, ServicePlan, error) {
	if !action.Valid() {
		return compose.Project{}, ServicePlan{}, fmt.Errorf("unknown service action %q", action)
	}
	p, err := m.Target(ctx, name)
	if err != nil {
		return compose.Project{}, ServicePlan{}, err
	}
	if !ValidServiceName(service) {
		return compose.Project{}, ServicePlan{}, ErrInvalidService
	}
	loader, ok := m.compose.(GraphLoader)
	if !ok {
		return compose.Project{}, ServicePlan{}, errors.New("service dependencies can't be read")
	}
	// Fresh, not cached: the plan must match the files as they are now.
	g, err := m.graph(ctx, loader, p, true)
	if err != nil {
		// Compose's message can quote the file; the caller logs it.
		return compose.Project{}, ServicePlan{}, fmt.Errorf("reading services of %s: %w", name, err)
	}
	if _, ok := g[service]; !ok {
		return compose.Project{}, ServicePlan{}, ErrServiceNotFound
	}
	containers, err := m.docker.ListComposeContainers(ctx)
	if err != nil {
		return compose.Project{}, ServicePlan{}, fmt.Errorf("%w: %v", ErrDockerUnavailable, err)
	}
	running := map[string]bool{}
	for _, c := range containers {
		if c.Labels[docker.LabelProject] == name && c.Labels[docker.LabelOneoff] != "True" && c.State == "running" {
			running[c.Labels[docker.LabelService]] = true
		}
	}
	return p, planServiceAction(g, running, service, action), nil
}

// planServiceAction is the plan for action on service, given the project's
// dependency graph and its running services:
//
//   - start: the service and its stopped dependencies are started.
//   - stop: the service and its running dependents are stopped.
//   - restart: the service and its running dependents are stopped and
//     started again; stopped dependencies are started first.
//   - update: the service's image is pulled and the service recreated if it
//     changed; stopped dependencies are started; running dependents are
//     stopped and brought up again, so they reconnect (and are recreated if
//     they share the service's network or volumes).
func planServiceAction(g compose.Graph, running map[string]bool, service string, action ServiceAction) ServicePlan {
	dependents := compose.Graph{}
	for s, deps := range g {
		for _, d := range deps {
			dependents[d] = append(dependents[d], s)
		}
	}
	stoppedDeps := slices.DeleteFunc(reachable(g, service), func(s string) bool { return running[s] })
	runningDependents := slices.DeleteFunc(reachable(dependents, service), func(s string) bool { return !running[s] })

	plan := ServicePlan{Service: service, Action: action}
	add := func(names []string, e Effect, r Reason) {
		for _, n := range names {
			plan.Affected = append(plan.Affected, Affected{Service: n, Effect: e, Reason: r})
		}
	}
	cmd := func(args []string, names ...[]string) []string {
		for _, n := range names {
			args = append(args, n...)
		}
		return args
	}
	target := []string{service}

	switch action {
	case ServiceStart:
		add(target, EffectStart, ReasonTarget)
		add(stoppedDeps, EffectStart, ReasonDependency)
		plan.Steps = [][]string{cmd([]string{"up", "-d", "--no-deps"}, stoppedDeps, target)}
	case ServiceStop:
		add(target, EffectStop, ReasonTarget)
		add(runningDependents, EffectStop, ReasonDependent)
		plan.Steps = [][]string{cmd([]string{"stop"}, target, runningDependents)}
	case ServiceRestart:
		add(target, EffectRestart, ReasonTarget)
		add(stoppedDeps, EffectStart, ReasonDependency)
		add(runningDependents, EffectRestart, ReasonDependent)
		plan.Steps = [][]string{cmd([]string{"stop"}, target, runningDependents)}
		if len(stoppedDeps) > 0 {
			plan.Steps = append(plan.Steps, cmd([]string{"up", "-d", "--no-deps"}, stoppedDeps))
		}
		plan.Steps = append(plan.Steps, cmd([]string{"start"}, target, runningDependents))
	case ServiceUpdate:
		add(target, EffectRecreate, ReasonTarget)
		add(stoppedDeps, EffectStart, ReasonDependency)
		add(runningDependents, EffectRestart, ReasonDependent)
		plan.Steps = [][]string{
			cmd([]string{"pull", "--ignore-buildable"}, target),
			cmd([]string{"up", "-d", "--no-deps"}, stoppedDeps, target),
		}
		if len(runningDependents) > 0 {
			plan.Steps = append(plan.Steps,
				cmd([]string{"stop"}, runningDependents),
				cmd([]string{"up", "-d", "--no-deps"}, runningDependents))
		}
	}
	return plan
}

// reachable returns every service reachable from start in g, sorted, without
// start itself. Cycles (which Compose rejects anyway) end the walk.
func reachable(g compose.Graph, start string) []string {
	seen := map[string]bool{start: true}
	var out []string
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range g[cur] {
			if !seen[next] {
				seen[next] = true
				out = append(out, next)
				queue = append(queue, next)
			}
		}
	}
	slices.Sort(out)
	return out
}
