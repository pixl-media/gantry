package compose

import (
	"context"
	"io"
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

	got, err := CLI{Docker: bin}.Services(context.Background(), Project{Name: "app", Dir: dir, File: file})
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
	want := []string{"compose", "-p", "app", "-f", file, "--project-directory", dir, "config", "--services", "--no-interpolate"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestServicesError(t *testing.T) {
	bin, _ := fakeDocker(t, `echo "no such file" >&2; exit 1`)
	dir := t.TempDir()
	_, err := CLI{Docker: bin}.Services(context.Background(), Project{Name: "app", Dir: dir, File: filepath.Join(dir, "compose.yaml")})
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("err = %v, want stderr in error", err)
	}
}

// Compose interpolates ${...} from its environment, so it must never see the
// agent's GANTRY_* settings, the token above all.
func TestComposeDoesNotSeeGantrySettings(t *testing.T) {
	t.Setenv("GANTRY_TOKEN", "secret-token-value")
	t.Setenv("GANTRY_PUBLIC_URL", "https://gantry.example.com")
	envFile := filepath.Join(t.TempDir(), "env")
	t.Setenv("ENV_OUT", envFile)
	bin, _ := fakeDocker(t, `env >> "$ENV_OUT"`)
	dir := t.TempDir()
	p := Project{Name: "app", Dir: dir, File: filepath.Join(dir, "compose.yaml")}
	cli := CLI{Docker: bin}

	if _, err := cli.Services(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := cli.Validate(context.Background(), p, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Run(context.Background(), p, []string{"up", "-d"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Version(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	env := string(raw)
	if strings.Count(env, "ENV_OUT=") != 4 {
		t.Fatalf("expected four runs, env:\n%s", env)
	}
	if strings.Contains(env, "GANTRY_") || strings.Contains(env, "secret-token-value") {
		t.Errorf("Compose saw GANTRY_* settings:\n%s", env)
	}
	if !strings.Contains(env, "PATH=") {
		t.Error("the rest of the environment (PATH, HOME, DOCKER_HOST) must be passed on")
	}
}

func TestVersion(t *testing.T) {
	bin, argsFile := fakeDocker(t, `printf '2.39.1\n'`)
	got, err := CLI{Docker: bin}.Version(context.Background())
	if err != nil || got != "2.39.1" {
		t.Fatalf("version = %q, %v", got, err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if args := strings.Fields(string(raw)); !slices.Equal(args, []string{"compose", "version", "--short"}) {
		t.Errorf("args = %v", args)
	}
}

func TestVersionError(t *testing.T) {
	bin, _ := fakeDocker(t, `echo "docker: 'compose' is not a docker command." >&2; exit 1`)
	if _, err := (CLI{Docker: bin}).Version(context.Background()); err == nil {
		t.Fatal("expected an error without the Compose plugin")
	}
}
