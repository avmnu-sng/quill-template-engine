package ext

import (
	"context"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// kv builds one [key, value] pair for the mapOf helper.
func kv(k, v runtime.Value) [2]runtime.Value { return [2]runtime.Value{k, v} }

// flagged builds a mapping with a name and a single flag attribute, used to
// exercise the selectattr(path) one-argument truthiness form.
func flagged(name string, flag runtime.Value) runtime.Value {
	a := runtime.NewArray()
	a.SetStr("name", runtime.Str(name))
	a.SetStr("active", flag)
	return runtime.Arr(a)
}

// gridRows renders a list-of-lists as "a|b;c|d": rows separated by ";", cells by
// "|", each cell via ToText. It flattens the nested-array shape the columns and
// entries filters produce for a single equality assertion.
func gridRows(t *testing.T, v runtime.Value) string {
	t.Helper()
	var rows []string
	for _, p := range v.AsArray().Pairs() {
		var cells []string
		for _, c := range p.Val.AsArray().Pairs() {
			s, err := runtime.ToText(c.Val)
			if err != nil {
				t.Fatalf("ToText: %v", err)
			}
			cells = append(cells, s)
		}
		rows = append(rows, strings.Join(cells, "|"))
	}
	return strings.Join(rows, ";")
}

// TestColumns covers columns(n) distributing a sequence into n balanced columns
// as the transpose of batch (spec 03 Section 2.2).
func TestColumns(t *testing.T) {
	seq := list(
		runtime.Int(1), runtime.Int(2), runtime.Int(3),
		runtime.Int(4), runtime.Int(5), runtime.Int(6), runtime.Int(7),
	)
	tests := []struct {
		name string
		n    int64
		want string
	}{
		// 7 items into 3 columns: index i -> column i%3, so the columns are the
		// transpose of batch(3) = [[1,2,3],[4,5,6],[7]].
		{"seven-into-three", 3, "1|4|7;2|5;3|6"},
		{"seven-into-two", 2, "1|3|5|7;2|4|6"},
		{"one-column", 1, "1|2|3|4|5|6|7"},
		// More columns than elements: the surplus columns are empty.
		{"more-columns-than-items", 8, "1;2;3;4;5;6;7;"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := callFilter(t, "columns", seq, runtime.Int(tc.n))
			if g := gridRows(t, got); g != tc.want {
				t.Errorf("columns(%d) = %q, want %q", tc.n, g, tc.want)
			}
		})
	}
}

// TestColumnsTransposeOfBatch asserts the transpose relationship directly: the
// c-th column equals the c-th cell of every batch row, in row order.
func TestColumnsTransposeOfBatch(t *testing.T) {
	seq := list(runtime.Int(1), runtime.Int(2), runtime.Int(3),
		runtime.Int(4), runtime.Int(5), runtime.Int(6), runtime.Int(7))
	n := runtime.Int(3)
	batched := callFilter(t, "batch", seq, n)
	columned := callFilter(t, "columns", seq, n)

	// batch(3) grid rows.
	rows := batched.AsArray().Pairs()
	cols := columned.AsArray().Pairs()
	for c, colPair := range cols {
		var want []string
		for _, rowPair := range rows {
			if cell, ok := rowPair.Val.AsArray().GetInt(int64(c)); ok {
				s, _ := runtime.ToText(cell)
				want = append(want, s)
			}
		}
		var got []string
		for _, cell := range colPair.Val.AsArray().Pairs() {
			s, _ := runtime.ToText(cell.Val)
			got = append(got, s)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("column %d = %v, want %v (transpose of batch)", c, got, want)
		}
	}
}

// TestColumnsFill pads every column to the tallest column's height with fill.
func TestColumnsFill(t *testing.T) {
	seq := list(runtime.Int(1), runtime.Int(2), runtime.Int(3), runtime.Int(4), runtime.Int(5))
	got := callFilter(t, "columns", seq, runtime.Int(3), runtime.Str("x"))
	// index i -> column i%3: col0=[1,4], col1=[2,5], col2=[3]; height=2, so col2
	// is padded with one "x".
	if g := gridRows(t, got); g != "1|4;2|5;3|x" {
		t.Errorf("columns fill = %q, want 1|4;2|5;3|x", g)
	}
}

// TestColumnsErrors covers the argument-domain errors: a non-collection source
// and a column count below one.
func TestColumnsErrors(t *testing.T) {
	s := Core()
	f, _ := s.Filter("columns")
	if _, err := f.Fn(context.Background(), []runtime.Value{runtime.Int(1), runtime.Int(2)}); err == nil {
		t.Error("columns on a non-collection should error")
	}
	if _, err := f.Fn(context.Background(), []runtime.Value{list(runtime.Int(1)), runtime.Int(0)}); err == nil {
		t.Error("columns count 0 should error")
	}
}

// TestEntries yields a mapping's [key, value] pairs as an ordered sequence of
// two-element lists (spec 03 Section 2.2).
func TestEntries(t *testing.T) {
	m := mapOf(
		kv(runtime.Str("b"), runtime.Int(2)),
		kv(runtime.Str("a"), runtime.Int(1)),
		kv(runtime.Str("c"), runtime.Int(3)),
	)
	got := callFilter(t, "entries", m)
	// Insertion order is preserved: b, a, c.
	if g := gridRows(t, got); g != "b|2;a|1;c|3" {
		t.Errorf("entries = %q, want b|2;a|1;c|3", g)
	}
}

// TestEntriesList pairs a list's integer keys with their values.
func TestEntriesList(t *testing.T) {
	got := callFilter(t, "entries", list(runtime.Str("x"), runtime.Str("y")))
	if g := gridRows(t, got); g != "0|x;1|y" {
		t.Errorf("entries of list = %q, want 0|x;1|y", g)
	}
}

// TestEntriesError covers the non-mapping source error.
func TestEntriesError(t *testing.T) {
	s := Core()
	f, _ := s.Filter("entries")
	if _, err := f.Fn(context.Background(), []runtime.Value{runtime.Int(1)}); err == nil {
		t.Error("entries on a non-mapping should error")
	}
}

// TestSortMap sorts a mapping deterministically by key or by value (spec 03
// Section 2.2).
func TestSortMap(t *testing.T) {
	m := mapOf(
		kv(runtime.Str("b"), runtime.Int(3)),
		kv(runtime.Str("a"), runtime.Int(1)),
		kv(runtime.Str("c"), runtime.Int(2)),
	)
	tests := []struct {
		name string
		by   []runtime.Value
		want string
	}{
		{"default-key", nil, "a=1,b=3,c=2"},
		{"by-key", []runtime.Value{runtime.Str("key")}, "a=1,b=3,c=2"},
		{"by-value", []runtime.Value{runtime.Str("value")}, "a=1,c=2,b=3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]runtime.Value{m}, tc.by...)
			got := callFilter(t, "sort_map", args...)
			var parts []string
			for _, p := range got.AsArray().Pairs() {
				k, _ := runtime.ToText(p.Key)
				val, _ := runtime.ToText(p.Val)
				parts = append(parts, k+"="+val)
			}
			if g := strings.Join(parts, ","); g != tc.want {
				t.Errorf("sort_map = %q, want %q", g, tc.want)
			}
		})
	}

	// Pairs comparing equal on the chosen component keep their original
	// insertion order, because the sort is stable.
	tied := mapOf(
		kv(runtime.Str("b"), runtime.Int(5)),
		kv(runtime.Str("a"), runtime.Int(5)),
	)
	tiedTests := []struct {
		name string
		by   []runtime.Value
		want string
	}{
		{"stable-tied-values", []runtime.Value{runtime.Str("value")}, "b=5,a=5"},
	}
	for _, tc := range tiedTests {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]runtime.Value{tied}, tc.by...)
			got := callFilter(t, "sort_map", args...)
			var parts []string
			for _, p := range got.AsArray().Pairs() {
				k, _ := runtime.ToText(p.Key)
				val, _ := runtime.ToText(p.Val)
				parts = append(parts, k+"="+val)
			}
			if g := strings.Join(parts, ","); g != tc.want {
				t.Errorf("sort_map = %q, want %q", g, tc.want)
			}
		})
	}
}

// TestSortMapErrors covers the non-mapping source and an unknown by argument.
func TestSortMapErrors(t *testing.T) {
	s := Core()
	f, _ := s.Filter("sort_map")
	if _, err := f.Fn(context.Background(), []runtime.Value{runtime.Int(1)}); err == nil {
		t.Error("sort_map on a non-mapping should error")
	}
	if _, err := f.Fn(context.Background(), []runtime.Value{mapOf(kv(runtime.Str("a"), runtime.Int(1))), runtime.Str("nope")}); err == nil {
		t.Error("sort_map with an unknown by argument should error")
	}
}

// TestSelectAttrTruthiness covers the one-argument selectattr(path)/rejectattr(path)
// form filtering by the engine truthiness rule (spec 03 Section 2.2). The flag
// values mix booleans, numbers, strings, and null so the truthiness rule -- not a
// strict `is true` test -- is what governs.
func TestSelectAttrTruthiness(t *testing.T) {
	people := list(
		flagged("ann", runtime.Bool(true)),
		flagged("bob", runtime.Bool(false)),
		flagged("cal", runtime.Int(1)),    // truthy non-bool
		flagged("dee", runtime.Int(0)),    // falsy number
		flagged("eve", runtime.Str("hi")), // truthy non-bool string
		flagged("fin", runtime.Str("")),   // falsy empty string
		flagged("guy", runtime.Null()),    // falsy null
	)

	sel := callFilter(t, "selectattr", people, runtime.Str("active"))
	if j := joinVals(t, callFilter(t, "map", sel, runtime.Str("name"))); j != "ann,cal,eve" {
		t.Errorf("selectattr(active) = %q, want ann,cal,eve", j)
	}

	rej := callFilter(t, "rejectattr", people, runtime.Str("active"))
	if j := joinVals(t, callFilter(t, "map", rej, runtime.Str("name"))); j != "bob,dee,fin,guy" {
		t.Errorf("rejectattr(active) = %q, want bob,dee,fin,guy", j)
	}
}

// TestSelectAttrTruthinessExplicitNull confirms selectattr(path, null) also takes
// the truthiness path: a null test name is treated as no test name.
func TestSelectAttrTruthinessExplicitNull(t *testing.T) {
	people := list(flagged("ann", runtime.Bool(true)), flagged("bob", runtime.Bool(false)))
	sel := callFilter(t, "selectattr", people, runtime.Str("active"), runtime.Null())
	if j := joinVals(t, callFilter(t, "map", sel, runtime.Str("name"))); j != "ann" {
		t.Errorf("selectattr(active, null) = %q, want ann", j)
	}
}

// TestSelectAttrTwoArgUnchanged confirms the (path, test, args...) form is
// unchanged and works with a truthy non-bool projected value under a named test.
func TestSelectAttrTwoArgUnchanged(t *testing.T) {
	people := list(person("ann", 30), person("bob", 15), person("cal", 18))
	got := callFilter(t, "selectattr", people, runtime.Str("age"), runtime.Str("ge"), runtime.Int(18))
	if j := joinVals(t, callFilter(t, "map", got, runtime.Str("name"))); j != "ann,cal" {
		t.Errorf("selectattr ge 18 = %q, want ann,cal", j)
	}
	got = callFilter(t, "rejectattr", people, runtime.Str("age"), runtime.Str("ge"), runtime.Int(18))
	if j := joinVals(t, callFilter(t, "map", got, runtime.Str("name"))); j != "bob" {
		t.Errorf("rejectattr ge 18 = %q, want bob", j)
	}
}

// TestSelectAttrUnknownTest confirms the named-test form still rejects an unknown
// test name.
func TestSelectAttrUnknownTest(t *testing.T) {
	s := Core()
	f, _ := s.Filter("selectattr")
	people := list(person("ann", 30))
	if _, err := f.Fn(context.Background(), []runtime.Value{people, runtime.Str("age"), runtime.Str("nope")}); err == nil {
		t.Error("selectattr with an unknown test should error")
	}
}
