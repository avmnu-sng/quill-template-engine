package quill

import (
	"bytes"
	"errors"
	"io"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/check"
	"github.com/avmnu-sng/quill-template-engine/compiled"
	"github.com/avmnu-sng/quill-template-engine/cover"
	"github.com/avmnu-sng/quill-template-engine/ext"
	"github.com/avmnu-sng/quill-template-engine/loader"
	"github.com/avmnu-sng/quill-template-engine/runtime"
	"github.com/avmnu-sng/quill-template-engine/sandbox"
)

// dispatchMarker is the output of the tracer manifests below. It deliberately
// differs from any interpreter output in these tests, so a render returning it
// PROVES the compiled path served the bytes and a render returning interpreter
// bytes proves the gate fell back.
const dispatchMarker = "\x02COMPILED\x02"

// defaultFingerprint matches an Environment built with no options: autoescape
// off, strict variables, tab width 4, unseeded randomness.
func defaultFingerprint() compiled.Fingerprint {
	return compiled.Fingerprint{TabWidth: 4}
}

// markerManifest builds a tracer manifest for entry over src: fingerprint and
// sources as given, render function emitting dispatchMarker.
func markerManifest(entry, src string, fp compiled.Fingerprint, usesLog bool) *compiled.Manifest {
	return &compiled.Manifest{
		Entry:       entry,
		Sources:     map[string]string{entry: src},
		Fingerprint: fp,
		UsesLog:     usesLog,
		Render: func(w io.Writer, _ *ext.ExtensionSet, _ map[string]runtime.Value) error {
			_, err := io.WriteString(w, dispatchMarker)
			return err
		},
	}
}

func TestWithCompiledServesInstalledUnit(t *testing.T) {
	env := NewWithArray(
		map[string]string{"t.ql": "hi", "other.ql": "bye"},
		WithCompiled(markerManifest("t.ql", "hi", defaultFingerprint(), false)),
	)
	out, err := env.Render("t.ql", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != dispatchMarker {
		t.Fatalf("Render did not dispatch compiled: got %q", out)
	}
	var b strings.Builder
	if err := env.RenderTo(&b, "t.ql", nil); err != nil {
		t.Fatal(err)
	}
	if b.String() != dispatchMarker {
		t.Fatalf("RenderTo did not dispatch compiled: got %q", b.String())
	}
	// A name with no installed unit renders through the interpreter.
	out, err = env.Render("other.ql", nil)
	if err != nil || out != "bye" {
		t.Fatalf("uninstalled name: got %q, %v", out, err)
	}
	// An ad-hoc string render never consults the dispatch table, even under a
	// colliding name.
	out, err = env.RenderString("t.ql", "adhoc", nil)
	if err != nil || out != "adhoc" {
		t.Fatalf("RenderString dispatched: got %q, %v", out, err)
	}
}

// TestWithCompiledOptionFlipFallsBack is the fingerprint mutation matrix: for
// each compile-option knob, a one-sided flip must fall back to the interpreter
// and a both-sided flip must dispatch again.
func TestWithCompiledOptionFlipFallsBack(t *testing.T) {
	const src = "hi"
	seeded := func(seed int64) compiled.Fingerprint {
		fp := defaultFingerprint()
		fp.RandomSeed = seed
		fp.RandomSeedSet = true
		return fp
	}
	cases := []struct {
		name   string
		opts   []Option
		fp     compiled.Fingerprint
		served bool
	}{
		{"matched-defaults", nil, defaultFingerprint(), true},
		{"env-autoescape-on", []Option{WithAutoescapeHTML(true)}, defaultFingerprint(), false},
		{"unit-autoescape-on", nil, compiled.Fingerprint{AutoescapeHTML: true, TabWidth: 4}, false},
		{"both-autoescape-on", []Option{WithAutoescapeHTML(true)}, compiled.Fingerprint{AutoescapeHTML: true, TabWidth: 4}, true},
		{"env-lenient", []Option{WithStrictVariables(false)}, defaultFingerprint(), false},
		{"unit-lenient", nil, compiled.Fingerprint{LenientVariables: true, TabWidth: 4}, false},
		{"both-lenient", []Option{WithStrictVariables(false)}, compiled.Fingerprint{LenientVariables: true, TabWidth: 4}, true},
		{"env-tabwidth-2", []Option{WithTabWidth(2)}, defaultFingerprint(), false},
		{"unit-tabwidth-2", nil, compiled.Fingerprint{TabWidth: 2}, false},
		{"both-tabwidth-2", []Option{WithTabWidth(2)}, compiled.Fingerprint{TabWidth: 2}, true},
		{"env-seeded", []Option{WithRandomSeed(7)}, defaultFingerprint(), false},
		{"unit-seeded", nil, seeded(7), false},
		{"both-seeded", []Option{WithRandomSeed(7)}, seeded(7), true},
		{"seed-value-differs", []Option{WithRandomSeed(8)}, seeded(7), false},
		{"env-seeded-zero", []Option{WithRandomSeed(0)}, defaultFingerprint(), false},
		{"both-seeded-zero", []Option{WithRandomSeed(0)}, seeded(0), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := append([]Option{WithCompiled(markerManifest("t.ql", src, tc.fp, false))}, tc.opts...)
			env := NewWithArray(map[string]string{"t.ql": src}, opts...)
			out, err := env.Render("t.ql", nil)
			if err != nil {
				t.Fatal(err)
			}
			want := src
			if tc.served {
				want = dispatchMarker
			}
			if out != want {
				t.Fatalf("got %q, want %q", out, want)
			}
		})
	}
}

// TestWithCompiledFeatureGatesFallBack pins the render-shaping features the
// generated code cannot honor: configuring any of them keeps every render on
// the interpreter even when the fingerprint matches.
func TestWithCompiledFeatureGatesFallBack(t *testing.T) {
	const src = "hi"
	cases := []struct {
		name string
		opt  Option
	}{
		{"sandbox-policy", WithSandboxPolicy(&sandbox.Policy{})},
		{"sandbox-active", WithSandboxActive(true)},
		{"coverage", WithCoverage(cover.NewCollector())},
		{"types", WithTypes(&check.Registry{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := NewWithArray(
				map[string]string{"t.ql": src},
				WithCompiled(markerManifest("t.ql", src, defaultFingerprint(), false)),
				tc.opt,
			)
			out, err := env.Render("t.ql", nil)
			if err != nil {
				t.Fatal(err)
			}
			if out != src {
				t.Fatalf("%s did not force fallback: got %q", tc.name, out)
			}
		})
	}
}

// TestWithCompiledSourceByteEditFallsBack is the source mutation test: a
// manifest whose embedded source differs from the loader's text by a single
// byte must never be served.
func TestWithCompiledSourceByteEditFallsBack(t *testing.T) {
	env := NewWithArray(
		map[string]string{"t.ql": "hi"},
		WithCompiled(markerManifest("t.ql", "hi!", defaultFingerprint(), false)),
	)
	out, err := env.Render("t.ql", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "hi" {
		t.Fatalf("stale-source unit served: got %q", out)
	}
	// A manifest citing a member the loader cannot serve is equally unprovable.
	m := markerManifest("t.ql", "hi", defaultFingerprint(), false)
	m.Sources["missing.ql"] = "gone"
	env2 := NewWithArray(map[string]string{"t.ql": "hi"}, WithCompiled(m))
	out, err = env2.Render("t.ql", nil)
	if err != nil || out != "hi" {
		t.Fatalf("unit with unloadable member served: got %q, %v", out, err)
	}
}

// TestWithCompiledAbsentIncludeGate drives the ignore-missing @include
// present/absent transition: a unit that inlined an ignore-missing include as
// rendering nothing carries the absent target in AbsentIncludes, so the gate
// serves the compiled render only while the target still fails to resolve and
// falls back to the interpreter the moment the loader begins serving it.
func TestWithCompiledAbsentIncludeGate(t *testing.T) {
	// The entry inlined `@include "gone.ql" ignore missing` as nothing; the
	// compiled render (the marker) is byte-exact only while gone.ql is absent.
	m := markerManifest("t.ql", "head\n", defaultFingerprint(), false)
	m.AbsentIncludes = []string{"gone.ql"}

	ldr := loader.NewArrayLoader(map[string]string{"t.ql": "head\n"})
	env := New(ldr, WithCompiled(m))
	out, err := env.Render("t.ql", nil)
	if err != nil || out != dispatchMarker {
		t.Fatalf("absent-include unit not served while target missing: got %q, %v", out, err)
	}

	// The moment the loader serves gone.ql, the interpreter's include would
	// inline its body, so the compiled render-nothing is no longer byte-exact
	// and the gate must fall back.
	ldr.Set("gone.ql", "partial")
	out, err = env.Render("t.ql", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "head\n" {
		t.Fatalf("absent-include unit served after target appeared: got %q", out)
	}

	// A fresh Environment whose loader never had the target serves compiled
	// again: the check runs per render against live loader state, not once at
	// install, so a target present in one Environment never poisons another.
	env2 := NewWithArray(map[string]string{"t.ql": "head\n"}, WithCompiled(m))
	out, err = env2.Render("t.ql", nil)
	if err != nil || out != dispatchMarker {
		t.Fatalf("absent-include unit not served in a fresh loader: got %q, %v", out, err)
	}
}

// TestWithCompiledStaleParseNeverServed drives the loader/parse-change
// contract end to end: while the parse cache pins the compiled-from module the
// unit keeps serving (the interpreter would walk the same pinned parse), and
// the moment the cache serves a re-parsed module with different text the
// pointer witness forces re-verification and the unit falls back for good.
func TestWithCompiledStaleParseNeverServed(t *testing.T) {
	ldr := loader.NewArrayLoader(map[string]string{"t.ql": "one"})
	env := New(ldr, WithCompiled(markerManifest("t.ql", "one", defaultFingerprint(), false)))
	out, err := env.Render("t.ql", nil)
	if err != nil || out != dispatchMarker {
		t.Fatalf("fresh unit not served: got %q, %v", out, err)
	}

	// The loader changes but the parse cache still serves the old module: the
	// interpreter path would render "one", so the unit remains coherent.
	ldr.Set("t.ql", "two")
	out, err = env.Render("t.ql", nil)
	if err != nil || out != dispatchMarker {
		t.Fatalf("pinned-parse render changed: got %q, %v", out, err)
	}

	// After eviction the re-parse serves the new text; the witness pointer
	// mismatch triggers the byte re-check and the unit must fall back.
	env.cache.Clear()
	out, err = env.Render("t.ql", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "two" {
		t.Fatalf("stale artifact served after re-parse: got %q", out)
	}
	// The negative verdict is itself memoized against the new module: a warm
	// repeat stays on the interpreter.
	out, err = env.Render("t.ql", nil)
	if err != nil || out != "two" {
		t.Fatalf("repeat render after fallback: got %q, %v", out, err)
	}
}

// TestWithCompiledLogGate pins the @log side-effect rule: a unit marked
// UsesLog dispatches only while the logger discards, so a host logger never
// loses the lines the interpreter would have written.
func TestWithCompiledLogGate(t *testing.T) {
	const src = "x\n@log \"note\"\n"
	m := func() *compiled.Manifest { return markerManifest("t.ql", src, defaultFingerprint(), true) }

	env := NewWithArray(map[string]string{"t.ql": src}, WithCompiled(m()))
	out, err := env.Render("t.ql", nil)
	if err != nil || out != dispatchMarker {
		t.Fatalf("UsesLog unit with discard logger not served: got %q, %v", out, err)
	}

	var buf bytes.Buffer
	env2 := NewWithArray(map[string]string{"t.ql": src},
		WithCompiled(m()), WithLogger(log.New(&buf, "", 0)))
	out, err = env2.Render("t.ql", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == dispatchMarker {
		t.Fatal("UsesLog unit served despite a real logger")
	}
	if !strings.Contains(buf.String(), "note") {
		t.Fatalf("fallback render did not log: %q", buf.String())
	}
}

func TestWithCompiledVerifyServesInterpAndReports(t *testing.T) {
	var divs []compiled.Divergence
	env := NewWithArray(
		map[string]string{"t.ql": "hi"},
		WithCompiled(markerManifest("t.ql", "hi", defaultFingerprint(), false)),
		WithCompiledVerify(func(d compiled.Divergence) { divs = append(divs, d) }),
	)
	out, err := env.Render("t.ql", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "hi" {
		t.Fatalf("verify mode served compiled bytes: got %q", out)
	}
	if len(divs) != 1 {
		t.Fatalf("expected one divergence, got %d", len(divs))
	}
	d := divs[0]
	if d.Template != "t.ql" || d.CompiledOutput != dispatchMarker || d.InterpOutput != "hi" ||
		d.CompiledErr != nil || d.InterpErr != nil {
		t.Fatalf("divergence fields wrong: %+v", d)
	}

	// RenderTo under verification also serves the interpreter's bytes.
	divs = nil
	var b strings.Builder
	if err := env.RenderTo(&b, "t.ql", nil); err != nil {
		t.Fatal(err)
	}
	if b.String() != "hi" || len(divs) != 1 {
		t.Fatalf("verify RenderTo: got %q, %d divergences", b.String(), len(divs))
	}
}

func TestWithCompiledVerifyCleanUnitReportsNothing(t *testing.T) {
	clean := &compiled.Manifest{
		Entry:       "t.ql",
		Sources:     map[string]string{"t.ql": "hi"},
		Fingerprint: defaultFingerprint(),
		Render: func(w io.Writer, _ *ext.ExtensionSet, _ map[string]runtime.Value) error {
			_, err := io.WriteString(w, "hi")
			return err
		},
	}
	called := 0
	env := NewWithArray(map[string]string{"t.ql": "hi"},
		WithCompiled(clean),
		WithCompiledVerify(func(compiled.Divergence) { called++ }))
	out, err := env.Render("t.ql", nil)
	if err != nil || out != "hi" {
		t.Fatalf("got %q, %v", out, err)
	}
	if called != 0 {
		t.Fatalf("clean unit reported %d divergences", called)
	}
}

func TestWithCompiledVerifyReportsErrorDivergence(t *testing.T) {
	failing := &compiled.Manifest{
		Entry:       "t.ql",
		Sources:     map[string]string{"t.ql": "hi"},
		Fingerprint: defaultFingerprint(),
		Render: func(w io.Writer, _ *ext.ExtensionSet, _ map[string]runtime.Value) error {
			if _, err := io.WriteString(w, "hi"); err != nil {
				return err
			}
			return io.ErrUnexpectedEOF
		},
	}
	var divs []compiled.Divergence
	env := NewWithArray(map[string]string{"t.ql": "hi"},
		WithCompiled(failing),
		WithCompiledVerify(func(d compiled.Divergence) { divs = append(divs, d) }))
	out, err := env.Render("t.ql", nil)
	if err != nil || out != "hi" {
		t.Fatalf("verify must serve the interpreter result: got %q, %v", out, err)
	}
	if len(divs) != 1 || divs[0].CompiledErr == nil || divs[0].InterpErr != nil {
		t.Fatalf("error divergence not reported: %+v", divs)
	}
}

// TestWithCompiledVerifyRenderToWritesErrorPartialOutput pins the verify-mode
// byte-identity contract on the error path: a gate-passing unit is slot-free,
// so the interpreter's RenderTo streams partial output before a mid-render
// error and direct dispatch does too. Verification buffers the render to
// compare both engines but must still leave those same bytes on w alongside
// the identical error -- and a faithful unit must report zero divergences.
func TestWithCompiledVerifyRenderToWritesErrorPartialOutput(t *testing.T) {
	const src = "hi{{ nope }}"
	tmpls := map[string]string{"t.ql": src}

	base := NewWithArray(tmpls)
	var wBase strings.Builder
	baseErr := base.RenderTo(&wBase, "t.ql", nil)
	if baseErr == nil {
		t.Fatal("interp RenderTo did not error under strict variables")
	}
	if wBase.String() != "hi" {
		t.Fatalf("interp RenderTo partial output: got %q, want %q", wBase.String(), "hi")
	}

	// A faithful unit mirrors the interpreter exactly: the partial bytes,
	// then an error with identical text.
	faithful := &compiled.Manifest{
		Entry:       "t.ql",
		Sources:     map[string]string{"t.ql": src},
		Fingerprint: defaultFingerprint(),
		Render: func(w io.Writer, _ *ext.ExtensionSet, _ map[string]runtime.Value) error {
			if _, err := io.WriteString(w, "hi"); err != nil {
				return err
			}
			return errors.New(baseErr.Error())
		},
	}

	direct := NewWithArray(tmpls, WithCompiled(faithful))
	var wDirect strings.Builder
	directErr := direct.RenderTo(&wDirect, "t.ql", nil)
	if directErr == nil || directErr.Error() != baseErr.Error() || wDirect.String() != wBase.String() {
		t.Fatalf("direct dispatch: w=%q err=%v, want w=%q err=%v",
			wDirect.String(), directErr, wBase.String(), baseErr)
	}

	divs := 0
	shadow := NewWithArray(tmpls,
		WithCompiled(faithful),
		WithCompiledVerify(func(compiled.Divergence) { divs++ }))
	var wShadow strings.Builder
	shadowErr := shadow.RenderTo(&wShadow, "t.ql", nil)
	if shadowErr == nil || shadowErr.Error() != baseErr.Error() {
		t.Fatalf("verify RenderTo error: got %v, want %v", shadowErr, baseErr)
	}
	if wShadow.String() != wBase.String() {
		t.Fatalf("verify RenderTo dropped error-path partial output: got %q, want %q",
			wShadow.String(), wBase.String())
	}
	if divs != 0 {
		t.Fatalf("faithful unit reported %d divergences", divs)
	}
}

// slotsErrorSrc is a template that uses a top-level @yield (so it compiles as a
// slots unit) and then errors mid-render on an undefined print. On this error
// the interpreter's buffered-slots path (RenderTo) writes nothing to w, while
// its string path (Render) returns the partial, still-unresolved buffer that
// carries a raw placeholder. A faithful slots unit reproduces both.
const slotsErrorSrc = "@yield s\nvisible output here\n@provide s {\nprovided\n@}\n{{ missing }}\n"

// faithfulSlotsManifest builds a UsesSlots manifest whose render mirrors the
// generated slots unit's error path: it writes the interpreter's partial,
// unresolved buffer (the exact bytes the generated function's deferred writer
// emits to w on error, raw placeholder and all) and then returns the render
// error. The partial is captured from the interpreter's string render so the
// manifest stays faithful regardless of the placeholder token's number.
func faithfulSlotsManifest(t *testing.T, tmpls map[string]string) (*compiled.Manifest, string, error) {
	t.Helper()
	interpEnv := NewWithArray(tmpls)
	partial, rerr := interpEnv.Render("t.ql", nil)
	if rerr == nil {
		t.Fatal("slots template did not error under strict variables")
	}
	if !strings.Contains(partial, "\x00\x01QUILL_SLOT_") {
		t.Fatalf("interp string render did not leave an unresolved placeholder: %q", partial)
	}
	m := &compiled.Manifest{
		Entry:       "t.ql",
		Sources:     map[string]string{"t.ql": slotsErrorSrc},
		Fingerprint: defaultFingerprint(),
		UsesSlots:   true,
		Render: func(w io.Writer, _ *ext.ExtensionSet, _ map[string]runtime.Value) error {
			if _, werr := io.WriteString(w, partial); werr != nil {
				return werr
			}
			return errors.New(rerr.Error())
		},
	}
	return m, partial, rerr
}

// TestWithCompiledRenderToSlotsDiscardsErrorPartial pins the streaming-dispatch
// contract for a compiled slots unit on the error path: RenderTo must write
// nothing to the caller's writer when the render fails, matching the
// interpreter's buffered-slots branch, and no raw placeholder may reach w. The
// generated slots render writes its partial, unresolved buffer to the writer it
// is handed on error (correct only for the internal buffer Render hands it), so
// RenderTo must route the unit through a scratch buffer it discards on error.
// Before Manifest.UsesSlots existed, dispatch handed the caller's writer
// straight to the generated function and leaked the placeholder-bearing partial.
func TestWithCompiledRenderToSlotsDiscardsErrorPartial(t *testing.T) {
	tmpls := map[string]string{"t.ql": slotsErrorSrc}

	// The interpreter's streaming path is the ground truth: nothing on error.
	interpEnv := NewWithArray(tmpls)
	var iw strings.Builder
	iErr := interpEnv.RenderTo(&iw, "t.ql", nil)
	if iErr == nil {
		t.Fatal("interp RenderTo did not error")
	}
	if iw.String() != "" {
		t.Fatalf("interp RenderTo wrote %q, want nothing on error", iw.String())
	}

	m, partial, rerr := faithfulSlotsManifest(t, tmpls)

	// Direct dispatch: RenderTo must discard the partial on error, matching the
	// interpreter, while the string path (Render) still returns that partial to
	// preserve Render's own contract.
	direct := NewWithArray(tmpls, WithCompiled(m))
	var dw strings.Builder
	dErr := direct.RenderTo(&dw, "t.ql", nil)
	if dErr == nil || dErr.Error() != rerr.Error() {
		t.Fatalf("compiled RenderTo error: got %v, want %v", dErr, rerr)
	}
	if dw.String() != iw.String() {
		t.Fatalf("compiled RenderTo wrote %q, want %q (interp parity)", dw.String(), iw.String())
	}
	if strings.Contains(dw.String(), "\x00\x01QUILL_SLOT_") {
		t.Fatalf("compiled RenderTo leaked a raw placeholder: %q", dw.String())
	}
	if out, err := direct.Render("t.ql", nil); err == nil || out != partial {
		t.Fatalf("compiled Render must keep its partial-buffer contract: got %q, %v", out, err)
	}

	// Verify mode: the same withholding applies -- a slots unit writes nothing
	// on error, so w must end empty even though the shadow comparison buffered
	// both engines' partials to compare them.
	var divs int
	shadow := NewWithArray(tmpls,
		WithCompiled(m),
		WithCompiledVerify(func(compiled.Divergence) { divs++ }))
	var sw strings.Builder
	sErr := shadow.RenderTo(&sw, "t.ql", nil)
	if sErr == nil || sErr.Error() != rerr.Error() {
		t.Fatalf("verify RenderTo error: got %v, want %v", sErr, rerr)
	}
	if sw.String() != "" {
		t.Fatalf("verify RenderTo wrote %q, want nothing on a slots error", sw.String())
	}
	if strings.Contains(sw.String(), "\x00\x01QUILL_SLOT_") {
		t.Fatalf("verify RenderTo leaked a raw placeholder: %q", sw.String())
	}
	_ = divs
}

// TestWithCompiledVerifyNilIsDirectDispatch pins that a nil callback leaves
// direct dispatch on, mirroring WithCoverage(nil).
func TestWithCompiledVerifyNilIsDirectDispatch(t *testing.T) {
	env := NewWithArray(map[string]string{"t.ql": "hi"},
		WithCompiled(markerManifest("t.ql", "hi", defaultFingerprint(), false)),
		WithCompiledVerify(nil))
	out, err := env.Render("t.ql", nil)
	if err != nil || out != dispatchMarker {
		t.Fatalf("got %q, %v", out, err)
	}
}

// TestWithCompiledVerifyDoubleRenderIsolation pins the copy-on-write claim the
// shadow mode relies on: rendering twice over one vars map (a template that
// mutates a bound array) must produce identical bytes both times, because the
// first render's mutation privatizes and cannot leak into the second.
func TestWithCompiledVerifyDoubleRenderIsolation(t *testing.T) {
	const src = "@set m.x = m.x + 1\n{{ m.x }}"
	env := NewWithArray(map[string]string{"t.ql": src},
		WithCompiled(markerManifest("t.ql", src, defaultFingerprint(), false)),
		WithCompiledVerify(func(compiled.Divergence) {}))
	m := runtime.NewArray()
	m.SetStr("x", runtime.Int(1))
	vars := map[string]runtime.Value{"m": runtime.Arr(m)}
	for i := 0; i < 2; i++ {
		out, err := env.Render("t.ql", vars)
		if err != nil {
			t.Fatal(err)
		}
		if out != "2" {
			t.Fatalf("render %d: got %q, want %q (input mutated across renders)", i, out, "2")
		}
	}
}

// TestWithCompiledRecordsOutSizeHint pins that a compiled render feeds the
// same warm-render Builder sizing hint the interpreter path maintains, so the
// second by-name render pre-grows instead of paying the doubling ladder.
func TestWithCompiledRecordsOutSizeHint(t *testing.T) {
	env := NewWithArray(map[string]string{"t.ql": "hi"},
		WithCompiled(markerManifest("t.ql", "hi", defaultFingerprint(), false)))
	if _, err := env.Render("t.ql", nil); err != nil {
		t.Fatal(err)
	}
	tmpl, err := env.LoadTemplate("t.ql")
	if err != nil {
		t.Fatal(err)
	}
	if got := tmpl.OutGrowHint(); got != len(dispatchMarker) {
		t.Fatalf("hint after compiled render: got %d, want %d", got, len(dispatchMarker))
	}
}

func TestWithCompiledMalformedManifestIgnored(t *testing.T) {
	noRender := &compiled.Manifest{Entry: "t.ql", Sources: map[string]string{"t.ql": "hi"}, Fingerprint: defaultFingerprint()}
	noEntrySource := markerManifest("t.ql", "hi", defaultFingerprint(), false)
	delete(noEntrySource.Sources, "t.ql")
	env := NewWithArray(map[string]string{"t.ql": "hi"},
		WithCompiled(nil, noRender, noEntrySource))
	out, err := env.Render("t.ql", nil)
	if err != nil || out != "hi" {
		t.Fatalf("got %q, %v", out, err)
	}
}

// TestWithCompiledConcurrentRenders exercises the shared dispatch state (the
// unit map, the coherence memo, the manifest) from concurrent renders on one
// Environment, alongside a verify-mode Environment sharing the same manifest
// value; run with -race this is the artifact-sharing safety gate.
func TestWithCompiledConcurrentRenders(t *testing.T) {
	parity := &compiled.Manifest{
		Entry:       "static.ql",
		Sources:     map[string]string{"static.ql": "Hello\n"},
		Fingerprint: defaultFingerprint(),
		Render: func(w io.Writer, _ *ext.ExtensionSet, _ map[string]runtime.Value) error {
			_, err := io.WriteString(w, "Hello\n")
			return err
		},
	}
	templates := map[string]string{"static.ql": "Hello\n", "dyn.ql": "{{ n + 1 }}"}
	direct := NewWithArray(templates, WithCompiled(parity))
	shadow := NewWithArray(templates, WithCompiled(parity),
		WithCompiledVerify(func(d compiled.Divergence) { t.Errorf("unexpected divergence: %+v", d) }))

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vars := map[string]runtime.Value{"n": runtime.Int(41)}
			for i := 0; i < 100; i++ {
				out, err := direct.Render("static.ql", nil)
				if err != nil || out != "Hello\n" {
					t.Errorf("direct compiled render: %q, %v", out, err)
					return
				}
				var b strings.Builder
				if err := direct.RenderTo(&b, "static.ql", nil); err != nil || b.String() != "Hello\n" {
					t.Errorf("direct compiled RenderTo: %q, %v", b.String(), err)
					return
				}
				out, err = direct.Render("dyn.ql", vars)
				if err != nil || out != "42" {
					t.Errorf("interp render: %q, %v", out, err)
					return
				}
				out, err = shadow.Render("static.ql", nil)
				if err != nil || out != "Hello\n" {
					t.Errorf("shadow render: %q, %v", out, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestEnvironmentFieldsClassifiedForCompiledDispatch is the dispatch-gate
// canary: every Environment field must be classified below, so adding a field
// without deciding how compiled dispatch treats it fails this test instead of
// silently failing open. The classes are: "fingerprint" (compared against the
// manifest's compile options), "feature-gate" (any non-zero configuration
// forces interpreter fallback), "log-gate" (UsesLog units fall back unless the
// logger discards), "source-coherence" (feeds the member byte-verification),
// "registry-passthrough" (handed to the generated function at render time, so
// both engines resolve through the same state), "load-machinery" (shapes
// template loading identically for both paths), "not-compilable" (reachable
// only through constructs the compile backend rejects, so no compiled unit
// can observe it), and "dispatch-state" (the dispatch's own bookkeeping).
func TestEnvironmentFieldsClassifiedForCompiledDispatch(t *testing.T) {
	classified := map[string]string{
		"loader":          "source-coherence",
		"cache":           "source-coherence",
		"renderCache":     "not-compilable",
		"extensions":      "registry-passthrough",
		"preparedMu":      "load-machinery",
		"prepared":        "load-machinery",
		"hostLayers":      "registry-passthrough",
		"autoescapeHTML":  "fingerprint",
		"strictVariables": "fingerprint",
		"randomSeed":      "fingerprint",
		"randomSeedSet":   "fingerprint",
		"policy":          "feature-gate",
		"sandboxActive":   "feature-gate",
		"typeRegistry":    "feature-gate",
		"coverage":        "feature-gate",
		"tabWidth":        "fingerprint",
		"logger":          "log-gate",
		"compiledUnits":   "dispatch-state",
		"compiledVerify":  "dispatch-state",
	}
	validClasses := map[string]bool{
		"fingerprint": true, "feature-gate": true, "log-gate": true,
		"source-coherence": true, "registry-passthrough": true,
		"load-machinery": true, "not-compilable": true, "dispatch-state": true,
	}
	for field, class := range classified {
		if !validClasses[class] {
			t.Errorf("field %q has unknown class %q", field, class)
		}
	}
	typ := reflect.TypeOf((*Environment)(nil)).Elem()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if _, ok := classified[name]; !ok {
			t.Errorf("Environment field %q is not classified for compiled dispatch; decide whether it belongs in the fingerprint, a gate, or passthrough, add it to compiledFor if needed, and record it here", name)
		}
	}
	for name := range classified {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("classified field %q no longer exists on Environment; remove it here and from compiledFor if it was gated", name)
		}
	}
}
