package quillbench

// Phase 2a scenario benchmarks: five workloads that isolate a specific cost the
// Tiny/Loop/Compose battery does not surface: filter dispatch, nested-loop
// scope/metadata, if/elseif/else branching, HTML autoescape, and the
// streamed-vs-buffered render split. Each render benchmark follows the Phase 1
// discipline: `for b.Loop()`, b.ReportAllocs(), b.SetBytes(len(output)) on
// renders, and a result pinned to the package-level sink (or a byte count) so
// dead-code elimination cannot delete the timed work. The size-parameterized
// scenarios sweep scenarioSizes with b.Run("n=%d").
//
// Every offline engine that should agree is asserted byte-identical in
// TestVerifyScenarios; the third-party peers (pongo2, jet) that also render the
// filter and conditional workloads are asserted in the thirdparty-tagged
// TestVerifyScenariosThirdparty. The benchmarks therefore time provably
// identical work across engines.

import (
	"bytes"
	"context"
	"fmt"
	htmltmpl "html/template"
	"io"
	"strings"
	"testing"
	texttmpl "text/template"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// scenarioSizes are the row/group counts the data-driven scenarios (filter,
// nested, conditional, streaming) sweep over, matching the task's {10, 100,
// 1000}. The autoescape scenario is single-size (autoescapeN) because it
// measures a per-value escaping cost, not scaling.
var scenarioSizes = []int{10, 100, 1000}

// autoescapeN is the single row count the autoescape scenario renders: enough
// rows that the per-value escaping work dominates the fixed render overhead.
const autoescapeN = 100

// ============================================================================
// Scenario 1: filter pipeline that chains several stdlib filters on each value.
// ============================================================================
//
// Each row runs name through upper|trim|replace|default and joins a per-row tag
// list, so the timed work is filter-dispatch-bound rather than loop-bound. The
// text/template equivalent supplies a FuncMap whose funcs reproduce each Quill
// filter EXACTLY (see filterFuncs), so the full output is byte-identical.
//
// Quill's `default` fills only a Null value (pkg/ext/core.go filterDefault), and
// every row's name is present, so default is exercised as a pass-through on the
// dispatch path; its FuncMap twin has the same present-value semantics, keeping
// the outputs identical.

const quillFilter = `@for u in users {
{{ loop.index }}. {{ u.name | upper | trim | replace({"O": "0"}) | default("anon") }} [{{ u.tags | join(", ") }}]
@}`

// stdFilter mirrors quillFilter through text/template with a FuncMap. The pipe
// order matches Quill's left-to-right filter chain: upper then trim then replace
// then default. {{$i}} is 1-based via add, and the -}} trims frame the loop so
// the full output is byte-identical to Quill's @for.
const stdFilter = `{{range $i, $u := .Users -}}
{{add $i 1}}. {{def (repl (qtrim (upper $u.Name))) "anon"}} [{{join $u.Tags ", "}}]
{{end -}}`

// filterRepl reproduces Quill's replace({"O": "0"}) (strtr-style,
// strings.NewReplacer) so the FuncMap chain matches byte for byte.
var filterRepl = strings.NewReplacer("O", "0")

// qtrimMask is Quill's default trim character set (pkg/ext/core.go
// defaultTrimMask). text/template's trim func must use this exact mask, NOT
// strings.TrimSpace, or a value trimmed of a vertical tab / NUL would diverge.
const qtrimMask = " \t\n\r\x00\x0B"

// filterFuncs supplies the text/template equivalents of the Quill filters in
// quillFilter. Each func reproduces its Quill counterpart's exact semantics so
// the rendered output is byte-identical (asserted in TestVerifyScenarios).
var filterFuncs = map[string]any{
	"add":   func(a, b int) int { return a + b },
	"upper": strings.ToUpper,
	"qtrim": func(s string) string { return strings.Trim(s, qtrimMask) },
	"repl":  filterRepl.Replace,
	// def mirrors Quill's default: it fills only a value Quill would see as Null.
	// A present (non-empty) string passes through, matching filterDefault, which
	// keys off IsNull, not emptiness.
	"def": func(s, fallback string) string {
		if s == "" {
			return fallback
		}
		return s
	},
	"join": strings.Join,
}

// filterRow is the plain-Go row the stdlib filter template consumes. Name
// carries a mix of case, surrounding whitespace, and an 'O' so upper, trim, and
// replace all do observable work.
type filterRow struct {
	Name string
	Tags []string
}

// filterRows builds n identical filter rows for the stdlib engines.
func filterRows(n int) []filterRow {
	rs := make([]filterRow, n)
	for i := range rs {
		rs[i] = filterRow{Name: "  aiko  ", Tags: []string{"red", "green", "blue"}}
	}
	return rs
}

// quillFilterVars builds the Quill-native vars for the filter scenario: a list
// of map rows each with a name string and a tags list, mirroring filterRows.
func quillFilterVars(n int) map[string]runtime.Value {
	vals := make([]runtime.Value, n)
	for i := range vals {
		r := runtime.NewArray()
		r.SetStr("name", runtime.Str("  aiko  "))
		r.SetStr("tags", runtime.Arr(runtime.NewList(
			runtime.Str("red"), runtime.Str("green"), runtime.Str("blue"))))
		vals[i] = runtime.Arr(r)
	}
	return map[string]runtime.Value{"users": runtime.Arr(runtime.NewList(vals...))}
}

func BenchmarkQuill_Filter_Render(b *testing.B) {
	env := quill.NewFromMap(map[string]string{"filter.ql": quillFilter})
	tmpl, err := env.LoadTemplate(context.Background(), "filter.ql")
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			vars := quillFilterVars(n)
			out, err := env.RenderPrepared(context.Background(), tmpl, vars)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			for b.Loop() {
				if sink, err = env.RenderPrepared(context.Background(), tmpl, vars); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkText_Filter_Render(b *testing.B) {
	t := texttmpl.Must(texttmpl.New("filter").Funcs(filterFuncs).Parse(stdFilter))
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			data := struct{ Users []filterRow }{Users: filterRows(n)}
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

// ============================================================================
// Scenario 2: nested loops (a loop over groups, each with a loop over items).
// ============================================================================
//
// The 2D data exercises the nested-scope and loop-metadata cost the flat Loop
// workload cannot: an inner loop re-established per outer row, with the inner
// body reading both the outer group's fields and the inner item's. groupsPerN is
// the outer count; each group holds itemsPerGroup rows.

// itemsPerGroup is the fixed inner-loop length; the size sweep varies the OUTER
// group count, so total rows scale as n * itemsPerGroup.
const itemsPerGroup = 5

const quillNested = `@for g in groups {
# {{ g.name }} ({{ g.items | length }})
@for it in g.items {
{{ loop.index }}/{{ loop.length }}: {{ it.label }} = {{ it.qty }}
@}
@}`

// stdNested mirrors quillNested. The outer {{range}} binds $g so the inner loop
// can read the group's own length via {{len $g.Items}} (inside the inner range
// the dot is an item, and $ is the root, so the group must be captured in $g).
// The inner {{$j}} is 1-based via add. Trim markers frame both loops so the full
// output is byte-identical.
const stdNested = `{{range $g := .Groups -}}
# {{$g.Name}} ({{len $g.Items}})
{{range $j, $it := $g.Items -}}
  {{add $j 1}}/{{len $g.Items}}: {{$it.Label}} = {{$it.Qty}}
{{end -}}
{{end -}}`

// item is one inner row.
type item struct {
	Label string
	Qty   int
}

// group is one outer row carrying its inner item slice.
type group struct {
	Name  string
	Items []item
}

// groupsData builds n groups, each with itemsPerGroup items, for the stdlib
// engine.
func groupsData(n int) []group {
	gs := make([]group, n)
	for i := range gs {
		its := make([]item, itemsPerGroup)
		for j := range its {
			its[j] = item{Label: "widget", Qty: 7}
		}
		gs[i] = group{Name: "group", Items: its}
	}
	return gs
}

// quillNestedVars builds the Quill-native nested data mirroring groupsData.
func quillNestedVars(n int) map[string]runtime.Value {
	groups := make([]runtime.Value, n)
	for i := range groups {
		items := make([]runtime.Value, itemsPerGroup)
		for j := range items {
			it := runtime.NewArray()
			it.SetStr("label", runtime.Str("widget"))
			it.SetStr("qty", runtime.Int(7))
			items[j] = runtime.Arr(it)
		}
		g := runtime.NewArray()
		g.SetStr("name", runtime.Str("group"))
		g.SetStr("items", runtime.Arr(runtime.NewList(items...)))
		groups[i] = runtime.Arr(g)
	}
	return map[string]runtime.Value{"groups": runtime.Arr(runtime.NewList(groups...))}
}

func BenchmarkQuill_Nested_Render(b *testing.B) {
	env := quill.NewFromMap(map[string]string{"nested.ql": quillNested})
	tmpl, err := env.LoadTemplate(context.Background(), "nested.ql")
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			vars := quillNestedVars(n)
			out, err := env.RenderPrepared(context.Background(), tmpl, vars)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			for b.Loop() {
				if sink, err = env.RenderPrepared(context.Background(), tmpl, vars); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkText_Nested_Render(b *testing.B) {
	t := texttmpl.Must(texttmpl.New("nested").Funcs(filterFuncs).Parse(stdNested))
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			data := struct{ Groups []group }{Groups: groupsData(n)}
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

// ============================================================================
// Scenario 3: conditionals (several if/elseif/else branches per row).
// ============================================================================
//
// Each row's score selects one of four arms (A/B/C/D), so every iteration walks
// the branch chain to a different depth. The scores cycle so all arms are hit,
// exercising the whole chain rather than a single hot branch.

const quillCond = `@for u in users {
{{ loop.index }}:
@if u.score >= 90 {
A
@} elseif u.score >= 70 {
B
@} elseif u.score >= 50 {
C
@} else {
D
@}
@}`

// stdCond mirrors quillCond, which renders each row as "{index}:\n{ARM}\n" (the
// @if/@} block form consumes the newlines around each arm, leaving just the
// arm's own line). The stdlib template reproduces that shape: the counter and
// colon, a newline, the selected arm letter, and a trailing newline. The {{if}}
// arms are written with -}} / {{- trims so only the arm letter and its newline
// reach the output.
const stdCond = `{{range $i, $u := .Users -}}
{{add $i 1}}:
{{if ge $u.Score 90}}A
{{else if ge $u.Score 70}}B
{{else if ge $u.Score 50}}C
{{else}}D
{{end -}}
{{end -}}`

// condRow carries the score the branch chain switches on.
type condRow struct{ Score int }

// condScores cycles the four arms so every branch is exercised.
var condScores = []int{95, 75, 55, 20}

// condRows builds n rows whose scores cycle through condScores.
func condRows(n int) []condRow {
	rs := make([]condRow, n)
	for i := range rs {
		rs[i] = condRow{Score: condScores[i%len(condScores)]}
	}
	return rs
}

// quillCondVars builds the Quill-native conditional rows mirroring condRows.
func quillCondVars(n int) map[string]runtime.Value {
	vals := make([]runtime.Value, n)
	for i := range vals {
		r := runtime.NewArray()
		r.SetStr("score", runtime.Int(int64(condScores[i%len(condScores)])))
		vals[i] = runtime.Arr(r)
	}
	return map[string]runtime.Value{"users": runtime.Arr(runtime.NewList(vals...))}
}

func BenchmarkQuill_Cond_Render(b *testing.B) {
	env := quill.NewFromMap(map[string]string{"cond.ql": quillCond})
	tmpl, err := env.LoadTemplate(context.Background(), "cond.ql")
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			vars := quillCondVars(n)
			out, err := env.RenderPrepared(context.Background(), tmpl, vars)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			for b.Loop() {
				if sink, err = env.RenderPrepared(context.Background(), tmpl, vars); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkText_Cond_Render(b *testing.B) {
	t := texttmpl.Must(texttmpl.New("cond").Funcs(filterFuncs).Parse(stdCond))
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			data := struct{ Users []condRow }{Users: condRows(n)}
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

// ============================================================================
// Scenario 4: autoescape cost (same template, escape OFF vs ON).
// ============================================================================
//
// The input values carry HTML-special characters (<, >, &, ", ') so escaping
// does real work. The SAME quillEscape template is rendered by two Environments:
// one default (autoescape OFF, byte-identical to text/template) and one built
// with quill.WithAutoescapeHTML(true) (byte-identical to html/template). The
// fairness test asserts both equalities; the benchmarks report the escape ON/OFF
// cost side by side.

const quillEscape = `@for c in comments {
{{ loop.index }}. {{ c.author }} says: {{ c.body }}
@}`

// stdEscape mirrors quillEscape for text/template and html/template. The two
// stdlib packages share this source; html/template contextually escapes the
// interpolated values, text/template does not.
const stdEscape = `{{range $i, $c := .Comments -}}
{{add $i 1}}. {{$c.Author}} says: {{$c.Body}}
{{end -}}`

// comment carries author and body fields loaded with HTML-special characters so
// autoescape has observable work to do.
type comment struct {
	Author string
	Body   string
}

// escapeAuthor and escapeBody are the HTML-special input strings both the Quill
// and stdlib comment builders use. They contain <, >, &, and ' so an escaping
// engine transforms every field and a non-escaping engine leaves it verbatim.
//
// The raw double-quote '"' is deliberately excluded: Quill escapes it to
// "&quot;" while html/template escapes it to "&#34;". Both are valid,
// semantically identical HTML escapes of the same character, but they are BYTE-
// different, so including '"' would make the byte-identity assertion require a
// normalization. Every other HTML-special character (<, >, &, ') escapes to
// byte-identical output under both engines, so restricting the input to those
// keeps escaping doing real work while letting the fairness test assert exact
// byte equality without any normalization.
const (
	escapeAuthor = `A&B <script>`
	escapeBody   = `x < y && z > 'w'`
)

// escapeComments builds n comments carrying the HTML-special strings.
func escapeComments(n int) []comment {
	cs := make([]comment, n)
	for i := range cs {
		cs[i] = comment{Author: escapeAuthor, Body: escapeBody}
	}
	return cs
}

// quillEscapeVars builds the Quill-native comment rows mirroring escapeComments.
func quillEscapeVars(n int) map[string]runtime.Value {
	vals := make([]runtime.Value, n)
	for i := range vals {
		c := runtime.NewArray()
		c.SetStr("author", runtime.Str(escapeAuthor))
		c.SetStr("body", runtime.Str(escapeBody))
		vals[i] = runtime.Arr(c)
	}
	return map[string]runtime.Value{"comments": runtime.Arr(runtime.NewList(vals...))}
}

// BenchmarkQuill_Autoescape_Off_Render times the default (autoescape OFF) path:
// the HTML-special characters pass through verbatim, so this is the no-escape
// baseline the ON variant is compared against.
func BenchmarkQuill_Autoescape_Off_Render(b *testing.B) {
	env := quill.NewFromMap(map[string]string{"esc.ql": quillEscape})
	tmpl, err := env.LoadTemplate(context.Background(), "esc.ql")
	if err != nil {
		b.Fatal(err)
	}
	vars := quillEscapeVars(autoescapeN)
	out, err := env.RenderPrepared(context.Background(), tmpl, vars)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for b.Loop() {
		if sink, err = env.RenderPrepared(context.Background(), tmpl, vars); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQuill_Autoescape_On_Render times the same template through an
// Environment built with WithAutoescapeHTML(true): every interpolated value is
// HTML-escaped, so ns/op minus the OFF variant is Quill's per-render escaping
// cost.
func BenchmarkQuill_Autoescape_On_Render(b *testing.B) {
	env := quill.NewFromMap(map[string]string{"esc.ql": quillEscape},
		quill.WithAutoescapeHTML(true))
	tmpl, err := env.LoadTemplate(context.Background(), "esc.ql")
	if err != nil {
		b.Fatal(err)
	}
	vars := quillEscapeVars(autoescapeN)
	out, err := env.RenderPrepared(context.Background(), tmpl, vars)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for b.Loop() {
		if sink, err = env.RenderPrepared(context.Background(), tmpl, vars); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkText_Autoescape_Render is the no-escape stdlib baseline: text/template
// emits the HTML-special characters verbatim, the bar Quill-autoescape-OFF is
// compared against.
func BenchmarkText_Autoescape_Render(b *testing.B) {
	t := texttmpl.Must(texttmpl.New("esc").Funcs(filterFuncs).Parse(stdEscape))
	data := struct{ Comments []comment }{Comments: escapeComments(autoescapeN)}
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

// BenchmarkHTML_Autoescape_Render is the escaping stdlib baseline: html/template
// contextually escapes every interpolated value, the bar Quill-autoescape-ON is
// compared against.
func BenchmarkHTML_Autoescape_Render(b *testing.B) {
	t := htmltmpl.Must(htmltmpl.New("esc").Funcs(filterFuncs).Parse(stdEscape))
	data := struct{ Comments []comment }{Comments: escapeComments(autoescapeN)}
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

// ============================================================================
// Scenario 5: streaming vs buffered (RenderTo(io.Writer) vs Render(string)).
// ============================================================================
//
// The loop scenario is rendered two ways on the same Environment: env.Render,
// which buffers the whole output into a returned string, and env.RenderTo(context.Background(), w),
// which streams to an io.Writer (io.Discard here) with bounded memory because the
// loop template is slot-free. Reporting allocs for both exposes the buffered
// path's single big builder allocation against the streamed path's near-zero
// steady-state footprint. Both time identical rendered bytes (the scenario reuses
// the flat Loop workload, whose output is quillLoop's).

// BenchmarkQuill_Stream_Buffered_Render times the buffered facade path: Render
// allocates and grows a strings.Builder, then returns its contents as a string.
func BenchmarkQuill_Stream_Buffered_Render(b *testing.B) {
	env := quill.NewFromMap(map[string]string{"loop.ql": quillLoop})
	if _, err := env.LoadTemplate(context.Background(), "loop.ql"); err != nil {
		b.Fatal(err)
	}
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			vars := map[string]runtime.Value{"users": quillUsersN(n)}
			out, err := env.Render(context.Background(), "loop.ql", vars)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			for b.Loop() {
				if sink, err = env.Render(context.Background(), "loop.ql", vars); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkQuill_Stream_Streamed_Render times the streamed facade path: RenderTo
// pushes each rendered chunk straight to io.Discard, so no full-output buffer is
// retained. The output length for SetBytes is taken from a one-shot buffered
// render before the loop (identical bytes), and the render error is checked so
// the timed work cannot be elided.
func BenchmarkQuill_Stream_Streamed_Render(b *testing.B) {
	env := quill.NewFromMap(map[string]string{"loop.ql": quillLoop})
	if _, err := env.LoadTemplate(context.Background(), "loop.ql"); err != nil {
		b.Fatal(err)
	}
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			vars := map[string]runtime.Value{"users": quillUsersN(n)}
			out, err := env.Render(context.Background(), "loop.ql", vars)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			for b.Loop() {
				if err := env.RenderTo(context.Background(), io.Discard, "loop.ql", vars); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ============================================================================
// Fairness: full-output byte-identity across the offline engines.
// ============================================================================

// TestVerifyScenarios asserts, for every offline scenario, that the FULL output
// is byte-identical across the engines that should agree, so each benchmark
// above provably times equivalent work. The documented exceptions:
//
//   - Autoescape ON: Quill-autoescape-on must equal html/template (both escape).
//   - Autoescape OFF: Quill-autoescape-off must equal text/template (neither
//     escapes). These are the two comparisons the task requires.
//
// html/template's escaping of '&' to "&amp;", '<' to "&lt;", etc. is EXPECTED and
// is the whole point of the autoescape-on comparison, so it is asserted equal to
// Quill-on, not normalized away.
func TestVerifyScenarios(t *testing.T) {
	const n = 8 // small, exercises every arm/field without a wall of output

	// ---- Scenario 1: filter pipeline ----
	{
		env := quill.NewFromMap(map[string]string{"filter.ql": quillFilter})
		qOut, err := env.Render(context.Background(), "filter.ql", quillFilterVars(n))
		if err != nil {
			t.Fatalf("quill filter: %v", err)
		}
		tt := texttmpl.Must(texttmpl.New("filter").Funcs(filterFuncs).Parse(stdFilter))
		var tb bytes.Buffer
		if err := tt.Execute(&tb, struct{ Users []filterRow }{Users: filterRows(n)}); err != nil {
			t.Fatalf("text filter: %v", err)
		}
		if qOut != tb.String() {
			t.Errorf("filter mismatch\n quill=%q\n text =%q", qOut, tb.String())
		}
		// Sanity: the pipeline actually transformed the value ("  aiko  " ->
		// upper -> trim -> replace O with 0 -> "AIK0"), so a no-op chain cannot
		// pass this test by accident.
		if !strings.Contains(qOut, "AIK0") {
			t.Errorf("filter pipeline did not transform the value: %q", qOut)
		}
	}

	// ---- Scenario 2: nested loops ----
	{
		env := quill.NewFromMap(map[string]string{"nested.ql": quillNested})
		qOut, err := env.Render(context.Background(), "nested.ql", quillNestedVars(n))
		if err != nil {
			t.Fatalf("quill nested: %v", err)
		}
		tt := texttmpl.Must(texttmpl.New("nested").Funcs(filterFuncs).Parse(stdNested))
		var tb bytes.Buffer
		if err := tt.Execute(&tb, struct{ Groups []group }{Groups: groupsData(n)}); err != nil {
			t.Fatalf("text nested: %v", err)
		}
		if qOut != tb.String() {
			t.Errorf("nested mismatch\n quill=%q\n text =%q", qOut, tb.String())
		}
	}

	// ---- Scenario 3: conditionals ----
	{
		env := quill.NewFromMap(map[string]string{"cond.ql": quillCond})
		qOut, err := env.Render(context.Background(), "cond.ql", quillCondVars(n))
		if err != nil {
			t.Fatalf("quill cond: %v", err)
		}
		tt := texttmpl.Must(texttmpl.New("cond").Funcs(filterFuncs).Parse(stdCond))
		var tb bytes.Buffer
		if err := tt.Execute(&tb, struct{ Users []condRow }{Users: condRows(n)}); err != nil {
			t.Fatalf("text cond: %v", err)
		}
		if qOut != tb.String() {
			t.Errorf("cond mismatch\n quill=%q\n text =%q", qOut, tb.String())
		}
		// Sanity: all four arms must appear, so a chain that always takes one
		// branch cannot pass by accident.
		for _, arm := range []string{"\nA\n", "\nB\n", "\nC\n", "\nD\n"} {
			if !strings.Contains(qOut, arm) {
				t.Errorf("cond output missing arm %q: %q", arm, qOut)
			}
		}
	}

	// ---- Scenario 4: autoescape OFF == text/template, ON == html/template ----
	{
		vars := quillEscapeVars(n)

		envOff := quill.NewFromMap(map[string]string{"esc.ql": quillEscape})
		qOff, err := envOff.Render(context.Background(), "esc.ql", vars)
		if err != nil {
			t.Fatalf("quill escape off: %v", err)
		}
		envOn := quill.NewFromMap(map[string]string{"esc.ql": quillEscape},
			quill.WithAutoescapeHTML(true))
		qOn, err := envOn.Render(context.Background(), "esc.ql", vars)
		if err != nil {
			t.Fatalf("quill escape on: %v", err)
		}

		data := struct{ Comments []comment }{Comments: escapeComments(n)}
		tt := texttmpl.Must(texttmpl.New("esc").Funcs(filterFuncs).Parse(stdEscape))
		var tb bytes.Buffer
		if err := tt.Execute(&tb, data); err != nil {
			t.Fatalf("text escape: %v", err)
		}
		ht := htmltmpl.Must(htmltmpl.New("esc").Funcs(filterFuncs).Parse(stdEscape))
		var hb bytes.Buffer
		if err := ht.Execute(&hb, data); err != nil {
			t.Fatalf("html escape: %v", err)
		}

		// Quill-off is byte-identical to text/template (neither escapes).
		if qOff != tb.String() {
			t.Errorf("autoescape-off mismatch (want == text/template)\n quill-off=%q\n text     =%q", qOff, tb.String())
		}
		// Quill-on is byte-identical to html/template (both escape).
		if qOn != hb.String() {
			t.Errorf("autoescape-on mismatch (want == html/template)\n quill-on=%q\n html    =%q", qOn, hb.String())
		}
		// The two Quill variants MUST differ, proving escaping actually fired on
		// this input rather than the values being escape-free.
		if qOff == qOn {
			t.Errorf("autoescape had no effect: on and off outputs identical\n%q", qOff)
		}
		// And the escaped output must contain an HTML entity, proving the
		// transformation is genuine HTML escaping.
		if !strings.Contains(qOn, "&amp;") && !strings.Contains(qOn, "&lt;") {
			t.Errorf("autoescape-on produced no HTML entities: %q", qOn)
		}
	}

	// ---- Scenario 5: streaming (RenderTo) == buffered (Render) ----
	{
		env := quill.NewFromMap(map[string]string{"loop.ql": quillLoop})
		vars := map[string]runtime.Value{"users": quillUsersN(n)}
		buffered, err := env.Render(context.Background(), "loop.ql", vars)
		if err != nil {
			t.Fatalf("quill buffered: %v", err)
		}
		var sb strings.Builder
		if err := env.RenderTo(context.Background(), &sb, "loop.ql", vars); err != nil {
			t.Fatalf("quill streamed: %v", err)
		}
		if sb.String() != buffered {
			t.Errorf("stream vs buffered mismatch\n streamed=%q\n buffered=%q", sb.String(), buffered)
		}
		if buffered != loopWant(n) {
			t.Errorf("stream scenario output drifted from loopWant\n got =%q\n want=%q", buffered, loopWant(n))
		}
	}
}
