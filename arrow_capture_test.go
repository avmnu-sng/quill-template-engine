package quill

import (
	"context"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/loader"
)

// TestArrowCapturesLexicalScopeLive pins the arrow-closure capture contract:
// an arrow captures its DEFINITION SCOPE by reference, so applying it later
// reads the current value of every captured name -- uniformly, whether the
// arrow was defined at top level or inside a scope-introducing construct.
// Names rebound inside the defining scope read that frame; names that live in
// an outer frame read through the live parent chain, so a rebind after the
// scope exits is visible to the closure.
func TestArrowCapturesLexicalScopeLive(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			// A top-level arrow sees a later rebind of a captured name.
			name: "top_level_live",
			body: "@set base = 10\n@set f = (n) => n + base\n@set base = 99\n{{ [0] | map(f) | first }}\n",
			want: "99\n",
		},
		{
			// An arrow defined inside a loop reads an outer name through the live
			// chain: the post-loop rebind is visible at application time, exactly
			// like the top-level case.
			name: "loop_defined_reads_outer_live",
			body: "@set base = 10\n@set f = null\n@for x in [1] {\n@set f = (n) => n + base\n@}\n@set base = 99\n{{ [0] | map(f) | first }}\n",
			want: "99\n",
		},
		{
			// A name rebound INSIDE the defining loop frame reads that frame's
			// final value (the loop frame is reused across iterations).
			name: "loop_frame_name_reads_final",
			body: "@set total = 0\n@set f = null\n@for x in [1, 2, 3] {\n@set f = (n) => n + total\n@set total = total + x\n@}\n{{ [0] | map(f) | first }}\n",
			want: "6\n",
		},
		{
			// An arrow returned by an arrow chains through the invoke frame to the
			// live outer scope: the outer rebind of x is visible when the inner
			// arrow finally runs (5 + 10 + 100).
			name: "arrow_returning_arrow_reads_live",
			body: "@set x = 1\n@set mk = (a) => ((b) => a + b + x)\n@set add = [5] | map(mk) | first\n@set x = 100\n{{ [10] | map(add) | first }}\n",
			want: "115\n",
		},
		{
			// An arrow defined inside an @include body and smuggled out through a
			// host cell reads the includer's live scope at application time.
			name: "arrow_via_cell_from_include",
			body: "", // multi-template case, handled below
			want: "99\n",
		},
		{
			// The same through a @call block body.
			name: "arrow_via_cell_from_caller_block",
			body: "@macro wrap() {\n{{ caller() }}\n@}\n@set x = 1\n@set c = cell(null)\n@call wrap() {\n@set c.value = () => x\n@}\n@set x = 42\n{{ [0] | map(c.value) | first }}\n",
			want: "\n42\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpls := map[string]string{"main.ql": tc.body}
			if tc.name == "arrow_via_cell_from_include" {
				tmpls = map[string]string{
					"main.ql": "@set x = 1\n@set c = cell(null)\n@include \"part.ql\"\n@set x = 99\n{{ [0] | map(c.value) | first }}\n",
					"part.ql": "@set c.value = () => x\n",
				}
			}
			got, err := New(loader.NewArrayLoader(tmpls)).Render(context.Background(), "main.ql", nil)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if got != tc.want {
				t.Fatalf("arrow capture contract drifted: got %q, want %q", got, tc.want)
			}
		})
	}
}
