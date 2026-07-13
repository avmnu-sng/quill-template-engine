package check

import (
	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
)

// checkWith checks "@with map [only] { body }": the map expression is typed
// (it merges new bindings into the body scope) and the body is checked in a
// child scope. Because the merged bindings arrive as a runtime map whose member
// names are dynamic, the body scope does not gain typed names from the map (it
// is a dynamic boundary), so names introduced by the with-map type as any.
func (c *checker) checkWith(n *ast.Node, sc *scope) error {
	if _, err := c.exprType(n.Child(0), sc); err != nil {
		return err
	}
	// The body runs in a fresh scope. Under "only" the outer bindings are hidden,
	// but since the with-map adds dynamic (any) names either way, a fresh child
	// scope with no inherited typed names models both forms conservatively for the
	// names the map may introduce; outer typed names remain visible (not "only")
	// or are shadowed dynamically. We keep outer visibility for the common case.
	body := newScope(sc)
	return c.checkItems(n.Children[1:], body)
}

// checkApply checks "@apply | f | g { body }": each apply filter's arguments are
// typed, and the body is checked. The applied filters transform the captured
// body text, so no binding is introduced.
func (c *checker) checkApply(n *ast.Node, sc *scope) error {
	count := n.IntCount()
	for i := 0; i < count; i++ {
		f := n.Child(i)
		if f == nil {
			continue
		}
		for _, arg := range f.Children {
			if arg.Kind == ast.KindArg {
				if _, err := c.exprType(arg.Child(0), sc); err != nil {
					return err
				}
			}
		}
	}
	return c.checkItems(n.Children[count:], newScope(sc))
}

// checkGuard checks "@guard kind("name") { body } [else { body }]": the guard
// selects a branch on whether a callable is registered, never evaluating it, so
// both bodies are checked in child scopes.
func (c *checker) checkGuard(n *ast.Node, sc *scope) error {
	for _, ch := range n.Children {
		switch ch.Kind {
		case ast.KindClause:
			if err := c.checkItems(ch.Children, newScope(sc)); err != nil {
				return err
			}
		case ast.KindString:
			// the callable name; nothing to type
		default:
			if err := c.checkStmt(ch, newScope(sc)); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkBlock checks a @block body against its own declared signature: its input
// params bind their declared types in the body scope, and a declared return type
// is recorded so a `block("name")` call site can be typed. The shortcut value
// form (@block name expr) checks the expression is renderable, like an
// interpolation.
func (c *checker) checkBlock(n *ast.Node, sc *scope) error {
	body := newScope(sc)
	for _, ch := range n.Children {
		if ch.Kind == ast.KindParams {
			if err := c.bindParams(ch, body); err != nil {
				return err
			}
		}
	}
	if n.Int == 1 { // shortcut value form: child is the single value expression
		expr := lastExpr(n.Children)
		if expr == nil {
			return nil
		}
		t, err := c.exprType(expr, body)
		if err != nil {
			return err
		}
		if !c.renderable(t) {
			return errAt(expr, "cannot render a block value of type %s as text", t.String())
		}
		return nil
	}
	return c.checkItems(blockBody(n.Children), body)
}

// checkMacro checks a @macro body against its declared signature: each parameter
// binds its declared type (a default is checked for consistency), and the body
// is checked in the isolated macro scope (a macro sees only its params plus host
// globals, spec 04 Section 6; we model that by a fresh scope with no inherited
// template bindings).
func (c *checker) checkMacro(n *ast.Node, sc *scope) error {
	body := newScope(nil) // macros are isolated: no enclosing template bindings
	params := n.Child(0)
	if params != nil {
		if err := c.bindParams(params, body); err != nil {
			return err
		}
	}
	return c.checkItems(macroBody(n.Children), body)
}

// checkCallBlock checks a "@call [(callerParams)] name(args) { body }": the macro
// arguments are typed at the call site, and the caller block is checked in a child
// scope with each declared caller parameter bound to its declared type (or any
// when untyped), since a value round-trips from the macro into the block via
// caller(...). The macro itself is resolved dynamically at render, so its name is
// not type-checked here (mirroring a dotted macro call).
func (c *checker) checkCallBlock(n *ast.Node, sc *scope) error {
	// Type the macro-call arguments (KindArg children) for their own soundness.
	for _, ch := range n.Children {
		if ch.Kind == ast.KindArg {
			if _, err := c.exprType(ch.Child(0), sc); err != nil {
				return err
			}
		}
	}
	body := newScope(sc)
	if params := n.Child(0); params != nil && params.Kind == ast.KindParams {
		if err := c.bindParams(params, body); err != nil {
			return err
		}
	}
	callerBody := n.Children[len(n.Children)-1]
	if callerBody != nil && callerBody.Kind == ast.KindBody {
		return c.checkItems(callerBody.Children, body)
	}
	return nil
}

// bindParams binds each parameter of a KindParams node to its declared type in
// the scope, validating the type and checking that a default value's type is
// consistent with the declared type (Section 5.2). A variadic binds list<T>; a
// "**name" kwargs tail binds map<string, any>.
func (c *checker) bindParams(params *ast.Node, sc *scope) error {
	for _, p := range params.Children {
		if p.Kind != ast.KindParam {
			continue
		}
		pt := c.paramType(p)
		if p.Int&ast.ParamHasType != 0 {
			if err := c.validateType(p.Child(0), pt); err != nil {
				return err
			}
		}
		if p.Int&ast.ParamKwargs != 0 { // "**name" kwargs tail binds map<string, any>
			sc.set(p.Str, MapOf(String, Any))
			continue
		}
		if p.Bool { // variadic ...rest binds list<elem>
			sc.set(p.Str, ListOf(pt))
			continue
		}
		// A default value must be consistent with the declared type.
		if p.Int&ast.ParamHasDefault != 0 {
			defNode := defaultNode(p)
			if defNode != nil {
				dt, err := c.exprType(defNode, sc)
				if err != nil {
					return err
				}
				if !c.consistent(dt, pt) {
					return errAt(defNode,
						"default value of type %s for parameter %s is not consistent with %s",
						dt.String(), quoteName(p.Str), pt.String())
				}
			}
		}
		sc.set(p.Str, pt)
	}
	return nil
}

// defaultNode returns a parameter's default-value expression child, accounting
// for the optional preceding type child (the type is child 0 when present, then
// the default follows).
func defaultNode(p *ast.Node) *ast.Node {
	idx := 0
	if p.Int&ast.ParamHasType != 0 {
		idx = 1
	}
	return p.Child(idx)
}

// blockBody returns a @block's body items: every child that is not a leading
// signature node (KindParams or the return KindType).
func blockBody(children []*ast.Node) []*ast.Node {
	var out []*ast.Node
	for _, ch := range children {
		if ch.Kind == ast.KindParams || ch.Kind == ast.KindType {
			continue
		}
		out = append(out, ch)
	}
	return out
}

// macroBody returns a @macro's body items: every child after the leading
// KindParams and the optional return KindType.
func macroBody(children []*ast.Node) []*ast.Node {
	var out []*ast.Node
	for _, ch := range children {
		if ch.Kind == ast.KindParams || ch.Kind == ast.KindType {
			continue
		}
		out = append(out, ch)
	}
	return out
}

// bodyAfter returns the children of n after every leading node of the given
// "head" kind (e.g. @cache's KindCacheArg heads precede the body).
func bodyAfter(children []*ast.Node, head ast.Kind) []*ast.Node {
	var out []*ast.Node
	for _, ch := range children {
		if ch.Kind == head {
			continue
		}
		out = append(out, ch)
	}
	return out
}

// lastExpr returns the last child that is an expression, used for a shortcut
// block's single value.
func lastExpr(children []*ast.Node) *ast.Node {
	for i := len(children) - 1; i >= 0; i-- {
		if isExpr(children[i].Kind) {
			return children[i]
		}
	}
	return nil
}
