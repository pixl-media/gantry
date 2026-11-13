// Package stackfiles reads and writes the files of managed stacks: the
// Compose file and the .env file.
//
// Writes are atomic (temporary file, fsync, rename), never follow symbolic
// links, and are guarded by versions: a writer sends the version it read and
// gets ErrConflict if the file has changed since. .env values only leave
// this package one at a time through RevealEnv.
package stackfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/pixl-media/gantry/agent/internal/compose"
)

// MaxSize is the largest file the agent reads or writes.
const MaxSize = 1 << 20

// EnvFileName is the name of a stack's variables file.
const EnvFileName = ".env"

// ComposeFileName is used for new stacks.
const ComposeFileName = "compose.yaml"

var (
	// ErrConflict means the file changed since the version the caller read.
	ErrConflict = errors.New("the file has changed since it was read")
	// ErrTooLarge means a file or content exceeds MaxSize.
	ErrTooLarge = fmt.Errorf("larger than %d bytes", MaxSize)
	// ErrSymlink means the file is a symbolic link, which isn't edited.
	ErrSymlink = errors.New("refusing to edit a symbolic link")
	// ErrVariableNotFound means the .env file has no such variable.
	ErrVariableNotFound = errors.New("no such variable")
	// ErrExists means a stack directory with that name exists.
	ErrExists = errors.New("a stack with this name exists")
)

// Validator checks a Compose file; compose.CLI implements it.
type Validator interface {
	Validate(ctx context.Context, p compose.Project, displayName string) error
}

// Files reads and writes stack files.
type Files struct {
	validator Validator

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New returns Files that validate Compose files with v.
func New(v Validator) *Files {
	return &Files{validator: v, locks: map[string]*sync.Mutex{}}
}

// lock serializes writes per stack directory.
func (f *Files) lock(dir string) func() {
	f.mu.Lock()
	l, ok := f.locks[dir]
	if !ok {
		l = &sync.Mutex{}
		f.locks[dir] = l
	}
	f.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Version returns the version of file content; "" stands for a missing file.
func Version(content []byte) string {
	if content == nil {
		return ""
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// readFile reads a regular file of at most MaxSize. A missing file returns
// nil content and no error.
func readFile(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, 0, ErrSymlink
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	if info.Size() > MaxSize {
		return nil, 0, ErrTooLarge
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxSize+1))
	if err != nil {
		return nil, 0, err
	}
	if data == nil {
		data = []byte{}
	}
	return data, info.Mode().Perm(), nil
}

// writeTemp writes content to a new hidden temporary file in dir.
func writeTemp(dir, pattern string, content []byte, mode os.FileMode) (string, error) {
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	_, err = tmp.Write(content)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// replace moves tmp over path and syncs the directory, so the change
// survives a crash.
func replace(tmp, path string) error {
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// ReplaceCompose validates content and, if the Compose file is still at
// version, atomically replaces it. It returns the new version, or
// ErrConflict, a *compose.InvalidError, ErrTooLarge or ErrSymlink.
func (f *Files) ReplaceCompose(ctx context.Context, p compose.Project, version string, content []byte) (string, error) {
	if len(content) > MaxSize {
		return "", ErrTooLarge
	}
	defer f.lock(p.Dir)()

	current, mode, err := readFile(p.File)
	if err != nil {
		return "", err
	}
	if current == nil {
		return "", fmt.Errorf("compose file %s: %w", filepath.Base(p.File), fs.ErrNotExist)
	}
	if Version(current) != version {
		return "", ErrConflict
	}

	tmp, err := writeTemp(p.Dir, ".gantry-compose-*.yaml", content, mode)
	if err != nil {
		return "", err
	}
	check := compose.Project{Name: p.Name, Dir: p.Dir, File: tmp}
	if err := f.validator.Validate(ctx, check, filepath.Base(p.File)); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := replace(tmp, p.File); err != nil {
		return "", err
	}
	return Version(content), nil
}

// EnvInfo describes a stack's .env file without its values.
type EnvInfo struct {
	Exists  bool
	Version string
	Keys    []string
}

// Env returns the variable names of a stack's .env file.
func (f *Files) Env(p compose.Project) (EnvInfo, error) {
	data, _, err := readFile(filepath.Join(p.Dir, EnvFileName))
	if err != nil {
		return EnvInfo{}, err
	}
	return envInfo(data), nil
}

func envInfo(data []byte) EnvInfo {
	keys := parseEnv(data).keys()
	if keys == nil {
		keys = []string{}
	}
	return EnvInfo{Exists: data != nil, Version: Version(data), Keys: keys}
}

// UpdateEnv sets and deletes variables if the .env file is still at version
// ("" for a file that doesn't exist yet), keeping the rest of the file as
// it is. A new file gets mode 0600.
func (f *Files) UpdateEnv(p compose.Project, version string, set map[string]string, del []string) (EnvInfo, error) {
	defer f.lock(p.Dir)()
	path := filepath.Join(p.Dir, EnvFileName)
	data, mode, err := readFile(path)
	if err != nil {
		return EnvInfo{}, err
	}
	if Version(data) != version {
		return EnvInfo{}, ErrConflict
	}
	if data == nil {
		mode = 0o600
	}

	env := parseEnv(data)
	for _, key := range del {
		env.remove(key)
	}
	// Sorted, so new variables are appended in a stable order.
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := env.set(k, set[k]); err != nil {
			return EnvInfo{}, err
		}
	}
	out := env.bytes()
	if len(out) > MaxSize {
		return EnvInfo{}, ErrTooLarge
	}

	tmp, err := writeTemp(p.Dir, ".gantry-env-*", out, mode)
	if err != nil {
		return EnvInfo{}, err
	}
	if err := replace(tmp, path); err != nil {
		return EnvInfo{}, err
	}
	return envInfo(out), nil
}

// RevealEnv returns one variable's value as written in the .env file
// (quotes removed, escapes decoded, variables not expanded).
func (f *Files) RevealEnv(p compose.Project, key string) (string, error) {
	data, _, err := readFile(filepath.Join(p.Dir, EnvFileName))
	if err != nil {
		return "", err
	}
	v, ok := parseEnv(data).value(key)
	if !ok {
		return "", ErrVariableNotFound
	}
	return v, nil
}

// Create creates the stack directory stacksDir/name with a Compose file and,
// if env isn't empty, a .env file (0600). It works in a hidden directory
// first and renames it into place only after the Compose file validated, so
// a failed create leaves nothing behind.
func (f *Files) Create(ctx context.Context, stacksDir, name string, composeContent []byte, env map[string]string) (string, error) {
	if len(composeContent) > MaxSize {
		return "", ErrTooLarge
	}
	final := filepath.Join(stacksDir, name)
	defer f.lock(final)()

	if err := os.MkdirAll(stacksDir, 0o755); err != nil {
		return "", err
	}
	if _, err := os.Lstat(final); err == nil {
		return "", ErrExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	staging, err := os.MkdirTemp(stacksDir, ".gantry-new-"+name+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging) // a no-op after the rename
	if err := os.Chmod(staging, 0o755); err != nil {
		return "", err
	}

	composePath := filepath.Join(staging, ComposeFileName)
	if err := os.WriteFile(composePath, composeContent, 0o644); err != nil {
		return "", err
	}
	if len(env) > 0 {
		file := parseEnv(nil)
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := file.set(k, env[k]); err != nil {
				return "", err
			}
		}
		if err := os.WriteFile(filepath.Join(staging, EnvFileName), file.bytes(), 0o600); err != nil {
			return "", err
		}
	}
	check := compose.Project{Name: name, Dir: staging, File: composePath}
	if err := f.validator.Validate(ctx, check, ComposeFileName); err != nil {
		return "", err
	}

	// Rename fails if the name was taken meanwhile; never overwrite.
	if _, err := os.Lstat(final); err == nil {
		return "", ErrExists
	}
	if err := os.Rename(staging, final); err != nil {
		return "", err
	}
	return final, nil
}
