package runtime

import (
	"reflect"
	"testing"
)

func TestContextGetSetHas(t *testing.T) {
	c := NewContext()
	if c.Has("x") {
		t.Fatal("empty context reports x present")
	}
	c.Set("x", Int(1))
	if v, ok := c.Get("x"); !ok || v.I != 1 {
		t.Fatalf("Get(x) = %v, %v", v, ok)
	}
	if !c.Has("x") {
		t.Fatal("x not present after Set")
	}
	// A name bound to Null is PRESENT (is defined tests presence, not value).
	c.Set("n", Null())
	if !c.Has("n") {
		t.Fatal("name bound to Null reported absent")
	}
}

func TestContextNamesInsertionOrder(t *testing.T) {
	c := NewContext()
	c.Set("users", Null())
	c.Set("name", Null())
	c.Set("title", Null())
	c.Set("users", Int(1)) // re-set keeps position
	if got := c.Names(); !reflect.DeepEqual(got, []string{"users", "name", "title"}) {
		t.Fatalf("Names = %v", got)
	}
}

func TestContextCloneValueCopyBoundary(t *testing.T) {
	c := NewContext()
	c.Set("arr", Arr(NewList(Int(1), Int(2))))
	c.Set("scalar", Int(5))

	clone := c.Clone()
	// Editing the clone's binding set does not leak to the parent: a rebind writes
	// into the clone's own map.
	clone.Set("scalar", Int(99))
	if v, _ := c.Get("scalar"); v.I != 5 {
		t.Fatalf("clone rebind leaked: parent scalar = %d", v.I)
	}
	// Clone shares the array copy-on-write: the clone and the parent hold the SAME
	// *Array pointer, both marked shared, so the copy is deferred until a mutation.
	cv, _ := clone.Get("arr")
	pv, _ := c.Get("arr")
	if cv.Arr != pv.Arr {
		t.Fatal("Clone should share the array pointer copy-on-write, not deep-copy it")
	}
	if !cv.Arr.shared {
		t.Fatal("a shared array must be marked shared after Clone")
	}
	// Own privatizes the shared array before an in-place mutation, so the write
	// does not leak to the parent -- the value semantics the interpreter's
	// assignment path relies on (proper copy-on-write, spec 04 Section 6.3).
	owned, copied := Own(cv)
	if !copied {
		t.Fatal("Own must clone a shared array")
	}
	owned.Arr.SetInt(0, Int(777))
	if first, _ := pv.Arr.GetInt(0); first.I != 1 {
		t.Fatalf("owned mutation leaked into parent: %d", first.I)
	}
	if first, _ := owned.Arr.GetInt(0); first.I != 777 {
		t.Fatalf("owned mutation lost: %d", first.I)
	}
}
