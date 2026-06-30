package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a tiny test helper that writes a file under dir and fails the
// test on any I/O error.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunRendersTemplate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hello.ql", "Hello, {{ name | upper }}!\n")
	writeFile(t, dir, "data.json", `{"name":"ada"}`)

	var out bytes.Buffer
	err := run([]string{
		"-root", dir,
		"-data", filepath.Join(dir, "data.json"),
		"hello.ql",
	}, &out, strings.NewReader(""))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.String() != "Hello, ADA!\n" {
		t.Errorf("output: %q", out.String())
	}
}

func TestRunInheritanceFromDisk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.ql", "BEGIN\n@block body {\nX\n@}\nEND\n")
	writeFile(t, dir, "page.ql", "@extends \"base.ql\"\n@block body {\n{{ msg }}\n@}\n")
	writeFile(t, dir, "data.json", `{"msg":"hi"}`)

	var out bytes.Buffer
	err := run([]string{"-root", dir, "-data", filepath.Join(dir, "data.json"), "page.ql"}, &out, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "hi") || !strings.Contains(out.String(), "BEGIN") {
		t.Errorf("inheritance output: %q", out.String())
	}
}

func TestRunDataFromStdin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "t.ql", "n={{ n }}")
	var out bytes.Buffer
	err := run([]string{"-root", dir, "-data", "-", "t.ql"}, &out, strings.NewReader(`{"n":7}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.String() != "n=7" {
		t.Errorf("stdin data: %q", out.String())
	}
}

func TestRunAutoescapeHTML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "t.ql", "{{ v }}")
	writeFile(t, dir, "d.json", `{"v":"<b>"}`)
	var out bytes.Buffer
	if err := run([]string{"-root", dir, "-data", filepath.Join(dir, "d.json"), "-autoescape", "html", "t.ql"}, &out, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.String() != "&lt;b&gt;" {
		t.Errorf("html escape: %q", out.String())
	}
}

func TestRunNoDataUsesEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "t.ql", "{{ x ?? \"default\" }}")
	var out bytes.Buffer
	if err := run([]string{"-root", dir, "t.ql"}, &out, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.String() != "default" {
		t.Errorf("no-data render: %q", out.String())
	}
}

func TestRunErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "t.ql", "ok")

	cases := []struct {
		name string
		args []string
	}{
		{"missing template name", []string{"-root", dir}},
		{"too many args", []string{"-root", dir, "a.ql", "b.ql"}},
		{"unknown autoescape", []string{"-root", dir, "-autoescape", "js", "t.ql"}},
		{"missing template file", []string{"-root", dir, "nope.ql"}},
		{"bad data file", []string{"-root", dir, "-data", filepath.Join(dir, "absent.json"), "t.ql"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := run(c.args, &out, nil); err == nil {
				t.Errorf("expected an error, got output %q", out.String())
			}
		})
	}
}

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-version"}, &out, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(out.String(), "quill ") {
		t.Errorf("version output: %q", out.String())
	}
}
