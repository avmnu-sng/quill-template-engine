package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileSubcommandWritesGeneratedSource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hello.ql", "Hello, {{ name | upper }}!\n")

	var out bytes.Buffer
	err := dispatch([]string{
		"compile", "-root", dir, "-pkg", "mytpl", "-func", "RenderHello", "hello.ql",
	}, &out, os.Stderr, strings.NewReader(""))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	src := out.String()
	for _, want := range []string{
		"package mytpl",
		"func RenderHello(w io.Writer, exts *ext.ExtensionSet, vars map[string]runtime.Value) error {",
		"var RenderHelloManifest = &compiled.Manifest{",
		"Fingerprint: compiled.Fingerprint{AutoescapeHTML: false, LenientVariables: false, TabWidth: 4, RandomSeed: 0, RandomSeedSet: false},",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated source missing %q", want)
		}
	}
}

func TestCompileSubcommandOptionFlags(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "t.ql", "{{ x }}")

	var out bytes.Buffer
	err := runCompile([]string{
		"-root", dir, "-autoescape", "html", "-strict=false",
		"-tabwidth", "2", "-seed", "0", "t.ql",
	}, &out)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// A -seed of zero is a deliberate seed: RandomSeedSet must be true.
	want := "Fingerprint: compiled.Fingerprint{AutoescapeHTML: true, LenientVariables: true, TabWidth: 2, RandomSeed: 0, RandomSeedSet: true},"
	if !strings.Contains(out.String(), want) {
		t.Errorf("generated source missing %q", want)
	}
}

func TestCompileSubcommandOutputFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "t.ql", "hi")
	genPath := filepath.Join(dir, "t_gen.go")

	var out bytes.Buffer
	if err := runCompile([]string{"-root", dir, "-o", genPath, "t.ql"}, &out); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("-o must suppress stdout, got %d bytes", out.Len())
	}
	b, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if !strings.Contains(string(b), "var RenderManifest = &compiled.Manifest{") {
		t.Error("generated file missing the manifest")
	}
}

func TestCompileSubcommandReportsNotCompilable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "t.ql", "@include \"other.ql\"\n")

	var out bytes.Buffer
	err := runCompile([]string{"-root", dir, "t.ql"}, &out)
	if err == nil || !strings.Contains(err.Error(), "not compilable") {
		t.Fatalf("expected a not-compilable error, got %v", err)
	}
}

func TestCompileSubcommandRequiresOneTemplate(t *testing.T) {
	var out bytes.Buffer
	if err := runCompile([]string{"-root", t.TempDir()}, &out); err == nil {
		t.Fatal("expected an argument-count error")
	}
}
