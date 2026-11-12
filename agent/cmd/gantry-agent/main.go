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
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	token := os.Getenv("GANTRY_TOKEN")
	if token == "" {
		logger.Error("GANTRY_TOKEN must be set")
		os.Exit(1)
	}

	stacksDir := os.Getenv("GANTRY_STACKS_DIR")
	if stacksDir == "" {
		logger.Error("GANTRY_STACKS_DIR must be set")
		os.Exit(1)
	}
	dockerClient, err := docker.New(os.Getenv("DOCKER_HOST"))
	if err != nil {
		logger.Error("docker client", "err", err)
		os.Exit(1)
	}
	manager, err := stacks.New(stacksDir, dockerClient, compose.CLI{})
	if err != nil {
		logger.Error("stacks", "err", err)
		os.Exit(1)
	}

	addr := envOr("GANTRY_LISTEN", ":7070")
	srv := &http.Server{
		Addr: addr,
		Handler: api.New(api.Config{
			Token:   token,
			Version: version,
			Stacks:  manager,
			Logger:  logger,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
