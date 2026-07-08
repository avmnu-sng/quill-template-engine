package ext

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// person builds a small mapping value for the attribute-projection tests.
func person(name string, age int64) runtime.Value {
	a := runtime.NewArray()
	a.SetStr("name", runtime.Str(name))
	a.SetStr("age", runtime.Int(age))
	return runtime.Arr(a)
}

// joinVals renders a list *Array's values, comma-separated via ToText.
func joinVals(t *testing.T, v runtime.Value) string {
	t.Helper()
	var parts []string
	for _, p := range v.AsArray().Pairs() {
		s, err := runtime.ToText(p.Val)
		if err != nil {
			t.Fatalf("ToText: %v", err)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ",")
}

// TestMapAttribute covers map(attribute:'path') plucking a dotted path, alongside
// the arrow form still working (spec 03 Section 2.2).
func TestMapAttribute(t *testing.T) {
	people := list(person("ann", 30), person("bob", 25))
	got := callFilter(t, "map", people, runtime.Str("name"))
	if j := joinVals(t, got); j != "ann,bob" {
		t.Errorf("map attribute = %q, want ann,bob", j)
	}

	// A dotted path descends nested mappings.
	nest := runtime.NewArray()
	inner := runtime.NewArray()
	inner.SetStr("id", runtime.Int(7))
	nest.SetStr("meta", runtime.Arr(inner))
	nested := list(runtime.Arr(nest))
	got = callFilter(t, "map", nested, runtime.Str("meta.id"))
	if j := joinVals(t, got); j != "7" {
		t.Errorf("map dotted attribute = %q, want 7", j)
	}
}

// TestMapPreservesKeys checks the attribute form is key-preserving on a mapping
// source (spec 03 Section 2.2).
func TestMapPreservesKeys(t *testing.T) {
	src := runtime.NewArray()
	src.SetStr("a", person("ann", 1))
	src.SetStr("b", person("bob", 2))
	got := callFilter(t, "map", runtime.Arr(src), runtime.Str("name"))
	keys := got.AsArray().Keys()
	if len(keys) != 2 || keys[0].AsStr() != "a" || keys[1].AsStr() != "b" {
		t.Errorf("map did not preserve keys: %v", keys)
	}
}

// TestSum covers plain summation and the sum(attribute:'path') form, and the
// int/float promotion (spec 03 Section 2.2).
func TestSum(t *testing.T) {
	if got := callFilter(t, "sum", list(runtime.Int(1), runtime.Int(2), runtime.Int(3))); got.Kind() != runtime.KInt || got.AsInt() != 6 {
		t.Errorf("sum ints = %+v, want Int 6", got)
	}
	if got := callFilter(t, "sum", list()); got.Kind() != runtime.KInt || got.AsInt() != 0 {
		t.Errorf("sum empty = %+v, want Int 0", got)
	}
	if got := callFilter(t, "sum", list(runtime.Int(1), runtime.Float(0.5))); got.Kind() != runtime.KFloat || got.AsFloat() != 1.5 {
		t.Errorf("sum mixed = %+v, want Float 1.5", got)
	}
	people := list(person("ann", 30), person("bob", 25))
	if got := callFilter(t, "sum", people, runtime.Str("age")); got.Kind() != runtime.KInt || got.AsInt() != 55 {
		t.Errorf("sum attribute = %+v, want Int 55", got)
	}
}

// TestSumNonNumber reports an error on a non-numeric element.
func TestSumNonNumber(t *testing.T) {
	s := Core()
	f, _ := s.Filter("sum")
	if _, err := f.Fn([]runtime.Value{list(runtime.Str("x"))}); err == nil {
		t.Fatal("sum of a non-number should error")
	}
}

// TestUnique covers whole-value dedup and the unique(attribute:'path') form,
// first occurrence wins, order preserved (spec 03 Section 2.2).
func TestUnique(t *testing.T) {
	got := callFilter(t, "unique", list(runtime.Int(1), runtime.Int(2), runtime.Int(1), runtime.Int(3), runtime.Int(2)))
	if j := joinVals(t, got); j != "1,2,3" {
		t.Errorf("unique = %q, want 1,2,3", j)
	}
	// By attribute: first element carrying each distinct type is kept.
	typed := func(typ, id string) runtime.Value {
		a := runtime.NewArray()
		a.SetStr("type", runtime.Str(typ))
		a.SetStr("id", runtime.Str(id))
		return runtime.Arr(a)
	}
	src := list(typed("a", "1"), typed("b", "2"), typed("a", "3"))
	got = callFilter(t, "unique", src, runtime.Str("type"))
	ids := callFilter(t, "map", got, runtime.Str("id"))
	if j := joinVals(t, ids); j != "1,2" {
		t.Errorf("unique attribute ids = %q, want 1,2", j)
	}
}

// TestComparisonTests covers eq/ne/lt/le/gt/ge (spec 03 Section 4).
func TestComparisonTests(t *testing.T) {
	cases := []struct {
		name string
		test string
		x, y runtime.Value
		want bool
	}{
		{"eq", "eq", runtime.Int(3), runtime.Int(3), true},
		{"eq-str", "eq", runtime.Str("a"), runtime.Str("a"), true},
		{"ne", "ne", runtime.Int(3), runtime.Int(4), true},
		{"lt", "lt", runtime.Int(3), runtime.Int(4), true},
		{"lt-false", "lt", runtime.Int(4), runtime.Int(4), false},
		{"le", "le", runtime.Int(4), runtime.Int(4), true},
		{"gt", "gt", runtime.Int(5), runtime.Int(4), true},
		{"ge", "ge", runtime.Int(4), runtime.Int(4), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := callTest(t, c.test, c.x, c.y); got != c.want {
				t.Errorf("%s(%v,%v) = %v, want %v", c.test, c.x, c.y, got, c.want)
			}
		})
	}
}

// TestComparisonTestsCrossKind confirms an unorderable pair surfaces an error
// (the runtime ordering is defined nowhere across unlike kinds).
func TestComparisonTestsCrossKind(t *testing.T) {
	s := Core()
	tst, _ := s.Test("lt")
	if _, err := tst.Fn([]runtime.Value{runtime.Int(1), runtime.Str("x")}); err == nil {
		t.Fatal("lt across unlike kinds should error")
	}
}

// TestSelectReject covers the select/reject filters with a named test, including
// an extra test argument (spec 03 Section 2.2).
func TestSelectReject(t *testing.T) {
	nums := list(runtime.Int(1), runtime.Int(2), runtime.Int(3), runtime.Int(4))
	got := callFilter(t, "select", nums, runtime.Str("even"))
	if j := joinVals(t, got); j != "2,4" {
		t.Errorf("select even = %q, want 2,4", j)
	}
	got = callFilter(t, "reject", nums, runtime.Str("even"))
	if j := joinVals(t, got); j != "1,3" {
		t.Errorf("reject even = %q, want 1,3", j)
	}
	// select with the ge comparison test and an argument.
	got = callFilter(t, "select", nums, runtime.Str("ge"), runtime.Int(3))
	if j := joinVals(t, got); j != "3,4" {
		t.Errorf("select ge 3 = %q, want 3,4", j)
	}
}

// TestSelectPreservesKeys confirms select is key-preserving on a mapping source.
func TestSelectPreservesKeys(t *testing.T) {
	src := runtime.NewArray()
	src.SetStr("x", runtime.Int(1))
	src.SetStr("y", runtime.Int(2))
	got := callFilter(t, "select", runtime.Arr(src), runtime.Str("even"))
	keys := got.AsArray().Keys()
	if len(keys) != 1 || keys[0].AsStr() != "y" {
		t.Errorf("select did not preserve mapping key: %v", keys)
	}
}

// TestSelectAttr covers selectattr/rejectattr projecting a path then applying a
// named test (spec 03 Section 2.2).
func TestSelectAttr(t *testing.T) {
	people := list(person("ann", 30), person("bob", 15), person("cal", 18))
	got := callFilter(t, "selectattr", people, runtime.Str("age"), runtime.Str("ge"), runtime.Int(18))
	names := callFilter(t, "map", got, runtime.Str("name"))
	if j := joinVals(t, names); j != "ann,cal" {
		t.Errorf("selectattr ge 18 = %q, want ann,cal", j)
	}
	got = callFilter(t, "rejectattr", people, runtime.Str("age"), runtime.Str("ge"), runtime.Int(18))
	names = callFilter(t, "map", got, runtime.Str("name"))
	if j := joinVals(t, names); j != "bob" {
		t.Errorf("rejectattr ge 18 = %q, want bob", j)
	}
}

// TestMapRejectsNonCallableNonString confirms map errors on an argument that is
// neither an attribute-path string nor a callable, rather than mis-plucking.
func TestMapRejectsNonCallableNonString(t *testing.T) {
	s := Core()
	f, _ := s.Filter("map")
	if _, err := f.Fn([]runtime.Value{list(runtime.Int(1)), runtime.Int(3)}); err == nil {
		t.Fatal("map with an int argument should error")
	}
}

// TestGroupByRejectsNonCallableNonString confirms group_by errors on an argument
// that is neither a path string nor an arrow.
func TestGroupByRejectsNonCallableNonString(t *testing.T) {
	s := Core()
	f, _ := s.Filter("group_by")
	if _, err := f.Fn([]runtime.Value{list(runtime.Int(1)), runtime.Int(3)}); err == nil {
		t.Fatal("group_by with an int argument should error")
	}
}

// TestGroupBy covers group_by returning first-appearance-ordered {key, items}
// mappings by a dotted path (spec 03 Section 2.2). The arrow-selector form is
// exercised through the conformance fixture.
func TestGroupBy(t *testing.T) {
	row := func(dept, name string) runtime.Value {
		a := runtime.NewArray()
		a.SetStr("dept", runtime.Str(dept))
		a.SetStr("name", runtime.Str(name))
		return runtime.Arr(a)
	}
	people := list(
		row("eng", "ann"),
		row("sales", "bob"),
		row("eng", "cal"),
		row("sales", "dee"),
	)
	got := callFilter(t, "group_by", people, runtime.Str("dept"))
	groups := got.AsArray().Pairs()
	if len(groups) != 2 {
		t.Fatalf("group_by got %d groups, want 2", len(groups))
	}
	// First-appearance order: eng before sales.
	k0, _ := groups[0].Val.AsArray().GetStr("key")
	k1, _ := groups[1].Val.AsArray().GetStr("key")
	if k0.AsStr() != "eng" || k1.AsStr() != "sales" {
		t.Errorf("group_by keys = %q,%q, want eng,sales", k0.AsStr(), k1.AsStr())
	}
	items0, _ := groups[0].Val.AsArray().GetStr("items")
	names0 := callFilter(t, "map", items0, runtime.Str("name"))
	if j := joinVals(t, names0); j != "ann,cal" {
		t.Errorf("group_by eng items = %q, want ann,cal", j)
	}
}

// TestGroupByTypedKeyDistinctness pins group_by to the runtime.Equal contract
// unique(attribute:) uses: keys that render to the same text but are distinct
// under typed equality (Int 1 vs Str "1", Bool true vs Str "true") stay in
// separate groups rather than collapsing on their text form.
func TestGroupByTypedKeyDistinctness(t *testing.T) {
	row := func(k runtime.Value, tag string) runtime.Value {
		a := runtime.NewArray()
		a.SetStr("k", k)
		a.SetStr("tag", runtime.Str(tag))
		return runtime.Arr(a)
	}
	rows := list(
		row(runtime.Int(1), "int1"),
		row(runtime.Str("1"), "str1"),
		row(runtime.Bool(true), "booltrue"),
		row(runtime.Str("true"), "strtrue"),
	)
	got := callFilter(t, "group_by", rows, runtime.Str("k"))
	groups := got.AsArray().Pairs()
	if len(groups) != 4 {
		t.Fatalf("group_by got %d groups, want 4 (typed keys stay distinct)", len(groups))
	}
	wantKinds := []runtime.Kind{runtime.KInt, runtime.KStr, runtime.KBool, runtime.KStr}
	wantTags := []string{"int1", "str1", "booltrue", "strtrue"}
	for i, g := range groups {
		key, _ := g.Val.AsArray().GetStr("key")
		if key.Kind() != wantKinds[i] {
			t.Errorf("group %d key kind = %v, want %v", i, key.Kind(), wantKinds[i])
		}
		items, _ := g.Val.AsArray().GetStr("items")
		tags := callFilter(t, "map", items, runtime.Str("tag"))
		if j := joinVals(t, tags); j != wantTags[i] {
			t.Errorf("group %d items = %q, want %q", i, j, wantTags[i])
		}
	}
}
