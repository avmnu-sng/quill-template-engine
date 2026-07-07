package quill

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/loader"
)

// TestPrivatizedArrayIsolatesInChildScopes pins the copy-on-write escape class
// the frame-stack scope made possible: after a member assignment privatizes a
// binding's array (its shared mark is cleared), a member write inside a
// DISCARDING child scope must still copy-on-write, never mutate the outer
// array in place. The cross-frame share-mark in Scope.Get is what enforces
// this; without it the mutation survives the child frame's discard. Every
// discarding construct is pinned: with, include, embed, cache, caller blocks,
// nested with, nested arrays, maps, and the recursive @for (which has no
// copy-back, so an escape there flips its whole mutation model).
func TestPrivatizedArrayIsolatesInChildScopes(t *testing.T) {
	cases := []struct {
		name  string
		tmpls map[string]string
		want  string
	}{
		{
			name: "with_scope",
			tmpls: map[string]string{"main.ql": "@set x = [1, 2]\n@set x[0] = 9\n" +
				"@with { d: 1 } {\n@set x[1] = 77\nin: {{ x | json }}\n@}\nout: {{ x | json }}\n"},
			want: "in: [9,77]\nout: [9,2]\n",
		},
		{
			name: "include_scope",
			tmpls: map[string]string{
				"main.ql": "@set x = [1, 2]\n@set x[0] = 9\n@include \"part.ql\"\nout: {{ x | json }}\n",
				"part.ql": "@set x[1] = 77\nin: {{ x | json }}\n",
			},
			want: "in: [9,77]\nout: [9,2]\n",
		},
		{
			name: "embed_scope",
			tmpls: map[string]string{
				"main.ql": "@set x = [1, 2]\n@set x[0] = 9\n@embed \"box.ql\" {\n@block b {\n@set x[1] = 77\nin: {{ x | json }}\n@}\n@}\nout: {{ x | json }}\n",
				"box.ql":  "@block b {\ndefault\n@}\n",
			},
			want: "in: [9,77]\nout: [9,2]\n",
		},
		{
			name: "cache_scope",
			tmpls: map[string]string{"main.ql": "@set x = [1, 2]\n@set x[0] = 9\n" +
				"@cache key=\"k\" {\n@set x[1] = 77\nin: {{ x | json }}\n@}\nout: {{ x | json }}\n"},
			want: "in: [9,77]\nout: [9,2]\n",
		},
		{
			name: "caller_block_scope",
			tmpls: map[string]string{"main.ql": "@macro wrap() {\n{{ caller() }}\n@}\n" +
				"@set x = [1, 2]\n@set x[0] = 9\n@call wrap() {\n@set x[1] = 77\nin: {{ x | json }}\n@}\nout: {{ x | json }}\n"},
			want: "in: [9,77]\n\nout: [9,2]\n",
		},
		{
			name: "nested_with_scopes",
			tmpls: map[string]string{"main.ql": "@set x = [1, 2]\n@set x[0] = 9\n" +
				"@with { a: 1 } {\n@with { b: 2 } {\n@set x[1] = 77\n@}\nmid: {{ x | json }}\n@}\nout: {{ x | json }}\n"},
			want: "mid: [9,2]\nout: [9,2]\n",
		},
		{
			name: "privatize_inside_with_then_deeper",
			tmpls: map[string]string{"main.ql": "@set x = [1, 2]\n" +
				"@with { a: 1 } {\n@set x[0] = 9\n@with { b: 2 } {\n@set x[1] = 77\n@}\nmid: {{ x | json }}\n@}\nout: {{ x | json }}\n"},
			want: "mid: [9,2]\nout: [1,2]\n",
		},
		{
			name: "nested_array_inner_privatized",
			tmpls: map[string]string{"main.ql": "@set x = [[1], [2]]\n@set x[0][0] = 9\n" +
				"@with { d: 1 } {\n@set x[0][0] = 55\n@}\nout: {{ x | json }}\n"},
			want: "out: [[9],[2]]\n",
		},
		{
			name: "map_member_add",
			tmpls: map[string]string{"main.ql": "@set m = {a: 1}\n@set m.a = 2\n" +
				"@with { d: 1 } {\n@set m.b = 99\n@}\nout: {{ m | json }}\n"},
			want: "out: {\"a\":2}\n",
		},
		{
			name: "recursive_for_member_mutation_discards",
			tmpls: map[string]string{"main.ql": "@set agg = {n: 0}\n@set agg.n = 0\n" +
				"@for node in tree recursive {\n@set agg.n = agg.n + 1\n{{ loop.depth }}:{{ agg.n }}\n{{ loop(node.children) }}\n@}\nend: {{ agg.n }}\n"},
			want: "", // filled below from data-driven expectation
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vars := map[string]any{}
			if tc.name == "recursive_for_member_mutation_discards" {
				vars["tree"] = []any{
					map[string]any{"name": "a", "children": []any{
						map[string]any{"name": "b", "children": []any{}},
					}},
				}
				// Each descent level's member write copies on write into its own
				// frame (a deeper level reads the shadowed value through the chain,
				// so depth 2 sees n=1 and prints 2), and every frame discards on
				// unwind: the outer agg.n stays 0 after the loop -- the same
				// isolation the flat-clone scope enforced.
				tc.want = "1:1\n2:2\n\n\nend: 0\n"
			}
			env := New(loader.NewArrayLoader(tc.tmpls))
			got, err := env.RenderValues("main.ql", vars)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if got != tc.want {
				t.Fatalf("privatized-array isolation drifted\ngot:  %q\nwant: %q", got, tc.want)
			}
		})
	}
}
