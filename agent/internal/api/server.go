// Package api exposes the agent's HTTP API. Types and routing are generated
// from api/openapi.yaml into api.gen.go (`make generate`); this package wires
// them to the agent's logic.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/live"
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
	PlanService(ctx context.Context, name, service string, action stacks.ServiceAction) (compose.Project, stacks.ServicePlan, error)
	Remove(name string) error
	Dir() string
}

// OperationService is what the API needs from the operations package.
type OperationService interface {
	Start(p compose.Project, a operations.Action) (*operations.Operation, error)
	StartService(p compose.Project, a operations.Action, service string, services []string, cmds [][]string) (*operations.Operation, error)
	Delete(p compose.Project, volumes bool, remove func() error) (*operations.Operation, error)
	Get(id string) (*operations.Operation, error)
	Cancel(id string) (operations.Snapshot, error)
	List(stack string) []operations.Snapshot
	Active(stack string) (operations.Snapshot, bool)
}

// LiveService is what the API needs from the live package.
type LiveService interface {
	Subscribe(ctx context.Context) *live.Subscription
	Notify(stack string)
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
	// Live tells clients which stacks changed (GET /api/v1/events); optional.
	Live LiveService
	// EventsPing is how often the live event stream sends a ping event;
	// zero means 25 seconds.
	EventsPing time.Duration
	// Context, if set, ends open event streams when it is done, so that a
	// shutting-down agent doesn't wait for them.
	Context context.Context
	Logger  *slog.Logger // optional
	// PublicOrigin is the web UI's origin (see ParsePublicURL), or "".
	PublicOrigin string
	// TrustedProxies are reverse proxies whose X-Forwarded-For and
	// X-Forwarded-Proto the agent believes (see ParseTrustedProxies).
	TrustedProxies []netip.Prefix
	// AllowInsecureHTTP accepts credentials over plain HTTP from internet
	// addresses.
	AllowInsecureHTTP bool
}

// New returns the agent's HTTP handler.
func New(cfg Config) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	auth := &authenticator{
		token:        cfg.Token,
		sessions:     session.NewStore(0),
		limiter:      session.NewLimiter(maxFailedAttempts, time.Minute),
		failures:     newFailureLog(cfg.Logger),
		net:          network{trusted: cfg.TrustedProxies, allowInsecureHTTP: cfg.AllowInsecureHTTP},
		publicOrigin: cfg.PublicOrigin,
		cookieName:   session.CookieName,
	}
	if strings.HasPrefix(cfg.PublicOrigin, "https://") {
		auth.cookieName = "__Host-" + session.CookieName
	}
	h := &handlers{cfg: cfg, auth: auth}
	invalid := ErrorCodeInvalidRequest
	strict := NewStrictHandlerWithOptions(h, []StrictMiddlewareFunc{withRequest}, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok {
				code := ErrorCodeRequestTooLarge
				msg := fmt.Sprintf("request body larger than %d bytes", tooLarge.Limit)
				writeJSON(w, http.StatusRequestEntityTooLarge, Error{Error: msg, Code: &code})
				return
			}
			writeJSON(w, http.StatusBadRequest, Error{Error: err.Error(), Code: &invalid})
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			// The details (paths, commands) go to the log, not the client.
			cfg.Logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
			writeJSON(w, http.StatusInternalServerError, Error{Error: "internal error; the agent's log has the details"})
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

// Server limits. Requests are small and quick to read; only responses run
// long. ReadTimeout doesn't cut event streams: net/http lifts the read
// deadline once a request has been read (TestEventStreamOutlivesReadTimeout).
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = time.Minute
	idleTimeout       = 2 * time.Minute
	maxHeaderBytes    = 16 << 10
)

// NewServer returns the HTTP server for handler with the agent's limits, so
// slow or endless requests can't hold connections. There is no WriteTimeout:
// event streams stay open while an operation runs.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

// maxFailedAttempts is how many wrong tokens a client may send per minute,
// by login or bearer header.
const maxFailedAttempts = 10

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
