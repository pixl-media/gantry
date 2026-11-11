// Package api exposes the agent's HTTP API.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// Config holds what the API needs to serve requests.
type Config struct {
	// Token is the shared secret clients send as "Authorization: Bearer <token>".
	Token   string
	Version string
}

// New returns the agent's HTTP handler. /healthz is public; everything under
// /api/ requires the bearer token, because the agent controls the Docker host.
func New(cfg Config) http.Handler {
	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": cfg.Version})
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("/api/", requireToken(cfg.Token, protected))
	return mux
}

// requireToken rejects requests without the expected bearer token. An empty
// configured token rejects everything instead of accepting an empty header.
func requireToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || !ok || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
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
