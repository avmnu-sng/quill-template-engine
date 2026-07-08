package compile

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// TestLoopEscapeAnalysis pins the escape analysis itself, independent of the
// end-to-end byte parity: for each template, the @for nodes in source walk
// order must classify exactly as listed (true = inline, false = materialized).
// The table exercises every escape trigger and the outward propagation rule.
func TestLoopEscapeAnalysis(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []bool // per @for in walk order: true means inline
	}{
		// The pure-arithmetic surface stays inline.
		{"no_captures",
			"@for x in xs {\n{{ loop.index }}{{ loop.prev ?? 0 }}{{ loop.next ?? 0 }}\n@}", []bool{true}},
		{"all_fields_inline",
			"@for x in xs {\n{{ loop.index0 }}{{ loop.revindex }}{{ loop.revindex0 }}{{ loop.first }}{{ loop.last }}{{ loop.length }}\n@}", []bool{true}},
		{"literal_subscript",
			"@for x in xs {\n{{ loop[\"index\"] }}\n@}", []bool{true}},
		{"changed_stays_inline",
			"@for x in xs {\n@if loop.changed(x) {\nc\n@}\n@}", []bool{true}},
		{"changed_in_filter_inline",
			"@for x in xs if loop.changed(x) {\n{{ x }}\n@}", []bool{true}},
		{"plain_filter_call_inline",
			"@for x in xs {\n{{ x | upper }}\n@}", []bool{true}},
		{"dump_call_inline_on_demand",
			"@for x in xs {\n{{ dump() }}\n@}", []bool{true}},
		{"two_target_inline",
			"@for k, v in m {\n{{ k }}{{ loop.index }}\n@}", []bool{true}},
		{"else_arm_no_ref",
			"@for x in xs {\n{{ loop.index }}\n@} else {\nnone\n@}", []bool{true}},
		{"with_only_no_ref",
			"@for x in xs {\n@with {} only {\nhi\n@}\n@}", []bool{true}},

		// Direct escapes: bare loop as a value in any position.
		{"bind_bare_loop",
			"@for x in xs {\n@set snap = loop\n@}", []bool{false}},
		{"loop_piped",
			"@for x in xs {\n{{ loop | default(1) }}\n@}", []bool{false}},
		{"loop_fn_arg",
			"@for x in xs {\n{{ dump(loop) }}\n@}", []bool{false}},
		{"loop_in_list",
			"@for x in xs {\n{{ [loop] | length }}\n@}", []bool{false}},
		{"loop_compared",
			"@for x in xs {\n{{ loop == x }}\n@}", []bool{false}},
		{"loop_emitted",
			"@for x in xs {\n{{ loop }}\n@}", []bool{false}},
		{"loop_test_subject",
			"@for x in xs {\n{{ loop is mapping }}\n@}", []bool{false}},
		{"loop_map_shorthand",
			"@for x in xs {\n{{ {loop} | length }}\n@}", []bool{false}},
		{"loop_changed_arg_bare_loop",
			"@for x in xs {\n{{ loop.changed(loop) }}\n@}", []bool{false}},
		{"method_call_on_loop",
			"@for x in xs {\n{{ loop.foo() }}\n@}", []bool{false}},

		// Non-inline member shapes.
		{"unknown_field",
			"@for x in xs {\n{{ loop.bogus }}\n@}", []bool{false}},
		{"depth_field",
			"@for x in xs {\n{{ loop.depth }}\n@}", []bool{false}},
		{"nonliteral_subscript",
			"@for x in xs {\n{{ loop[k] }}\n@}", []bool{false}},
		{"literal_subscript_unknown",
			"@for x in xs {\n{{ loop[\"bogus\"] }}\n@}", []bool{false}},
		{"nullsafe_field",
			"@for x in xs {\n{{ loop?.index }}\n@}", []bool{false}},
		{"parent_as_value",
			"@for a in xs {\n@for b in ys {\n@set p = loop.parent\n@}\n@}", []bool{false, false}},
		{"is_defined_chain",
			"@for x in xs {\n{{ loop.index is defined }}\n@}", []bool{false}},

		// Rebinds of the name loop.
		{"set_loop",
			"@for x in xs {\n@set loop = 99\n@}", []bool{false}},
		{"set_loop_in_if_arm",
			"@for x in xs {\n@if x {\n@set loop = 1\n@}\n@}", []bool{false}},
		{"inline_assign_loop",
			"@for x in xs {\n{{ (loop = 3) }}\n@}", []bool{false}},
		{"capture_named_loop",
			"@for x in xs {\n@set loop = capture {\nhi\n@}\n@}", []bool{false}},
		{"destructure_loop",
			"@for x in xs {\n@set [loop] = [5]\n@}", []bool{false}},
		{"member_assign_root_loop",
			"@for x in xs {\n@set loop.index = 5\n@}", []bool{false}},
		{"target_named_loop",
			"@for loop in xs {\n{{ 1 }}\n@}", []bool{false}},
		{"set_loop_in_nested_with",
			"@for x in xs {\n@with {a: 1} {\n@set loop = 2\n@}\n@}", []bool{false}},

		// Arrows: any loop reference inside a deferred body escapes, and so
		// does any call site (its needs-context injection runs at call time).
		{"loop_in_arrow",
			"@for x in xs {\n@set f = (n) => n + loop.index\n@}", []bool{false}},
		{"call_in_arrow",
			"@for x in xs {\n@set f = (n) => (n | upper)\n@}", []bool{false}},
		{"fn_call_in_arrow",
			"@for x in xs {\n@set f = () => dump()\n@}", []bool{false}},
		{"arrow_outside_loop_ok",
			"@set f = (n) => (n | upper)\n@for x in xs {\n{{ loop.index }}\n@}", []bool{true}},

		// Scope enumeration.
		{"context_in_body",
			"@for x in xs {\n{{ _context | length }}\n@}", []bool{false}},
		{"context_in_inner_propagates",
			"@for a in xs {\n@for b in ys {\n{{ _context | length }}\n@}\n@}", []bool{false, false}},

		// Resolution the analyzer cannot prove: a with frame between the
		// reference and the loop may shadow the name at runtime.
		{"with_between",
			"@for x in xs {\n@with {m: 1} {\n{{ loop.index }}\n@}\n@}", []bool{false}},
		{"with_only_cuts_no_loop_ref",
			"@for x in xs {\n@with {m: 1} only {\n{{ m }}\n@}\n@}", []bool{true}},
		{"filter_binds_loop_ambiguous",
			"@for a in xs {\n@for b in ys if (loop = 1) == loop {\n{{ b }}\n@}\n@}", []bool{false, true}},

		// Parent chains.
		{"parent_chain_2deep",
			"@for a in xs {\n@for b in ys {\n{{ loop.parent.index }}{{ loop.index }}\n@}\n@}", []bool{true, true}},
		{"parent_chain_3deep",
			"@for a in xs {\n@for b in ys {\n@for c in zs {\n{{ loop.parent.parent.index }}{{ loop.parent.revindex }}{{ loop.prev ?? 0 }}\n@}\n@}\n@}", []bool{true, true, true}},
		{"parent_read_outer_materialized",
			"@for a in xs {\n@set s = loop\n@for b in ys {\n{{ loop.parent.index }}\n@}\n@}", []bool{false, false}},
		{"outer_captures_inner_inline",
			"@for a in xs {\n@set s = loop\n@for b in ys {\n{{ loop.index }}{{ s.first }}\n@}\n@}", []bool{false, true}},
		{"inner_captures_outer_forced",
			"@for a in xs {\n@for b in ys {\n@set snap = loop\n@}\n@}", []bool{false, false}},
		{"withonly_cuts_propagation",
			"@for a in xs {\n@with {} only {\n@for b in ys {\n@set snap = loop\n@}\n@}\n@}", []bool{true, false}},
		{"parent_chase_through_with_fails",
			"@for a in xs {\n@with {m: 1} {\n@for b in ys {\n{{ loop.parent.index }}\n@}\n@}\n@}", []bool{false, false}},

		// Filter conditions resolve loop in the enclosing frame: the fused
		// condition of a nested loop reads the OUTER loop inline; at the top
		// level there is no loop to reference at all.
		{"fused_cond_outer_ref",
			"@for a in xs {\n@for b in ys if loop.index == 1 {\n{{ b }}\n@}\n@}", []bool{true, true}},
		{"fused_cond_toplevel_no_loop",
			"@for x in xs if loop.index > 0 {\n{{ x }}\n@}", []bool{true}},
		{"else_arm_reads_outer",
			"@for a in xs {\n@for b in [] {\n{{ b }}\n@} else {\n{{ loop.index }}\n@}\n@}", []bool{true, true}},
		{"iterand_reads_outer",
			"@for a in xs {\n@for b in [loop.index] {\n{{ b }}\n@}\n@}", []bool{true, true}},

		// Independent siblings classify independently.
		{"sibling_independence",
			"@for x in xs {\n{{ loop.index }}\n@}\n@for y in ys {\n@set s = loop\n@}", []bool{true, false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod, err := parse.Parse(source.New(tc.name+".ql", tc.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			an := analyzeLoops(mod, nil)
			if len(an.fors) != len(tc.want) {
				t.Fatalf("analyzed %d loops, want %d", len(an.fors), len(tc.want))
			}
			for i, n := range an.fors {
				got := an.inlineFor(n)
				if got != tc.want[i] {
					t.Errorf("loop %d (line %d): inline = %v, want %v", i, n.Line, got, tc.want[i])
				}
			}
		})
	}
}

// TestLoopEscapeAnalysisAcrossInclude pins the escape decision for a @for whose
// body inlines a static @include: because stmtInclude splices the partial's
// statements into the caller's render, a partial that reads the caller's loop
// (directly by name, through a with-map expression at the include site, or
// through loop.parent from the partial's own nested loop) makes that loop
// escape, so the analyzer must descend the include boundary and materialize the
// enclosing loop. A partial that reads no caller loop leaves the loop inline,
// and a partial the lowering would not inline (a slot-using or composing partial,
// or a self-include cycle) is walked conservatively, never under-materialized.
// The want slice lists the CALLER loops in walk order (the partial's own loops
// are analyzed as their own nodes, so only the entry template's loops are
// pinned here via len(mod.Children) walk order).
func TestLoopEscapeAnalysisAcrossInclude(t *testing.T) {
	cases := []struct {
		name     string
		entry    string
		partials map[string]string
		// wantCallerInline is the inline decision of the FIRST @for the walk
		// records (the caller loop in the entry template).
		wantCallerInline bool
		// wantInlineReadOfF, when non-empty, asserts the entry template records
		// exactly one approved inline loop-field read of that field, proving a
		// with-map loop read at the include site lowers to inline arithmetic
		// rather than a value read of the elided loop binding.
		wantInlineReadOfF string
	}{
		{
			name:  "partial_reads_caller_loop_index",
			entry: "@for r in rows {\n@include \"p.ql\"\n@}",
			partials: map[string]string{
				"p.ql": "i={{ loop.index }}\n",
			},
			wantCallerInline: false,
		},
		{
			name:  "partial_reads_caller_loop_field",
			entry: "@for r in rows {\n@include \"p.ql\"\n@}",
			partials: map[string]string{
				"p.ql": "f={{ loop.first }}\n",
			},
			wantCallerInline: false,
		},
		{
			// The with-map loop.index is read in the CALLER scope at the include
			// site, where the inline loop counter is live, so it lowers to inline
			// arithmetic and the loop stays inline. The RED here was not a missing
			// materialization but a missing analysis visit: before the fix the
			// analyzer never walked the with-map, so the read was not recorded as
			// approved-inline and the lowering fell back to a value read of the
			// elided (null) loop. Descending records it, keeping the loop inline.
			name:  "only_with_map_reads_caller_loop",
			entry: "@for r in rows {\n@include \"p.ql\" with { pos: loop.index } only\n@}",
			partials: map[string]string{
				"p.ql": "pos={{ pos }}\n",
			},
			wantCallerInline:  true,
			wantInlineReadOfF: "index",
		},
		{
			name:  "partial_nested_loop_reads_caller_parent",
			entry: "@for r in rows {\n@include \"p.ql\"\n@}",
			partials: map[string]string{
				"p.ql": "@for c in cols {\n{{ loop.parent.index }}\n@}\n",
			},
			wantCallerInline: false,
		},
		{
			name:  "partial_reads_nothing_leaves_caller_inline",
			entry: "@for r in rows {\n@include \"p.ql\"\n@}",
			partials: map[string]string{
				"p.ql": "just text {{ r }}\n",
			},
			wantCallerInline: true,
		},
		{
			name:  "partial_reads_own_loop_only_leaves_caller_inline",
			entry: "@for r in rows {\n{{ loop.index }}\n@include \"p.ql\"\n@}",
			partials: map[string]string{
				"p.ql": "@for c in cols {\n{{ loop.index }}\n@}\n",
			},
			wantCallerInline: true,
		},
		{
			name:  "slot_using_partial_walked_without_underescape",
			entry: "@for r in rows {\n@include \"p.ql\"\n@}",
			partials: map[string]string{
				"p.ql": "@yield s\n@provide s {\nx\n@}\n",
			},
			wantCallerInline: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod, err := parse.Parse(source.New("main.ql", tc.entry+"\n"))
			if err != nil {
				t.Fatalf("parse entry: %v", err)
			}
			includes := map[string]*ast.Node{}
			for name, body := range tc.partials {
				pmod, perr := parse.Parse(source.New(name, body))
				if perr != nil {
					t.Fatalf("parse %s: %v", name, perr)
				}
				includes[name] = pmod
			}
			an := analyzeLoops(mod, includes)
			if len(an.fors) == 0 {
				t.Fatal("no @for analyzed in the entry template")
			}
			caller := an.fors[0]
			if got := an.inlineFor(caller); got != tc.wantCallerInline {
				t.Errorf("caller loop inline = %v, want %v", got, tc.wantCallerInline)
			}
			if tc.wantInlineReadOfF != "" {
				var fields []string
				for _, ir := range an.inlineReads {
					fields = append(fields, ir.field)
				}
				if len(fields) != 1 || fields[0] != tc.wantInlineReadOfF {
					t.Errorf("inline reads = %v, want exactly [%q]", fields, tc.wantInlineReadOfF)
				}
			}
		})
	}
}

// TestLoopMutationSafetyAnalysis pins the live-iteration decision (true = the
// zero-copy path, false = the pair-snapshot path) for each @for in source walk
// order, across every trigger of liveFor's rule: member assignment in any
// form and on any name, @do, method calls on object receivers (including
// inside arrows, capture bodies, arguments, and nested constructs), the fused
// filter, the C2 escape triggers, and the transitivity over nested loops.
func TestLoopMutationSafetyAnalysis(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []bool // per @for in walk order: true means live iteration
	}{
		// Read-only bodies stay live; the runtime KArray check covers the
		// iterand's kind, so even a statically non-array iterand is live here.
		{"read_only_list",
			"@for x in xs {\n{{ loop.index }}{{ x }}\n@}", []bool{true}},
		{"two_target_read_only",
			"@for k, v in m {\n{{ k }}{{ v }}{{ loop.last }}\n@}", []bool{true}},
		{"prev_next_read_only",
			"@for x in xs {\n{{ loop.prev ?? 0 }}{{ loop.next ?? 0 }}\n@}", []bool{true}},
		{"non_array_iterand_runtime_checked",
			"@for x in 5 {\n{{ x }}\n@}", []bool{true}},
		{"plain_rebind_is_not_mutation",
			"@for x in xs {\n@set xs = [9]\n{{ x }}\n@}", []bool{true}},
		{"filter_call_is_not_mutation",
			"@for x in xs {\n{{ x | upper }}\n@}", []bool{true}},
		{"function_call_is_not_mutation",
			"@for x in xs {\n{{ dump() }}\n@}", []bool{true}},

		// Member assignment of ANY form on ANY name: aliasing cannot be ruled
		// out statically, so the iterand's name is irrelevant.
		{"member_assign_iterand_name",
			"@for x in xs {\n@set xs[0] = 9\n@}", []bool{false}},
		{"member_assign_unrelated_name",
			"@for x in xs {\n@set ys[0] = 9\n@}", []bool{false}},
		{"member_assign_dotted",
			"@for x in xs {\n@set c.value = x\n@}", []bool{false}},
		{"member_assign_in_if_arm",
			"@for x in xs {\n@if x {\n@set m.k = x\n@}\n@}", []bool{false}},
		{"member_assign_in_nested_with",
			"@for x in xs {\n@with {a: 1} {\n@set m.k = x\n@}\n@}", []bool{false}},

		// attribute() is the function spelling of a receiver-method call (it
		// dispatches Object.CallMethod), so a bare-name attribute call forces
		// the pairs path wherever it appears, including inside a
		// loop.changed(...) argument.
		{"attribute_call_forces_pairs",
			"@for x in xs {\n{{ attribute(c, \"push\", [x]) }}\n@}", []bool{false}},
		{"attribute_in_changed_argument",
			"@for x in xs {\n@if loop.changed(attribute(c, \"key\", [x])) {\nc\n@}\n@}", []bool{false}},

		// @do and method calls on object receivers.
		{"do_statement",
			"@for x in xs {\n@do x\n@}", []bool{false}},
		{"do_in_capture_body",
			"@for x in xs {\n@set y = capture {\n@do x\n@}\n{{ y }}\n@}", []bool{false}},
		{"method_call_printed",
			"@for x in xs {\n{{ c.push(x) }}\n@}", []bool{false}},
		{"method_call_in_argument",
			"@for x in xs {\n{{ [1] | join(c.sep()) }}\n@}", []bool{false}},
		{"method_call_in_arrow_body",
			"@for x in xs {\n@set f = (n) => c.push(n)\n@}", []bool{false}},
		{"attr_read_in_arrow_is_not_mutation",
			"@for x in xs {\n@set f = () => c.value\n{{ x }}\n@}", []bool{true}},

		// loop.changed(...) is loop bookkeeping, not a receiver call; only its
		// argument expressions stay in scope for the scan.
		{"loop_changed_stays_live",
			"@for x in xs {\n@if loop.changed(x) {\nc\n@}\n@}", []bool{true}},
		{"loop_changed_mutating_argument",
			"@for x in xs {\n@if loop.changed(c.key(x)) {\nc\n@}\n@}", []bool{false}},

		// The fused filter and the C2 escape triggers force the pairs path.
		{"fused_filter",
			"@for x in xs if x > 1 {\n{{ x }}\n@}", []bool{false}},
		{"escaping_loop",
			"@for x in xs {\n@set snap = loop\n@}", []bool{false}},

		// Nested loops: a mutator anywhere under the outer body forces the
		// OUTER to pairs too (it runs between outer iterations), and the inner
		// scans its own body only.
		{"nested_read_only_both_live",
			"@for a in xs {\n@for b in ys {\n{{ loop.parent.index }}{{ loop.index }}\n@}\n@}", []bool{true, true}},
		{"inner_mutation_forces_both",
			"@for a in xs {\n@for b in ys {\n@set m[b] = a\n@}\n@}", []bool{false, false}},
		{"inner_do_forces_both",
			"@for a in xs {\n@for b in ys {\n@do b\n@}\n@}", []bool{false, false}},
		{"outer_mutation_inner_clean",
			"@for a in xs {\n@set m.k = a\n@for b in ys {\n{{ b }}\n@}\n@}", []bool{false, true}},
		{"inner_else_mutation_outer_only",
			"@for a in xs {\n@for b in ys {\n{{ b }}\n@} else {\n@do a\n@}\n@}", []bool{false, true}},
		{"inner_iterand_method_outer_only",
			"@for a in xs {\n@for b in c.items() {\n{{ b }}\n@}\n@}", []bool{false, true}},
		{"fused_inner_inside_live_outer",
			"@for a in xs {\n@for b in ys if b > 3 {\n{{ b }}\n@}\n@}", []bool{true, false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod, err := parse.Parse(source.New(tc.name+".ql", tc.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			an := analyzeLoops(mod, nil)
			if len(an.fors) != len(tc.want) {
				t.Fatalf("analyzed %d loops, want %d", len(an.fors), len(tc.want))
			}
			for i, n := range an.fors {
				got := an.liveFor(n)
				if got != tc.want[i] {
					t.Errorf("loop %d (line %d): live = %v, want %v", i, n.Line, got, tc.want[i])
				}
			}
		})
	}
}

// TestInlineLoopParentProbeHoisted pins the parent-probe timing of an inline
// loop against the interpreter's execFor, which resolves pre.Get("loop") once
// before iterating: the probe must be emitted exactly once, at loop entry,
// with every on-demand materialization (here two dump() sites) consuming the
// hoisted local. A consumption-time re-probe -- one per materialization site
// -- could observe a scope entry named loop that changed mid-loop and hand
// out a parent the interpreter never bound.
func TestInlineLoopParentProbeHoisted(t *testing.T) {
	src := "@for x in xs {\n{{ dump() }}{{ dump() }}\n@}"
	mod, err := parse.Parse(source.New("probe.ql", src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := Module("probe.ql", mod, Options{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	gen := string(res.Source)
	probe := `vars["loop"]`
	if got := strings.Count(gen, probe); got != 1 {
		t.Fatalf("emitted %d parent probes, want exactly 1 hoisted at loop entry", got)
	}
	if strings.Index(gen, probe) > strings.Index(gen, "for qi") {
		t.Error("parent probe emitted after the iteration statement, want at loop entry")
	}
}

// TestIncludeLoopChangedFloorCutsCallerLoop is the white-box guard on the
// loop.changed scope cut: a partial that calls loop.changed but opens no @for of
// its own, inlined into a caller @for, must lower to the "only available inside a
// for loop" error path anchored to the PARTIAL's source (qSrc2), never to the
// caller loop's changed-tracking. changedFloor rises to the caller loop depth
// while stmtInclude inlines the body, so currentChangedLoop returns nil at the
// call site; without the floor the generated code would silently track the caller
// loop and render clean bytes where the interpreter errors.
func TestIncludeLoopChangedFloorCutsCallerLoop(t *testing.T) {
	tmpls := map[string]string{
		"main.ql": "@for r in rows {\n@include \"p.ql\"\n@}\n",
		"p.ql":    "@if loop.changed(r) {\nnew:{{ r }}\n@}\n",
	}
	mods := map[string]*ast.Node{}
	for name, body := range tmpls {
		mod, err := parse.Parse(source.New(name, body))
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		mods[name] = mod
	}
	res, err := Unit("main.ql", mods, Options{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	gen := string(res.Source)
	// The error must fire, anchored to the partial (qSrc2 is p.ql), never the
	// entry source qSrc.
	wantErr := `qerrors.New(qerrors.KindRuntime, "loop.changed is only available inside a for loop"), qSrc2, 1`
	if !strings.Contains(gen, wantErr) {
		t.Errorf("generated code does not emit the loop.changed error at the partial source; want a substring %q", wantErr)
	}
	// No changed-tracking memory locals may be minted for this call site: the
	// interpreter never reaches the tracking branch, so the compiler must not
	// either. runtime.Equal is the tell of the changed-tracking lowering.
	if strings.Contains(gen, "runtime.Equal") {
		t.Error("generated code tracks loop.changed across the include boundary; the caller loop's changed memory must be cut")
	}
}
