// Package check is Quill's gradual type checker: a front-end pass that runs
// between parse and interpret, consumes the type annotations the parser already
// threads through the AST (the @types block, @set/@for targets, @macro/@block
// params and returns, and arrow params), infers static types where the spec
// defines it, applies the gradual `any` fallback for everything unannotated, and
// reports ill-typed templates with positioned errors BEFORE any byte is rendered
// (spec 04 Sections 1-3, design/type-system.md).
//
// The one binding invariant: annotations NEVER change runtime behavior. The
// checker only moves an error earlier in time; it emits no code and mutates no
// AST node the renderer reads. An unannotated template types entirely as `any`,
// so the checker is silent and the template renders byte-identically to a build
// without this package. That is why Check over a zero-annotation module returns
// nil and the pre-S9 conformance fixtures still pass verbatim.
//
// Soundness is honest about the shallow boundary cast (design/type-system.md
// Section 7.5-7.6): the checker reasons statically where types are known and
// leaves the strict-by-default runtime as the floor everywhere a value is `any`
// or crossed an `any`-to-typed boundary. The checker never accepts a program the
// runtime would reject; every static rejection is the static shadow of an
// existing runtime error, promoted to load time.
package check

import (
	"sort"
	"strconv"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/core/ast"
)

// Kind tags a static Type. The scalar kinds mirror the runtime kinds of
// spec 04 Section 2.1; the structured kinds (list/map/object/arrow/union) carry
// sub-types; KAny is the gradual top and KNever is the empty type used only
// internally as the identity element of a join.
type Kind uint8

const (
	// KAny is the gradual top: consistent with every type in both directions, an
	// unannotated binding is KAny (design/type-system.md Section 4.6).
	KAny Kind = iota
	// KNull is the null (none) type.
	KNull
	// KBool is the boolean type.
	KBool
	// KInt is the integer type.
	KInt
	// KFloat is the float type.
	KFloat
	// KString is the string type.
	KString
	// KList is a list type; Elem is the element type.
	KList
	// KMap is a map type; Key and Val are the key/value types.
	KMap
	// KObject is a host object type; Name is the host type name.
	KObject
	// KArrow is an arrow type; Params are the parameter types, Ret the result.
	KArrow
	// KUnion is a union type; Union holds two or more alternative arms.
	KUnion
	// KNever is the empty type, the identity of join; it never appears in a
	// well-formed annotation and is not renderable.
	KNever
)

// Type is the checker's compile-time static type. It is never seen by the
// interpreter (the checker erases types after the pass) and carries no runtime
// payload. The zero value is KAny, so a freshly-made Type is the gradual top.
type Type struct {
	Kind   Kind
	Name   string  // KObject host type name
	Elem   *Type   // KList element type
	Key    *Type   // KMap key type
	Val    *Type   // KMap value type
	Params []*Type // KArrow parameter types
	Ret    *Type   // KArrow result type
	Union  []*Type // KUnion arms (each non-union, non-any)
}

// The scalar singletons. Types are immutable once built, so sharing is safe.
var (
	Any    = &Type{Kind: KAny}
	Null   = &Type{Kind: KNull}
	Bool   = &Type{Kind: KBool}
	Int    = &Type{Kind: KInt}
	Float  = &Type{Kind: KFloat}
	String = &Type{Kind: KString}
	Never  = &Type{Kind: KNever}
)

// ListOf returns list<elem>.
func ListOf(elem *Type) *Type { return &Type{Kind: KList, Elem: elem} }

// MapOf returns map<key, val>.
func MapOf(key, val *Type) *Type { return &Type{Kind: KMap, Key: key, Val: val} }

// ObjectOf returns Object<"name">.
func ObjectOf(name string) *Type { return &Type{Kind: KObject, Name: name} }

// ArrowOf returns (params...) => ret.
func ArrowOf(ret *Type, params ...*Type) *Type {
	return &Type{Kind: KArrow, Params: params, Ret: ret}
}

// String renders a Type in the surface annotation syntax, used in error
// messages so a diagnostic names types exactly as the author wrote them.
func (t *Type) String() string {
	if t == nil {
		return "any"
	}
	switch t.Kind {
	case KAny:
		return "any"
	case KNull:
		return "null"
	case KBool:
		return "bool"
	case KInt:
		return "int"
	case KFloat:
		return "float"
	case KString:
		return "string"
	case KNever:
		return "never"
	case KList:
		return "list<" + t.Elem.String() + ">"
	case KMap:
		return "map<" + t.Key.String() + ", " + t.Val.String() + ">"
	case KObject:
		return "Object<\"" + t.Name + "\">"
	case KArrow:
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = p.String()
		}
		return "(" + strings.Join(parts, ", ") + ") => " + t.Ret.String()
	case KUnion:
		// Render `T | null` as the `T?` sugar when that is the exact shape, since
		// that is how the author most often wrote it.
		if base, ok := t.asNullable(); ok {
			return base.String() + "?"
		}
		parts := make([]string, len(t.Union))
		for i, a := range t.Union {
			parts[i] = a.String()
		}
		return strings.Join(parts, " | ")
	}
	return "any"
}

// asNullable reports whether a union is exactly `base | null` for a single
// non-null base, returning that base.
func (t *Type) asNullable() (*Type, bool) {
	if t == nil || t.Kind != KUnion || len(t.Union) != 2 {
		return nil, false
	}
	switch {
	case t.Union[0].Kind == KNull:
		return t.Union[1], true
	case t.Union[1].Kind == KNull:
		return t.Union[0], true
	}
	return nil, false
}

// isAny reports the gradual top (a nil Type is also treated as any, so callers
// that forget to default are safe).
func (t *Type) isAny() bool { return t == nil || t.Kind == KAny }

// hasNull reports whether null is one of the type's inhabitants.
func (t *Type) hasNull() bool {
	if t == nil {
		return false
	}
	if t.Kind == KNull {
		return true
	}
	if t.Kind == KUnion {
		for _, a := range t.Union {
			if a.hasNull() {
				return true
			}
		}
	}
	return false
}

// arms returns the union arms, or the single type as a one-element slice, so a
// caller can treat a scalar and a union uniformly.
func (t *Type) arms() []*Type {
	if t != nil && t.Kind == KUnion {
		return t.Union
	}
	return []*Type{t}
}

// removeNull drops the null arm from a type, used by the `??`/`?:` coalescing
// rule and by `is not null` narrowing (design/type-system.md Section 6.5, 8.1).
func removeNull(t *Type) *Type {
	if t == nil || t.Kind == KAny {
		return t
	}
	if t.Kind == KNull {
		return Never
	}
	if t.Kind != KUnion {
		return t
	}
	var kept []*Type
	for _, a := range t.Union {
		if a.Kind != KNull {
			kept = append(kept, a)
		}
	}
	return unionOf(kept)
}

// unionOf builds the least type whose inhabitants are the union of the inputs'.
// It flattens nested unions, drops duplicates, absorbs to any when any input is
// any, and collapses to a scalar when only one distinct arm remains. The arm
// order is canonicalized so two equal unions stringify identically.
func unionOf(parts []*Type) *Type {
	var flat []*Type
	for _, p := range parts {
		if p == nil {
			continue
		}
		if p.Kind == KAny {
			return Any
		}
		if p.Kind == KNever {
			continue
		}
		if p.Kind == KUnion {
			flat = append(flat, p.Union...)
		} else {
			flat = append(flat, p)
		}
	}
	var uniq []*Type
	for _, p := range flat {
		dup := false
		for _, q := range uniq {
			if equalType(p, q) {
				dup = true
				break
			}
		}
		if !dup {
			uniq = append(uniq, p)
		}
	}
	switch len(uniq) {
	case 0:
		return Never
	case 1:
		return uniq[0]
	}
	sort.SliceStable(uniq, func(i, j int) bool { return uniq[i].String() < uniq[j].String() })
	return &Type{Kind: KUnion, Union: uniq}
}

// equalType reports structural type equality, used for union dedup and the
// reflexive case of consistency.
func equalType(a, b *Type) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KObject:
		return a.Name == b.Name
	case KList:
		return equalType(a.Elem, b.Elem)
	case KMap:
		return equalType(a.Key, b.Key) && equalType(a.Val, b.Val)
	case KArrow:
		if len(a.Params) != len(b.Params) || !equalType(a.Ret, b.Ret) {
			return false
		}
		for i := range a.Params {
			if !equalType(a.Params[i], b.Params[i]) {
				return false
			}
		}
		return true
	case KUnion:
		if len(a.Union) != len(b.Union) {
			return false
		}
		for i := range a.Union {
			if !equalType(a.Union[i], b.Union[i]) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

// join is the least upper bound of two types (design/type-system.md Section
// 6.6): identical types join to themselves; any absorbs; null widens a type to
// nullable; int and float join to their union (no `number` supertype);
// otherwise the join is the union. The join never silently widens a scalar to
// any -- widening to any happens only when one input is already any.
func join(a, b *Type) *Type {
	if a == nil {
		a = Any
	}
	if b == nil {
		b = Any
	}
	if a.Kind == KAny || b.Kind == KAny {
		return Any
	}
	if a.Kind == KNever {
		return b
	}
	if b.Kind == KNever {
		return a
	}
	if equalType(a, b) {
		return a
	}
	return unionOf([]*Type{a, b})
}

// fromAST converts a parsed KindType annotation node to a checker Type. It
// resolves the surface grammar (atom/list/map/Object/arrow/union/group) the
// parser produced (parse/types.go). An ill-formed type (a bad Object name, a
// map key that is not int|string) is reported by the caller via validate; this
// function only shapes the tree.
func fromAST(n *ast.Node) *Type {
	if n == nil || n.Kind != ast.KindType {
		return Any
	}
	switch n.Str {
	case "any":
		return Any
	case "null":
		return Null
	case "bool":
		return Bool
	case "int":
		return Int
	case "float":
		return Float
	case "string":
		return String
	case "list":
		return ListOf(fromAST(n.Child(0)))
	case "map":
		return MapOf(fromAST(n.Child(0)), fromAST(n.Child(1)))
	case "Object":
		name := ""
		if c := n.Child(0); c != nil {
			name = c.Str
		}
		return ObjectOf(name)
	case "arrow":
		// Children are the params, then the return type (parse/types.go).
		ret := Any
		var params []*Type
		nc := n.NumChildren()
		if nc > 0 {
			ret = fromAST(n.Child(nc - 1))
			for i := 0; i < nc-1; i++ {
				params = append(params, fromAST(n.Child(i)))
			}
		}
		return ArrowOf(ret, params...)
	case "group":
		return fromAST(n.Child(0))
	case "union":
		var arms []*Type
		for _, c := range n.Children {
			arms = append(arms, fromAST(c))
		}
		t := unionOf(arms)
		if n.Bool { // trailing "?" nullable
			t = join(t, Null)
		}
		return t
	}
	return Any
}

// quoteName renders an identifier for an error message.
func quoteName(s string) string { return strconv.Quote(s) }
