package live

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/docker"
)

// fakeSource is a Docker event stream driven by the test. Each Events call
// is one connection; fail ends the current one with an error.
type fakeSource struct {
	mu      sync.Mutex
	running int // connections currently open
	opened  int // connections opened in total
	refuse  bool
	events  chan docker.Event
	fail    chan struct{}
}

func newFakeSource() *fakeSource {
	return &fakeSource{events: make(chan docker.Event), fail: make(chan struct{})}
}

func (f *fakeSource) Events(ctx context.Context, connected func(), fn func(docker.Event)) error {
	f.mu.Lock()
	if f.refuse {
		f.mu.Unlock()
		return errors.New("docker is down")
	}
	f.running++
	f.opened++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.running--
		f.mu.Unlock()
	}()
	connected()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.fail:
			return errors.New("stream broke")
		case e := <-f.events:
			fn(e)
		}
	}
}

func (f *fakeSource) state() (running, opened int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running, f.opened
}

// eventually polls cond until it holds or a deadline passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// next waits for the subscription to be signaled and takes the change.
func next(t *testing.T, s *Subscription) Change {
	t.Helper()
	select {
	case <-s.Ready():
		return s.Take()
	case <-time.After(5 * time.Second):
		t.Fatal("no change")
		return Change{}
	}
}

func TestWatcherRunsOnlyWithSubscribers(t *testing.T) {
	src := newFakeSource()
	h := New(src, Options{})

	if running, _ := src.state(); running != 0 {
		t.Fatal("watching Docker without subscribers")
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	h.Subscribe(ctx1)
	h.Subscribe(ctx2)
	eventually(t, "the watcher to start", func() bool { r, _ := src.state(); return r == 1 })

	cancel1()
	time.Sleep(20 * time.Millisecond)
	if running, opened := src.state(); running != 1 || opened != 1 {
		t.Errorf("after one left: running %d, opened %d", running, opened)
	}
	cancel2()
	eventually(t, "the watcher to stop", func() bool { r, _ := src.state(); return r == 0 })
}

func TestChangesReachSubscribers(t *testing.T) {
	src := newFakeSource()
	h := New(src, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := h.Subscribe(ctx)

	// Connecting counts as "everything may have changed".
	if c := next(t, s); !c.All {
		t.Errorf("on connect: %+v", c)
	}
	src.events <- docker.Event{Action: "start", Project: "web"}
	if c := next(t, s); !slices.Equal(c.Stacks, []string{"web"}) || c.All {
		t.Errorf("event: %+v", c)
	}
	h.Notify("db")
	if c := next(t, s); !slices.Equal(c.Stacks, []string{"db"}) {
		t.Errorf("notify: %+v", c)
	}
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	src := newFakeSource()
	h := New(src, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := h.Subscribe(ctx)
	next(t, s) // the connect

	// Many changes while the subscriber doesn't read: nothing blocks, and
	// they arrive merged.
	done := make(chan struct{})
	go func() {
		for i := range 1000 {
			h.Notify([]string{"a", "b", "c"}[i%3])
		}
		src.events <- docker.Event{Action: "die", Project: "d"}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing blocked on a slow subscriber")
	}
	// The Docker event is passed on after the source received it, so collect
	// until everything has arrived.
	seen := map[string]bool{}
	eventually(t, "all changes", func() bool {
		select {
		case <-s.Ready():
			c := s.Take()
			if c.All {
				t.Errorf("unexpected All")
			}
			for _, n := range c.Stacks {
				seen[n] = true
			}
		default:
		}
		return len(seen) == 4
	})
}

func TestReconnectReportsEverything(t *testing.T) {
	src := newFakeSource()
	h := New(src, Options{RetryMin: time.Millisecond, RetryMax: 4 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := h.Subscribe(ctx)
	next(t, s)

	// The stream breaks and Docker is away for a while.
	src.mu.Lock()
	src.refuse = true
	src.mu.Unlock()
	src.fail <- struct{}{}
	if c := next(t, s); !c.All {
		t.Errorf("on break: %+v", c)
	}
	time.Sleep(20 * time.Millisecond) // several failed attempts
	select {
	case <-s.Ready():
		t.Error("a change for every failed attempt")
	default:
	}

	src.mu.Lock()
	src.refuse = false
	src.mu.Unlock()
	if c := next(t, s); !c.All {
		t.Errorf("on reconnect: %+v", c)
	}
	if _, opened := src.state(); opened != 2 {
		t.Errorf("opened %d connections, want 2", opened)
	}
}

func TestWithoutDocker(t *testing.T) {
	h := New(nil, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := h.Subscribe(ctx)
	h.Notify("app")
	if c := next(t, s); !slices.Equal(c.Stacks, []string{"app"}) {
		t.Errorf("notify: %+v", c)
	}
}
