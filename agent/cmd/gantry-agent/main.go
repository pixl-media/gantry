// Command gantry-agent is the headless gantry agent: it runs on a Docker host
// and exposes an HTTP API for managing the Compose stacks on it.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pixl-media/gantry/agent/internal/api"
	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/docker"
	"github.com/pixl-media/gantry/agent/internal/logs"
	"github.com/pixl-media/gantry/agent/internal/operations"
	"github.com/pixl-media/gantry/agent/internal/stackfiles"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

// minTokenLength is the length below which the agent warns about its token.
const minTokenLength = 24

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	token := os.Getenv("GANTRY_TOKEN")
	if token == "" {
		logger.Error("GANTRY_TOKEN must be set")
		os.Exit(1)
	}
	if len(token) < minTokenLength {
		logger.Warn("GANTRY_TOKEN is short; use at least 24 random characters, e.g. from `openssl rand -hex 24`",
			"length", len(token))
	}

	dockerClient, err := docker.New(os.Getenv("DOCKER_HOST"))
	if err != nil {
		logger.Error("docker client", "err", err)
		os.Exit(1)
	}
	manager, err := stacks.New(envOr("GANTRY_STACKS_DIR", stacks.DefaultDir), dockerClient, compose.CLI{})
	if err != nil {
		logger.Error("stacks", "err", err)
		os.Exit(1)
	}
	if !manager.DirExists() {
		logger.Warn("stacks directory does not exist; no managed stacks until it is created (set GANTRY_STACKS_DIR to use another one)",
			"stacks_dir", manager.Dir())
	}

	// Canceled on SIGINT/SIGTERM. Running operations end with it: Compose
	// gets an interrupt, and open event streams finish.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ops := operations.NewManager(ctx, compose.CLI{}, operations.Options{})

	addr := envOr("GANTRY_LISTEN", ":7070")
	srv := &http.Server{
		Addr: addr,
		Handler: api.New(api.Config{
			Token:      token,
			Version:    version,
			Stacks:     manager,
			Operations: ops,
			Logs:       logs.New(dockerClient),
			Files:      stackfiles.New(compose.CLI{}),
			Logger:     logger,
		}),
		// No WriteTimeout: event streams stay open while an operation runs.
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("gantry agent listening", "addr", addr, "version", version, "stacks_dir", manager.Dir())
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutting down; running operations are canceled")
		// Long enough for Compose to react to the interrupt (see
		// compose.CLI.CancelGrace), so event streams can send their end.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown failed", "err", err)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
