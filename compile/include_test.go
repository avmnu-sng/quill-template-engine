package compile_test

import (
	"errors"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/compile"
)

// includeCase is one static-@include parity contract: the entry template, its
// sibling partials, the entry name, the pinned expected output, and the
// autoescape/lenient knobs. The compiled render (through both the Unit and the
// single-template Module lowerings) must equal the facade byte for byte, and the
// facade must equal the pinned contract, so a drift on either engine fails.
type includeCase struct {
	name    string
	entry   string
	tmpls   map[string]string
	vars    string
	want    string
	auto    bool
	lenient bool
}

// includeBattery pins the compiled static-@include contracts: a plain include
// inheriting the caller scope (cross-frame reads share-marked), a with-map
// binding, an only cut to a fresh scope root, ignore missing rendering nothing,
// the privatized-array copy-on-write isolation across the include boundary, the
// four late-alias adversarial shapes from the falsification study (privatize a,
// include under each scope form, then create an alias and mutate), escape-strategy
// inheritance into the partial body, and a @tab region re-indenting the spliced
// block. It also pins the loop-escape corner an inline-optimized @for opens once
// its body inlines a partial: a partial that reads the CALLER's loop (directly,
// through a with-map expression at the include site, through loop.parent from a
// nested loop, or through the loop's metadata fields) forces the enclosing loop
// to materialize a live loop value the partial can share cross-frame, so the
// escape analyzer must descend the include boundary. Deeper cross-boundary shapes
// pin the fixpoint's reach and the loop.changed scope cut: a nested @include chain
// whose innermost partial reads the outer loop, a loop.parent.parent read two
// include boundaries deep, and loop.changed sites that must land in the include
// child scope -- a partial's own @for tracks changes normally (its changed memory
// resets each caller iteration because the partial re-renders fresh), while a
// caller @for keeps its own changed memory untouched by an include after it. Every
// case runs strict and, where marked, lenient.
var includeBattery = []includeCase{
	{
		name:  "plain_inherits_scope",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@set label = \"w\"\n@set value = 80\n@include \"row.ql\"\ndone\n",
			"row.ql":  "row: {{ label }} = {{ value }}\n",
		},
		want: "row: w = 80\ndone\n",
	},
	{
		name:  "with_map_overrides",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@set label = \"outer\"\n@include \"row.ql\" with { label: \"inner\", value: 7 }\nlabel stays {{ label }}\n",
			"row.ql":  "row: {{ label }} = {{ value }}\n",
		},
		want: "row: inner = 7\nlabel stays outer\n",
	},
	{
		name:  "only_cuts_scope",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@set label = \"outer\"\n@include \"row.ql\" with { value: 9 } only\n",
			"row.ql":  "row: [{{ label ?? \"none\" }}] = {{ value }}\n",
		},
		want:    "row: [none] = 9\n",
		lenient: true,
	},
	{
		name:  "ignore_missing_renders_nothing",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "before\n@include \"gone.ql\" ignore missing\nafter\n",
		},
		want: "before\nafter\n",
	},
	{
		name:  "for_loop_with_map",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "table:\n@for r in rows {\n@include \"row.ql\" with { label: r.label, value: r.value }\n@}\n@include \"gone.ql\" ignore missing\ndone\n",
			"row.ql":  "  row: {{ label }} = {{ value }}\n",
		},
		vars: `{"rows":[{"label":"width","value":80},{"label":"height","value":24}]}`,
		want: "table:\n  row: width = 80\n  row: height = 24\ndone\n",
	},
	{
		name:  "cow_privatized_isolates",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@set x = [1, 2]\n@set x[0] = 9\n@include \"part.ql\"\nout: {{ x | json }}\n",
			"part.ql": "@set x[1] = 77\nin: {{ x | json }}\n",
		},
		want: "in: [9,77]\nout: [9,2]\n",
	},
	{
		name:  "late_alias_unread",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@set a = [1, 2]\n@set a[0] = 9\n@include \"part.ql\"\n@set c = a\n@set a[0] = 42\nc: {{ c | json }}\na: {{ a | json }}\n",
			"part.ql": "noop\n",
		},
		want: "noop\nc: [9,2]\na: [42,2]\n",
	},
	{
		name:  "late_alias_read",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@set a = [1, 2]\n@set a[0] = 9\n@include \"part.ql\"\n@set c = a\n@set a[0] = 42\nc: {{ c | json }}\na: {{ a | json }}\n",
			"part.ql": "seen: {{ a | json }}\n",
		},
		want: "seen: [9,2]\nc: [9,2]\na: [42,2]\n",
	},
	{
		name:  "late_alias_with_passed",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@set a = [1, 2]\n@set a[0] = 9\n@include \"part.ql\" with { a: a }\n@set c = a\n@set a[0] = 42\nc: {{ c | json }}\na: {{ a | json }}\n",
			"part.ql": "@set a[1] = 5\nin: {{ a | json }}\n",
		},
		want: "in: [9,5]\nc: [9,2]\na: [42,2]\n",
	},
	{
		name:  "late_alias_only",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@set a = [1, 2]\n@set a[0] = 9\n@include \"part.ql\" with { a: a } only\n@set c = a\n@set a[0] = 42\nc: {{ c | json }}\na: {{ a | json }}\n",
			"part.ql": "@set a[1] = 5\nin: {{ a | json }}\n",
		},
		want: "in: [9,5]\nc: [9,2]\na: [42,2]\n",
	},
	{
		name:  "escape_inherits_into_partial",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@include \"row.ql\"\n",
			"row.ql":  "val: {{ raw }}\n",
		},
		vars: `{"raw":"a<b&c"}`,
		want: "val: a&lt;b&amp;c\n",
		auto: true,
	},
	{
		name:  "escape_region_around_include",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@escape html {\n@include \"row.ql\"\n@}\n",
			"row.ql":  "val: {{ raw }}\n",
		},
		vars: `{"raw":"a<b"}`,
		want: "val: a&lt;b\n",
	},
	{
		name:  "tab_region_reindents_block",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "head\n@tab(1) {\n@include \"row.ql\"\n@}\ntail\n",
			"row.ql":  "line one\nline two\n\nline four\n",
		},
		want: "head\n    line one\n    line two\n\n    line four\ntail\n",
	},
	{
		name:  "member_write_in_partial_maps",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@set m = { a: 1 }\n@set m.a = 2\n@include \"part.ql\"\nout: {{ m | json }}\n",
			"part.ql": "@set m.b = 9\nin: {{ m | json }}\n",
		},
		want: "in: {\"a\":2,\"b\":9}\nout: {\"a\":2}\n",
	},
	{
		name:  "partial_has_own_loop",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@include \"list.ql\" with { items: nums }\n",
			"list.ql": "@for n in items {\n{{ loop.index }}:{{ n }}\n@}\n",
		},
		vars: `{"nums":[10,20,30]}`,
		want: "1:10\n2:20\n3:30\n",
	},
	{
		name:  "slots_caller_includes_slot_free_partial",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@yield head\n@include \"row.ql\" with { v: 7 }\n@provide head {\nHEADER\n@}\n",
			"row.ql":  "row v={{ v }}\n",
		},
		want: "HEADER\nrow v=7\n",
	},
	{
		name:  "for_include_reads_caller_loop",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@include \"p.ql\"\n@}\n",
			"p.ql":    "r={{ r }} i={{ loop.index }}\n",
		},
		vars: `{"rows":["a","b","c"]}`,
		want: "r=a i=1\nr=b i=2\nr=c i=3\n",
	},
	{
		name:  "for_include_only_with_reads_caller_loop",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@include \"p.ql\" with { pos: loop.index } only\n@}\n",
			"p.ql":    "pos={{ pos }}\n",
		},
		vars: `{"rows":["a","b"]}`,
		want: "pos=1\npos=2\n",
	},
	{
		name:  "for_include_reads_caller_loop_fields",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@include \"p.ql\"\n@}\n",
			"p.ql":    "r={{ r }} first={{ loop.first }} last={{ loop.last }}\n",
		},
		vars: `{"rows":["a","b","c"]}`,
		want: "r=a first=true last=false\nr=b first=false last=false\nr=c first=false last=true\n",
	},
	{
		name:  "for_include_partial_nested_loop_reads_parent",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@include \"p.ql\"\n@}\n",
			"p.ql":    "@for c in cols {\nr={{ loop.parent.index }} c={{ loop.index }}\n@}\n",
		},
		vars: `{"rows":["a","b"],"cols":["x","y"]}`,
		want: "r=1 c=1\nr=1 c=2\nr=2 c=1\nr=2 c=2\n",
	},
	{
		name:  "for_include_partial_has_own_and_reads_caller_loop",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@include \"p.ql\"\n@}\n",
			"p.ql":    "outer={{ loop.index }}\n@for c in cols {\ninner={{ loop.index }}\n@}\n",
		},
		vars: `{"rows":["a","b"],"cols":["x"]}`,
		want: "outer=1\ninner=1\nouter=2\ninner=1\n",
	},
	{
		name:  "nested_caller_loops_partial_reads_both",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for a in xs {\n@for b in ys {\n@include \"p.ql\"\n@}\n@}\n",
			"p.ql":    "b={{ loop.index }} a={{ loop.parent.index }}\n",
		},
		vars: `{"xs":["p","q"],"ys":["m","n"]}`,
		want: "b=1 a=1\nb=2 a=1\nb=1 a=2\nb=2 a=2\n",
	},
	{
		name:  "partial_own_loop_reads_parent_of_caller",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for a in xs {\n@include \"p.ql\"\n@}\n",
			"p.ql":    "@for b in ys {\ncaller={{ loop.parent.index }} b={{ loop.index }}\n@}\n",
		},
		vars: `{"xs":["p","q"],"ys":["m"]}`,
		want: "caller=1 b=1\ncaller=2 b=1\n",
	},
	{
		name:  "nested_include_chain_in_loop_reads_loop",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@include \"p.ql\"\n@}\n",
			"p.ql":    "p:{{ loop.index }}\n@include \"q.ql\"\n",
			"q.ql":    "q:{{ loop.index }} r={{ r }}\n",
		},
		vars: `{"rows":["a","b","c"]}`,
		want: "p:1\nq:1 r=a\np:2\nq:2 r=b\np:3\nq:3 r=c\n",
	},
	{
		name:  "nested_include_chain_in_loop_reads_loop_lenient",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@include \"p.ql\"\n@}\n",
			"p.ql":    "p:{{ loop.index }}\n@include \"q.ql\"\n",
			"q.ql":    "q:{{ loop.index }} r={{ r }}\n",
		},
		vars:    `{"rows":["a","b","c"]}`,
		want:    "p:1\nq:1 r=a\np:2\nq:2 r=b\np:3\nq:3 r=c\n",
		lenient: true,
	},
	{
		name:  "two_deep_parent_across_two_include_boundaries",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for a in xs {\n@for b in ys {\n@include \"p.ql\"\n@}\n@}\n",
			"p.ql":    "@for c in zs {\ndeep={{ loop.parent.parent.index }} mid={{ loop.parent.index }} c={{ loop.index }}\n@}\n",
		},
		vars: `{"xs":["p","q"],"ys":["m","n"],"zs":["u"]}`,
		want: "deep=1 mid=1 c=1\ndeep=1 mid=2 c=1\ndeep=2 mid=1 c=1\ndeep=2 mid=2 c=1\n",
	},
	{
		name:  "two_deep_parent_across_two_include_boundaries_lenient",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for a in xs {\n@for b in ys {\n@include \"p.ql\"\n@}\n@}\n",
			"p.ql":    "@for c in zs {\ndeep={{ loop.parent.parent.index }} mid={{ loop.parent.index }} c={{ loop.index }}\n@}\n",
		},
		vars:    `{"xs":["p","q"],"ys":["m","n"],"zs":["u"]}`,
		want:    "deep=1 mid=1 c=1\ndeep=1 mid=2 c=1\ndeep=2 mid=1 c=1\ndeep=2 mid=2 c=1\n",
		lenient: true,
	},
	{
		name:  "loop_changed_in_partial_own_for",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@include \"p.ql\"\n@}\n",
			"p.ql":    "@for c in cols {\n@if loop.changed(c) {\nnew-c:{{ c }}\n@}\n@}\n",
		},
		vars: `{"rows":["a","b"],"cols":["x","x","y"]}`,
		want: "new-c:x\nnew-c:y\nnew-c:x\nnew-c:y\n",
	},
	{
		name:  "caller_loop_changed_then_include",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@if loop.changed(r) {\ncaller-new:{{ r }}\n@}\n@include \"p.ql\"\n@}\n",
			"p.ql":    "  row:{{ r }} i={{ loop.index }}\n",
		},
		vars: `{"rows":["a","a","b"]}`,
		want: "caller-new:a\n  row:a i=1\n  row:a i=2\ncaller-new:b\n  row:b i=3\n",
	},
	{
		name:  "nested_include_deeper_own_for_changed_reads_parent",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@include \"p.ql\"\n@}\n",
			"p.ql":    "p:{{ loop.index }}\n@include \"q.ql\"\n",
			"q.ql":    "@for c in cols {\n@if loop.changed(c) {\nqnew:{{ c }} outer={{ loop.parent.index }}\n@}\n@}\n",
		},
		vars: `{"rows":["a","b"],"cols":["x","x","y"]}`,
		want: "p:1\nqnew:x outer=1\nqnew:y outer=1\np:2\nqnew:x outer=2\nqnew:y outer=2\n",
	},
}

// TestIncludeBattery renders the static-@include battery through both compiled
// lowerings (Unit and single-template Module), asserting each output byte-equal
// to the facade's Render AND to the pinned contract, with the dispatch gate
// proven to serve the compiled unit under the fixture's own configuration.
func TestIncludeBattery(t *testing.T) {
	var cases []compiledCase
	results := map[string]*compile.Result{}
	for _, ic := range includeBattery {
		for _, viaModule := range []bool{false, true} {
			suffix := "-unit"
			if viaModule {
				suffix = "-module"
			}
			cs := compiledCase{
				name:      ic.name + suffix,
				templates: ic.tmpls,
				entry:     ic.entry,
				varsJSON:  ic.vars,
				opts:      compile.Options{AutoescapeHTML: ic.auto, LenientVariables: ic.lenient},
				envCheck:  true,
				viaModule: viaModule,
			}
			res, err := compileCase(t, cs)
			if err != nil {
				t.Fatalf("%s: compile: %v", cs.name, err)
			}
			results[cs.name] = res
			cases = append(cases, cs)
		}
	}
	got := runCompiled(t, cases, results)
	for _, cs := range cases {
		base := includeCaseFor(cs.name)
		r, ok := got[cs.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", cs.name)
			continue
		}
		if r.failed {
			t.Errorf("%s: compiled render errored: %s", cs.name, r.errText)
			continue
		}
		want, err := renderInterp(t, cs)
		if err != nil {
			t.Errorf("%s: interp render errored: %v", cs.name, err)
			continue
		}
		if want != base.want {
			t.Errorf("%s: interpreter drifted from the pinned contract\n got  %q\n want %q", cs.name, want, base.want)
		}
		if r.out != want {
			t.Errorf("%s: compiled output differs from interpreter\n got  %q\n want %q", cs.name, r.out, want)
		}
		if envR, ok := got[cs.name+"@env"]; ok {
			if envR.failed {
				t.Errorf("%s: env-dispatch render errored: %s", cs.name, envR.errText)
			} else if envR.out != want {
				t.Errorf("%s: env-dispatch output differs from interpreter\n got  %q\n want %q", cs.name, envR.out, want)
			}
		}
		if tr, ok := got[cs.name+"@tracer"]; ok && tr.out != "served" {
			t.Errorf("%s: dispatch gate fell back for an include unit it should serve", cs.name)
		}
		if mx, ok := got[cs.name+"@matrix"]; ok && mx.out != "ok" {
			t.Errorf("%s: fingerprint-matrix leg reported %q", cs.name, mx.out)
		}
	}
}

// includeCaseFor returns the includeBattery entry a suffixed case name derives from.
func includeCaseFor(caseName string) includeCase {
	base := caseName
	for _, suffix := range []string{"-unit", "-module"} {
		if len(base) > len(suffix) && base[len(base)-len(suffix):] == suffix {
			base = base[:len(base)-len(suffix)]
			break
		}
	}
	for _, e := range includeBattery {
		if e.name == base {
			return e
		}
	}
	return includeCase{}
}

// includeErrorCase is one static-@include error-parity contract: an entry that
// inlines a partial whose loop.changed call has no @for of its own to bind to.
// The interpreter renders the partial through a fresh sub-interpreter with an
// empty loop.changed memory stack, so the call raises "only available inside a
// for loop" even though the include sits inside a caller @for; the compiled
// inline must reproduce that error text and position rather than silently
// tracking the caller loop's changes.
type includeErrorCase struct {
	name    string
	entry   string
	tmpls   map[string]string
	vars    string
	wantErr string
}

// includeLoopChangedErrorBattery pins the loop.changed scope cut across the
// include boundary: a partial that calls loop.changed but opens no @for of its
// own, spliced directly into a caller @for (shape b), the same reading a loop
// metadata field in the changed guard (shape b2), and a partial reached through a
// nested @include chain, each inside a caller @for. Every case errors on both
// engines; the compiled inline must cite the partial's own template and line.
var includeLoopChangedErrorBattery = []includeErrorCase{
	{
		name:  "changed_in_partial_no_own_for",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@include \"p.ql\"\n@}\n",
			"p.ql":    "@if loop.changed(r) {\nnew:{{ r }}\n@}\nrow:{{ r }}\n",
		},
		vars:    `{"rows":["a","a","b"]}`,
		wantErr: "quill runtime error: loop.changed is only available inside a for loop (p.ql:1)",
	},
	{
		name:  "changed_field_in_partial_no_own_for",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@include \"p.ql\"\n@}\n",
			"p.ql":    "@if loop.changed(r) {\nfirst i={{ loop.index }}\n@}\n",
		},
		vars:    `{"rows":["a","a","b"]}`,
		wantErr: "quill runtime error: loop.changed is only available inside a for loop (p.ql:1)",
	},
	{
		name:  "changed_in_nested_include_no_own_for",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@for r in rows {\n@include \"p.ql\"\n@}\n",
			"p.ql":    "p:{{ loop.index }}\n@include \"q.ql\"\n",
			"q.ql":    "@if loop.changed(r) {\nq-new\n@}\n",
		},
		vars:    `{"rows":["a","b"]}`,
		wantErr: "quill runtime error: loop.changed is only available inside a for loop (q.ql:1)",
	},
}

// TestIncludeLoopChangedErrorParity compiles each loop.changed scope-cut shape
// through both the Unit and Module lowerings under strict and lenient variables,
// renders it, and asserts the compiled render fails with the same error text and
// position the interpreter raises AND that the pinned contract matches interp, so
// a compiler that let loop.changed track the caller loop across the include
// boundary (rendering clean bytes where the interpreter errors) fails loudly.
func TestIncludeLoopChangedErrorParity(t *testing.T) {
	var cases []compiledCase
	results := map[string]*compile.Result{}
	for _, ec := range includeLoopChangedErrorBattery {
		for _, lenient := range []bool{false, true} {
			for _, viaModule := range []bool{false, true} {
				suffix := "-unit"
				if viaModule {
					suffix = "-module"
				}
				if lenient {
					suffix += "-lenient"
				} else {
					suffix += "-strict"
				}
				cs := compiledCase{
					name:      ec.name + suffix,
					templates: ec.tmpls,
					entry:     ec.entry,
					varsJSON:  ec.vars,
					opts:      compile.Options{LenientVariables: lenient},
					viaModule: viaModule,
				}
				res, err := compileCase(t, cs)
				if err != nil {
					t.Fatalf("%s: compile: %v", cs.name, err)
				}
				results[cs.name] = res
				cases = append(cases, cs)
			}
		}
	}
	got := runCompiled(t, cases, results)
	for _, cs := range cases {
		base := includeErrorCaseFor(cs.name)
		r, ok := got[cs.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", cs.name)
			continue
		}
		want, werr := renderInterp(t, cs)
		if werr == nil {
			t.Errorf("%s: interpreter rendered %q, want the loop.changed error", cs.name, want)
			continue
		}
		if werr.Error() != base.wantErr {
			t.Errorf("%s: interpreter error drifted from the pinned contract\n got  %q\n want %q", cs.name, werr.Error(), base.wantErr)
		}
		if !r.failed {
			t.Errorf("%s: compiled rendered %q but interp errored %q", cs.name, r.out, werr.Error())
			continue
		}
		if r.errText != werr.Error() {
			t.Errorf("%s: compiled error differs from interpreter\n got  %q\n want %q", cs.name, r.errText, werr.Error())
		}
	}
}

// includeErrorCaseFor returns the includeLoopChangedErrorBattery entry a suffixed
// case name derives from.
func includeErrorCaseFor(caseName string) includeErrorCase {
	base := caseName
	for _, suffix := range []string{"-unit-strict", "-unit-lenient", "-module-strict", "-module-lenient"} {
		if len(base) > len(suffix) && base[len(base)-len(suffix):] == suffix {
			base = base[:len(base)-len(suffix)]
			break
		}
	}
	for _, e := range includeLoopChangedErrorBattery {
		if e.name == base {
			return e
		}
	}
	return includeErrorCase{}
}

// TestIncludeNotCompilable pins the constructs the static-@include lowering
// refuses, deferring each to the interpreter through the typed subset error: a
// dynamic (non-literal) source, a candidate-list source whose winner is a
// render-time loader decision, a partial that itself composes other templates,
// a self-including partial, and a plain include of a template outside the
// compile set. A partial that merely uses deferred slots is NOT here: it inlines
// through the cross-template slot-sharing path (TestSlotsIncludeBattery).
func TestIncludeNotCompilable(t *testing.T) {
	cases := []struct {
		name      string
		entry     string
		templates map[string]string
		construct string
	}{
		{"dynamic-source", "main.ql", map[string]string{
			"main.ql": "@include name\n", "row.ql": "x\n"}, "@include with a dynamic source"},
		{"candidate-list", "main.ql", map[string]string{
			"main.ql": "@include [\"a.ql\", \"row.ql\"]\n", "a.ql": "A\n", "row.ql": "R\n"}, "@include with a dynamic source"},
		{"partial-composes", "main.ql", map[string]string{
			"main.ql": "@include \"row.ql\"\n", "row.ql": "@extends \"base.ql\"\n@block b {\nx\n@}\n", "base.ql": "@block b {\nd\n@}\n"}, "@include of a partial that composes other templates"},
		{"self-include", "main.ql", map[string]string{
			"main.ql": "@include \"main.ql\"\n"}, "recursive @include (cycle?)"},
		{"plain-outside-set", "main.ql", map[string]string{
			"main.ql": "@include \"gone.ql\"\n"}, "@include of a template outside the compile set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mods := parseUnit(t, tc.templates)
			_, cerr := compile.Unit(tc.entry, mods, compile.Options{})
			if cerr == nil {
				t.Fatalf("expected ErrNotCompilable for %s", tc.construct)
			}
			var nce *compile.NotCompilableError
			if !errors.As(cerr, &nce) {
				t.Fatalf("error is not *NotCompilableError: %v", cerr)
			}
			if !errors.Is(cerr, compile.ErrNotCompilable) {
				t.Fatalf("error does not match ErrNotCompilable sentinel: %v", cerr)
			}
			if nce.Construct != tc.construct {
				t.Fatalf("construct = %q, want %q", nce.Construct, tc.construct)
			}
		})
	}
}

// TestIncludeInBlockBody renders a static @include inlined inside a unit's
// @block body across an @extends chain through the compiled Unit path, proving
// the partial's statements splice into the block frame and the render stays
// byte-exact to the facade.
func TestIncludeInBlockBody(t *testing.T) {
	tmpls := map[string]string{
		"base.ql": "base:\n@block b {\ndefault\n@}\n",
		"page.ql": "@extends \"base.ql\"\n@block b {\n@include \"row.ql\" with { label: \"hi\", value: 3 }\n@}\n",
		"row.ql":  "row: {{ label }} = {{ value }}\n",
	}
	cs := compiledCase{
		name:      "include-in-block",
		templates: tmpls,
		entry:     "page.ql",
		opts:      compile.Options{},
		envCheck:  true,
	}
	res, err := compileCase(t, cs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got := runCompiled(t, []compiledCase{cs}, map[string]*compile.Result{cs.name: res})
	want, rerr := renderInterp(t, cs)
	if rerr != nil {
		t.Fatalf("interp render: %v", rerr)
	}
	const pinned = "base:\nrow: hi = 3\n"
	if want != pinned {
		t.Fatalf("interpreter drifted from the pinned contract\n got  %q\n want %q", want, pinned)
	}
	if r := got[cs.name]; r.failed || r.out != want {
		t.Fatalf("compiled output differs from interpreter\n got  %q (failed=%v)\n want %q", r.out, r.failed, want)
	}
	if tr, ok := got[cs.name+"@tracer"]; ok && tr.out != "served" {
		t.Fatal("dispatch gate fell back for an include-in-block unit it should serve")
	}
}

// TestIncludeSelfIncludeRejectedBeforeCycle pins that a partial including itself
// through a second template (a-includes-b, b-includes-a) is rejected as a cycle
// rather than expanding the inline without bound.
func TestIncludeSelfIncludeRejectedBeforeCycle(t *testing.T) {
	templates := map[string]string{
		"a.ql": "A\n@include \"b.ql\"\n",
		"b.ql": "B\n@include \"a.ql\"\n",
	}
	mods := parseUnit(t, templates)
	_, cerr := compile.Unit("a.ql", mods, compile.Options{})
	if cerr == nil {
		t.Fatal("expected ErrNotCompilable for an include cycle")
	}
	var nce *compile.NotCompilableError
	if !errors.As(cerr, &nce) {
		t.Fatalf("error is not *NotCompilableError: %v", cerr)
	}
	if nce.Construct != "recursive @include (cycle?)" {
		t.Fatalf("construct = %q, want recursive @include (cycle?)", nce.Construct)
	}
}
