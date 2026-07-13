package compile_test

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/internal/compile"
)

// liveIterationCases is the differential battery for the zero-copy loop
// iteration (slice C3): every template renders through the compiled path and
// must match the facade byte-for-byte (output or error text). It centers on
// mutation-during-iteration shapes (which must take the pair-snapshot path,
// so an in-place write stays invisible to the iteration exactly like the
// interpreter's entry-time snapshot) plus the live-path parity surfaces:
// key reconstruction, prev/next boundaries, nesting, the empty else arm, the
// runtime non-array fallback, and the on-demand loop-object materialization.
var liveIterationCases = []compiledCase{
	// Mutation during iteration: member assignment on the iterated name in
	// the SAME frame, reading both the elements and the name afterwards.
	{name: "live-mut-same-frame", template: "@set xs = [1,2,3]\n@for x in xs {\n@set xs[0] = 99\n{{ x }};\n@}\n{{ xs[0] }}\n"},
	// Genuine in-place mutation: the cell's inner array is single-owner, so
	// the subscript write lands in the very array being iterated; the
	// snapshot semantics keep it invisible to x.
	{name: "live-mut-cell-slot", template: "@set c = cell([1,2,3])\n@for x in c.value {\n@set c.value[0] = 99\n{{ x }};\n@}\n{{ c.value[0] }}\n"},
	// Append during iteration through the cell: the growth is invisible to
	// the loop but visible afterwards.
	{name: "live-mut-cell-append", template: "@set c = cell([1,2])\n@for x in c.value {\n@set c.value[c.value | length] = x * 10\n@}\n{{ c.value | join(\",\") }}\n"},
	// @do with a method call: a cell has no methods, so both engines raise
	// the same error mid-loop.
	{name: "live-mut-do-method-error", template: "@set c = cell([1,2,3])\n@for x in c.value {\n@do c.push(x)\n@}\n"},
	// @do alone forces the pairs path even when its expression is benign.
	{name: "live-mut-do-benign", template: "@set t = 0\n@for x in [1,2,3] {\n@do (t = t + x)\n@}\n{{ t }}\n"},
	// Two-target map iteration with member assignment growing the map.
	{name: "live-mut-map-two-target", template: "@set m = {a: 1, b: 2}\n@for k, v in m {\n@set m[k ~ \"x\"] = v * 10\n{{ k }}={{ v }};\n@}\n{{ m | keys | join(\",\") }}\n"},
	// Member assignment on an UNRELATED name still forces the pairs path.
	{name: "live-mut-unrelated-name", template: "@set ys = [9]\n@for x in [1,2] {\n@set ys[0] = x\n{{ x }};\n@}\n{{ ys[0] }}\n"},
	// An arrow whose body carries a method call forces the pairs path even
	// though the arrow is never invoked.
	{name: "live-arrow-method-forces-pairs", template: "@set c = cell(5)\n@for x in [1,2] {\n@set f = () => c.push(x)\n{{ x }};\n@}\n"},

	// Live-path parity: plain read-only loops over lists and maps.
	{name: "live-plain-list", template: "@for x in [10,20,30] {\n{{ loop.index }}:{{ x }};\n@}\n"},
	{name: "live-two-target-map", template: "@for k, v in m {\n{{ k }}={{ v }};\n@}\n", varsJSON: `{"m":{"a":1,"01":2,"2":3}}`},
	{name: "live-cell-value-read-only", template: "@set c = cell([1,2,3])\n@for x in c.value {\n{{ x }};\n@}\n"},
	// A plain rebind of the iterated name is not a mutation: the live loop
	// keeps iterating the entry-time array exactly like the snapshot.
	{name: "live-rebind-iterand", template: "@set xs = [1,2,3]\n@for x in xs {\n@set xs = [9]\n{{ x }};\n@}\n{{ xs | join(\",\") }}\n"},

	// prev/next and revindex at the boundaries on the live path.
	{name: "live-prev-next", template: "@for x in [1,2,3] {\n({{ loop.prev ?? \"-\" }}<{{ x }}>{{ loop.next ?? \"-\" }}){{ loop.revindex }}\n@}\n"},
	{name: "live-prev-next-single", template: "@for x in [5] {\n{{ loop.prev ?? \"-\" }}/{{ loop.next ?? \"-\" }}\n@}\n"},

	// Nested read-only loops, including a parent-chain read into the outer
	// live loop's neighbours.
	{name: "live-nested-read-only", template: "@for a in [1,2] {\n@for b in [3,4] {\n{{ loop.parent.index }}{{ loop.index }}{{ loop.parent.prev ?? \"-\" }};\n@}\n@}\n"},

	// The empty else arm fires on the live length snapshot.
	{name: "live-empty-else", template: "@for x in [] {\n{{ x }}\n@} else {\nnone\n@}\n"},

	// Non-array iterands fall back to the pairs path at runtime: the strict
	// error and the lenient empty loop match the interpreter exactly.
	{name: "live-non-array-strict", template: "@for x in n {\n{{ x }}\n@}\n", varsJSON: `{"n":5}`},
	{name: "live-non-array-lenient", template: "@for x in n {\n{{ x }}\n@} else {\nempty\n@}\n", varsJSON: `{"n":5}`, opts: compile.Options{LenientVariables: true}},

	// A with-map value as the iterand.
	{name: "live-with-map-value", template: "@with {xs: [7,8]} {\n@for x in xs {\n{{ x }}{{ loop.last }};\n@}\n@}\n"},

	// Nesting across the path split: a live inner inside a pairs outer (the
	// outer's member assignment forces it), and a pairs inner (fused) inside
	// a live outer.
	{name: "live-inner-in-pairs-outer", template: "@set c = cell(0)\n@for a in [1,2] {\n@set c.value = c.value + a\n@for b in [5,6] {\n{{ a }}{{ b }}{{ loop.index }};\n@}\n@}\n{{ c.value }}\n"},
	{name: "pairs-inner-in-live-outer", template: "@for a in [1,2] {\n@for b in [3,4,5] if b > 3 {\n{{ a }}{{ b }}{{ loop.index }}/{{ loop.length }}{{ loop.parent.last }};\n@}\n@}\n"},

	// On-demand loop-object materialization off the live path (dump's
	// needs-context injection), and loop.changed's per-site memory.
	{name: "live-dump-on-demand", template: "@for x in [7,8] {\n{{ dump() }}{{ loop.index }}\n@}\n"},
	{name: "live-changed", template: "@for x in [1,1,2] {\n{{ loop.changed(x) }};\n@}\n"},
}

// TestLiveIterationParity renders the live-iteration battery through the
// compiled path and asserts byte-equality (output or error text) against the
// facade.
func TestLiveIterationParity(t *testing.T) {
	results := map[string]*compile.Result{}
	for _, cs := range liveIterationCases {
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cs.name, err)
		}
		results[cs.name] = res
	}
	got := runCompiled(t, liveIterationCases, results)
	for _, cs := range liveIterationCases {
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
