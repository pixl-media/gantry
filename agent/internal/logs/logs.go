// Package logs streams the logs of all containers of a stack, merged like
// `docker compose logs -f`: first the recent past of every container sorted
// by time, then new lines as they arrive.
//
// While following, a watcher re-attaches to containers that are started or
// recreated later, continuing after the last line already sent, so a stack
// restart doesn't interrupt the stream or repeat lines.
package logs

import (
	"context"
	"slices"
	"time"

	"github.com/pixl-media/gantry/agent/internal/docker"
)

// Source is what the streamer needs from Docker; docker.Client implements it.
type Source interface {
	ListComposeContainers(ctx context.Context) ([]docker.Container, error)
	ContainerTTY(ctx context.Context, id string) (bool, error)
	ContainerLogs(ctx context.Context, id string, tty bool, opts docker.LogOptions, fn func(docker.LogEntry) error) error
}

// Line is one line of a stack's logs.
type Line struct {
	Time      time.Time
	Service   string
	Container string
	Stream    string // "stdout" or "stderr"
	Text      string
}

// Options select what to stream.
type Options struct {
	Tail      int    // past lines per container
	Follow    bool   // keep streaming new lines
	Service   string // only this service, if set
	Container string // only this container (name), if set
}

const (
	// MaxTail is the most past lines per container.
	MaxTail = 5000
	// maxBacklog is the most past lines in total.
	maxBacklog = 10_000
)

// Streamer streams stack logs from a Source.
type Streamer struct {
	src  Source
	poll time.Duration
}

// New returns a Streamer that looks for (re)started containers every 2s.
func New(src Source) *Streamer {
	return &Streamer{src: src, poll: 2 * time.Second}
}

type event struct {
	id   string
	line Line
}

// Stream calls emit for every line of the stack's logs and flush whenever
// no more lines are waiting, so writers can batch. Both are called from the
// calling goroutine only. Without Follow it returns after the past lines;
// otherwise it runs until ctx ends or emit or flush fail.
func (s *Streamer) Stream(ctx context.Context, project string, opts Options, emit func(Line) error, flush func() error) error {
	opts.Tail = min(max(opts.Tail, 0), MaxTail)
	containers, err := s.containers(ctx, project, opts)
	if err != nil {
		return err
	}

	// Past lines of every container (also stopped ones), merged by time.
	lastSeen := map[string]time.Time{}
	ttys := map[string]bool{}
	var past []event
	for _, c := range containers {
		tty, err := s.src.ContainerTTY(ctx, c.ID)
		if err != nil {
			continue // removed meanwhile
		}
		ttys[c.ID] = tty
		_ = s.src.ContainerLogs(ctx, c.ID, tty, docker.LogOptions{Tail: opts.Tail}, func(e docker.LogEntry) error {
			past = append(past, event{c.ID, toLine(c, e)})
			if e.Time.After(lastSeen[c.ID]) {
				lastSeen[c.ID] = e.Time
			}
			return nil
		})
	}
	slices.SortStableFunc(past, func(a, b event) int { return a.line.Time.Compare(b.line.Time) })
	if len(past) > maxBacklog {
		past = past[len(past)-maxBacklog:]
	}
	for _, ev := range past {
		if err := emit(ev.line); err != nil {
			return err
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if !opts.Follow {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan event, 256)
	ended := make(chan string, 16)
	attached := map[string]bool{}
	known := map[string]bool{}
	for _, c := range containers {
		known[c.ID] = true
	}

	attach := func(c docker.Container) {
		tty, ok := ttys[c.ID]
		if !ok {
			var err error
			if tty, err = s.src.ContainerTTY(ctx, c.ID); err != nil {
				return
			}
			ttys[c.ID] = tty
		}
		lo := docker.LogOptions{Follow: true, Tail: -1}
		switch {
		case !lastSeen[c.ID].IsZero():
			// Continue right after the last line already sent.
			lo.Since = lastSeen[c.ID].Add(time.Nanosecond)
		case known[c.ID]:
			// Existing container without past lines (e.g. tail=0): only new ones.
			lo.Tail = 0
		default:
			// Created while following: its whole (short) log.
		}
		known[c.ID] = true
		attached[c.ID] = true
		go func() {
			_ = s.src.ContainerLogs(ctx, c.ID, tty, lo, func(e docker.LogEntry) error {
				select {
				case events <- event{c.ID, toLine(c, e)}:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			select {
			case ended <- c.ID:
			case <-ctx.Done():
			}
		}()
	}

	for _, c := range containers {
		if c.State == "running" {
			attach(c)
		}
	}

	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-events:
			if ev.line.Time.After(lastSeen[ev.id]) {
				lastSeen[ev.id] = ev.line.Time
			}
			if err := emit(ev.line); err != nil {
				return err
			}
			if len(events) == 0 {
				if err := flush(); err != nil {
					return err
				}
			}
		case id := <-ended:
			delete(attached, id)
		case <-ticker.C:
			current, err := s.containers(ctx, project, opts)
			if err != nil {
				continue // Docker briefly unreachable; try again
			}
			for _, c := range current {
				if c.State == "running" && !attached[c.ID] {
					attach(c)
				}
			}
		}
	}
}

// containers returns the stack's containers that match the filters, without
// `docker compose run` containers.
func (s *Streamer) containers(ctx context.Context, project string, opts Options) ([]docker.Container, error) {
	all, err := s.src.ListComposeContainers(ctx)
	if err != nil {
		return nil, err
	}
	var out []docker.Container
	for _, c := range all {
		switch {
		case c.Labels[docker.LabelProject] != project,
			c.Labels[docker.LabelOneoff] == "True",
			opts.Service != "" && c.Labels[docker.LabelService] != opts.Service,
			opts.Container != "" && c.Name != opts.Container:
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func toLine(c docker.Container, e docker.LogEntry) Line {
	return Line{
		Time:      e.Time,
		Service:   c.Labels[docker.LabelService],
		Container: c.Name,
		Stream:    e.Stream,
		Text:      e.Line,
	}
}
