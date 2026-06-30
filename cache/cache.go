// Package cache memoizes parsed templates by name so a template referenced many
// times (a base layout, a shared macro file) is lexed and parsed once. The cache
// stores the parsed *ast.Node module; the engine wraps it into its render-time
// Template structure on top.
//
// The cache is intentionally small: an in-memory map guarded by a mutex, with no
// eviction. A long-lived process that loads an unbounded set of templates from
// strings should clear it; the common case (a fixed template directory) caches
// the whole working set and never grows.
package cache

import (
	"sync"

	"github.com/avmnusng/quill-template-engine/ast"
)

// Cache is a concurrency-safe name->module memo.
type Cache struct {
	mu      sync.RWMutex
	modules map[string]*ast.Node
}

// New returns an empty cache.
func New() *Cache {
	return &Cache{modules: map[string]*ast.Node{}}
}

// Get returns the cached module for name, if present.
func (c *Cache) Get(name string) (*ast.Node, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.modules[name]
	return m, ok
}

// Put stores the parsed module under name.
func (c *Cache) Put(name string, mod *ast.Node) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.modules[name] = mod
}

// Clear empties the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.modules = map[string]*ast.Node{}
}
