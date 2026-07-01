package check

import "testing"

// TestTypeString pins the surface rendering of every Type shape, including the
// `T?` nullable sugar and the arrow/union/map/list nesting used in diagnostics.
func TestTypeString(t *testing.T) {
	cases := []struct {
		typ  *Type
		want string
	}{
		{nil, "any"},
		{Any, "any"},
		{Null, "null"},
		{Bool, "bool"},
		{Int, "int"},
		{Float, "float"},
		{String, "string"},
		{Never, "never"},
		{ListOf(Int), "list<int>"},
		{ListOf(ListOf(String)), "list<list<string>>"},
		{MapOf(String, Int), "map<string, int>"},
		{ObjectOf("User"), "Object<\"User\">"},
		{ArrowOf(Bool), "() => bool"},
		{ArrowOf(Int, String, Bool), "(string, bool) => int"},
		// `T | null` renders as the `T?` sugar.
		{join(String, Null), "string?"},
		// a genuine multi-arm union sorts its arms for a stable string.
		{unionOf([]*Type{String, Int}), "int | string"},
	}
	for _, tc := range cases {
		if got := tc.typ.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
}

// TestAsNullable checks the exact `base | null` shape detection.
func TestAsNullable(t *testing.T) {
	// null in first arm.
	u := &Type{Kind: KUnion, Union: []*Type{Null, Int}}
	if b, ok := u.asNullable(); !ok || b.Kind != KInt {
		t.Fatalf("asNullable(null|int) = %v, %v", b, ok)
	}
	// null in second arm.
	u2 := &Type{Kind: KUnion, Union: []*Type{Int, Null}}
	if b, ok := u2.asNullable(); !ok || b.Kind != KInt {
		t.Fatalf("asNullable(int|null) = %v, %v", b, ok)
	}
	// three arms is not the nullable sugar.
	u3 := unionOf([]*Type{Int, String, Null})
	if _, ok := u3.asNullable(); ok {
		t.Fatalf("three-arm union must not be asNullable")
	}
	// a scalar is not asNullable.
	if _, ok := Int.asNullable(); ok {
		t.Fatalf("scalar must not be asNullable")
	}
}

// TestEqualType covers structural equality across every structured kind.
func TestEqualType(t *testing.T) {
	tt := []struct {
		a, b *Type
		want bool
	}{
		{nil, nil, true},
		{nil, Int, false},
		{Int, Int, true},
		{Int, Float, false},
		{ObjectOf("A"), ObjectOf("A"), true},
		{ObjectOf("A"), ObjectOf("B"), false},
		{ListOf(Int), ListOf(Int), true},
		{ListOf(Int), ListOf(String), false},
		{MapOf(String, Int), MapOf(String, Int), true},
		{MapOf(String, Int), MapOf(String, Bool), false},
		{ArrowOf(Int, String), ArrowOf(Int, String), true},
		{ArrowOf(Int, String), ArrowOf(Int, Bool), false},
		{ArrowOf(Int, String), ArrowOf(Int), false},
		{ArrowOf(Int, String), ArrowOf(Bool, String), false},
		{unionOf([]*Type{Int, String}), unionOf([]*Type{Int, String}), true},
		{unionOf([]*Type{Int, String}), unionOf([]*Type{Int, Bool}), false},
		{unionOf([]*Type{Int, String}), Int, false},
	}
	for i, c := range tt {
		if got := equalType(c.a, c.b); got != c.want {
			t.Errorf("case %d: equalType(%s, %s) = %v, want %v",
				i, c.a.String(), c.b.String(), got, c.want)
		}
	}
}

// TestJoinAndUnion checks the least-upper-bound rules: any absorbs, never is the
// identity, equal types collapse, and distinct scalars form a union.
func TestJoinAndUnion(t *testing.T) {
	if got := join(Int, Any); got.Kind != KAny {
		t.Errorf("join(int, any) must be any, got %s", got)
	}
	if got := join(Never, Int); got.Kind != KInt {
		t.Errorf("join(never, int) must be int, got %s", got)
	}
	if got := join(Int, Never); got.Kind != KInt {
		t.Errorf("join(int, never) must be int, got %s", got)
	}
	if got := join(Int, Int); got.Kind != KInt {
		t.Errorf("join(int, int) must be int, got %s", got)
	}
	if got := join(Int, String); got.String() != "int | string" {
		t.Errorf("join(int, string) = %s", got)
	}
	// unionOf absorbs any and dedups.
	if got := unionOf([]*Type{Int, Any, String}); got.Kind != KAny {
		t.Errorf("unionOf with any must be any, got %s", got)
	}
	if got := unionOf([]*Type{Int, Int}); got.Kind != KInt {
		t.Errorf("unionOf of duplicates collapses to the scalar, got %s", got)
	}
	if got := unionOf(nil); got.Kind != KNever {
		t.Errorf("unionOf(empty) is never, got %s", got)
	}
	// unionOf flattens a nested union.
	nested := unionOf([]*Type{unionOf([]*Type{Int, String}), Bool})
	if nested.String() != "bool | int | string" {
		t.Errorf("nested union flatten = %s", nested)
	}
}

// TestRemoveNull covers the null-subtraction used by ?? / ?: and is-not-null.
func TestRemoveNull(t *testing.T) {
	if got := removeNull(Any); got.Kind != KAny {
		t.Errorf("removeNull(any) = %s", got)
	}
	if got := removeNull(Null); got.Kind != KNever {
		t.Errorf("removeNull(null) = %s", got)
	}
	if got := removeNull(Int); got.Kind != KInt {
		t.Errorf("removeNull(int) = %s", got)
	}
	if got := removeNull(join(String, Null)); got.Kind != KString {
		t.Errorf("removeNull(string?) = %s", got)
	}
}

// TestConsistentRelation exercises the gradual consistency relation directly:
// any flows both ways, a raw union is not consistent with a single arm, list/map
// are element-wise, and nominal Object flow follows declared supertype edges.
func TestConsistentRelation(t *testing.T) {
	c := &checker{reg: nominalRegistry()}
	yes := func(s, t2 *Type) {
		if !c.consistent(s, t2) {
			t.Errorf("expected %s ~ %s", s.String(), t2.String())
		}
	}
	no := func(s, t2 *Type) {
		if c.consistent(s, t2) {
			t.Errorf("expected NOT %s ~ %s", s.String(), t2.String())
		}
	}
	yes(Any, Int)
	yes(Int, Any)
	yes(nil, Int) // nil defaults to any
	yes(Never, Int)
	yes(Int, Int)
	no(Int, String)
	// a source union is consistent with t iff every arm is.
	yes(unionOf([]*Type{Int, Int}), Int)
	no(unionOf([]*Type{Int, String}), Int)
	// a non-union source matches a target union if any arm matches.
	yes(Int, unionOf([]*Type{Int, String}))
	no(Bool, unionOf([]*Type{Int, String}))
	// list / map element-wise.
	yes(ListOf(Int), ListOf(Int))
	no(ListOf(Int), ListOf(String))
	yes(MapOf(String, Int), MapOf(String, Int))
	no(MapOf(String, Int), MapOf(String, Bool))
	// arrow: param count, param consistency, covariant return.
	yes(ArrowOf(Int, String), ArrowOf(Int, String))
	no(ArrowOf(Int, String), ArrowOf(Int))
	no(ArrowOf(Int, String), ArrowOf(Bool, String))
	// nominal Object flow across a declared supertype edge.
	yes(ObjectOf("Dog"), ObjectOf("Animal"))
	no(ObjectOf("Animal"), ObjectOf("Dog"))
}

// TestRenderableAndIterable checks the two collection predicates directly,
// including the union-requires-all rule and the Object stringify/elem hooks.
func TestRenderableAndIterable(t *testing.T) {
	c := &checker{reg: nominalRegistry()}
	// renderable.
	for _, r := range []*Type{Any, Null, Bool, Int, Float, String, Never} {
		if !c.renderable(r) {
			t.Errorf("%s must be renderable", r.String())
		}
	}
	if c.renderable(ListOf(Int)) || c.renderable(MapOf(String, Int)) {
		t.Errorf("list/map must not be renderable")
	}
	if !c.renderable(ObjectOf("Dog")) {
		t.Errorf("Dog stringifies, must be renderable")
	}
	if c.renderable(ObjectOf("Bag")) {
		t.Errorf("Bag has no stringify hook, must not be renderable")
	}
	// a union is renderable iff every arm is.
	if !c.renderable(unionOf([]*Type{Int, String})) {
		t.Errorf("int|string must be renderable")
	}
	if c.renderable(unionOf([]*Type{Int, ListOf(Int)})) {
		t.Errorf("int|list must not be renderable")
	}
	// iterableElem.
	elem, key, ok := c.iterableElem(ListOf(String))
	if !ok || elem.Kind != KString || key.Kind != KInt {
		t.Errorf("list<string> iterableElem = %s,%s,%v", elem, key, ok)
	}
	elem, key, ok = c.iterableElem(MapOf(String, Int))
	if !ok || elem.Kind != KInt || key.Kind != KString {
		t.Errorf("map iterableElem = %s,%s,%v", elem, key, ok)
	}
	if _, _, ok := c.iterableElem(Int); ok {
		t.Errorf("int must not be iterable")
	}
	if _, _, ok := c.iterableElem(ObjectOf("Bag")); !ok {
		t.Errorf("Bag has an elem type, must be iterable")
	}
	if _, _, ok := c.iterableElem(ObjectOf("Animal")); ok {
		t.Errorf("Animal has no elem type, must not be iterable")
	}
}
