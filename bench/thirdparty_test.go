//go:build thirdparty

// This file is compiled only under the "thirdparty" build tag, so the default
// benchmark run stays offline with zero external dependencies. The peers here
// (pongo2, stick) are Twig/Jinja-family template engines implemented in Go. They
// run in the SAME Go runtime, so timing against them is as fair as timing against
// text/template; but their FEATURE model (Twig/Jinja semantics, HTML autoescape
// defaults, dynamic-only) differs from Quill, so treat these as a same-runtime
// peer comparison, not a like-for-like language comparison.
//
// To run them, fetch the peers into this module first, then pass the tag:
//
//	cd bench
//	go get github.com/flosch/pongo2/v6@v6.1.0 github.com/tyler-sommer/stick@v1.0.10
//	go test -tags thirdparty -bench=. -benchmem
//
// `go get` writes the peer requirements into bench/go.mod and bench/go.sum; the
// default (untagged) build ignores them.

package quillbench

import (
	"io"
	"strings"
	"testing"

	pongo2 "github.com/flosch/pongo2/v6"
	"github.com/tyler-sommer/stick"
	sticktwig "github.com/tyler-sommer/stick/twig"
)

// ---- pongo2 templates (Django/Jinja syntax) ----

const pongoTiny = `Hello {{ name|upper }}!`

const pongoLoop = `{% for u in users %}
{{ forloop.Counter }}. {{ u.Name|upper }} <{{ u.Email }}>
{% endfor %}`

// ---- stick templates (Twig syntax) ----

const stickTiny = `Hello {{ name|upper }}!`

const stickLoop = `{% for u in users %}
{{ loop.index }}. {{ u.Name|upper }} <{{ u.Email }}>
{% endfor %}`

// TestVerifyThirdparty asserts the third-party peers render the same first loop
// data line as the offline engines, so their benchmarks time equivalent work.
func TestVerifyThirdparty(t *testing.T) {
	pt := pongo2.Must(pongo2.FromString(pongoLoop))
	pout, err := pt.Execute(pongo2.Context{"users": goRecords()})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := stickEnv().Execute(stickLoop, &sb, map[string]stick.Value{"users": goRecords()}); err != nil {
		t.Fatal(err)
	}
	want := "1. USER <user@example.com>"
	norm := func(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "&lt;", "<") }
	for name, got := range map[string]string{
		"pongo": firstDataLine(pout),
		"stick": firstDataLine(sb.String()),
	} {
		if norm(got) != want {
			t.Errorf("%s first data line = %q, want %q (normalized)", name, got, want)
		}
	}
}

// ==================== pongo2 (clean load/render split) ====================

func BenchmarkPongo2_Tiny_Load(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := pongo2.FromString(pongoTiny); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPongo2_Tiny_Render(b *testing.B) {
	t := pongo2.Must(pongo2.FromString(pongoTiny))
	ctx := pongo2.Context{"name": "ada"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := t.ExecuteWriter(ctx, io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPongo2_Loop_Load(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := pongo2.FromString(pongoLoop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPongo2_Loop_Render(b *testing.B) {
	t := pongo2.Must(pongo2.FromString(pongoLoop))
	ctx := pongo2.Context{"users": goRecords()}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := t.ExecuteWriter(ctx, io.Discard); err != nil {
			b.Fatal(err)
		}
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
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := env.Execute(stickTiny, io.Discard, ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStick_Loop_LoadRender(b *testing.B) {
	env := stickEnv()
	ctx := map[string]stick.Value{"users": goRecords()}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := env.Execute(stickLoop, io.Discard, ctx); err != nil {
			b.Fatal(err)
		}
	}
}
