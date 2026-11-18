// Package compose runs `docker compose` CLI commands, so that Compose
// behaves for gantry exactly as it does in a terminal.
package compose

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// CLI runs the docker compose plugin.
type CLI struct {
	// Docker is the docker binary. Empty means "docker" from PATH.
	Docker string
	// Timeout for short commands such as Services. Zero means 30 seconds.
	// Run has no own timeout; its context decides.
	Timeout time.Duration
	// CancelGrace is how long Run waits after an interrupt before killing
	// Compose. Zero means 15 seconds.
	CancelGrace time.Duration
}

func (c CLI) bin() string {
	if c.Docker == "" {
		return "docker"
	}
	return c.Docker
}

// command returns a Compose process for p, which always gets the project
// explicitly and runs in its directory, with the agent's environment minus
// its GANTRY_* settings: Compose interpolates ${...} from its environment,
// so a Compose file could otherwise read GANTRY_TOKEN.
func (c CLI) command(ctx context.Context, p Project, args ...string) *exec.Cmd {
	full := append([]string{"compose", "-p", p.Name, "-f", p.File, "--project-directory", p.Dir}, args...)
	cmd := exec.CommandContext(ctx, c.bin(), full...)
	cmd.Dir = p.Dir
	cmd.Env = childEnv()
	return cmd
}

// childEnv is the agent's environment without GANTRY_* variables.
func childEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GANTRY_") {
			env = append(env, kv)
		}
	}
	return env
}

// Services returns the sorted service names of the Compose project p.
// Variables are not interpolated, so no values from .env files are read
// into the agent.
func (c CLI) Services(ctx context.Context, p Project) ([]string, error) {
	out, err := c.output(ctx, p, "config", "--services", "--no-interpolate")
	if err != nil {
		return nil, err
	}
	var services []string
	for line := range strings.Lines(string(out)) {
		if s := strings.TrimSpace(line); s != "" {
			services = append(services, s)
		}
	}
	sort.Strings(services)
	return services, nil
}

// Version returns the version of the Compose plugin, as `docker compose
// version --short` prints it.
func (c CLI) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin(), "compose", "version", "--short")
	cmd.Env = childEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker compose version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c CLI) timeout() time.Duration {
	if c.Timeout == 0 {
		return 30 * time.Second
	}
	return c.Timeout
}

// output runs a short Compose command for p and returns its stdout.
func (c CLI) output(ctx context.Context, p Project, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	cmd := c.command(ctx, p, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
		}
		return nil, fmt.Errorf("docker compose %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return out, nil
}
