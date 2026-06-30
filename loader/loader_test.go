package loader

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArrayLoader(t *testing.T) {
	l := NewArrayLoader(map[string]string{"a.ql": "hello"})
	if !l.Exists("a.ql") {
		t.Fatal("a.ql should exist")
	}
	if l.Exists("missing.ql") {
		t.Fatal("missing.ql should not exist")
	}
	src, err := l.Get("a.ql")
	if err != nil {
		t.Fatal(err)
	}
	if src.Code() != "hello" || src.Name() != "a.ql" {
		t.Errorf("got name=%q code=%q", src.Name(), src.Code())
	}
	_, err = l.Get("missing.ql")
	if !IsNotFound(err) {
		t.Errorf("expected not-found, got %v", err)
	}
	// Set adds incrementally.
	l.Set("b.ql", "world")
	if !l.Exists("b.ql") {
		t.Error("b.ql should exist after Set")
	}
}

func TestFilesystemLoader(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.ql"), []byte("PAGE"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewFilesystemLoader(dir)
	if !l.Exists("page.ql") {
		t.Fatal("page.ql should exist")
	}
	src, err := l.Get("page.ql")
	if err != nil {
		t.Fatal(err)
	}
	if src.Code() != "PAGE" {
		t.Errorf("code = %q", src.Code())
	}
	if _, err := l.Get("nope.ql"); !IsNotFound(err) {
		t.Errorf("expected not-found, got %v", err)
	}
}

// TestFilesystemEscape verifies a "../" name cannot read outside the root.
func TestFilesystemEscape(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(filepath.Dir(dir), "secret.txt")
	_ = os.WriteFile(secret, []byte("SECRET"), 0o644)
	defer os.Remove(secret)
	l := NewFilesystemLoader(dir)
	if l.Exists("../secret.txt") {
		t.Error("loader must not see files outside its root")
	}
	if _, err := l.Get("../secret.txt"); err == nil {
		t.Error("loader must reject an escaping path")
	}
}
