// Command gantry-agent is the headless gantry agent: it runs on a Docker host
// and exposes an HTTP API for managing the Compose stacks on it.
package main

import (
	"context"
	"errors"
	"fmt"
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

// Token lengths: below minTokenLength the agent refuses to start, below
// goodTokenLength it warns. The login limit slows guessing down, but only a
// long random token makes it hopeless.
const (
	minTokenLength  = 16
	goodTokenLength = 24
)

// checkToken returns an error for a missing or too short token, or a warning
// for a short one.
func checkToken(token string) (warning string, err error) {
	const advice = "use at least 24 random characters, e.g. from `openssl rand -hex 24`"
	switch {
	case token == "":
		return "", fmt.Errorf("GANTRY_TOKEN must be set; %s", advice)
	case len(token) < minTokenLength:
		return "", fmt.Errorf("GANTRY_TOKEN is too short (%d characters, at least %d); %s", len(token), minTokenLength, advice)
	case len(token) < goodTokenLength:
		return fmt.Sprintf("GANTRY_TOKEN is short (%d characters); %s", len(token), advice), nil
	}
	return "", nil
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	token := os.Getenv("GANTRY_TOKEN")
	warning, err := checkToken(token)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	if warning != "" {
		logger.Warn(warning)
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

	publicOrigin, err := api.ParsePublicURL(os.Getenv("GANTRY_PUBLIC_URL"))
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	addr := envOr("GANTRY_LISTEN", ":7070")
	srv := api.NewServer(addr, api.New(api.Config{
		Token:        token,
		Version:      version,
		Stacks:       manager,
		Operations:   ops,
		Logs:         logs.New(dockerClient),
		Files:        stackfiles.New(compose.CLI{}),
		Logger:       logger,
		PublicOrigin: publicOrigin,
	}))

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
