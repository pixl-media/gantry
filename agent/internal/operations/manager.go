// Package operations runs stack actions as asynchronous operations and keeps
// their state and output in memory.
//
// At most one operation runs per stack. start, restart and update are
// rejected while another operation runs; stop and down take precedence: they
// cancel the running operation, wait for it to end, and then run.
package operations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pixl-media/gantry/agent/internal/compose"
)

// Action is something that can be done to a stack.
type Action string

const (
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	ActionUpdate  Action = "update"
	ActionDown    Action = "down"
	// ActionDelete is started with Manager.Delete, not Start.
	ActionDelete Action = "delete"
)

// commands are the Compose commands of each action, run in order. None gets
// -v: removing volumes deletes data, which only Delete does, on request.
//
// restart is stop + start rather than `docker compose restart`: Docker runs a
// restart as one job (stop, then start) that it finishes even when Compose is
// interrupted, so a stop issued meanwhile would be undone by the restart's
// start. Separate commands stop after the current step when canceled.
var commands = map[Action][][]string{
	ActionStart:   {{"up", "-d", "--remove-orphans"}},
	ActionStop:    {{"stop"}},
	ActionRestart: {{"stop"}, {"start"}},
	ActionUpdate:  {{"pull", "--ignore-buildable"}, {"up", "-d", "--remove-orphans"}},
	ActionDown:    {{"down", "--remove-orphans"}},
}

// Valid reports whether a is an action that Start runs.
func (a Action) Valid() bool { _, ok := commands[a]; return ok }

// preempts reports whether a cancels a running operation instead of being
// rejected: stop, down and delete work as an emergency brake.
func (a Action) preempts() bool {
	return a == ActionStop || a == ActionDown || a == ActionDelete
}

// step is one part of an operation: a Compose command (args), or a Go
// function (fn) announced by desc.
type step struct {
	args   []string
	fn     func(ctx context.Context) error
	desc   string
	settle bool // a repeated command after canceling another operation
}

func composeSteps(cmds [][]string) []step {
	steps := make([]step, len(cmds))
	for i, c := range cmds {
		steps[i] = step{args: c}
	}
	return steps
}

// withSettle repeats the last Compose command right after it. A Docker call
// the canceled operation had already sent (e.g. a container start) can still
// complete after that command; running stop/down once more settles it. Both
// are idempotent.
func withSettle(steps []step) []step {
	last := -1
	for i, s := range steps {
		if s.args != nil {
			last = i
		}
	}
	if last < 0 {
		return steps
	}
	out := slices.Clone(steps[:last+1])
	out = append(out, step{args: steps[last].args, settle: true})
	return append(out, steps[last+1:]...)
}

var (
	// ErrNotFound means there is no operation with this ID (any more).
	ErrNotFound = errors.New("operation not found")
	// ErrFinished means the operation has already finished.
	ErrFinished = errors.New("operation has already finished")
)

// BusyError is returned when another operation runs on the stack.
type BusyError struct {
	Active Snapshot
}

func (e *BusyError) Error() string {
	return fmt.Sprintf("stack %s is busy with %s (%s)", e.Active.Stack, e.Active.ID, e.Active.Action)
}

// Runner runs a Compose command; compose.CLI implements it.
type Runner interface {
	Run(ctx context.Context, p compose.Project, args []string, out io.Writer) (int, error)
}

// Options tune a Manager. Zero values mean the defaults.
type Options struct {
	Timeout     time.Duration // per operation; default 30 minutes
	Retention   time.Duration // how long finished operations are kept; default 1 hour
	MaxKept     int           // finished operations kept at most; default 200
	MaxOutput   int           // output bytes kept per operation; default 1 MiB
	PreemptWait time.Duration // how long stop/down wait for a canceled operation; default 30 seconds
	Now         func() time.Time
}

// Manager starts and tracks operations.
type Manager struct {
	runner Runner
	base   context.Context
	opts   Options

	mu     sync.Mutex
	ops    []*Operation          // in start order
	byID   map[string]*Operation //
	active map[string]*Operation // by stack
}

// NewManager returns a Manager. Cancelling ctx cancels all running
// operations, e.g. when the agent shuts down.
func NewManager(ctx context.Context, r Runner, opts Options) *Manager {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Minute
	}
	if opts.Retention == 0 {
		opts.Retention = time.Hour
	}
	if opts.MaxKept == 0 {
		opts.MaxKept = 200
	}
	if opts.MaxOutput == 0 {
		opts.MaxOutput = 1 << 20
	}
	if opts.PreemptWait == 0 {
		opts.PreemptWait = 30 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Manager{
		runner: r,
		base:   ctx,
		opts:   opts,
		byID:   map[string]*Operation{},
		active: map[string]*Operation{},
	}
}

// Start runs action on project p in the background and returns the new
// operation. It returns a *BusyError if another operation runs on the stack
// and action doesn't take precedence.
func (m *Manager) Start(p compose.Project, action Action) (*Operation, error) {
	if !action.Valid() {
		return nil, fmt.Errorf("unknown action %q", action)
	}
	return m.start(p, action, composeSteps(commands[action]), serviceInfo{})
}

// StartService runs the Compose commands cmds of a service action on project
// p: action on service, changing services (all affected ones). The commands
// come from stacks.ServicePlan. A service action never takes precedence: it
// returns a *BusyError while any operation runs on the stack, while stop,
// down and delete still cancel a running service action.
func (m *Manager) StartService(p compose.Project, action Action, service string, services []string, cmds [][]string) (*Operation, error) {
	switch action {
	case ActionStart, ActionStop, ActionRestart, ActionUpdate:
	default:
		return nil, fmt.Errorf("unknown service action %q", action)
	}
	for _, c := range cmds {
		for _, arg := range c {
			// Never remove volumes or other services' containers here.
			if arg == "-v" || arg == "--volumes" || arg == "--remove-orphans" {
				return nil, fmt.Errorf("service action must not use %s", arg)
			}
		}
	}
	return m.start(p, action, composeSteps(cmds), serviceInfo{name: service, services: slices.Clone(services)})
}

// serviceInfo names the service a service action is about; empty for stack
// actions.
type serviceInfo struct {
	name     string
	services []string
}

// Delete runs `down --remove-orphans` on project p, with --volumes if
// volumes is set, and then remove, which deletes the stack's files. Like stop
// and down it takes precedence over a running operation. remove only runs if
// down succeeded.
func (m *Manager) Delete(p compose.Project, volumes bool, remove func() error) (*Operation, error) {
	down := []string{"down", "--remove-orphans"}
	if volumes {
		down = append(down, "--volumes")
	}
	steps := []step{
		{args: down},
		{desc: "removing " + p.Dir, fn: func(context.Context) error { return remove() }},
	}
	return m.start(p, ActionDelete, steps, serviceInfo{})
}

func (m *Manager) start(p compose.Project, action Action, steps []step, svc serviceInfo) (*Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	prev := m.active[p.Name]
	if prev != nil && (svc.name != "" || !action.preempts()) {
		return nil, &BusyError{Active: prev.Snapshot()}
	}
	ctx, cancel := context.WithTimeout(m.base, m.opts.Timeout)
	op := newOperation(newID(), p.Name, action, m.opts.Now(), m.opts.MaxOutput, cancel)
	op.service, op.services = svc.name, svc.services
	m.pruneLocked()
	m.ops = append(m.ops, op)
	m.byID[op.id] = op
	// The new operation is active right away, so nothing else can start
	// while it waits for prev to end.
	m.active[p.Name] = op

	if prev != nil {
		steps = withSettle(steps)
	}
	go m.run(ctx, op, p, prev, steps)
	return op, nil
}

func (m *Manager) run(ctx context.Context, op *Operation, p compose.Project, prev *Operation, steps []step) {
	defer op.cancel()
	if prev != nil {
		op.logf("canceling %s (%s) first", prev.id, prev.action)
		_ = prev.requestCancel(fmt.Sprintf("canceled by %s (%s)", op.id, op.action), op.id)
		// Wait for prev even if op itself gets canceled meanwhile, so that
		// two operations never run Compose on the same stack at once.
		select {
		case <-prev.done:
		case <-time.After(m.opts.PreemptWait):
			op.logf("warning: %s didn't stop within %s", prev.id, m.opts.PreemptWait)
		}
	}

	code, err := -1, error(nil)
	for _, s := range steps {
		if ctx.Err() != nil {
			break
		}
		switch {
		case s.fn != nil:
			op.logf("%s", s.desc)
			if err = s.fn(ctx); err != nil {
				op.logf("failed: %v", err)
			}
		case s.settle:
			op.logf("$ docker compose %s (again, to settle the canceled operation)", strings.Join(s.args, " "))
			code, err = m.runner.Run(ctx, p, s.args, op)
		default:
			op.logf("$ docker compose %s", strings.Join(s.args, " "))
			code, err = m.runner.Run(ctx, p, s.args, op)
		}
		if err != nil {
			break
		}
	}

	m.mu.Lock()
	if m.active[op.stack] == op {
		delete(m.active, op.stack)
	}
	op.finish(m.opts.Now(), code, err, ctx, m.opts.Timeout)
	m.mu.Unlock()
}

// Get returns the operation with id.
func (m *Manager) Get(id string) (*Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	return op, nil
}

// Cancel cancels the running operation with id.
func (m *Manager) Cancel(id string) (Snapshot, error) {
	op, err := m.Get(id)
	if err != nil {
		return Snapshot{}, err
	}
	if err := op.requestCancel("canceled", ""); err != nil {
		return Snapshot{}, err
	}
	return op.Snapshot(), nil
}

// List returns the kept operations, newest first. If stack isn't empty, only
// that stack's operations are returned.
func (m *Manager) List(stack string) []Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	out := []Snapshot{}
	for _, op := range slices.Backward(m.ops) {
		if stack == "" || op.stack == stack {
			out = append(out, op.Snapshot())
		}
	}
	return out
}

// Active returns the operation running on stack, if any.
func (m *Manager) Active(stack string) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.active[stack]
	if !ok {
		return Snapshot{}, false
	}
	return op.Snapshot(), true
}

// pruneLocked drops finished operations older than the retention period and
// the oldest finished ones beyond MaxKept. Running operations are kept.
func (m *Manager) pruneLocked() {
	cutoff := m.opts.Now().Add(-m.opts.Retention)
	finished := 0
	for _, op := range m.ops {
		if op.Snapshot().Status != StatusRunning {
			finished++
		}
	}
	kept := m.ops[:0]
	for _, op := range m.ops {
		s := op.Snapshot()
		drop := s.Status != StatusRunning && (s.FinishedAt.Before(cutoff) || finished > m.opts.MaxKept)
		if drop {
			finished--
			delete(m.byID, op.id)
			continue
		}
		kept = append(kept, op)
	}
	clear(m.ops[len(kept):])
	m.ops = kept
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "op_" + hex.EncodeToString(b)
}
