package cache

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/ast"
)

func TestCachePutGet(t *testing.T) {
	c := New()
	if _, ok := c.Get("x"); ok {
		t.Fatal("empty cache should miss")
	}
	mod := &ast.Node{Kind: ast.KindModule}
	c.Put("x", mod)
	got, ok := c.Get("x")
	if !ok || got != mod {
		t.Fatalf("cache did not return stored module")
	}
	c.Clear()
	if _, ok := c.Get("x"); ok {
		t.Fatal("Clear should empty the cache")
	}
}

func TestRenderCachePutGet(t *testing.T) {
	c := NewRenderCache()
	if _, ok := c.Get("k"); ok {
		t.Fatal("empty render cache should miss")
	}
	c.Put("k", "body", nil)
	if got, ok := c.Get("k"); !ok || got != "body" {
		t.Fatalf("render cache did not return stored body: %q ok=%v", got, ok)
	}
	c.Clear()
	if _, ok := c.Get("k"); ok {
		t.Fatal("Clear should empty the render cache")
	}
}

func TestRenderCacheTagInvalidation(t *testing.T) {
	c := NewRenderCache()
	c.Put("a", "A", []string{"nav", "footer"})
	c.Put("b", "B", []string{"footer"})
	c.Put("c", "C", nil)

	c.InvalidateTag("footer") // drops a and b, leaves c
	if _, ok := c.Get("a"); ok {
		t.Error("a tagged footer should be invalidated")
	}
	if _, ok := c.Get("b"); ok {
		t.Error("b tagged footer should be invalidated")
	}
	if got, ok := c.Get("c"); !ok || got != "C" {
		t.Errorf("c (untagged) should survive: %q ok=%v", got, ok)
	}
}
