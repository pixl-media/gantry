package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/pixl-media/gantry/agent/internal/operations"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

func (h *handlers) RunStackAction(ctx context.Context, req RunStackActionRequestObject) (RunStackActionResponseObject, error) {
	action := operations.Action(req.Action)
	if !action.Valid() {
		return RunStackAction400JSONResponse(apiError(fmt.Errorf("unknown action %q", req.Action), ErrorCodeInvalidAction)), nil
	}
	target, err := h.cfg.Stacks.Target(ctx, req.Name)
	switch {
	case errors.Is(err, stacks.ErrInvalidName):
		return RunStackAction400JSONResponse(apiError(err, ErrorCodeInvalidName)), nil
	case errors.Is(err, stacks.ErrNotFound):
		return RunStackAction404JSONResponse{NotFoundJSONResponse(apiError(err, ErrorCodeNotFound))}, nil
	case errors.Is(err, stacks.ErrUnmanaged):
		return RunStackAction409JSONResponse(apiError(err, ErrorCodeStackUnmanaged)), nil
	case err != nil:
		return nil, err
	}

	op, err := h.cfg.Operations.Start(target, action)
	var busy *operations.BusyError
	if errors.As(err, &busy) {
		resp := apiError(err, ErrorCodeStackBusy)
		resp.Operation = &busy.Active.ID
		return RunStackAction409JSONResponse(resp), nil
	}
	if err != nil {
		return nil, err
	}
	location := "/api/v1/operations/" + op.ID()
	return RunStackAction202JSONResponse{
		Body:    toOperation(op.Snapshot()),
		Headers: RunStackAction202ResponseHeaders{Location: &location},
	}, nil
}

func (h *handlers) ListOperations(_ context.Context, req ListOperationsRequestObject) (ListOperationsResponseObject, error) {
	stack := ""
	if req.Params.Stack != nil {
		stack = *req.Params.Stack
	}
	resp := ListOperations200JSONResponse{Operations: []Operation{}}
	for _, s := range h.cfg.Operations.List(stack) {
		resp.Operations = append(resp.Operations, toOperation(s))
	}
	return resp, nil
}

func (h *handlers) GetOperation(_ context.Context, req GetOperationRequestObject) (GetOperationResponseObject, error) {
	op, err := h.cfg.Operations.Get(req.Id)
	if err != nil {
		return GetOperation404JSONResponse{NotFoundJSONResponse(apiError(err, ErrorCodeNotFound))}, nil
	}
	o := toOperation(op.Snapshot())
	return GetOperation200JSONResponse{
		Id:         o.Id,
		Stack:      o.Stack,
		Action:     o.Action,
		Status:     o.Status,
		StartedAt:  o.StartedAt,
		FinishedAt: o.FinishedAt,
		ExitCode:   o.ExitCode,
		Error:      o.Error,
		CanceledBy: o.CanceledBy,
		Output:     op.Output(),
	}, nil
}

func (h *handlers) CancelOperation(_ context.Context, req CancelOperationRequestObject) (CancelOperationResponseObject, error) {
	s, err := h.cfg.Operations.Cancel(req.Id)
	switch {
	case errors.Is(err, operations.ErrNotFound):
		return CancelOperation404JSONResponse{NotFoundJSONResponse(apiError(err, ErrorCodeNotFound))}, nil
	case errors.Is(err, operations.ErrFinished):
		return CancelOperation409JSONResponse(apiError(err, ErrorCodeOperationFinished)), nil
	case err != nil:
		return nil, err
	}
	return CancelOperation202JSONResponse(toOperation(s)), nil
}

func (h *handlers) StreamOperationEvents(ctx context.Context, req StreamOperationEventsRequestObject) (StreamOperationEventsResponseObject, error) {
	op, err := h.cfg.Operations.Get(req.Id)
	if err != nil {
		return StreamOperationEvents404JSONResponse{NotFoundJSONResponse(apiError(err, ErrorCodeNotFound))}, nil
	}
	return eventStream{ctx: ctx, op: op}, nil
}

// eventStream writes an operation's output as Server-Sent Events. It
// implements the generated response interface itself, because the generated
// io.Reader response can't flush after each event.
type eventStream struct {
	ctx context.Context
	op  *operations.Operation
}

func (s eventStream) VisitStreamOperationEventsResponse(w http.ResponseWriter) error {
	rc, err := startEventStream(w)
	if err != nil {
		return err
	}

	final, err := s.op.Follow(s.ctx, func(line string) error {
		// A CR would end the SSE data line early.
		line = strings.ReplaceAll(line, "\r", "")
		if _, err := fmt.Fprintf(w, "event: output\ndata: %s\n\n", line); err != nil {
			return err
		}
		return rc.Flush()
	})
	if err != nil {
		return nil // the client went away; nothing left to send
	}
	data, err := json.Marshal(toOperation(final))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: done\ndata: %s\n\n", data); err != nil {
		return nil
	}
	return rc.Flush()
}

// startEventStream sends the headers of a Server-Sent Events response and
// returns a controller to flush each event with.
func startEventStream(w http.ResponseWriter) (*http.ResponseController, error) {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store") // logs and output may hold secrets
	h.Set("X-Accel-Buffering", "no")   // tell reverse proxies not to buffer
	w.WriteHeader(http.StatusOK)
	return rc, rc.Flush()
}

func toOperation(s operations.Snapshot) Operation {
	o := Operation{
		Id:         s.ID,
		Stack:      s.Stack,
		Action:     OperationAction(s.Action),
		Status:     OperationStatus(s.Status),
		StartedAt:  s.StartedAt,
		FinishedAt: s.FinishedAt,
		ExitCode:   s.ExitCode,
	}
	if s.Error != "" {
		o.Error = &s.Error
	}
	if s.CanceledBy != "" {
		o.CanceledBy = &s.CanceledBy
	}
	return o
}
