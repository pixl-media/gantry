package compose

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	bin, argsFile := fakeDocker(t, `echo "to stdout"; echo "to stderr" >&2; exit 3`)
	dir := t.TempDir()
	p := Project{Name: "app", Dir: dir, File: dir + "/compose.yaml"}

	var out bytes.Buffer
	code, err := CLI{Docker: bin}.Run(context.Background(), p, []string{"up", "-d"}, &out)
	if err == nil || code != 3 {
		t.Fatalf("code = %d, err = %v, want exit code 3 and an error", code, err)
	}
	if got := out.String(); !strings.Contains(got, "to stdout") || !strings.Contains(got, "to stderr") {
		t.Errorf("output = %q, want stdout and stderr", got)
	}

	raw, _ := os.ReadFile(argsFile)
	want := []string{"compose", "--ansi", "never", "--progress", "plain",
		"-p", "app", "-f", p.File, "--project-directory", dir, "up", "-d"}
	if args := strings.Fields(string(raw)); !slices.Equal(args, want) {
		t.Errorf("args = %v\nwant   %v", args, want)
	}
}

func TestRunSuccess(t *testing.T) {
	bin, _ := fakeDocker(t, `echo ok`)
	dir := t.TempDir()
	code, err := CLI{Docker: bin}.Run(context.Background(), Project{Name: "a", Dir: dir, File: "f"}, []string{"stop"}, &bytes.Buffer{})
	if err != nil || code != 0 {
		t.Fatalf("code = %d, err = %v", code, err)
	}
}

func TestRunCancelInterrupts(t *testing.T) {
	// The script reacts to SIGINT like Compose: it reports and exits.
	bin, _ := fakeDocker(t, `trap 'echo interrupted; exit 130' INT; echo started; while true; do sleep 0.1; done`)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuffer

	done := make(chan struct{})
	var code int
	var err error
	go func() {
		code, err = CLI{Docker: bin, CancelGrace: 5 * time.Second}.Run(ctx, Project{Name: "a", Dir: dir, File: "f"}, []string{"up"}, &out)
		close(done)
	}()
	waitFor(t, func() bool { return strings.Contains(out.String(), "started") })
	cancel()

	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("Run didn't return after cancel; SIGINT not delivered?")
	}
	if code != 130 || err == nil || !strings.Contains(out.String(), "interrupted") {
		t.Errorf("code = %d, err = %v, output = %q", code, err, out.String())
	}
}

func TestRunCancelKillsAfterGrace(t *testing.T) {
	// This one ignores SIGINT, so it must be killed after the grace period.
	bin, _ := fakeDocker(t, `trap '' INT; echo started; while true; do sleep 0.1; done`)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuffer

	done := make(chan struct{})
	go func() {
		CLI{Docker: bin, CancelGrace: 300 * time.Millisecond}.Run(ctx, Project{Name: "a", Dir: dir, File: "f"}, []string{"up"}, &out)
		close(done)
	}()
	waitFor(t, func() bool { return strings.Contains(out.String(), "started") })
	cancel()

	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("Run didn't return; process not killed after grace")
	}
}
