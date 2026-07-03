package runtime

import (
	"testing"
	"unsafe"
)

// TestLoopInfoStaysIn64ByteClass pins the allocation size class of the
// per-iteration loop metadata object: parent is stored as a pointer at the
// per-loop entry-time probe exactly so the struct fits in 64 bytes, and a field
// added or widened past that line doubles the allocator bytes of every
// materialized loop iteration.
func TestLoopInfoStaysIn64ByteClass(t *testing.T) {
	if got := unsafe.Sizeof(loopInfo{}); got > 64 {
		t.Fatalf("loopInfo is %d bytes; the per-iteration object must stay in the 64-byte size class", got)
	}
}

// TestLoopSnapshotsStayEqualAndIndependent builds loop values for several
// iterations over one pair slice and one shared parent probe, the way a loop
// executes, and asserts each snapshot keeps its own step's fields while every
// snapshot's parent reads back the same entry-time bits. This is the runtime
// half of the frozen-capture contract: constructing later iterations must not
// disturb earlier captures even though they share the parent pointee.
func TestLoopSnapshotsStayEqualAndIndependent(t *testing.T) {
	outerPairs := []Pair{{Key: Int(0), Val: Str("x")}, {Key: Int(1), Val: Str("y")}}
	top := Null()
	parent := NewLoopValue(1, outerPairs, &top)

	pairs := []Pair{
		{Key: Int(0), Val: Str("a")},
		{Key: Int(1), Val: Str("b")},
		{Key: Int(2), Val: Str("c")},
	}
	snaps := make([]Value, 0, len(pairs))
	for i := range pairs {
		snaps = append(snaps, NewLoopValue(i, pairs, &parent))
	}

	field := func(v Value, name string) Value {
		t.Helper()
		got, ok := v.Obj.GetField(name)
		if !ok {
			t.Fatalf("loop.%s not resolved", name)
		}
		return got
	}
	for i, s := range snaps {
		if got := field(s, "index0"); got.Kind != KInt || got.I != int64(i) {
			t.Fatalf("snapshot %d drifted: index0 = %v", i, got)
		}
		if got := field(s, "first"); got.B != (i == 0) {
			t.Fatalf("snapshot %d drifted: first = %v", i, got)
		}
		if got := field(s, "last"); got.B != (i == len(pairs)-1) {
			t.Fatalf("snapshot %d drifted: last = %v", i, got)
		}
	}

	p0 := field(snaps[0], "parent")
	p2 := field(snaps[2], "parent")
	if p0.Kind != KObject || p0.Obj != parent.Obj || p2.Obj != parent.Obj {
		t.Fatalf("snapshot parents do not read the shared entry-time probe: %v / %v", p0, p2)
	}
	if got := field(p0, "index"); got.I != 2 {
		t.Fatalf("parent read through a snapshot drifted: index = %v", got)
	}
}

// TestLoopParentNilReadsNull pins the exported constructors as total for host
// callers: a nil parent pointer resolves loop.parent to Null, the same value a
// top-level loop's parent probe yields.
func TestLoopParentNilReadsNull(t *testing.T) {
	v := NewLoopValue(0, []Pair{{Key: Int(0), Val: Str("a")}}, nil)
	got, ok := v.Obj.GetField("parent")
	if !ok || got.Kind != KNull {
		t.Fatalf("nil parent must read as Null, got %v (ok=%v)", got, ok)
	}
}

// TestRecursiveLoopDepthFields pins the recursive constructor's extra field
// surface on the compact layout: depth/depth0 resolve only on a recursive
// level, and a plain loop reports them absent so a strict read raises
// undefined.
func TestRecursiveLoopDepthFields(t *testing.T) {
	pairs := []Pair{{Key: Int(0), Val: Str("a")}}
	top := Null()
	rec := NewRecursiveLoopValue(0, pairs, 3, &top)
	if got, ok := rec.Obj.GetField("depth"); !ok || got.I != 4 {
		t.Fatalf("recursive depth = %v (ok=%v), want 4", got, ok)
	}
	if got, ok := rec.Obj.GetField("depth0"); !ok || got.I != 3 {
		t.Fatalf("recursive depth0 = %v (ok=%v), want 3", got, ok)
	}
	plain := NewLoopValue(0, pairs, &top)
	if _, ok := plain.Obj.GetField("depth"); ok {
		t.Fatal("plain loop must not resolve depth")
	}
	if _, ok := plain.Obj.GetField("depth0"); ok {
		t.Fatal("plain loop must not resolve depth0")
	}
}
