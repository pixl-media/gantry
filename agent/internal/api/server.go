// Package api exposes the agent's HTTP API. Types and routing are generated
// from api/openapi.yaml into api.gen.go (`make generate`); this package wires
// them to the agent's logic.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/pixl-media/gantry/agent/internal/stacks"
)

// StackService is what the API needs from the stacks package.
type StackService interface {
	List(ctx context.Context) (stacks.List, error)
	Get(ctx context.Context, name string) (stacks.Detail, error)
}

// Config holds what the API needs to serve requests.
type Config struct {
	// Token is the shared secret clients send as "Authorization: Bearer <token>".
	Token   string
	Version string
	Stacks  StackService
	Logger  *slog.Logger // optional
}

// publicPaths are served without a token. Everything else requires it.
var publicPaths = map[string]bool{"/healthz": true}

// New returns the agent's HTTP handler.
func New(cfg Config) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	strict := NewStrictHandlerWithOptions(&handlers{cfg: cfg}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeJSON(w, http.StatusBadRequest, Error{Error: err.Error()})
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
			writeJSON(w, http.StatusBadRequest, Error{Error: err.Error()})
		},
	})
	return requireToken(cfg.Token, routes)
}

// requireToken rejects requests without the expected bearer token, except
// for publicPaths. Denying by default keeps new routes protected. An empty
// configured token rejects everything instead of accepting an empty header.
func requireToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || !ok || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, Error{Error: "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
