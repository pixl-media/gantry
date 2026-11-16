package operations

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Status is the state of an operation.
type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

// truncatedNote is the first line of Output when earlier output was dropped.
const truncatedNote = "[earlier output truncated]"

// Snapshot is a point-in-time copy of an operation's state.
type Snapshot struct {
	ID         string
	Stack      string
	Action     Action
	Status     Status
	StartedAt  time.Time
	FinishedAt *time.Time
	ExitCode   *int
	Error      string
	CanceledBy string
	Service    string   // for a service action, the chosen service
	Services   []string // for a service action, every affected service
}

// Operation is one action running or run on a stack. Its output is written
// by the runner through Write and can be followed live.
type Operation struct {
	id        string
	stack     string
	action    Action
	service   string   // set for service actions
	services  []string // set for service actions; never changed
	startedAt time.Time
	maxOutput int
	cancel    context.CancelFunc
	done      chan struct{} // closed when finished

	mu           sync.Mutex
	status       Status
	finishedAt   time.Time
	exitCode     *int
	errMsg       string
	cancelReason string // set when canceled on purpose
	canceledBy   string
	lines        []string
	size         int // bytes in lines, including newlines
	dropped      int // lines dropped from the start
	partial      []byte
	changed      chan struct{} // closed and replaced on every change
}

func newOperation(id, stack string, action Action, now time.Time, maxOutput int, cancel context.CancelFunc) *Operation {
	return &Operation{
		id:        id,
		stack:     stack,
		action:    action,
		startedAt: now,
		maxOutput: maxOutput,
		cancel:    cancel,
		done:      make(chan struct{}),
		status:    StatusRunning,
		changed:   make(chan struct{}),
	}
}

// ID returns the operation's ID.
func (o *Operation) ID() string { return o.id }

// Snapshot returns the current state.
func (o *Operation) Snapshot() Snapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.snapshotLocked()
}

func (o *Operation) snapshotLocked() Snapshot {
	s := Snapshot{
		ID:         o.id,
		Stack:      o.stack,
		Action:     o.action,
		Status:     o.status,
		StartedAt:  o.startedAt,
		ExitCode:   o.exitCode,
		Error:      o.errMsg,
		CanceledBy: o.canceledBy,
		Service:    o.service,
		Services:   slices.Clone(o.services),
	}
	if o.status != StatusRunning {
		t := o.finishedAt
		s.FinishedAt = &t
	}
	return s
}

// maxLineBytes is the longest output line kept in one piece.
const maxLineBytes = 64 << 10

// Output returns the output so far, limited to the last maxOutput bytes.
func (o *Operation) Output() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var b strings.Builder
	if o.dropped > 0 {
		b.WriteString(truncatedNote + "\n")
	}
	for _, l := range o.lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// Write appends runner output, split into lines. It never fails.
func (o *Operation) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.partial = append(o.partial, p...)
	for {
		i := bytes.IndexByte(o.partial, '\n')
		if i < 0 {
			break
		}
		o.appendLineLocked(string(bytes.TrimRight(o.partial[:i], "\r")))
		o.partial = o.partial[i+1:]
	}
	// Output without line breaks is split, so it can't grow without limit.
	for len(o.partial) >= maxLineBytes {
		o.appendLineLocked(string(o.partial[:maxLineBytes]))
		o.partial = o.partial[maxLineBytes:]
	}
	o.notifyLocked()
	return len(p), nil
}

// logf adds a line of gantry's own output.
func (o *Operation) logf(format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.appendLineLocked(fmt.Sprintf(format, args...))
	o.notifyLocked()
}

func (o *Operation) appendLineLocked(line string) {
	o.lines = append(o.lines, line)
	o.size += len(line) + 1
	for o.size > o.maxOutput && len(o.lines) > 1 {
		o.size -= len(o.lines[0]) + 1
		o.lines = o.lines[1:]
		o.dropped++
	}
	// Re-slicing keeps the old backing array alive; compact now and then.
	if cap(o.lines) > 64 && len(o.lines) < cap(o.lines)/4 {
		o.lines = slices.Clone(o.lines)
	}
}

func (o *Operation) notifyLocked() {
	close(o.changed)
	o.changed = make(chan struct{})
}

// requestCancel cancels a running operation. reason ends up in Error.
func (o *Operation) requestCancel(reason, by string) error {
	o.mu.Lock()
	if o.status != StatusRunning {
		o.mu.Unlock()
		return ErrFinished
	}
	if o.cancelReason == "" {
		o.cancelReason = reason
		o.canceledBy = by
	}
	o.mu.Unlock()
	o.cancel()
	return nil
}

// finish records the result of the last command. ctx is the operation's
// context, used to tell cancellation and timeouts from failures.
func (o *Operation) finish(now time.Time, code int, err error, ctx context.Context, timeout time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.partial) > 0 {
		o.appendLineLocked(string(o.partial))
		o.partial = nil
	}
	switch {
	case o.cancelReason != "":
		o.status = StatusCanceled
		o.errMsg = o.cancelReason
	case ctx.Err() == context.DeadlineExceeded:
		o.status = StatusFailed
		o.errMsg = fmt.Sprintf("timed out after %s", timeout)
	case ctx.Err() != nil:
		o.status = StatusCanceled
		o.errMsg = "agent shut down"
	case err != nil:
		o.status = StatusFailed
		o.errMsg = err.Error()
	default:
		o.status = StatusSucceeded
	}
	if code >= 0 {
		o.exitCode = &code
	}
	o.finishedAt = now
	o.notifyLocked()
	close(o.done)
}

// Follow calls emit for every output line, from the first one still kept,
// then for new lines as they are written, and returns the final state once
// the operation has finished. It stops early if ctx ends or emit fails.
func (o *Operation) Follow(ctx context.Context, emit func(line string) error) (Snapshot, error) {
	next := 0 // absolute line number, counting dropped lines
	for {
		o.mu.Lock()
		if next < o.dropped {
			next = o.dropped
		}
		lines := slices.Clone(o.lines[next-o.dropped:])
		next += len(lines)
		finished := o.status != StatusRunning
		snap := o.snapshotLocked()
		changed := o.changed
		o.mu.Unlock()

		for _, l := range lines {
			if err := emit(l); err != nil {
				return Snapshot{}, err
			}
		}
		if finished {
			return snap, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
}
