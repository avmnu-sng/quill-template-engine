package quillbench

import (
	"bytes"
	"fmt"
	htmltmpl "html/template"
	"io"
	"strings"
	"testing"
	texttmpl "text/template"
)

// ---- Equivalent stdlib templates ----
//
// text/template and html/template share the same source. "upper" is supplied as
// a FuncMap entry mapping to strings.ToUpper, the closest equivalent to Quill's
// upper filter.

const stdTiny = `Hello {{ upper .Name }}!`

// {{$i}} is 1-based via add, matching Quill loop.index (1-based). The trim
// markers frame the loop so the FULL output is byte-identical to Quill's: the
// leading newline after {{range}} is trimmed with -}} and each row is terminated
// by its own newline, giving "1. USER ...>\n2. USER ...>\n" with no leading blank
// line and a trailing newline, exactly as the Quill @for template produces. This
// is what lets TestVerifyOutputs assert byte-for-byte equality across engines.
const stdLoop = `{{range $i, $u := .Users -}}
{{add $i 1}}. {{upper $u.Name}} <{{$u.Email}}>
{{end -}}`

// Composition: the stdlib idiom is a base template with overridable blocks,
// with the child associated in the same template set. This mirrors the Quill
// @extends/@block/parent shape: a title, a summary block (that pulls the base
// default via nesting), and an items block that loops.
const stdBase = `# {{.Title}}

{{block "summary" .}}(no summary){{end}}
{{block "items" .}}(no items){{end}}
`

const stdPage = `{{define "summary"}}(no summary)
A short report with {{len .Items}} items.
{{end}}{{define "items"}}{{range .Items}}
- {{.}}
{{- end}}
{{end}}`

var stdFuncs = map[string]any{
	"upper": strings.ToUpper,
	"add":   func(a, b int) int { return a + b },
}

// composeData carries the fields the stdlib composition templates read.
type composeData struct {
	Title string
	Items []string
}

func stdItems() []string {
	its := make([]string, loopN)
	for i := range its {
		its[i] = "item"
	}
	return its
}

// ==================== text/template ====================

func BenchmarkText_Tiny_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := texttmpl.New("tiny").Funcs(stdFuncs).Parse(stdTiny); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkText_Tiny_Render(b *testing.B) {
	t := texttmpl.Must(texttmpl.New("tiny").Funcs(stdFuncs).Parse(stdTiny))
	data := record{Name: "ada"}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(buf.Len()))
	b.ReportAllocs()
	for b.Loop() {
		if err := t.Execute(io.Discard, data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkText_Loop_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := texttmpl.New("loop").Funcs(stdFuncs).Parse(stdLoop); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkText_Loop_Render sweeps the loop row count so text/template's scaling
// lines up column-for-column with the other engines' n=1..1000 sub-benchmarks.
func BenchmarkText_Loop_Render(b *testing.B) {
	t := texttmpl.Must(texttmpl.New("loop").Funcs(stdFuncs).Parse(stdLoop))
	for _, n := range loopSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			data := struct{ Users []record }{Users: records(n)}
			var buf bytes.Buffer
			if err := t.Execute(&buf, data); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(buf.Len()))
			b.ReportAllocs()
			for b.Loop() {
				if err := t.Execute(io.Discard, data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkText_Compose_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		root := texttmpl.Must(texttmpl.New("base").Funcs(stdFuncs).Parse(stdBase))
		if _, err := root.Parse(stdPage); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkText_Compose_Render(b *testing.B) {
	root := texttmpl.Must(texttmpl.New("base").Funcs(stdFuncs).Parse(stdBase))
	texttmpl.Must(root.Parse(stdPage))
	data := composeData{Title: "Daily Report", Items: stdItems()}
	var buf bytes.Buffer
	if err := root.Execute(&buf, data); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(buf.Len()))
	b.ReportAllocs()
	for b.Loop() {
		if err := root.Execute(io.Discard, data); err != nil {
			b.Fatal(err)
		}
	}
}

// ==================== html/template ====================

func BenchmarkHTML_Tiny_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := htmltmpl.New("tiny").Funcs(stdFuncs).Parse(stdTiny); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHTML_Tiny_Render(b *testing.B) {
	t := htmltmpl.Must(htmltmpl.New("tiny").Funcs(stdFuncs).Parse(stdTiny))
	data := record{Name: "ada"}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(buf.Len()))
	b.ReportAllocs()
	for b.Loop() {
		if err := t.Execute(io.Discard, data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHTML_Loop_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := htmltmpl.New("loop").Funcs(stdFuncs).Parse(stdLoop); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHTML_Loop_Render sweeps the loop row count. html/template contextually
// escapes '<' in the email angle brackets; the inputs are otherwise escape-free,
// so the timed work matches the other engines byte-for-byte after the documented
// &lt; normalization (see TestVerifyOutputs).
func BenchmarkHTML_Loop_Render(b *testing.B) {
	t := htmltmpl.Must(htmltmpl.New("loop").Funcs(stdFuncs).Parse(stdLoop))
	for _, n := range loopSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			data := struct{ Users []record }{Users: records(n)}
			var buf bytes.Buffer
			if err := t.Execute(&buf, data); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(buf.Len()))
			b.ReportAllocs()
			for b.Loop() {
				if err := t.Execute(io.Discard, data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkHTML_Compose_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		root := htmltmpl.Must(htmltmpl.New("base").Funcs(stdFuncs).Parse(stdBase))
		if _, err := root.Parse(stdPage); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHTML_Compose_Render(b *testing.B) {
	root := htmltmpl.Must(htmltmpl.New("base").Funcs(stdFuncs).Parse(stdBase))
	htmltmpl.Must(root.Parse(stdPage))
	data := composeData{Title: "Daily Report", Items: stdItems()}
	var buf bytes.Buffer
	if err := root.Execute(&buf, data); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(buf.Len()))
	b.ReportAllocs()
	for b.Loop() {
		if err := root.Execute(io.Discard, data); err != nil {
			b.Fatal(err)
		}
	}
}
