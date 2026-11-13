// Package stacks discovers Compose stacks on the host and their state.
//
// Managed stacks are the subdirectories of the stacks directory that contain a
// Compose file. Other Compose projects on the host are found through the
// labels Compose puts on their containers and reported as unmanaged.
package stacks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/docker"
	"github.com/pixl-media/gantry/agent/internal/stackfiles"
)

var (
	// ErrNotFound means there is no stack with the requested name.
	ErrNotFound = errors.New("stack not found")
	// ErrInvalidName means the name is not a valid Compose project name.
	ErrInvalidName = errors.New("invalid stack name")
	// ErrUnmanaged means the stack is a Compose project outside the stacks
	// directory. gantry shows it but doesn't change it.
	ErrUnmanaged = errors.New("stack is not managed by gantry")
)

// DefaultDir is the default stacks directory. It's the same as Dockge's, and
// so is the layout (one directory per stack), so gantry picks up existing
// Dockge stacks without configuration.
const DefaultDir = "/opt/stacks"

// composeFileNames are the default Compose file names, in the order Compose
// itself looks for them.
var composeFileNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// maxComposeFileSize limits how much of a Compose file the API returns.
const maxComposeFileSize = 1 << 20

// maxParallelCompose limits concurrent `docker compose` processes.
const maxParallelCompose = 8

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ValidName reports whether name is a valid Compose project name. Valid names
// can't contain path separators or dots, so they are safe as directory names.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// Status summarizes whether a stack's services are running.
type Status string

const (
	StatusRunning Status = "running" // every service has a running container
	StatusPartial Status = "partial" // some services have a running container
	StatusStopped Status = "stopped" // no service has a running container
	StatusUnknown Status = "unknown" // Docker is unreachable
)

// Summary is a stack in the stack list.
type Summary struct {
	Name            string
	Managed         bool
	Status          Status
	ServicesTotal   int
	ServicesRunning int
	Path            string
}

// List is the result of Manager.List.
type List struct {
	Stacks          []Summary
	DockerAvailable bool
	Warnings        []string
}

// Detail is a single stack with its services and Compose file.
type Detail struct {
	Summary
	ComposeFile     *ComposeFile // nil if unknown
	Services        []Service
	Volumes         []string // named volumes of the Compose project
	DockerAvailable bool
	Warnings        []string
}

// ComposeFile is a stack's Compose file, as stored on disk.
type ComposeFile struct {
	Path    string
	Content *string // nil if the agent can't read it
	Version string  // version of Content, to send back when saving
}

// VolumeLister lists a Compose project's volumes. If the ContainerLister
// passed to New implements it (docker.Client does), details include volumes.
type VolumeLister interface {
	ListProjectVolumes(ctx context.Context, project string) ([]string, error)
}

// Service is a Compose service and its containers.
type Service struct {
	Name       string
	Containers []docker.Container
}

// ContainerLister lists the containers of all Compose projects.
type ContainerLister interface {
	ListComposeContainers(ctx context.Context) ([]docker.Container, error)
}

// ServiceLister lists the services a Compose file defines.
type ServiceLister interface {
	Services(ctx context.Context, dir, file string) ([]string, error)
}

// Manager finds stacks in a stacks directory and on the Docker host.
type Manager struct {
	dir     string
	docker  ContainerLister
	compose ServiceLister
}

// New returns a Manager for the stacks directory dir. The directory may not
// exist yet: then there are no managed stacks, and List and Get report it as a
// warning, while unmanaged stacks are still found. An existing path that isn't
// a directory is an error.
func New(dir string, d ContainerLister, c ServiceLister) (*Manager, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Allowed; see above.
	case err != nil:
		return nil, fmt.Errorf("stacks directory: %w", err)
	case !info.IsDir():
		return nil, fmt.Errorf("stacks directory %s is not a directory", abs)
	}
	return &Manager{dir: normPath(abs), docker: d, compose: c}, nil
}

// Dir returns the absolute stacks directory.
func (m *Manager) Dir() string { return m.dir }

// DirExists reports whether the stacks directory exists.
func (m *Manager) DirExists() bool {
	_, err := os.Stat(m.dir)
	return err == nil
}

// List returns all managed and unmanaged stacks, sorted by name.
func (m *Manager) List(ctx context.Context) (List, error) {
	snap, err := m.collect(ctx, "")
	if err != nil {
		return List{}, err
	}
	list := List{DockerAvailable: snap.dockerAvailable, Warnings: snap.warnings, Stacks: []Summary{}}
	for _, st := range snap.stacks {
		list.Stacks = append(list.Stacks, st.summary(snap.dockerAvailable))
		for _, w := range st.warnings {
			list.Warnings = append(list.Warnings, st.name+": "+w)
		}
	}
	return list, nil
}

// Get returns the stack called name.
func (m *Manager) Get(ctx context.Context, name string) (Detail, error) {
	if !ValidName(name) {
		return Detail{}, ErrInvalidName
	}
	snap, err := m.collect(ctx, name)
	if err != nil {
		return Detail{}, err
	}
	if len(snap.stacks) == 0 {
		return Detail{}, ErrNotFound
	}
	st := snap.stacks[0]
	d := Detail{
		Summary:         st.summary(snap.dockerAvailable),
		DockerAvailable: snap.dockerAvailable,
		Warnings:        append(slices.Clone(snap.warnings), st.warnings...),
		Services:        st.servicesWithContainers(),
	}
	if st.composeFile != "" {
		content, warning := readComposeFile(st.composeFile)
		d.ComposeFile = &ComposeFile{Path: st.composeFile, Content: content}
		if content != nil {
			// The same version stackfiles compares when saving.
			d.ComposeFile.Version = stackfiles.Version([]byte(*content))
		}
		if warning != "" {
			d.Warnings = append(d.Warnings, warning)
		}
	}
	d.Volumes = []string{}
	if vl, ok := m.docker.(VolumeLister); ok && snap.dockerAvailable {
		if volumes, err := vl.ListProjectVolumes(ctx, name); err == nil {
			d.Volumes = volumes
		}
	}
	return d, nil
}

// Remove permanently deletes the directory of the managed stack called name.
// Callers must have stopped the stack first (the delete operation runs
// `down` before this).
func (m *Manager) Remove(name string) error {
	if !ValidName(name) {
		return ErrInvalidName
	}
	dir := filepath.Join(m.dir, name)
	// ValidName rules out separators and dots, so dir is a direct child of
	// the stacks directory; still, never follow a symlink out of it.
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("stack %s: not a directory", name)
	}
	return os.RemoveAll(dir)
}

// Target returns the Compose project of the managed stack called name, to run
// actions on. It returns ErrUnmanaged for Compose projects outside the stacks
// directory and ErrNotFound if there is no such stack at all.
func (m *Manager) Target(ctx context.Context, name string) (compose.Project, error) {
	if !ValidName(name) {
		return compose.Project{}, ErrInvalidName
	}
	managed, _, err := m.scan(name)
	if err != nil {
		return compose.Project{}, err
	}
	if len(managed) == 1 {
		st := managed[0]
		return compose.Project{Name: st.name, Dir: st.path, File: st.composeFile}, nil
	}
	containers, err := m.docker.ListComposeContainers(ctx)
	if err == nil && slices.ContainsFunc(containers, func(c docker.Container) bool {
		return c.Labels[docker.LabelProject] == name
	}) {
		return compose.Project{}, ErrUnmanaged
	}
	return compose.Project{}, ErrNotFound
}

// stack is the internal, merged view of one stack.
type stack struct {
	name        string
	managed     bool
	path        string
	composeFile string             // "" if unknown
	services    []string           // defined services, sorted
	containers  []docker.Container // excluding one-off containers
	warnings    []string
}

type snapshot struct {
	stacks          []*stack // sorted by name
	dockerAvailable bool
	warnings        []string
}

// collect merges the stacks directory with the Docker state. If only is not
// empty, it returns at most the stack with that name.
func (m *Manager) collect(ctx context.Context, only string) (snapshot, error) {
	var snap snapshot

	managed, warnings, err := m.scan(only)
	if err != nil {
		return snap, err
	}
	snap.warnings = warnings

	containers, err := m.docker.ListComposeContainers(ctx)
	snap.dockerAvailable = err == nil
	if err != nil {
		snap.warnings = append(snap.warnings, fmt.Sprintf("docker unavailable: %v", err))
	}

	byProject := map[string][]docker.Container{}
	for _, c := range containers {
		if c.Labels[docker.LabelOneoff] == "True" {
			continue // `docker compose run` containers aren't part of the stack
		}
		p := c.Labels[docker.LabelProject]
		byProject[p] = append(byProject[p], c)
	}

	for _, st := range managed {
		st.containers = byProject[st.name]
		delete(byProject, st.name)
		if other := foreignWorkingDirs(st); len(other) > 0 {
			st.warnings = append(st.warnings, fmt.Sprintf(
				"the Compose project name is also used by containers from %s", strings.Join(other, ", ")))
		}
	}
	m.loadServices(ctx, managed)
	snap.stacks = managed

	for project, cs := range byProject {
		if only != "" && project != only {
			continue
		}
		if !ValidName(project) {
			snap.warnings = append(snap.warnings, fmt.Sprintf("ignoring Compose project %q: not a valid project name", project))
			continue
		}
		snap.stacks = append(snap.stacks, unmanaged(project, cs))
	}

	slices.SortFunc(snap.stacks, func(a, b *stack) int { return strings.Compare(a.name, b.name) })
	return snap, nil
}

// scan reads the managed stacks from the stacks directory.
func (m *Manager) scan(only string) ([]*stack, []string, error) {
	if !m.DirExists() {
		return nil, []string{fmt.Sprintf("stacks directory %s does not exist", m.dir)}, nil
	}
	var names []string
	if only != "" {
		names = []string{only}
	} else {
		entries, err := os.ReadDir(m.dir)
		if err != nil {
			return nil, nil, fmt.Errorf("reading stacks directory: %w", err)
		}
		for _, e := range entries {
			names = append(names, e.Name())
		}
	}

	var stacks []*stack
	var warnings []string
	for _, name := range names {
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(m.dir, name)
		info, err := os.Stat(path) // follows symlinks
		if err != nil || !info.IsDir() {
			continue
		}
		if !ValidName(name) {
			warnings = append(warnings, fmt.Sprintf("skipping directory %q: not a valid Compose project name (lowercase letters, digits, - and _)", name))
			continue
		}
		file := findComposeFile(path)
		if file == "" {
			warnings = append(warnings, fmt.Sprintf("skipping directory %q: no Compose file", name))
			continue
		}
		stacks = append(stacks, &stack{name: name, managed: true, path: path, composeFile: file})
	}
	return stacks, warnings, nil
}

// loadServices reads the defined services of managed stacks via Compose, in
// parallel. On failure it falls back to the services seen in containers.
func (m *Manager) loadServices(ctx context.Context, stacks []*stack) {
	sem := make(chan struct{}, maxParallelCompose)
	var wg sync.WaitGroup
	for _, st := range stacks {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			services, err := m.compose.Services(ctx, st.path, st.composeFile)
			if err != nil {
				st.warnings = append(st.warnings, fmt.Sprintf("reading services: %v", err))
				services = containerServices(st.containers)
			}
			st.services = services
		})
	}
	wg.Wait()
}

func unmanaged(project string, cs []docker.Container) *stack {
	st := &stack{name: project, containers: cs, services: containerServices(cs)}
	st.path = cs[0].Labels[docker.LabelWorkingDir]
	if files := cs[0].Labels[docker.LabelConfigFiles]; files != "" {
		list := strings.Split(files, ",")
		st.composeFile = list[0]
		if len(list) > 1 {
			st.warnings = append(st.warnings, fmt.Sprintf("uses %d Compose files; showing %s", len(list), list[0]))
		}
	}
	return st
}

// foreignWorkingDirs returns the working directories of a managed stack's
// containers that aren't the stack's own directory.
func foreignWorkingDirs(st *stack) []string {
	var dirs []string
	for _, c := range st.containers {
		wd := c.Labels[docker.LabelWorkingDir]
		if wd != "" && normPath(wd) != st.path && !slices.Contains(dirs, wd) {
			dirs = append(dirs, wd)
		}
	}
	return dirs
}

func (st *stack) summary(dockerAvailable bool) Summary {
	services := st.allServices()
	running := 0
	for _, s := range services {
		if slices.ContainsFunc(st.containers, func(c docker.Container) bool {
			return c.Labels[docker.LabelService] == s && c.State == "running"
		}) {
			running++
		}
	}
	return Summary{
		Name:            st.name,
		Managed:         st.managed,
		Status:          status(len(services), running, dockerAvailable),
		ServicesTotal:   len(services),
		ServicesRunning: running,
		Path:            st.path,
	}
}

// allServices returns the defined services plus services that only exist as
// containers (e.g. removed from the Compose file but still running), sorted.
func (st *stack) allServices() []string {
	all := slices.Clone(st.services)
	for _, s := range containerServices(st.containers) {
		if !slices.Contains(all, s) {
			all = append(all, s)
		}
	}
	slices.Sort(all)
	return all
}

func (st *stack) servicesWithContainers() []Service {
	services := []Service{}
	for _, name := range st.allServices() {
		s := Service{Name: name, Containers: []docker.Container{}}
		for _, c := range st.containers {
			if c.Labels[docker.LabelService] == name {
				s.Containers = append(s.Containers, c)
			}
		}
		slices.SortFunc(s.Containers, func(a, b docker.Container) int { return strings.Compare(a.Name, b.Name) })
		services = append(services, s)
	}
	return services
}

func status(total, running int, dockerAvailable bool) Status {
	switch {
	case !dockerAvailable:
		return StatusUnknown
	case running == 0:
		return StatusStopped
	case running == total:
		return StatusRunning
	default:
		return StatusPartial
	}
}

func containerServices(cs []docker.Container) []string {
	var services []string
	for _, c := range cs {
		if s := c.Labels[docker.LabelService]; s != "" && !slices.Contains(services, s) {
			services = append(services, s)
		}
	}
	slices.Sort(services)
	return services
}

func findComposeFile(dir string) string {
	for _, name := range composeFileNames {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// readComposeFile returns the raw content of a Compose file, or nil and a
// warning if it can't or shouldn't be returned.
func readComposeFile(path string) (*string, string) {
	if ext := filepath.Ext(path); ext != ".yaml" && ext != ".yml" {
		return nil, fmt.Sprintf("not returning %s: not a .yaml or .yml file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Sprintf("can't read Compose file: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxComposeFileSize+1))
	if err != nil {
		return nil, fmt.Sprintf("can't read Compose file: %v", err)
	}
	if len(data) > maxComposeFileSize {
		return nil, fmt.Sprintf("not returning %s: larger than %d bytes", path, maxComposeFileSize)
	}
	content := string(data)
	return &content, ""
}

// normPath resolves symlinks where possible, so that paths from Docker labels
// compare equal to paths on disk (e.g. /var vs /private/var on macOS).
func normPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
