package ext

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// TestSeparatorValue covers separator(): the returned value is callable, yields
// "" on the first call, and the separator on every call after, with independent
// state per separator.
func TestSeparatorValue(t *testing.T) {
	sep := callFn(t, "separator", runtime.Str(", "))
	if !runtime.IsCallable(sep) {
		t.Fatal("separator() should return a callable")
	}
	first, err := runtime.Call(sep, nil)
	if err != nil || first.Kind != runtime.KStr || first.S != "" {
		t.Fatalf("first call = %v (err %v), want empty string", first, err)
	}
	for i := 0; i < 3; i++ {
		v, err := runtime.Call(sep, nil)
		if err != nil || v.S != ", " {
			t.Fatalf("later call %d = %v (err %v), want %q", i, v, err, ", ")
		}
	}

	// A second separator has independent state (its first call is still empty).
	other := callFn(t, "separator", runtime.Str("|"))
	v, _ := runtime.Call(other, nil)
	if v.S != "" {
		t.Fatalf("independent separator first call = %q, want empty", v.S)
	}
}

// TestSeparatorDefault covers the default glue.
func TestSeparatorDefault(t *testing.T) {
	sep := callFn(t, "separator")
	_, _ = runtime.Call(sep, nil) // discard the leading empty
	v, _ := runtime.Call(sep, nil)
	if v.S != "," {
		t.Fatalf("default separator = %q, want %q", v.S, ",")
	}
}

// TestCellValue covers cell(): the value member reads and writes, the mutation is
// visible through the shared pointer, and the initial value is held.
func TestCellValue(t *testing.T) {
	c := callFn(t, "cell", runtime.Int(0))
	got, err := runtime.GetAttribute(c, runtime.Str("value"), runtime.AccessDot, false)
	if err != nil || got.Kind != runtime.KInt || got.I != 0 {
		t.Fatalf("initial value = %v (err %v), want 0", got, err)
	}

	if err := runtime.SetMember(c, "value", runtime.Int(42)); err != nil {
		t.Fatalf("SetMember: %v", err)
	}
	got, _ = runtime.GetAttribute(c, runtime.Str("value"), runtime.AccessDot, false)
	if got.I != 42 {
		t.Fatalf("after set, value = %v, want 42", got)
	}

	// A copy of the Value shares the same pointee, so the mutation is visible
	// through it -- the property that lets a cell survive a loop-scope clone.
	alias := runtime.CopyValue(c)
	got, _ = runtime.GetAttribute(alias, runtime.Str("value"), runtime.AccessDot, false)
	if got.I != 42 {
		t.Fatalf("aliased cell value = %v, want 42", got)
	}
}

// TestCellUnknownMember rejects reading or assigning a member other than value.
func TestCellUnknownMember(t *testing.T) {
	c := callFn(t, "cell", runtime.Null())
	if _, ok := c.Obj.GetField("other"); ok {
		t.Error("cell should expose only the value member")
	}
	if err := runtime.SetMember(c, "other", runtime.Int(1)); err == nil {
		t.Error("assigning an unknown cell member should error")
	}
}

// TestCellStringify renders the held value.
func TestCellStringify(t *testing.T) {
	c := callFn(t, "cell", runtime.Str("held"))
	got, err := runtime.ToText(c)
	if err != nil || got != "held" {
		t.Fatalf("cell stringify = %q (err %v), want %q", got, err, "held")
	}
}

// TestSetMemberImmutable rejects a member assignment against an immutable Object.
func TestSetMemberImmutable(t *testing.T) {
	sep := callFn(t, "separator", runtime.Str(","))
	if err := runtime.SetMember(sep, "value", runtime.Int(1)); err == nil {
		t.Error("assigning a member of a separator should error")
	}
}
