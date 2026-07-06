package check

import (
	"github.com/avmnu-sng/quill-template-engine/core/ast"
)

// validateType checks that a type annotation is well-formed and names only known
// host types (design/type-system.md Section 7.2 items 8). It rejects:
//
//   - a map<K, V> whose key kind is not int or string (the typed shadow of the
//     bool/float/null-subscript runtime error, spec 04 Section 7);
//   - an Object<"Name"> whose name is unknown to a nominal host registry (a typo
//     in an annotation, caught at load time). When the host registered no static
//     types at all, Object<...> is opaque-but-known and this check is skipped, so
//     a host that did not opt into nominal typing is never burdened.
//
// The node n is the annotation's KindType node, used for positioning; t is its
// converted Type. validateType recurses into structured types so a nested
// malformed type is caught.
func (c *checker) validateType(n *ast.Node, t *Type) error {
	if t == nil {
		return nil
	}
	switch t.Kind {
	case KMap:
		if !validMapKey(t.Key) {
			return errAt(n, "map key type must be int or string, found %s", t.Key.String())
		}
		if err := c.validateChildType(n, 0, t.Key); err != nil {
			return err
		}
		return c.validateChildType(n, 1, t.Val)
	case KList:
		return c.validateChildType(n, 0, t.Elem)
	case KObject:
		if t.Name == "" {
			return errAt(n, "Object<...> requires a non-empty type name")
		}
		if !c.reg.knowsType(t.Name) {
			return errAt(n, "unknown host type %s", quoteName(t.Name))
		}
		return nil
	case KArrow:
		for i, p := range t.Params {
			if err := c.validateChildType(n, i, p); err != nil {
				return err
			}
		}
		return c.validateType(retNode(n), t.Ret)
	case KUnion:
		for i, a := range t.Union {
			if err := c.validateChildType(n, i, a); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

// validateChildType validates a sub-type, anchoring errors to the i-th child
// annotation node where available, else the parent node.
func (c *checker) validateChildType(parent *ast.Node, i int, t *Type) error {
	child := parent
	if cn := parent.Child(i); cn != nil && cn.Kind == ast.KindType {
		child = cn
	}
	return c.validateType(child, t)
}

// retNode returns the return-type child of an arrow type node (its last child),
// for positioning a return-type error.
func retNode(n *ast.Node) *ast.Node {
	if n == nil || n.NumChildren() == 0 {
		return n
	}
	last := n.Child(n.NumChildren() - 1)
	if last != nil && last.Kind == ast.KindType {
		return last
	}
	return n
}

// validMapKey reports whether a map key type is int or string (the only legal
// *Array key kinds). any is permitted (the dynamic key), and a union of int and
// string is permitted.
func validMapKey(k *Type) bool {
	if k == nil {
		return true
	}
	switch k.Kind {
	case KAny, KInt, KString:
		return true
	case KUnion:
		for _, a := range k.Union {
			if !validMapKey(a) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
