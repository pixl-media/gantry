package operations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/compose"
)

// fakeRunner records calls and runs a behavior per Compose command.
type fakeRunner struct {
	mu       sync.Mutex
	calls    []string // "stack: args"
	behavior map[string]func(ctx context.Context, out io.Writer) (int, error)
}

func (f *fakeRunner) Run(ctx context.Context, p compose.Project, args []string, out io.Writer) (int, error) {
	cmd := strings.Join(args, " ")
	f.mu.Lock()
	f.calls = append(f.calls, p.Name+": "+cmd)
	b := f.behavior[args[0]]
	f.mu.Unlock()
	if b == nil {
		fmt.Fprintf(out, "ran %s\n", cmd)
		return 0, nil
	}
	return b(ctx, out)
}

func (f *fakeRunner) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// blockUntilCanceled behaves like Compose interrupted by Ctrl+C.
func blockUntilCanceled(started chan<- struct{}) func(context.Context, io.Writer) (int, error) {
	return func(ctx context.Context, out io.Writer) (int, error) {
		fmt.Fprintln(out, "working")
		if started != nil {
			close(started)
		}
		<-ctx.Done()
		fmt.Fprintln(out, "interrupted")
		return 130, ctx.Err()
	}
}

var project = compose.Project{Name: "app", Dir: "/stacks/app", File: "/stacks/app/compose.yaml"}

func wait(t *testing.T, op *Operation) Snapshot {
	t.Helper()
	select {
	case <-op.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("operation %s (%s) didn't finish", op.id, op.action)
	}
	return op.Snapshot()
}

func TestStartSucceeds(t *testing.T) {
	r := &fakeRunner{}
	m := NewManager(context.Background(), r, Options{})
	op, err := m.Start(project, ActionStart)
	if err != nil {
		t.Fatal(err)
	}
	s := wait(t, op)
	if s.Status != StatusSucceeded || s.ExitCode == nil || *s.ExitCode != 0 || s.FinishedAt == nil {
		t.Errorf("snapshot = %+v", s)
	}
	out := op.Output()
	if !strings.Contains(out, "$ docker compose up -d --remove-orphans") || !strings.Contains(out, "ran up -d --remove-orphans") {
		t.Errorf("output = %q", out)
	}
	if _, active := m.Active("app"); active {
		t.Error("stack still active after finishing")
	}
}

func TestActionCommands(t *testing.T) {
	want := map[Action][]string{
		ActionStart:   {"app: up -d --remove-orphans"},
		ActionStop:    {"app: stop"},
		ActionRestart: {"app: stop", "app: start"},
		ActionUpdate:  {"app: pull --ignore-buildable", "app: up -d --remove-orphans"},
		ActionDown:    {"app: down --remove-orphans"},
	}
	for action, calls := range want {
		r := &fakeRunner{}
		op, err := NewManager(context.Background(), r, Options{}).Start(project, action)
		if err != nil {
			t.Fatal(err)
		}
		wait(t, op)
		if got := r.callList(); strings.Join(got, "|") != strings.Join(calls, "|") {
			t.Errorf("%s: calls = %v, want %v", action, got, calls)
		}
		for _, c := range r.callList() {
			if strings.Contains(c, "-v") || strings.Contains(c, "--volumes") {
				t.Errorf("%s removes volumes: %s", action, c)
			}
		}
	}
	if _, err := NewManager(context.Background(), &fakeRunner{}, Options{}).Start(project, "explode"); err == nil {
		t.Error("unknown action: want error")
	}
}

func TestUpdateStopsAfterFailedPull(t *testing.T) {
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){
		"pull": func(_ context.Context, out io.Writer) (int, error) {
			fmt.Fprintln(out, "pull access denied")
			return 1, errors.New("docker compose pull: exit status 1")
		},
	}}
	op, _ := NewManager(context.Background(), r, Options{}).Start(project, ActionUpdate)
	s := wait(t, op)
	if s.Status != StatusFailed || *s.ExitCode != 1 || !strings.Contains(s.Error, "exit status 1") {
		t.Errorf("snapshot = %+v", s)
	}
	if calls := r.callList(); len(calls) != 1 {
		t.Errorf("calls = %v, want only pull", calls)
	}
}

func TestBusy(t *testing.T) {
	started := make(chan struct{})
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){"up": blockUntilCanceled(started)}}
	m := NewManager(context.Background(), r, Options{})
	first, _ := m.Start(project, ActionStart)
	<-started

	for _, a := range []Action{ActionStart, ActionRestart, ActionUpdate} {
		_, err := m.Start(project, a)
		var busy *BusyError
		if !errors.As(err, &busy) || busy.Active.ID != first.ID() {
			t.Errorf("%s while busy: err = %v, want BusyError for %s", a, err, first.ID())
		}
	}
	// Other stacks aren't affected.
	other, err := m.Start(compose.Project{Name: "other"}, ActionStop)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, other)

	if a, ok := m.Active("app"); !ok || a.ID != first.ID() {
		t.Errorf("Active = %+v, %v", a, ok)
	}
	m.Cancel(first.ID())
	wait(t, first)
}

func TestStopPreemptsUpdate(t *testing.T) {
	started := make(chan struct{})
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){"pull": blockUntilCanceled(started)}}
	m := NewManager(context.Background(), r, Options{})
	update, _ := m.Start(project, ActionUpdate)
	<-started

	stop, err := m.Start(project, ActionStop)
	if err != nil {
		t.Fatalf("stop while busy: %v, want precedence", err)
	}
	u := wait(t, update)
	s := wait(t, stop)

	if u.Status != StatusCanceled || u.CanceledBy != stop.ID() || !strings.Contains(u.Error, stop.ID()) {
		t.Errorf("update = %+v, want canceled by %s", u, stop.ID())
	}
	if s.Status != StatusSucceeded {
		t.Errorf("stop = %+v", s)
	}
	// stop ran only after update had ended, update never got to "up", and
	// stop ran twice to settle Docker calls of the canceled update.
	if calls := r.callList(); strings.Join(calls, "|") != "app: pull --ignore-buildable|app: stop|app: stop" {
		t.Errorf("calls = %v", calls)
	}
	if !strings.Contains(stop.Output(), "canceling "+update.ID()) {
		t.Errorf("stop output = %q", stop.Output())
	}
}

func TestDelete(t *testing.T) {
	for _, volumes := range []bool{false, true} {
		r := &fakeRunner{}
		removed := false
		op, err := NewManager(context.Background(), r, Options{}).Delete(project, volumes, func() error {
			removed = true
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		s := wait(t, op)
		want := "app: down --remove-orphans"
		if volumes {
			want += " --volumes"
		}
		if calls := r.callList(); strings.Join(calls, "|") != want {
			t.Errorf("volumes=%v: calls = %v, want %s", volumes, calls, want)
		}
		if !removed || s.Status != StatusSucceeded || s.Action != ActionDelete {
			t.Errorf("volumes=%v: removed = %v, snapshot %+v", volumes, removed, s)
		}
		if !strings.Contains(op.Output(), "removing /stacks/app") {
			t.Errorf("output = %q", op.Output())
		}
	}
}

func TestDeleteKeepsFilesIfDownFails(t *testing.T) {
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){
		"down": func(context.Context, io.Writer) (int, error) { return 1, errors.New("exit status 1") },
	}}
	removed := false
	op, _ := NewManager(context.Background(), r, Options{}).Delete(project, false, func() error {
		removed = true
		return nil
	})
	if s := wait(t, op); s.Status != StatusFailed || removed {
		t.Errorf("status %s, removed %v; files must stay when down fails", s.Status, removed)
	}
}

func TestDeletePreempts(t *testing.T) {
	started := make(chan struct{})
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){"pull": blockUntilCanceled(started)}}
	m := NewManager(context.Background(), r, Options{})
	update, _ := m.Start(project, ActionUpdate)
	<-started
	removeErr := errors.New("disk full")
	del, err := m.Delete(project, false, func() error { return removeErr })
	if err != nil {
		t.Fatalf("delete while busy: %v, want precedence", err)
	}
	wait(t, update)
	s := wait(t, del)
	// The remove step comes after the settling down, and its error fails it.
	if calls := r.callList(); strings.Join(calls, "|") != "app: pull --ignore-buildable|app: down --remove-orphans|app: down --remove-orphans" {
		t.Errorf("calls = %v", calls)
	}
	if s.Status != StatusFailed || !strings.Contains(s.Error, "disk full") {
		t.Errorf("delete = %+v", s)
	}
}

func TestDeleteIsNotAnAction(t *testing.T) {
	if ActionDelete.Valid() {
		t.Error("delete must only be started through Delete, with its confirmation")
	}
}

func TestRestartCanceledAfterStop(t *testing.T) {
	// Canceled during its stop step, restart must not go on to start.
	started := make(chan struct{})
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){"stop": blockUntilCanceled(started)}}
	m := NewManager(context.Background(), r, Options{})
	op, _ := m.Start(project, ActionRestart)
	<-started
	m.Cancel(op.ID())
	wait(t, op)
	if calls := r.callList(); strings.Join(calls, "|") != "app: stop" {
		t.Errorf("calls = %v, want no start after cancel", calls)
	}
}

func TestCancel(t *testing.T) {
	started := make(chan struct{})
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){"up": blockUntilCanceled(started)}}
	m := NewManager(context.Background(), r, Options{})
	op, _ := m.Start(project, ActionStart)
	<-started

	if _, err := m.Cancel(op.ID()); err != nil {
		t.Fatal(err)
	}
	s := wait(t, op)
	if s.Status != StatusCanceled || s.Error != "canceled" || s.CanceledBy != "" || *s.ExitCode != 130 {
		t.Errorf("snapshot = %+v", s)
	}
	if _, err := m.Cancel(op.ID()); !errors.Is(err, ErrFinished) {
		t.Errorf("second cancel: err = %v, want ErrFinished", err)
	}
	if _, err := m.Cancel("op_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: err = %v, want ErrNotFound", err)
	}
}

func TestTimeout(t *testing.T) {
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){"up": blockUntilCanceled(nil)}}
	op, _ := NewManager(context.Background(), r, Options{Timeout: 50 * time.Millisecond}).Start(project, ActionStart)
	if s := wait(t, op); s.Status != StatusFailed || !strings.Contains(s.Error, "timed out") {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestShutdownCancels(t *testing.T) {
	ctx, shutdown := context.WithCancel(context.Background())
	started := make(chan struct{})
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){"up": blockUntilCanceled(started)}}
	op, _ := NewManager(ctx, r, Options{}).Start(project, ActionStart)
	<-started
	shutdown()
	if s := wait(t, op); s.Status != StatusCanceled || s.Error != "agent shut down" {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestOutputLimit(t *testing.T) {
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){
		"up": func(_ context.Context, out io.Writer) (int, error) {
			for i := range 100 {
				fmt.Fprintf(out, "line %03d\n", i)
			}
			return 0, nil
		},
	}}
	op, _ := NewManager(context.Background(), r, Options{MaxOutput: 100}).Start(project, ActionStart)
	wait(t, op)
	out := op.Output()
	if !strings.HasPrefix(out, truncatedNote+"\n") || !strings.HasSuffix(out, "line 099\n") || strings.Contains(out, "line 000") {
		t.Errorf("output = %q", out)
	}
	if len(out) > 100+len(truncatedNote)+1 {
		t.Errorf("output is %d bytes, limit 100", len(out))
	}
}

func TestPartialLines(t *testing.T) {
	op := newOperation("op_x", "app", ActionStart, time.Now(), 1<<20, func() {})
	op.Write([]byte("a\nb"))
	op.Write([]byte("c\r\nd"))
	op.finish(time.Now(), 0, nil, context.Background(), time.Minute)
	if got := op.Output(); got != "a\nbc\nd\n" {
		t.Errorf("output = %q", got)
	}
}

func TestFollow(t *testing.T) {
	step := make(chan struct{})
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){
		"up": func(_ context.Context, out io.Writer) (int, error) {
			fmt.Fprintln(out, "one")
			<-step
			fmt.Fprintln(out, "two")
			return 0, nil
		},
	}}
	op, _ := NewManager(context.Background(), r, Options{}).Start(project, ActionStart)

	var mu sync.Mutex
	var lines []string
	result := make(chan Snapshot)
	go func() {
		s, _ := op.Follow(context.Background(), func(l string) error {
			mu.Lock()
			lines = append(lines, l)
			mu.Unlock()
			if l == "one" {
				close(step) // the rest is written while following
			}
			return nil
		})
		result <- s
	}()
	s := <-result
	if s.Status != StatusSucceeded {
		t.Errorf("final = %+v", s)
	}
	mu.Lock()
	got := strings.Join(lines, "|")
	mu.Unlock()
	if got != "$ docker compose up -d --remove-orphans|one|two" {
		t.Errorf("lines = %q", got)
	}

	// Following a finished operation replays everything and returns.
	var replay []string
	op.Follow(context.Background(), func(l string) error { replay = append(replay, l); return nil })
	if strings.Join(replay, "|") != got {
		t.Errorf("replay = %v", replay)
	}
}

func TestFollowStopsWithContext(t *testing.T) {
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){"up": blockUntilCanceled(nil)}}
	m := NewManager(context.Background(), r, Options{})
	op, _ := m.Start(project, ActionStart)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := op.Follow(ctx, func(string) error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	m.Cancel(op.ID())
	wait(t, op)
}

func TestListAndRetention(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	m := NewManager(context.Background(), &fakeRunner{}, Options{Now: clock, MaxKept: 2})

	a, _ := m.Start(project, ActionStart)
	wait(t, a)
	b, _ := m.Start(compose.Project{Name: "other"}, ActionStart)
	wait(t, b)

	list := m.List("")
	if len(list) != 2 || list[0].ID != b.ID() || list[1].ID != a.ID() {
		t.Fatalf("list = %+v, want newest first", list)
	}
	if l := m.List("app"); len(l) != 1 || l[0].ID != a.ID() {
		t.Errorf("filtered = %+v", l)
	}

	// MaxKept 2: a third operation drops the oldest.
	c, _ := m.Start(project, ActionStop)
	wait(t, c)
	m.List("")
	if _, err := m.Get(a.ID()); !errors.Is(err, ErrNotFound) {
		t.Errorf("oldest still kept: %v", err)
	}

	// After the retention period, finished operations are gone.
	now = now.Add(2 * time.Hour)
	if l := m.List(""); len(l) != 0 {
		t.Errorf("after retention: %+v", l)
	}
}

// Output without line breaks is split instead of growing without limit.
func TestLongLinesAreSplit(t *testing.T) {
	op := newOperation("op_x", "app", ActionStart, time.Now(), 1<<20, func() {})
	op.Write(bytes.Repeat([]byte("x"), 3*maxLineBytes+10))
	op.mu.Lock()
	partial, lines := len(op.partial), len(op.lines)
	op.mu.Unlock()
	if partial != 10 || lines != 3 {
		t.Errorf("partial = %d bytes, lines = %d; want 10 and 3", partial, lines)
	}
}

func TestStartService(t *testing.T) {
	r := &fakeRunner{}
	m := NewManager(context.Background(), r, Options{})
	op, err := m.StartService(project, ActionRestart, "db", []string{"db", "app"},
		[][]string{{"stop", "db", "app"}, {"start", "db", "app"}})
	if err != nil {
		t.Fatal(err)
	}
	s := wait(t, op)
	if s.Status != StatusSucceeded || s.Service != "db" || strings.Join(s.Services, ",") != "db,app" || s.Action != ActionRestart {
		t.Errorf("snapshot = %+v", s)
	}
	if got := strings.Join(r.callList(), "|"); got != "app: stop db app|app: start db app" {
		t.Errorf("calls = %s", got)
	}

	for _, cmds := range [][][]string{
		{{"down", "-v"}},
		{{"stop", "db"}, {"rm", "--volumes", "db"}},
		{{"up", "-d", "--remove-orphans", "db"}},
	} {
		if _, err := m.StartService(project, ActionStop, "db", []string{"db"}, cmds); err == nil {
			t.Errorf("%v: want error", cmds)
		}
	}
	if _, err := m.StartService(project, ActionDown, "db", []string{"db"}, [][]string{{"stop", "db"}}); err == nil {
		t.Error("down as a service action: want error")
	}
}

func TestServiceActionNeverPreempts(t *testing.T) {
	started := make(chan struct{}, 1)
	r := &fakeRunner{behavior: map[string]func(context.Context, io.Writer) (int, error){"up": blockUntilCanceled(started)}}
	m := NewManager(context.Background(), r, Options{})
	start, err := m.Start(project, ActionStart)
	if err != nil {
		t.Fatal(err)
	}
	var busy *BusyError
	if _, err := m.StartService(project, ActionStop, "db", []string{"db"}, [][]string{{"stop", "db"}}); !errors.As(err, &busy) || busy.Active.ID != start.ID() {
		t.Errorf("service stop while busy = %v", err)
	}
	if _, err := m.Cancel(start.ID()); err != nil {
		t.Fatal(err)
	}
	wait(t, start)
}
