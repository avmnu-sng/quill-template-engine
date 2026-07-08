package runtime

import "testing"

// mutBox is a tiny host Object supporting both member and subscript assignment,
// used to exercise SetMember/SetIndex against the FieldSetter/IndexSetter hooks.
type mutBox struct {
	fields map[string]Value
}

func newMutBox() *mutBox { return &mutBox{fields: map[string]Value{}} }

func (m *mutBox) GetField(name string) (Value, bool) {
	v, ok := m.fields[name]
	return v, ok
}

func (m *mutBox) CallMethod(string, []Value) (Value, error) { return Null(), nil }

func (m *mutBox) SetField(name string, v Value) error {
	m.fields[name] = v
	return nil
}

func (m *mutBox) GetIndex(key Value) (Value, bool) {
	v, ok := m.fields[key.AsStr()]
	return v, ok
}

func (m *mutBox) SetIndex(key, v Value) error {
	m.fields[key.AsStr()] = v
	return nil
}

// immBox is a host Object with no setter hooks: assignments against it error.
type immBox struct{}

func (immBox) GetField(string) (Value, bool)             { return Null(), false }
func (immBox) CallMethod(string, []Value) (Value, error) { return Null(), nil }

func TestSetMemberArray(t *testing.T) {
	a := NewArray()
	recv := Arr(a)
	if err := SetMember(recv, "name", Str("ada")); err != nil {
		t.Fatalf("SetMember: %v", err)
	}
	got, ok := a.GetStr("name")
	if !ok || got.AsStr() != "ada" {
		t.Fatalf("array member after set = %v (ok %v), want ada", got, ok)
	}
	// Overwrite in place.
	if err := SetMember(recv, "name", Str("bob")); err != nil {
		t.Fatalf("SetMember overwrite: %v", err)
	}
	got, _ = a.GetStr("name")
	if got.AsStr() != "bob" {
		t.Fatalf("array member after overwrite = %v, want bob", got)
	}
}

func TestSetMemberObject(t *testing.T) {
	box := newMutBox()
	recv := Obj(box)
	if err := SetMember(recv, "x", Int(7)); err != nil {
		t.Fatalf("SetMember: %v", err)
	}
	got, _ := GetAttribute(recv, Str("x"), AccessDot, false)
	if got.AsInt() != 7 {
		t.Fatalf("object member after set = %v, want 7", got)
	}
}

func TestSetMemberImmutableObject(t *testing.T) {
	if err := SetMember(Obj(immBox{}), "x", Int(1)); err == nil {
		t.Fatal("expected an error assigning a member of an immutable object")
	}
}

func TestSetMemberNonCollection(t *testing.T) {
	if err := SetMember(Int(1), "x", Int(1)); err == nil {
		t.Fatal("expected an error assigning a member of an int")
	}
	if err := SetMember(Arr(nil), "x", Int(1)); err == nil {
		t.Fatal("expected an error assigning a member of an empty (nil) array")
	}
}

func TestSetIndexArray(t *testing.T) {
	a := NewArray()
	recv := Arr(a)
	if err := SetIndex(recv, Int(0), Str("zero")); err != nil {
		t.Fatalf("SetIndex int: %v", err)
	}
	if err := SetIndex(recv, Str("k"), Str("v")); err != nil {
		t.Fatalf("SetIndex str: %v", err)
	}
	if got, _ := a.GetInt(0); got.AsStr() != "zero" {
		t.Fatalf("array[0] = %v, want zero", got)
	}
	if got, _ := a.GetStr("k"); got.AsStr() != "v" {
		t.Fatalf("array[k] = %v, want v", got)
	}
}

func TestSetIndexObject(t *testing.T) {
	box := newMutBox()
	if err := SetIndex(Obj(box), Str("k"), Int(3)); err != nil {
		t.Fatalf("SetIndex: %v", err)
	}
	if got, _ := box.GetField("k"); got.AsInt() != 3 {
		t.Fatalf("object[k] = %v, want 3", got)
	}
}

func TestSetIndexBadKey(t *testing.T) {
	a := NewArray()
	if err := SetIndex(Arr(a), Bool(true), Int(1)); err == nil {
		t.Fatal("expected an error subscripting with a bool key")
	}
}

func TestSetIndexUnsupported(t *testing.T) {
	if err := SetIndex(Obj(immBox{}), Str("k"), Int(1)); err == nil {
		t.Fatal("expected an error subscript-assigning an object without IndexSetter")
	}
	if err := SetIndex(Int(1), Str("k"), Int(1)); err == nil {
		t.Fatal("expected an error subscript-assigning an int")
	}
}
