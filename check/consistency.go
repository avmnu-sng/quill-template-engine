package check

// consistent reports whether a value of static type s may flow into a context
// expecting type t -- the gradual consistency relation S ~ T of
// design/type-system.md Section 7.1, NOT subtyping. Consistency, not subtyping,
// governs flow:
//
//   - any ~ T and T ~ any for every T (the gradual rule; any is consistent with
//     everything in both directions).
//   - T ~ T (reflexive, structural).
//   - nominal/structural subtyping: Object<Sub> ~ Object<Super> across declared
//     edges; list<S> ~ list<T> when S ~ T; map<K,S> ~ map<K,T> when S ~ T;
//     T ~ T? and null ~ T?; S ~ (A|B) when S ~ A or S ~ B; (A|B) ~ T when each
//     arm is consistent with T.
//
// Consistency is deliberately NOT transitive through any (int ~ any and
// any ~ string both hold, but int ~ string does not): that non-transitivity is
// what lets any be a waypoint without collapsing the lattice.
func (c *checker) consistent(s, t *Type) bool {
	if s == nil {
		s = Any
	}
	if t == nil {
		t = Any
	}
	// The gradual rule: any is consistent with everything, both directions.
	if s.Kind == KAny || t.Kind == KAny {
		return true
	}
	if s.Kind == KNever {
		return true // the empty type flows anywhere (used as a join identity)
	}

	// A source union is consistent with t iff every arm is. This is the obligation
	// that a polymorphic value must be narrowed before single-arm use: a raw
	// `int | string` is NOT consistent with `int` (the string arm fails).
	if s.Kind == KUnion {
		for _, arm := range s.Union {
			if !c.consistent(arm, t) {
				return false
			}
		}
		return true
	}
	// A non-union source is consistent with a target union iff it matches any arm.
	if t.Kind == KUnion {
		for _, arm := range t.Union {
			if c.consistent(s, arm) {
				return true
			}
		}
		return false
	}

	if s.Kind != t.Kind {
		return false
	}
	switch s.Kind {
	case KObject:
		// Nominal flow across declared subtype/interface edges (Sub ~ Super).
		return c.reg.subtypeOf(s.Name, t.Name)
	case KList:
		return c.consistent(s.Elem, t.Elem)
	case KMap:
		return c.consistent(s.Key, t.Key) && c.consistent(s.Val, t.Val)
	case KArrow:
		if len(s.Params) != len(t.Params) {
			return false
		}
		// Parameters are checked invariantly-by-consistency (an any param accepts
		// anything), and the return covariantly. This is permissive enough for the
		// gradual arrow use the pipeline relies on.
		for i := range s.Params {
			if !c.consistent(s.Params[i], t.Params[i]) {
				return false
			}
		}
		return c.consistent(s.Ret, t.Ret)
	default:
		// Same scalar kind: reflexively consistent.
		return true
	}
}

// renderable reports whether a value of type t may be rendered to text at a
// {{ ... }} site or in a ~ concat (design/type-system.md Section 6.4). The
// renderable types are null, bool, int, float, string, and an Object whose host
// node declares a stringify hook; list and map are NOT renderable (rendering an
// *Array is the explicit runtime error, promoted to check time). A union is
// renderable iff EVERY arm is renderable -- the narrowing requirement. any is
// renderable (the check is inert under any; the runtime is the guard).
func (c *checker) renderable(t *Type) bool {
	if t == nil {
		return true
	}
	switch t.Kind {
	case KAny, KNull, KBool, KInt, KFloat, KString, KNever:
		return true
	case KObject:
		return c.reg.stringifies(t.Name)
	case KUnion:
		for _, a := range t.Union {
			if !c.renderable(a) {
				return false
			}
		}
		return true
	default:
		// list, map, arrow are not renderable.
		return false
	}
}

// iterableElem reports the element type a for-loop over a value of type t binds,
// and whether t is iterable at all (design/type-system.md Section 5.5). A list
// iterates its element; a map iterates its value (with the key as the second
// target); a string is iterable per the runtime; an Object iterates its declared
// element type; any is iterable as any. A scalar (int/bool/float/null) is NOT
// iterable -- a typed for over it is the promoted T4 runtime error.
func (c *checker) iterableElem(t *Type) (elem *Type, key *Type, ok bool) {
	if t == nil || t.Kind == KAny {
		return Any, Any, true
	}
	switch t.Kind {
	case KList:
		return t.Elem, Int, true
	case KMap:
		return t.Val, t.Key, true
	case KString:
		// A string iterates its characters as one-char strings (runtime iterate.go).
		return String, Int, true
	case KObject:
		e, can := c.reg.iterElem(t.Name)
		return e, Any, can
	case KUnion:
		// Iterable iff every arm is, joining the element types.
		var je, jk *Type = Never, Never
		for _, a := range t.Union {
			ae, ak, can := c.iterableElem(a)
			if !can {
				return nil, nil, false
			}
			je = join(je, ae)
			jk = join(jk, ak)
		}
		return je, jk, true
	default:
		return nil, nil, false
	}
}
