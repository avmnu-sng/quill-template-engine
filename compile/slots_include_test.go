package compile_test

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/compile"
)

// slotsIncludeBattery pins the cross-template slot-sharing contracts: a static
// @include of a slot-using partial whose statements inline into the SAME
// generated render, so the partial's @provide appends to the render-level slot
// buffers and its @yield reaches the single post-render resolve pass -- the
// compiled analog of interp shareSlotsFrom, where one Render means one slot map.
//
// The shapes are the two headline fixtures plus the plan's adversarial corners:
// a self-contained partial that both @yields and @provides its own slot (the
// partial's own yield placeholder must resolve even though the caller has no
// slot construct); sibling includes that each @provide into the caller's @yield
// with execution-order accumulation across the include boundary; the reverse
// direction, where the caller @provides a label the included partial @yields; an
// ignore-missing include whose absent partial contributes nothing to any slot; a
// partial that @yields its own label AND @provides into a label the caller also
// @yields (interleaved accumulation from both sides); a @provide inside a @for
// feeding a caller @yield through the include boundary; and escape-strategy
// inheritance, where a provide body escapes once under the caller's strategy and
// the resolved slot is not escaped a second time. Every case runs strict through
// both the Unit and single-template Module lowerings, and the whole battery
// asserts no raw NUL-wrapped yield placeholder survives in any compiled output.
var slotsIncludeBattery = []includeCase{
	{
		name:  "self_contained_partial_yield",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql": "page top.\n@include \"note.ql\"\npage bottom.\n",
			"note.ql": "note top:\n@yield note\n@provide note {\ncollected line\n@}\nnote bottom.\n",
		},
		want: "page top.\nnote top:\ncollected line\nnote bottom.\npage bottom.\n",
	},
	{
		name:  "include_into_shell_yield",
		entry: "shell.ql",
		tmpls: map[string]string{
			"shell.ql":  "imports:\n@yield imports\nbody:\n@include \"part-a.ql\"\n@include \"part-b.ql\"\n",
			"part-a.ql": "@provide imports {\nimport \"os\"\n@}\nA rendered.\n",
			"part-b.ql": "@provide imports {\nimport \"fmt\"\n@}\nB rendered.\n",
		},
		want: "imports:\nimport \"os\"\nimport \"fmt\"\nbody:\nA rendered.\nB rendered.\n",
	},
	{
		name:  "caller_provides_partial_yields",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@provide msg {\nfrom caller\n@}\n@include \"p.ql\"\n",
			"p.ql":    "note:\n@yield msg\n",
		},
		want: "note:\nfrom caller\n",
	},
	{
		name:  "ignore_missing_contributes_nothing",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@yield s\n@include \"gone.ql\" ignore missing\n@provide s {\nkept\n@}\n",
		},
		want: "kept\n",
	},
	{
		name:  "partial_yields_own_and_provides_caller",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@yield head\n@include \"body.ql\"\n@provide head {\nHEAD\n@}\n",
			"body.ql": "@yield body\n@provide body {\nBODY\n@}\n@provide head {\nHEAD2\n@}\n",
		},
		want: "HEAD2\nHEAD\nBODY\n",
	},
	{
		name:  "provide_in_for_across_include",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@yield rows\n@for r in items {\n@include \"row.ql\" with { v: r }\n@}\n",
			"row.ql":  "@provide rows {\nrow {{ v }}\n@}\n",
		},
		vars: `{"items":[1,2,3]}`,
		want: "row 1\nrow 2\nrow 3\n",
	},
	{
		name:  "escape_slot_across_include",
		entry: "main.ql",
		tmpls: map[string]string{
			"main.ql": "@yield h\n@include \"p.ql\"\n",
			"p.ql":    "@provide h {\n{{ raw }}\n@}\n",
		},
		vars: `{"raw":"a<b&c"}`,
		want: "a&lt;b&amp;c\n",
		auto: true,
	},
}

// TestSlotsIncludeBattery renders the cross-template slot-sharing battery through
// both compiled lowerings (Unit and single-template Module), asserting each
// output byte-equal to the facade's Render AND to the pinned contract, the
// dispatch gate served the compiled unit under the fixture's configuration, and
// that no raw yield placeholder survives -- the leak class a slot reached only
// through an include would open if the render failed to buffer and resolve.
func TestSlotsIncludeBattery(t *testing.T) {
	var cases []compiledCase
	results := map[string]*compile.Result{}
	for _, ic := range slotsIncludeBattery {
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
		base := slotsIncludeCaseFor(cs.name)
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
		if strings.Contains(r.out, "\x00\x01QUILL_SLOT_") {
			t.Errorf("%s: a raw yield placeholder leaked into compiled output: %q", cs.name, r.out)
		}
		if envR, ok := got[cs.name+"@env"]; ok {
			if envR.failed {
				t.Errorf("%s: env-dispatch render errored: %s", cs.name, envR.errText)
			} else if envR.out != want {
				t.Errorf("%s: env-dispatch output differs from interpreter\n got  %q\n want %q", cs.name, envR.out, want)
			}
		}
		if tr, ok := got[cs.name+"@tracer"]; ok && tr.out != "served" {
			t.Errorf("%s: dispatch gate fell back for a slots-include unit it should serve", cs.name)
		}
		if mx, ok := got[cs.name+"@matrix"]; ok && mx.out != "ok" {
			t.Errorf("%s: fingerprint-matrix leg reported %q", cs.name, mx.out)
		}
		if rt, ok := got[cs.name+"@renderto-ok"]; ok && rt.out != "ok" {
			if rt.failed {
				t.Errorf("%s: success RenderTo leg reported %q", cs.name, rt.errText)
			} else {
				t.Errorf("%s: success RenderTo leg reported %q", cs.name, rt.out)
			}
		}
	}
}

// slotsIncludeCaseFor returns the slotsIncludeBattery entry a suffixed case name
// derives from.
func slotsIncludeCaseFor(caseName string) includeCase {
	base := caseName
	for _, suffix := range []string{"-unit", "-module"} {
		if len(base) > len(suffix) && base[len(base)-len(suffix):] == suffix {
			base = base[:len(base)-len(suffix)]
			break
		}
	}
	for _, e := range slotsIncludeBattery {
		if e.name == base {
			return e
		}
	}
	return includeCase{}
}
