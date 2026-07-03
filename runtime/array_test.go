package runtime

import "testing"

func TestArrayKeyCanonicalization(t *testing.T) {
	// "1" and the integer 1 address the SAME slot; "01", "1.0", " 1", "+1",
	// "1e3" stay distinct string keys (spec 04 Section 6.1).
	tests := []struct {
		name      string
		strKey    string
		wantInt   bool
		sameAsInt int64 // only checked when wantInt
	}{
		{"plain one", "1", true, 1},
		{"zero", "0", true, 0},
		{"negative", "-3", true, -3},
		{"leading zero", "01", false, 0},
		{"float-ish", "1.0", false, 0},
		{"leading space", " 1", false, 0},
		{"plus sign", "+1", false, 0},
		{"exponent", "1e3", false, 0},
		{"word", "x", false, 0},
		{"empty", "", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewArray()
			a.SetStr(tt.strKey, Str("v"))
			keys := a.Keys()
			if len(keys) != 1 {
				t.Fatalf("want 1 key, got %d", len(keys))
			}
			gotInt := keys[0].Kind == KInt
			if gotInt != tt.wantInt {
				t.Fatalf("key %q: int=%v, want int=%v", tt.strKey, gotInt, tt.wantInt)
			}
			if tt.wantInt {
				// The integer subscript must hit the same slot.
				if _, ok := a.GetInt(tt.sameAsInt); !ok {
					t.Fatalf("GetInt(%d) missed slot set via %q", tt.sameAsInt, tt.strKey)
				}
				if keys[0].I != tt.sameAsInt {
					t.Fatalf("canon key = %d, want %d", keys[0].I, tt.sameAsInt)
				}
			}
		})
	}
}

func TestArrayStringIntSameSlot(t *testing.T) {
	a := NewArray()
	a.SetInt(1, Str("by-int"))
	if v, ok := a.GetStr("1"); !ok || v.S != "by-int" {
		t.Fatalf(`GetStr("1") = %v, %v; want by-int`, v, ok)
	}
	a.SetStr("1", Str("by-str"))
	if a.Len() != 1 {
		t.Fatalf("string 1 created a second slot; len=%d", a.Len())
	}
	if v, _ := a.GetInt(1); v.S != "by-str" {
		t.Fatalf("GetInt(1) = %v; want by-str", v)
	}
	// "01" is a distinct string slot.
	a.SetStr("01", Str("distinct"))
	if a.Len() != 2 {
		t.Fatalf(`"01" did not create a distinct slot; len=%d`, a.Len())
	}
}

func TestArrayInsertionOrder(t *testing.T) {
	a := NewArray()
	a.SetStr("b", Int(1))
	a.SetInt(5, Int(2))
	a.SetStr("a", Int(3))
	a.SetInt(0, Int(4))
	a.SetStr("b", Int(99)) // update keeps original position
	keys := a.Keys()
	wantKinds := []Kind{KStr, KInt, KStr, KInt}
	wantText := []string{"b", "5", "a", "0"}
	if len(keys) != 4 {
		t.Fatalf("len=%d, want 4", len(keys))
	}
	for i, k := range keys {
		if k.Kind != wantKinds[i] {
			t.Errorf("key %d kind=%v, want %v", i, k.Kind, wantKinds[i])
		}
		got, _ := ToText(k)
		if got != wantText[i] {
			t.Errorf("key %d text=%q, want %q", i, got, wantText[i])
		}
	}
	if v, _ := a.GetStr("b"); v.I != 99 {
		t.Errorf("updated b = %d, want 99", v.I)
	}
}

func TestArrayIsList(t *testing.T) {
	tests := []struct {
		name  string
		build func() *Array
		want  bool
	}{
		{"empty is a list", NewArray, true},
		{"contiguous from 0", func() *Array { return NewList(Int(1), Int(2), Int(3)) }, true},
		{"gap", func() *Array {
			a := NewArray()
			a.SetInt(0, Int(1))
			a.SetInt(2, Int(2))
			return a
		}, false},
		{"not from zero", func() *Array {
			a := NewArray()
			a.SetInt(1, Int(1))
			return a
		}, false},
		{"string key present", func() *Array {
			a := NewArray()
			a.SetInt(0, Int(1))
			a.SetStr("x", Int(2))
			return a
		}, false},
		{"out of order insertion", func() *Array {
			a := NewArray()
			a.SetInt(1, Int(1))
			a.SetInt(0, Int(0))
			return a
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.build().IsList(); got != tt.want {
				t.Fatalf("IsList = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestArrayPairAt(t *testing.T) {
	a := NewArray()
	a.SetStr("b", Int(1))
	a.SetInt(5, Int(2))
	a.SetStr("01", Int(3))
	a.SetInt(-1, Int(4))
	a.SetInt(1000, Int(5))

	// PairAt(i) must be exactly Pairs()[i], entry for entry.
	pairs := a.Pairs()
	for i := range pairs {
		k, v := a.PairAt(i)
		if !Equal(k, pairs[i].Key) || k.Kind != pairs[i].Key.Kind {
			t.Errorf("PairAt(%d) key = %v, want %v", i, k, pairs[i].Key)
		}
		if !Equal(v, pairs[i].Val) {
			t.Errorf("PairAt(%d) val = %v, want %v", i, v, pairs[i].Val)
		}
	}

	// The indexed accessor exists so live loop iteration never allocates,
	// covering both the interned and the wide integer-key reconstruction.
	allocs := testing.AllocsPerRun(100, func() {
		for i := 0; i < a.Len(); i++ {
			_, _ = a.PairAt(i)
		}
	})
	if allocs != 0 {
		t.Fatalf("PairAt allocates %v times per sweep, want 0", allocs)
	}
}

// TestArrayPairsInto pins the buffer-reuse contract of PairsInto: it produces
// the same entries in the same order as Pairs(), reuses a supplied buffer's
// backing array when the capacity suffices (the loop snapshot pool relies on
// this to stay allocation-free in steady state), and correctly shrinks when a
// smaller array is materialized into a larger buffer.
func TestArrayPairsInto(t *testing.T) {
	a := NewArray()
	a.SetStr("b", Int(1))
	a.SetInt(5, Int(2))
	a.SetStr("01", Int(3))
	a.SetInt(-1, Int(4))
	a.SetInt(1000, Int(5))

	want := a.Pairs()
	// PairsInto(nil) equals Pairs() entry for entry.
	got := a.PairsInto(nil)
	if len(got) != len(want) {
		t.Fatalf("PairsInto(nil) length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !Equal(got[i].Key, want[i].Key) || got[i].Key.Kind != want[i].Key.Kind || !Equal(got[i].Val, want[i].Val) {
			t.Errorf("PairsInto(nil)[%d] = %v, want %v", i, got[i], want[i])
		}
	}

	// Reusing a buffer with enough capacity must not allocate and must yield the
	// same entries, proving the pool's recycled buffer path is allocation-free.
	buf := make([]Pair, 0, a.Len())
	allocs := testing.AllocsPerRun(100, func() {
		buf = a.PairsInto(buf)
	})
	if allocs != 0 {
		t.Fatalf("PairsInto into a sized buffer allocates %v times, want 0", allocs)
	}
	if len(buf) != len(want) {
		t.Fatalf("reused PairsInto length = %d, want %d", len(buf), len(want))
	}
	for i := range want {
		if !Equal(buf[i].Val, want[i].Val) {
			t.Errorf("reused PairsInto[%d].Val = %v, want %v", i, buf[i].Val, want[i].Val)
		}
	}

	// A smaller array materialized into the larger buffer shrinks to its own
	// length; the tail beyond len is stale but out of range.
	small := NewList(Str("x"), Str("y"))
	buf = small.PairsInto(buf)
	if len(buf) != 2 || buf[0].Val.S != "x" || buf[1].Val.S != "y" {
		t.Fatalf("PairsInto did not shrink to the smaller array: %v", buf)
	}
}

func TestArrayCloneIsDeepValueCopy(t *testing.T) {
	inner := NewList(Int(1), Int(2))
	outer := NewArray()
	outer.SetStr("nested", Arr(inner))
	outer.SetStr("scalar", Int(7))

	clone := outer.Clone()
	// Mutating the clone's nested array must not touch the original.
	cv, _ := clone.GetStr("nested")
	cv.Arr.SetInt(0, Int(999))

	ov, _ := outer.GetStr("nested")
	if first, _ := ov.Arr.GetInt(0); first.I != 1 {
		t.Fatalf("clone mutation leaked into original: got %d, want 1", first.I)
	}
}
