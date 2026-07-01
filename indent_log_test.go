package quill

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/cover"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// renderQ is a small helper: render a single ad-hoc template with vars and fail
// on error.
func renderQ(t *testing.T, src string, opts ...Option) string {
	t.Helper()
	env := NewWithArray(map[string]string{"t.ql": src}, opts...)
	out, err := env.Render("t.ql", map[string]runtime.Value{})
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	return out
}

// TestIndentFunctions covers space/break/tab as standalone emitters (spec 03
// Section 5.1a). space(n) emits n spaces, break(n) emits n newlines, tab(n)
// emits n indent levels of WithTabWidth spaces.
func TestIndentFunctions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
		opts []Option
	}{
		{"space default", "[{{ space() }}]", "[ ]", nil},
		{"space n", "[{{ space(3) }}]", "[   ]", nil},
		{"space zero", "[{{ space(0) }}]", "[]", nil},
		{"space negative", "[{{ space(0 - 2) }}]", "[]", nil},
		{"break default", "[{{ break() }}]", "[\n]", nil},
		{"break n", "[{{ break(2) }}]", "[\n\n]", nil},
		{"tab default", "[{{ tab() }}]", "[    ]", nil},
		{"tab n default width", "[{{ tab(2) }}]", "[        ]", nil},
		{"tab n custom width", "[{{ tab(2) }}]", "[    ]", []Option{WithTabWidth(2)}},
		{"tab zero", "[{{ tab(0) }}]", "[]", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderQ(t, tc.src, tc.opts...); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestTabFilterWidth proves the tab filter honors WithTabWidth: one level is the
// configured number of spaces (default 4), for both the standalone and
// multi-line forms.
func TestTabFilterWidth(t *testing.T) {
	if got := renderQ(t, "[{{ 1 | tab }}]"); got != "[    ]" {
		t.Errorf("default width: got %q", got)
	}
	if got := renderQ(t, "[{{ 2 | tab }}]"); got != "[        ]" {
		t.Errorf("two levels: got %q", got)
	}
	if got := renderQ(t, "[{{ 1 | tab }}]", WithTabWidth(2)); got != "[  ]" {
		t.Errorf("custom width: got %q", got)
	}
	// The multi-line form indents each non-blank line by n levels of the width.
	if got := renderQ(t, `{{ "a\nb" | tab(1) }}`); got != "    a\n    b" {
		t.Errorf("multiline: got %q", got)
	}
}

// TestTabBlockIndents proves @tab(n){} indents the entire rendered body by n
// levels, leaves blank lines blank, and composes with interpolation output.
func TestTabBlockIndents(t *testing.T) {
	src := "@tab(1) {\nline one\n\nline two\n@}\n"
	// Each non-blank line gains four spaces; the blank line stays empty.
	want := "    line one\n\n    line two\n"
	if got := renderQ(t, src); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// TestTabBlockCustomWidth proves the @tab region reads WithTabWidth.
func TestTabBlockCustomWidth(t *testing.T) {
	src := "@tab(2) {\nx\n@}\n"
	if got := renderQ(t, src, WithTabWidth(3)); got != "      x\n" { // 2 levels * 3 spaces
		t.Errorf("got %q", got)
	}
}

// TestTabBlockCumulative proves nested @tab regions stack cumulatively.
func TestTabBlockCumulative(t *testing.T) {
	src := "@tab(2) {\nouter\n@tab(1) {\ninner\n@}\n@}\n"
	// outer: 2 levels = 8 spaces; inner: 2+1 = 3 levels = 12 spaces.
	want := "        outer\n            inner\n"
	if got := renderQ(t, src); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// TestTabBlockInterpolation proves indentation applies to interpolated values,
// not just literal text.
func TestTabBlockInterpolation(t *testing.T) {
	env := NewWithArray(map[string]string{"t.ql": "@tab(1) {\n{{ name }}\n@}\n"})
	out, err := env.Render("t.ql", map[string]runtime.Value{"name": runtime.Str("Ada")})
	if err != nil {
		t.Fatal(err)
	}
	if out != "    Ada\n" {
		t.Errorf("got %q", out)
	}
}

// TestLogWritesToLoggerNoOutput proves @log writes to the configured logger and
// produces no rendered output.
func TestLogWritesToLoggerNoOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	// @log is a line statement recognized at line start; it consumes its line and
	// emits nothing, so the surrounding A and B lines abut.
	env := NewWithArray(map[string]string{"t.ql": "A\n@log \"hello\"\nB"}, WithLogger(logger))
	out, err := env.Render("t.ql", map[string]runtime.Value{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "A\nB" {
		t.Errorf("render output = %q; @log must emit nothing", out)
	}
	if got := strings.TrimSpace(buf.String()); got != "hello" {
		t.Errorf("logger got %q want %q", got, "hello")
	}
}

// TestLogDefaultLoggerDiscards proves @log is inert (no output, no error) when no
// logger is configured.
func TestLogDefaultLoggerDiscards(t *testing.T) {
	if got := renderQ(t, "X\n@log 1 + 1\nY"); got != "X\nY" {
		t.Errorf("got %q", got)
	}
}

// TestLogExpressionEvaluated proves @log evaluates its expression (a context
// value) and logs its text form.
func TestLogExpressionEvaluated(t *testing.T) {
	var buf bytes.Buffer
	env := NewWithArray(map[string]string{"t.ql": "@log user.name\n"},
		WithLogger(log.New(&buf, "", 0)))
	_, err := env.Render("t.ql", map[string]runtime.Value{
		"user": func() runtime.Value {
			a := runtime.NewArray()
			a.SetStr("name", runtime.Str("Grace"))
			return runtime.Arr(a)
		}(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "Grace" {
		t.Errorf("logger got %q", got)
	}
}

// TestLogIsCoverable proves the coverage Collector records an executed @log as a
// covered unit, and an unreached @log as an uncovered unit.
func TestLogIsCoverable(t *testing.T) {
	// 1: @if flag {
	// 2: @log "ran"
	// 3: @}
	// 4: @log "always"
	src := "@if flag {\n@log \"ran\"\n@}\n@log \"always\"\n"
	coll := cover.NewCollector()
	env := NewWithArray(map[string]string{"t.ql": src},
		WithLogger(log.New(&bytes.Buffer{}, "", 0)), WithCoverage(coll))
	if _, err := env.Render("t.ql", map[string]runtime.Value{"flag": runtime.Bool(false)}); err != nil {
		t.Fatal(err)
	}
	r := coll.Report()
	assertUncovered(t, r, "t.ql", 2, cover.UnitLog) // inside the not-taken @if
	assertCovered(t, r, "t.ql", 4, cover.UnitLog)   // top-level @log ran
}

// TestCommentNotCoverableNorRendered proves a comment {# #} is ignored by both
// render output AND the coverage Collector: it emits nothing and seeds no unit.
func TestCommentNotCoverableNorRendered(t *testing.T) {
	// 1: A{# this is a comment #}B
	src := "A{# this is a comment #}B\n"
	coll := cover.NewCollector()
	env := NewWithArray(map[string]string{"t.ql": src}, WithCoverage(coll))
	out, err := env.Render("t.ql", map[string]runtime.Value{})
	if err != nil {
		t.Fatal(err)
	}
	// The comment produced no output.
	if out != "AB\n" {
		t.Errorf("render output = %q; comment must emit nothing", out)
	}
	// No coverage region of any kind exists for the comment: only Text units for
	// the surrounding literal spans are seeded. Confirm no region carries the
	// comment as a covered/uncovered unit by asserting every seeded region is Text.
	r := coll.Report()
	for _, tc := range r.Templates() {
		if tc.Name != "t.ql" {
			continue
		}
		for _, reg := range tc.Regions {
			if reg.Kind != cover.UnitText {
				t.Errorf("unexpected coverable region %s at line %d; a comment must not be a unit",
					reg.Kind, reg.Line)
			}
		}
	}
}

// TestTabBlockCoverable proves the @tab region body is a coverable unit.
func TestTabBlockCoverable(t *testing.T) {
	src := "@tab(1) {\nx\n@}\n"
	coll := cover.NewCollector()
	env := NewWithArray(map[string]string{"t.ql": src}, WithCoverage(coll))
	if _, err := env.Render("t.ql", map[string]runtime.Value{}); err != nil {
		t.Fatal(err)
	}
	assertCovered(t, coll.Report(), "t.ql", 1, cover.UnitTabBlock)
}
