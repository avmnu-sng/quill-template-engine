//go:build thirdparty

// This file is compiled only under the "thirdparty" build tag, so the default
// benchmark run stays offline with zero external dependencies. jet is an
// interpreted Go template engine (Django/Jinja-family surface, but its own
// syntax) that supports blocks and ranges. It runs in the SAME Go runtime, so
// timing against it is as fair as timing against text/template; its feature
// model differs from Quill, so treat it as a same-runtime peer comparison.
//
// jet is fetched into this module with:
//
//	cd bench
//	go get github.com/CloudyKit/jet/v6@v6.2.0
//	go test -tags thirdparty -bench=. -benchmem
//
// The default (untagged) build ignores it.

package quillbench

import (
	"fmt"
	"io"
	"testing"

	jet "github.com/CloudyKit/jet/v6"
)

// ---- jet templates ----
//
// jet's index (the `i` in `range i := s`) is 0-based, so `i + 1` reproduces the
// 1-based loop counter the other engines emit. The trim markers ({{- ... -}})
// frame the loop so the FULL output is byte-identical to Quill's: no leading
// blank line, and a trailing newline per row. jet exposes `upper` as a builtin,
// used here via the pipe form to mirror Quill's `| upper`.

const jetTiny = `Hello {{ name | upper }}!`

const jetLoop = `{{- range i := users -}}
{{ i + 1 }}. {{ .Name | upper }} <{{ .Email }}>
{{ end -}}`

// jetSet builds a jet Set backed by an in-memory loader holding the two
// benchmark templates. Development mode is left off so parsed templates are
// cached, which is the fair "loaded once, render many" configuration.
func jetSet() *jet.Set {
	loader := jet.NewInMemLoader()
	loader.Set("tiny.jet", jetTiny)
	loader.Set("loop.jet", jetLoop)
	return jet.NewSet(loader)
}

// jetRenderLoop renders the jet loop template for n rows and returns the output,
// used by the fairness check and to size SetBytes.
func jetRenderLoop(n int) (string, error) {
	t, err := jetSet().GetTemplate("loop.jet")
	if err != nil {
		return "", err
	}
	var sb writerString
	if err := t.Execute(&sb, jet.VarMap{}.Set("users", records(n)), nil); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// writerString is a minimal io.Writer capturing bytes into a string, avoiding a
// bytes/strings.Builder import churn in the fairness helper.
type writerString struct{ b []byte }

func (w *writerString) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }
func (w *writerString) String() string              { return string(w.b) }

// ==================== jet (clean load/render split) ====================

func BenchmarkJet_Tiny_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		set := jetSet()
		if _, err := set.GetTemplate("tiny.jet"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJet_Tiny_Render(b *testing.B) {
	t, err := jetSet().GetTemplate("tiny.jet")
	if err != nil {
		b.Fatal(err)
	}
	vars := jet.VarMap{}.Set("name", "ada")
	var buf writerString
	if err := t.Execute(&buf, vars, nil); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(buf.String())))
	b.ReportAllocs()
	for b.Loop() {
		if err := t.Execute(io.Discard, vars, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJet_Loop_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		set := jetSet()
		if _, err := set.GetTemplate("loop.jet"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkJet_Loop_Render sweeps the loop row count so jet's scaling lines up
// column-for-column with the other engines' n=1..1000 sub-benchmarks.
func BenchmarkJet_Loop_Render(b *testing.B) {
	t, err := jetSet().GetTemplate("loop.jet")
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range loopSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			vars := jet.VarMap{}.Set("users", records(n))
			var buf writerString
			if err := t.Execute(&buf, vars, nil); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(buf.String())))
			b.ReportAllocs()
			for b.Loop() {
				if err := t.Execute(io.Discard, vars, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
