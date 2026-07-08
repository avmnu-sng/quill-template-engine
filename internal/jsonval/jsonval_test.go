package jsonval

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

func TestDecodeScalars(t *testing.T) {
	cases := []struct {
		in   string
		want runtime.Value
	}{
		{`null`, runtime.Null()},
		{`true`, runtime.Bool(true)},
		{`false`, runtime.Bool(false)},
		{`"hi"`, runtime.Str("hi")},
		{`3`, runtime.Int(3)},
		{`-42`, runtime.Int(-42)},
		{`3.5`, runtime.Float(3.5)},
		{`9223372036854775807`, runtime.Int(9223372036854775807)},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := Decode([]byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if !runtime.Equal(got, c.want) || got.Kind() != c.want.Kind() {
				t.Errorf("got %+v want %+v", got, c.want)
			}
		})
	}
}

// TestIntVsFloat pins the ToText-relevant distinction: an integral JSON number
// renders without a decimal point, a fractional one keeps it.
func TestIntVsFloat(t *testing.T) {
	v, err := Decode([]byte(`3`))
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := runtime.ToText(v); s != "3" {
		t.Errorf("integral number rendered %q, want 3", s)
	}
	v, err = Decode([]byte(`3.0`))
	if err != nil {
		t.Fatal(err)
	}
	// 3.0 has no integer literal form, so it stays a Float and renders "3".
	if v.Kind() != runtime.KFloat {
		t.Errorf("3.0 should decode to a float, got %s", v.Kind())
	}
}

func TestDecodeNested(t *testing.T) {
	v, err := Decode([]byte(`{"name":"ada","tags":["x","y"],"age":30}`))
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind() != runtime.KArray {
		t.Fatalf("want array, got %s", v.Kind())
	}
	name, _ := v.AsArray().GetStr("name")
	if name.Kind() != runtime.KStr || name.AsStr() != "ada" {
		t.Errorf("name: %+v", name)
	}
	tags, _ := v.AsArray().GetStr("tags")
	if tags.Kind() != runtime.KArray || tags.AsArray().Len() != 2 {
		t.Errorf("tags: %+v", tags)
	}
}

func TestDecodeMapRequiresObject(t *testing.T) {
	if _, err := DecodeMap([]byte(`[1,2,3]`)); err == nil {
		t.Error("a non-object root must be rejected")
	}
	m, err := DecodeMap([]byte(`{"a":1,"b":"two"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m["a"].Kind() != runtime.KInt || m["b"].AsStr() != "two" {
		t.Errorf("map: %+v", m)
	}
}

func TestDecodeInvalid(t *testing.T) {
	if _, err := Decode([]byte(`{bad`)); err == nil {
		t.Error("invalid json must error")
	}
}

// TestObjectOrderPreserved pins the order guarantee: object members iterate in
// source order, not the randomized order a Go map would give. Run it enough
// times that a map-backed decode would almost certainly have reordered.
func TestObjectOrderPreserved(t *testing.T) {
	const src = `{"z":1,"a":2,"m":3,"b":4,"y":5}`
	want := []string{"z", "a", "m", "b", "y"}
	for iter := 0; iter < 50; iter++ {
		v, err := Decode([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, p := range v.AsArray().Pairs() {
			got = append(got, p.Key.AsStr())
		}
		if len(got) != len(want) {
			t.Fatalf("len got %d want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d order: got %v want %v", iter, got, want)
			}
		}
	}
}

// TestObjectNumericKeysCanonicalize pins the spec 04 Section 7 key model at the
// JSON boundary: an object whose member names are canonical decimal integers
// becomes Int slots (so the array is list-shaped), while non-canonical names
// ("01", "name") stay Str keys. There is deliberately NO JSON-specific exception
// preserving such keys as strings -- the one key model holds everywhere.
func TestObjectNumericKeysCanonicalize(t *testing.T) {
	v, err := Decode([]byte(`{"0":"a","1":"b"}`))
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind() != runtime.KArray {
		t.Fatalf("want array, got %s", v.Kind())
	}
	keys := v.AsArray().Keys()
	if len(keys) != 2 {
		t.Fatalf("want 2 keys, got %d", len(keys))
	}
	for i, k := range keys {
		if k.Kind() != runtime.KInt || k.AsInt() != int64(i) {
			t.Errorf("key %d: got %+v, want Int(%d)", i, k, i)
		}
	}
	// All-integer 0..n-1 keys make the object list-shaped (is sequence true).
	if !v.AsArray().IsList() {
		t.Error("object with keys 0,1 should be list-shaped")
	}

	// A mixed object keeps non-canonical names as Str keys and is NOT a list.
	v, err = Decode([]byte(`{"0":"a","01":"b","name":"c"}`))
	if err != nil {
		t.Fatal(err)
	}
	keys = v.AsArray().Keys()
	if keys[0].Kind() != runtime.KInt || keys[0].AsInt() != 0 {
		t.Errorf("key 0: got %+v, want Int(0)", keys[0])
	}
	if keys[1].Kind() != runtime.KStr || keys[1].AsStr() != "01" {
		t.Errorf(`key "01": got %+v, want Str("01")`, keys[1])
	}
	if keys[2].Kind() != runtime.KStr || keys[2].AsStr() != "name" {
		t.Errorf(`key "name": got %+v, want Str("name")`, keys[2])
	}
	if v.AsArray().IsList() {
		t.Error("object with a non-integer key must not be list-shaped")
	}
}

func TestTrailingData(t *testing.T) {
	if _, err := Decode([]byte(`{} extra`)); err == nil {
		t.Error("trailing data after the root must error")
	}
}
