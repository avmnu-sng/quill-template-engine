package ext

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// callFilter looks up and invokes a core filter by name with the given args.
func callFilter(t *testing.T, name string, args ...runtime.Value) runtime.Value {
	t.Helper()
	s := Core()
	f, ok := s.Filter(name)
	if !ok {
		t.Fatalf("filter %q not registered", name)
	}
	v, err := f.Fn(args)
	if err != nil {
		t.Fatalf("filter %q error: %v", name, err)
	}
	return v
}

func callFn(t *testing.T, name string, args ...runtime.Value) runtime.Value {
	t.Helper()
	s := Core()
	f, ok := s.Function(name)
	if !ok {
		t.Fatalf("function %q not registered", name)
	}
	v, err := f.Fn(args)
	if err != nil {
		t.Fatalf("function %q error: %v", name, err)
	}
	return v
}

func callTest(t *testing.T, name string, args ...runtime.Value) bool {
	t.Helper()
	s := Core()
	tst, ok := s.Test(name)
	if !ok {
		t.Fatalf("test %q not registered", name)
	}
	b, err := tst.Fn(args)
	if err != nil {
		t.Fatalf("test %q error: %v", name, err)
	}
	return b
}

func list(vals ...runtime.Value) runtime.Value { return runtime.Arr(runtime.NewList(vals...)) }

func TestStringFilters(t *testing.T) {
	if got := callFilter(t, "upper", runtime.Str("hi")); got.AsStr() != "HI" {
		t.Errorf("upper = %q", got.AsStr())
	}
	if got := callFilter(t, "lower", runtime.Str("Hi")); got.AsStr() != "hi" {
		t.Errorf("lower = %q", got.AsStr())
	}
	if got := callFilter(t, "trim", runtime.Str("  x  ")); got.AsStr() != "x" {
		t.Errorf("trim = %q", got.AsStr())
	}
	if got := callFilter(t, "trim", runtime.Str("xxhixx"), runtime.Str("both"), runtime.Str("x")); got.AsStr() != "hi" {
		t.Errorf("trim mask = %q", got.AsStr())
	}
	if got := callFilter(t, "trim", runtime.Str("  x  "), runtime.Str("left")); got.AsStr() != "x  " {
		t.Errorf("ltrim = %q", got.AsStr())
	}
}

// TestReplaceStrtr verifies the longest-key-first, non-cascading semantics that
// source emission depends on (spec 03 Section 2.5).
func TestReplaceStrtr(t *testing.T) {
	pairs := runtime.NewArray()
	pairs.SetStr("a", runtime.Str("b"))
	pairs.SetStr("b", runtime.Str("c"))
	got := callFilter(t, "replace", runtime.Str("ab"), runtime.Arr(pairs))
	if got.AsStr() != "bc" { // not "cc": a replacement is never re-scanned
		t.Errorf("replace cascade leaked: %q", got.AsStr())
	}
}

func TestDefaultFilter(t *testing.T) {
	cases := []struct {
		name string
		in   runtime.Value
		want runtime.Value
	}{
		{"null falls back", runtime.Null(), runtime.Str("fb")},
		{"zero kept", runtime.Int(0), runtime.Int(0)},
		{"empty string kept", runtime.Str(""), runtime.Str("")},
		{"value kept", runtime.Str("x"), runtime.Str("x")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := callFilter(t, "default", c.in, runtime.Str("fb"))
			if !runtime.Same(got, c.want) {
				t.Errorf("default(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestLength(t *testing.T) {
	if got := callFilter(t, "length", runtime.Str("abc")); got.AsInt() != 3 {
		t.Errorf("length string = %d", got.AsInt())
	}
	if got := callFilter(t, "length", list(runtime.Int(1), runtime.Int(2))); got.AsInt() != 2 {
		t.Errorf("length list = %d", got.AsInt())
	}
	if got := callFilter(t, "length", runtime.Int(5)); got.AsInt() != 1 {
		t.Errorf("length scalar = %d", got.AsInt())
	}
}

func TestJoin(t *testing.T) {
	got := callFilter(t, "join", list(runtime.Str("a"), runtime.Str("b"), runtime.Str("c")), runtime.Str(", "))
	if got.AsStr() != "a, b, c" {
		t.Errorf("join = %q", got.AsStr())
	}
	got = callFilter(t, "join", list(runtime.Str("a"), runtime.Str("b"), runtime.Str("c")), runtime.Str(", "), runtime.Str(" and "))
	if got.AsStr() != "a, b and c" {
		t.Errorf("join final = %q", got.AsStr())
	}
}

func TestCollectionFilters(t *testing.T) {
	l := list(runtime.Int(3), runtime.Int(1), runtime.Int(2))
	if got := callFilter(t, "first", l); got.AsInt() != 3 {
		t.Errorf("first = %d", got.AsInt())
	}
	if got := callFilter(t, "last", l); got.AsInt() != 2 {
		t.Errorf("last = %d", got.AsInt())
	}
	sorted := callFilter(t, "sort", l)
	ps := sorted.AsArray().Pairs()
	if ps[0].Val.AsInt() != 1 || ps[2].Val.AsInt() != 3 {
		t.Errorf("sort = %v", ps)
	}
	rev := callFilter(t, "reverse", l, runtime.Bool(false))
	if rev.AsArray().Pairs()[0].Val.AsInt() != 2 {
		t.Errorf("reverse = %v", rev.AsArray().Pairs())
	}
}

func TestKeysAndMerge(t *testing.T) {
	m := runtime.NewArray()
	m.SetStr("x", runtime.Int(1))
	m.SetStr("y", runtime.Int(2))
	keys := callFilter(t, "keys", runtime.Arr(m))
	if keys.AsArray().Len() != 2 || keys.AsArray().Pairs()[0].Val.AsStr() != "x" {
		t.Errorf("keys = %v", keys.AsArray().Pairs())
	}
	a := runtime.NewList(runtime.Int(1), runtime.Int(2))
	b := runtime.NewList(runtime.Int(3))
	merged := callFilter(t, "merge", runtime.Arr(a), runtime.Arr(b))
	if merged.AsArray().Len() != 3 || merged.AsArray().Pairs()[2].Val.AsInt() != 3 {
		t.Errorf("merge = %v", merged.AsArray().Pairs())
	}
}

func TestSliceFilter(t *testing.T) {
	if got := callFilter(t, "slice", runtime.Str("hello"), runtime.Int(1), runtime.Int(3)); got.AsStr() != "ell" {
		t.Errorf("slice string = %q", got.AsStr())
	}
	if got := callFilter(t, "slice", runtime.Str("hello"), runtime.Int(-2)); got.AsStr() != "lo" {
		t.Errorf("slice negative = %q", got.AsStr())
	}
}

func TestEscapeAndRaw(t *testing.T) {
	got := callFilter(t, "escape", runtime.Str(`<a href="x">&'`))
	if got.Kind() != runtime.KSafe {
		t.Fatalf("escape should return Safe, got %s", got.Kind())
	}
	if got.AsStr() != "&lt;a href=&quot;x&quot;&gt;&amp;&#39;" {
		t.Errorf("escape html = %q", got.AsStr())
	}
	// Already-safe content is returned unchanged.
	if got := callFilter(t, "escape", runtime.Safe("<b>")); got.AsStr() != "<b>" {
		t.Errorf("escape of safe = %q", got.AsStr())
	}
	if got := callFilter(t, "raw", runtime.Str("<b>")); got.Kind() != runtime.KSafe || got.AsStr() != "<b>" {
		t.Errorf("raw = %v", got)
	}
}

func TestFunctions(t *testing.T) {
	r := callFn(t, "range", runtime.Int(1), runtime.Int(4))
	if r.AsArray().Len() != 4 || r.AsArray().Pairs()[3].Val.AsInt() != 4 {
		t.Errorf("range = %v", r.AsArray().Pairs())
	}
	rc := callFn(t, "range", runtime.Str("a"), runtime.Str("c"))
	if rc.AsArray().Len() != 3 || rc.AsArray().Pairs()[0].Val.AsStr() != "a" {
		t.Errorf("char range = %v", rc.AsArray().Pairs())
	}
	if got := callFn(t, "max", runtime.Int(3), runtime.Int(7), runtime.Int(1)); got.AsInt() != 7 {
		t.Errorf("max = %d", got.AsInt())
	}
	if got := callFn(t, "min", list(runtime.Int(3), runtime.Int(7), runtime.Int(1))); got.AsInt() != 1 {
		t.Errorf("min iterable = %d", got.AsInt())
	}
}

func TestTests(t *testing.T) {
	if !callTest(t, "null", runtime.Null()) {
		t.Error("null test failed")
	}
	if callTest(t, "null", runtime.Int(0)) {
		t.Error("0 is not null")
	}
	if !callTest(t, "empty", runtime.Str("")) || callTest(t, "empty", runtime.Int(0)) {
		t.Error("empty test wrong")
	}
	if !callTest(t, "even", runtime.Int(4)) || callTest(t, "even", runtime.Int(3)) {
		t.Error("even test wrong")
	}
	if !callTest(t, "odd", runtime.Int(3)) {
		t.Error("odd test wrong")
	}
	if !callTest(t, "iterable", list(runtime.Int(1))) || callTest(t, "iterable", runtime.Str("x")) {
		t.Error("iterable test wrong (a string is NOT iterable)")
	}
	if !callTest(t, "same as", runtime.Int(1), runtime.Int(1)) {
		t.Error("same as test wrong")
	}
	if callTest(t, "same_as", runtime.Int(1), runtime.Float(1)) {
		t.Error("same_as must not bridge int/float")
	}
}

// TestHostShadowsCore verifies a host registration overrides a core callable of
// the same name and kind (spec 03 Section 1).
func TestHostShadowsCore(t *testing.T) {
	s := Core()
	s.AddFilter(&Filter{Name: "upper", Fn: func(a []runtime.Value) (runtime.Value, error) {
		return runtime.Str("SHADOWED"), nil
	}})
	f, _ := s.Filter("upper")
	got, _ := f.Fn([]runtime.Value{runtime.Str("hi")})
	if got.AsStr() != "SHADOWED" {
		t.Errorf("host did not shadow core: %q", got.AsStr())
	}
}
