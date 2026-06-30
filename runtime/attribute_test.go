package runtime

import (
	"testing"

	"github.com/avmnusng/quill-template-engine/errors"
)

func TestGetAttributeDotArray(t *testing.T) {
	a := NewArray()
	a.SetStr("name", Str("ada"))
	recv := Arr(a)

	// present string key
	got, err := GetAttribute(recv, Str("name"), AccessDot, false)
	if err != nil || got.S != "ada" {
		t.Fatalf("u.name = %v, %v; want ada", got, err)
	}
	// absent under strict -> undefined error
	_, err = GetAttribute(recv, Str("nope"), AccessDot, false)
	if errors.KindOf(err) != errors.KindUndefined {
		t.Fatalf("absent dot key kind = %v, want undefined", errors.KindOf(err))
	}
	// absent under suppression -> Null, no error
	got, err = GetAttribute(recv, Str("nope"), AccessDot, true)
	if err != nil || !got.IsNull() {
		t.Fatalf("suppressed miss = %v, %v; want null,nil", got, err)
	}
}

func TestGetAttributeDotObject(t *testing.T) {
	o := newFieldObj("User", map[string]Value{"name": Str("grace")})
	recv := Obj(o)
	got, err := GetAttribute(recv, Str("name"), AccessDot, false)
	if err != nil || got.S != "grace" {
		t.Fatalf("obj.name = %v, %v", got, err)
	}
	_, err = GetAttribute(recv, Str("missing"), AccessDot, false)
	if errors.KindOf(err) != errors.KindUndefined {
		t.Fatalf("absent member kind = %v, want undefined", errors.KindOf(err))
	}
}

func TestGetAttributeDotScalarIsError(t *testing.T) {
	_, err := GetAttribute(Int(3), Str("b"), AccessDot, false)
	if errors.KindOf(err) != errors.KindAttribute {
		t.Fatalf("scalar dot kind = %v, want attribute", errors.KindOf(err))
	}
	// suppression does NOT rescue a kind that has no members structurally;
	// a scalar member access is an attribute error, not a strict miss.
	_, err = GetAttribute(Int(3), Str("b"), AccessDot, true)
	if err == nil {
		t.Fatal("scalar dot access should error even under suppression")
	}
}

func TestGetAttributeIndexArray(t *testing.T) {
	a := NewArray()
	a.SetInt(0, Str("zero"))
	a.SetStr("key", Str("val"))
	recv := Arr(a)

	if got, err := GetAttribute(recv, Int(0), AccessIndex, false); err != nil || got.S != "zero" {
		t.Fatalf("a[0] = %v, %v", got, err)
	}
	if got, err := GetAttribute(recv, Str("key"), AccessIndex, false); err != nil || got.S != "val" {
		t.Fatalf(`a["key"] = %v, %v`, got, err)
	}
	// "0" canonicalizes to the integer-0 slot
	if got, err := GetAttribute(recv, Str("0"), AccessIndex, false); err != nil || got.S != "zero" {
		t.Fatalf(`a["0"] = %v, %v`, got, err)
	}
}

func TestGetAttributeIndexBadKeyKinds(t *testing.T) {
	recv := Arr(NewList(Int(1)))
	for _, key := range []Value{Bool(true), Float(2.7), Null()} {
		_, err := GetAttribute(recv, key, AccessIndex, false)
		if errors.KindOf(err) != errors.KindKey {
			t.Fatalf("subscript with %s: kind = %v, want key", key.Kind, errors.KindOf(err))
		}
		// Even suppression does not turn a bad key KIND into a Null.
		if _, err := GetAttribute(recv, key, AccessIndex, true); err == nil {
			t.Fatalf("bad key kind %s should error under suppression too", key.Kind)
		}
	}
}

func TestGetAttributeIndexObject(t *testing.T) {
	o := &indexObj{
		fieldObj: newFieldObj("Map", nil),
		byKey:    map[string]Value{"timeout": Int(30)},
	}
	recv := Obj(o)
	if got, err := GetAttribute(recv, Str("timeout"), AccessIndex, false); err != nil || got.I != 30 {
		t.Fatalf(`obj["timeout"] = %v, %v`, got, err)
	}
	_, err := GetAttribute(recv, Str("absent"), AccessIndex, false)
	if errors.KindOf(err) != errors.KindUndefined {
		t.Fatalf("absent index kind = %v, want undefined", errors.KindOf(err))
	}
	// An object that does not implement Indexable cannot be subscripted.
	plain := Obj(newFieldObj("Plain", nil))
	if _, err := GetAttribute(plain, Str("x"), AccessIndex, false); errors.KindOf(err) != errors.KindAttribute {
		t.Fatalf("non-indexable kind = %v, want attribute", errors.KindOf(err))
	}
}

// TestGetAttributeNilArray pins the BLOCKING fix: a Value{Kind:KArray,Arr:nil}
// is a benign empty collection (like the Arr == nil guards in truthy/iterate/
// compare), so dotted/index access must return a clean strict-undefined miss
// (or Null under suppression), never panic.
func TestGetAttributeNilArray(t *testing.T) {
	nilArr := Value{Kind: KArray, Arr: nil}
	cases := []struct {
		name string
		key  Value
		kind AccessKind
	}{
		{"dot", Str("x"), AccessDot},
		{"index int", Int(0), AccessIndex},
		{"index str", Str("x"), AccessIndex},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// strict: a miss, not a panic
			_, err := GetAttribute(nilArr, tc.key, tc.kind, false)
			if errors.KindOf(err) != errors.KindUndefined {
				t.Fatalf("strict nil-array %s: kind = %v, want undefined", tc.name, errors.KindOf(err))
			}
			// suppressed: Null, no error
			got, err := GetAttribute(nilArr, tc.key, tc.kind, true)
			if err != nil || !got.IsNull() {
				t.Fatalf("suppressed nil-array %s = %v, %v; want null,nil", tc.name, got, err)
			}
		})
	}
}

// TestIsDefinedAttributeNilArray pins that presence on a nil *Array is false
// without dereferencing.
func TestIsDefinedAttributeNilArray(t *testing.T) {
	nilArr := Value{Kind: KArray, Arr: nil}
	if IsDefinedAttribute(nilArr, Str("x"), AccessDot) {
		t.Fatal("nil-array dot is defined should be false")
	}
	if IsDefinedAttribute(nilArr, Int(0), AccessIndex) {
		t.Fatal("nil-array index is defined should be false")
	}
}

func TestIsDefinedAttributePresenceNotValue(t *testing.T) {
	a := NewArray()
	a.SetStr("present", Null()) // present but Null
	recv := Arr(a)
	if !IsDefinedAttribute(recv, Str("present"), AccessDot) {
		t.Fatal("present-but-null key should be is defined = true")
	}
	if IsDefinedAttribute(recv, Str("absent"), AccessDot) {
		t.Fatal("absent key should be is defined = false")
	}
}
