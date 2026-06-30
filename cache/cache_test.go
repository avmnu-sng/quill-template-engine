package cache

import (
	"testing"

	"github.com/avmnusng/quill-template-engine/ast"
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
