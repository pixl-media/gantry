// Command devdocs serves Swagger UI for api/openapi.yaml during development.
//
// It also forwards API requests to a locally running agent, so the browser
// sees one origin and "Try it out" works without CORS. It is a development
// tool only: it isn't part of the agent binary or its image.
//
// Run it with `make docs`.
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"
)

//go:embed index.html
var indexHTML []byte

func main() {
	listen := flag.String("listen", "127.0.0.1:8082", "address to serve the docs on")
	spec := flag.String("spec", "../api/openapi.yaml", "path to the OpenAPI spec")
	agent := flag.String("agent", "http://127.0.0.1:7070", "agent to forward API requests to")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	target, err := url.Parse(*agent)
	if err != nil || target.Scheme == "" || target.Host == "" {
		logger.Error("invalid -agent URL", "agent", *agent)
		os.Exit(1)
	}
	if _, err := os.Stat(*spec); err != nil {
		logger.Error("spec not found", "err", err)
		os.Exit(1)
	}

	logger.Info("API docs on http://"+*listen, "spec", *spec, "agent", target.String())
	srv := &http.Server{Addr: *listen, Handler: newHandler(*spec, target), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("server failed", "err", err)
		os.Exit(1)
	}
}

// newHandler serves the Swagger UI page, the spec (read on every request, so
// edits show up on reload) and forwards everything else to the agent.
func newHandler(spec string, agent *url.URL) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(agent)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "agent not reachable at " + agent.String() + " (start it with `make run` or `docker compose up`): " + err.Error(),
			})
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/yaml")
		http.ServeFile(w, r, spec)
	})
	mux.Handle("/", proxy)
	return mux
}
