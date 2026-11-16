// Package live tells clients which stacks changed, so they can reload them
// instead of polling. Changes come from Docker's container events and from
// the agent itself (operations, file changes).
//
// A subscriber never blocks the hub: its pending changes are merged into one
// set until it takes them, so a slow client costs memory for one set of
// stack names at most.
package live

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/pixl-media/gantry/agent/internal/docker"
)

// Change names the stacks that changed. All means anything may have changed
// (e.g. events were missed), so clients should reload everything.
type Change struct {
	Stacks []string
	All    bool
}

// Source is Docker's event stream; docker.Client implements it.
type Source interface {
	Events(ctx context.Context, connected func(), fn func(docker.Event)) error
}

// Options tune a Hub. Zero values mean the defaults.
type Options struct {
	RetryMin time.Duration // first wait after the event stream broke; default 1s
	RetryMax time.Duration // longest wait between attempts; default 30s
	Logger   *slog.Logger
}

// Hub collects changes and hands them to subscribers. It follows Docker's
// events only while someone subscribes.
type Hub struct {
	src  Source // nil: only the agent's own changes
	opts Options

	mu    sync.Mutex
	subs  map[*Subscription]struct{}
	watch context.CancelFunc // stops the Docker watcher; nil if not running
}

// New returns a hub fed by src, which may be nil.
func New(src Source, opts Options) *Hub {
	if opts.RetryMin <= 0 {
		opts.RetryMin = time.Second
	}
	if opts.RetryMax < opts.RetryMin {
		opts.RetryMax = max(30*time.Second, opts.RetryMin)
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Hub{src: src, opts: opts, subs: map[*Subscription]struct{}{}}
}

// Subscription receives changes until its context ends.
type Subscription struct {
	ready chan struct{} // gets a value when changes are pending

	mu     sync.Mutex
	all    bool
	stacks map[string]bool
}

// Ready is signaled when changes are pending; collect them with Take.
func (s *Subscription) Ready() <-chan struct{} { return s.ready }

// Take returns the pending changes and clears them.
func (s *Subscription) Take() Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := Change{All: s.all}
	if !s.all {
		for name := range s.stacks {
			c.Stacks = append(c.Stacks, name)
		}
		slices.Sort(c.Stacks)
	}
	s.all = false
	clear(s.stacks)
	return c
}

func (s *Subscription) add(c Change) {
	s.mu.Lock()
	if c.All {
		s.all = true
	}
	for _, name := range c.Stacks {
		s.stacks[name] = true
	}
	s.mu.Unlock()
	select {
	case s.ready <- struct{}{}:
	default: // already signaled
	}
}

// Subscribe returns a subscription that ends with ctx. The first subscriber
// starts following Docker's events, the last one to leave stops it.
func (h *Hub) Subscribe(ctx context.Context) *Subscription {
	s := &Subscription{ready: make(chan struct{}, 1), stacks: map[string]bool{}}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	if h.watch == nil && h.src != nil {
		wctx, cancel := context.WithCancel(context.Background())
		h.watch = cancel
		go h.follow(wctx)
	}
	h.mu.Unlock()

	context.AfterFunc(ctx, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subs, s)
		if len(h.subs) == 0 && h.watch != nil {
			h.watch()
			h.watch = nil
		}
	})
	return s
}

// Notify reports that stack changed, for changes the agent makes itself.
func (h *Hub) Notify(stack string) {
	h.publish(Change{Stacks: []string{stack}})
}

func (h *Hub) publish(c Change) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		s.add(c)
	}
}

// follow passes Docker's events on until ctx ends, reconnecting with a
// growing wait when the stream breaks. Every (re)connect is reported as
// Change{All: true}: events may have been missed before it.
func (h *Hub) follow(ctx context.Context) {
	wait := h.opts.RetryMin
	failing := false
	for {
		err := h.src.Events(ctx, func() {
			if failing {
				h.opts.Logger.Info("docker events: reconnected")
			}
			failing = false
			wait = h.opts.RetryMin
			h.publish(Change{All: true})
		}, func(e docker.Event) {
			h.publish(Change{Stacks: []string{e.Project}})
		})
		if ctx.Err() != nil {
			return
		}
		if !failing {
			// Once per outage; Docker being away also changes what
			// clients show.
			h.opts.Logger.Warn("docker events: stream broke, retrying", "err", err)
			failing = true
			h.publish(Change{All: true})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, h.opts.RetryMax)
	}
}
