// Command gantry-agent is the headless gantry agent: it runs on a Docker host
// and exposes an HTTP API for managing the Compose stacks on it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pixl-media/gantry/agent/internal/api"
	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/docker"
	"github.com/pixl-media/gantry/agent/internal/live"
	"github.com/pixl-media/gantry/agent/internal/logs"
	"github.com/pixl-media/gantry/agent/internal/operations"
	"github.com/pixl-media/gantry/agent/internal/stackfiles"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

// minTokenLength is the shortest token the agent accepts. The login limit
// slows guessing down, but against many addresses only a long random token
// makes it hopeless; the token is root on the host.
const minTokenLength = 24

// minTokenChars is how many different characters a token needs at least. A
// random token of 24 hex digits has fewer with a chance of about 1 in 30,000.
const minTokenChars = 8

const tokenAdvice = "use at least 24 random characters, e.g. from `openssl rand -hex 24`"

// checkToken rejects a missing token and tokens that are obviously weak:
// short, made of few characters, or one piece repeated. It can't tell a
// random token from a chosen one, so the advice stays: generate it. Errors
// never contain the token.
func checkToken(token string) error {
	if token == "" {
		return fmt.Errorf("set GANTRY_TOKEN or GANTRY_TOKEN_FILE; %s", tokenAdvice)
	}
	if len(token) < minTokenLength {
		return fmt.Errorf("the token is too short (%d characters, at least %d); %s", len(token), minTokenLength, tokenAdvice)
	}
	chars := map[rune]bool{}
	for _, r := range token {
		chars[r] = true
	}
	if len(chars) < minTokenChars {
		return fmt.Errorf("the token uses only %d different characters; %s", len(chars), tokenAdvice)
	}
	for period := 1; period <= len(token)/2; period++ {
		if repeats(token, period) {
			return fmt.Errorf("the token repeats a piece of %d characters; %s", period, tokenAdvice)
		}
	}
	return nil
}

// repeats reports whether s consists of its first n bytes over and over.
func repeats(s string, n int) bool {
	for i := n; i < len(s); i++ {
		if s[i] != s[i-n] {
			return false
		}
	}
	return true
}

// loadToken returns the token from GANTRY_TOKEN, or from the file that
// GANTRY_TOKEN_FILE names (for systemd credentials or Docker secrets, which
// keep it out of the process environment), and a warning, if any.
func loadToken() (token, warning string, err error) {
	value, file := os.Getenv("GANTRY_TOKEN"), os.Getenv("GANTRY_TOKEN_FILE")
	switch {
	case value != "" && file != "":
		return "", "", errors.New("set GANTRY_TOKEN or GANTRY_TOKEN_FILE, not both")
	case file == "":
		return value, "", nil
	}
	info, err := os.Stat(file)
	if err != nil {
		return "", "", fmt.Errorf("GANTRY_TOKEN_FILE: %w", err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", "", fmt.Errorf("GANTRY_TOKEN_FILE: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		warning = fmt.Sprintf("GANTRY_TOKEN_FILE %s can be read by other users; make it readable only by the agent (chmod 600)", file)
	}
	return strings.TrimSpace(string(data)), warning, nil
}

// config is the agent's network-facing configuration from the environment.
type config struct {
	listen            string
	publicOrigin      string
	trustedProxies    []netip.Prefix
	allowInsecureHTTP bool
}

func loadConfig() (config, error) {
	var c config
	var err error
	// Only this host by default: reaching the agent from elsewhere is a
	// decision (the container image sets :7070, where published ports
	// decide).
	c.listen = envOr("GANTRY_LISTEN", "127.0.0.1:7070")
	if c.publicOrigin, err = api.ParsePublicURL(os.Getenv("GANTRY_PUBLIC_URL")); err != nil {
		return c, err
	}
	if c.trustedProxies, err = api.ParseTrustedProxies(os.Getenv("GANTRY_TRUSTED_PROXIES")); err != nil {
		return c, err
	}
	if v := os.Getenv("GANTRY_ALLOW_INSECURE_HTTP"); v != "" {
		if c.allowInsecureHTTP, err = strconv.ParseBool(v); err != nil {
			return c, fmt.Errorf("GANTRY_ALLOW_INSECURE_HTTP: want true or false, not %q", v)
		}
	}
	return c, nil
}

// loopbackOnly reports whether listen only accepts connections from this
// host.
func loopbackOnly(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.IsLoopback()
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	token, warning, err := loadToken()
	if err == nil {
		err = checkToken(token)
	}
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	if warning != "" {
		logger.Warn(warning)
	}
	cfg, err := loadConfig()
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	if cfg.allowInsecureHTTP {
		logger.Warn("GANTRY_ALLOW_INSECURE_HTTP is set: tokens and session cookies from internet addresses are accepted over plain HTTP, where anyone on the way can read them")
	}
	if !loopbackOnly(cfg.listen) && !strings.HasPrefix(cfg.publicOrigin, "https://") {
		logger.Info("reachable from other hosts over plain HTTP; logins and tokens from internet addresses are refused, use HTTPS in front (see README)",
			"addr", cfg.listen)
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
	// Tells web UIs which stacks changed: from Docker's events and from
	// operations and file changes made through the API.
	hub := live.New(dockerClient, live.Options{Logger: logger})
	ops := operations.NewManager(ctx, compose.CLI{}, operations.Options{OnChange: hub.Notify})

	addr := cfg.listen
	srv := api.NewServer(addr, api.New(api.Config{
		Token:             token,
		Version:           version,
		Stacks:            manager,
		Operations:        ops,
		Logs:              logs.New(dockerClient),
		Files:             stackfiles.New(compose.CLI{}),
		Live:              hub,
		Logger:            logger,
		PublicOrigin:      cfg.publicOrigin,
		TrustedProxies:    cfg.trustedProxies,
		AllowInsecureHTTP: cfg.allowInsecureHTTP,
	}))

	errc := make(chan error, 1)
	go func() {
		logger.Info("gantry agent listening", "addr", addr, "version", version, "stacks_dir", manager.Dir())
		ln, err := api.Listen(addr, cfg.trustedProxies)
		if err != nil {
			errc <- err
			return
		}
		errc <- srv.Serve(ln)
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
