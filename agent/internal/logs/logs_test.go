package logs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/docker"
)

var t0 = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

// fakeSource simulates Docker: past log lines per container and, while
// following, live lines sent through a channel per container.
type fakeSource struct {
	mu         sync.Mutex
	containers []docker.Container
	past       map[string][]docker.LogEntry
	live       map[string]chan docker.LogEntry
	follows    []docker.LogOptions // options of follow calls, per call
	followIDs  []string
}

func newFake(cs ...docker.Container) *fakeSource {
	f := &fakeSource{containers: cs, past: map[string][]docker.LogEntry{}, live: map[string]chan docker.LogEntry{}}
	for _, c := range cs {
		f.live[c.ID] = make(chan docker.LogEntry, 16)
	}
	return f
}

func (f *fakeSource) ListComposeContainers(context.Context) ([]docker.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.containers), nil
}

func (f *fakeSource) ContainerTTY(context.Context, string) (bool, error) { return false, nil }

func (f *fakeSource) ContainerLogs(ctx context.Context, id string, _ bool, opts docker.LogOptions, fn func(docker.LogEntry) error) error {
	if !opts.Follow {
		f.mu.Lock()
		past := f.past[id]
		f.mu.Unlock()
		if opts.Tail >= 0 && len(past) > opts.Tail {
			past = past[len(past)-opts.Tail:]
		}
		for _, e := range past {
			if err := fn(e); err != nil {
				return err
			}
		}
		return nil
	}
	f.mu.Lock()
	f.follows = append(f.follows, opts)
	f.followIDs = append(f.followIDs, id)
	ch := f.live[id]
	f.mu.Unlock()
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return nil // container stopped
			}
			if err := fn(e); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (f *fakeSource) setState(id, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.containers {
		if f.containers[i].ID == id {
			f.containers[i].State = state
		}
	}
}

func ctr(id, project, service, state string) docker.Container {
	return docker.Container{
		ID: id, Name: project + "-" + service + "-1", State: state,
		Labels: map[string]string{
			docker.LabelProject: project,
			docker.LabelService: service,
			docker.LabelOneoff:  "False",
		},
	}
}

func entry(sec int, text string) docker.LogEntry {
	return docker.LogEntry{Time: t0.Add(time.Duration(sec) * time.Second), Stream: "stdout", Line: text}
}

// recorder collects emitted lines safely and lets tests wait for them.
type recorder struct {
	mu      sync.Mutex
	lines   []Line
	flushes int
}

func (r *recorder) emit(l Line) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, l)
	return nil
}

func (r *recorder) flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushes++
	return nil
}

func (r *recorder) texts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, l := range r.lines {
		out = append(out, l.Service+":"+l.Text)
	}
	return out
}

func (r *recorder) waitFor(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if got := r.texts(); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d lines, got %v", n, r.texts())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPastLinesMergedByTime(t *testing.T) {
	f := newFake(ctr("a", "app", "web", "running"), ctr("b", "app", "db", "exited"), ctr("x", "other", "web", "running"))
	f.past["a"] = []docker.LogEntry{entry(1, "w1"), entry(3, "w3"), entry(5, "w5")}
	f.past["b"] = []docker.LogEntry{entry(2, "d2"), entry(4, "d4")}
	f.past["x"] = []docker.LogEntry{entry(1, "other project")}

	var r recorder
	err := New(f).Stream(context.Background(), "app", Options{Tail: 2}, r.emit, r.flush)
	if err != nil {
		t.Fatal(err)
	}
	// tail=2 per container, merged by time, other projects excluded.
	if got, want := strings.Join(r.texts(), " "), "db:d2 web:w3 db:d4 web:w5"; got != want {
		t.Errorf("lines = %s, want %s", got, want)
	}
	if r.flushes == 0 {
		t.Error("never flushed")
	}
}

func TestFilters(t *testing.T) {
	oneoff := ctr("o", "app", "web", "running")
	oneoff.Labels[docker.LabelOneoff] = "True"
	f := newFake(ctr("a", "app", "web", "running"), ctr("b", "app", "db", "running"), oneoff)
	f.past["a"] = []docker.LogEntry{entry(1, "w")}
	f.past["b"] = []docker.LogEntry{entry(2, "d")}
	f.past["o"] = []docker.LogEntry{entry(3, "run")}

	for _, tt := range []struct {
		opts Options
		want string
	}{
		{Options{Tail: 10}, "web:w db:d"},
		{Options{Tail: 10, Service: "db"}, "db:d"},
		{Options{Tail: 10, Container: "app-web-1"}, "web:w"},
	} {
		var r recorder
		if err := New(f).Stream(context.Background(), "app", tt.opts, r.emit, r.flush); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(r.texts(), " "); got != tt.want {
			t.Errorf("%+v: lines = %s, want %s", tt.opts, got, tt.want)
		}
	}
}

func TestFollowLiveAndRestart(t *testing.T) {
	f := newFake(ctr("a", "app", "web", "running"))
	f.past["a"] = []docker.LogEntry{entry(1, "old")}
	s := New(f)
	s.poll = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	var r recorder
	done := make(chan error)
	go func() { done <- s.Stream(ctx, "app", Options{Tail: 10, Follow: true}, r.emit, r.flush) }()

	r.waitFor(t, 1)
	f.live["a"] <- entry(2, "live")
	r.waitFor(t, 2)

	// The container stops (its log stream ends) and starts again.
	f.setState("a", "exited")
	f.mu.Lock()
	close(f.live["a"])
	f.live["a"] = make(chan docker.LogEntry, 16)
	f.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	f.setState("a", "running")
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	f.live["a"] <- entry(3, "after restart")
	f.mu.Unlock()

	got := r.waitFor(t, 3)
	if strings.Join(got, " ") != "web:old web:live web:after restart" {
		t.Errorf("lines = %v", got)
	}
	f.mu.Lock()
	follows := slices.Clone(f.follows)
	f.mu.Unlock()
	if len(follows) != 2 {
		t.Fatalf("follow calls = %d, want 2 (initial + after restart)", len(follows))
	}
	// Each follow continues right after the last line sent.
	if want := t0.Add(time.Second + time.Nanosecond); !follows[0].Since.Equal(want) {
		t.Errorf("first follow since = %v, want %v", follows[0].Since, want)
	}
	if want := t0.Add(2*time.Second + time.Nanosecond); !follows[1].Since.Equal(want) {
		t.Errorf("follow after restart since = %v, want %v", follows[1].Since, want)
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Stream after cancel = %v", err)
	}
}

func TestFollowPicksUpNewContainers(t *testing.T) {
	f := newFake(ctr("a", "app", "web", "running"))
	s := New(f)
	s.poll = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var r recorder
	go s.Stream(ctx, "app", Options{Tail: 0, Follow: true}, r.emit, r.flush)

	// A container recreated by `update` gets a new ID.
	time.Sleep(30 * time.Millisecond)
	f.mu.Lock()
	f.containers = append(f.containers, ctr("b", "app", "worker", "running"))
	f.live["b"] = make(chan docker.LogEntry, 16)
	f.live["b"] <- entry(5, "started")
	f.mu.Unlock()

	if got := r.waitFor(t, 1); got[0] != "worker:started" {
		t.Errorf("lines = %v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, id := range f.followIDs {
		switch id {
		case "a": // existing, no past lines with tail=0: only new lines
			if f.follows[i].Tail != 0 {
				t.Errorf("existing container: tail = %d, want 0", f.follows[i].Tail)
			}
		case "b": // new: its whole log
			if f.follows[i].Tail != -1 || !f.follows[i].Since.IsZero() {
				t.Errorf("new container: options = %+v, want whole log", f.follows[i])
			}
		}
	}
}

func TestBacklogLimit(t *testing.T) {
	f := newFake(ctr("a", "app", "web", "running"), ctr("b", "app", "db", "running"), ctr("c", "app", "cache", "running"))
	for _, id := range []string{"a", "b", "c"} {
		for i := range MaxTail {
			f.past[id] = append(f.past[id], entry(i, fmt.Sprint(i)))
		}
	}
	var r recorder
	if err := New(f).Stream(context.Background(), "app", Options{Tail: 100_000}, r.emit, r.flush); err != nil {
		t.Fatal(err)
	}
	if n := len(r.texts()); n != maxBacklog {
		t.Errorf("past lines = %d, want the limit %d", n, maxBacklog)
	}
}

func TestEmitErrorStops(t *testing.T) {
	f := newFake(ctr("a", "app", "web", "running"))
	f.past["a"] = []docker.LogEntry{entry(1, "x")}
	boom := errors.New("client gone")
	err := New(f).Stream(context.Background(), "app", Options{Tail: 10, Follow: true},
		func(Line) error { return boom }, func() error { return nil })
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
}

// Long past lines are limited by size too, keeping the newest.
func TestBacklogByteLimit(t *testing.T) {
	f := newFake(ctr("a", "app", "web", "running"))
	long := strings.Repeat("x", 4<<10)
	n := 2 * maxBacklogBytes / len(long)
	for i := range n {
		f.past["a"] = append(f.past["a"], entry(i, fmt.Sprintf("%d %s", i, long)))
	}
	var r recorder
	if err := New(f).Stream(context.Background(), "app", Options{Tail: MaxTail}, r.emit, r.flush); err != nil {
		t.Fatal(err)
	}
	size := 0
	for _, l := range r.lines {
		size += len(l.Text)
	}
	if size > maxBacklogBytes || len(r.lines) == 0 {
		t.Fatalf("%d lines, %d bytes; limit %d bytes", len(r.lines), size, maxBacklogBytes)
	}
	if last := r.lines[len(r.lines)-1].Text; !strings.HasPrefix(last, fmt.Sprintf("%d ", n-1)) {
		t.Errorf("the newest line is missing; last = %.20q", last)
	}
}
