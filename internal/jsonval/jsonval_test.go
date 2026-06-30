package jsonval

import (
	"testing"

	"github.com/avmnusng/quill-template-engine/runtime"
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
			if !runtime.Equal(got, c.want) || got.Kind != c.want.Kind {
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
	if v.Kind != runtime.KFloat {
		t.Errorf("3.0 should decode to a float, got %s", v.Kind)
	}
}

func TestDecodeNested(t *testing.T) {
	v, err := Decode([]byte(`{"name":"ada","tags":["x","y"],"age":30}`))
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != runtime.KArray {
		t.Fatalf("want array, got %s", v.Kind)
	}
	name, _ := v.Arr.GetStr("name")
	if name.Kind != runtime.KStr || name.S != "ada" {
		t.Errorf("name: %+v", name)
	}
	tags, _ := v.Arr.GetStr("tags")
	if tags.Kind != runtime.KArray || tags.Arr.Len() != 2 {
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
	if m["a"].Kind != runtime.KInt || m["b"].S != "two" {
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
		for _, p := range v.Arr.Pairs() {
			got = append(got, p.Key.S)
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

func TestTrailingData(t *testing.T) {
	if _, err := Decode([]byte(`{} extra`)); err == nil {
		t.Error("trailing data after the root must error")
	}
}
