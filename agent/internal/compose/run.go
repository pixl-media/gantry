package compose

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// Project identifies a Compose project on disk.
type Project struct {
	Name string // Compose project name
	Dir  string // project directory
	File string // Compose file
}

// defaultCancelGrace is how long Compose may take to stop after an
// interrupt before it is killed.
const defaultCancelGrace = 15 * time.Second

// Run runs `docker compose <args>` for project p and writes its combined
// stdout and stderr to out. It returns the exit code, or -1 if Compose didn't
// exit on its own.
//
// Cancelling ctx interrupts Compose like Ctrl+C in a terminal. The signal goes
// to the whole process group, because `docker compose` runs the Compose
// plugin as a child of the docker CLI. If Compose hasn't stopped after
// CancelGrace, the group is killed.
func (c CLI) Run(ctx context.Context, p Project, args []string, out io.Writer) (int, error) {
	full := append([]string{
		"compose", "--ansi", "never", "--progress", "plain",
		"-p", p.Name, "-f", p.File, "--project-directory", p.Dir,
	}, args...)
	grace := c.CancelGrace
	if grace == 0 {
		grace = defaultCancelGrace
	}

	cmd := exec.CommandContext(ctx, c.bin(), full...)
	cmd.Dir = p.Dir
	// Same writer for both: os/exec then serializes the writes.
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	exited := make(chan struct{})
	defer close(exited)
	cmd.Cancel = func() error {
		pgid := -cmd.Process.Pid
		_ = syscall.Kill(pgid, syscall.SIGINT)
		go func() {
			select {
			case <-exited:
			case <-time.After(grace):
				_ = syscall.Kill(pgid, syscall.SIGKILL)
			}
		}()
		return nil
	}
	// Stop waiting for output pipes shortly after the forced kill.
	cmd.WaitDelay = grace + 5*time.Second

	err := cmd.Run()
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		if ctx.Err() != nil {
			return code, fmt.Errorf("docker compose %s: %w", args[0], ctx.Err())
		}
		return code, fmt.Errorf("docker compose %s: %w", args[0], err)
	}
	return code, nil
}
