package compile_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/compile"
	"github.com/avmnu-sng/quill-template-engine/parse"
	"github.com/avmnu-sng/quill-template-engine/source"
)

// emitCases is the differential battery for typed emission and the tab-free
// writer elision: each case renders through the compiled path and must match
// the facade byte-for-byte (output or error text). It walks every emission
// shape the print lowering distinguishes -- the static-Int direct write, the
// Str/Safe guard, the emit-helper fallback for the remaining kind spellings
// and their authoritative errors, prints under an active strategy, and all of
// them on both sides of the whole-module tab-free split.
var emitCases = []compiledCase{
	// Static-Int direct writes: literal digits across sign and width, and the
	// inline loop fields spanning the two-to-three-digit boundary at 100.
	{name: "emit-int-literals", template: "{{ 0 }}|{{ 7 }}|{{ -42 }}|{{ 100 }}|{{ 9223372036854775807 }}|{{ -9223372036854775807 }}\n"},
	{name: "emit-int-100-loop", template: "@for i in (1..100) {\n{{ loop.index }}:{{ loop.index0 }}:{{ loop.revindex }}:{{ loop.revindex0 }}:{{ loop.length }};\n@}\n"},
	// A materialized loop keeps the value read; the guard's fallback arm
	// spells the Int exactly like qemit.
	{name: "emit-int-materialized-loop", template: "@for i in [1,2] {\n@set snap = loop\n{{ loop.index }}/{{ snap.index }}\n@}\n"},

	// The guard's fallback arm: Float/Bool/Null spellings, and the Array and
	// Object print behavior (error text and position for the array; the
	// Stringify hook for a host object).
	{name: "emit-float-bool-null", template: "{{ 1.0 }}|{{ 2.5 }}|{{ true }}|{{ false }}|[{{ null }}]\n"},
	{name: "emit-array-error", template: "before\n{{ xs }}", varsJSON: `{"xs":[1,2]}`},
	{name: "emit-array-error-in-loop", template: "@for x in [1] {\nok\n{{ xs }}\n@}\n", varsJSON: `{"xs":[1]}`},
	{name: "emit-object-print", template: "@set c = cell(7)\n{{ c }}\n"},

	// The Str/Safe guard arm: raw output under off, Safe under an active html
	// strategy (unchanged qemit path), and an @escape off region inside an
	// html module flipping the site back to the guard.
	{name: "emit-safe-off", template: "{{ v | raw }}|{{ v }}\n", varsJSON: `{"v":"<a&b>"}`},
	{name: "emit-safe-html", template: "{{ v | raw }}|{{ v }}\n", varsJSON: `{"v":"<a&b>"}`, opts: compile.Options{AutoescapeHTML: true}},
	{name: "emit-escape-off-region", template: "@escape off {\n{{ v | raw }}|{{ v }}|{{ 42 }}\n@}\n{{ v }}\n", varsJSON: `{"v":"<x>"}`, opts: compile.Options{AutoescapeHTML: true}},
	{name: "emit-int-under-html", template: "@for x in [1] {\n{{ loop.index }}|{{ 7 }}\n@}\n", opts: compile.Options{AutoescapeHTML: true}},

	// @tab regions: the static-Int write and the guard both flow through the
	// qWriter indent layer, including a region entered mid-line.
	{name: "emit-tab-loop-index", template: "@tab(1) {\n@for x in [\"a\",\"b\"] {\n{{ loop.index }}. {{ x }}\n@}\n@}\n"},
	{name: "emit-tab-int-literal", template: "head{{ \"\" -}}\n@tab(2) {\n{{ 5 }}|{{ s }}\nplain\n@}\n", varsJSON: `{"s":"v"}`},

	// Capture bodies write into their builder on both module shapes.
	{name: "emit-capture-tabfree", template: "@set b = capture {\n{{ 42 }}|{{ s }}\n@}\n[{{ b }}]\n", varsJSON: `{"s":"z"}`},
	{name: "emit-capture-with-tab", template: "@set b = capture {\n{{ 42 }}|{{ s }}\n@}\n@tab(1) {\n{{ b }}\n@}\n", varsJSON: `{"s":"z"}`},

	// The hoisted injection flag: a needs-context function inside a loop
	// materializes the same per-iteration _context, and injection-free
	// filters skip the residue without observable change.
	{name: "emit-dump-in-loop", template: "@for x in [1,2] {\n@set y = x * 10\n{{ dump() }}\n@}\n"},
	{name: "emit-inject-free-filter-args", template: "@for x in [3,1] {\n{{ [x, 9] | join(\"-\") }}\n@}\n"},
	{name: "emit-needs-env-filter", template: "{{ \"a\\nb\" | tab(1) }}\n", opts: compile.Options{TabWidth: 8}},
}

// TestTypedEmissionParity renders the emission battery through the compiled
// path and asserts byte-equality (output or error text) against the facade.
func TestTypedEmissionParity(t *testing.T) {
	results := map[string]*compile.Result{}
	for _, cs := range emitCases {
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cs.name, err)
		}
		results[cs.name] = res
	}
	got := runCompiled(t, emitCases, results)
	for _, cs := range emitCases {
		r, ok := got[cs.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", cs.name)
			continue
		}
		wantOut, wantErr := renderInterp(t, cs)
		if wantErr != nil {
			if !r.failed {
				t.Errorf("%s: interp errored (%v) but compiled rendered %q", cs.name, wantErr, r.out)
				continue
			}
			if r.errText != wantErr.Error() {
				t.Errorf("%s: error text mismatch\n got  %q\n want %q", cs.name, r.errText, wantErr.Error())
			}
			continue
		}
		if r.failed {
			t.Errorf("%s: compiled errored (%s) but interp rendered %q", cs.name, r.errText, wantOut)
			continue
		}
		if r.out != wantOut {
			t.Errorf("%s: output mismatch\n got  %q\n want %q", cs.name, r.out, wantOut)
		}
	}
}

// renderPortion cuts a generated file down to the part ahead of the fixed
// helper prelude, so shape assertions cannot match the prelude's own code.
func renderPortion(t *testing.T, src []byte) string {
	t.Helper()
	marker := "// qWriter is the output layer"
	i := strings.Index(string(src), marker)
	if i < 0 {
		t.Fatalf("generated source lacks the prelude marker")
	}
	return string(src[:i])
}

// mustCompileSrc compiles one template and returns the generated source.
func mustCompileSrc(t *testing.T, name, body string) []byte {
	t.Helper()
	mod, err := parse.Parse(source.New(name, body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := compile.Module(name, mod, compile.Options{PackageName: pkgName(name)})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return res.Source
}

// TestTabFreeCodegenSplit pins the whole-module writer decision: a module
// with no @tab region writes through io.WriteString with no qWriter
// constructed in the render function, while one @tab region anywhere keeps
// every write on the qWriter layer; the strconv import appears exactly when a
// static-Int direct write was emitted.
func TestTabFreeCodegenSplit(t *testing.T) {
	free := renderPortion(t, mustCompileSrc(t, "free.ql",
		"@for x in [1] {\n{{ loop.index }}\n@}\n@set b = capture {\n{{ s }}\n@}\n{{ b }}\n"))
	if strings.Contains(free, "&qWriter{") {
		t.Error("tab-free module still constructs a qWriter")
	}
	if !strings.Contains(free, "io.WriteString(w, ") {
		t.Error("tab-free module does not write through io.WriteString")
	}
	if !strings.Contains(free, "strconv.FormatInt(") {
		t.Error("tab-free module lacks the static-Int direct write")
	}
	if !strings.Contains(free, "\"strconv\"") {
		t.Error("static-Int module does not import strconv")
	}

	tabbed := renderPortion(t, mustCompileSrc(t, "tabbed.ql",
		"@tab(1) {\n@for x in [1] {\n{{ loop.index }}\n@}\n@}\n"))
	if !strings.Contains(tabbed, "qw := &qWriter{w: w, atLineStart: true}") {
		t.Error("tab-containing module does not construct the qWriter")
	}
	if strings.Contains(tabbed, "io.WriteString(w, ") {
		t.Error("tab-containing module bypasses the qWriter layer")
	}
	if !strings.Contains(tabbed, "strconv.FormatInt(") {
		t.Error("tab-containing module lost the static-Int direct write")
	}

	noInt := mustCompileSrc(t, "noint.ql", "{{ s }}\n")
	if strings.Contains(renderPortion(t, noInt), "strconv.FormatInt(") {
		t.Error("module without a static-Int print emitted a FormatInt write")
	}
	if bytes.Contains(noInt, []byte("\"strconv\"")) {
		t.Error("module without a static-Int print imports strconv")
	}
}

// TestNeedsContextFilterInjectParity pins the hoisted injection flag against
// a host filter that requests context injection: the compiled render must
// hand the filter the same _context mapping (names, order, and values) the
// interpreter builds, per iteration, from inside a loop frame. The filter is
// registered on the shared ExtensionSet both engines resolve through, exactly
// as a host would.
func TestNeedsContextFilterInjectParity(t *testing.T) {
	cs := compiledCase{
		name:     "emit-ctxfilter",
		template: "@set a = 1\n@for x in [7,8] {\n@set y = x\n{{ x | ctxkeys }};{{ x | ctxkeys(0) }};\n@}\n",
	}
	res, err := compileCase(t, cs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	dir := t.TempDir()
	root := repoRoot(t)
	gomod := "module qctxinj\n\ngo 1.23\n\nrequire github.com/avmnu-sng/quill-template-engine v0.0.0\n\nreplace github.com/avmnu-sng/quill-template-engine => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := os.ReadFile(filepath.Join(root, ".go-version")); err == nil {
		if err := os.WriteFile(filepath.Join(dir, ".go-version"), v, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkg := pkgName(cs.name)
	sub := filepath.Join(dir, pkg)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "gen.go"), res.Source, 0o644); err != nil {
		t.Fatal(err)
	}

	mainSrc := fmt.Sprintf(`package main

import (
	"fmt"
	"strings"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/ext"
	"github.com/avmnu-sng/quill-template-engine/runtime"

	gen "qctxinj/%s"
)

// ctxkeys renders the injected _context mapping as name=kind pairs (with the
// text of scalar entries), then the piped value, so any drift in the context
// names, order, or values between the two engines changes the output bytes.
func ctxkeys(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || args[0].Kind != runtime.KArray || args[0].Arr == nil {
		return runtime.Null(), fmt.Errorf("ctxkeys: no context injected (got %%d args)", len(args))
	}
	var b strings.Builder
	for _, p := range args[0].Arr.Pairs() {
		k, err := runtime.ToText(p.Key)
		if err != nil {
			return runtime.Null(), err
		}
		b.WriteString(k)
		b.WriteByte('=')
		fmt.Fprintf(&b, "%%s", p.Val.Kind)
		if p.Val.Kind == runtime.KInt || p.Val.Kind == runtime.KStr {
			s, err := runtime.ToText(p.Val)
			if err != nil {
				return runtime.Null(), err
			}
			b.WriteByte(':')
			b.WriteString(s)
		}
		b.WriteByte(' ')
	}
	fmt.Fprintf(&b, "| piped=%%d args=%%d", args[1].I, len(args)-2)
	return runtime.Str(b.String()), nil
}

func main() {
	env := quill.NewWithArray(map[string]string{%q: %q})
	env.Extensions().AddFilter(&ext.Filter{Name: "ctxkeys", NeedsContext: true, Fn: ctxkeys})

	want, werr := env.Render(%q, map[string]runtime.Value{})
	var b strings.Builder
	cerr := gen.%s(&b, env.Extensions(), map[string]runtime.Value{}, env.RenderCache())

	switch {
	case (cerr != nil) != (werr != nil):
		fmt.Printf("MISMATCH: compiled err=%%v interp err=%%v", cerr, werr)
	case cerr != nil:
		if cerr.Error() != werr.Error() {
			fmt.Printf("MISMATCH: compiled %%q interp %%q", cerr.Error(), werr.Error())
			return
		}
		fmt.Print("OK")
	default:
		if b.String() != want {
			fmt.Printf("MISMATCH:\ncompiled %%q\ninterp   %%q", b.String(), want)
			return
		}
		fmt.Print("OK")
	}
}
`, pkg, cs.name+".ql", cs.template, cs.name+".ql", res.FuncName)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run scratch module: %v\nstderr:\n%s", err, stderr.String())
	}
	if stdout.String() != "OK" {
		t.Fatalf("needs-context filter injection diverges: %s", stdout.String())
	}
}

// TestFailingWriterParity pins the error and bytes-written behavior of the
// compiled write path against the interpreter's streaming RenderTo: for a
// writer that fails after every possible byte count, the compiled render must
// leave the identical byte prefix in the sink and return the identical error
// text (or succeed identically), across text spans, static-Int writes, the
// Str/Safe guard, and the emit-helper fallback.
func TestFailingWriterParity(t *testing.T) {
	cs := compiledCase{
		name:     "emit-failwriter",
		template: "abc{{ 42 }}-{{ s }}-{{ 1.5 }}\n@for i in (1..3) {\n{{ loop.index }}{{ \"!\" | raw }}\n@}\n",
	}
	res, err := compileCase(t, cs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	dir := t.TempDir()
	root := repoRoot(t)
	gomod := "module qfailw\n\ngo 1.23\n\nrequire github.com/avmnu-sng/quill-template-engine v0.0.0\n\nreplace github.com/avmnu-sng/quill-template-engine => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := os.ReadFile(filepath.Join(root, ".go-version")); err == nil {
		if err := os.WriteFile(filepath.Join(dir, ".go-version"), v, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkg := pkgName(cs.name)
	sub := filepath.Join(dir, pkg)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "gen.go"), res.Source, 0o644); err != nil {
		t.Fatal(err)
	}

	mainSrc := fmt.Sprintf(`package main

import (
	"errors"
	"fmt"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/runtime"

	gen "qfailw/%s"
)

// capWriter accepts up to cap bytes, then fails every further write with a
// fixed error after absorbing nothing from the failing call.
type capWriter struct {
	cap int
	buf []byte
}

func (w *capWriter) Write(p []byte) (int, error) {
	if len(w.buf)+len(p) > w.cap {
		return 0, errors.New("sink full")
	}
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func main() {
	env := quill.NewWithArray(map[string]string{%q: %q})
	vars := map[string]runtime.Value{"s": runtime.Str("mid")}

	full, err := env.Render(%q, vars)
	if err != nil {
		fmt.Printf("MISMATCH: reference render failed: %%v", err)
		return
	}
	for cap := 0; cap <= len(full)+1; cap++ {
		cw := &capWriter{cap: cap}
		cerr := gen.%s(cw, env.Extensions(), map[string]runtime.Value{"s": runtime.Str("mid")}, env.RenderCache())
		iw := &capWriter{cap: cap}
		werr := env.RenderTo(iw, %q, map[string]runtime.Value{"s": runtime.Str("mid")})
		if (cerr != nil) != (werr != nil) {
			fmt.Printf("MISMATCH at cap %%d: compiled err=%%v interp err=%%v", cap, cerr, werr)
			return
		}
		if cerr != nil && cerr.Error() != werr.Error() {
			fmt.Printf("MISMATCH at cap %%d: compiled %%q interp %%q", cap, cerr.Error(), werr.Error())
			return
		}
		if string(cw.buf) != string(iw.buf) {
			fmt.Printf("MISMATCH at cap %%d: compiled prefix %%q interp prefix %%q", cap, cw.buf, iw.buf)
			return
		}
	}
	fmt.Print("OK")
}
`, pkg, cs.name+".ql", cs.template, cs.name+".ql", res.FuncName, cs.name+".ql")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run scratch module: %v\nstderr:\n%s", err, stderr.String())
	}
	if stdout.String() != "OK" {
		t.Fatalf("failing-writer behavior diverges: %s", stdout.String())
	}
}
