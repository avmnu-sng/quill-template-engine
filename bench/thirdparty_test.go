//go:build thirdparty

// This file is compiled only under the "thirdparty" build tag, so the default
// benchmark run stays offline with zero external dependencies. The peers here
// (pongo2, stick) are Twig/Jinja-family template engines implemented in Go; the
// other two peer files add jet (jet_test.go, interpreted) and quicktemplate
// (quicktemplate_test.go, qtc-compiled). All run in the SAME Go runtime, so
// timing against them is as fair as timing against text/template; but their
// FEATURE models differ from Quill, so treat these as a same-runtime peer
// comparison, not a like-for-like language comparison.
//
// To run them, fetch the peers into this module first, then pass the tag:
//
//	cd bench
//	go get github.com/flosch/pongo2/v6@v6.1.0 github.com/tyler-sommer/stick@v1.0.10 \
//	       github.com/CloudyKit/jet/v6@v6.2.0 github.com/valyala/quicktemplate@latest
//	go test -tags thirdparty -bench=. -benchmem
//
// `go get` writes the peer requirements into bench/go.mod and bench/go.sum; the
// default (untagged) build ignores them.

package quillbench

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	texttmpl "text/template"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"

	jet "github.com/CloudyKit/jet/v6"
	pongo2 "github.com/flosch/pongo2/v6"
	"github.com/tyler-sommer/stick"
	sticktwig "github.com/tyler-sommer/stick/twig"

	"quillbench/qtpl"
)

// ---- pongo2 templates (Django/Jinja syntax) ----
//
// The loop body is on a single physical line with the row-terminating newline
// INSIDE the loop (before {% endfor %}), so the full output is byte-identical to
// the Quill @for loop: "1. USER ...>\n2. USER ...>\n" with no leading blank line
// and no blank line between rows. A multi-line {% for %} body would leak the
// source newlines around the tags (Twig/Jinja preserve them), which is why the
// body is kept on one line here.

const pongoTiny = `Hello {{ name|upper }}!`

const pongoLoop = `{% for u in users %}{{ forloop.Counter }}. {{ u.Name|upper }} <{{ u.Email }}>
{% endfor %}`

// ---- stick templates (Twig syntax) ----

const stickTiny = `Hello {{ name|upper }}!`

const stickLoop = `{% for u in users %}{{ loop.index }}. {{ u.Name|upper }} <{{ u.Email }}>
{% endfor %}`

// TestVerifyThirdparty asserts the ENTIRE rendered loop output is byte-identical
// across ALL EIGHT engines the benchmarks compare: Quill (interpreter), Quill
// (real compile backend), text/template, and the four peers pongo2, stick, jet,
// and quicktemplate. html/template is intentionally excluded here because it
// contextually escapes '<' to "&lt;"; that engine's normalized equivalence is
// covered by the offline TestVerifyOutputs. The point of this check is that every
// timed engine produces exactly the same bytes, so the benchmarks compare
// equivalent work rather than accidentally divergent output.
//
// pongo2 and jet default to HTML-autoescaping, but the loop's angle brackets are
// TEMPLATE LITERALS (not interpolated data) and the interpolated values (name,
// email) contain no HTML-special characters, so no escaping fires and the raw
// output already matches byte for byte -- no normalization is needed.
func TestVerifyThirdparty(t *testing.T) {
	want := loopWant(loopN)

	// ----- Quill interpreter -----
	env := quill.NewFromMap(map[string]string{"loop.ql": quillLoop})
	qInterp, err := env.Render(context.Background(), "loop.ql", map[string]runtime.Value{"users": quillUsers()})
	if err != nil {
		t.Fatal(err)
	}

	// ----- Quill real compile backend -----
	var qCompiled strings.Builder
	if err := RenderLoop(context.Background(), &qCompiled, ext.Core(), map[string]runtime.Value{"users": quillUsers()}, nil); err != nil {
		t.Fatal(err)
	}

	// ----- text/template -----
	tt := texttmpl.Must(texttmpl.New("loop").Funcs(stdFuncs).Parse(stdLoop))
	var textBuf strings.Builder
	if err := tt.Execute(&textBuf, struct{ Users []record }{Users: goRecords()}); err != nil {
		t.Fatal(err)
	}

	// ----- pongo2 -----
	pt := pongo2.Must(pongo2.FromString(pongoLoop))
	pOut, err := pt.Execute(pongo2.Context{"users": goRecords()})
	if err != nil {
		t.Fatal(err)
	}

	// ----- stick -----
	var sOut strings.Builder
	if err := stickEnv().Execute(stickLoop, &sOut, map[string]stick.Value{"users": goRecords()}); err != nil {
		t.Fatal(err)
	}

	// ----- jet -----
	jt, err := jetSet().GetTemplate("loop.jet")
	if err != nil {
		t.Fatal(err)
	}
	var jOut writerString
	if err := jt.Execute(&jOut, jet.VarMap{}.Set("users", goRecords()), nil); err != nil {
		t.Fatal(err)
	}

	// ----- quicktemplate (qtc-compiled) -----
	qtOut := qtpl.Loop(qtplRows(loopN))

	for name, got := range map[string]string{
		"quill-interp":   qInterp,
		"quill-compiled": qCompiled.String(),
		"text":           textBuf.String(),
		"pongo2":         pOut,
		"stick":          sOut.String(),
		"jet":            jOut.String(),
		"quicktemplate":  qtOut,
	} {
		if got != want {
			t.Errorf("%s full loop output mismatch\n got=%q\nwant=%q", name, got, want)
		}
	}
}

// ==================== pongo2 (clean load/render split) ====================

func BenchmarkPongo2_Tiny_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := pongo2.FromString(pongoTiny); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPongo2_Tiny_Render(b *testing.B) {
	t := pongo2.Must(pongo2.FromString(pongoTiny))
	ctx := pongo2.Context{"name": "ada"}
	out, err := t.Execute(ctx)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for b.Loop() {
		if err := t.ExecuteWriter(ctx, io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPongo2_Loop_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := pongo2.FromString(pongoLoop); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPongo2_Loop_Render sweeps the loop row count so pongo2's scaling lines
// up column-for-column with the other engines' n=1..1000 sub-benchmarks.
func BenchmarkPongo2_Loop_Render(b *testing.B) {
	t := pongo2.Must(pongo2.FromString(pongoLoop))
	for _, n := range loopSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ctx := pongo2.Context{"users": records(n)}
			out, err := t.Execute(ctx)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			for b.Loop() {
				if err := t.ExecuteWriter(ctx, io.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ==================== stick (parse+render coupled in Execute) ====================
//
// stick.Env.Execute takes a template STRING and parses it on every call: its
// public API does not expose a "compile once, render many" path. So the stick
// numbers are a COMBINED load+render figure, reported under the Render column and
// flagged as combined in the comparison doc. twig.New wires the Twig filter set
// (upper, length, ...) onto the env.

func stickEnv() *stick.Env { return sticktwig.New(nil) }

func BenchmarkStick_Tiny_LoadRender(b *testing.B) {
	env := stickEnv()
	ctx := map[string]stick.Value{"name": "ada"}
	var buf strings.Builder
	if err := env.Execute(stickTiny, &buf, ctx); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(buf.Len()))
	b.ReportAllocs()
	for b.Loop() {
		if err := env.Execute(stickTiny, io.Discard, ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStick_Loop_LoadRender sweeps the loop row count. stick re-parses the
// template STRING on every Execute (no compile-once API), so these are COMBINED
// load+render figures; the size sweep still lines up column-for-column with the
// other engines' n=1..1000 sub-benchmarks.
func BenchmarkStick_Loop_LoadRender(b *testing.B) {
	env := stickEnv()
	for _, n := range loopSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ctx := map[string]stick.Value{"users": records(n)}
			var buf strings.Builder
			if err := env.Execute(stickLoop, &buf, ctx); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(buf.Len()))
			b.ReportAllocs()
			for b.Loop() {
				if err := env.Execute(stickLoop, io.Discard, ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
