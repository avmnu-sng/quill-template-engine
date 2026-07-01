package runtime

import (
	"math"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/errors"
)

// mustFromGo marshals v or fails the test.
func mustFromGo(t *testing.T, v any) Value {
	t.Helper()
	got, err := FromGo(v)
	if err != nil {
		t.Fatalf("FromGo(%#v) error: %v", v, err)
	}
	return got
}

// TestFromGoScalars pins each scalar kind's mapping, including every integer
// width and the nil cases.
func TestFromGoScalars(t *testing.T) {
	var nilPtr *int
	var nilIface any
	tests := []struct {
		name string
		in   any
		want Value
	}{
		{"nil", nil, Null()},
		{"nil pointer", nilPtr, Null()},
		{"nil interface", nilIface, Null()},
		{"bool true", true, Bool(true)},
		{"bool false", false, Bool(false)},
		{"string", "hi", Str("hi")},
		{"empty string", "", Str("")},
		{"int", int(7), Int(7)},
		{"int8", int8(-8), Int(-8)},
		{"int16", int16(16), Int(16)},
		{"int32", int32(-32), Int(-32)},
		{"int64 max", int64(math.MaxInt64), Int(math.MaxInt64)},
		{"uint", uint(9), Int(9)},
		{"uint8", uint8(255), Int(255)},
		{"uint16", uint16(65535), Int(65535)},
		{"uint32", uint32(4294967295), Int(4294967295)},
		{"uint64 in range", uint64(1000), Int(1000)},
		{"float32", float32(1.5), Float(1.5)},
		{"float64", float64(2.25), Float(2.25)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustFromGo(t, tt.in)
			if got.Kind != tt.want.Kind || !Equal(got, tt.want) {
				t.Fatalf("FromGo(%#v) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

// TestFromGoPointerFollow follows a non-nil pointer to its element.
func TestFromGoPointerFollow(t *testing.T) {
	n := 42
	got := mustFromGo(t, &n)
	if got.Kind != KInt || got.I != 42 {
		t.Fatalf("FromGo(&42) = %+v, want Int(42)", got)
	}
	s := "deep"
	pp := &s
	got = mustFromGo(t, &pp)
	if got.Kind != KStr || got.S != "deep" {
		t.Fatalf("FromGo(**string) = %+v, want Str(deep)", got)
	}
}

// TestFromGoSlice marshals a slice into a list-shaped Array in element order,
// and a nil slice into Null.
func TestFromGoSlice(t *testing.T) {
	got := mustFromGo(t, []string{"a", "b", "c"})
	want := NewList(Str("a"), Str("b"), Str("c"))
	if got.Kind != KArray || !Equal(got, Arr(want)) {
		t.Fatalf("FromGo(slice) = %+v, want list a,b,c", got)
	}
	if !got.Arr.IsList() {
		t.Fatalf("slice did not marshal to a list-shaped array")
	}

	var nilSlice []int
	if g := mustFromGo(t, nilSlice); g.Kind != KNull {
		t.Fatalf("FromGo(nil slice) = %+v, want Null", g)
	}

	// []any mixes kinds and passes each element through FromGo.
	mixed := mustFromGo(t, []any{1, "two", true, 3.5})
	wantMixed := NewList(Int(1), Str("two"), Bool(true), Float(3.5))
	if !Equal(mixed, Arr(wantMixed)) {
		t.Fatalf("FromGo([]any) = %+v, want 1,two,true,3.5", mixed)
	}
}

// TestFromGoArray marshals a fixed-size Go array like a slice.
func TestFromGoArray(t *testing.T) {
	got := mustFromGo(t, [3]int{10, 20, 30})
	want := NewList(Int(10), Int(20), Int(30))
	if !Equal(got, Arr(want)) {
		t.Fatalf("FromGo([3]int) = %+v, want 10,20,30", got)
	}
}

// TestFromGoMapDeterministic marshals a map into a string-keyed Array with a
// deterministic, sorted key order, stable across repeated calls.
func TestFromGoMapDeterministic(t *testing.T) {
	m := map[string]any{"gamma": 3, "alpha": 1, "beta": 2}
	first := mustFromGo(t, m)
	if first.Kind != KArray {
		t.Fatalf("FromGo(map) kind = %s, want array", first.Kind)
	}
	keys := first.Arr.Keys()
	wantOrder := []string{"alpha", "beta", "gamma"}
	if len(keys) != len(wantOrder) {
		t.Fatalf("got %d keys, want %d", len(keys), len(wantOrder))
	}
	for i, k := range keys {
		if k.Kind != KStr || k.S != wantOrder[i] {
			t.Fatalf("key[%d] = %+v, want %q", i, k, wantOrder[i])
		}
	}
	// Repeat many times: sorted order must never vary despite Go's randomized
	// map iteration.
	for n := 0; n < 50; n++ {
		g := mustFromGo(t, m)
		gk := g.Arr.Keys()
		for i, k := range gk {
			if k.S != wantOrder[i] {
				t.Fatalf("run %d: key[%d] = %q, want %q", n, i, k.S, wantOrder[i])
			}
		}
	}
}

// TestFromGoMapTypedValues marshals a map[string]T (non-any element type).
func TestFromGoMapTypedValues(t *testing.T) {
	m := map[string]int{"x": 1, "y": 2}
	got := mustFromGo(t, m)
	if v, ok := got.Arr.GetStr("x"); !ok || v.Kind != KInt || v.I != 1 {
		t.Fatalf("map[string]int x = %+v ok=%v, want Int(1)", v, ok)
	}
}

// TestFromGoMapIntKeys marshals an integer-keyed map through the canonical key
// model so a "0".."n-1" map is list-shaped.
func TestFromGoMapIntKeys(t *testing.T) {
	m := map[int]string{0: "a", 1: "b", 2: "c"}
	got := mustFromGo(t, m)
	if !got.Arr.IsList() {
		t.Fatalf("int-keyed 0..2 map did not marshal list-shaped: %+v", got.Arr.Keys())
	}
	if v, ok := got.Arr.GetInt(1); !ok || v.S != "b" {
		t.Fatalf("GetInt(1) = %+v ok=%v, want Str(b)", v, ok)
	}
}

// TestFromGoStruct marshals exported fields honoring quill and json tags, in
// declaration order, skipping unexported and '-' fields.
func TestFromGoStruct(t *testing.T) {
	type person struct {
		Name    string `quill:"name"`
		ID      int    `json:"id"`
		Email   string `json:"email,omitempty"`
		Plain   bool
		Skipped string `quill:"-"`
		JSkip   string `json:"-"`
		hidden  string //nolint:unused // exercises unexported-field skipping
	}
	p := person{Name: "ada", ID: 7, Email: "a@x", Plain: true, Skipped: "no", JSkip: "no", hidden: "no"}
	got := mustFromGo(t, p)
	if got.Kind != KArray {
		t.Fatalf("FromGo(struct) kind = %s, want array", got.Kind)
	}
	checks := []struct {
		key  string
		want Value
	}{
		{"name", Str("ada")},
		{"id", Int(7)},
		{"email", Str("a@x")},
		{"Plain", Bool(true)},
	}
	for _, c := range checks {
		v, ok := got.Arr.GetStr(c.key)
		if !ok || !Equal(v, c.want) {
			t.Fatalf("struct[%q] = %+v ok=%v, want %+v", c.key, v, ok, c.want)
		}
	}
	if _, ok := got.Arr.GetStr("Skipped"); ok {
		t.Fatalf("quill:\"-\" field should be skipped")
	}
	if _, ok := got.Arr.GetStr("JSkip"); ok {
		t.Fatalf("json:\"-\" field should be skipped")
	}
	if _, ok := got.Arr.GetStr("hidden"); ok {
		t.Fatalf("unexported field should be skipped")
	}
	// Declaration order is preserved.
	keys := got.Arr.Keys()
	wantOrder := []string{"name", "id", "email", "Plain"}
	if len(keys) != len(wantOrder) {
		t.Fatalf("got %d keys %v, want %d", len(keys), keys, len(wantOrder))
	}
	for i, k := range keys {
		if k.S != wantOrder[i] {
			t.Fatalf("key[%d] = %q, want %q", i, k.S, wantOrder[i])
		}
	}
}

// TestFromGoStructEmbedded flattens an embedded anonymous struct in place.
func TestFromGoStructEmbedded(t *testing.T) {
	type base struct {
		A int `quill:"a"`
	}
	type derived struct {
		base
		B int `quill:"b"`
	}
	got := mustFromGo(t, derived{base: base{A: 1}, B: 2})
	if v, ok := got.Arr.GetStr("a"); !ok || v.I != 1 {
		t.Fatalf("embedded field a = %+v ok=%v, want Int(1)", v, ok)
	}
	if v, ok := got.Arr.GetStr("b"); !ok || v.I != 2 {
		t.Fatalf("field b = %+v ok=%v, want Int(2)", v, ok)
	}
}

// TestFromGoNested marshals nested combinations of struct/slice/map and asserts
// parity with the equivalent hand-built runtime.Value.
func TestFromGoNested(t *testing.T) {
	type addr struct {
		City string `quill:"city"`
		Zip  string `quill:"zip"`
	}
	type user struct {
		Name  string            `quill:"name"`
		Tags  []string          `quill:"tags"`
		Addr  addr              `quill:"addr"`
		Meta  map[string]int    `quill:"meta"`
		Extra map[string]string `quill:"extra"`
	}
	u := user{
		Name:  "ada",
		Tags:  []string{"x", "y"},
		Addr:  addr{City: "here", Zip: "00000"},
		Meta:  map[string]int{"age": 30},
		Extra: map[string]string{"k": "v"},
	}
	got := mustFromGo(t, u)

	// Hand-build the equivalent value.
	want := NewArray()
	want.SetStr("name", Str("ada"))
	want.SetStr("tags", Arr(NewList(Str("x"), Str("y"))))
	inner := NewArray()
	inner.SetStr("city", Str("here"))
	inner.SetStr("zip", Str("00000"))
	want.SetStr("addr", Arr(inner))
	meta := NewArray()
	meta.SetStr("age", Int(30))
	want.SetStr("meta", Arr(meta))
	extra := NewArray()
	extra.SetStr("k", Str("v"))
	want.SetStr("extra", Arr(extra))

	if !Equal(got, Arr(want)) {
		t.Fatalf("nested FromGo mismatch:\n got %+v\nwant %+v", got.Arr.Pairs(), want.Pairs())
	}
}

// TestFromGoPassthrough leaves an existing runtime.Value (and *Array) untouched,
// including when carried inside a native container.
func TestFromGoPassthrough(t *testing.T) {
	orig := Str("kept")
	if got := mustFromGo(t, orig); got.Kind != KStr || got.S != "kept" {
		t.Fatalf("Value passthrough = %+v, want Str(kept)", got)
	}
	arr := NewList(Int(1), Int(2))
	if got := mustFromGo(t, arr); got.Kind != KArray || !Equal(got, Arr(arr)) {
		t.Fatalf("*Array passthrough = %+v", got)
	}
	// A slice of Values passes each element straight through.
	got := mustFromGo(t, []any{Str("a"), Int(5), Bool(true)})
	want := NewList(Str("a"), Int(5), Bool(true))
	if !Equal(got, Arr(want)) {
		t.Fatalf("[]any of Values = %+v, want a,5,true", got)
	}
	// A map whose value is a hand-built Value passes it through.
	m := mustFromGo(t, map[string]any{"v": Bool(false)})
	if v, ok := m.Arr.GetStr("v"); !ok || v.Kind != KBool || v.B {
		t.Fatalf("map Value passthrough = %+v ok=%v, want Bool(false)", v, ok)
	}
}

// fromGoCallable is a Callable Object used to prove a registered callable passes
// through FromGo as an Object.
type fromGoCallable struct{}

func (fromGoCallable) GetField(string) (Value, bool)             { return Null(), false }
func (fromGoCallable) CallMethod(string, []Value) (Value, error) { return Null(), nil }
func (fromGoCallable) Invoke([]Value) (Value, error)             { return Str("called"), nil }

// TestFromGoObjectPassthrough passes a host Object (here also a Callable)
// straight through as KObject.
func TestFromGoObjectPassthrough(t *testing.T) {
	var obj Object = fromGoCallable{}
	got := mustFromGo(t, obj)
	if got.Kind != KObject {
		t.Fatalf("Object passthrough kind = %s, want object", got.Kind)
	}
	if !IsCallable(got) {
		t.Fatalf("callable Object should stay callable after FromGo")
	}
}

// TestFromGoUnsupported returns a clear typed error on each unsupported kind.
func TestFromGoUnsupported(t *testing.T) {
	ch := make(chan int)
	fn := func() {}
	tests := []struct {
		name string
		in   any
	}{
		{"chan", ch},
		{"func", fn},
		{"complex64", complex64(1 + 2i)},
		{"complex128", complex128(3 + 4i)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := FromGo(tt.in)
			if err == nil {
				t.Fatalf("FromGo(%s) = nil error, want unsupported-kind error", tt.name)
			}
			qe, ok := err.(*errors.Error)
			if !ok {
				t.Fatalf("FromGo(%s) error type = %T, want *errors.Error", tt.name, err)
			}
			if qe.Kind != errors.KindRuntime {
				t.Fatalf("FromGo(%s) error kind = %v, want runtime", tt.name, qe.Kind)
			}
		})
	}
}

// TestFromGoUnsignedOverflow rejects a uint64 above the int64 ceiling.
func TestFromGoUnsignedOverflow(t *testing.T) {
	_, err := FromGo(uint64(math.MaxUint64))
	if err == nil {
		t.Fatalf("FromGo(MaxUint64) = nil error, want overflow error")
	}
}

// TestFromGoUnsupportedNested surfaces an unsupported element inside a container
// as an error rather than a partial value.
func TestFromGoUnsupportedNested(t *testing.T) {
	if _, err := FromGo([]any{1, make(chan int)}); err == nil {
		t.Fatalf("FromGo(slice with chan) = nil error, want error")
	}
	if _, err := FromGo(map[string]any{"ok": 1, "bad": func() {}}); err == nil {
		t.Fatalf("FromGo(map with func) = nil error, want error")
	}
	type holder struct {
		C chan int `quill:"c"`
	}
	if _, err := FromGo(holder{C: make(chan int)}); err == nil {
		t.Fatalf("FromGo(struct with chan field) = nil error, want error")
	}
}

// TestFromGoUnsupportedMapKey rejects a map key kind with no Quill spelling.
func TestFromGoUnsupportedMapKey(t *testing.T) {
	if _, err := FromGo(map[float64]int{1.5: 2}); err == nil {
		t.Fatalf("FromGo(map[float64]int) = nil error, want key error")
	}
}
