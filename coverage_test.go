package quill

import (
	"bytes"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/cover"
	"github.com/avmnu-sng/quill-template-engine/loader"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// findRegion returns the region of the given kind at the given 1-based line in a
// template's coverage, and whether it was found. Col is not matched so a test can
// assert on line+kind without hard-coding columns.
func findRegion(t *testing.T, r *cover.Report, tmpl string, line int, kind cover.RegionKind) (cover.Region, bool) {
	t.Helper()
	for _, tc := range r.Templates() {
		if tc.Name != tmpl {
			continue
		}
		for _, reg := range tc.Regions {
			if reg.Line == line && reg.Kind == kind {
				return reg, true
			}
		}
	}
	return cover.Region{}, false
}

// assertCovered fails unless a region of kind at line exists and was hit.
func assertCovered(t *testing.T, r *cover.Report, tmpl string, line int, kind cover.RegionKind) {
	t.Helper()
	reg, ok := findRegion(t, r, tmpl, line, kind)
	if !ok {
		t.Fatalf("%s:%d:%s region not found (was it seeded?)", tmpl, line, kind)
	}
	if !reg.Covered() {
		t.Errorf("%s:%d:%s expected COVERED, got %d hits", tmpl, line, kind, reg.Hits)
	}
}

// assertUncovered fails unless a region of kind at line exists and was NOT hit.
func assertUncovered(t *testing.T, r *cover.Report, tmpl string, line int, kind cover.RegionKind) {
	t.Helper()
	reg, ok := findRegion(t, r, tmpl, line, kind)
	if !ok {
		t.Fatalf("%s:%d:%s region not found (was it seeded?)", tmpl, line, kind)
	}
	if reg.Covered() {
		t.Errorf("%s:%d:%s expected UNCOVERED, got %d hits", tmpl, line, kind, reg.Hits)
	}
}

// TestCoverageIfArms renders an @if/@elseif/@else chain with data that takes the
// first arm only, then asserts exactly which arms are covered.
func TestCoverageIfArms(t *testing.T) {
	// Line map (clauses close-and-reopen on one line, as Quill spells them):
	// 1: @if a {
	// 2: A
	// 3: @} elseif b {
	// 4: B
	// 5: @} else {
	// 6: C
	// 7: @}
	src := "@if a {\nA\n@} elseif b {\nB\n@} else {\nC\n@}\n"
	coll := cover.NewCollector()
	env := NewWithArray(map[string]string{"t.ql": src}, WithCoverage(coll))

	// a=true, b=false: the first clause is taken; b is never evaluated.
	if _, err := env.Render("t.ql", map[string]runtime.Value{
		"a": runtime.Bool(true), "b": runtime.Bool(false),
	}); err != nil {
		t.Fatal(err)
	}
	r := coll.Report()

	assertCovered(t, r, "t.ql", 1, cover.UnitIf)       // the @if reached
	assertCovered(t, r, "t.ql", 1, cover.IfThen)       // first arm taken
	assertUncovered(t, r, "t.ql", 1, cover.IfNotTaken) // first cond never false
	assertUncovered(t, r, "t.ql", 3, cover.IfThen)     // elseif never taken
	assertUncovered(t, r, "t.ql", 3, cover.IfNotTaken) // elseif never evaluated
	assertUncovered(t, r, "t.ql", 5, cover.IfElse)     // else never ran
	assertCovered(t, r, "t.ql", 2, cover.UnitText)     // body A ran
	assertUncovered(t, r, "t.ql", 4, cover.UnitText)   // body B did not
	assertUncovered(t, r, "t.ql", 6, cover.UnitText)   // body C did not
}

// TestCoverageIfArmsBothRenders unions two renders that take different arms, so a
// clause becomes fully covered (both taken and not-taken fired).
func TestCoverageIfArmsBothRenders(t *testing.T) {
	// 1: @if a {
	// 2: A
	// 3: @} else {
	// 4: B
	// 5: @}
	src := "@if a {\nA\n@} else {\nB\n@}\n"
	coll := cover.NewCollector()
	env := NewWithArray(map[string]string{"t.ql": src}, WithCoverage(coll))

	if _, err := env.Render("t.ql", map[string]runtime.Value{"a": runtime.Bool(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Render("t.ql", map[string]runtime.Value{"a": runtime.Bool(false)}); err != nil {
		t.Fatal(err)
	}
	r := coll.Report()

	// Across the two renders the if clause's then and not-taken both fired, and the
	// else arm fired: the branch point is fully covered.
	assertCovered(t, r, "t.ql", 1, cover.IfThen)
	assertCovered(t, r, "t.ql", 1, cover.IfNotTaken)
	assertCovered(t, r, "t.ql", 3, cover.IfElse)
	assertCovered(t, r, "t.ql", 2, cover.UnitText)
	assertCovered(t, r, "t.ql", 4, cover.UnitText)

	var tc cover.TemplateCoverage
	for _, x := range r.Templates() {
		if x.Name == "t.ql" {
			tc = x
		}
	}
	// Three branch arms (if-then, if-not-taken, else), all covered.
	if tc.Branches != (cover.Counts{Covered: 3, Total: 3}) {
		t.Errorf("branches = %+v want {3 3}", tc.Branches)
	}
}

// TestCoverageForArms covers the two @for arms across a non-empty and an empty
// render.
func TestCoverageForArms(t *testing.T) {
	// 1: @for x in xs {
	// 2: {{ x }}
	// 3: @} else {
	// 4: none
	// 5: @}
	src := "@for x in xs {\n{{ x }}\n@} else {\nnone\n@}\n"
	coll := cover.NewCollector()
	env := NewWithArray(map[string]string{"t.ql": src}, WithCoverage(coll))

	// Non-empty: body arm.
	if _, err := env.Render("t.ql", map[string]runtime.Value{
		"xs": runtime.Arr(runtime.NewList(runtime.Int(1), runtime.Int(2))),
	}); err != nil {
		t.Fatal(err)
	}
	r := coll.Report()
	assertCovered(t, r, "t.ql", 1, cover.UnitFor)
	assertCovered(t, r, "t.ql", 1, cover.ForBody)
	assertUncovered(t, r, "t.ql", 1, cover.ForEmpty)
	assertCovered(t, r, "t.ql", 2, cover.UnitPrint)  // body ran
	assertUncovered(t, r, "t.ql", 4, cover.UnitText) // else body did not

	// Empty: the empty arm fires and the @else body runs.
	if _, err := env.Render("t.ql", map[string]runtime.Value{
		"xs": runtime.Arr(runtime.NewArray()),
	}); err != nil {
		t.Fatal(err)
	}
	r = coll.Report()
	assertCovered(t, r, "t.ql", 1, cover.ForBody)
	assertCovered(t, r, "t.ql", 1, cover.ForEmpty)
	assertCovered(t, r, "t.ql", 4, cover.UnitText) // else body ran on the empty render
}

// TestCoveragePostfixIf covers the ternary desugaring of postfix if.
func TestCoveragePostfixIf(t *testing.T) {
	src := "{{ x if c }}\n"
	coll := cover.NewCollector()
	env := NewWithArray(map[string]string{"t.ql": src}, WithCoverage(coll))

	if _, err := env.Render("t.ql", map[string]runtime.Value{
		"x": runtime.Str("Y"), "c": runtime.Bool(true),
	}); err != nil {
		t.Fatal(err)
	}
	r := coll.Report()
	// Postfix if is a ternary: the then arm is taken, the else arm is not.
	assertCovered(t, r, "t.ql", 1, cover.TernThen)
	assertUncovered(t, r, "t.ql", 1, cover.TernElse)
}

// TestCoverageCoalesceAndElvis covers ?? and ?: arms.
func TestCoverageCoalesceAndElvis(t *testing.T) {
	// coalesce: left null -> right used; elvis: left truthy -> left kept.
	src := "{{ a ?? b }}{{ c ?: d }}\n"
	coll := cover.NewCollector()
	env := NewWithArray(map[string]string{"t.ql": src},
		WithCoverage(coll), WithStrictVariables(false))

	if _, err := env.Render("t.ql", map[string]runtime.Value{
		// a undefined -> null -> coalesce right; c truthy -> elvis left.
		"b": runtime.Str("B"), "c": runtime.Str("C"), "d": runtime.Str("D"),
	}); err != nil {
		t.Fatal(err)
	}
	r := coll.Report()
	assertUncovered(t, r, "t.ql", 1, cover.CoalLeft)
	assertCovered(t, r, "t.ql", 1, cover.CoalRight)
	assertCovered(t, r, "t.ql", 1, cover.ElvisLeft)
	assertUncovered(t, r, "t.ql", 1, cover.ElvisRight)
}

// TestCoverageMacroInclude covers a macro body and an include, each recorded
// under its own template name so a partial aggregates under itself.
func TestCoverageMacroInclude(t *testing.T) {
	main := "@import \"m.ql\" as m\n{{ m.hi(\"a\") }}\n@include \"p.ql\"\n"
	macros := "@macro hi(x) {\n[{{ x }}]\n@}\n"
	partial := "PARTIAL\n"
	coll := cover.NewCollector()
	env := NewWithArray(map[string]string{
		"main.ql": main, "m.ql": macros, "p.ql": partial,
	}, WithCoverage(coll))

	if _, err := env.Render("main.ql", nil); err != nil {
		t.Fatal(err)
	}
	r := coll.Report()

	assertCovered(t, r, "main.ql", 2, cover.UnitPrint)   // the macro call print
	assertCovered(t, r, "main.ql", 3, cover.UnitInclude) // the include
	assertCovered(t, r, "m.ql", 1, cover.UnitMacro)      // macro body invoked
	assertCovered(t, r, "p.ql", 1, cover.UnitText)       // partial text emitted
}

// TestCoverageInheritanceBlock asserts a block unit is counted under the template
// that actually renders it: a child override counts under the child, and a
// parent's overridden default (never rendered, no parent() call) stays uncovered
// under the parent.
func TestCoverageInheritanceBlock(t *testing.T) {
	parent := "P\n@block body {\ndefault\n@}\n"                  // parent.ql: block at line 2
	child := "@extends \"parent.ql\"\n@block body {\nover\n@}\n" // child.ql: block at line 2
	coll := cover.NewCollector()
	env := NewWithArray(map[string]string{"parent.ql": parent, "child.ql": child},
		WithCoverage(coll))
	out, err := env.Render("child.ql", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "P\nover\n" {
		t.Fatalf("render = %q", out)
	}
	r := coll.Report()
	assertCovered(t, r, "child.ql", 2, cover.UnitBlock)    // child override rendered
	assertCovered(t, r, "child.ql", 3, cover.UnitText)     // its body ran
	assertCovered(t, r, "parent.ql", 1, cover.UnitText)    // parent free text ran
	assertUncovered(t, r, "parent.ql", 2, cover.UnitBlock) // parent default never rendered
	assertUncovered(t, r, "parent.ql", 3, cover.UnitText)  // parent default body did not
}

// TestCoverageGuardArms covers @guard present/absent across two environments.
func TestCoverageGuardArms(t *testing.T) {
	// 1: @guard filter("upper") {
	// 2: yes
	// 3: @} else {
	// 4: no
	// 5: @}
	src := "@guard filter(\"upper\") {\nyes\n@} else {\nno\n@}\n"
	coll := cover.NewCollector()
	env := NewWithArray(map[string]string{"t.ql": src}, WithCoverage(coll))
	if _, err := env.Render("t.ql", nil); err != nil {
		t.Fatal(err)
	}
	r := coll.Report()
	// "upper" is a core filter, so the guarded body runs (present arm).
	assertCovered(t, r, "t.ql", 1, cover.UnitGuardTag)
	assertCovered(t, r, "t.ql", 1, cover.GuardYes)
	assertUncovered(t, r, "t.ql", 1, cover.GuardNo)
	assertCovered(t, r, "t.ql", 2, cover.UnitText)   // yes body ran
	assertUncovered(t, r, "t.ql", 4, cover.UnitText) // no body did not
}

// TestCoverageZeroOverheadWhenDisabled asserts that without WithCoverage the
// Environment reports no Collector, so the interp's coverage hooks are inert.
func TestCoverageZeroOverheadWhenDisabled(t *testing.T) {
	env := NewWithArray(map[string]string{"t.ql": "{{ x }}"})
	if env.Coverage() != nil {
		t.Error("an Environment without WithCoverage must have a nil Collector")
	}
	// WithCoverage(nil) is the same as off.
	env2 := NewWithArray(map[string]string{"t.ql": "{{ x }}"}, WithCoverage(nil))
	if env2.Coverage() != nil {
		t.Error("WithCoverage(nil) must leave coverage off")
	}
}

// TestCoverageWriters smoke-tests the three writers produce non-empty output with
// the expected markers.
func TestCoverageWriters(t *testing.T) {
	src := "@if a {\nA\n@}\n{{ x }}\n"
	coll := cover.NewCollector()
	env := NewWithArray(map[string]string{"page.ql": src}, WithCoverage(coll))
	if _, err := env.Render("page.ql", map[string]runtime.Value{
		"a": runtime.Bool(true), "x": runtime.Str("hi"),
	}); err != nil {
		t.Fatal(err)
	}
	r := coll.Report()

	var text, lcov, html bytes.Buffer
	if err := r.WriteText(&text); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteLCOV(&lcov); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteHTML(&html); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "page.ql") || !strings.Contains(text.String(), "TOTAL") {
		t.Errorf("text report missing table:\n%s", text.String())
	}
	if !strings.Contains(lcov.String(), "SF:page.ql") || !strings.Contains(lcov.String(), "end_of_record") {
		t.Errorf("lcov report malformed:\n%s", lcov.String())
	}
	if !strings.Contains(lcov.String(), "BRDA:1,") {
		t.Errorf("lcov report missing branch record:\n%s", lcov.String())
	}
	if !strings.Contains(html.String(), "<!doctype html>") || !strings.Contains(html.String(), "page.ql") {
		t.Errorf("html report malformed")
	}
}

// TestCoverageDoesNotChangeOutput renders a range of constructs with and without
// a Collector and asserts byte-identical output -- the binding invariant, checked
// directly here in addition to the conformance variant.
func TestCoverageDoesNotChangeOutput(t *testing.T) {
	src := "@if a {\n{{ x }}\n@} else {\nno\n@}\n" +
		"@for i in xs {\n[{{ i }}]\n@}\n" +
		"{{ y if a }}{{ z ?? \"d\" }}\n"
	vars := map[string]runtime.Value{
		"a":  runtime.Bool(true),
		"x":  runtime.Str("X"),
		"y":  runtime.Str("Y"),
		"xs": runtime.Arr(runtime.NewList(runtime.Int(1), runtime.Int(2))),
	}
	tmpls := map[string]string{"t.ql": src}

	plain := New(loader.NewArrayLoader(tmpls), WithStrictVariables(false))
	out1, err := plain.Render("t.ql", vars)
	if err != nil {
		t.Fatal(err)
	}
	coll := cover.NewCollector()
	instr := New(loader.NewArrayLoader(tmpls), WithStrictVariables(false), WithCoverage(coll))
	out2, err := instr.Render("t.ql", vars)
	if err != nil {
		t.Fatal(err)
	}
	if out1 != out2 {
		t.Errorf("coverage changed output:\n plain=%q\n instr=%q", out1, out2)
	}
}
