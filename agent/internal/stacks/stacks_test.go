package stacks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/docker"
	"github.com/pixl-media/gantry/agent/internal/stackfiles"
)

type fakeDocker struct {
	containers []docker.Container
	err        error
}

func (f fakeDocker) ListComposeContainers(context.Context) ([]docker.Container, error) {
	return f.containers, f.err
}

// fakeCompose returns services per project directory name.
type fakeCompose struct {
	services map[string][]string
	err      error
}

func (f fakeCompose) Services(_ context.Context, p compose.Project) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.services[filepath.Base(p.Dir)], nil
}

// stacksDir creates a stacks directory with a compose.yaml per name.
func stacksDir(t *testing.T, names ...string) string {
	t.Helper()
	dir := normPath(t.TempDir())
	for _, n := range names {
		writeFile(t, filepath.Join(dir, n, "compose.yaml"), "services: {}\n")
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ctr(project, service, state, workingDir string) docker.Container {
	return docker.Container{
		ID:    project + "-" + service + "-" + state,
		Name:  project + "-" + service + "-1",
		State: state,
		Labels: map[string]string{
			docker.LabelProject:    project,
			docker.LabelService:    service,
			docker.LabelWorkingDir: workingDir,
			docker.LabelOneoff:     "False",
		},
	}
}

func newManager(t *testing.T, dir string, d ContainerLister, c ServiceLister) *Manager {
	t.Helper()
	m, err := New(dir, d, c)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func find(t *testing.T, l List, name string) Summary {
	t.Helper()
	for _, s := range l.Stacks {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("stack %q not in list %+v", name, l.Stacks)
	return Summary{}
}

func TestValidName(t *testing.T) {
	valid := []string{"whoami", "my-app_2", "0day", "a"}
	invalid := []string{"", "-app", "_app", "App", "../etc", "a/b", ".", "..", "a.b", "a b", "app\n"}
	for _, n := range valid {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false, want true", n)
		}
	}
	for _, n := range invalid {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true, want false", n)
		}
	}
}

func TestStatus(t *testing.T) {
	tests := []struct {
		total, running int
		docker         bool
		want           Status
	}{
		{2, 2, true, StatusRunning},
		{2, 1, true, StatusPartial},
		{2, 0, true, StatusStopped},
		{0, 0, true, StatusStopped},
		{2, 2, false, StatusUnknown},
	}
	for _, tt := range tests {
		if got := status(tt.total, tt.running, tt.docker); got != tt.want {
			t.Errorf("status(%d, %d, %v) = %s, want %s", tt.total, tt.running, tt.docker, got, tt.want)
		}
	}
}

func TestListManagedAndUnmanaged(t *testing.T) {
	dir := stacksDir(t, "whoami", "app", "half")
	oneoff := ctr("whoami", "whoami", "running", filepath.Join(dir, "whoami"))
	oneoff.Labels[docker.LabelOneoff] = "True"
	d := fakeDocker{containers: []docker.Container{
		ctr("whoami", "whoami", "running", filepath.Join(dir, "whoami")),
		oneoff, // a `compose run` container must not count
		ctr("half", "web", "running", filepath.Join(dir, "half")),
		ctr("half", "db", "exited", filepath.Join(dir, "half")),
		ctr("monitoring", "grafana", "running", "/srv/monitoring"),
		ctr("monitoring", "prometheus", "running", "/srv/monitoring"),
	}}
	c := fakeCompose{services: map[string][]string{
		"whoami": {"whoami"},
		"app":    {"db", "web"},
		"half":   {"db", "web"},
	}}

	l, err := newManager(t, dir, d, c).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !l.DockerAvailable || len(l.Warnings) != 0 {
		t.Errorf("dockerAvailable = %v, warnings = %v", l.DockerAvailable, l.Warnings)
	}
	names := []string{}
	for _, s := range l.Stacks {
		names = append(names, s.Name)
	}
	if want := []string{"app", "half", "monitoring", "whoami"}; !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v (sorted)", names, want)
	}

	tests := []Summary{
		{Name: "app", Managed: true, Status: StatusStopped, ServicesTotal: 2, ServicesRunning: 0, Path: filepath.Join(dir, "app")},
		{Name: "half", Managed: true, Status: StatusPartial, ServicesTotal: 2, ServicesRunning: 1, Path: filepath.Join(dir, "half")},
		{Name: "monitoring", Managed: false, Status: StatusRunning, ServicesTotal: 2, ServicesRunning: 2, Path: "/srv/monitoring"},
		{Name: "whoami", Managed: true, Status: StatusRunning, ServicesTotal: 1, ServicesRunning: 1, Path: filepath.Join(dir, "whoami")},
	}
	for _, want := range tests {
		if got := find(t, l, want.Name); got != want {
			t.Errorf("%s:\n got  %+v\n want %+v", want.Name, got, want)
		}
	}
}

func TestListDockerUnavailable(t *testing.T) {
	dir := stacksDir(t, "app")
	d := fakeDocker{err: errors.New("connection refused")}
	c := fakeCompose{services: map[string][]string{"app": {"db", "web"}}}

	l, err := newManager(t, dir, d, c).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l.DockerAvailable {
		t.Error("dockerAvailable = true, want false")
	}
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0], "docker unavailable") {
		t.Errorf("warnings = %v, want docker unavailable", l.Warnings)
	}
	app := find(t, l, "app")
	if app.Status != StatusUnknown || app.ServicesTotal != 2 {
		t.Errorf("app = %+v, want unknown with 2 services", app)
	}
}

func TestListSkipsDirectories(t *testing.T) {
	dir := stacksDir(t, "ok")
	writeFile(t, filepath.Join(dir, "Bad.Name", "compose.yaml"), "services: {}\n")
	writeFile(t, filepath.Join(dir, ".hidden", "compose.yaml"), "services: {}\n")
	writeFile(t, filepath.Join(dir, "empty", "README.md"), "no compose file here\n")
	writeFile(t, filepath.Join(dir, "notes.txt"), "a file, not a directory\n")

	l, err := newManager(t, dir, fakeDocker{}, fakeCompose{}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Stacks) != 1 || l.Stacks[0].Name != "ok" {
		t.Errorf("stacks = %+v, want only ok", l.Stacks)
	}
	joined := strings.Join(l.Warnings, "\n")
	if len(l.Warnings) != 2 || !strings.Contains(joined, `"Bad.Name"`) || !strings.Contains(joined, `"empty"`) {
		t.Errorf("warnings = %v, want Bad.Name and empty (hidden dirs and files are silent)", l.Warnings)
	}
}

func TestComposeFileNames(t *testing.T) {
	dir := normPath(t.TempDir())
	writeFile(t, filepath.Join(dir, "legacy", "docker-compose.yml"), "services: {}\n")
	writeFile(t, filepath.Join(dir, "both", "compose.yaml"), "services: {}\n")
	writeFile(t, filepath.Join(dir, "both", "docker-compose.yml"), "services: {}\n")
	m := newManager(t, dir, fakeDocker{}, fakeCompose{})

	for name, want := range map[string]string{"legacy": "docker-compose.yml", "both": "compose.yaml"} {
		d, err := m.Get(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		if got := filepath.Base(d.ComposeFile.Path); got != want {
			t.Errorf("%s: compose file = %s, want %s", name, got, want)
		}
	}
}

func TestNameConflictWarning(t *testing.T) {
	dir := stacksDir(t, "app")
	d := fakeDocker{containers: []docker.Container{ctr("app", "web", "running", "/home/me/old-app")}}
	l, err := newManager(t, dir, d, fakeCompose{services: map[string][]string{"app": {"web"}}}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0], "/home/me/old-app") || !strings.HasPrefix(l.Warnings[0], "app: ") {
		t.Errorf("warnings = %v, want conflict warning for app", l.Warnings)
	}
}

func TestComposeErrorFallsBackToContainers(t *testing.T) {
	dir := stacksDir(t, "app")
	d := fakeDocker{containers: []docker.Container{ctr("app", "web", "running", filepath.Join(dir, "app"))}}
	c := fakeCompose{err: errors.New("compose not installed")}

	l, err := newManager(t, dir, d, c).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	app := find(t, l, "app")
	if app.ServicesTotal != 1 || app.Status != StatusRunning {
		t.Errorf("app = %+v, want 1 service from containers", app)
	}
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0], "compose not installed") {
		t.Errorf("warnings = %v", l.Warnings)
	}
}

func TestGetManaged(t *testing.T) {
	dir := normPath(t.TempDir())
	raw := "services:\n  db:\n    environment:\n      PASSWORD: ${DB_PASSWORD}\n  web: {}\n"
	writeFile(t, filepath.Join(dir, "app", "compose.yaml"), raw)
	writeFile(t, filepath.Join(dir, "app", ".env"), "DB_PASSWORD=secret\n")
	wd := filepath.Join(dir, "app")
	d := fakeDocker{containers: []docker.Container{
		ctr("app", "web", "running", wd),
		ctr("app", "db", "exited", wd),
	}}
	c := fakeCompose{services: map[string][]string{"app": {"db", "web"}}}

	got, err := newManager(t, dir, d, c).Get(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPartial || !got.Managed || !got.DockerAvailable {
		t.Errorf("summary = %+v", got.Summary)
	}
	if got.ComposeFile == nil || got.ComposeFile.Content == nil || *got.ComposeFile.Content != raw {
		t.Fatalf("compose file = %+v, want raw content", got.ComposeFile)
	}
	if strings.Contains(*got.ComposeFile.Content, "secret") {
		t.Error("compose content contains a value from .env")
	}
	if len(got.Services) != 2 || got.Services[0].Name != "db" || got.Services[1].Name != "web" {
		t.Fatalf("services = %+v", got.Services)
	}
	if c := got.Services[1].Containers; len(c) != 1 || c[0].State != "running" {
		t.Errorf("web containers = %+v", c)
	}
}

func TestGetUnmanaged(t *testing.T) {
	ext := normPath(t.TempDir())
	file := filepath.Join(ext, "compose.yaml")
	writeFile(t, file, "services:\n  x: {}\n")
	c := ctr("ext", "x", "running", ext)
	c.Labels[docker.LabelConfigFiles] = file
	missing := ctr("gone", "y", "running", "/nonexistent")
	missing.Labels[docker.LabelConfigFiles] = "/nonexistent/compose.yaml"
	m := newManager(t, stacksDir(t), fakeDocker{containers: []docker.Container{c, missing}}, fakeCompose{})

	got, err := m.Get(context.Background(), "ext")
	if err != nil {
		t.Fatal(err)
	}
	if got.Managed || got.Path != ext || got.ComposeFile.Content == nil {
		t.Errorf("ext = %+v, compose file %+v", got.Summary, got.ComposeFile)
	}

	got, err = m.Get(context.Background(), "gone")
	if err != nil {
		t.Fatal(err)
	}
	if got.ComposeFile.Content != nil || len(got.Warnings) != 1 {
		t.Errorf("unreadable file: content = %v, warnings = %v", got.ComposeFile.Content, got.Warnings)
	}
}

func TestGetErrors(t *testing.T) {
	m := newManager(t, stacksDir(t, "app"), fakeDocker{}, fakeCompose{})
	for _, name := range []string{"../app", "app/..", "", "APP", "a.b"} {
		if _, err := m.Get(context.Background(), name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Get(%q) err = %v, want ErrInvalidName", name, err)
		}
	}
	if _, err := m.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(nope) err = %v, want ErrNotFound", err)
	}
}

func TestTarget(t *testing.T) {
	dir := stacksDir(t, "app")
	writeFile(t, filepath.Join(dir, "empty", "README.md"), "no compose file\n")
	d := fakeDocker{containers: []docker.Container{ctr("monitoring", "grafana", "running", "/srv/monitoring")}}
	m := newManager(t, dir, d, fakeCompose{})

	p, err := m.Target(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "app" || p.Dir != filepath.Join(dir, "app") || p.File != filepath.Join(dir, "app", "compose.yaml") {
		t.Errorf("project = %+v", p)
	}

	tests := map[string]error{
		"monitoring": ErrUnmanaged,
		"nope":       ErrNotFound,
		"empty":      ErrNotFound,
		"../app":     ErrInvalidName,
	}
	for name, want := range tests {
		if _, err := m.Target(context.Background(), name); !errors.Is(err, want) {
			t.Errorf("Target(%q) err = %v, want %v", name, err, want)
		}
	}
}

// volumeDocker adds volumes to fakeDocker.
type volumeDocker struct {
	fakeDocker
	volumes []string
}

func (v volumeDocker) ListProjectVolumes(context.Context, string) ([]string, error) {
	return v.volumes, nil
}

func TestGetVersionAndVolumes(t *testing.T) {
	dir := stacksDir(t, "app")
	m := newManager(t, dir, volumeDocker{volumes: []string{"app_data"}}, fakeCompose{})
	d, err := m.Get(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	// The version stackfiles checks when saving; stacksDir writes this content.
	if want := stackfiles.Version([]byte("services: {}\n")); d.ComposeFile.Version != want {
		t.Errorf("version = %q, want %q", d.ComposeFile.Version, want)
	}
	if len(d.Volumes) != 1 || d.Volumes[0] != "app_data" {
		t.Errorf("volumes = %v", d.Volumes)
	}
	// Without volume support there are simply none.
	d, _ = newManager(t, dir, fakeDocker{}, fakeCompose{}).Get(context.Background(), "app")
	if d.Volumes == nil || len(d.Volumes) != 0 {
		t.Errorf("volumes without lister = %v", d.Volumes)
	}
}

func TestRemove(t *testing.T) {
	dir := stacksDir(t, "app")
	writeFile(t, filepath.Join(dir, "app", "data", "db.sqlite"), "data")
	m := newManager(t, dir, fakeDocker{}, fakeCompose{})
	if err := m.Remove("app"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "app")); !errors.Is(err, os.ErrNotExist) {
		t.Error("stack directory still exists")
	}
	if err := m.Remove("app"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second remove: %v", err)
	}
	for _, bad := range []string{"..", "../x", ""} {
		if err := m.Remove(bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Remove(%q) = %v", bad, err)
		}
	}
	// A symlink in the stacks directory is never followed.
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "keep"), "x")
	os.Symlink(target, filepath.Join(dir, "linked"))
	if err := m.Remove("linked"); err == nil {
		t.Error("removing a symlinked stack should fail")
	}
	if _, err := os.Stat(filepath.Join(target, "keep")); err != nil {
		t.Error("the symlink target was deleted")
	}
}

func TestRemoveKeepsComposeFileUntilLast(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can delete read-only directories")
	}
	dir := stacksDir(t, "app")
	locked := filepath.Join(dir, "app", "data", "locked")
	writeFile(t, filepath.Join(locked, "db"), "data")
	os.Chmod(locked, 0o500)
	t.Cleanup(func() { os.Chmod(locked, 0o700) })

	if err := newManager(t, dir, fakeDocker{}, fakeCompose{}).Remove("app"); err == nil {
		t.Fatal("Remove succeeded although data couldn't be deleted")
	}
	// The stack is still a stack, so the delete can be retried.
	if _, err := os.Stat(filepath.Join(dir, "app", "compose.yaml")); err != nil {
		t.Errorf("compose file gone after a failed remove: %v", err)
	}
}

func TestSymlinksAreNotManaged(t *testing.T) {
	dir := stacksDir(t, "real")
	outside := normPath(t.TempDir())
	writeFile(t, filepath.Join(outside, "compose.yaml"), "services: {}\n")
	os.Symlink(outside, filepath.Join(dir, "linked"))
	// A real directory whose Compose file is a link.
	writeFile(t, filepath.Join(dir, "linkfile", "README.md"), "x\n")
	os.Symlink(filepath.Join(outside, "compose.yaml"), filepath.Join(dir, "linkfile", "compose.yaml"))
	m := newManager(t, dir, fakeDocker{}, fakeCompose{})

	l, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Stacks) != 1 || l.Stacks[0].Name != "real" {
		t.Errorf("stacks = %+v, want only real", l.Stacks)
	}
	joined := strings.Join(l.Warnings, "\n")
	if !strings.Contains(joined, `"linked": a symbolic link`) || !strings.Contains(joined, "compose.yaml is a symbolic link") {
		t.Errorf("warnings = %v", l.Warnings)
	}
	for _, name := range []string{"linked", "linkfile"} {
		if _, err := m.Target(context.Background(), name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Target(%s) = %v, want ErrNotFound", name, err)
		}
	}
}

func TestUnmanagedComposeFileSymlinkIsNotRead(t *testing.T) {
	ext := normPath(t.TempDir())
	secret := filepath.Join(ext, "secret.yaml")
	writeFile(t, secret, "password: hunter2\n")
	link := filepath.Join(ext, "compose.yaml")
	os.Symlink(secret, link)
	c := ctr("ext", "x", "running", ext)
	c.Labels[docker.LabelConfigFiles] = link
	m := newManager(t, stacksDir(t), fakeDocker{containers: []docker.Container{c}}, fakeCompose{})

	got, err := m.Get(context.Background(), "ext")
	if err != nil {
		t.Fatal(err)
	}
	if got.ComposeFile.Content != nil {
		t.Errorf("content of a symlinked Compose file returned: %q", *got.ComposeFile.Content)
	}
}

func TestComposeFileTooLarge(t *testing.T) {
	dir := normPath(t.TempDir())
	writeFile(t, filepath.Join(dir, "big", "compose.yaml"), strings.Repeat("#", maxComposeFileSize+1))
	got, err := newManager(t, dir, fakeDocker{}, fakeCompose{}).Get(context.Background(), "big")
	if err != nil {
		t.Fatal(err)
	}
	if got.ComposeFile.Content != nil || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "larger than") {
		t.Errorf("content = %v, warnings = %v", got.ComposeFile.Content != nil, got.Warnings)
	}
}

func TestNewRejectsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	writeFile(t, file, "x")
	if _, err := New(file, fakeDocker{}, fakeCompose{}); err == nil {
		t.Error("New with a file: want error")
	}
}

func TestMissingStacksDirectory(t *testing.T) {
	missing := filepath.Join(normPath(t.TempDir()), "missing")
	d := fakeDocker{containers: []docker.Container{ctr("monitoring", "grafana", "running", "/srv/monitoring")}}
	m := newManager(t, missing, d, fakeCompose{})
	if m.DirExists() {
		t.Fatal("DirExists = true for a missing directory")
	}

	l, err := m.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v, want no error for a missing directory", err)
	}
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0], "does not exist") {
		t.Errorf("warnings = %v, want missing directory warning", l.Warnings)
	}
	// Unmanaged stacks are still found.
	if len(l.Stacks) != 1 || l.Stacks[0].Name != "monitoring" || l.Stacks[0].Managed {
		t.Errorf("stacks = %+v, want only unmanaged monitoring", l.Stacks)
	}

	if _, err := m.Get(context.Background(), "monitoring"); err != nil {
		t.Errorf("Get unmanaged with missing directory: %v", err)
	}

	// Once the directory is created, its stacks show up.
	writeFile(t, filepath.Join(missing, "app", "compose.yaml"), "services: {}\n")
	if l, _ := m.List(context.Background()); len(l.Stacks) != 2 || len(l.Warnings) != 0 {
		t.Errorf("after creating the directory: stacks = %+v, warnings = %v", l.Stacks, l.Warnings)
	}
}
