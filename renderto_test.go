package quill

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/loader"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// slotPlaceholderMark is the stable fragment of every deferred @yield
// placeholder token (interp.newYieldToken). A raw occurrence in RenderTo
// output means a placeholder streamed without a resolveSlots pass -- an
// immediate correctness failure of the streaming gate.
const slotPlaceholderMark = "QUILL_SLOT_"

// countingWriter records how many Write calls reached the destination, so a
// test can assert which RenderTo path ran: the buffered fallback writes the
// whole output in exactly one call, while the streaming path pushes one write
// per rendered chunk. It deliberately implements only io.Writer, so it also
// exercises writerSink's []byte fallback.
type countingWriter struct {
	buf    bytes.Buffer
	writes int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes++
	return c.buf.Write(p)
}

// failAfterWriter accepts n bytes then fails every later write, modeling a
// destination that dies mid-stream (a closed connection).
type failAfterWriter struct {
	n       int
	written int
}

func (f *failAfterWriter) Write(p []byte) (int, error) {
	if f.written+len(p) > f.n {
		return 0, errors.New("destination failed")
	}
	f.written += len(p)
	return len(p), nil
}

// TestRenderToMatchesConformanceCorpus is the equivalence oracle over the
// whole conformance corpus: for every fixture, Render's returned string and
// RenderTo's written bytes must be identical (and a failing fixture must fail
// identically through both entries), and no raw slot placeholder may ever
// reach the writer. Each path renders through its own fresh Environment so
// the two cannot share render state.
func TestRenderToMatchesConformanceCorpus(t *testing.T) {
	root := filepath.Join("testdata", "conformance")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read conformance dir: %v", err)
	}
	var ran int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			tmpls, main, vars, opts, _ := fixtureSetup(t, dir)

			got, rerr := New(loader.NewArrayLoader(tmpls), opts...).Render(main, vars)

			var buf bytes.Buffer
			terr := New(loader.NewArrayLoader(tmpls), opts...).RenderTo(&buf, main, vars)

			if bytes.Contains(buf.Bytes(), []byte(slotPlaceholderMark)) {
				t.Fatalf("raw slot placeholder leaked into RenderTo output")
			}
			if rerr != nil {
				if terr == nil {
					t.Fatalf("Render failed (%v) but RenderTo succeeded", rerr)
				}
				if rerr.Error() != terr.Error() {
					t.Fatalf("error mismatch\nRender:   %v\nRenderTo: %v", rerr, terr)
				}
				return
			}
			if terr != nil {
				t.Fatalf("RenderTo error: %v (Render succeeded)", terr)
			}
			if buf.String() != got {
				t.Errorf("output mismatch\n--- Render ----\n%q\n--- RenderTo ---\n%q", got, buf.String())
			}
		})
		ran++
	}
	if ran == 0 {
		t.Fatal("no conformance fixtures found")
	}
}

// streamCase is one streaming-vs-buffering battery scenario: a set of
// templates, the main entry, and whether the slot-freedom gate must choose the
// streaming path (stream) or the buffered fallback (!stream).
type streamCase struct {
	name   string
	tmpls  map[string]string
	main   string
	vars   map[string]runtime.Value
	stream bool
}

// batteryCases exercises every slot/streaming interaction the closure walk
// must classify correctly. Every case must render byte-identically through
// Render and RenderTo regardless of the path taken.
func batteryCases() []streamCase {
	return []streamCase{
		{
			name: "plain_large_output_streams",
			tmpls: map[string]string{"main.ql": "@for i in 1..50 {\n" +
				"line {{ i }}: {{ msg }}\n@}\n"},
			main:   "main.ql",
			vars:   map[string]runtime.Value{"msg": runtime.Str("hello")},
			stream: true,
		},
		{
			name: "yield_provide_same_file_buffers",
			tmpls: map[string]string{"main.ql": "HEAD\n@yield imports\n" +
				"@provide imports {import a\n@}\n@provide imports {import b\n@}\ntail\n"},
			main:   "main.ql",
			stream: false,
		},
		{
			name: "slot_function_form_buffers",
			tmpls: map[string]string{"main.ql": "@set x = \"seed\"\n" +
				"got: [{{ slot(\"names\") }}]\n"},
			main:   "main.ql",
			stream: false,
		},
		{
			name: "own_yield_plus_include_buffers",
			tmpls: map[string]string{
				"main.ql": "shell:\n@yield syms\n@include \"part.ql\"\nend\n",
				"part.ql": "@provide syms {SYM\n@}\npartbody\n",
			},
			main:   "main.ql",
			stream: false,
		},
		{
			name: "static_include_slot_free_streams",
			tmpls: map[string]string{
				"main.ql": "top\n@include \"part.ql\"\nbottom\n",
				"part.ql": "plain partial {{ 1 + 1 }}\n",
			},
			main:   "main.ql",
			stream: true,
		},
		{
			name: "static_include_of_providing_partial_buffers",
			tmpls: map[string]string{
				"main.ql": "@include \"part.ql\"\nend\n",
				"part.ql": "@provide syms {SYM\n@}\npartbody\n",
			},
			main:   "main.ql",
			stream: false,
		},
		{
			name: "dynamic_include_of_slot_partial_buffers",
			tmpls: map[string]string{
				"main.ql": "@set which = \"part.ql\"\nshell:\n@yield syms\n@include which\nend\n",
				"part.ql": "@provide syms {SYM\n@}\npartbody\n",
			},
			main:   "main.ql",
			stream: false,
		},
		{
			name: "dynamic_include_slot_free_buffers_conservatively",
			tmpls: map[string]string{
				"main.ql": "@set which = \"part.ql\"\ntop\n@include which\nbottom\n",
				"part.ql": "plain partial\n",
			},
			main:   "main.ql",
			stream: false,
		},
		{
			name: "embed_of_yield_template_buffers",
			tmpls: map[string]string{
				"main.ql": "before\n@embed \"box.ql\" {\n@block title {\nOverridden\n@}\n@}\nafter\n",
				"box.ql": "@block title {\nDefault\n@}\ntags:\n@yield tags\n" +
					"@provide tags {\nalpha\n@}\n",
			},
			main:   "main.ql",
			stream: false,
		},
		{
			name: "extends_parent_yields_buffers",
			tmpls: map[string]string{
				"child.ql": "@extends \"parent.ql\"\n@block main {\nCHILD\n@}\n",
				"parent.ql": "P:\n@yield hdr\n@block main {\nBASE\n@}\n" +
					"@provide hdr {\nH\n@}\n",
			},
			main:   "child.ql",
			stream: false,
		},
		{
			name: "extends_slot_free_streams",
			tmpls: map[string]string{
				"child.ql":  "@extends \"parent.ql\"\n@block main {\nCHILD body line\nsecond line\n@}\n",
				"parent.ql": "open\n@block main {\nBASE\n@}\nclose\n",
			},
			main:   "child.ql",
			stream: true,
		},
		{
			name: "imported_macro_yields_buffers",
			tmpls: map[string]string{
				"main.ql": "@import \"macros.ql\" as m\n{{ m.header() }}\n" +
					"@provide hdr {\nH\n@}\n",
				"macros.ql": "@macro header() {\nhdr:\n@yield hdr\n@}\n",
			},
			main:   "main.ql",
			stream: false,
		},
		{
			name: "imported_macro_slot_free_streams",
			tmpls: map[string]string{
				"main.ql":   "@import \"macros.ql\" as m\nA {{ m.header() }}\nB tail line\n",
				"macros.ql": "@macro header() {\n[HDR]\n@}\n",
			},
			main:   "main.ql",
			stream: true,
		},
		{
			name: "from_import_macro_yields_buffers",
			tmpls: map[string]string{
				"main.ql": "@from \"macros.ql\" import header\n{{ header() }}\n" +
					"@provide hdr {\nH\n@}\n",
				"macros.ql": "@macro header() {\nhdr:\n@yield hdr\n@}\n",
			},
			main:   "main.ql",
			stream: false,
		},
		{
			name: "use_trait_with_yield_buffers",
			tmpls: map[string]string{
				"main.ql": "@use \"trait.ql\"\nS:\n@block banner {\n{{ parent() }}\n@}\n" +
					"@provide b {\nB\n@}\n",
				"trait.ql": "@block banner {\nbanner:\n@yield b\n@}\n",
			},
			main:   "main.ql",
			stream: false,
		},
		{
			name: "block_other_template_with_yield_buffers",
			tmpls: map[string]string{
				"main.ql":  "X{{ block(\"banner\", \"other.ql\") }}Y\n@provide b {\nB\n@}\n",
				"other.ql": "@block banner {\nbanner:\n@yield b\n@}\n",
			},
			main:   "main.ql",
			stream: false,
		},
		{
			name: "block_other_template_slot_free_streams",
			tmpls: map[string]string{
				"main.ql":  "X {{ block(\"banner\", \"other.ql\") }}\nY tail line\n",
				"other.ql": "@block banner {\n[BANNER]\n@}\n",
			},
			main:   "main.ql",
			stream: true,
		},
		{
			name: "ignore_missing_include_still_streams",
			tmpls: map[string]string{
				"main.ql": "top line\n@include \"gone.ql\" ignore missing\nbottom line\n",
			},
			main:   "main.ql",
			stream: true,
		},
		{
			name: "candidate_list_all_slot_free_streams",
			tmpls: map[string]string{
				"main.ql": "top line\n@include [\"gone.ql\", \"part.ql\"]\nbottom line\n",
				"part.ql": "partial body\n",
			},
			main:   "main.ql",
			stream: true,
		},
		{
			name: "candidate_list_with_slot_candidate_buffers",
			tmpls: map[string]string{
				"main.ql": "top line\n@include [\"slotty.ql\", \"part.ql\"]\nbottom line\n",
				// The picked candidate uses a self-contained provide+yield pair, so
				// the walk must see it through the candidate list.
				"slotty.ql": "@provide s {\nS\n@}\nyielded:\n@yield s\n",
				"part.ql":   "partial body\n",
			},
			main:   "main.ql",
			stream: false,
		},
		{
			name: "tab_region_streams",
			tmpls: map[string]string{
				"main.ql": "root\n@tab(1) {\nindented {{ v }}\nlines here\n@}\ndone\n",
			},
			main:   "main.ql",
			vars:   map[string]runtime.Value{"v": runtime.Int(7)},
			stream: true,
		},
	}
}

// TestRenderToStreamingBattery drives every battery case through Render and
// RenderTo, asserting byte-identical output, no leaked placeholder, and that
// the streaming gate picked the expected path (streaming pushes many writes;
// the buffered fallback writes exactly once).
func TestRenderToStreamingBattery(t *testing.T) {
	for _, tc := range batteryCases() {
		t.Run(tc.name, func(t *testing.T) {
			want, err := New(loader.NewArrayLoader(tc.tmpls)).Render(tc.main, tc.vars)
			if err != nil {
				t.Fatalf("Render error: %v", err)
			}
			var cw countingWriter
			if err := New(loader.NewArrayLoader(tc.tmpls)).RenderTo(&cw, tc.main, tc.vars); err != nil {
				t.Fatalf("RenderTo error: %v", err)
			}
			if bytes.Contains(cw.buf.Bytes(), []byte(slotPlaceholderMark)) {
				t.Fatalf("raw slot placeholder leaked: %q", cw.buf.String())
			}
			if cw.buf.String() != want {
				t.Fatalf("output mismatch\n--- Render ----\n%q\n--- RenderTo ---\n%q", want, cw.buf.String())
			}
			if tc.stream && cw.writes <= 1 {
				t.Errorf("expected the streaming path (many writes), saw %d write(s)", cw.writes)
			}
			if !tc.stream && cw.writes != 1 {
				t.Errorf("expected the buffered fallback (exactly one write), saw %d write(s)", cw.writes)
			}
		})
	}
}

// TestRenderStringToSelfNameShadow pins the ad-hoc-name-collision hole: an
// ad-hoc body rendered under a name that ALSO exists in the loader, whose body
// statically includes that same name. The render-time include loads the
// LOADER's version (which uses slots), so the closure walk must too -- seeding
// the walk's visited set with the root's own name would skip it, stream, and
// leak a raw placeholder.
func TestRenderStringToSelfNameShadow(t *testing.T) {
	tmpls := map[string]string{
		"part.ql": "@provide s {\nS\n@}\nyielded:\n@yield s\n",
	}
	body := "top\n@include \"part.ql\"\nbottom\n"

	want, err := New(loader.NewArrayLoader(tmpls)).RenderString("part.ql", body, nil)
	if err != nil {
		t.Fatalf("RenderString error: %v", err)
	}
	var cw countingWriter
	if err := New(loader.NewArrayLoader(tmpls)).RenderStringTo(&cw, "part.ql", body, nil); err != nil {
		t.Fatalf("RenderStringTo error: %v", err)
	}
	if bytes.Contains(cw.buf.Bytes(), []byte(slotPlaceholderMark)) {
		t.Fatalf("raw slot placeholder leaked: %q", cw.buf.String())
	}
	if cw.buf.String() != want {
		t.Fatalf("output mismatch\n--- RenderString ----\n%q\n--- RenderStringTo ---\n%q", want, cw.buf.String())
	}
	if cw.writes != 1 {
		t.Errorf("expected the buffered fallback (the include target uses slots), saw %d write(s)", cw.writes)
	}
}

// TestRenderToFlush proves @flush drains a bufio.Writer mid-render on the
// streaming path: the bytes before the @flush must be visible in the
// destination when the statement runs, without waiting for the caller's final
// flush, and the total output must still match Render.
func TestRenderToFlush(t *testing.T) {
	tmpls := map[string]string{
		"main.ql": "before-marker\n@flush\nafter-marker\n",
	}
	want, err := New(loader.NewArrayLoader(tmpls)).Render("main.ql", nil)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}

	var dest bytes.Buffer
	bw := bufio.NewWriterSize(&dest, 1<<16)
	if err := New(loader.NewArrayLoader(tmpls)).RenderTo(bw, "main.ql", nil); err != nil {
		t.Fatalf("RenderTo error: %v", err)
	}
	// The buffer is far larger than the output, so anything visible in dest
	// before the caller's final flush got there through @flush.
	if got := dest.String(); got != "before-marker\n" {
		t.Fatalf("@flush did not drain the prefix: destination holds %q", got)
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("final flush: %v", err)
	}
	if dest.String() != want {
		t.Fatalf("output mismatch\n--- Render ----\n%q\n--- RenderTo ---\n%q", want, dest.String())
	}
}

// TestRenderToWriteErrorStopsRender proves a destination failure surfaces as
// the RenderTo error and stops the walk instead of rendering on into a dead
// writer.
func TestRenderToWriteErrorStopsRender(t *testing.T) {
	tmpls := map[string]string{
		"main.ql": "@for i in 1..10000 {\nrow {{ i }}\n@}",
	}
	w := &failAfterWriter{n: 64}
	err := New(loader.NewArrayLoader(tmpls)).RenderTo(w, "main.ql", nil)
	if err == nil {
		t.Fatal("expected a write error, got nil")
	}
	if !strings.Contains(err.Error(), "destination failed") {
		t.Fatalf("expected the destination error, got: %v", err)
	}
}

// TestRenderToErrorMatchesRender proves a mid-render template error surfaces
// identically through Render and RenderTo on both gate paths, and that the
// buffered fallback writes nothing on error.
func TestRenderToErrorMatchesRender(t *testing.T) {
	cases := []streamCase{
		{
			name:  "streaming_path_error",
			tmpls: map[string]string{"main.ql": "ok {{ missing_var }} tail"},
			main:  "main.ql",
		},
		{
			name: "buffered_path_error",
			tmpls: map[string]string{
				"main.ql": "@provide a {\nx\n@}\n@yield a\n{{ missing_var }}",
			},
			main: "main.ql",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, rerr := New(loader.NewArrayLoader(tc.tmpls)).Render(tc.main, nil)
			if rerr == nil {
				t.Fatal("expected a Render error")
			}
			var cw countingWriter
			terr := New(loader.NewArrayLoader(tc.tmpls)).RenderTo(&cw, tc.main, nil)
			if terr == nil {
				t.Fatal("expected a RenderTo error")
			}
			if rerr.Error() != terr.Error() {
				t.Fatalf("error mismatch\nRender:   %v\nRenderTo: %v", rerr, terr)
			}
			if tc.name == "buffered_path_error" && cw.writes != 0 {
				t.Fatalf("buffered fallback wrote %d time(s) on error; want none", cw.writes)
			}
		})
	}
}

// TestRenderPartialOnErrorShape pins Render's documented error shape after the
// renderBuffered refactor: on a mid-render error the partial buffer is
// returned alongside the error.
func TestRenderPartialOnErrorShape(t *testing.T) {
	tmpls := map[string]string{"main.ql": "prefix-{{ missing_var }}"}
	got, err := New(loader.NewArrayLoader(tmpls)).Render("main.ql", nil)
	if err == nil {
		t.Fatal("expected a render error")
	}
	if got != "prefix-" {
		t.Fatalf("partial-on-error shape changed: got %q, want %q", got, "prefix-")
	}
}

// TestRenderToValuesAndStringToAndDisplay covers the remaining facade entries:
// the native-bindings form, the ad-hoc body form, and Display, each equivalent
// to its buffered sibling.
func TestRenderToValuesAndStringToAndDisplay(t *testing.T) {
	tmpls := map[string]string{"main.ql": "sum: {{ a + b }}\n"}
	env := New(loader.NewArrayLoader(tmpls))
	want, err := env.RenderValues("main.ql", map[string]any{"a": 2, "b": 40})
	if err != nil {
		t.Fatalf("RenderValues error: %v", err)
	}
	var buf bytes.Buffer
	if err := New(loader.NewArrayLoader(tmpls)).RenderToValues(&buf, "main.ql", map[string]any{"a": 2, "b": 40}); err != nil {
		t.Fatalf("RenderToValues error: %v", err)
	}
	if buf.String() != want {
		t.Fatalf("RenderToValues mismatch: %q vs %q", buf.String(), want)
	}

	body := "inline {{ x * 2 }}\n"
	wantStr, err := env.RenderString("inline.ql", body, map[string]runtime.Value{"x": runtime.Int(21)})
	if err != nil {
		t.Fatalf("RenderString error: %v", err)
	}
	buf.Reset()
	if err := New(loader.NewArrayLoader(tmpls)).RenderStringTo(&buf, "inline.ql", body, map[string]runtime.Value{"x": runtime.Int(21)}); err != nil {
		t.Fatalf("RenderStringTo error: %v", err)
	}
	if buf.String() != wantStr {
		t.Fatalf("RenderStringTo mismatch: %q vs %q", buf.String(), wantStr)
	}

	buf.Reset()
	if err := New(loader.NewArrayLoader(tmpls)).RenderTo(&buf, "main.ql",
		map[string]runtime.Value{"a": runtime.Int(1), "b": runtime.Int(2)}); err != nil {
		t.Fatalf("RenderTo error: %v", err)
	}
	if buf.String() != "sum: 3\n" {
		t.Fatalf("RenderTo mismatch: %q", buf.String())
	}
}

// TestRenderToLargeOutputEquivalence stress-checks a megabyte-scale streaming
// render byte-for-byte against Render, covering builder-growth and chunking
// edges a small template cannot.
func TestRenderToLargeOutputEquivalence(t *testing.T) {
	tmpls := map[string]string{
		"main.ql": "@for i in 1..2000 {\nrow {{ i }}: {{ payload }}\n@}",
	}
	vars := map[string]runtime.Value{"payload": runtime.Str(strings.Repeat("x", 600))}

	want, err := New(loader.NewArrayLoader(tmpls)).Render("main.ql", vars)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	var buf bytes.Buffer
	if err := New(loader.NewArrayLoader(tmpls)).RenderTo(&buf, "main.ql", vars); err != nil {
		t.Fatalf("RenderTo error: %v", err)
	}
	if buf.String() != want {
		t.Fatal("large-output streaming render diverged from Render")
	}
	if len(want) < 1_000_000 {
		t.Fatalf("stress template too small to be meaningful: %d bytes", len(want))
	}
}

// TestRenderToStringWriterFastPath proves the io.StringWriter fast path (a
// bufio.Writer destination) produces the same bytes as the []byte fallback.
func TestRenderToStringWriterFastPath(t *testing.T) {
	tmpls := map[string]string{"main.ql": "plain {{ v }} output\nsecond line\n"}
	vars := map[string]runtime.Value{"v": runtime.Int(9)}
	want, err := New(loader.NewArrayLoader(tmpls)).Render("main.ql", vars)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	var dest bytes.Buffer
	bw := bufio.NewWriter(&dest)
	if err := New(loader.NewArrayLoader(tmpls)).RenderTo(bw, "main.ql", vars); err != nil {
		t.Fatalf("RenderTo error: %v", err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if dest.String() != want {
		t.Fatalf("StringWriter path mismatch: %q vs %q", dest.String(), want)
	}
	var _ io.Writer = bw // bufio.Writer implements io.StringWriter; pin the intent
}

// TestRenderToSandboxActive proves the streaming path honors a globally active
// sandbox exactly as Render does: an allowed render matches byte-for-byte and
// a denied one fails identically through both entries.
func TestRenderToSandboxActive(t *testing.T) {
	tmpls := map[string]string{"main.ql": "V: {{ v | upper }}\nW: {{ v | lower }}\nsecond line\n"}
	vars := map[string]runtime.Value{"v": runtime.Str("Ok")}

	run := func(filters []string) (string, error, string, error) {
		mk := func() *Environment {
			pol := buildPolicy(&sandboxConfig{Filters: filters}, false)
			return New(loader.NewArrayLoader(tmpls),
				WithSandboxPolicy(pol), WithSandboxActive(true))
		}
		got, rerr := mk().Render("main.ql", vars)
		var buf bytes.Buffer
		terr := mk().RenderTo(&buf, "main.ql", vars)
		return got, rerr, buf.String(), terr
	}

	got, rerr, streamed, terr := run([]string{"upper", "lower"})
	if rerr != nil || terr != nil {
		t.Fatalf("allowed render failed: Render err=%v RenderTo err=%v", rerr, terr)
	}
	if streamed != got {
		t.Fatalf("sandbox output mismatch: %q vs %q", streamed, got)
	}

	_, rerr, _, terr = run([]string{"upper"})
	if rerr == nil || terr == nil {
		t.Fatalf("denied render succeeded: Render err=%v RenderTo err=%v", rerr, terr)
	}
	if rerr.Error() != terr.Error() {
		t.Fatalf("sandbox deny error mismatch: %v vs %v", rerr, terr)
	}
}
