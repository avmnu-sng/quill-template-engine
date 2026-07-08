package ext

import (
	"context"
	"math/rand"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

func mapOf(pairs ...[2]runtime.Value) runtime.Value {
	a := runtime.NewArray()
	for _, p := range pairs {
		a.SetKey(p[0], p[1])
	}
	return runtime.Arr(a)
}

// TestStdlibStringFilters covers the new string filters (spec 03 Sections 2.1,
// 5.1-5.3).
func TestStdlibStringFilters(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"capitalize", callFilter(t, "capitalize", runtime.Str("hELLO wORLD")).AsStr(), "Hello world"},
		{"capitalize empty", callFilter(t, "capitalize", runtime.Str("")).AsStr(), ""},
		{"title", callFilter(t, "title", runtime.Str("hello world-foo")).AsStr(), "Hello World-Foo"},
		{"ucfirst", callFilter(t, "ucfirst", runtime.Str("hELLO")).AsStr(), "HELLO"},
		{"ucfirst keeps rest", callFilter(t, "ucfirst", runtime.Str("camelCase")).AsStr(), "CamelCase"},
		{"nl2br", callFilter(t, "nl2br", runtime.Str("a\nb")).AsStr(), "a<br />\nb"},
		{"nl2br escapes", callFilter(t, "nl2br", runtime.Str("<x>\ny")).AsStr(), "&lt;x&gt;<br />\ny"},
		{"spaceless", callFilter(t, "spaceless", runtime.Str("<a>  <b>")).AsStr(), "<a><b>"},
		{"striptags all", callFilter(t, "striptags", runtime.Str("<b>hi</b>")).AsStr(), "hi"},
		{"striptags allowed", callFilter(t, "striptags", runtime.Str("<b>hi</b><i>x</i>"), runtime.Str("<b>")).AsStr(), "<b>hi</b>x"},
		{"format verbs", callFilter(t, "format", runtime.Str("%s=%d"), runtime.Str("n"), runtime.Int(3)).AsStr(), "n=3"},
		{"format quote", callFilter(t, "format", runtime.Str("%q"), runtime.Str("x")).AsStr(), `"x"`},
		{"convert_encoding utf8", callFilter(t, "convert_encoding", runtime.Str("ok"), runtime.Str("UTF-8")).AsStr(), "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

// TestStdlibSplit covers the split filter, including the rune-chunking and
// limit forms (spec 03 Section 2.1).
func TestStdlibSplit(t *testing.T) {
	joined := func(v runtime.Value) string {
		var parts []string
		for _, p := range v.AsArray().Pairs() {
			parts = append(parts, p.Val.AsStr())
		}
		return strings.Join(parts, "|")
	}
	if got := joined(callFilter(t, "split", runtime.Str("a,b,c"), runtime.Str(","))); got != "a|b|c" {
		t.Errorf("split = %q", got)
	}
	if got := joined(callFilter(t, "split", runtime.Str("a,b,c"), runtime.Str(","), runtime.Int(2))); got != "a|b,c" {
		t.Errorf("split limit = %q", got)
	}
	if got := joined(callFilter(t, "split", runtime.Str("abc"), runtime.Str(""))); got != "a|b|c" {
		t.Errorf("split runes = %q", got)
	}
	if got := joined(callFilter(t, "split", runtime.Str("abcd"), runtime.Str(""), runtime.Int(2))); got != "ab|cd" {
		t.Errorf("split rune chunks = %q", got)
	}
}

// TestStdlibCollectionFilters covers batch, column, and shuffle determinism
// (spec 03 Section 2.2). The arrow-driven filters are exercised at the interp
// level where an arrow value is available.
func TestStdlibCollectionFilters(t *testing.T) {
	// batch into chunks of 2 with fill.
	src := list(runtime.Int(1), runtime.Int(2), runtime.Int(3))
	batched := callFilter(t, "batch", src, runtime.Int(2), runtime.Int(0))
	if batched.AsArray().Len() != 2 {
		t.Fatalf("batch chunks = %d, want 2", batched.AsArray().Len())
	}
	last := batched.AsArray().Pairs()[1].Val
	if last.AsArray().Len() != 2 || last.AsArray().Pairs()[1].Val.AsInt() != 0 {
		t.Errorf("batch fill = %+v", last.AsArray().Pairs())
	}

	// column extracts a key from each row.
	row := func(id int64) runtime.Value { return mapOf([2]runtime.Value{runtime.Str("id"), runtime.Int(id)}) }
	rows := list(row(1), row(2))
	col := callFilter(t, "column", rows, runtime.Str("id"))
	if col.AsArray().Len() != 2 || col.AsArray().Pairs()[0].Val.AsInt() != 1 || col.AsArray().Pairs()[1].Val.AsInt() != 2 {
		t.Errorf("column = %+v", col.AsArray().Pairs())
	}

	// shuffle with a fixed seed is deterministic.
	a := callFilter(t, "shuffle", list(runtime.Int(1), runtime.Int(2), runtime.Int(3), runtime.Int(4)), runtime.Int(42))
	b := callFilter(t, "shuffle", list(runtime.Int(1), runtime.Int(2), runtime.Int(3), runtime.Int(4)), runtime.Int(42))
	if !runtime.Equal(a, b) {
		t.Errorf("seeded shuffle not deterministic: %+v vs %+v", a.AsArray().Pairs(), b.AsArray().Pairs())
	}
}

// TestStdlibMathFilters covers abs, round (modes and negative precision), and
// format_number (spec 03 Sections 2.1, 2.3).
func TestStdlibMathFilters(t *testing.T) {
	if got := callFilter(t, "abs", runtime.Int(-5)); got.Kind() != runtime.KInt || got.AsInt() != 5 {
		t.Errorf("abs int = %+v", got)
	}
	if got := callFilter(t, "abs", runtime.Float(-2.5)); got.Kind() != runtime.KFloat || got.AsFloat() != 2.5 {
		t.Errorf("abs float = %+v", got)
	}
	if got := callFilter(t, "round", runtime.Float(2.5)); got.AsFloat() != 3 {
		t.Errorf("round common = %v", got.AsFloat())
	}
	if got := callFilter(t, "round", runtime.Float(2.1), runtime.Int(0), runtime.Str("ceil")); got.AsFloat() != 3 {
		t.Errorf("round ceil = %v", got.AsFloat())
	}
	if got := callFilter(t, "round", runtime.Float(2.9), runtime.Int(0), runtime.Str("floor")); got.AsFloat() != 2 {
		t.Errorf("round floor = %v", got.AsFloat())
	}
	if got := callFilter(t, "round", runtime.Float(123.0), runtime.Int(-2)); got.AsFloat() != 100 {
		t.Errorf("round neg precision = %v", got.AsFloat())
	}
	if got := callFilter(t, "format_number", runtime.Int(1234567)); got.AsStr() != "1,234,567" {
		t.Errorf("format_number = %q", got.AsStr())
	}
	if got := callFilter(t, "number_format", runtime.Float(1234.5), runtime.Int(2), runtime.Str("."), runtime.Str(" ")); got.AsStr() != "1 234.50" {
		t.Errorf("number_format = %q", got.AsStr())
	}
	if got := callFilter(t, "format_number", runtime.Float(-12.5), runtime.Int(1)); got.AsStr() != "-12.5" {
		t.Errorf("format_number neg = %q", got.AsStr())
	}
}

// TestStdlibJSON covers the json filter's Go-dialect output: ordered keys, no
// HTML escaping, literal slash (spec 03 Section 2.6).
func TestStdlibJSON(t *testing.T) {
	m := mapOf(
		[2]runtime.Value{runtime.Str("b"), runtime.Int(1)},
		[2]runtime.Value{runtime.Str("a"), runtime.Str("</x>")},
	)
	got := callFilter(t, "json", m).AsStr()
	want := `{"b":1,"a":"</x>"}`
	if got != want {
		t.Errorf("json = %q, want %q", got, want)
	}
	arr := list(runtime.Int(1), runtime.Int(2))
	if got := callFilter(t, "json", arr).AsStr(); got != "[1,2]" {
		t.Errorf("json list = %q", got)
	}
	pretty := callFilter(t, "json", arr, runtime.Bool(true)).AsStr()
	if !strings.Contains(pretty, "\n") {
		t.Errorf("pretty json missing newlines: %q", pretty)
	}
}

// TestStdlibEscapeStrategies covers all six escape strategies (spec 03 Section
// 5.5).
func TestStdlibEscapeStrategies(t *testing.T) {
	cases := []struct{ strategy, in, want string }{
		{"html", `<a>&"'`, "&lt;a&gt;&amp;&quot;&#39;"},
		{"js", "a b", "a\\x20b"},
		{"css", "a b", "a\\20 b"},
		{"html_attr", "a b", "a&#x20;b"},
		{"html_attr_relaxed", "a:b", "a:b"},
		{"url", "a b/c", "a%20b%2Fc"},
	}
	for _, c := range cases {
		t.Run(c.strategy, func(t *testing.T) {
			got, err := Escape(c.strategy, c.in)
			if err != nil {
				t.Fatalf("escape %s: %v", c.strategy, err)
			}
			if got != c.want {
				t.Errorf("escape %s(%q) = %q, want %q", c.strategy, c.in, got, c.want)
			}
		})
	}
	if _, err := Escape("bogus", "x"); err == nil {
		t.Error("expected error for unknown strategy")
	}
}

// TestStdlibURLEncode covers the url_encode filter on a string and on a mapping
// (spec 03 Section 2.4).
func TestStdlibURLEncode(t *testing.T) {
	if got := callFilter(t, "url_encode", runtime.Str("a b")).AsStr(); got != "a%20b" {
		t.Errorf("url_encode string = %q", got)
	}
	q := mapOf(
		[2]runtime.Value{runtime.Str("k1"), runtime.Str("v 1")},
		[2]runtime.Value{runtime.Str("k2"), runtime.Int(2)},
	)
	if got := callFilter(t, "url_encode", q).AsStr(); got != "k1=v%201&k2=2" {
		t.Errorf("url_encode map = %q", got)
	}
}

// TestStdlibSourceFilters covers tab and indent (spec 03 Sections 5.1, 5.3). One
// tab level is DefaultTabWidth (4) spaces; the engine's WithTabWidth override is
// exercised through the facade.
func TestStdlibSourceFilters(t *testing.T) {
	if got := callFilter(t, "tab", runtime.Int(2)).AsStr(); got != "        " {
		t.Errorf("tab standalone = %q", got)
	}
	if got := callFilter(t, "tab", runtime.Str("a\nb"), runtime.Int(1)).AsStr(); got != "    a\n    b" {
		t.Errorf("tab lines = %q", got)
	}
	// A blank line is not indented.
	if got := callFilter(t, "tab", runtime.Str("a\n\nb"), runtime.Int(1)).AsStr(); got != "    a\n\n    b" {
		t.Errorf("tab blank line = %q", got)
	}
	// A negative level (e.g. a computed depth-1 yielding -1) clamps to zero
	// levels, leaving the string unindented rather than panicking.
	if got := callFilter(t, "tab", runtime.Str("a\nb"), runtime.Int(-1)).AsStr(); got != "a\nb" {
		t.Errorf("tab negative level = %q", got)
	}
	// Level 0 is a no-op.
	if got := callFilter(t, "tab", runtime.Str("a\nb"), runtime.Int(0)).AsStr(); got != "a\nb" {
		t.Errorf("tab zero level = %q", got)
	}
	if got := callFilter(t, "indent", runtime.Str("x\ny"), runtime.Int(2)).AsStr(); got != "        x\n        y" {
		t.Errorf("indent = %q", got)
	}
	if got := callFilter(t, "indent", runtime.Str("x"), runtime.Int(1), runtime.Str("--")).AsStr(); got != "--x" {
		t.Errorf("indent unit = %q", got)
	}
}

// TestStdlibFunctions covers cycle, random (seeded), and attribute (spec 03
// Section 3.2). constant/enum are covered in TestStdlibRegistry.
func TestStdlibFunctions(t *testing.T) {
	vals := list(runtime.Str("a"), runtime.Str("b"), runtime.Str("c"))
	if got := callFn(t, "cycle", vals, runtime.Int(4)).AsStr(); got != "b" {
		t.Errorf("cycle = %q", got)
	}
	if got := callFn(t, "cycle", vals, runtime.Int(-1)).AsStr(); got != "c" {
		t.Errorf("cycle negative = %q", got)
	}
	// random(values, max): arg1 is the inclusive upper bound, not a seed (spec 03
	// Section 3.2). random(n) draws in [0, n]; random(lo, hi) draws in [lo, hi].
	for i := 0; i < 200; i++ {
		r := callFn(t, "random", runtime.Int(5))
		if r.AsInt() < 0 || r.AsInt() > 5 {
			t.Fatalf("random(5) out of [0,5]: %d", r.AsInt())
		}
		r2 := callFn(t, "random", runtime.Int(10), runtime.Int(20))
		if r2.AsInt() < 10 || r2.AsInt() > 20 {
			t.Fatalf("random(10,20) out of [10,20]: %d", r2.AsInt())
		}
	}
	// RandomWith against a fixed source is deterministic, the engine's seed hook.
	src := func() *rand.Rand { return rand.New(rand.NewSource(7)) }
	a, _ := RandomWith(src(), []runtime.Value{runtime.Null(), runtime.Int(1000)})
	b, _ := RandomWith(src(), []runtime.Value{runtime.Null(), runtime.Int(1000)})
	if a.AsInt() != b.AsInt() {
		t.Errorf("seeded RandomWith not deterministic: %d vs %d", a.AsInt(), b.AsInt())
	}
	// attribute reads a map member dynamically.
	m := mapOf([2]runtime.Value{runtime.Str("name"), runtime.Str("quill")})
	if got := callFn(t, "attribute", m, runtime.Str("name")).AsStr(); got != "quill" {
		t.Errorf("attribute = %q", got)
	}
}

// TestStdlibRegistry covers constant / enum / enum_cases reading the host
// registry, and the is constant test (spec 03 Sections 3.2, 4).
func TestStdlibRegistry(t *testing.T) {
	s := Core()
	s.AddConstant("PI", runtime.Float(3.14))
	s.AddEnum("Color", []runtime.Value{runtime.Str("red"), runtime.Str("green"), runtime.Str("blue")})

	cst, _ := s.Function("constant")
	v, err := cst.Fn(context.Background(), []runtime.Value{runtime.Str("PI")})
	if err != nil || v.AsFloat() != 3.14 {
		t.Errorf("constant PI = %+v, err=%v", v, err)
	}
	check, err := cst.Fn(context.Background(), []runtime.Value{runtime.Str("MISSING"), runtime.Null(), runtime.Bool(true)})
	if err != nil || check.Kind() != runtime.KBool || check.AsBool() {
		t.Errorf("constant check_defined missing = %+v, err=%v", check, err)
	}

	en, _ := s.Function("enum")
	first, err := en.Fn(context.Background(), []runtime.Value{runtime.Str("Color")})
	if err != nil || first.AsStr() != "red" {
		t.Errorf("enum first = %+v, err=%v", first, err)
	}
	cases, _ := s.Function("enum_cases")
	all, err := cases.Fn(context.Background(), []runtime.Value{runtime.Str("Color")})
	if err != nil || all.AsArray().Len() != 3 {
		t.Errorf("enum_cases = %+v, err=%v", all, err)
	}

	tst, _ := s.Test("constant")
	ok, err := tst.Fn(context.Background(), []runtime.Value{runtime.Float(3.14), runtime.Str("PI")})
	if err != nil || !ok {
		t.Errorf("is constant PI = %v, err=%v", ok, err)
	}
}

// TestStdlibTests covers divisible_by, sequence, mapping, true (spec 03 Section
// 4).
func TestStdlibTests(t *testing.T) {
	if !callTest(t, "divisible_by", runtime.Int(10), runtime.Int(5)) {
		t.Error("10 divisible_by 5 should be true")
	}
	if callTest(t, "divisible_by", runtime.Int(10), runtime.Int(3)) {
		t.Error("10 divisible_by 3 should be false")
	}
	if !callTest(t, "sequence", list(runtime.Int(1))) {
		t.Error("list should be a sequence")
	}
	if callTest(t, "sequence", mapOf([2]runtime.Value{runtime.Str("a"), runtime.Int(1)})) {
		t.Error("map should not be a sequence")
	}
	if !callTest(t, "mapping", mapOf([2]runtime.Value{runtime.Str("a"), runtime.Int(1)})) {
		t.Error("map should be a mapping")
	}
	if !callTest(t, "true", runtime.Bool(true)) {
		t.Error("true is true")
	}
	if callTest(t, "true", runtime.Int(1)) {
		t.Error("1 is not Bool true")
	}
}

// TestStdlibInvoke covers the invoke filter applying a callable. A bare
// non-callable is a clear error.
func TestStdlibInvoke(t *testing.T) {
	s := Core()
	f, _ := s.Filter("invoke")
	if _, err := f.Fn(context.Background(), []runtime.Value{runtime.Int(3)}); err == nil {
		t.Error("invoke on a non-callable should error")
	}
}

// TestStdlibDate covers the date function, the date filter's Go-layout
// formatting, and date_modify deltas (spec 03 Sections 2.4, 2.6, 3.2).
func TestStdlibDate(t *testing.T) {
	s := Core()
	// date function from a Unix timestamp (UTC), then format with a Go layout.
	dateFn, _ := s.Function("date")
	d, err := dateFn.Fn(context.Background(), []runtime.Value{runtime.Int(0)}) // 1970-01-01T00:00:00Z
	if err != nil {
		t.Fatalf("date(): %v", err)
	}
	dateFilt, _ := s.Filter("date")
	out, err := dateFilt.Fn(context.Background(), []runtime.Value{d, runtime.Str("2006-01-02")})
	if err != nil || out.AsStr() != "1970-01-01" {
		t.Errorf("date filter = %q, err=%v", out.AsStr(), err)
	}
	// date filter coerces a string directly.
	out, err = dateFilt.Fn(context.Background(), []runtime.Value{runtime.Str("2021-03-04"), runtime.Str("01/02/2006")})
	if err != nil || out.AsStr() != "03/04/2021" {
		t.Errorf("date filter string = %q, err=%v", out.AsStr(), err)
	}
	// date_modify adds a day.
	mod, _ := s.Filter("date_modify")
	dm, err := mod.Fn(context.Background(), []runtime.Value{runtime.Str("2021-03-04"), runtime.Str("+1 day")})
	if err != nil {
		t.Fatalf("date_modify: %v", err)
	}
	out, _ = dateFilt.Fn(context.Background(), []runtime.Value{dm, runtime.Str("2006-01-02")})
	if out.AsStr() != "2021-03-05" {
		t.Errorf("date_modify +1 day = %q", out.AsStr())
	}
}

// TestDumpFormat covers the Go-native, kind-tagged dump format (spec 03 Section
// 3.3).
func TestDumpFormat(t *testing.T) {
	if got := Dump(runtime.Int(0)); got != "int(0)" {
		t.Errorf("dump int = %q", got)
	}
	if got := Dump(runtime.Str("0")); got != `string("0")` {
		t.Errorf("dump string = %q", got)
	}
	if got := Dump(runtime.Bool(false)); got != "bool(false)" {
		t.Errorf("dump bool = %q", got)
	}
	d := Dump(list(runtime.Int(1), runtime.Int(2)))
	if !strings.Contains(d, "array(2)") || !strings.Contains(d, "int(1)") {
		t.Errorf("dump array = %q", d)
	}
}
