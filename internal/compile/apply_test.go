package compile_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/internal/compile"
	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// applyCases pin the compiled @apply lowering against the facade: each renders
// through the compiled path (and, via envCheck, through a WithCompiled
// Environment whose tracer proves the dispatch gate served the unit) and must
// match interp.Render byte for byte, or match its error text and position for
// the failing constructs. The battery covers the risk surfaces the compiler
// tail names for @apply: the double-escape guard under a live strategy, the
// indent-suspended capture inside a @tab region, multi-filter and nested
// regions, a Safe result a later print must not re-escape, a general-path
// filter taking extra arguments, and the two "in apply" unknown-filter error
// spellings whose streamed timing is observable.
var applyCases = []compiledCase{
	{name: "apply-single-fast", template: "@apply | upper {\nhello world\n@}\n", envCheck: true},
	{name: "apply-two-filters", template: "@apply | upper | trim {\n  hello  \n@}\n", envCheck: true},
	{name: "apply-nested-region", template: "@apply | upper {\nA\n@apply | lower {\nBcD\n@}\nZ\n@}\n", envCheck: true},
	{name: "apply-extra-args", template: "@apply | replace({\"a\": \"X\"}) {\nbanana\n@}\n", envCheck: true},
	// execApply appends a spread argument as ONE array value, never its expanded
	// elements (interp/exec.go's own loop, unlike the inline filter path's
	// collectArgs). The render-equal case pipes into default, which ignores its
	// extra argument when the piped value is present, so the one-array-vs-many
	// difference is byte-invisible and both engines return the captured text; the
	// errors case pipes the same spread into replace, whose array argument both
	// engines refuse identically as "cannot render an array as text". A compiled
	// path that expanded the spread would diverge in both.
	{name: "apply-spread-ignored-arg", template: "@apply | default(...[\"ignored one\", \"ignored two\"]) {\nkeep me\n@}\n", envCheck: true},
	{name: "apply-spread-array-arg-errors", template: "@apply | replace(...[{\"a\": \"X\"}]) {\nbanana\n@}\n"},
	{
		name:     "apply-escape-no-double",
		template: "@escape html {\n@apply | upper {\n{{ p }}\n@}\n@}\n",
		varsJSON: `{"p":"<x>&y"}`,
		opts:     compile.Options{AutoescapeHTML: true},
		envCheck: true,
	},
	{
		name:     "apply-safe-not-reescaped",
		template: "@escape html {\n@apply | trim {\n {{ p }} \n@}\n{{ q }}\n@}\n",
		varsJSON: `{"p":"<x>&y","q":"<z>"}`,
		opts:     compile.Options{AutoescapeHTML: true},
		envCheck: true,
	},
	{name: "apply-in-tab-region", template: "@tab(1) {\n@apply | upper {\nline1\nline2\n@}\n@}\ntail\n", envCheck: true},
	{name: "apply-tab-around-region", template: "before\n@tab(2) {\n@apply | trim {\n  x  \n@}\n@}\nafter\n", envCheck: true},
	{name: "apply-set-copies-back", template: "@apply | upper {\n@set w = \"inside\"\n{{ w }}\n@}\n{{ w }}\n"},
	{name: "apply-unknown-filter", template: "@apply | nope {\nx\n@}\n"},
	{name: "apply-unknown-filter-after-emit", template: "pre\n@apply | upper | nope {\nx\n@}\n"},
	{name: "apply-general-filter-error", template: "@apply | replace {\nhello\n@}\n"},
}

// TestApplyParityBattery renders every @apply case through the compiled path in
// one scratch module and asserts byte-equal output, or byte-equal error text
// and position, against the facade. The two unknown-filter cases additionally
// prove the compiler emits execApply's "unknown filter %q in apply" spelling at
// the filter's own line, distinct from the inline "unknown filter %q" form.
func TestApplyParityBattery(t *testing.T) {
	results := map[string]*compile.Result{}
	for _, cs := range applyCases {
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cs.name, err)
		}
		results[cs.name] = res
	}
	got := runCompiled(t, applyCases, results)
	for _, cs := range applyCases {
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
			continue
		}
		if !cs.envCheck {
			continue
		}
		tr, ok := got[cs.name+"@tracer"]
		switch {
		case !ok:
			t.Errorf("%s: no tracer result from scratch run", cs.name)
		case tr.failed:
			t.Errorf("%s: tracer render errored: %s", cs.name, tr.errText)
		case tr.out != "served":
			t.Errorf("%s: dispatch gate fell back for an @apply unit it should serve", cs.name)
		}
	}
}

// applySpreadCases pin the one behavioral seam between @apply's filter path and
// the inline filter path: execApply appends a spread argument as a single array
// value while evalFilter/collectArgs expand it into its elements. The replace
// pair renders the SAME spread through @apply (non-expanding, one array as the
// from argument, which no-ops the captured text) and inline (expanding to
// replace(from, to), which rewrites it), so a compiled @apply that mistakenly
// reused collectArgs would either diverge from interp on the @apply row or make
// the two rows collapse to one behavior. The default row is @apply-only because
// inline default over a spread produces a list|string print position the compiler
// rejects at type time; its non-error byte-parity is asserted against interp
// directly. The trim row is the interp-errors half of the seam: @apply hands trim
// one array as its strip set, and trim returns that array unchanged, so the print
// site cannot render it; both engines raise the array-as-text error at the same
// position, and a compiled @apply that expanded the spread would instead pass a
// string strip set and render clean bytes. The lenient rows re-run the two
// non-error shapes under strict-off, an axis the strict rows do not cover. The
// compiled path is compared to interp.Render for every row through the same
// scratch harness the main battery uses.
var applySpreadCases = []compiledCase{
	// A spread of two strings into replace: @apply hands replace one array
	// argument; inline expands it to replace(from, to) and rewrites the text.
	{name: "apply-spread-replace", template: "@apply | replace(...[\"a\", \"X\"]) {\nbanana\n@}\n"},
	{name: "inline-spread-replace", template: "{{ \"banana\" | replace(...[\"a\", \"X\"]) }}\n"},
	// A spread into default: @apply hands default one array as its fallback,
	// ignored because the piped value is present, so both engines render the
	// captured text: the non-error spread half of the seam.
	{name: "apply-spread-default", template: "@apply | default(...[\"fb one\", \"fb two\"]) {\nvalue here\n@}\n"},
	// A spread into trim: @apply hands trim one array as its strip set, which
	// trim returns unchanged, so the array reaches the print site and both
	// engines raise "cannot render an array as text" at the filter's line: the
	// interp-errors half of the seam, error text and position compared to interp.
	{name: "apply-spread-trim-error", template: "@apply | trim(...[\"x\"]) {\nxhix\n@}\n"},
	// The two non-error shapes re-run under lenient variables: the non-expansion
	// seam holds identically whether strict or lenient shapes the print positions.
	{name: "apply-spread-replace-lenient", template: "@apply | replace(...[\"a\", \"X\"]) {\nbanana\n@}\n", opts: compile.Options{LenientVariables: true}},
	{name: "apply-spread-default-lenient", template: "@apply | default(...[\"fb one\", \"fb two\"]) {\nvalue here\n@}\n", opts: compile.Options{LenientVariables: true}},
}

// TestApplySpreadNonExpansionParity compiles the spread-argument seam cases and
// asserts each compiled render matches interp.Render byte for byte (or error for
// error). The @apply rows exercise execApply's non-expanding argument loop, which
// the compiled applyArgs replicates; the inline rows are the expanding control.
// The RED this pins: a compiled @apply that expanded the spread rendered wrong
// bytes where interp errored.
func TestApplySpreadNonExpansionParity(t *testing.T) {
	results := map[string]*compile.Result{}
	for _, cs := range applySpreadCases {
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cs.name, err)
		}
		results[cs.name] = res
	}
	got := runCompiled(t, applySpreadCases, results)
	for _, cs := range applySpreadCases {
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
	// The seam itself: the @apply row and its inline sibling must NOT agree on
	// the replace pair (one errors, one rewrites), proving the two paths keep
	// distinct spread semantics rather than both expanding.
	applyReplace, inlineReplace := got["apply-spread-replace"], got["inline-spread-replace"]
	if applyReplace.failed == inlineReplace.failed && applyReplace.out == inlineReplace.out {
		t.Errorf("spread-replace seam collapsed: @apply and inline produced the same outcome (%q / failed=%v)",
			applyReplace.out, applyReplace.failed)
	}
}

// TestApplyDeterminism compiles an @apply template twice and asserts
// byte-identical generated source, so the lowering carries no map-iteration or
// pointer-address nondeterminism.
func TestApplyDeterminism(t *testing.T) {
	body := "@escape html {\n@apply | upper | trim {\n{{ p }}\n@}\n@}\n"
	gen := func() []byte {
		mod, err := parse.Parse(source.New("a.ql", body))
		if err != nil {
			t.Fatal(err)
		}
		res, err := compile.Module("a.ql", mod, compile.Options{AutoescapeHTML: true})
		if err != nil {
			t.Fatal(err)
		}
		return res.Source
	}
	if !bytes.Equal(gen(), gen()) {
		t.Error("compiled @apply source is not deterministic across two compilations")
	}
}

// TestApplyNeedsContextFilterParity puts a host NeedsContext filter inside an
// @apply region and byte-compares the compiled render against the interpreter
// in the same process. The filter renders the injected _context mapping, so any
// drift in the names, order, or values emitContext materializes for the @apply
// filter chain (built from the live scope at the point the region's body
// finished capturing) changes the output bytes. No built-in filter requests
// context, so the host registration is the only way to exercise the injection
// path in the compiled @apply lowering.
func TestApplyNeedsContextFilterParity(t *testing.T) {
	cs := compiledCase{
		name:     "apply-ctxfilter",
		template: "@set a = 1\n@for x in [7,8] {\n@set y = x\n@apply | ctxkeys {\nbody-{{ x }}\n@}\n@}\n",
	}
	res, err := compileCase(t, cs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	dir := t.TempDir()
	root := repoRoot(t)
	gomod := "module qapplyctx\n\ngo 1.23\n\nrequire github.com/avmnu-sng/quill-template-engine v0.0.0\n\nreplace github.com/avmnu-sng/quill-template-engine => " + root + "\n"
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

	mainSrc := applyCtxMain(pkg, cs.name+".ql", cs.template, res.FuncName)
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
		t.Fatalf("needs-context filter injection diverges inside @apply: %s", stdout.String())
	}
}

// applyCtxMain builds the scratch main that renders the @apply case through the
// interpreter and the compiled function with a shared Set carrying a
// host NeedsContext filter, then byte-compares the two renders. The filter
// serializes the injected context, so a drift in what the compiled @apply hands
// it fails the comparison.
func applyCtxMain(pkg, tmplName, tmpl, fn string) string {
	const tmt = `package main

import (
	"context"
	"fmt"
	"strings"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"

	gen "qapplyctx/%s"
)

// ctxkeys renders the injected _context mapping as name=kind pairs (with the
// text of scalar entries) then the piped value, so any drift in the context
// names, order, or values between the two engines changes the output bytes.
func ctxkeys(ctx context.Context, args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 || args[0].Kind() != runtime.KArray || args[0].AsArray() == nil {
		return runtime.Null(), fmt.Errorf("ctxkeys: no context injected (got %%d args)", len(args))
	}
	var b strings.Builder
	for _, p := range args[0].AsArray().Pairs() {
		k, err := runtime.ToText(p.Key)
		if err != nil {
			return runtime.Null(), err
		}
		b.WriteString(k)
		b.WriteByte('=')
		fmt.Fprintf(&b, "%%s", p.Val.Kind())
		if p.Val.Kind() == runtime.KInt || p.Val.Kind() == runtime.KStr {
			s, err := runtime.ToText(p.Val)
			if err != nil {
				return runtime.Null(), err
			}
			b.WriteByte(':')
			b.WriteString(s)
		}
		b.WriteByte(' ')
	}
	piped, err := runtime.ToText(args[1])
	if err != nil {
		return runtime.Null(), err
	}
	fmt.Fprintf(&b, "| piped=%%q", piped)
	return runtime.Str(b.String()), nil
}

func main() {
	env := quill.NewFromMap(map[string]string{%q: %q})
	env.Extensions().AddFilter(&ext.Filter{Name: "ctxkeys", NeedsContext: true, Fn: ctxkeys})

	want, werr := env.Render(context.Background(), %q, map[string]runtime.Value{})
	var b strings.Builder
	cerr := gen.%s(context.Background(), &b, env.Extensions(), map[string]runtime.Value{}, env.RenderCache())

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
`
	return fmt.Sprintf(tmt, pkg, tmplName, tmpl, tmplName, fn)
}
