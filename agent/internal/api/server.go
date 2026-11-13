// Package api exposes the agent's HTTP API. Types and routing are generated
// from api/openapi.yaml into api.gen.go (`make generate`); this package wires
// them to the agent's logic.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/logs"
	"github.com/pixl-media/gantry/agent/internal/operations"
	"github.com/pixl-media/gantry/agent/internal/session"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

// StackService is what the API needs from the stacks package.
type StackService interface {
	List(ctx context.Context) (stacks.List, error)
	Get(ctx context.Context, name string) (stacks.Detail, error)
	Target(ctx context.Context, name string) (compose.Project, error)
	Remove(name string) error
	Dir() string
}

// OperationService is what the API needs from the operations package.
type OperationService interface {
	Start(p compose.Project, a operations.Action) (*operations.Operation, error)
	Delete(p compose.Project, volumes bool, remove func() error) (*operations.Operation, error)
	Get(id string) (*operations.Operation, error)
	Cancel(id string) (operations.Snapshot, error)
	List(stack string) []operations.Snapshot
	Active(stack string) (operations.Snapshot, bool)
}

// LogService is what the API needs from the logs package.
type LogService interface {
	Stream(ctx context.Context, project string, opts logs.Options, emit func(logs.Line) error, flush func() error) error
}

// Config holds what the API needs to serve requests.
type Config struct {
	// Token is the shared secret clients send as "Authorization: Bearer <token>".
	Token      string
	Version    string
	Stacks     StackService
	Operations OperationService
	Logs       LogService
	Files      FileService
	Logger     *slog.Logger // optional
}

// New returns the agent's HTTP handler.
func New(cfg Config) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	auth := &authenticator{token: cfg.Token, signer: session.NewSigner(cfg.Token, 0)}
	h := &handlers{cfg: cfg, auth: auth, limiter: session.NewLimiter(maxFailedLogins, time.Minute)}
	invalid := ErrorCodeInvalidRequest
	strict := NewStrictHandlerWithOptions(h, []StrictMiddlewareFunc{withRequest}, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeJSON(w, http.StatusBadRequest, Error{Error: err.Error(), Code: &invalid})
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			cfg.Logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
			writeJSON(w, http.StatusInternalServerError, Error{Error: err.Error()})
		},
	})
	mux := http.NewServeMux()
	routes := HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseRouter: mux,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeJSON(w, http.StatusBadRequest, Error{Error: err.Error(), Code: &invalid})
		},
	})
	return auth.middleware(routes)
}

// maxFailedLogins is how many wrong tokens a client may send per minute.
const maxFailedLogins = 10

type requestKey struct{}

// withRequest makes the *http.Request available to strict handlers, which
// otherwise only see the decoded request object.
func withRequest(f StrictHandlerFunc, _ string) StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		return f(context.WithValue(ctx, requestKey{}, r), w, r, req)
	}
}

func requestFrom(ctx context.Context) *http.Request {
	r, _ := ctx.Value(requestKey{}).(*http.Request)
	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
