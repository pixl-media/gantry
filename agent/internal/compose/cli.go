// Package compose runs `docker compose` CLI commands, so that Compose
// behaves for gantry exactly as it does in a terminal.
package compose

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// CLI runs the docker compose plugin.
type CLI struct {
	// Docker is the docker binary. Empty means "docker" from PATH.
	Docker string
	// Timeout per command. Zero means 30 seconds.
	Timeout time.Duration
}

// Services returns the sorted service names of the Compose project in dir,
// defined by file. Variables are not interpolated, so no values from .env
// files are read into the agent.
func (c CLI) Services(ctx context.Context, dir, file string) ([]string, error) {
	out, err := c.run(ctx, dir,
		"compose", "-f", file, "--project-directory", dir,
		"config", "--services", "--no-interpolate")
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

func (c CLI) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	bin := c.Docker
	if bin == "" {
		bin = "docker"
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("%s %s: %w", bin, strings.Join(args, " "), err)
		}
		return nil, fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args, " "), err, msg)
	}
	return out, nil
}
