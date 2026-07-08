package quill

import (
	"context"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// TestArrayValueSemantics pins the *Array copy-on-write value semantics (spec 04
// Section 6.3) that the conformance suite does not exercise: arrays are value
// types, so a mutation through one binding never reaches an alias, a loop variable
// is a copy of the element, and a member write to a pre-existing binding persists;
// a host Object (cell) keeps reference identity so its mutation persists. Two
// earlier optimization attempts regressed exactly these behaviors while the
// conformance suite stayed green, so this test is the guard.
func TestArrayValueSemantics(t *testing.T) {
	cases := []struct{ name, src, want string }{
		// Mutating a loop variable's member must not leak into the source.
		{"loop_var_no_leak",
			"@set src = [[1,2],[3,4]]\n@for row in src {\n@set row[0] = 99\n@}\n{{ src[0][0] }},{{ src[1][0] }}",
			"1,3"},
		// @set b = a is a value copy: mutating a leaves b unchanged.
		{"alias_is_value_copy",
			"@set a = [1,2]\n@set b = a\n@set a[0] = 99\n{{ a[0] }},{{ b[0] }}",
			"99,1"},
		// Isolation holds at depth two through a nested path.
		{"nested_isolation",
			"@set d = {list: [1,2,3]}\n@set d2 = d\n@set d.list[0] = 99\n{{ d.list[0] }},{{ d2.list[0] }}",
			"99,1"},
		// A cell() (host Object) is reference identity: its mutation persists across
		// the loop, never copied.
		{"cell_persists",
			"@set acc = cell(0)\n@for w in [1,2,3,4] {\n@set acc.value = acc.value + w\n@}\n{{ acc.value }}",
			"10"},
		// Member-set accumulation into a pre-existing map persists via copy-back.
		{"member_accumulate",
			"@set m = {}\n@for k in [1,2,3] {\n@set m[k] = k * 10\n@}\n{{ m[1] }},{{ m[2] }},{{ m[3] }}",
			"10,20,30"},
		// A loop-local binding does not leak out; a member write to a pre-existing
		// name persists.
		{"local_vs_persist",
			"@set x = [10,20]\n@for i in [1] {\n@set y = [99]\n@set x[0] = y[0]\n@}\n{{ x[0] }} {{ y is defined }}",
			"99 false"},
		// A sub-value returned by a filter does not leak back to its source.
		{"filter_subvalue_no_leak",
			"@set a = [[1,2],[3,4]]\n@set f = a | first\n@set f[0] = 99\n{{ a[0][0] }},{{ f[0] }}",
			"1,99"},
	}
	for _, c := range cases {
		env := NewFromMap(map[string]string{"t.ql": c.src})
		out, err := env.Render(context.Background(), "t.ql", map[string]runtime.Value{})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if out != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, out, c.want)
		}
	}
}
