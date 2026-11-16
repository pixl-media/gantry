package stackfiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pixl-media/gantry/agent/internal/compose"
)

// fakeValidator rejects content containing "invalid" and records the
// display name it was given.
type fakeValidator struct{ displayName string }

func (v *fakeValidator) Validate(_ context.Context, p compose.Project, displayName string) error {
	v.displayName = displayName
	data, err := os.ReadFile(p.File)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), "invalid") {
		return &compose.InvalidError{Message: displayName + ": services must be a mapping"}
	}
	return nil
}

func project(t *testing.T, content string) compose.Project {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(file, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	return compose.Project{Name: "app", Dir: dir, File: file}
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".gantry-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestReplaceCompose(t *testing.T) {
	p := project(t, "services: {}\n")
	v := &fakeValidator{}
	f := New(v)

	newVersion, err := f.ReplaceCompose(context.Background(), p, Version([]byte("services: {}\n")), []byte("services:\n  app: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p.File)
	if string(got) != "services:\n  app: {}\n" || newVersion != Version(got) {
		t.Errorf("file = %q, version %s", got, newVersion)
	}
	if mode(t, p.File) != 0o640 {
		t.Errorf("mode = %v, want the old 0640", mode(t, p.File))
	}
	if v.displayName != "docker-compose.yml" {
		t.Errorf("validated as %q, want the real file name", v.displayName)
	}
	noTempFiles(t, p.Dir)

	// A stale version is a conflict, and nothing is written.
	if _, err := f.ReplaceCompose(context.Background(), p, Version([]byte("services: {}\n")), []byte("x: 1\n")); !errors.Is(err, ErrConflict) {
		t.Errorf("stale version: err = %v, want ErrConflict", err)
	}
	// Invalid content is rejected, and the file stays as it was.
	var invalid *compose.InvalidError
	if _, err := f.ReplaceCompose(context.Background(), p, newVersion, []byte("invalid\n")); !errors.As(err, &invalid) {
		t.Errorf("invalid content: err = %v", err)
	}
	if got, _ := os.ReadFile(p.File); string(got) != "services:\n  app: {}\n" {
		t.Errorf("file changed after a rejected save: %q", got)
	}
	noTempFiles(t, p.Dir)

	if _, err := f.ReplaceCompose(context.Background(), p, newVersion, make([]byte, MaxSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: err = %v", err)
	}
}

func TestSymlinksAreNotEdited(t *testing.T) {
	p := project(t, "services: {}\n")
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("TOKEN=x\n"), 0o600)
	os.Symlink(outside, filepath.Join(p.Dir, EnvFileName))
	f := New(&fakeValidator{})

	if _, err := f.Env(p); !errors.Is(err, ErrSymlink) {
		t.Errorf("Env: %v, want ErrSymlink", err)
	}
	if _, err := f.RevealEnv(p, "TOKEN"); !errors.Is(err, ErrSymlink) {
		t.Errorf("RevealEnv: %v, want ErrSymlink", err)
	}
	if _, err := f.UpdateEnv(p, "", map[string]string{"A": "1"}, nil); !errors.Is(err, ErrSymlink) {
		t.Errorf("UpdateEnv: %v, want ErrSymlink", err)
	}
	if got, _ := os.ReadFile(outside); string(got) != "TOKEN=x\n" {
		t.Error("the symlink target was changed")
	}
}

// A stack directory that is a symlink is not edited either: everything below
// it would land in the link's target.
func TestSymlinkedStackDirectoryIsNotEdited(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "compose.yaml"), []byte("services: {}\n"), 0o644)
	os.WriteFile(filepath.Join(outside, EnvFileName), []byte("TOKEN=x\n"), 0o600)
	stacks := t.TempDir()
	dir := filepath.Join(stacks, "linked")
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	p := compose.Project{Name: "linked", Dir: dir, File: filepath.Join(dir, "compose.yaml")}
	f := New(&fakeValidator{})

	if _, err := f.Env(p); !errors.Is(err, ErrSymlink) {
		t.Errorf("Env: %v, want ErrSymlink", err)
	}
	if _, err := f.RevealEnv(p, "TOKEN"); !errors.Is(err, ErrSymlink) {
		t.Errorf("RevealEnv: %v, want ErrSymlink", err)
	}
	if _, err := f.UpdateEnv(p, Version([]byte("TOKEN=x\n")), map[string]string{"A": "1"}, nil); !errors.Is(err, ErrSymlink) {
		t.Errorf("UpdateEnv: %v, want ErrSymlink", err)
	}
	if _, err := f.ReplaceCompose(context.Background(), p, Version([]byte("services: {}\n")), []byte("services: {a: {}}\n")); !errors.Is(err, ErrSymlink) {
		t.Errorf("ReplaceCompose: %v, want ErrSymlink", err)
	}
	if got, _ := os.ReadFile(filepath.Join(outside, "compose.yaml")); string(got) != "services: {}\n" {
		t.Error("the Compose file behind the link was changed")
	}
	if got, _ := os.ReadFile(filepath.Join(outside, EnvFileName)); string(got) != "TOKEN=x\n" {
		t.Error("the .env file behind the link was changed")
	}
	noTempFiles(t, outside)
}

func TestSymlinkedComposeFileIsNotEdited(t *testing.T) {
	p := project(t, "services: {}\n")
	outside := filepath.Join(t.TempDir(), "compose.yaml")
	os.WriteFile(outside, []byte("services: {}\n"), 0o644)
	link := filepath.Join(p.Dir, "compose.yaml")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	p.File = link
	_, err := New(&fakeValidator{}).ReplaceCompose(context.Background(), p, Version([]byte("services: {}\n")), []byte("services: {a: {}}\n"))
	if !errors.Is(err, ErrSymlink) {
		t.Errorf("ReplaceCompose: %v, want ErrSymlink", err)
	}
	if got, _ := os.ReadFile(outside); string(got) != "services: {}\n" {
		t.Error("the link's target was changed")
	}
}

func TestComposeFileOutsideStackDirectory(t *testing.T) {
	p := project(t, "services: {}\n")
	p.File = filepath.Join(t.TempDir(), "compose.yaml")
	os.WriteFile(p.File, []byte("services: {}\n"), 0o644)
	if _, err := New(&fakeValidator{}).ReplaceCompose(context.Background(), p, Version([]byte("services: {}\n")), []byte("x: 1\n")); err == nil {
		t.Error("ReplaceCompose wrote a file outside the stack directory")
	}
}

func TestEnvLifecycle(t *testing.T) {
	p := project(t, "services: {}\n")
	f := New(&fakeValidator{})
	path := filepath.Join(p.Dir, EnvFileName)

	info, err := f.Env(p)
	if err != nil || info.Exists || info.Version != "" || len(info.Keys) != 0 {
		t.Fatalf("missing file: %+v, %v", info, err)
	}

	info, err = f.UpdateEnv(p, "", map[string]string{"DB_PASSWORD": "s3cr3t", "DB_USER": "admin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Exists || !slices.Equal(info.Keys, []string{"DB_PASSWORD", "DB_USER"}) {
		t.Errorf("after create: %+v", info)
	}
	if mode(t, path) != 0o600 {
		t.Errorf("new .env mode = %v, want 0600", mode(t, path))
	}

	if v, err := f.RevealEnv(p, "DB_PASSWORD"); err != nil || v != "s3cr3t" {
		t.Errorf("reveal = %q, %v", v, err)
	}
	if _, err := f.RevealEnv(p, "NOPE"); !errors.Is(err, ErrVariableNotFound) {
		t.Errorf("reveal unknown: %v", err)
	}

	// Stale version → conflict.
	if _, err := f.UpdateEnv(p, "", map[string]string{"X": "1"}, nil); !errors.Is(err, ErrConflict) {
		t.Errorf("stale version: %v", err)
	}
	// Existing mode is kept on later writes.
	os.Chmod(path, 0o640)
	info, err = f.UpdateEnv(p, info.Version, map[string]string{"DB_USER": "root"}, []string{"DB_PASSWORD"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(info.Keys, []string{"DB_USER"}) || mode(t, path) != 0o640 {
		t.Errorf("after update: %+v, mode %v", info, mode(t, path))
	}
	noTempFiles(t, p.Dir)

	var invalid *InvalidVariableError
	if _, err := f.UpdateEnv(p, info.Version, map[string]string{"BAD-KEY": "x"}, nil); !errors.As(err, &invalid) {
		t.Errorf("invalid key: %v", err)
	}
}

func TestCreate(t *testing.T) {
	stacksDir := filepath.Join(t.TempDir(), "stacks") // doesn't exist yet
	f := New(&fakeValidator{})

	dir, err := f.Create(context.Background(), stacksDir, "app", []byte("services: {}\n"), map[string]string{"TOKEN": "x y"})
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(stacksDir, "app") {
		t.Errorf("dir = %s", dir)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, ComposeFileName)); string(got) != "services: {}\n" {
		t.Errorf("compose = %q", got)
	}
	if mode(t, filepath.Join(dir, EnvFileName)) != 0o600 {
		t.Error(".env of a new stack must be 0600")
	}
	if v, _ := f.RevealEnv(compose.Project{Dir: dir}, "TOKEN"); v != "x y" {
		t.Errorf("TOKEN = %q", v)
	}

	if _, err := f.Create(context.Background(), stacksDir, "app", []byte("services: {}\n"), nil); !errors.Is(err, ErrExists) {
		t.Errorf("existing name: %v", err)
	}

	// An invalid Compose file leaves nothing behind.
	var invalid *compose.InvalidError
	if _, err := f.Create(context.Background(), stacksDir, "broken", []byte("invalid\n"), nil); !errors.As(err, &invalid) {
		t.Errorf("invalid compose: %v", err)
	}
	entries, _ := os.ReadDir(stacksDir)
	if len(entries) != 1 || entries[0].Name() != "app" {
		t.Errorf("stacks dir after failed create: %v", entries)
	}
}
