package runtime

import (
	"github.com/avmnusng/quill-template-engine/errors"
)

// Equal is Quill's ONE typed equality (spec 04 Section 3.1 / 4.1). It is
// reflexive, symmetric, total, and performs NO coercion. The rules:
//
//   - Safe normalizes to its wrapped Str before comparison (the second
//     cross-kind bridge), so Safe("x") == "x" and Safe("x") == Safe("x").
//   - Same-kind values compare by value.
//   - Int and Float bridge numerically (1 == 1.0 true), the only numeric bridge.
//   - *Array equality is structural: same length, same keys in the same order,
//     recursively Equal on the paired values.
//   - Object equality is identity, unless the host type provides an Equal hook.
//   - EVERY other cross-kind pair is false (1 == "1" false, null == false false).
//
// Equal never errors and never coerces; it backs ==, !=, ===, !==, and the
// array membership test `in`.
func Equal(a, b Value) bool {
	a = normalizeSafe(a)
	b = normalizeSafe(b)

	// The two cross-kind bridges live here; everything else requires same kind.
	if a.Kind != b.Kind {
		if isNumeric(a) && isNumeric(b) {
			return numericEqual(a, b)
		}
		return false
	}

	switch a.Kind {
	case KNull:
		return true
	case KBool:
		return a.B == b.B
	case KInt:
		return a.I == b.I
	case KFloat:
		return a.F == b.F
	case KStr:
		return a.S == b.S
	case KArray:
		return arrayEqual(a.Arr, b.Arr)
	case KObject:
		return objectEqual(a.Obj, b)
	default:
		return false
	}
}

func isNumeric(v Value) bool { return v.Kind == KInt || v.Kind == KFloat }

// numericEqual compares an Int/Float pair within the one number tower. Because
// non-finite floats never circulate (spec 04 Section 2.1), this is reflexive
// over the whole Float kind with no NaN special case.
func numericEqual(a, b Value) bool {
	return toFloat(a) == toFloat(b)
}

func toFloat(v Value) float64 {
	if v.Kind == KInt {
		return float64(v.I)
	}
	return v.F
}

func arrayEqual(a, b *Array) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Len() != b.Len() {
		return false
	}
	// Same keys in the SAME order, recursively Equal on the paired values.
	for i := range a.keys {
		ka, kb := a.keys[i], b.keys[i]
		if ka != kb || a.ints[ka] != b.ints[kb] {
			return false
		}
		if !Equal(a.vals[ka], b.vals[kb]) {
			return false
		}
	}
	return true
}

func objectEqual(o Object, other Value) bool {
	if eq, ok := o.(Equaler); ok {
		return eq.Equal(other)
	}
	// Identity: the same host instance. Two distinct values both wrapping the
	// same Object pointer are equal; different instances are not.
	if other.Kind != KObject {
		return false
	}
	return o == other.Obj
}

// Same is raw reference/kind identity, the `same(a, b)` builtin and the
// `is same as` test (spec 04 Section 4.1). Unlike Equal it does NOT normalize
// Safe and does NOT bridge Int/Float: it answers "is this literally the same
// value", kind-for-kind.
func Same(a, b Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KNull:
		return true
	case KBool:
		return a.B == b.B
	case KInt:
		return a.I == b.I
	case KFloat:
		return a.F == b.F
	case KStr, KSafe:
		return a.S == b.S
	case KArray:
		return a.Arr == b.Arr // pointer identity
	case KObject:
		return a.Obj == b.Obj // pointer identity
	default:
		return false
	}
}

// Order is Quill's ONE comparator (spec 04 Section 4.2), backing < > <= >= <=>,
// membership over strings, and sort/min/max. It returns -1, 0, or 1 plus a nil
// error when the comparison is defined, and a KindComparison error otherwise.
// It is total within the number tower and between two strings (byte-
// lexicographic), and defined NOWHERE across unlike kinds -- never a silent
// coercion. Safe orders as its wrapped Str.
func Order(a, b Value) (int, error) {
	a = normalizeSafe(a)
	b = normalizeSafe(b)

	if isNumeric(a) && isNumeric(b) {
		fa, fb := toFloat(a), toFloat(b)
		switch {
		case fa < fb:
			return -1, nil
		case fa > fb:
			return 1, nil
		default:
			return 0, nil
		}
	}
	if a.Kind == KStr && b.Kind == KStr {
		switch {
		case a.S < b.S:
			return -1, nil
		case a.S > b.S:
			return 1, nil
		default:
			return 0, nil
		}
	}
	return 0, errors.New(errors.KindComparison,
		"cannot order %s against %s", a.Kind, b.Kind)
}

// In is the membership operator `in` / `not in` (spec 04 Section 4.3). For an
// *Array haystack it is true iff some element is Equal to x under the one typed
// equality (so "1" in [1] is FALSE, 1 in [1] is true). For a Str haystack it is
// substring containment of x's ToText rendering. Any other haystack kind is a
// KindComparison error.
func In(x, haystack Value) (bool, error) {
	haystack = normalizeSafe(haystack)
	switch haystack.Kind {
	case KArray:
		if haystack.Arr == nil {
			return false, nil
		}
		for _, enc := range haystack.Arr.keys {
			if Equal(x, haystack.Arr.vals[enc]) {
				return true, nil
			}
		}
		return false, nil
	case KStr:
		needle, err := ToText(x)
		if err != nil {
			return false, err
		}
		return containsString(haystack.S, needle), nil
	default:
		return false, errors.New(errors.KindComparison,
			"cannot test membership in %s", haystack.Kind)
	}
}

// containsString reports whether sub is a byte-substring of s, including the
// empty-needle case (always true), matching strings.Contains without importing
// strings for this one use.
func containsString(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
