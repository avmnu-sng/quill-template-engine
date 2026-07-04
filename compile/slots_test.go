package compile_test

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/compile"
)

// slotCase is one deferred-slot parity contract: the template, the pinned
// expected output, an autoescape flag, and the case name. The compiled render
// must match the facade byte for byte AND the facade must match the pinned
// contract, so a drift on either side fails loudly.
type slotCase struct {
	name string
	src  string
	want string
	auto bool
}

// slotsBattery pins the @provide / @yield / slot() contracts through the
// compiled buffered-then-resolve path: deferral (a yield before its provides),
// execution-order accumulation across multiple provides and a provide inside a
// @for, slot()'s read-as-of-call immediacy, an unprovided label resolving to
// "", a label yielded twice, two interleaved labels, @provide @set copy-back to
// the enclosing frame, the escape-region rules (provide body escaped, yield
// placeholder raw, slot() wrapped Safe), a @tab region around a @yield, and a
// single-token non-collision proof where authored text mimics the placeholder.
var slotsBattery = []slotCase{
	{"yield_before_provide",
		"top:\n@yield body\nbottom.\n@provide body {\ncollected\n@}\n",
		"top:\ncollected\nbottom.\n", false},
	{"execution_order_multi_provide",
		"@yield x\n@provide x {\na\n@}\n@provide x {\nb\n@}\n@provide x {\nc\n@}\n",
		"a\nb\nc\n", false},
	{"provide_in_for_accumulates",
		"@yield rows\n@for n in [1,2,3] {\n@provide rows {\nrow {{ n }}\n@}\n@}\n",
		"row 1\nrow 2\nrow 3\n", false},
	{"slot_fn_immediate_read",
		"@provide s {\none\n@}\nmid={{ slot(\"s\") }}\n@provide s {\ntwo\n@}\nend={{ slot(\"s\") }}\n",
		"mid=one\n\nend=one\ntwo\n\n", false},
	{"unprovided_label_empty",
		"before\n@yield gone\nafter\n",
		"before\nafter\n", false},
	{"label_yielded_twice",
		"@yield dup\n---\n@yield dup\n@provide dup {\nX\n@}\n",
		"X\n---\nX\n", false},
	{"two_interleaved_labels",
		"@yield a\n@yield b\n@provide a {\nAA\n@}\n@provide b {\nBB\n@}\n@provide a {\naa\n@}\n",
		"AA\naa\nBB\n", false},
	{"provide_set_copyback",
		"@set n = 0\n@provide s {\n@set n = n + 5\nx\n@}\n@provide s {\n@set n = n + 5\ny\n@}\nn={{ n }}\n@yield s\n",
		"n=10\nx\ny\n", false},
	{"slot_fn_before_any_provide",
		"empty=[{{ slot(\"nope\") }}]\n",
		"empty=[]\n", false},
	{"escape_region_provide_and_yield",
		"@yield h\n@provide h {\n{{ raw }}\n@}\n",
		"a&lt;b\n", true},
	{"escape_slot_fn_safe_not_double_escaped",
		"@provide h {\n{{ raw }}\n@}\ngot={{ slot(\"h\") }}\n",
		"got=a&lt;b\n\n", true},
	{"tab_region_around_yield",
		"@tab(1) {\n@yield body\n@}\n@provide body {\nline one\nline two\n@}\n",
		"    line one\nline two\n", false},
	{"authored_token_lookalike_survives",
		"@yield s\n@provide s {\nQUILL_SLOT_9 stays\n@}\n",
		"QUILL_SLOT_9 stays\n", false},
}

// slotCaseVars supplies the one autoescape battery input: a value carrying an
// HTML-special byte so the escape-region cases can prove the provide body is
// escaped once and the yield placeholder and slot() result are not escaped a
// second time.
func slotCaseVars(name string) string {
	switch name {
	case "escape_region_provide_and_yield", "escape_slot_fn_safe_not_double_escaped":
		return `{"raw": "a<b"}`
	default:
		return ""
	}
}

// TestSlotsBattery renders the deferred-slot battery through the compiled path,
// asserting each output byte-equal to the facade's Render AND to the pinned
// contract, and that no raw NUL-wrapped yield placeholder survives in any
// compiled output (the leak class the static yield-nesting gate removes).
func TestSlotsBattery(t *testing.T) {
	var cases []compiledCase
	results := map[string]*compile.Result{}
	for _, sc := range slotsBattery {
		cs := compiledCase{
			name:     sc.name,
			template: sc.src,
			varsJSON: slotCaseVars(sc.name),
			opts:     compile.Options{AutoescapeHTML: sc.auto},
			envCheck: true,
		}
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", sc.name, err)
		}
		results[sc.name] = res
		cases = append(cases, cs)
	}
	got := runCompiled(t, cases, results)
	for i, sc := range slotsBattery {
		r, ok := got[sc.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", sc.name)
			continue
		}
		if r.failed {
			t.Errorf("%s: compiled render errored: %s", sc.name, r.errText)
			continue
		}
		want, err := renderInterp(t, cases[i])
		if err != nil {
			t.Errorf("%s: interp render errored: %v", sc.name, err)
			continue
		}
		if want != sc.want {
			t.Errorf("%s: interpreter drifted from the pinned contract\n got  %q\n want %q", sc.name, want, sc.want)
		}
		if r.out != want {
			t.Errorf("%s: compiled output differs from interpreter\n got  %q\n want %q", sc.name, r.out, want)
		}
		if strings.Contains(r.out, "\x00\x01QUILL_SLOT_") {
			t.Errorf("%s: a raw yield placeholder leaked into compiled output: %q", sc.name, r.out)
		}
		// The by-name Environment render (WithCompiled) must equal the
		// interpreter too, and the tracer must confirm the dispatch gate served
		// the compiled unit rather than falling back.
		if envR, ok := got[sc.name+"@env"]; ok {
			if envR.failed {
				t.Errorf("%s: env-dispatch render errored: %s", sc.name, envR.errText)
			} else if envR.out != want {
				t.Errorf("%s: env-dispatch output differs from interpreter\n got  %q\n want %q", sc.name, envR.out, want)
			}
		}
		if tr, ok := got[sc.name+"@tracer"]; ok && tr.out != "served" {
			t.Errorf("%s: dispatch gate fell back for a slots unit it should serve", sc.name)
		}
		if mx, ok := got[sc.name+"@matrix"]; ok && mx.out != "ok" {
			t.Errorf("%s: fingerprint-matrix leg reported %q", sc.name, mx.out)
		}
		// The streaming dispatch must reproduce the resolved output on the success
		// path too: a slots unit routed through RenderTo's scratch buffer writes
		// the same bytes Render returns, with no placeholder surviving.
		if rt, ok := got[sc.name+"@renderto-ok"]; ok && rt.out != "ok" {
			if rt.failed {
				t.Errorf("%s: success RenderTo leg reported %q", sc.name, rt.errText)
			} else {
				t.Errorf("%s: success RenderTo leg reported %q", sc.name, rt.out)
			}
		}
	}
}

// slotsErrorBattery pins the streaming-dispatch error path for compiled slots
// units: each template uses a top-level @yield (so it compiles as a buffered
// slots unit) and then errors mid-render -- before the placeholder is written,
// after it, inside an escape region, inside a @provide body after a second
// label's placeholder was already yielded, and through an array-as-text failure
// rather than an undefined variable. The interpreter's RenderTo buffers a slots
// render and writes nothing on error, so the compiled Environment.RenderTo must
// withhold its partial, still-unresolved buffer too -- otherwise a raw yield
// placeholder leaks to the caller's writer, regardless of which label is open or
// what raised the error. The success-path battery renders only fully resolved
// output, so it cannot observe this class; this battery drives each case through
// Environment.RenderTo on both engines and compares the bytes.
var slotsErrorBattery = []slotCase{
	{"error_after_yield_withholds_placeholder",
		"@yield s\nvisible\n@provide s {\nprovided\n@}\n{{ missing }}\n",
		"", false},
	{"error_before_yield_withholds_partial",
		"head\n{{ missing }}\n@yield s\n@provide s {\nprovided\n@}\n",
		"", false},
	{"error_in_escape_region_withholds_placeholder",
		"@yield s\n@provide s {\n{{ raw }}\n@}\n{{ missing }}\n",
		"", true},
	// The error fires inside a @provide body after a SECOND label's placeholder
	// was already yielded: neither the resolved nor the still-open label may reach
	// the writer, so the interleaved-label buffer must be withheld whole.
	{"error_in_provide_after_other_yield",
		"@yield a\n@yield b\n@provide a {\nAAA\n@}\n@provide b {\npre {{ missing }}\n@}\n",
		"", false},
	// The error is an array-as-text render failure, not an undefined variable, so
	// the withholding cannot key off the undefined path -- any mid-render error on
	// a slots unit must discard the partial buffer.
	{"error_array_as_text_after_yield",
		"@yield s\nrow\n@provide s {\nx\n@}\n{{ arr }}\n",
		"", false},
}

// slotsErrorCaseVars feeds the escape-region error case its HTML-special value
// and the array-as-text case its unrenderable array; the undefined prints that
// raise the other errors take no input.
func slotsErrorCaseVars(name string) string {
	switch name {
	case "error_in_escape_region_withholds_placeholder":
		return `{"raw": "a<b"}`
	case "error_array_as_text_after_yield":
		return `{"arr": [1]}`
	}
	return ""
}

// TestSlotsErrorPathNoLeak drives each mid-render error case through
// Environment.RenderTo on both the interpreter and the installed compiled unit
// and asserts they write byte-identical output (nothing, for a slots error) and
// that no raw yield placeholder reaches the writer. This is the leak class the
// success-only TestSlotsBattery misses: the generated slots render writes its
// partial, unresolved buffer to whatever writer it is handed on error, so
// streaming dispatch must route it through a scratch buffer discarded on error.
func TestSlotsErrorPathNoLeak(t *testing.T) {
	var cases []compiledCase
	results := map[string]*compile.Result{}
	for _, sc := range slotsErrorBattery {
		cs := compiledCase{
			name:     sc.name,
			template: sc.src,
			varsJSON: slotsErrorCaseVars(sc.name),
			opts:     compile.Options{AutoescapeHTML: sc.auto},
			errPath:  true,
		}
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", sc.name, err)
		}
		results[sc.name] = res
		cases = append(cases, cs)
	}
	got := runCompiled(t, cases, results)
	for _, sc := range slotsErrorBattery {
		r, ok := got[sc.name+"@renderto"]
		if !ok {
			t.Errorf("%s: no RenderTo result from scratch run", sc.name)
			continue
		}
		if r.failed {
			t.Errorf("%s: RenderTo leg errored: %s", sc.name, r.errText)
			continue
		}
		if r.out != "ok" {
			t.Errorf("%s: RenderTo leg reported %q", sc.name, r.out)
		}
	}
}
