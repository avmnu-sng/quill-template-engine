package runtime

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/errors"
)

// TestIsScalarByKind pins the scalar/non-scalar partition IsScalar draws: the
// five scalar kinds (Null, Bool, Int, Float, Str) are scalars; the three
// reference/carrier kinds (Array, Object, Safe) are not. IsScalar backs the
// gradual checker's boundary cast, so a Safe carrier and a host Object landing
// on the scalar side would wrongly be trusted as already-checked.
func TestIsScalarByKind(t *testing.T) {
	scalars := []struct {
		name string
		v    Value
	}{
		{"null", Null()},
		{"bool", Bool(true)},
		{"int", Int(7)},
		{"float", Float(1.5)},
		{"str", Str("s")},
	}
	for _, c := range scalars {
		if !c.v.IsScalar() {
			t.Fatalf("IsScalar(%s) = false, want true", c.name)
		}
	}

	nonScalars := []struct {
		name string
		v    Value
	}{
		{"array", Arr(NewList(Int(1)))},
		{"object", Obj(newFieldObj("T", nil))},
		{"safe", Safe("<b>")},
	}
	for _, c := range nonScalars {
		if c.v.IsScalar() {
			t.Fatalf("IsScalar(%s) = true, want false", c.name)
		}
	}
}

// TestContextSetOwnedDoesNotShareArray contrasts SetOwned with Set: Set marks a
// bound array shared (copy-on-write), while SetOwned binds the caller-owned array
// WITHOUT the shared flag so a following in-place member write mutates it
// directly rather than paying a needless clone. Asserting the shared flag and the
// direct-mutation effect pins the exact semantics the member-assignment path
// depends on to stay linear.
func TestContextSetOwnedDoesNotShareArray(t *testing.T) {
	c := NewContext()

	shared := NewList(Int(1))
	c.Set("viaSet", Arr(shared))
	if v, ok := c.Get("viaSet"); !ok || !v.Arr.shared {
		t.Fatalf("Set must mark the array shared, got ok=%v shared=%v", ok, v.Arr.shared)
	}

	owned := NewList(Int(1))
	c.SetOwned("viaOwned", Arr(owned))
	v, ok := c.Get("viaOwned")
	if !ok {
		t.Fatal("SetOwned binding not found")
	}
	if v.Arr.shared {
		t.Fatal("SetOwned must NOT mark the array shared")
	}
	// The unshared array is owned, so Own is a no-op (no clone) and an in-place
	// write lands on the very same *Array the caller passed in.
	got, copied := Own(v)
	if copied {
		t.Fatal("Own must not clone an unshared (owned) array")
	}
	got.Arr.SetInt(0, Int(42))
	if first, _ := owned.GetInt(0); first.I != 42 {
		t.Fatalf("owned mutation did not reach the original array: %d", first.I)
	}
}

// TestContextSetOwnedScalarAndRebind pins that SetOwned records first-seen order
// like Set and re-binds in place, and that a scalar (no array) round-trips
// unchanged.
func TestContextSetOwnedScalarAndRebind(t *testing.T) {
	c := NewContext()
	c.SetOwned("a", Int(1))
	c.Set("b", Int(2))
	c.SetOwned("a", Int(9)) // rebind keeps position
	if v, ok := c.Get("a"); !ok || v.I != 9 {
		t.Fatalf("SetOwned rebind = %v, %v; want 9", v, ok)
	}
	names := c.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("SetOwned did not preserve first-seen order: %v", names)
	}
}

// TestLoopCursorAtReusesObjectAndRecomputes drives NewLoopCursor + At the way the
// escape-proof loop path does: one boxed loop object is bound every step and At
// only advances the index. It asserts (a) the SAME object is returned every call
// (pointer identity), and (b) every derived field recomputes from the current
// index so the reused value reads exactly as a fresh NewLoopValue(i) would.
func TestLoopCursorAtReusesObjectAndRecomputes(t *testing.T) {
	pairs := []Pair{
		{Key: Int(0), Val: Str("a")},
		{Key: Int(1), Val: Str("b")},
		{Key: Int(2), Val: Str("c")},
	}
	cur := NewLoopCursor(pairs, nil)

	v0 := cur.At(0)
	v1 := cur.At(1)
	// Reuse: both calls hand back the identical boxed Object.
	if v0.Kind != KObject || v0.Obj != v1.Obj {
		t.Fatalf("At must return the same reused object, got %p vs %p", v0.Obj, v1.Obj)
	}

	field := func(v Value, name string) Value {
		t.Helper()
		got, ok := v.Obj.GetField(name)
		if !ok {
			t.Fatalf("loop.%s not resolved", name)
		}
		return got
	}
	for i := range pairs {
		v := cur.At(i)
		if got := field(v, "index0"); got.I != int64(i) {
			t.Fatalf("At(%d).index0 = %d", i, got.I)
		}
		if got := field(v, "index"); got.I != int64(i+1) {
			t.Fatalf("At(%d).index = %d", i, got.I)
		}
		if got := field(v, "length"); got.I != int64(len(pairs)) {
			t.Fatalf("At(%d).length = %d", i, got.I)
		}
		if got := field(v, "first"); got.B != (i == 0) {
			t.Fatalf("At(%d).first = %v", i, got.B)
		}
		if got := field(v, "last"); got.B != (i == len(pairs)-1) {
			t.Fatalf("At(%d).last = %v", i, got.B)
		}
		if got := field(v, "revindex0"); got.I != int64(len(pairs)-1-i) {
			t.Fatalf("At(%d).revindex0 = %d", i, got.I)
		}
		if got := field(v, "revindex"); got.I != int64(len(pairs)-i) {
			t.Fatalf("At(%d).revindex = %d", i, got.I)
		}
	}

	// prev/next read the neighbouring materialized pair at the current index.
	if got := field(cur.At(0), "prev"); got.Kind != KNull {
		t.Fatalf("At(0).prev = %v, want Null", got)
	}
	if got := field(cur.At(1), "prev"); got.Kind != KStr || got.S != "a" {
		t.Fatalf("At(1).prev = %v, want \"a\"", got)
	}
	if got := field(cur.At(1), "next"); got.Kind != KStr || got.S != "c" {
		t.Fatalf("At(1).next = %v, want \"c\"", got)
	}
	if got := field(cur.At(2), "next"); got.Kind != KNull {
		t.Fatalf("At(2).next = %v, want Null", got)
	}
}

// TestLoopCursorParentPointer pins that NewLoopCursor threads the entry-time
// parent probe through: a non-nil parent reads back the shared value, a nil
// parent reads Null (constructors total for host callers).
func TestLoopCursorParentPointer(t *testing.T) {
	top := Null()
	parent := NewLoopValue(0, []Pair{{Key: Int(0), Val: Str("x")}}, &top)
	cur := NewLoopCursor([]Pair{{Key: Int(0), Val: Str("a")}}, &parent)
	got, ok := cur.At(0).Obj.GetField("parent")
	if !ok || got.Kind != KObject || got.Obj != parent.Obj {
		t.Fatalf("cursor parent = %v (ok=%v), want the shared probe object", got, ok)
	}

	nilCur := NewLoopCursor([]Pair{{Key: Int(0), Val: Str("a")}}, nil)
	gotNil, okNil := nilCur.At(0).Obj.GetField("parent")
	if !okNil || gotNil.Kind != KNull {
		t.Fatalf("nil-parent cursor parent = %v (ok=%v), want Null", gotNil, okNil)
	}
}

// TestLoopGetIndexMatchesGetField pins loop["field"] against loop.field: a
// string subscript resolves the same field (present with ok true), an unknown
// string is absent, and a non-string key (Int here) is always absent -- only
// string subscripts name a loop field.
func TestLoopGetIndexMatchesGetField(t *testing.T) {
	pairs := []Pair{
		{Key: Int(0), Val: Str("a")},
		{Key: Int(1), Val: Str("b")},
	}
	v := NewLoopValue(1, pairs, nil)
	idx := v.Obj.(Indexable)

	got, ok := idx.GetIndex(Str("index"))
	if !ok || got.Kind != KInt || got.I != 2 {
		t.Fatalf("loop[\"index\"] = %v (ok=%v), want Int 2", got, ok)
	}
	// Matches dotted access exactly.
	dot, _ := v.Obj.GetField("index")
	if dot.I != got.I {
		t.Fatalf("loop[\"index\"]=%d disagrees with loop.index=%d", got.I, dot.I)
	}

	if _, ok := idx.GetIndex(Str("nope")); ok {
		t.Fatal("unknown loop subscript must be absent")
	}
	if _, ok := idx.GetIndex(Int(0)); ok {
		t.Fatal("a non-string loop subscript must be absent")
	}
}

// TestLoopCallMethodAlwaysErrors pins loop's method surface: loop exposes no
// callable members, so CallMethod returns an Attribute-kind error naming the
// method, for any name (changed is special-cased syntactically and never routes
// here).
func TestLoopCallMethodAlwaysErrors(t *testing.T) {
	v := NewLoopValue(0, []Pair{{Key: Int(0), Val: Str("a")}}, nil)

	// Two distinct names, plus the no-arg call, so the assertions pin that the
	// message interpolates the requested name (not a hardcoded "changed" literal)
	// and that every name -- including one with no special syntax and no args --
	// routes to the same Attribute-kind error returning Null.
	cases := []struct {
		name string
		args []Value
		want string
	}{
		{"changed", []Value{Int(1)}, "loop has no method \"changed\""},
		{"whatever", nil, "loop has no method \"whatever\""},
	}
	for _, tc := range cases {
		got, err := v.Obj.CallMethod(tc.name, tc.args)
		if err == nil {
			t.Fatalf("loop.CallMethod(%q) must error", tc.name)
		}
		if got.Kind != KNull {
			t.Fatalf("errored CallMethod(%q) must return Null, got %v", tc.name, got)
		}
		var qe *errors.Error
		if !asError(err, &qe) || qe.Kind != errors.KindAttribute {
			t.Fatalf("CallMethod(%q) error kind = %v, want KindAttribute (%v)", tc.name, err, errors.KindAttribute)
		}
		if qe.Msg != tc.want {
			t.Fatalf("CallMethod(%q) message = %q, want %q", tc.name, qe.Msg, tc.want)
		}
	}
}

// TestLoopClassNameIsLoop pins ClassName so an undefined-member error names the
// value "loop" rather than a generic object.
func TestLoopClassNameIsLoop(t *testing.T) {
	v := NewLoopValue(0, []Pair{{Key: Int(0), Val: Str("a")}}, nil)
	cn, ok := v.Obj.(ClassNamed)
	if !ok {
		t.Fatal("loop value must implement ClassNamed")
	}
	if got := cn.ClassName(); got != "loop" {
		t.Fatalf("ClassName = %q, want \"loop\"", got)
	}
}

// asError is a tiny local errors.As shim so the test can assert the concrete
// *errors.Error Kind without importing the standard errors package alongside the
// project's.
func asError(err error, target **errors.Error) bool {
	if e, ok := err.(*errors.Error); ok {
		*target = e
		return true
	}
	return false
}
