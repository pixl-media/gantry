package stacks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/compose"
)

// countingGraph counts Graph calls and can fail.
type countingGraph struct {
	fakeCompose
	mu    sync.Mutex
	calls int
	err   error
}

func (c *countingGraph) Graph(context.Context, compose.Project) (compose.Graph, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return compose.Graph{"web": {"db"}, "db": nil}, nil
}

func (c *countingGraph) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestGraphCache(t *testing.T) {
	dir := stacksDir(t, "app")
	g := &countingGraph{}
	m, err := New(dir, fakeDocker{}, g)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	m.cache.now = func() time.Time { return now }
	list := func() []string {
		t.Helper()
		l, err := m.List(context.Background())
		if err != nil || len(l.Stacks) != 1 {
			t.Fatalf("list: %+v, %v", l, err)
		}
		d, err := m.Get(context.Background(), "app")
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, len(d.Services))
		for i, s := range d.Services {
			names[i] = s.Name
		}
		return names
	}

	// Services come from the graph, and repeated lists reuse it.
	if got := list(); !slices.Equal(got, []string{"db", "web"}) {
		t.Errorf("services = %v", got)
	}
	list()
	if n := g.count(); n != 1 {
		t.Errorf("Graph called %d times, want 1", n)
	}

	// A changed Compose file or .env is read again.
	file := filepath.Join(dir, "app", "compose.yaml")
	later := now.Add(time.Second)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	list()
	if n := g.count(); n != 2 {
		t.Errorf("after editing compose.yaml: %d calls, want 2", n)
	}
	if err := os.WriteFile(filepath.Join(dir, "app", ".env"), []byte("COMPOSE_PROFILES=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	list()
	if n := g.count(); n != 3 {
		t.Errorf("after adding .env: %d calls, want 3", n)
	}

	// Entries expire, for files the key doesn't cover.
	now = now.Add(graphTTL)
	list()
	if n := g.count(); n != 4 {
		t.Errorf("after the TTL: %d calls, want 4", n)
	}

	// Forget drops the entry at once.
	m.Forget("app")
	list()
	if n := g.count(); n != 5 {
		t.Errorf("after Forget: %d calls, want 5", n)
	}

	// Plans always read the files again.
	if _, _, err := m.PlanService(context.Background(), "app", "web", ServiceStart); err != nil {
		t.Fatal(err)
	}
	if n := g.count(); n != 6 {
		t.Errorf("PlanService used the cache: %d calls, want 6", n)
	}
}

func TestGraphCacheSkipsErrors(t *testing.T) {
	dir := stacksDir(t, "app")
	g := &countingGraph{err: errors.New("broken")}
	m, err := New(dir, fakeDocker{}, g)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		l, err := m.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(l.Stacks) != 1 {
			t.Fatalf("stacks = %+v", l.Stacks)
		}
	}
	if n := g.count(); n != 2 {
		t.Errorf("Graph called %d times, want 2 (errors aren't cached)", n)
	}
	g.mu.Lock()
	g.err = nil
	g.mu.Unlock()
	if d, err := m.Get(context.Background(), "app"); err != nil || len(d.Services) != 2 {
		t.Errorf("after fixing: %+v, %v", d.Services, err)
	}
}
