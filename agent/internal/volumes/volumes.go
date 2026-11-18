// Package volumes reports the Docker volumes of Compose projects with their
// sizes. Docker measures them on request, which can take a while, so a
// measurement is kept for a minute and shared by concurrent requests.
package volumes

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/pixl-media/gantry/agent/internal/docker"
)

// TTL is how long a measurement is used.
const TTL = time.Minute

// Source measures volumes; docker.Client implements it.
type Source interface {
	VolumeUsage(ctx context.Context) ([]docker.Volume, error)
}

// Volume is a Compose project's volume.
type Volume struct {
	Name       string
	Stack      string // the Compose project
	Driver     string
	Size       int64 // bytes; -1 if unknown
	Containers int64 // containers using it; -1 if unknown
	CreatedAt  time.Time
}

// Usage is one measurement.
type Usage struct {
	Volumes    []Volume // sorted by stack and name
	MeasuredAt time.Time
}

// Manager keeps the latest measurement.
type Manager struct {
	src Source
	now func() time.Time

	mu      sync.Mutex
	last    Usage
	have    bool
	running *measurement
}

type measurement struct {
	done  chan struct{}
	usage Usage
	err   error
}

// New returns a Manager that measures through src.
func New(src Source) *Manager {
	return &Manager{src: src, now: time.Now}
}

// List returns the volumes of all Compose projects, measured at most TTL
// ago. Concurrent calls share one measurement, which isn't canceled when a
// caller gives up; errors aren't kept.
func (m *Manager) List(ctx context.Context) (Usage, error) {
	m.mu.Lock()
	if m.have && m.now().Sub(m.last.MeasuredAt) < TTL {
		defer m.mu.Unlock()
		return m.last, nil
	}
	run := m.running
	if run == nil {
		run = &measurement{done: make(chan struct{})}
		m.running = run
		go m.measure(run)
	}
	m.mu.Unlock()

	select {
	case <-run.done:
		return run.usage, run.err
	case <-ctx.Done():
		return Usage{}, ctx.Err()
	}
}

func (m *Manager) measure(run *measurement) {
	// Docker's client limits how long this takes.
	all, err := m.src.VolumeUsage(context.Background())
	var usage Usage
	if err == nil {
		usage = Usage{Volumes: compose(all), MeasuredAt: m.now()}
	}
	m.mu.Lock()
	if err == nil {
		m.last, m.have = usage, true
	}
	m.running = nil
	m.mu.Unlock()
	run.usage, run.err = usage, err
	close(run.done)
}

// compose keeps the volumes that belong to a Compose project.
func compose(all []docker.Volume) []Volume {
	out := []Volume{}
	for _, v := range all {
		stack := v.Labels[docker.LabelProject]
		if stack == "" {
			continue
		}
		out = append(out, Volume{
			Name: v.Name, Stack: stack, Driver: v.Driver,
			Size: v.Size, Containers: v.RefCount, CreatedAt: v.CreatedAt,
		})
	}
	slices.SortFunc(out, func(a, b Volume) int {
		return cmp.Or(cmp.Compare(a.Stack, b.Stack), cmp.Compare(a.Name, b.Name))
	})
	return out
}
