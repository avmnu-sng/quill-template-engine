package quill

import (
	"context"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/cover"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// TestCoverageProvideYield seeds and hits the @provide and @yield units.
func TestCoverageProvideYield(t *testing.T) {
	// 1: @provide s {
	// 2: x
	// 3: @}
	// 4: @yield s
	src := "@provide s {\nx\n@}\n@yield s\n"
	coll := cover.NewCollector()
	env := NewFromMap(map[string]string{"t.ql": src}, WithCoverage(coll))
	if _, err := env.Render(context.Background(), "t.ql", nil); err != nil {
		t.Fatal(err)
	}
	r := coll.Report()
	assertCovered(t, r, "t.ql", 1, cover.UnitProvide)
	assertCovered(t, r, "t.ql", 4, cover.UnitYield)
}

// TestCoverageCallBlock seeds and hits the @call unit.
func TestCoverageCallBlock(t *testing.T) {
	// 1: @macro w() {
	// 2: {{ caller() }}
	// 3: @}
	// 4: @call w() {
	// 5: body
	// 6: @}
	src := "@macro w() {\n{{ caller() }}\n@}\n@call w() {\nbody\n@}\n"
	coll := cover.NewCollector()
	env := NewFromMap(map[string]string{"t.ql": src}, WithCoverage(coll))
	if _, err := env.Render(context.Background(), "t.ql", nil); err != nil {
		t.Fatal(err)
	}
	r := coll.Report()
	assertCovered(t, r, "t.ql", 4, cover.UnitCallBlock)
	assertCovered(t, r, "t.ql", 1, cover.UnitMacro)
}

// TestCoverageRecursiveForArms exercises the body/empty arms of a recursive @for.
func TestCoverageRecursiveForArms(t *testing.T) {
	src := "@for n in tree recursive {\n{{ n.name }}{{ loop(n.children) }}\n@}\n"
	coll := cover.NewCollector()
	env := NewFromMap(map[string]string{"t.ql": src}, WithCoverage(coll))

	leaf := runtime.NewArray()
	leaf.SetStr("name", runtime.Str("only"))
	leaf.SetStr("children", runtime.Arr(runtime.NewArray()))
	top := runtime.NewArray()
	top.SetInt(0, runtime.Arr(leaf))
	if _, err := env.Render(context.Background(), "t.ql", map[string]runtime.Value{"tree": runtime.Arr(top)}); err != nil {
		t.Fatal(err)
	}
	r := coll.Report()
	assertCovered(t, r, "t.ql", 1, cover.UnitFor)
	assertCovered(t, r, "t.ql", 1, cover.ForBody)
	assertUncovered(t, r, "t.ql", 1, cover.ForEmpty)
}
