package compile_test

import (
	"bytes"
	"errors"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/compile"
	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// parseUnit parses a name->body map into the module map compile.Unit consumes.
func parseUnit(t *testing.T, templates map[string]string) map[string]*ast.Node {
	t.Helper()
	mods := map[string]*ast.Node{}
	for name, body := range templates {
		mod, err := parse.Parse(source.New(name, body))
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		mods[name] = mod
	}
	return mods
}

// unitBattery is the multi-template parity battery: every case compiles
// through compile.Unit, renders in the scratch process, and must match the
// facade byte-for-byte (output or error text). It covers the adversarial
// shapes the flattening can get wrong: binds escaping an overriding block,
// nested blocks, trait aliasing with parent() chains three deep, candidate
// lists, parent() under an active escape strategy, block sites inside loops
// and with-frames, the same body inlined at several sites, loop.changed
// memory shared across inlined sites, @tab state threading through the
// parent() capture, and the interpreter's exact composition error texts.
var unitBattery = []compiledCase{
	// The bench Compose shape: extends + overrides + parent() + a loop.
	{name: "u-compose", entry: "page.ql", varsJSON: `{"title":"T","items":["a","b","c"]}`,
		templates: map[string]string{
			"base.ql": "# {{ title }}\n\n@block summary {\n(no summary)\n@}\n@block items {\n(no items)\n@}\n",
			"page.ql": "@extends \"base.ql\"\n@block summary {\n{{ parent() }}\nA short report with {{ items | length }} items.\n@}\n@block items {\n@for it in items {\n- {{ it }}\n@}\n@}\n",
		}},
	// @set inside an overriding block observed AFTER the site: blocks bind in
	// the shared enclosing scope, so the parent body reads the child's bind.
	{name: "u-set-in-override", entry: "child.ql",
		templates: map[string]string{
			"base.ql":  "@block b {\n@}\nafter={{ x ?? \"-\" }}|{{ y ?? \"-\" }}\n",
			"child.ql": "@extends \"base.ql\"\n@block b {\n@set x = 42\n@set y = [1, 2]\n@}\n",
		}},
	// Nested blocks: the inner definition is a flat table entry a child can
	// override without touching the outer.
	{name: "u-nested-blocks", entry: "child.ql",
		templates: map[string]string{
			"base.ql":  "@block outer {\nO[\n@block inner {\nbase-inner\n@}\n]O\n@}\n",
			"child.ql": "@extends \"base.ql\"\n@block inner {\nchild-inner\n@}\n",
		}},
	// Nested blocks where the child overrides the OUTER with parent(): the
	// parent's outer body re-renders, and its inner site still resolves to
	// the merged table.
	{name: "u-nested-outer-parent", entry: "child.ql",
		templates: map[string]string{
			"base.ql":  "@block outer {\nO[\n@block inner {\nbase-inner\n@}\n]O\n@}\n",
			"child.ql": "@extends \"base.ql\"\n@block outer {\nC<\n{{ parent() }}\n>C\n@}\n@block inner {\nchild-inner\n@}\n",
		}},
	// Trait alias + override + parent() three deep: child overrides b, its
	// parent() reaches base's own b, whose parent() reaches the trait's t
	// aliased to b.
	{name: "u-trait-alias-3deep", entry: "child.ql",
		templates: map[string]string{
			"trait.ql": "@block t {\ntrait-body\n@}\n",
			"base.ql":  "@use \"trait.ql\" with {t: b}\n@block b {\nbase[{{ parent() }}]\n@}\n",
			"child.ql": "@extends \"base.ql\"\n@block b {\nchild[{{ parent() }}]\n@}\n",
		}},
	// A trait using another trait: the nested trait's blocks flatten first,
	// and block() renders the merged table's winning definition.
	{name: "u-trait-nested", entry: "page.ql",
		templates: map[string]string{
			"inner.ql": "@block shared {\ninner-version\n@}\n@block only_inner {\nio\n@}\n",
			"outer.ql": "@use \"inner.ql\"\n@block shared {\nouter-version\n@}\n",
			"page.ql":  "@use \"outer.ql\"\n{{ block(\"shared\") }}|{{ block(\"only_inner\") }}\n",
		}},
	// An @extends candidate list whose first candidate is a unit member.
	{name: "u-extends-candidates", entry: "page.ql", varsJSON: `{"v":"x"}`,
		templates: map[string]string{
			"base.ql": "B[{{ v }}]\n@block c {\nbc\n@}\n",
			"page.ql": "@extends [\"base.ql\", \"missing.ql\"]\n@block c {\npc\n@}\n",
		}},
	// {{ parent() | upper }} under html autoescape: the captured parent text
	// is Safe, survives the filter as its upper-cased bytes, and the print
	// does not re-escape the filter's plain-Str result differently from the
	// interpreter.
	{name: "u-parent-upper-html", entry: "page.ql", opts: compile.Options{AutoescapeHTML: true},
		templates: map[string]string{
			"base.ql": "@block b {\n<em>&amp;</em>\n@}\n",
			"page.ql": "@extends \"base.ql\"\n@block b {\n{{ parent() | upper }}|{{ parent() }}\n@}\n",
		}},
	// block(name) and block(name, other) captures, including a null second
	// argument selecting the one-argument form.
	{name: "u-block-fn", entry: "page.ql",
		templates: map[string]string{
			"lib.ql":  "@block widget {\nWIDGET\n@}\n",
			"page.ql": "@block local {\nLOCAL\n@}\nx={{ block(\"local\") }} y={{ block(\"widget\", \"lib.ql\") }} z={{ block(\"local\", null) }}\n",
		}},
	// A block site inside a loop body: the inlined body reads the loop's
	// metadata and binds a name copied back per iteration.
	{name: "u-block-in-loop", entry: "page.ql", varsJSON: `{"items":[10,20,30]}`,
		templates: map[string]string{
			"base.ql": "@block row {\nR\n@}\n",
			"page.ql": "@extends \"base.ql\"\n@block row {\n{{ loop.index }}:{{ it }} last={{ prev ?? \"-\" }}\n@set prev = it\n@}\n",
		}},
	// The row block above never renders through page's own body (child body
	// is dropped); this case puts the SITE in the topmost body's loop.
	{name: "u-loop-site", entry: "page.ql", varsJSON: `{"items":[10,20,30]}`,
		templates: map[string]string{
			"base.ql": "@for it in items {\n@block row {\nbase {{ loop.index }}\n@}\n@}\ntotal={{ seen ?? 0 }}\n",
			"page.ql": "@extends \"base.ql\"\n@block row {\npage {{ loop.index }}/{{ loop.length }} {{ it }}\n@set seen = (seen ?? 0) + 1\n@}\n",
		}},
	// The u-block-in-loop shape without a base loop: the site sits directly
	// in the topmost body, the override binds a name read after the loop.
	// The same block name defined (and sited) twice in the topmost body: both
	// sites render the merged table's winning definition.
	{name: "u-two-sites", entry: "page.ql", varsJSON: `{"t":"x"}`,
		templates: map[string]string{
			"base.ql": "1:\n@block b {\nD\n@}\n2:\n@block b {\nD2\n@}\n",
			"page.ql": "@extends \"base.ql\"\n@block b {\nOV[{{ t }}]\n@}\n",
		}},
	// Two sites nested in DIFFERENT loops: the shared body's loop reads must
	// stay correct at both sites (the analyzer materializes on conflict).
	{name: "u-two-sites-two-loops", entry: "page.ql",
		templates: map[string]string{
			"base.ql": "@for a in [1,2] {\n@block b {\nx\n@}\n@}\n@for c in [7,8,9] {\n@block b {\ny\n@}\n@}\n",
			"page.ql": "@extends \"base.ql\"\n@block b {\ni={{ loop.index }} n={{ loop.length }}\n@}\n",
		}},
	// loop.changed inside a block inlined TWICE within one loop body: the two
	// sites share one memory, exactly like the interpreter's node-keyed map.
	{name: "u-changed-two-sites", entry: "page.ql",
		templates: map[string]string{
			"page.ql": "@for x in [1, 1, 2] {\n@block c {\n[{{ loop.changed(x) }}]\n@}\n@block c {\nsecond\n@}\n@}\n",
		}},
	// A @for INSIDE a block body nested under the topmost body's loop: the
	// inner loop's parent link crosses the template boundary.
	{name: "u-loop-parent-cross", entry: "page.ql",
		templates: map[string]string{
			"base.ql": "@for a in [1,2] {\n@block cell {\nc\n@}\n@}\n",
			"page.ql": "@extends \"base.ql\"\n@block cell {\n@for b in [7,8] {\np{{ loop.parent.index }}i{{ loop.index }}\n@}\n@}\n",
		}},
	// A block site inside a with-frame: the body binds into the with frame
	// and reads the with-map through the frame chain.
	{name: "u-block-in-with", entry: "page.ql",
		templates: map[string]string{
			"base.ql": "@with {v: 5} {\n@block b {\nd\n@}\n{{ w }}\n@}\n",
			"page.ql": "@extends \"base.ql\"\n@block b {\nv={{ v }}\n@set w = v * 2\n@}\n",
		}},
	// Shortcut blocks: the value form on both the base and the override.
	{name: "u-shortcut-block", entry: "page.ql", varsJSON: `{"t":"&x"}`, opts: compile.Options{AutoescapeHTML: true},
		templates: map[string]string{
			"base.ql": "t=\n@block title \"none\"\n!\n@block sub \"base-sub\"\n",
			"page.ql": "@extends \"base.ql\"\n@block title t\n",
		}},
	// A @tab region around a block site whose override calls parent(): the
	// indent and line-start cursor thread through the capture exactly like
	// the interpreter's shared state.
	{name: "u-tab-parent", entry: "page.ql",
		templates: map[string]string{
			"base.ql": "@tab(1) {\n@block b {\nbase line\nsecond\n@}\nafter\n@}\n",
			"page.ql": "@extends \"base.ql\"\n@block b {\nover\n{{ parent() }}tail\n@}\n",
		}},
	// COW value semantics across the block boundary: an array bound in the
	// child's block, aliased and member-assigned in the parent body.
	{name: "u-cow-across", entry: "child.ql",
		templates: map[string]string{
			"base.ql":  "@block init {\n@}\n@set alias = data\n@set data[0] = 99\n{{ data[0] }},{{ alias[0] }}\n",
			"child.ql": "@extends \"base.ql\"\n@block init {\n@set data = [1, 2]\n@}\n",
		}},
	// An unregistered block site (no table entry anywhere): renders its own
	// body, and a parent() inside it resolves against the ENCLOSING block
	// context, exactly like execBlockSite's fallthrough.
	{name: "u-local-block", entry: "page.ql",
		templates: map[string]string{
			"page.ql": "@for x in [1] {\n@block dyn9 {\nlocal {{ x }}\n@}\n@}\n",
		}},
	// A loop-metadata read in a body inlined BOTH inside a loop and at a
	// non-loop site, absence-suppressed: the loop site reads the counter, the
	// root site falls back, and neither site may borrow the other's inline
	// arithmetic.
	{name: "u-mixed-context-read", entry: "page.ql",
		templates: map[string]string{
			"page.ql": "@for x in [1,2] {\n@block b {\nfirst\n@}\n@}\n@block b {\ni={{ loop.index ?? \"-\" }}\n@}\n",
		}},
	// A vars-pre-existing name rebound by a block body inside a loop: the
	// copy-back must propagate the block's bind into the enclosing frame.
	{name: "u-copyback-vars", entry: "page.ql", varsJSON: `{"acc":100,"items":[1,2,3]}`,
		templates: map[string]string{
			"base.ql": "@for it in items {\n@block add {\nd\n@}\n@}\n{{ acc }}\n",
			"page.ql": "@extends \"base.ql\"\n@block add {\n@set acc = acc + it\n@}\n",
		}},
	// _context and dump() inside a block body in a loop: the needs-context
	// materialization lists the block's binds in actual first-bind order.
	{name: "u-context-in-block", entry: "page.ql",
		templates: map[string]string{
			"base.ql": "@for x in [1,2] {\n@block b {\nd\n@}\n@}\n",
			"page.ql": "@extends \"base.ql\"\n@block b {\n@if x > 1 {\n@set late = 1\n@}\n@set z = x\n{{ _context | keys | join(\",\") }};\n@}\n",
		}},
	// A block site under an @escape region: the region's strategy applies to
	// the inlined body's prints, and pops back after the site.
	{name: "u-escape-region-block", entry: "page.ql", varsJSON: `{"v":"<x&y>"}`,
		templates: map[string]string{
			"base.ql": "@escape html {\n@block b {\nd\n@}\n@}\n@block b {\nd2\n@}\nplain={{ v }}\n",
			"page.ql": "@extends \"base.ql\"\n@block b {\n{{ v }}\n@}\n",
		}},
	// A block site inside an @if arm: the body's binds stay conditional.
	{name: "u-block-in-if", entry: "page.ql", varsJSON: `{"c":true}`,
		templates: map[string]string{
			"base.ql": "@if c {\n@block b {\nd\n@}\n@}\n{{ v ?? \"unset\" }}\n",
			"page.ql": "@extends \"base.ql\"\n@block b {\n@set v = 1\n@}\n",
		}},
	// block() inside a fused filter condition: the capture renders into the
	// filter frame with the loop's candidate bound.
	{name: "u-block-in-filter", entry: "page.ql",
		templates: map[string]string{
			"page.ql": "@block flag {\nf\n@}\n@for x in [1,2,3] if (block(\"flag\") ~ x) != \"f2\" {\n{{ x }}\n@}\n",
		}},
	// block() under an active @tab region: the capture threads the qWriter
	// indent and line-start state like parent() does.
	{name: "u-tab-block-fn", entry: "page.ql",
		templates: map[string]string{
			"page.ql": "@block w {\nwide\nload\n@}\n@tab(2) {\nx={{ block(\"w\") }}y\nnext\n@}\n",
		}},
	// parent() at the end of a one-definition chain yields the empty string.
	{name: "u-parent-at-chain-end", entry: "page.ql",
		templates: map[string]string{
			"page.ql": "@block b {\n[{{ parent() }}]\n@}\n",
		}},
	// Shortcut blocks with an Int value on both writer shapes.
	{name: "u-shortcut-int", entry: "page.ql", opts: compile.Options{AutoescapeHTML: true},
		templates: map[string]string{
			"base.ql": "n=\n@block n 42\n!\n",
			"page.ql": "@extends \"base.ql\"\n",
		}},
	// A three-level extends chain: grandparent body, both children override.
	{name: "u-three-level", entry: "leaf.ql",
		templates: map[string]string{
			"root.ql": "R[\n@block a {\nroot-a\n@}\n@block b {\nroot-b\n@}\n]R\n",
			"mid.ql":  "@extends \"root.ql\"\n@block a {\nmid-a({{ parent() }})\n@}\n",
			"leaf.ql": "@extends \"mid.ql\"\n@block a {\nleaf-a({{ parent() }})\n@}\n@block b {\nleaf-b\n@}\n",
		}},
}

// TestUnitParityBattery renders the multi-template battery through the
// compiled unit path and asserts byte-equality (output or error text) against
// the facade.
func TestUnitParityBattery(t *testing.T) {
	results := map[string]*compile.Result{}
	for _, cs := range unitBattery {
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cs.name, err)
		}
		results[cs.name] = res
	}
	got := runCompiled(t, unitBattery, results)
	for _, cs := range unitBattery {
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

// unitErrorBattery pins the composition error paths: each case's compiled
// render must return the interpreter's exact error text and position, with no
// partial output beyond what the interpreter wrote.
var unitErrorBattery = []compiledCase{
	// A non-traitable @use target (it has body content).
	{name: "ue-non-traitable", entry: "page.ql",
		templates: map[string]string{
			"trait.ql": "free text\n@block t {\nx\n@}\n",
			"page.ql":  "@use \"trait.ql\"\nbody\n",
		}},
	// A @use target that is not a constant string.
	{name: "ue-dynamic-use", entry: "page.ql",
		templates: map[string]string{
			"page.ql": "@use name\nbody\n",
		}},
	// An alias naming a block the trait does not define.
	{name: "ue-alias-missing", entry: "page.ql",
		templates: map[string]string{
			"trait.ql": "@block t {\nx\n@}\n",
			"page.ql":  "@use \"trait.ql\" with {nope: local}\nbody\n",
		}},
	// An inheritance cycle: the chain-depth limit fires with the exact text.
	{name: "ue-chain-cycle", entry: "a.ql",
		templates: map[string]string{
			"a.ql": "@extends \"b.ql\"\n@block x {\na\n@}\n",
			"b.ql": "@extends \"a.ql\"\n@block x {\nb\n@}\n",
		}},
	// block() naming a block no chain member defines.
	{name: "ue-unknown-block", entry: "page.ql",
		templates: map[string]string{
			"page.ql": "head\n{{ block(\"nope\") }}\n",
		}},
	// block(name, other) naming a block the other template does not define.
	{name: "ue-unknown-block-other", entry: "page.ql",
		templates: map[string]string{
			"lib.ql":  "@block w {\nx\n@}\n",
			"page.ql": "head\n{{ block(\"nope\", \"lib.ql\") }}\n",
		}},
	// A block() miss on the left of ??: the runtime error propagates (only
	// undefined reads are suppressed), exactly like the interpreter's
	// coalesce.
	{name: "ue-block-miss-coalesce", entry: "page.ql",
		templates: map[string]string{
			"page.ql": "head\n{{ block(\"nope\") ?? \"fb\" }}\n",
		}},
	// parent() outside any overriding block.
	{name: "ue-parent-outside", entry: "page.ql",
		templates: map[string]string{
			"page.ql": "head\n{{ parent() }}\n",
		}},
	// block() with no arguments.
	{name: "ue-block-no-args", entry: "page.ql",
		templates: map[string]string{
			"page.ql": "head\n{{ block() }}\n",
		}},
	// An undefined variable read inside an inlined child block: the error
	// must cite the CHILD template's name and line, not the parent's.
	{name: "ue-undef-in-child", entry: "child.ql",
		templates: map[string]string{
			"base.ql":  "line1\n@block b {\n@}\n",
			"child.ql": "@extends \"base.ql\"\n@block b {\nok\n{{ missing }}\n@}\n",
		}},
	// A strict loop-metadata read in a body inlined both inside a loop and at
	// a non-loop site: the loop site renders, the root site raises the
	// interpreter's undefined-variable error after the loop's partial output.
	{name: "ue-mixed-context-strict", entry: "page.ql",
		templates: map[string]string{
			"page.ql": "@for x in [1,2] {\n@block b {\nfirst\n@}\n@}\n@block b {\ni={{ loop.index }}\n@}\n",
		}},
	// A runtime error inside the parent()-rendered body: positioned in the
	// PARENT template even though the call site is in the child.
	{name: "ue-error-in-parent-body", entry: "child.ql", varsJSON: `{"s":"x"}`,
		templates: map[string]string{
			"base.ql":  "@block b {\n{{ 1 + s }}\n@}\n",
			"child.ql": "@extends \"base.ql\"\n@block b {\nchild says {{ parent() }}\n@}\n",
		}},
}

// TestUnitErrorPathParity pins the composition error texts and positions of
// the compiled unit path against the facade.
func TestUnitErrorPathParity(t *testing.T) {
	results := map[string]*compile.Result{}
	for _, cs := range unitErrorBattery {
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cs.name, err)
		}
		results[cs.name] = res
	}
	got := runCompiled(t, unitErrorBattery, results)
	for _, cs := range unitErrorBattery {
		r, ok := got[cs.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", cs.name)
			continue
		}
		wantOut, wantErr := renderInterp(t, cs)
		if wantErr == nil {
			t.Fatalf("%s: expected an interp error, rendered %q", cs.name, wantOut)
		}
		if !r.failed {
			t.Errorf("%s: interp errored (%v) but compiled rendered %q", cs.name, wantErr, r.out)
			continue
		}
		if r.errText != wantErr.Error() {
			t.Errorf("%s: error text mismatch\n got  %q\n want %q", cs.name, r.errText, wantErr.Error())
		}
	}
}

// TestUnitNotCompilable asserts every construct outside the unit-compilable
// subset is rejected as a typed *NotCompilableError naming the construct:
// dynamic composition, includes, embeds, slots, macros on the entry, and
// recursive block composition. A rejection is how the unit path guarantees it
// can never leak a slot placeholder or a wrong composition.
func TestUnitNotCompilable(t *testing.T) {
	cases := []struct {
		name      string
		entry     string
		templates map[string]string
		construct string
	}{
		{"embed-dynamic", "p.ql", map[string]string{"p.ql": "@embed name {\n@block b {\no\n@}\n@}\n"}, "@embed with a dynamic source"},
		{"embed-outside-set", "p.ql", map[string]string{"p.ql": "@embed \"gone.ql\" {\n@block b {\no\n@}\n@}\n"}, "@embed of a template outside the flattenable subset"},
		// A @yield nested in a capture context is outside the compilable subset:
		// its placeholder token cannot match the interpreter's process-global
		// counter, so only top-level yields compile.
		{"yield-in-provide", "p.ql", map[string]string{"p.ql": "@provide s {\n@yield t\n@}\n"}, "@yield inside a capture/provide body"},
		{"caller-fn", "p.ql", map[string]string{"p.ql": "{{ caller() }}\n"}, `function "caller"`},
		{"entry-macro", "p.ql", map[string]string{"p.ql": "@macro m() {\nx\n@}\nbody\n"}, "@macro"},
		{"entry-import", "p.ql", map[string]string{"p.ql": "@import \"l.ql\" as l\nbody\n", "l.ql": "@macro m() {\nx\n@}\n"}, "@import"},
		{"entry-from", "p.ql", map[string]string{"p.ql": "@from \"l.ql\" import m\nbody\n", "l.ql": "@macro m() {\nx\n@}\n"}, "@from"},
		{"dynamic-extends", "p.ql", map[string]string{"p.ql": "@extends name\n@block b {\nx\n@}\n"}, "@extends with a dynamic source"},
		{"extends-outside-unit", "p.ql", map[string]string{"p.ql": "@extends \"gone.ql\"\n"}, `@extends target "gone.ql" outside the unit`},
		{"candidates-first-missing", "p.ql", map[string]string{
			"p.ql":    "@extends [\"gone.ql\", \"base.ql\"]\n@block b {\nx\n@}\n",
			"base.ql": "@block b {\nd\n@}\n"}, "@extends candidate list whose first candidate is outside the unit"},
		{"use-outside-unit", "p.ql", map[string]string{"p.ql": "@use \"gone.ql\"\n"}, `@use target "gone.ql" outside the unit`},
		{"dynamic-block-name", "p.ql", map[string]string{"p.ql": "@block b {\nx\n@}\n{{ block(name) }}\n"}, `function "block" with a dynamic block name`},
		{"dynamic-block-tpl", "p.ql", map[string]string{"p.ql": "@block b {\nx\n@}\n{{ block(\"b\", name) }}\n"}, `function "block" with a dynamic template name`},
		{"block-target-outside", "p.ql", map[string]string{"p.ql": "{{ block(\"b\", \"gone.ql\") }}\n"}, `function "block" targeting "gone.ql" outside the unit`},
		{"recursive-block", "p.ql", map[string]string{"p.ql": "@block b {\n{{ block(\"b\") }}\n@}\n"}, "block inlining beyond depth 64 (recursive block composition)"},
		{"parent-in-arrow", "c.ql", map[string]string{
			"b.ql": "@block b {\nx\n@}\n",
			"c.ql": "@extends \"b.ql\"\n@block b {\n{{ [1] | map(v => parent()) | first }}\n@}\n"}, `function "parent" inside an arrow function`},
		{"trait-cycle", "p.ql", map[string]string{
			"p.ql":  "@use \"t1.ql\"\n",
			"t1.ql": "@use \"t2.ql\"\n@block a {\nx\n@}\n",
			"t2.ql": "@use \"t1.ql\"\n@block b {\ny\n@}\n"}, "@use nesting beyond depth 64 (trait cycle?)"},
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

// TestUnitDeterminismAndHygiene compiles one unit twice and asserts
// byte-identical, gofmt-stable, ASCII-only source free of the forbidden
// tokens, with every member's source pinned in the manifest.
func TestUnitDeterminismAndHygiene(t *testing.T) {
	templates := map[string]string{
		"trait.ql": "@block t {\ntrait\n@}\n",
		"base.ql":  "@use \"trait.ql\" with {t: b}\nhead\n@block b {\nbase({{ parent() }})\n@}\n@for x in [1,2] {\n@block row {\nr{{ loop.index }}\n@}\n@}\n",
		"page.ql":  "@extends \"base.ql\"\n@block b {\npage({{ parent() }})\n@}\n@block row {\nR{{ loop.index }} {{ block(\"t\", \"trait.ql\") }}\n@}\n",
	}
	compileOnce := func() *compile.Result {
		res, err := compile.Unit("page.ql", parseUnit(t, templates), compile.Options{})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		return res
	}
	r1 := compileOnce()
	r2 := compileOnce()
	if !bytes.Equal(r1.Source, r2.Source) {
		t.Error("unit recompilation is not byte-identical")
	}
	formatted, err := format.Source(r1.Source)
	if err != nil {
		t.Fatalf("generated source does not format: %v", err)
	}
	if !bytes.Equal(formatted, r1.Source) {
		t.Error("generated source is not gofmt-stable")
	}
	for i := 0; i < len(r1.Source); i++ {
		if r1.Source[i] >= 0x80 {
			t.Fatalf("non-ASCII byte at offset %d", i)
		}
	}
	for _, token := range hygieneTokens() {
		if strings.Contains(string(r1.Source), token) {
			t.Errorf("generated source contains forbidden token %q", token)
		}
	}
	src := string(r1.Source)
	for _, want := range []string{
		`source.New("page.ql"`,
		`source.New("base.ql"`,
		`source.New("trait.ql"`,
		"Entry: qSrc.Name(),",
		"qSrc2.Name(): qSrc2.Code(),",
		"qSrc3.Name(): qSrc3.Code(),",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated unit source missing %q", want)
		}
	}
	if len(r1.LineMap) == 0 {
		t.Error("line map is empty")
	}
}

// unitOracleBucket classifies one fixture's unit-oracle outcome.
type unitOracleBucket struct {
	fixture string
	bucket  string // "unit-equal", "not-compilable", "not-compilable-config"
	reason  string
}

// TestUnitConformanceOracle drives the whole conformance corpus through
// compile.Unit with each fixture's ENTIRE template set: every fixture either
// compiles and renders byte-identical to the interpreter (including by-name
// dispatch through its multi-source manifest and the fingerprint matrix), or
// is reported not compilable with a named construct, or is excluded for a
// config the compiled path cannot honor. Every fixture lands in exactly one
// bucket; none is silently skipped.
func TestUnitConformanceOracle(t *testing.T) {
	root := filepath.Join(repoRoot(t), "testdata", "conformance")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read conformance dir: %v", err)
	}

	var buckets []unitOracleBucket
	var cases []compiledCase
	results := map[string]*compile.Result{}
	interpOut := map[string]string{}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fixture := e.Name()
		caseName := "u-" + fixture
		dir := filepath.Join(root, fixture)
		cfg := loadOracleConfig(t, dir)

		if reason := configExclusion(cfg); reason != "" {
			buckets = append(buckets, unitOracleBucket{fixture, "not-compilable-config", reason})
			continue
		}

		main := cfg.Main
		if main == "" {
			main = "template.ql"
		}
		tmpls := loadFixtureTemplates(t, dir)
		if _, ok := tmpls[main]; !ok {
			t.Fatalf("%s: main template %q missing", fixture, main)
		}

		// Parse the whole fixture set; a sibling that does not parse simply
		// stays outside the unit (a reference to it is then a typed
		// rejection), while a main that does not parse excludes the fixture
		// exactly like the Module oracle.
		mods := map[string]*ast.Node{}
		parseFailed := false
		for name, body := range tmpls {
			mod, perr := parse.Parse(source.New(name, body))
			if perr != nil {
				if name == main {
					buckets = append(buckets, unitOracleBucket{fixture, "not-compilable-config", "parse error: " + perr.Error()})
					parseFailed = true
					break
				}
				continue
			}
			mods[name] = mod
		}
		if parseFailed {
			continue
		}

		opts := compile.Options{
			PackageName:      pkgName(caseName),
			AutoescapeHTML:   cfg.Autoescape == "html",
			LenientVariables: cfg.Strict != nil && !*cfg.Strict,
		}
		res, cerr := compile.Unit(main, mods, opts)
		if cerr != nil {
			var nce *compile.NotCompilableError
			if errors.As(cerr, &nce) {
				buckets = append(buckets, unitOracleBucket{fixture, "not-compilable", nce.Construct})
				continue
			}
			t.Fatalf("%s: unexpected unit compile error: %v", fixture, cerr)
		}

		varsJSON := loadFixtureData(t, dir)
		want, rerr := renderFixtureInterp(t, tmpls, main, varsJSON, cfg)
		if rerr != nil {
			buckets = append(buckets, unitOracleBucket{fixture, "not-compilable-config", "interp render error: " + rerr.Error()})
			continue
		}

		results[caseName] = res
		interpOut[caseName] = want
		cases = append(cases, compiledCase{
			name: caseName, templates: tmpls, entry: main, varsJSON: varsJSON,
			opts: opts, envCheck: true,
		})
	}

	got := runCompiled(t, cases, results)
	for _, cs := range cases {
		fixture := strings.TrimPrefix(cs.name, "u-")
		r, ok := got[cs.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", fixture)
			continue
		}
		if r.failed {
			t.Errorf("%s: unit render errored: %s", fixture, r.errText)
			continue
		}
		if r.out != interpOut[cs.name] {
			t.Errorf("%s: unit output differs from interpreter\n got  %q\n want %q", fixture, r.out, interpOut[cs.name])
			continue
		}
		buckets = append(buckets, unitOracleBucket{fixture, "unit-equal", ""})

		envR, ok := got[cs.name+"@env"]
		switch {
		case !ok:
			t.Errorf("%s: no env-dispatch result from scratch run", fixture)
		case envR.failed:
			t.Errorf("%s: env-dispatch render errored: %s", fixture, envR.errText)
		case envR.out != interpOut[cs.name]:
			t.Errorf("%s: env-dispatch output differs from interpreter\n got  %q\n want %q", fixture, envR.out, interpOut[cs.name])
		}
		tr, ok := got[cs.name+"@tracer"]
		switch {
		case !ok:
			t.Errorf("%s: no tracer result from scratch run", fixture)
		case tr.failed:
			t.Errorf("%s: tracer render errored: %s", fixture, tr.errText)
		case tr.out != "served":
			t.Errorf("%s: dispatch gate fell back for a unit it should serve", fixture)
		}
		mx, ok := got[cs.name+"@matrix"]
		switch {
		case !ok:
			t.Errorf("%s: no fingerprint-matrix result from scratch run", fixture)
		case mx.failed:
			t.Errorf("%s: fingerprint-matrix leg failed: %s", fixture, mx.errText)
		case mx.out != "ok":
			t.Errorf("%s: fingerprint-matrix leg reported %q", fixture, mx.out)
		}
	}

	sort.Slice(buckets, func(i, j int) bool { return buckets[i].fixture < buckets[j].fixture })
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, b := range buckets {
		if seen[b.fixture] {
			t.Errorf("%s: classified twice", b.fixture)
		}
		seen[b.fixture] = true
		counts[b.bucket]++
		if b.reason != "" {
			t.Logf("%-32s %s (%s)", b.fixture, b.bucket, b.reason)
		} else {
			t.Logf("%-32s %s", b.fixture, b.bucket)
		}
	}
	total := 0
	for _, e := range entries {
		if e.IsDir() {
			total++
		}
	}
	if len(seen) != total {
		t.Errorf("classified %d fixtures, corpus has %d", len(seen), total)
	}
	t.Logf("unit oracle: unit-equal=%d not-compilable=%d not-compilable-config=%d total=%d",
		counts["unit-equal"], counts["not-compilable"], counts["not-compilable-config"], total)
	if counts["unit-equal"] < 38 {
		t.Errorf("only %d fixtures compiled byte-equal through Unit; the flattening should cover at least 38", counts["unit-equal"])
	}
}
