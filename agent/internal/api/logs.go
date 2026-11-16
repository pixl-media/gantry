package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/pixl-media/gantry/agent/internal/logs"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

// defaultLogTail is how many past lines per container the logs start with.
const defaultLogTail = 200

func (h *handlers) StreamStackLogs(ctx context.Context, req StreamStackLogsRequestObject) (StreamStackLogsResponseObject, error) {
	// Unmanaged stacks are fine: reading logs changes nothing.
	_, err := h.cfg.Stacks.Target(ctx, req.Name)
	switch {
	case errors.Is(err, stacks.ErrInvalidName):
		return StreamStackLogs400JSONResponse{InvalidNameJSONResponse(apiError(err, ErrorCodeInvalidName))}, nil
	case errors.Is(err, stacks.ErrNotFound):
		return StreamStackLogs404JSONResponse{NotFoundJSONResponse(apiError(err, ErrorCodeNotFound))}, nil
	case err != nil && !errors.Is(err, stacks.ErrUnmanaged):
		return nil, err
	}

	opts := logs.Options{Tail: defaultLogTail, Follow: true}
	p := req.Params
	if p.Tail != nil {
		opts.Tail = *p.Tail
	}
	if p.Follow != nil {
		opts.Follow = *p.Follow
	}
	if p.Service != nil {
		opts.Service = *p.Service
	}
	if p.Container != nil {
		opts.Container = *p.Container
	}
	return logStream{ctx: ctx, bind: h.auth.sessionBound, logs: h.cfg.Logs, stack: req.Name, opts: opts, logger: h.cfg.Logger}, nil
}

// logStream writes a stack's logs as Server-Sent Events.
type logStream struct {
	ctx    context.Context
	bind   func(context.Context) (context.Context, context.CancelFunc)
	logs   LogService
	stack  string
	opts   logs.Options
	logger *slog.Logger
}

func (s logStream) VisitStreamStackLogsResponse(w http.ResponseWriter) error {
	ctx, cancel := s.bind(s.ctx)
	defer cancel()
	s.ctx = ctx
	rc, err := startEventStream(w)
	if err != nil {
		return err
	}
	err = s.logs.Stream(s.ctx, s.stack, s.opts, func(l logs.Line) error {
		data, err := json.Marshal(LogLine{
			Time:      l.Time,
			Service:   l.Service,
			Container: l.Container,
			Stream:    LogLineStream(l.Stream),
			Line:      l.Text,
		})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
		return err
	}, rc.Flush)

	switch {
	case err == nil: // not following: the past lines are complete
		fmt.Fprint(w, "event: end\ndata: {}\n\n")
	case s.ctx.Err() != nil:
		return nil // the client went away
	default:
		s.logger.Warn("log stream failed", "stack", s.stack, "err", err)
		data, _ := json.Marshal(Error{Error: err.Error()})
		// Not "error": EventSource reserves that name for connection errors.
		fmt.Fprintf(w, "event: failed\ndata: %s\n\n", data)
	}
	_ = rc.Flush()
	return nil
}
