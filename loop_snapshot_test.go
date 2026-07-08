package quill

import (
	"context"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// TestLoopValueIsFrozenSnapshot pins the load-bearing invariant behind the
// computed loop value: because a fresh loop is bound each iteration, a captured
// loop (@set snap = loop) is a frozen snapshot of the step it was taken in, not a
// live handle that later iterations mutate. The template captures loop at the end
// of each iteration and reads its index at the start of the next, so a correct
// snapshot yields the PRIOR index (1 then 2) and never the current one (3). A
// future loop-metadata optimization that reused one object per loop would regress
// this to 2 then 3.
func TestLoopValueIsFrozenSnapshot(t *testing.T) {
	src := "@for n in [10,20,30] {\n" +
		"@if not loop.first {\n" +
		"was={{ snap.index }}\n" +
		"@}\n" +
		"@set snap = loop\n" +
		"@}"
	env := NewFromMap(map[string]string{"t.ql": src})
	out, err := env.Render(context.Background(), "t.ql", map[string]runtime.Value{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out, "was=1\nwas=2\n"; got != want {
		t.Fatalf("captured loop is not a frozen snapshot: got %q, want %q", got, want)
	}
}

// TestLoopParentCapturedInsideInnerIterations pins the parent half of the
// frozen-capture contract across nesting: loop.parent captured INSIDE an inner
// iteration is a standalone snapshot of the outer loop's current step, readable
// unchanged after the inner loop has ended and, via copy-back, after the outer
// loop has ended too. The parent metadata object outlives the loop frame that
// probed it, so the entry-time probe it reads through must stay intact.
func TestLoopParentCapturedInsideInnerIterations(t *testing.T) {
	src := "@set keep = {}\n" +
		"@for a in [10, 20] {\n" +
		"@for b in [1, 2, 3] {\n" +
		"@if loop.index == 2 {\n" +
		"@set keep = loop.parent\n" +
		"@}\n" +
		"@}\n" +
		"after-inner: {{ keep.index }}/{{ keep.first }}/{{ keep.last }}\n" +
		"@}\n" +
		"post: {{ keep.index }}/{{ keep.last }}\n"
	env := NewFromMap(map[string]string{"t.ql": src})
	out, err := env.Render(context.Background(), "t.ql", map[string]runtime.Value{})
	if err != nil {
		t.Fatal(err)
	}
	want := "after-inner: 1/true/false\nafter-inner: 2/false/true\npost: 2/true\n"
	if out != want {
		t.Fatalf("captured loop.parent drifted: got %q, want %q", out, want)
	}
}

// TestLoopParentChainCapturedAcrossNesting captures a MIDDLE loop's value from
// the innermost body of a three-deep nest and reads both it and its own parent
// after every loop has ended: the middle snapshot's parent pointer must still
// reach the outer step that was current when the middle loop entered.
func TestLoopParentChainCapturedAcrossNesting(t *testing.T) {
	src := "@set snap = {}\n" +
		"@for a in [1, 2] {\n" +
		"@for b in [1, 2] {\n" +
		"@for c in [1, 2] {\n" +
		"@if loop.parent.parent.index == 2 and loop.parent.index == 1 and loop.index == 1 {\n" +
		"@set snap = loop.parent\n" +
		"@}\n" +
		"@}\n" +
		"@}\n" +
		"@}\n" +
		"{{ snap.index }}/{{ snap.parent.index }}\n"
	env := NewFromMap(map[string]string{"t.ql": src})
	out, err := env.Render(context.Background(), "t.ql", map[string]runtime.Value{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out, "1/2\n"; got != want {
		t.Fatalf("parent chain through a captured snapshot drifted: got %q, want %q", got, want)
	}
}

// TestCapturedLoopValuesStayEqualAndIndependent captures the loop value in two
// different iterations of one nested loop and reads both after the loops end:
// each capture keeps its own step's fields (equal to what that step resolved
// live) and does not disturb the other, while both parents read the same outer
// step -- the three properties a layout that shared or reused per-iteration
// state would break.
func TestCapturedLoopValuesStayEqualAndIndependent(t *testing.T) {
	src := "@set first = {}\n" +
		"@set second = {}\n" +
		"@for a in [5] {\n" +
		"@for n in [7, 8, 9] {\n" +
		"@if loop.index == 1 {\n" +
		"@set first = loop\n" +
		"@}\n" +
		"@if loop.index == 2 {\n" +
		"@set second = loop\n" +
		"@}\n" +
		"@}\n" +
		"@}\n" +
		"{{ first.index }}/{{ first.prev }}/{{ first.next }} " +
		"{{ second.index }}/{{ second.prev }}/{{ second.next }} " +
		"{{ first.parent.index }}/{{ second.parent.index }}\n"
	env := NewFromMap(map[string]string{"t.ql": src})
	out, err := env.Render(context.Background(), "t.ql", map[string]runtime.Value{})
	if err != nil {
		t.Fatal(err)
	}
	want := "1//8 2/7/9 1/1\n"
	if out != want {
		t.Fatalf("iteration captures are not independent snapshots: got %q, want %q", out, want)
	}
}

// TestRecursiveLoopDepthSurface renders a recursive @for over a small tree and
// pins depth/depth0 alongside index/length at every level, byte-exact: the
// recursive constructor threads its extra counters through the same compact
// metadata object as a plain loop.
func TestRecursiveLoopDepthSurface(t *testing.T) {
	src := "@for node in tree recursive {\n" +
		"d{{ loop.depth }}/d0{{ loop.depth0 }}:{{ node.name }}:i{{ loop.index }}:l{{ loop.length }}\n" +
		"{{ loop(node.children) }}\n" +
		"@}\n"
	leaf := func(name string) runtime.Value {
		m := runtime.NewArray()
		m.SetStr("name", runtime.Str(name))
		m.SetStr("children", runtime.Arr(runtime.NewArray()))
		return runtime.Arr(m)
	}
	node := func(name string, children ...runtime.Value) runtime.Value {
		kids := runtime.NewArray()
		for i, c := range children {
			kids.SetInt(int64(i), c)
		}
		m := runtime.NewArray()
		m.SetStr("name", runtime.Str(name))
		m.SetStr("children", runtime.Arr(kids))
		return runtime.Arr(m)
	}
	top := runtime.NewArray()
	top.SetInt(0, node("a", node("b", leaf("c"), leaf("d"))))
	env := NewFromMap(map[string]string{"t.ql": src})
	out, err := env.Render(context.Background(), "t.ql", map[string]runtime.Value{"tree": runtime.Arr(top)})
	if err != nil {
		t.Fatal(err)
	}
	want := "d1/d00:a:i1:l1\nd2/d01:b:i1:l1\nd3/d02:c:i1:l2\n\nd3/d02:d:i2:l2\n\n\n\n"
	if out != want {
		t.Fatalf("recursive depth surface drifted: got %q, want %q", out, want)
	}
}

// TestLoopValueFieldSurface checks the loop.* fields the computed value resolves,
// including nested loop.parent, the loop["field"] subscript form, prev/next around
// a middle element, and that loop still reports as a mapping.
func TestLoopValueFieldSurface(t *testing.T) {
	cases := []struct{ name, src, wantContains string }{
		{"parent", "@for a in [1,2] {\n@for b in [7,8] {\np{{ loop.parent.index }}i{{ loop.index }}\n@}\n@}", "p2i1"},
		{"subscript", "@for x in [5] {\n[{{ loop[\"index\"] }},{{ loop.first }},{{ loop.last }},{{ loop.length }}]\n@}", "[1,true,true,1]"},
		{"prevnext", "@for x in [1,2,3] {\n({{ loop.prev }}<{{ x }}>{{ loop.next }})\n@}", "(1<2>3)"},
		{"ismapping", "@for x in [1] {\n{{ loop is mapping }}\n@}", "true"},
	}
	for _, c := range cases {
		env := NewFromMap(map[string]string{"t.ql": c.src})
		out, err := env.Render(context.Background(), "t.ql", map[string]runtime.Value{})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !strings.Contains(out, c.wantContains) {
			t.Errorf("%s: got %q, want contains %q", c.name, out, c.wantContains)
		}
	}
}
