// Package stackfiles reads and writes the files of managed stacks: the
// Compose file and the .env file.
//
// All access goes through an os.Root on the stack directory, so no path can
// lead outside it, even if files are swapped for links meanwhile. Symbolic
// links are never read, written or followed. Writes are atomic (temporary
// file, fsync, rename) and guarded by versions: a writer sends the version it
// read and gets ErrConflict if the file has changed since. .env values only
// leave this package one at a time through RevealEnv.
package stackfiles

import (
	"context"
	"crypto/rand"
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

// errChanged means a file or directory was replaced while it was opened.
var errChanged = errors.New("changed while it was opened")

// openDir opens dir as an os.Root. dir itself must be a real directory, not
// a symbolic link; its parent may be reached through links (configuration).
func openDir(dir string) (*os.Root, error) {
	parent, err := os.OpenRoot(filepath.Dir(dir))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	name := filepath.Base(dir)
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, ErrSymlink
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", name)
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	// Opened what was checked, not something swapped in meanwhile.
	if opened, err := root.Stat("."); err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, fmt.Errorf("%s: %w", name, errChanged)
	}
	return root, nil
}

// fileIn returns the name of file within dir; the agent only works on files
// directly in a stack directory.
func fileIn(dir, file string) (string, error) {
	if filepath.Dir(file) != filepath.Clean(dir) {
		return "", fmt.Errorf("%s is not in the stack directory", file)
	}
	return filepath.Base(file), nil
}

// readFile reads the regular file name in root, of at most MaxSize. A
// missing file returns nil content and no error.
func readFile(root *os.Root, name string) ([]byte, os.FileMode, error) {
	info, err := root.Lstat(name)
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
		return nil, 0, fmt.Errorf("%s is not a regular file", name)
	}
	if info.Size() > MaxSize {
		return nil, 0, ErrTooLarge
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	if opened, err := file.Stat(); err != nil || !os.SameFile(info, opened) {
		return nil, 0, fmt.Errorf("%s: %w", name, errChanged)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxSize+1))
	if err != nil {
		return nil, 0, err
	}
	if len(data) > MaxSize {
		return nil, 0, ErrTooLarge
	}
	if data == nil {
		data = []byte{}
	}
	return data, info.Mode().Perm(), nil
}

// writeNew writes content to the new file name in root, failing if it
// exists, with exactly mode (not reduced by the umask).
func writeNew(root *os.Root, name string, content []byte, mode os.FileMode) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(content)
	if err == nil {
		err = file.Chmod(mode)
	}
	if err == nil {
		err = file.Sync()
	}
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		root.Remove(name)
	}
	return err
}

// writeTemp writes content to a new hidden temporary file in root and
// returns its name.
func writeTemp(root *os.Root, prefix, suffix string, content []byte, mode os.FileMode) (string, error) {
	name := prefix + rand.Text() + suffix
	return name, writeNew(root, name, content, mode)
}

// replace moves tmp over name in root and syncs the directory, so the change
// survives a crash. Renaming replaces a symbolic link at name, never its
// target.
func replace(root *os.Root, tmp, name string) error {
	if err := root.Rename(tmp, name); err != nil {
		root.Remove(tmp)
		return err
	}
	if d, err := root.Open("."); err == nil {
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
	name, err := fileIn(p.Dir, p.File)
	if err != nil {
		return "", err
	}
	root, err := openDir(p.Dir)
	if err != nil {
		return "", err
	}
	defer root.Close()

	current, mode, err := readFile(root, name)
	if err != nil {
		return "", err
	}
	if current == nil {
		return "", fmt.Errorf("compose file %s: %w", name, fs.ErrNotExist)
	}
	if Version(current) != version {
		return "", ErrConflict
	}

	tmp, err := writeTemp(root, ".gantry-compose-", ".yaml", content, mode)
	if err != nil {
		return "", err
	}
	check := compose.Project{Name: p.Name, Dir: p.Dir, File: filepath.Join(p.Dir, tmp)}
	if err := f.validator.Validate(ctx, check, name); err != nil {
		root.Remove(tmp)
		return "", err
	}
	if err := replace(root, tmp, name); err != nil {
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
	data, err := readEnv(p)
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
	root, err := openDir(p.Dir)
	if err != nil {
		return EnvInfo{}, err
	}
	defer root.Close()
	data, mode, err := readFile(root, EnvFileName)
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
		// Lines without a variable (comments, blank lines) have no key;
		// an invalid one must not match them.
		if !ValidKey(key) {
			return EnvInfo{}, &InvalidVariableError{Key: key, Reason: "not a valid name (letters, digits and _, not starting with a digit)"}
		}
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

	tmp, err := writeTemp(root, ".gantry-env-", "", out, mode)
	if err != nil {
		return EnvInfo{}, err
	}
	if err := replace(root, tmp, EnvFileName); err != nil {
		return EnvInfo{}, err
	}
	return envInfo(out), nil
}

// RevealEnv returns one variable's value as written in the .env file
// (quotes removed, escapes decoded, variables not expanded).
func (f *Files) RevealEnv(p compose.Project, key string) (string, error) {
	data, err := readEnv(p)
	if err != nil {
		return "", err
	}
	v, ok := parseEnv(data).value(key)
	if !ok {
		return "", ErrVariableNotFound
	}
	return v, nil
}

// readEnv reads a stack's .env file; nil if there is none.
func readEnv(p compose.Project) ([]byte, error) {
	root, err := openDir(p.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, _, err := readFile(root, EnvFileName)
	return data, err
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
	root, err := os.OpenRoot(stacksDir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if _, err := root.Lstat(name); err == nil {
		return "", ErrExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	staging := ".gantry-new-" + name + "-" + rand.Text()
	if err := root.Mkdir(staging, 0o700); err != nil {
		return "", err
	}
	defer root.RemoveAll(staging) // a no-op after the rename
	if err := root.Chmod(staging, 0o755); err != nil {
		return "", err
	}
	dir, err := root.OpenRoot(staging)
	if err != nil {
		return "", err
	}
	defer dir.Close()

	if err := writeNew(dir, ComposeFileName, composeContent, 0o644); err != nil {
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
		if err := writeNew(dir, EnvFileName, file.bytes(), 0o600); err != nil {
			return "", err
		}
	}
	stagingDir := filepath.Join(stacksDir, staging)
	check := compose.Project{Name: name, Dir: stagingDir, File: filepath.Join(stagingDir, ComposeFileName)}
	if err := f.validator.Validate(ctx, check, ComposeFileName); err != nil {
		return "", err
	}

	// Check again: the name may have been taken meanwhile. Rename never
	// replaces a non-empty directory; an empty one created in this instant
	// would be replaced, which loses nothing.
	if _, err := root.Lstat(name); err == nil {
		return "", ErrExists
	}
	if err := root.Rename(staging, name); err != nil {
		return "", err
	}
	return final, nil
}
