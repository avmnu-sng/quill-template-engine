package quill

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/loader"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// spillSets returns n @set statements binding prefix01..prefixNN, enough to
// push the surrounding frame past the ordered-slice spill width so a fixture
// exercises the map-indexed frame regime, not just the linear scan.
func spillSets(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "@set %s%02d = %d\n", prefix, i, i)
	}
	return b.String()
}

// TestScopeFrameSpillSemantics pins the observable behavior of frames on both
// sides of the spill boundary against outputs recorded from the flat-map scope
// representation: copy-on-write privatization after a cross-frame read inside
// a spilled child frame, member writes under a spilled root, loop write-back
// landing in the immediately enclosing frame (shadowing, not updating, a
// grandparent binding), _context name ordering across a spilled frame, and the
// strict-undefined "available:" hint listing a spilled frame's names in
// first-bind order.
func TestScopeFrameSpillSemantics(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		want string
	}{
		{
			name: "member_write_after_cross_frame_read_spilled_child",
			tmpl: "@set x = [1, 2]\n@set x[0] = 9\n@with { d: 1 } {\n" +
				spillSets("a", 9) +
				"@set y = x\n@set x[1] = 77\nin: {{ x | json }} {{ y | json }} {{ a09 }}\n@}\nout: {{ x | json }}\n",
			want: "in: [9,77] [9,2] 9\nout: [9,2]\n",
		},
		{
			name: "spilled_root_with_child_mutation",
			tmpl: spillSets("n", 10) +
				"@set x = [1, 2]\n@set x[0] = 9\n@with { d: 1 } {\n@set x[1] = 77\nin: {{ x | json }}\n@}\nout: {{ x | json }} {{ n10 }}\n",
			want: "in: [9,77]\nout: [9,2] 10\n",
		},
		{
			name: "loop_writeback_shadows_grandparent",
			tmpl: "@set total = 0\n@with { a: 1 } {\n@for x in [1, 2, 3] {\n@set total = total + x\n@}\nmid: {{ total }}\n@}\nout: {{ total }}\n",
			want: "mid: 6\nout: 0\n",
		},
		{
			name: "context_ordering_across_spilled_frame",
			tmpl: spillSets("c", 10) +
				"@with { w: 0 } {\n@set z = 1\nctx: {{ _context | keys | json }}\n@}\n",
			want: "ctx: [\"c01\",\"c02\",\"c03\",\"c04\",\"c05\",\"c06\",\"c07\",\"c08\",\"c09\",\"c10\",\"w\",\"z\"]\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := New(loader.NewArrayLoader(map[string]string{"main.ql": tc.tmpl}))
			got, err := env.Render(context.Background(), "main.ql", nil)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if got != tc.want {
				t.Fatalf("spill-frame semantics drifted\ngot:  %q\nwant: %q", got, tc.want)
			}
			var sb strings.Builder
			if err := env.RenderTo(context.Background(), &sb, "main.ql", nil); err != nil {
				t.Fatalf("renderto: %v", err)
			}
			if sb.String() != tc.want {
				t.Fatalf("RenderTo diverged from Render\ngot:  %q\nwant: %q", sb.String(), tc.want)
			}
		})
	}
}

// TestStrictUndefinedHintAcrossSpilledFrame pins the full error text -- the
// "available:" list byte for byte -- when the frame holding the hint names has
// spilled to its map index: the entries slice stays the order record, so the
// hint lists first-bind order exactly as the flat-map representation did.
func TestStrictUndefinedHintAcrossSpilledFrame(t *testing.T) {
	env := New(loader.NewArrayLoader(map[string]string{
		"main.ql": spillSets("b", 10) + "{{ missing_name }}\n",
	}))
	_, err := env.Render(context.Background(), "main.ql", nil)
	if err == nil {
		t.Fatal("want strict-undefined error, got nil")
	}
	want := "quill undefined error: undefined variable \"missing_name\" " +
		"(available: b01, b02, b03, b04, b05, b06, b07, b08, b09, b10) (main.ql:11)"
	if err.Error() != want {
		t.Fatalf("hint text drifted\ngot:  %q\nwant: %q", err.Error(), want)
	}
}

// TestWideRootFrameStress feeds the render entrypoints a root frame far past
// the spill width -- the NewScopeSized pre-sizing path -- and checks every
// binding reads back correctly and copy-on-write isolation still holds for a
// host-provided array mutated inside a child frame.
func TestWideRootFrameStress(t *testing.T) {
	vars := map[string]runtime.Value{}
	var refs strings.Builder
	for i := 0; i < 30; i++ {
		vars[fmt.Sprintf("v%02d", i)] = runtime.Int(int64(i * i))
		fmt.Fprintf(&refs, "{{ v%02d }} ", i)
	}
	list := runtime.NewArray()
	list.SetInt(0, runtime.Int(1))
	list.SetInt(1, runtime.Int(2))
	vars["wx"] = runtime.Arr(list)
	tmpl := refs.String() + "\n@with { d: 1 } {\n@set wx[0] = 42\nin: {{ wx | json }}\n@}\nout: {{ wx | json }}\n"
	want := "0 1 4 9 16 25 36 49 64 81 100 121 144 169 196 225 256 289 324 361 " +
		"400 441 484 529 576 625 676 729 784 841 \nin: [42,2]\nout: [1,2]\n"
	env := New(loader.NewArrayLoader(map[string]string{"main.ql": tmpl}))
	got, err := env.Render(context.Background(), "main.ql", vars)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got != want {
		t.Fatalf("wide root frame drifted\ngot:  %q\nwant: %q", got, want)
	}
	var sb strings.Builder
	if err := env.RenderTo(context.Background(), &sb, "main.ql", vars); err != nil {
		t.Fatalf("renderto: %v", err)
	}
	if sb.String() != want {
		t.Fatalf("RenderTo diverged from Render\ngot:  %q\nwant: %q", sb.String(), want)
	}
}
