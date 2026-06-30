package runtime

import "testing"

func TestEqual(t *testing.T) {
	objA := Obj(newFieldObj("T", nil))
	objAsame := objA // same wrapped pointer
	objB := Obj(newFieldObj("T", nil))

	tests := []struct {
		name string
		a, b Value
		want bool
	}{
		// same-kind by value
		{"null==null", Null(), Null(), true},
		{"true==true", Bool(true), Bool(true), true},
		{"true==false", Bool(true), Bool(false), false},
		{"int==int", Int(3), Int(3), true},
		{"int!=int", Int(3), Int(4), false},
		{"float==float", Float(1.5), Float(1.5), true},
		{"str==str", Str("a"), Str("a"), true},
		{"str!=str", Str("a"), Str("b"), false},

		// the ONE numeric bridge
		{"int==float bridge", Int(1), Float(1.0), true},
		{"float==int bridge", Float(2.0), Int(2), true},
		{"int!=float", Int(2), Float(2.5), false},

		// every other cross-kind pair is false, no coercion
		{`1 == "1" false`, Int(1), Str("1"), false},
		{`0 == "" false`, Int(0), Str(""), false},
		{"0 == false false", Int(0), Bool(false), false},
		{"true == 1 false", Bool(true), Int(1), false},
		{`null == "" false`, Null(), Str(""), false},
		{"null == false false", Null(), Bool(false), false},
		{`"" == [] false`, Str(""), Arr(NewArray()), false},
		{"null == 0 false", Null(), Int(0), false},

		// Safe normalizes to Str before compare (the second bridge)
		{`Safe("x") == "x"`, Safe("x"), Str("x"), true},
		{`Safe("x") == Safe("x")`, Safe("x"), Safe("x"), true},
		{`Safe("1") == 1 false`, Safe("1"), Int(1), false},

		// Object identity, and the host Equal hook
		{"object identity same", objA, objAsame, true},
		{"object identity differ", objA, objB, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Equal(tt.a, tt.b); got != tt.want {
				t.Fatalf("Equal(%v,%v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			// symmetry
			if got := Equal(tt.b, tt.a); got != tt.want {
				t.Fatalf("asymmetric: Equal(%v,%v) = %v, want %v", tt.b, tt.a, got, tt.want)
			}
		})
	}
}

func TestEqualObjectHook(t *testing.T) {
	a := Obj(&equalObj{fieldObj: newFieldObj("E", nil), id: 7})
	b := Obj(&equalObj{fieldObj: newFieldObj("E", nil), id: 7})
	c := Obj(&equalObj{fieldObj: newFieldObj("E", nil), id: 9})
	if !Equal(a, b) {
		t.Error("equal hook: same id should be equal despite distinct instances")
	}
	if Equal(a, c) {
		t.Error("equal hook: different id should be unequal")
	}
}

// TestEqualObjectHookAsymmetric pins that objectEqual consults the LEFT
// operand's Equaler hook against the right Value as-is. A hook that matches a
// different object type answers true left-to-right; the plain right operand has
// no hook, so the reverse routes through identity and is false. Equal as a
// whole is therefore only as symmetric as the host hooks make it -- the runtime
// does not symmetrize a one-sided host hook (spec 04 Section 4.1).
func TestEqualObjectHookAsymmetric(t *testing.T) {
	tagged := newFieldObj("Tag", map[string]Value{"id": Int(5)})
	left := Obj(&fieldMatchObj{fieldObj: newFieldObj("Matcher", nil), wantClass: "Tag"})
	right := Obj(tagged)

	if !Equal(left, right) {
		t.Fatal("left hook matching the right object's class should be equal")
	}
	// Reverse: right is a plain fieldObj with no hook -> identity, distinct
	// instances -> false. This asymmetry is the host's, not the runtime's.
	if Equal(right, left) {
		t.Fatal("plain right operand has no hook; reverse should fall to identity (false)")
	}
}

func TestEqualArrayStructural(t *testing.T) {
	tests := []struct {
		name string
		a, b *Array
		want bool
	}{
		{"lists equal", NewList(Int(1), Int(2)), NewList(Int(1), Int(2)), true},
		{"lists differ", NewList(Int(1), Int(2)), NewList(Int(1), Int(3)), false},
		{"length differs", NewList(Int(1)), NewList(Int(1), Int(2)), false},
		{
			"map order matters",
			mapOf(t, "a", Int(1), "b", Int(2)),
			mapOf(t, "b", Int(2), "a", Int(1)),
			false,
		},
		{
			"same map same order",
			mapOf(t, "a", Int(1), "b", Int(2)),
			mapOf(t, "a", Int(1), "b", Int(2)),
			true,
		},
		{
			"nested recursive",
			NewList(Arr(NewList(Int(1))), Int(2)),
			NewList(Arr(NewList(Int(1))), Int(2)),
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Equal(Arr(tt.a), Arr(tt.b)); got != tt.want {
				t.Fatalf("array Equal = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEqualArraySafeTransparentInElements(t *testing.T) {
	// An array containing a Safe element compares equal to the same array with
	// the equivalent Str element (spec 04 Section 4.1).
	withSafe := NewList(Safe("x"), Int(1))
	withStr := NewList(Str("x"), Int(1))
	if !Equal(Arr(withSafe), Arr(withStr)) {
		t.Fatal("Safe element should be transparent in structural array equality")
	}
}

func mapOf(t *testing.T, kvs ...any) *Array {
	t.Helper()
	a := NewArray()
	for i := 0; i < len(kvs); i += 2 {
		a.SetStr(kvs[i].(string), kvs[i+1].(Value))
	}
	return a
}

func TestSame(t *testing.T) {
	arr := NewList(Int(1))
	tests := []struct {
		name string
		a, b Value
		want bool
	}{
		{"int same", Int(1), Int(1), true},
		{"int!=float (no bridge)", Int(1), Float(1.0), false},
		{`Safe not normalized: Safe("x") vs "x"`, Safe("x"), Str("x"), false},
		{"same array pointer", Arr(arr), Arr(arr), true},
		{"distinct array pointers", Arr(NewList(Int(1))), Arr(NewList(Int(1))), false},
		{"null same", Null(), Null(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Same(tt.a, tt.b); got != tt.want {
				t.Fatalf("Same = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOrder(t *testing.T) {
	tests := []struct {
		name    string
		a, b    Value
		want    int
		wantErr bool
	}{
		{"2 < 2.5", Int(2), Float(2.5), -1, false},
		{"2.5 > 2", Float(2.5), Int(2), 1, false},
		{"equal numbers", Int(3), Float(3.0), 0, false},
		{`"a" < "b"`, Str("a"), Str("b"), -1, false},
		{`"abc" < "abd"`, Str("abc"), Str("abd"), -1, false},
		{"equal strings", Str("x"), Str("x"), 0, false},
		{"Safe orders as str", Safe("a"), Str("b"), -1, false},
		// Safe normalizes to Str on both sides: equal content orders as 0.
		{`Safe("x") vs Safe("x") equal`, Safe("x"), Safe("x"), 0, false},
		// Safe normalizes to Str, and Str-vs-number is defined nowhere.
		{`Safe("1") vs 1 error`, Safe("1"), Int(1), 0, true},
		{`1 vs "1" error`, Int(1), Str("1"), 0, true},
		{"null vs 0 error", Null(), Int(0), 0, true},
		{"bool vs bool error", Bool(true), Bool(false), 0, true},
		{"array vs array error", Arr(NewArray()), Arr(NewArray()), 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Order(tt.a, tt.b)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Order(%v,%v) want error, got %d", tt.a, tt.b, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Order(%v,%v) unexpected error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Fatalf("Order = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIn(t *testing.T) {
	arr := Arr(NewList(Int(1), Int(2), Int(3)))
	tests := []struct {
		name    string
		x, hay  Value
		want    bool
		wantErr bool
	}{
		{"1 in [1,2,3]", Int(1), arr, true, false},
		{"4 not in", Int(4), arr, false, false},
		// typed equality: no numeric-string bridge, so "1" in [1] is FALSE
		{`"1" in [1] false`, Str("1"), Arr(NewList(Int(1))), false, false},
		{"substring true", Str("ab"), Str("xabz"), true, false},
		{"substring false", Str("qq"), Str("xabz"), false, false},
		{"empty needle true", Str(""), Str("abc"), true, false},
		{"int needle into str renders", Int(2), Str("a2b"), true, false},
		// An un-renderable needle (an *Array) into a Str haystack surfaces the
		// ToText render error rather than a silent miss (spec 04 Section 4.3 /
		// Section 5: arrays do not render as text).
		{"array needle into str errors", Arr(NewList(Int(1))), Str("ab"), false, true},
		{"non-collection error", Int(1), Int(1), false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := In(tt.x, tt.hay)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("In want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("In unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("In = %v, want %v", got, tt.want)
			}
		})
	}
}
