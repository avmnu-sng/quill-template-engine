package runtime

// Truthy is Quill's ONE truthiness predicate (spec 04 Section 2.2 / design
// semantics.md Section 3), used by if, postfix if/unless, the ternary, the
// Elvis ?:, and the boolean operators. The falsy set is exactly five shapes:
//
//	Null   false   0 (Int)   0.0 (Float)   "" (empty Str)   [] (empty *Array)
//
// Everything else is truthy. In particular "0" is TRUTHY (a non-empty string),
// and any Object is truthy regardless of its internal state. A Safe value takes
// the truthiness of its wrapped content, so Safe("") is falsy.
func Truthy(v Value) bool {
	switch v.kind {
	case KNull:
		return false
	case KBool:
		return v.b
	case KInt:
		return v.i != 0
	case KFloat:
		return v.f != 0
	case KStr:
		return len(v.s) != 0
	case KArray:
		return v.arr != nil && v.arr.Len() != 0
	case KObject:
		return true
	case KSafe:
		return len(v.s) != 0
	default:
		return false
	}
}

// Empty is the one explicit length test backing the `is empty` test (spec 04
// Section 2.2 / design semantics.md Section 3). It is TOTAL over all eight
// kinds and is deliberately distinct from truthiness so emptiness never
// silently re-enters a boolean context:
//
//   - Null            -> true
//   - Str / *Array    -> true iff length 0
//   - Int/Float/Bool  -> false (so `0 is empty` is FALSE)
//   - Object          -> false (an Object is never empty here)
//   - Safe            -> the result for its unwrapped content
//
// Consequence: 0 is falsy but NOT empty and IS defined, so 0 | default("y")
// keeps 0.
func Empty(v Value) bool {
	switch v.kind {
	case KNull:
		return true
	case KStr:
		return len(v.s) == 0
	case KArray:
		return v.arr == nil || v.arr.Len() == 0
	case KSafe:
		return len(v.s) == 0
	case KInt, KFloat, KBool, KObject:
		return false
	default:
		return false
	}
}
