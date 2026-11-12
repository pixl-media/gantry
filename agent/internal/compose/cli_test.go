package compose

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeDocker writes a shell script that records its arguments and then runs
// body, and returns the script's path and the file holding the arguments.
func fakeDocker(t *testing.T, body string) (bin, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	bin = filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

func TestServices(t *testing.T) {
	bin, argsFile := fakeDocker(t, `printf 'web\n\ndb\n'`)
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")

	got, err := CLI{Docker: bin}.Services(context.Background(), dir, file)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"db", "web"}; !slices.Equal(got, want) {
		t.Errorf("services = %v, want %v", got, want)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(string(raw))
	want := []string{"compose", "-f", file, "--project-directory", dir, "config", "--services", "--no-interpolate"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestServicesError(t *testing.T) {
	bin, _ := fakeDocker(t, `echo "no such file" >&2; exit 1`)
	_, err := CLI{Docker: bin}.Services(context.Background(), t.TempDir(), "compose.yaml")
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("err = %v, want stderr in error", err)
	}
}
