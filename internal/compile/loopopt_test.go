package compile_test

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/internal/compile"
)

// loopOptCases is the capture-centered battery for the compiled loop
// optimizer: every template renders through the compiled path and must match
// the facade byte-for-byte (output or error text), across the frozen-capture
// contract, the parent-chain inlining, the materialization triggers, and the
// scope-enumeration parity surfaces (_context, dump, hints).
var loopOptCases = []compiledCase{
	// T7 frozen snapshot and variants: a captured loop is a frozen
	// per-iteration object, never a live handle.
	{name: "opt-t7-snapshot", template: "@for n in [10,20,30] {\n@if not loop.first {\nwas={{ snap.index }}\n@}\n@set snap = loop\n@}\n"},
	{name: "opt-snap-first-read-later", template: "@for n in [10,20,30] {\n@if loop.first {\n@set snap = loop\n@}\n{{ snap.index }}/{{ snap.last }};\n@}\n{{ snap.revindex }}\n", varsJSON: `{"snap":0}`},
	{name: "opt-snap-subscript", template: "@for n in [7,8] {\n@set snap = loop\n{{ snap[\"index\"] }}{{ snap[\"first\"] }};\n@}\n"},
	{name: "opt-snap-parent", template: "@for a in [10,20] {\n@for b in [1] {\n@set snap = loop\n@}\n{{ snap.parent.index }},{{ snap.index }};\n@}\n", varsJSON: `{"snap":0}`},

	// Bare loop escaping into calls, filters, lists, and comparisons.
	{name: "opt-loop-piped", template: "@for x in [1] {\n{{ loop | default(1) }}\n@}\n"},
	{name: "opt-loop-in-list", template: "@for x in [1,2] {\n{{ [loop] | length }}\n@}\n"},
	{name: "opt-loop-emitted", template: "@for x in [1] {\n{{ loop }}\n@}\n"},
	{name: "opt-loop-is-mapping", template: "@for x in [1] {\n{{ loop is mapping }}\n@}\n"},
	{name: "opt-context-loop-eq", template: "@for x in [1] {\n{{ _context.loop == loop }}\n@}\n"},

	// Arrows: a loop read inside an arrow applied after the loop sees the
	// live (last) binding, exactly like the interpreter's lexical capture.
	{name: "opt-arrow-applied-after", template: "@set f = null\n@for x in [10,20] {\n@set f = (n) => loop.index + n\n@}\n{{ [0] | map(f) | first }}\n"},
	{name: "opt-arrow-applied-inside", template: "@for x in [10,20] {\n{{ [100] | map((n) => loop.index + n) | first }};\n@}\n"},
	{name: "opt-arrow-dump-inside", template: "@for x in [1,2] {\n{{ [0] | map((n) => dump()) | first }};\n@}\n"},
	{name: "opt-arrow-dump-after", template: "@set f = null\n@for x in [1,2] {\n@set f = () => dump()\n@}\n{{ f() }}\n"},

	// Parent chains: all-inline chains at depth, and the coupling rules when
	// one side of the chain is captured.
	{name: "opt-parent-3deep", template: "@for a in [1,2] {\n@for b in [3] {\n@for c in [4,5] {\n{{ loop.parent.parent.index }}{{ loop.parent.revindex }}{{ loop.index }},\n@}\n@}\n@}\n"},
	{name: "opt-parent-prev-next", template: "@for a in [1,2,3] {\n@for b in [9] {\n({{ loop.parent.prev ?? \"-\" }}<{{ loop.parent.next ?? \"-\" }})\n@}\n@}\n"},
	{name: "opt-inner-captures-outer-forced", template: "@for a in [10,20] {\n@for b in [1,2] {\n@set snap = loop\n{{ snap.parent.index }}{{ snap.index }};\n@}\n@}\n"},
	{name: "opt-outer-captures-inner-inline", template: "@for a in [10,20] {\n@set s = loop\n@for b in [1,2] {\n{{ loop.index }}{{ s.first }};\n@}\n@}\n"},
	{name: "opt-parent-read-outer-captured", template: "@for a in [10,20] {\n@set s = loop\n@for b in [1,2] {\n{{ loop.parent.index }}{{ s.last }};\n@}\n@}\n"},

	// loop.changed keeps its per-call-site memory under inlining.
	{name: "opt-changed-multi-site", template: "@for r in rows {\n@if loop.changed(r.g) {\nG{{ r.g }}\n@}\n@if loop.changed(r.v) {\nV{{ r.v }}\n@}\n{{ loop.index }};\n@}\n", varsJSON: `{"rows":[{"g":"a","v":1},{"g":"a","v":1},{"g":"b","v":2}]}`},
	{name: "opt-changed-filter-and-body", template: "@for x in [1,1,2,2,3] if loop.changed(x) {\n{{ loop.index }}:{{ x }}\n@}\n"},

	// prev/next at the boundaries, single-element and multi-element.
	{name: "opt-prev-next-single", template: "@for x in [5] {\n{{ loop.prev ?? \"-\" }}/{{ loop.next ?? \"-\" }}\n@}\n"},
	{name: "opt-prev-next-multi", template: "@for x in [1,2,3] {\n({{ loop.prev ?? \"-\" }}<{{ x }}>{{ loop.next ?? \"-\" }})\n@}\n"},

	// Fused @for..if: loop.* in the body counts survivors only; the condition
	// resolves loop in the ENCLOSING frame (the interpreter's filter scope has
	// no loop bound), so a nested condition reads the outer loop and a
	// top-level one is an undefined-variable error.
	{name: "opt-fused-if-fields", template: "@for x in [1,2,3,4,5,6] if x % 2 == 0 {\n{{ loop.index }}/{{ loop.length }}/{{ loop.revindex }}:{{ x }}\n@}\n"},
	{name: "opt-fused-cond-outer-ref", template: "@for a in [1,2] {\n@for b in [7,8] if loop.index == 1 {\n{{ b }}(p{{ loop.parent.index }}i{{ loop.index }})\n@}\n@}\n"},
	{name: "opt-fused-cond-toplevel-error", template: "@for x in [1] if loop.index > 0 {\n{{ x }}\n@}\n"},

	// Two-target loops and the empty @else arm.
	{name: "opt-two-target-fields", template: "@for k, v in {a: 1, b: 2} {\n{{ k }}={{ v }}@{{ loop.index }}/{{ loop.length }}\n@}\n"},
	{name: "opt-empty-else", template: "@for x in [] {\n{{ loop.index }}\n@} else {\nnone\n@}\n"},

	// Strict-undefined parity: unknown loop members carry the interpreter's
	// exact error text and position in both member forms.
	{name: "opt-bogus-field", template: "@for x in [1] {\n{{ loop.bogus }}\n@}\n"},
	{name: "opt-bogus-subscript", template: "@for x in [1] {\n{{ loop[\"bogus\"] }}\n@}\n"},
	{name: "opt-depth-field", template: "@for x in [1] {\n{{ loop.depth }}\n@}\n"},
	{name: "opt-nonliteral-subscript", template: "@set k = \"index\"\n@for x in [7,8] {\n{{ loop[k] }}\n@}\n"},
	{name: "opt-member-assign-loop", template: "@for x in [1] {\n@set loop.index = 5\n@}\n"},

	// Scope enumeration: _context and dump() must show loop at the right
	// position with the right value, and the undefined-name hint must list
	// loop, whether or not the loop is inline.
	{name: "opt-context-keys", template: "@for x in [7,8] {\n{{ _context | keys | join(\",\") }};\n@}\n"},
	{name: "opt-context-loop-value", template: "@for x in [7,8] {\n@set c = _context\n{{ c.loop.index }}\n@}\n"},
	{name: "opt-context-identity", template: "@for x in [7] {\n@set c1 = _context\n@set c2 = _context\n{{ c1.loop == c2.loop }}\n@}\n"},
	{name: "opt-dump-inline-loop", template: "@for x in [7,8] {\n{{ dump() }}\n@}\n"},
	{name: "opt-dump-nested-inline", template: "@for a in [1,2] {\n@for b in [3] {\n{{ dump() }}{{ loop.parent.index }}{{ loop.index }}\n@}\n@}\n"},
	{name: "opt-dump-after-set", template: "@for x in [1,2] {\n@set y = x * 10\n{{ dump() }}\n@}\n"},
	{name: "opt-dump-vars-loop-name", template: "@for x in [1] {\n{{ dump() }}\n@}\n", varsJSON: `{"loop":5}`},
	// The on-demand materialization must hand out the ENTRY-time parent, like
	// the interpreter's pre.Get("loop") before the first iteration: here the
	// probe resolves to a with-map entry named loop backed by a cell-held
	// array that is member-assigned mid-loop, so a consumption-time re-probe
	// at the dump() site would be one host-visible mutation away from
	// diverging.
	{name: "opt-parent-probe-with-map-mutated", template: "@set c = cell([1,2,3])\n@with {loop: c.value} {\n@for x in [7,8] {\n@set c.value[0] = 99\n{{ dump() }};\n@}\n@}\n{{ c.value[0] }}\n"},
	{name: "opt-dump-under-captured-outer", template: "@for a in [1,2] {\n@set s = loop\n@for b in [3] {\n{{ dump() }}{{ s.index }}\n@}\n@}\n"},
	{name: "opt-dump-in-filter-cond", template: "@for x in [1,2] if (dump() | length) > 0 {\n{{ loop.index }}:{{ x }}\n@}\n"},
	{name: "opt-changed-under-with", template: "@for x in [1,1,2] {\n@with {loop: 5} {\n{{ loop.changed(x) }}\n@}\n@}\n"},
	{name: "opt-withonly-inner-captures", template: "@for a in [1,2] {\n@with {} only {\n@for b in [3,4] {\n@set s = loop\n{{ s.index }}{{ s.parent ?? \"P\" }};\n@}\n@}\n{{ loop.index }}\n@}\n"},
	{name: "opt-hint-inline-loop", template: "@for x in [7] {\n@set a = 1\n{{ nope }}\n@}\n"},
	{name: "opt-hint-nested-inline", template: "@for a in [1] {\n@for b in [2] {\n{{ nope }}\n@}\n@}\n"},

	// Rebinds of the name loop: the language allows them; the body then works
	// on the user value with the interpreter's exact behavior.
	{name: "opt-set-loop", template: "@for x in [1,2] {\n@set loop = 99\n{{ loop }}\n@}\n"},
	{name: "opt-inline-assign-loop", template: "@for x in [1] {\n{{ (loop = 3) }}{{ loop }}\n@}\n"},

	// A with frame between the reference and the loop resolves at runtime:
	// a map key loop shadows, anything else falls through to the loop.
	{name: "opt-with-shadow-hit", template: "@for x in [1] {\n@with {loop: {index: 42}} {\n{{ loop.index }}\n@}\n@}\n"},
	{name: "opt-with-shadow-miss", template: "@for x in [7,8] {\n@with {m: 1} {\n{{ loop.index }}\n@}\n@}\n"},
	{name: "opt-with-only-cuts", template: "@for x in [1] {\n@with {m: 2} only {\n{{ loop.index }}\n@}\n@}\n"},

	// is defined decomposes chains into presence probes; the loop object
	// answers exactly like the interpreter's.
	{name: "opt-is-defined", template: "@for x in [1] {\n{{ loop is defined }} {{ loop.index is defined }} {{ loop.bogus is defined }}\n@}\n"},

	// Null-safe member reads on loop.
	{name: "opt-nullsafe-field", template: "@for x in [7,8] {\n{{ loop?.index }}\n@}\n"},

	// Copy-back interplay: body rebinds of pre-existing names persist while
	// the loop's own control bindings never leak, inline or not.
	{name: "opt-copyback-under-inline", template: "@set total = 0\n@for x in [1,2,3] {\n@set total = total + loop.index\n@}\n{{ total }} {{ loop is defined }}\n"},
	{name: "opt-else-reads-outer", template: "@for a in [4,5] {\n@for b in [] {\n{{ b }}\n@} else {\nE{{ loop.index }}\n@}\n@}\n"},
	{name: "opt-iterand-reads-outer", template: "@for a in [2,3] {\n@for b in [loop.index, loop.revindex] {\n{{ b }};\n@}\n@}\n"},
}

// TestLoopOptimizerParity renders the capture battery through the compiled
// path and asserts byte-equality (output or error text) against the facade.
func TestLoopOptimizerParity(t *testing.T) {
	results := map[string]*compile.Result{}
	for _, cs := range loopOptCases {
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cs.name, err)
		}
		results[cs.name] = res
	}
	got := runCompiled(t, loopOptCases, results)
	for _, cs := range loopOptCases {
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
