package runtime

import (
	"testing"

	"github.com/avmnusng/quill-template-engine/errors"
)

func TestEnsureTraversableArray(t *testing.T) {
	a := NewArray()
	a.SetStr("b", Int(2))
	a.SetInt(0, Int(1))
	pairs, err := EnsureTraversable(Arr(a), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// insertion order preserved: "b" then 0
	if len(pairs) != 2 {
		t.Fatalf("len=%d, want 2", len(pairs))
	}
	if pairs[0].Key.Kind != KStr || pairs[0].Key.S != "b" {
		t.Fatalf("first key = %v, want str b", pairs[0].Key)
	}
	if pairs[1].Key.Kind != KInt || pairs[1].Key.I != 0 {
		t.Fatalf("second key = %v, want int 0", pairs[1].Key)
	}
}

func TestEnsureTraversableObject(t *testing.T) {
	o := &iterObj{
		fieldObj: newFieldObj("Seq", nil),
		pairs:    []Pair{{Key: Int(0), Val: Str("a")}},
	}
	pairs, err := EnsureTraversable(Obj(o), false)
	if err != nil || len(pairs) != 1 || pairs[0].Val.S != "a" {
		t.Fatalf("iterable object = %v, %v", pairs, err)
	}
}

func TestEnsureTraversableNonIterableStrict(t *testing.T) {
	for _, v := range []Value{Null(), Int(3), Str("x"), Bool(true), Obj(newFieldObj("Plain", nil))} {
		_, err := EnsureTraversable(v, false)
		if errors.KindOf(err) != errors.KindIteration {
			t.Fatalf("non-iterable %s: kind = %v, want iteration", v.Kind, errors.KindOf(err))
		}
	}
}

func TestEnsureTraversableNonIterableLenient(t *testing.T) {
	// Under lenient, a non-iterable yields zero pairs and no error.
	pairs, err := EnsureTraversable(Null(), true)
	if err != nil || len(pairs) != 0 {
		t.Fatalf("lenient non-iterable = %v, %v; want empty,nil", pairs, err)
	}
}

func TestSequenceMappingPredicates(t *testing.T) {
	tests := []struct {
		name    string
		v       Value
		wantSeq bool
		wantMap bool
	}{
		{"empty array is sequence", Arr(NewArray()), true, false},
		{"list is sequence", Arr(NewList(Int(1), Int(2))), true, false},
		{"non-list array is mapping", Arr(mapOf(t, "a", Int(1))), false, true},
		{"object is mapping", Obj(newFieldObj("T", nil)), false, true},
		{"scalar is neither", Int(3), false, false},
		{"null is neither", Null(), false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSequence(tt.v); got != tt.wantSeq {
				t.Errorf("IsSequence = %v, want %v", got, tt.wantSeq)
			}
			if got := IsMapping(tt.v); got != tt.wantMap {
				t.Errorf("IsMapping = %v, want %v", got, tt.wantMap)
			}
		})
	}
}
