package stacks

import (
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/stackfiles"
)

// MissingAccess is access to a path that the agent lacks.
type MissingAccess struct {
	Path  string
	Write bool // false: read
	// ReadOnlyFilesystem is set if the path is on a read-only file system
	// (for example through systemd's ProtectSystem); changing its owner
	// doesn't help then.
	ReadOnlyFilesystem bool
}

// FileAccess is what the agent may not read or write, as User.
type FileAccess struct {
	User    string
	Missing []MissingAccess
}

// Access is a managed stack's FileAccess and what it allows.
type Access struct {
	FileAccess
	// Editable: the agent may write the stack's directory and read its
	// Compose file and .env. Files are replaced through a temporary file,
	// never written in place, so they need not be writable themselves.
	Editable bool
	// Deletable: Editable, and the agent may write the stacks directory.
	// The directory's contents (data of the stack's containers) aren't
	// checked.
	Deletable bool
}

// access(2) modes.
const (
	accessRead  = 4
	accessWrite = 2
	accessExec  = 1
)

// accessFunc is syscall.Access, replaced in tests.
var accessFunc = syscall.Access

// User returns the name of the user the agent runs as, or its numeric ID if
// it has none.
var User = sync.OnceValue(func() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return strconv.Itoa(os.Getuid())
})

// missing checks path with access(2), which also honors ACLs. A directory
// needs search permission too for writing into it. Other errors, such as a
// missing path, aren't reported here: they show up where the path is used.
func missing(path string, write bool) (MissingAccess, bool) {
	mode := uint32(accessRead)
	if write {
		mode = accessWrite | accessExec
	}
	err := accessFunc(path, mode)
	switch {
	case err == nil:
		return MissingAccess{}, false
	case errors.Is(err, syscall.EROFS):
		return MissingAccess{Path: path, Write: write, ReadOnlyFilesystem: true}, true
	case errors.Is(err, fs.ErrPermission):
		return MissingAccess{Path: path, Write: write}, true
	}
	return MissingAccess{}, false
}

// dirAccess returns the missing write access to the stacks directory. If it
// doesn't exist yet, creating a stack creates it, so its nearest existing
// parent counts.
func (m *Manager) dirAccess() []MissingAccess {
	dir := m.dir
	for {
		if _, err := os.Stat(dir); err == nil || filepath.Dir(dir) == dir {
			break
		}
		dir = filepath.Dir(dir)
	}
	if ma, ok := missing(dir, true); ok {
		return []MissingAccess{ma}
	}
	return nil
}

// stackAccess returns the missing access to a managed stack's directory,
// Compose file and .env (if there is one).
func stackAccess(dir, composeFile string) []MissingAccess {
	var out []MissingAccess
	if ma, ok := missing(dir, true); ok {
		out = append(out, ma)
	}
	if composeFile != "" {
		if ma, ok := missing(composeFile, false); ok {
			out = append(out, ma)
		}
	}
	env := filepath.Join(dir, stackfiles.EnvFileName)
	if _, err := os.Lstat(env); err == nil {
		if ma, ok := missing(env, false); ok {
			out = append(out, ma)
		}
	}
	return out
}

func (m *Manager) access(dir, composeFile string) Access {
	common := m.dirAccess()
	own := stackAccess(dir, composeFile)
	return Access{
		FileAccess: FileAccess{User: User(), Missing: append(common, own...)},
		Editable:   len(own) == 0,
		Deletable:  len(own) == 0 && len(common) == 0,
	}
}

// StacksDirAccess returns the access to the stacks directory; stacks can be
// created only if nothing is missing.
func (m *Manager) StacksDirAccess() FileAccess {
	return FileAccess{User: User(), Missing: m.dirAccess()}
}

// Access returns the access to the managed stack p, as resolved by Target.
func (m *Manager) Access(p compose.Project) Access {
	return m.access(p.Dir, p.File)
}

// unreadable returns the paths in ma that the agent may not read.
func unreadable(ma []MissingAccess) []string {
	var paths []string
	for _, a := range ma {
		if !a.Write {
			paths = append(paths, a.Path)
		}
	}
	return paths
}
