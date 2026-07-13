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

	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
)

// Kind tags a static Type. The scalar kinds mirror the runtime kinds of
// spec 04 Section 2.1; the structured kinds (list/map/object/arrow/union) carry
// sub-types; KAny is the gradual top and kNever is the empty type used only
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
	// KList is a list type; elem is the element type.
	KList
	// KMap is a map type; key and val are the key/value types.
	KMap
	// KObject is a host object type; name is the host type name.
	KObject
	// KArrow is an arrow type; params are the parameter types, ret the result.
	KArrow
	// KUnion is a union type; union holds two or more alternative arms.
	KUnion
	// kNever is the empty type, the identity of join; it never appears in a
	// well-formed annotation and is not renderable.
	kNever
)

// Type is the checker's compile-time static type. It is never seen by the
// interpreter (the checker erases types after the pass) and carries no runtime
// payload. The zero value is KAny, so a freshly-made Type is the gradual top.
type Type struct {
	kind   Kind
	name   string  // KObject host type name
	elem   *Type   // KList element type
	key    *Type   // KMap key type
	val    *Type   // KMap value type
	params []*Type // KArrow parameter types
	ret    *Type   // KArrow result type
	union  []*Type // KUnion arms (each non-union, non-any)
}

// The scalar singletons. Each *Type is immutable (all fields unexported), so the
// pointed-to values are safe to share and use concurrently. These package
// variables are part of the frozen v1 API and MUST NOT be reassigned by a host:
// the checker uses them internally as canonical return values and constructor
// arguments, so clobbering a binding (for example check.Int = check.String)
// silently corrupts inference for every template in the process.
var (
	Any    = &Type{kind: KAny}
	Null   = &Type{kind: KNull}
	Bool   = &Type{kind: KBool}
	Int    = &Type{kind: KInt}
	Float  = &Type{kind: KFloat}
	String = &Type{kind: KString}
	never  = &Type{kind: kNever}
)

// ListOf returns list<elem>.
func ListOf(elem *Type) *Type { return &Type{kind: KList, elem: elem} }

// MapOf returns map<key, val>.
func MapOf(key, val *Type) *Type { return &Type{kind: KMap, key: key, val: val} }

// ObjectOf returns Object<"name">.
func ObjectOf(name string) *Type { return &Type{kind: KObject, name: name} }

// ArrowOf returns (params...) => ret.
func ArrowOf(ret *Type, params ...*Type) *Type {
	return &Type{kind: KArrow, params: params, ret: ret}
}

// String renders a Type in the surface annotation syntax, used in error
// messages so a diagnostic names types exactly as the author wrote them.
func (t *Type) String() string {
	if t == nil {
		return "any"
	}
	switch t.kind {
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
	case kNever:
		return "never"
	case KList:
		return "list<" + t.elem.String() + ">"
	case KMap:
		return "map<" + t.key.String() + ", " + t.val.String() + ">"
	case KObject:
		return "Object<\"" + t.name + "\">"
	case KArrow:
		parts := make([]string, len(t.params))
		for i, p := range t.params {
			parts[i] = p.String()
		}
		return "(" + strings.Join(parts, ", ") + ") => " + t.ret.String()
	case KUnion:
		// Render `T | null` as the `T?` sugar when that is the exact shape, since
		// that is how the author most often wrote it.
		if base, ok := t.asNullable(); ok {
			return base.String() + "?"
		}
		parts := make([]string, len(t.union))
		for i, a := range t.union {
			parts[i] = a.String()
		}
		return strings.Join(parts, " | ")
	}
	return "any"
}

// asNullable reports whether a union is exactly `base | null` for a single
// non-null base, returning that base.
func (t *Type) asNullable() (*Type, bool) {
	if t == nil || t.kind != KUnion || len(t.union) != 2 {
		return nil, false
	}
	switch {
	case t.union[0].kind == KNull:
		return t.union[1], true
	case t.union[1].kind == KNull:
		return t.union[0], true
	}
	return nil, false
}

// isAny reports the gradual top (a nil Type is also treated as any, so callers
// that forget to default are safe).
func (t *Type) isAny() bool { return t == nil || t.kind == KAny }

// hasNull reports whether null is one of the type's inhabitants.
func (t *Type) hasNull() bool {
	if t == nil {
		return false
	}
	if t.kind == KNull {
		return true
	}
	if t.kind == KUnion {
		for _, a := range t.union {
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
	if t != nil && t.kind == KUnion {
		return t.union
	}
	return []*Type{t}
}

// removeNull drops the null arm from a type, used by the `??`/`?:` coalescing
// rule and by `is not null` narrowing (design/type-system.md Section 6.5, 8.1).
func removeNull(t *Type) *Type {
	if t == nil || t.kind == KAny {
		return t
	}
	if t.kind == KNull {
		return never
	}
	if t.kind != KUnion {
		return t
	}
	var kept []*Type
	for _, a := range t.union {
		if a.kind != KNull {
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
		if p.kind == KAny {
			return Any
		}
		if p.kind == kNever {
			continue
		}
		if p.kind == KUnion {
			flat = append(flat, p.union...)
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
		return never
	case 1:
		return uniq[0]
	}
	sort.SliceStable(uniq, func(i, j int) bool { return uniq[i].String() < uniq[j].String() })
	return &Type{kind: KUnion, union: uniq}
}

// equalType reports structural type equality, used for union dedup and the
// reflexive case of consistency.
func equalType(a, b *Type) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case KObject:
		return a.name == b.name
	case KList:
		return equalType(a.elem, b.elem)
	case KMap:
		return equalType(a.key, b.key) && equalType(a.val, b.val)
	case KArrow:
		if len(a.params) != len(b.params) || !equalType(a.ret, b.ret) {
			return false
		}
		for i := range a.params {
			if !equalType(a.params[i], b.params[i]) {
				return false
			}
		}
		return true
	case KUnion:
		if len(a.union) != len(b.union) {
			return false
		}
		for i := range a.union {
			if !equalType(a.union[i], b.union[i]) {
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
// any; widening to any happens only when one input is already any.
func join(a, b *Type) *Type {
	if a == nil {
		a = Any
	}
	if b == nil {
		b = Any
	}
	if a.kind == KAny || b.kind == KAny {
		return Any
	}
	if a.kind == kNever {
		return b
	}
	if b.kind == kNever {
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
