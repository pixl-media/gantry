package stacks

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/pixl-media/gantry/agent/internal/compose"
)

// graphTTL is how long a cached graph is used at most. Its key covers the
// Compose file and .env; files those pull in (include:, extends, env_file)
// can change unseen, so entries also expire.
const graphTTL = time.Minute

// graphCache remembers each managed stack's services and dependencies, so
// that the stack list doesn't start a Compose process per stack on every
// refresh.
type graphCache struct {
	mu      sync.Mutex
	entries map[string]graphEntry // by project directory
	now     func() time.Time
}

type graphEntry struct {
	key   cacheKey
	graph compose.Graph
	at    time.Time
}

// cacheKey identifies the files a graph was read from. Saving a file (an
// atomic rename) changes its modification time.
type cacheKey struct {
	file    string
	compose fileStamp
	env     fileStamp
}

type fileStamp struct {
	size int64
	mod  time.Time
	ok   bool // the file exists
}

func stamp(path string) fileStamp {
	info, err := os.Lstat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{size: info.Size(), mod: info.ModTime(), ok: true}
}

func keyFor(p compose.Project) cacheKey {
	return cacheKey{file: p.File, compose: stamp(p.File), env: stamp(filepath.Join(p.Dir, ".env"))}
}

func (c *graphCache) get(p compose.Project, key cacheKey) (compose.Graph, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[p.Dir]
	if !ok || e.key != key || c.now().Sub(e.at) >= graphTTL {
		return nil, false
	}
	return e.graph, true
}

func (c *graphCache) put(p compose.Project, key cacheKey, g compose.Graph) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]graphEntry{}
	}
	c.entries[p.Dir] = graphEntry{key: key, graph: g, at: c.now()}
}

func (c *graphCache) forget(dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, dir)
}

// graph returns p's services and dependencies, from the cache unless fresh
// is set. Errors are not cached.
func (m *Manager) graph(ctx context.Context, loader GraphLoader, p compose.Project, fresh bool) (compose.Graph, error) {
	key := keyFor(p)
	if !fresh {
		if g, ok := m.cache.get(p, key); ok {
			return g, nil
		}
	}
	g, err := loader.Graph(ctx, p)
	if err != nil {
		return nil, err
	}
	m.cache.put(p, key, g)
	return g, nil
}

// Forget drops what is cached about the managed stack name, after its files
// were changed through the API.
func (m *Manager) Forget(name string) {
	if ValidName(name) {
		m.cache.forget(filepath.Join(m.dir, name)) // as scan builds it
	}
}

// services returns the sorted service names of p: from the dependency
// graph (cached) if the Compose runner can read one, else from Services.
func (m *Manager) services(ctx context.Context, p compose.Project) ([]string, error) {
	loader, ok := m.compose.(GraphLoader)
	if !ok {
		return m.compose.Services(ctx, p)
	}
	g, err := m.graph(ctx, loader, p, false)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(g))
	for name := range g {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}
