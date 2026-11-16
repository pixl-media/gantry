package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pixl-media/gantry/agent/internal/live"
)

// liveWindow collects a burst of changes (a stack starting is several
// container events) into one event.
const liveWindow = 250 * time.Millisecond

// defaultEventsPing keeps idle event streams alive through proxies, which
// often close connections after a minute without data.
const defaultEventsPing = 25 * time.Second

// stackForgetter drops cached data about a stack; stacks.Manager implements
// it.
type stackForgetter interface {
	Forget(name string)
}

// changed reports that the agent itself changed stack's files: cached data
// about it is dropped and clients are told to reload it.
func (h *handlers) changed(stack string) {
	if f, ok := h.cfg.Stacks.(stackForgetter); ok {
		f.Forget(stack)
	}
	if h.cfg.Live != nil {
		h.cfg.Live.Notify(stack)
	}
}

func (h *handlers) StreamEvents(ctx context.Context, _ StreamEventsRequestObject) (StreamEventsResponseObject, error) {
	ping := h.cfg.EventsPing
	if ping <= 0 {
		ping = defaultEventsPing
	}
	return liveStream{ctx: ctx, bind: h.auth.sessionBound, live: h.cfg.Live, ping: ping}, nil
}

// liveStream writes which stacks changed as Server-Sent Events.
type liveStream struct {
	ctx  context.Context
	bind func(context.Context) (context.Context, context.CancelFunc)
	live LiveService // nil: only ready and pings
	ping time.Duration
}

func (s liveStream) VisitStreamEventsResponse(w http.ResponseWriter) error {
	ctx, cancel := s.bind(s.ctx)
	defer cancel()
	// Subscribe before "ready", so nothing between the two is missed.
	var ready <-chan struct{}
	var sub *live.Subscription
	if s.live != nil {
		sub = s.live.Subscribe(ctx)
		ready = sub.Ready()
	}
	rc, err := startEventStream(w)
	if err != nil {
		return err
	}
	send := func(format string, args ...any) bool {
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send("event: ready\ndata: {}\n\n") {
		return nil
	}

	ping := time.NewTicker(s.ping)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil // client gone or session ended
		case <-ping.C:
			if !send(": ping\n\n") {
				return nil
			}
		case <-ready:
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(liveWindow):
			}
			c := sub.Take()
			if !c.All && len(c.Stacks) == 0 {
				continue
			}
			data, err := json.Marshal(StackChanges{Stacks: orEmpty(c.Stacks), All: c.All})
			if err != nil {
				return err
			}
			if !send("event: changed\ndata: %s\n\n", data) {
				return nil
			}
		}
	}
}
