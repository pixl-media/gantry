package stacks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/pixl-media/gantry/agent/internal/compose"
)

// noPermissions skips the test as root, who may read and write anything.
func noPermissions(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
}

// chmod changes path's mode and restores it after the test, so that the
// temporary directory can be removed.
func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, info.Mode().Perm()) })
}

func TestAccessAllowed(t *testing.T) {
	dir := stacksDir(t, "app")
	writeFile(t, filepath.Join(dir, "app", ".env"), "A=1\n")
	m := newManager(t, dir, fakeDocker{}, fakeCompose{})

	d, err := m.Get(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if d.Access == nil || !d.Access.Editable || !d.Access.Deletable || len(d.Access.Missing) != 0 || d.Access.User == "" {
		t.Errorf("access = %+v, want everything allowed", d.Access)
	}
	l, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Access.Missing) != 0 || l.Access.User != d.Access.User {
		t.Errorf("list access = %+v", l.Access)
	}
}

func TestAccessMissing(t *testing.T) {
	noPermissions(t)
	dir := stacksDir(t, "app")
	app := filepath.Join(dir, "app")
	env := filepath.Join(app, ".env")
	writeFile(t, env, "A=1\n")
	chmod(t, env, 0o000)
	chmod(t, app, 0o555)
	m := newManager(t, dir, fakeDocker{}, fakeCompose{})

	d, err := m.Get(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	want := []MissingAccess{{Path: app, Write: true}, {Path: env}}
	if d.Access == nil || d.Access.Editable || d.Access.Deletable || !slices.Equal(d.Access.Missing, want) {
		t.Errorf("access = %+v, want %+v, not editable", d.Access, want)
	}
	// The stacks directory is still writable: stacks can be created.
	l, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Access.Missing) != 0 {
		t.Errorf("list access = %+v", l.Access)
	}
}

func TestAccessStacksDirReadOnly(t *testing.T) {
	noPermissions(t)
	dir := stacksDir(t, "app")
	chmod(t, dir, 0o555)
	m := newManager(t, dir, fakeDocker{}, fakeCompose{})

	d, err := m.Get(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	// Editing works; deleting would need to remove the directory.
	want := []MissingAccess{{Path: dir, Write: true}}
	if !d.Access.Editable || d.Access.Deletable || !slices.Equal(d.Access.Missing, want) {
		t.Errorf("access = %+v", d.Access)
	}
	l, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(l.Access.Missing, want) {
		t.Errorf("list access = %+v, want %+v", l.Access, want)
	}
}

// A stacks directory that doesn't exist yet is created with the first stack,
// so its parent must be writable.
func TestAccessStacksDirMissing(t *testing.T) {
	noPermissions(t)
	parent := normPath(t.TempDir())
	chmod(t, parent, 0o555)
	m := newManager(t, filepath.Join(parent, "stacks", "more"), fakeDocker{}, fakeCompose{})

	if got, want := m.StacksDirAccess().Missing, []MissingAccess{{Path: parent, Write: true}}; !slices.Equal(got, want) {
		t.Errorf("missing = %+v, want %+v", got, want)
	}
}

func TestAccessReadOnlyFilesystem(t *testing.T) {
	dir := stacksDir(t, "app")
	orig := accessFunc
	accessFunc = func(path string, mode uint32) error {
		if mode&accessWrite != 0 {
			return syscall.EROFS
		}
		return nil
	}
	t.Cleanup(func() { accessFunc = orig })

	got := newManager(t, dir, fakeDocker{}, fakeCompose{}).Access(stackProject(dir, "app"))
	if got.Editable || len(got.Missing) != 2 || !got.Missing[0].ReadOnlyFilesystem || !got.Missing[1].ReadOnlyFilesystem {
		t.Errorf("access = %+v, want both directories on a read-only file system", got)
	}
}

// An unreadable .env breaks Compose; the warning says why instead of the
// general one.
func TestUnreadableEnvWarning(t *testing.T) {
	noPermissions(t)
	dir := stacksDir(t, "app")
	env := filepath.Join(dir, "app", ".env")
	writeFile(t, env, "SECRET=x\n")
	chmod(t, env, 0o000)
	m := newManager(t, dir, fakeDocker{}, fakeCompose{err: errors.New("open .env: permission denied")})

	l, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0], "may not read "+env) || strings.Contains(l.Warnings[0], "Compose can't read this stack's files") {
		t.Errorf("warnings = %v", l.Warnings)
	}
	// The stack's page has the hint from its access instead.
	d, err := m.Get(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Warnings) != 0 || len(d.Access.Missing) != 1 {
		t.Errorf("detail warnings = %v, access = %+v", d.Warnings, d.Access)
	}
}

func stackProject(dir, name string) compose.Project {
	return compose.Project{Name: name, Dir: filepath.Join(dir, name), File: filepath.Join(dir, name, "compose.yaml")}
}
