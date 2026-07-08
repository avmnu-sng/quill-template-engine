package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateGolden regenerates the committed cmd/quill golden files when set
// (`go test ./cmd/quill -run TestCLIGolden -update`). It is off by default so a
// normal test run only compares against the committed goldens. The flag name
// matches the repo-wide golden convention (see coverage_golden_test.go); the
// cmd/quill test binary is a separate package, so registering it here does not
// collide with the root package's flag of the same name.
var updateGolden = flag.Bool("update", false, "update cmd/quill golden files")

// These golden tests CHARACTERIZE the cmd/quill CLI as the stable public surface
// for AOT compilation and coverage; they PIN behavior, they do not change it. Any
// mismatch is a deliberate decision to record, not a test to relax: regenerate
// with -update only when the CLI contract is intentionally revised.

// compareGolden asserts got matches the committed golden file at
// testdata/<name>, or rewrites it under -update. The bytes are compared exactly
// (including trailing whitespace and newlines), which is the point of a
// characterization golden.
func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create): %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s mismatch (run with -update to regenerate)\n--- got ----\n%s\n--- want ---\n%s",
			name, got, want)
	}
}

// normalizePath rewrites an absolute temp-dir path to the stable placeholder
// <ROOT> so a golden that quotes a filesystem path (an OS error string) stays
// machine-independent. Nothing else in the CLI's output embeds an absolute path:
// templates are named relatively (page.ql), reports carry relative names, and the
// generated source embeds only the template's own text.
func normalizePath(s, dir string) string {
	// Absolute temp dir -> stable placeholder, in both its native and
	// forward-slash spellings (an OS error quotes the path in native form).
	s = strings.ReplaceAll(s, dir, "<ROOT>")
	s = strings.ReplaceAll(s, filepath.ToSlash(dir), "<ROOT>")
	// Windows quotes the path with backslash separators and reports a missing
	// file with a different message than Unix; fold both to the Unix forms the
	// goldens record so a path-quoting error golden stays machine-independent.
	s = strings.ReplaceAll(s, `<ROOT>\`, "<ROOT>/")
	s = strings.ReplaceAll(s, "The system cannot find the file specified.", "no such file or directory")
	return s
}

// representativeTemplate exercises the compilable subset broadly enough to pin a
// realistic generated-source shape: a filtered interpolation (name | upper), a
// fully covered @if/@else, and a bare interpolation (count). It is deliberately
// small so the generated golden stays reviewable, yet it touches the render
// entry, the manifest, and the emit/undefined helper paths the frozen
// compiled.RenderFunc ABI depends on.
const representativeTemplate = "Hello, {{ name | upper }}!\n" +
	"@if admin {\n" +
	"You are an admin.\n" +
	"@} @else {\n" +
	"Regular user.\n" +
	"@}\n" +
	"Total: {{ count }}\n"

// writeTemplate writes the representative template into a fresh temp dir and
// returns the dir. Every golden test roots its loader here.
func writeTemplate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "page.ql", representativeTemplate)
	return dir
}

// --- Generated Go source shape ------------------------------------------------

// TestCLIGoldenCompileSource pins the full generated Go source for the
// representative template. The generated file must match the frozen
// compiled.RenderFunc / compiled.Fingerprint ABI: the render signature, the
// NewManifest/NewFingerprint construction, and the helper set the backend emits.
// The output is deterministic (seed 0, no timestamps, template text embedded
// verbatim, helpers in a fixed order) so the golden is stable across runs and
// machines.
func TestCLIGoldenCompileSource(t *testing.T) {
	dir := writeTemplate(t)
	var out bytes.Buffer
	if err := runCompile([]string{"-root", dir, "-pkg", "qtpl", "-func", "Render", "page.ql"}, &out); err != nil {
		t.Fatalf("compile: %v", err)
	}
	compareGolden(t, "compile/page_gen.go.golden", out.Bytes())
}

// --- cover -cases JSON schema -------------------------------------------------

// TestCLIGoldenCoverCasesSchema pins the -cases JSON input schema by feeding a
// representative cases file through the cover subcommand and pinning the text
// report it produces. The committed testdata/cover/cases.json IS the schema
// fixture: a JSON array of {template, data} objects, exactly the coverCase shape
// the CLI decodes. Pinning both the fixture and the report it yields freezes the
// input contract and its observable effect together.
func TestCLIGoldenCoverCasesSchema(t *testing.T) {
	dir := writeTemplate(t)
	// The committed cases fixture is the pinned schema. Under -update we (re)write
	// it from the canonical text; otherwise we read the committed fixture. Either
	// way it is copied in beside the template so the loader resolves the name.
	var casesGolden []byte
	if *updateGolden {
		casesGolden = []byte(canonicalCasesJSON)
		compareGolden(t, "cover/cases.json", casesGolden)
	} else {
		var err error
		casesGolden, err = os.ReadFile(filepath.Join("testdata", "cover", "cases.json"))
		if err != nil {
			t.Fatalf("read cases fixture (run with -update to create): %v", err)
		}
	}
	casesPath := filepath.Join(dir, "cases.json")
	if err := os.WriteFile(casesPath, casesGolden, 0o644); err != nil {
		t.Fatalf("write cases file: %v", err)
	}

	var out, errOut bytes.Buffer
	if err := runCover([]string{"-root", dir, "-cases", casesPath, "-format", "text"}, &out, &errOut, nil); err != nil {
		t.Fatalf("cover cases: %v (stderr %q)", err, errOut.String())
	}
	compareGolden(t, "cover/cases_report.txt.golden", out.Bytes())
}

// canonicalCasesJSON is the -cases schema fixture written under -update: a JSON
// array of {template, data} case objects. The single case fires both the @if
// then-arm and every interpolation, so the report it yields has full unit
// coverage and one uncovered branch (the never-false @else).
const canonicalCasesJSON = `[
  {
    "template": "page.ql",
    "data": {
      "name": "ada",
      "admin": true,
      "count": 3
    }
  }
]
`

// --- cover output formats: text / lcov / html ---------------------------------

// coverArgsFor runs the cover subcommand for one format against the
// representative template with a single inline case and returns stdout. The case
// takes the @if then-arm so unit and line coverage are full and the @else branch
// stays uncovered -- enough signal to pin every column and marker in each format.
func coverStdout(t *testing.T, dir, format string) []byte {
	t.Helper()
	dataPath := filepath.Join(dir, "data.json")
	writeFile(t, dir, "data.json", `{"name":"ada","admin":true,"count":3}`)
	var out, errOut bytes.Buffer
	if err := runCover([]string{"-root", dir, "-data", dataPath, "-format", format, "page.ql"},
		&out, &errOut, nil); err != nil {
		t.Fatalf("cover -format %s: %v (stderr %q)", format, err, errOut.String())
	}
	return out.Bytes()
}

// TestCLIGoldenCoverFormats pins the text, lcov, and html coverage outputs for
// the representative template. All three are deterministic (regions sort by
// name/position, no timestamps, relative template names) so the golden bytes are
// stable.
func TestCLIGoldenCoverFormats(t *testing.T) {
	for _, tc := range []struct {
		format string
		golden string
	}{
		{"text", "cover/report.txt.golden"},
		{"lcov", "cover/report.lcov.golden"},
		{"html", "cover/report.html.golden"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			dir := writeTemplate(t)
			compareGolden(t, tc.golden, coverStdout(t, dir, tc.format))
		})
	}
}

// --- Usage text (stderr) ------------------------------------------------------

// TestCLIGoldenUsage pins the usage text of each entry point: the render default,
// the compile subcommand, and the cover subcommand. Usage enumerates every flag
// with its default, so this golden freezes the subcommand set and each
// subcommand's flag surface (compile: -pkg/-func/-root/...; cover: its flags
// including -fail-under and its -threshold alias). The flag package prints flags
// in a fixed (alphabetical) order, so the output is deterministic.
//
// The usage text is captured from the real code paths, not a reconstructed flag
// set: run and runCompile write their flag set's usage to os.Stderr (captured via
// an os.Pipe redirect), while runCover writes to the errOut writer it is handed.
// Triggering each with an argument-count or -h error makes the flag package emit
// the actual pinned usage.
func TestCLIGoldenUsage(t *testing.T) {
	dir := writeTemplate(t)

	// Render usage: an argument-count error triggers fs.Usage() to os.Stderr.
	t.Run("render", func(t *testing.T) {
		got, err := captureStderr(t, func() error {
			var out bytes.Buffer
			return run([]string{"-root", dir}, &out, nil)
		})
		if err == nil {
			t.Fatal("expected an argument-count error")
		}
		compareGolden(t, "usage/render.txt.golden", normalize(got, dir))
	})

	// Compile usage: same argument-count trigger, usage to os.Stderr.
	t.Run("compile", func(t *testing.T) {
		got, err := captureStderr(t, func() error {
			var out bytes.Buffer
			return runCompile([]string{"-root", dir}, &out)
		})
		if err == nil {
			t.Fatal("expected an argument-count error")
		}
		compareGolden(t, "usage/compile.txt.golden", normalize(got, dir))
	})

	// Cover usage: runCover writes its flag set's usage to the errOut buffer.
	// -h makes the flag package print usage and return flag.ErrHelp.
	t.Run("cover", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if err := runCover([]string{"-h"}, &out, &errOut, nil); err == nil {
			t.Fatal("expected -h to return an error")
		}
		compareGolden(t, "usage/cover.txt.golden", normalize(errOut.Bytes(), dir))
	})
}

// captureStderr redirects os.Stderr to a pipe for the duration of fn, returning
// everything fn wrote there and fn's error. run and runCompile hardcode their
// flag set's output to os.Stderr, so this captures their real usage text without
// reconstructing the flag registration.
func captureStderr(t *testing.T, fn func() error) ([]byte, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	fnErr := fn()
	os.Stderr = orig
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	_ = r.Close()
	return buf.Bytes(), fnErr
}

// normalize rewrites the temp dir to <ROOT> and returns bytes, so a golden that
// might quote a path stays machine-independent.
func normalize(b []byte, dir string) []byte {
	return []byte(normalizePath(string(b), dir))
}

// --- Error message shapes (stderr) --------------------------------------------

// TestCLIGoldenErrors pins the error-message text (as main would print it,
// "quill: <err>\n") for the main failure modes across all three entry points.
// Absolute temp paths are normalized to <ROOT>. These strings are part of the
// CLI contract a CI log and a human both read.
func TestCLIGoldenErrors(t *testing.T) {
	dir := writeTemplate(t)
	dataPath := filepath.Join(dir, "data.json")
	writeFile(t, dir, "data.json", `{"name":"ada","admin":true,"count":3}`)
	writeFile(t, dir, "inc.ql", "@include \"missing.ql\"\n")

	cases := []struct {
		name   string
		run    func(out, errOut *bytes.Buffer) error
		golden string
	}{
		{
			name: "render_missing_template",
			run: func(out, errOut *bytes.Buffer) error {
				return run([]string{"-root", dir, "nope.ql"}, out, nil)
			},
			golden: "errors/render_missing_template.txt.golden",
		},
		{
			name: "render_missing_data",
			run: func(out, errOut *bytes.Buffer) error {
				return run([]string{"-root", dir, "-data", filepath.Join(dir, "absent.json"), "page.ql"}, out, nil)
			},
			golden: "errors/render_missing_data.txt.golden",
		},
		{
			name: "render_bad_autoescape",
			run: func(out, errOut *bytes.Buffer) error {
				return run([]string{"-root", dir, "-autoescape", "js", "page.ql"}, out, nil)
			},
			golden: "errors/render_bad_autoescape.txt.golden",
		},
		{
			name: "compile_not_compilable",
			run: func(out, errOut *bytes.Buffer) error {
				return runCompile([]string{"-root", dir, "inc.ql"}, out)
			},
			golden: "errors/compile_not_compilable.txt.golden",
		},
		{
			name: "cover_unknown_format",
			run: func(out, errOut *bytes.Buffer) error {
				return runCover([]string{"-root", dir, "-data", dataPath, "-format", "xml", "page.ql"}, out, errOut, nil)
			},
			golden: "errors/cover_unknown_format.txt.golden",
		},
		{
			name: "cover_both_cases_and_name",
			run: func(out, errOut *bytes.Buffer) error {
				return runCover([]string{"-root", dir, "-cases", dataPath, "page.ql"}, out, errOut, nil)
			},
			golden: "errors/cover_both_cases_and_name.txt.golden",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			err := c.run(&out, &errOut)
			if err == nil {
				t.Fatalf("expected an error, got stdout %q", out.String())
			}
			// main prints "quill: <err>\n"; pin exactly that line.
			line := "quill: " + err.Error() + "\n"
			compareGolden(t, c.golden, normalize([]byte(line), dir))
		})
	}
}

// --- Exit-code contract -------------------------------------------------------

// TestCLIGoldenExitCodes pins the CLI's exit-code model through exitStatus -- the
// exact reduction main performs (main itself cannot be called without exiting the
// test process). The observable contract: success, including a PASSING coverage
// gate, is 0; an UNMET coverage gate is 2, distinct so a CI job can tell "coverage
// too low" from a real failure; every other error (a template not found, a
// not-compilable template) is 1.
func TestCLIGoldenExitCodes(t *testing.T) {
	dir := writeTemplate(t)
	dataPath := filepath.Join(dir, "data.json")
	writeFile(t, dir, "data.json", `{"name":"ada","admin":true,"count":3}`)
	writeFile(t, dir, "cases_fail.json",
		`[{"template":"page.ql","data":{"name":"ada","admin":false,"count":3}}]`)
	writeFile(t, dir, "inc.ql", "@include \"missing.ql\"\n")

	cases := []struct {
		name string
		run  func(out, errOut *bytes.Buffer) error
		want int
	}{
		{
			name: "render_success",
			run: func(out, errOut *bytes.Buffer) error {
				return run([]string{"-root", dir, "-data", dataPath, "page.ql"}, out, nil)
			},
			want: 0,
		},
		{
			name: "cover_gate_pass",
			run: func(out, errOut *bytes.Buffer) error {
				return runCover([]string{"-root", dir, "-data", dataPath, "-fail-under", "100", "page.ql"}, out, errOut, nil)
			},
			want: 0,
		},
		{
			name: "compile_success",
			run: func(out, errOut *bytes.Buffer) error {
				return runCompile([]string{"-root", dir, "page.ql"}, out)
			},
			want: 0,
		},
		{
			name: "cover_gate_fail",
			run: func(out, errOut *bytes.Buffer) error {
				return runCover([]string{"-root", dir, "-cases", filepath.Join(dir, "cases_fail.json"), "-fail-under", "100"}, out, errOut, nil)
			},
			want: 2,
		},
		{
			name: "render_hard_error",
			run: func(out, errOut *bytes.Buffer) error {
				return run([]string{"-root", dir, "nope.ql"}, out, nil)
			},
			want: 1,
		},
		{
			name: "compile_hard_error",
			run: func(out, errOut *bytes.Buffer) error {
				return runCompile([]string{"-root", dir, "inc.ql"}, out)
			},
			want: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			got := exitStatus(c.run(&out, &errOut))
			if got != c.want {
				t.Errorf("exit code = %d, want %d (stderr %q)", got, c.want, errOut.String())
			}
		})
	}
}
