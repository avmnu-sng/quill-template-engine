package quill

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/runtime"
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
	env := NewWithArray(map[string]string{"t.ql": src})
	out, err := env.Render("t.ql", map[string]runtime.Value{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out, "was=1\nwas=2\n"; got != want {
		t.Fatalf("captured loop is not a frozen snapshot: got %q, want %q", got, want)
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
		env := NewWithArray(map[string]string{"t.ql": c.src})
		out, err := env.Render("t.ql", map[string]runtime.Value{})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !strings.Contains(out, c.wantContains) {
			t.Errorf("%s: got %q, want contains %q", c.name, out, c.wantContains)
		}
	}
}
