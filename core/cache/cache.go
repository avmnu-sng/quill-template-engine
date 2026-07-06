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

	"github.com/avmnu-sng/quill-template-engine/core/ast"
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

// RenderCache memoizes rendered body fragments by key, backing the @cache region
// statement (spec 01 Section 4.7). It is the engine-default, in-memory pluggable
// cache: a key->string map guarded by a mutex with no eviction. The ttl is a
// documented no-op here (the in-memory cache never expires an entry); a host that
// needs ttl/tags-driven invalidation supplies its own implementation. Tags are
// recorded per key so an invalidation by tag can drop every keyed entry that
// carried it.
type RenderCache struct {
	mu      sync.RWMutex
	entries map[string]string
	byTag   map[string]map[string]struct{} // tag -> set of keys carrying it
}

// NewRenderCache returns an empty render cache.
func NewRenderCache() *RenderCache {
	return &RenderCache{
		entries: map[string]string{},
		byTag:   map[string]map[string]struct{}{},
	}
}

// Get returns the cached rendered body for key, if present.
func (c *RenderCache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.entries[key]
	return s, ok
}

// Put stores a rendered body under key, recording its tags for later
// tag-invalidation. ttl is accepted for API symmetry but is a no-op for this
// non-expiring in-memory cache.
func (c *RenderCache) Put(key string, body string, tags []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = body
	for _, tag := range tags {
		set := c.byTag[tag]
		if set == nil {
			set = map[string]struct{}{}
			c.byTag[tag] = set
		}
		set[key] = struct{}{}
	}
}

// InvalidateTag drops every cached entry that was stored carrying tag.
func (c *RenderCache) InvalidateTag(tag string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.byTag[tag] {
		delete(c.entries, key)
	}
	delete(c.byTag, tag)
}

// Clear empties the render cache.
func (c *RenderCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]string{}
	c.byTag = map[string]map[string]struct{}{}
}
