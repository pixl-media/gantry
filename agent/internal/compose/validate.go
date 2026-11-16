package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// InvalidError means Compose rejected a Compose file. Message is what
// Compose reported, for showing to users.
type InvalidError struct {
	Message string
}

func (e *InvalidError) Error() string { return "invalid Compose file: " + e.Message }

// Validate checks p.File with `docker compose config --quiet`, without
// interpolating variables, so missing .env values don't fail it and no
// values are read. It returns an *InvalidError if Compose rejects the file.
// displayName, if set, replaces the file's name in Compose's message (for
// validating a temporary copy).
func (c CLI) Validate(ctx context.Context, p Project, displayName string) error {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := c.command(ctx, p, "config", "--quiet", "--no-interpolate")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil {
		return fmt.Errorf("docker compose config: %w", err) // couldn't run at all
	}
	msg := strings.TrimSpace(stderr.String())
	if displayName != "" {
		msg = strings.ReplaceAll(msg, p.File, displayName)
		msg = strings.ReplaceAll(msg, filepath.Base(p.File), displayName)
	}
	if msg == "" {
		msg = exit.Error()
	}
	return &InvalidError{Message: msg}
}
