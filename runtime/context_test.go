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
	// Editing the clone's binding set does not leak to the parent...
	clone.Set("scalar", Int(99))
	if v, _ := c.Get("scalar"); v.I != 5 {
		t.Fatalf("clone rebind leaked: parent scalar = %d", v.I)
	}
	// ...and the cloned *Array is a deep value-copy.
	cv, _ := clone.Get("arr")
	cv.Arr.SetInt(0, Int(777))
	pv, _ := c.Get("arr")
	if first, _ := pv.Arr.GetInt(0); first.I != 1 {
		t.Fatalf("clone array mutation leaked into parent: %d", first.I)
	}
}
