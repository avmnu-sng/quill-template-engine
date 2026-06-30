package check

import (
	"github.com/avmnusng/quill-template-engine/ast"
)

// exprType runs the bottom-up inference pass over one expression node and
// returns its static type, reporting the first type error found (design/
// type-system.md Section 6). Inference is local and predictable: a literal has
// its obvious type, an operator mirrors its runtime rule, and an `any` operand
// makes the result `any` with the operator's check deferred to the runtime. The
// checker only ever sharpens; it never invents a concrete type for an `any`
// leaf (Section 6.7), which is what makes an unannotated template type entirely
// as `any` and the checker silent.
func (c *checker) exprType(n *ast.Node, sc *scope) (*Type, error) {
	if n == nil {
		return Any, nil
	}
	switch n.Kind {
	case ast.KindInt:
		return Int, nil
	case ast.KindFloat:
		return Float, nil
	case ast.KindString:
		return String, nil
	case ast.KindBool:
		return Bool, nil
	case ast.KindNull:
		return Null, nil

	case ast.KindName:
		t, _ := sc.lookup(n.Str)
		return t, nil
	case ast.KindSpecialName:
		// _self/_context/_charset are engine values; treat as any.
		return Any, nil

	case ast.KindList:
		return c.listType(n, sc)
	case ast.KindMap:
		return c.mapType(n, sc)

	case ast.KindAttr:
		return c.attrType(n, sc)
	case ast.KindIndex:
		return c.indexType(n, sc)
	case ast.KindSlice:
		return c.sliceType(n, sc)

	case ast.KindCall:
		return c.callType(n, sc)
	case ast.KindFilter:
		return c.filterType(n, sc)

	case ast.KindUnary:
		return c.unaryType(n, sc)
	case ast.KindSpread:
		// In a value position a spread carries its source's element type; we type
		// the source and report it (the surrounding list/call assembles it).
		return c.exprType(n.Child(0), sc)

	case ast.KindBinary:
		return c.binaryType(n, sc)
	case ast.KindPower:
		return c.arithType(n, "**", sc)
	case ast.KindLogical:
		if _, err := c.exprType(n.Child(0), sc); err != nil {
			return Any, err
		}
		if _, err := c.exprType(n.Child(1), sc); err != nil {
			return Any, err
		}
		return Bool, nil
	case ast.KindMembership:
		return c.membershipType(n, sc)
	case ast.KindTest:
		// A test subject and any argument are typed; the test yields bool.
		// `is defined` is a whole-chain absence-suppression tool (spec 04 Section 6):
		// it is "true/false, never throws", so an absent member/name at any hop in
		// the subject must yield bool, not a check-time miss. Type it leniently --
		// like ?? / default -- swallowing absence while surfacing a genuine error.
		if n.Str == "defined" {
			if _, err := c.exprTypeLenient(n.Child(0), sc); err != nil {
				return Any, err
			}
		} else if _, err := c.exprType(n.Child(0), sc); err != nil {
			return Any, err
		}
		for _, ch := range n.Children[1:] {
			if ch.Kind == ast.KindArg {
				if _, err := c.exprType(ch.Child(0), sc); err != nil {
					return Any, err
				}
			}
		}
		return Bool, nil

	case ast.KindTernary:
		return c.ternaryType(n, sc)
	case ast.KindCoalesce:
		return c.coalesceType(n, sc)
	case ast.KindElvis:
		a, err := c.exprType(n.Child(0), sc)
		if err != nil {
			return Any, err
		}
		b, err := c.exprType(n.Child(1), sc)
		if err != nil {
			return Any, err
		}
		return join(a, b), nil

	case ast.KindArrow:
		// A bare arrow outside a piped position: type its body with params as any.
		return c.arrowType(n, sc, nil)
	}
	// Unknown expression node: be permissive (any), the dynamic floor decides.
	return Any, nil
}

// listType infers a sequence literal's type as list<join of element types>; an
// empty [] is list<any> (Section 6.1).
func (c *checker) listType(n *ast.Node, sc *scope) (*Type, error) {
	elem := Never
	for _, ch := range n.Children {
		var et *Type
		var err error
		if ch.Kind == ast.KindSpread {
			st, e := c.exprType(ch.Child(0), sc)
			if e != nil {
				return Any, e
			}
			// A spread of a list contributes its element type; otherwise any.
			if st != nil && st.Kind == KList {
				et = st.Elem
			} else {
				et = Any
			}
		} else {
			et, err = c.exprType(ch, sc)
			if err != nil {
				return Any, err
			}
		}
		elem = join(elem, et)
	}
	if elem.Kind == KNever {
		elem = Any
	}
	return ListOf(elem), nil
}

// mapType infers a mapping literal's type. Keys of name/computed entries are
// string (the canonical key kind for a literal); the value type is the join of
// entry values. An empty {} is map<any, any> (Section 6.1).
func (c *checker) mapType(n *ast.Node, sc *scope) (*Type, error) {
	keyT := Never
	valT := Never
	for _, e := range n.Children {
		if e.Kind != ast.KindMapEntry {
			continue
		}
		switch e.Int {
		case ast.MapEntryShorthand:
			vt, err := c.exprType(e.Child(0), sc)
			if err != nil {
				return Any, err
			}
			keyT = join(keyT, String)
			valT = join(valT, vt)
		case ast.MapEntryKeyed:
			vt, err := c.exprType(e.Child(1), sc)
			if err != nil {
				return Any, err
			}
			keyT = join(keyT, String)
			valT = join(valT, vt)
		case ast.MapEntryComputed:
			kt, err := c.exprType(e.Child(0), sc)
			if err != nil {
				return Any, err
			}
			vt, err := c.exprType(e.Child(1), sc)
			if err != nil {
				return Any, err
			}
			keyT = join(keyT, kt)
			valT = join(valT, vt)
		case ast.MapEntrySpread:
			if _, err := c.exprType(e.Child(0), sc); err != nil {
				return Any, err
			}
			keyT, valT = Any, Any
		}
	}
	if keyT.Kind == KNever {
		keyT = Any
	}
	if valT.Kind == KNever {
		valT = Any
	}
	return MapOf(keyT, valT), nil
}

// attrType infers "a.b" / "a?.b" (Section 6.2). On an Object<"T"> it reads the
// registered member type, reporting a check-time miss for an unknown member
// (the promoted strict-undefined error); on a map<string,V> it is V; on any it
// is any. The null-safe "?." form makes the result nullable.
func (c *checker) attrType(n *ast.Node, sc *scope) (*Type, error) {
	recv, err := c.exprType(n.Child(0), sc)
	if err != nil {
		return Any, err
	}
	nullSafe := n.Bool
	base := recv
	if nullSafe {
		// "?." short-circuits a null receiver; type the non-null part.
		base = removeNull(recv)
	}
	mt, err := c.memberOf(n, base, n.Str)
	if err != nil {
		return Any, err
	}
	if nullSafe && recv.hasNull() {
		return join(mt, Null), nil
	}
	return mt, nil
}

// memberOf resolves member m on a (non-null) receiver type.
func (c *checker) memberOf(n *ast.Node, recv *Type, m string) (*Type, error) {
	if recv == nil || recv.Kind == KAny {
		return Any, nil
	}
	switch recv.Kind {
	case KObject:
		if !c.reg.nominal() {
			return Any, nil // opaque host type: members are dynamic
		}
		if t, ok := c.reg.memberType(recv.Name, m); ok {
			return t, nil
		}
		// A member miss is absence-class: the strict access path surfaces it, but the
		// suppression tools (?? / default / is defined) swallow it (Section 6).
		return Any, errAbsent(n, "type %s has no member %s", recv.String(), quoteName(m))
	case KMap:
		// a.b on a map reads the value type (string key "b").
		return recv.Val, nil
	case KUnion:
		// Resolve on each arm and join; a null arm is permitted only under ?. which
		// already removed it, so a plain union with a non-member arm errors.
		var out *Type = Never
		for _, arm := range recv.Union {
			mt, err := c.memberOf(n, arm, m)
			if err != nil {
				return Any, err
			}
			out = join(out, mt)
		}
		return out, nil
	default:
		// A scalar has no members; reading one is a check-time error.
		return Any, errAt(n, "cannot read member %s on a value of type %s", quoteName(m), recv.String())
	}
}

// indexType infers "a[k]" / "a?[k]" (Section 6.2). On a list<T> it is T (k must
// be int); on a map<K,V> it is V (k checked against K); on a string it is
// string; on any it is any.
func (c *checker) indexType(n *ast.Node, sc *scope) (*Type, error) {
	recv, err := c.exprType(n.Child(0), sc)
	if err != nil {
		return Any, err
	}
	keyT, err := c.exprType(n.Child(1), sc)
	if err != nil {
		return Any, err
	}
	base := recv
	if n.Bool {
		base = removeNull(recv)
	}
	res, err := c.subscriptOf(n, base, keyT)
	if err != nil {
		return Any, err
	}
	if n.Bool && recv.hasNull() {
		return join(res, Null), nil
	}
	return res, nil
}

// subscriptOf resolves a[k] on a (non-null) receiver, checking the key kind.
func (c *checker) subscriptOf(n *ast.Node, recv, keyT *Type) (*Type, error) {
	if recv == nil || recv.Kind == KAny {
		return Any, nil
	}
	switch recv.Kind {
	case KList:
		if !c.consistent(keyT, Int) {
			return Any, errAt(n, "list subscript must be an int, found %s", keyT.String())
		}
		return recv.Elem, nil
	case KMap:
		if !c.consistent(keyT, recv.Key) {
			return Any, errAt(n, "map key must be %s, found %s", recv.Key.String(), keyT.String())
		}
		return recv.Val, nil
	case KString:
		return String, nil
	case KObject:
		return Any, nil // host index interface: dynamic
	case KUnion:
		var out *Type = Never
		for _, arm := range recv.Union {
			rt, err := c.subscriptOf(n, arm, keyT)
			if err != nil {
				return Any, err
			}
			out = join(out, rt)
		}
		return out, nil
	default:
		return Any, errAt(n, "cannot subscript a value of type %s", recv.String())
	}
}

// sliceType infers "a[start:end]": a slice of a list is the same list type, of a
// string is a string; bounds are typed (must be int where known).
func (c *checker) sliceType(n *ast.Node, sc *scope) (*Type, error) {
	recv, err := c.exprType(n.Child(0), sc)
	if err != nil {
		return Any, err
	}
	for i := 1; i < n.NumChildren(); i++ {
		if b := n.Child(i); b != nil {
			bt, err := c.exprType(b, sc)
			if err != nil {
				return Any, err
			}
			if !c.consistent(bt, Int) {
				return Any, errAt(b, "slice bound must be an int, found %s", bt.String())
			}
		}
	}
	switch recv.Kind {
	case KList, KString, KAny:
		return recv, nil
	default:
		return Any, nil
	}
}

// unaryType infers a prefix operator. not yields bool; +/- preserve the numeric
// type (and reject a non-numeric known operand, the promoted arithmetic error).
func (c *checker) unaryType(n *ast.Node, sc *scope) (*Type, error) {
	t, err := c.exprType(n.Child(0), sc)
	if err != nil {
		return Any, err
	}
	if n.Str == "not" {
		return Bool, nil
	}
	// + / - numeric.
	if t.isAny() {
		return Any, nil
	}
	if t.Kind == KInt || t.Kind == KFloat {
		return t, nil
	}
	return Any, errAt(n, "unary %s requires a number, found %s", n.Str, t.String())
}
