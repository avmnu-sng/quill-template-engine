package check

import (
	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
)

// checkModule type-checks a whole template: it seeds the file scope from the
// @types block, then walks every top-level item. Composition heads (@extends,
// @import, @block, @macro) are walked for their bodies; cross-template signature
// checking is out of this slice's structural scope (the design's Section 10
// cross-file handshake needs the loader, and is gradual: an untyped includee
// is all-`any`), so a block/macro body is checked against its own declared
// signature here.
func (c *checker) checkModule(mod *ast.Node) error {
	root := newScope(nil)
	if err := c.seedTypes(mod, root); err != nil {
		return err
	}
	return c.checkItems(mod.Children, root)
}

// seedTypes reads every @types declaration at file scope into the root scope, so
// a declared context variable is checked at every use. A declared type is
// validated (a malformed type or unknown host type is a check-time error here,
// at the declaration, the earliest point). Multiple @types blocks accumulate.
func (c *checker) seedTypes(mod *ast.Node, root *scope) error {
	for _, item := range mod.Children {
		if item.Kind != ast.KindTypes {
			continue
		}
		for _, decl := range item.Children {
			if decl.Kind != ast.KindTypeDecl {
				continue
			}
			t := fromAST(decl.Child(0))
			if err := c.validateType(decl.Child(0), t); err != nil {
				return err
			}
			root.set(decl.Str, t)
		}
	}
	return nil
}

// checkItems walks an ordered run of body items in the given scope. A scope is
// mutated in place by @set (its binding outlives the statement within the same
// block, matching runtime set scoping), while @for/@macro/@block/@with introduce
// child scopes.
func (c *checker) checkItems(items []*ast.Node, sc *scope) error {
	for _, n := range items {
		if err := c.checkStmt(n, sc); err != nil {
			return err
		}
	}
	return nil
}

// checkStmt type-checks one statement or output node.
func (c *checker) checkStmt(n *ast.Node, sc *scope) error {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case ast.KindText, ast.KindVerbatim, ast.KindFlush, ast.KindLine,
		ast.KindDeprecated, ast.KindTypes, ast.KindImport, ast.KindFrom,
		ast.KindUse, ast.KindExtends, ast.KindInclude, ast.KindEmbed:
		// No expression to type, or a dynamic cross-template boundary handled at
		// render. (extends/include/embed sources are string-coerced expressions the
		// runtime resolves; their typed handshake is gradual and dynamic here.)
		return c.checkChildrenExprs(n, sc)

	case ast.KindPrint:
		return c.checkPrint(n, sc)

	case ast.KindDo, ast.KindLog:
		_, err := c.exprType(n.Child(0), sc)
		return err

	case ast.KindTabBlock:
		// The level expression is typed; the body renders in a child scope.
		if _, err := c.exprType(n.Child(0), sc); err != nil {
			return err
		}
		return c.checkItems(n.Children[1:], newScope(sc))

	case ast.KindSet:
		return c.checkSet(n, sc)

	case ast.KindCapture:
		return c.checkCapture(n, sc)

	case ast.KindIf:
		return c.checkIf(n, sc)

	case ast.KindFor:
		return c.checkFor(n, sc)

	case ast.KindWith:
		return c.checkWith(n, sc)

	case ast.KindApply:
		return c.checkApply(n, sc)

	case ast.KindGuard:
		return c.checkGuard(n, sc)

	case ast.KindEscape, ast.KindSandbox:
		return c.checkItems(n.Children, newScope(sc))

	case ast.KindCache:
		// Cache args are expressions; the body renders in a child scope.
		for _, ch := range n.Children {
			if ch.Kind == ast.KindCacheArg {
				if _, err := c.exprType(ch.Child(0), sc); err != nil {
					return err
				}
			}
		}
		return c.checkItems(bodyAfter(n.Children, ast.KindCacheArg), newScope(sc))

	case ast.KindBlock:
		return c.checkBlock(n, sc)

	case ast.KindMacro:
		return c.checkMacro(n, sc)

	case ast.KindProvide:
		// A @provide body renders in a child scope; its output accumulates into a
		// slot emitted later at @yield, so there is nothing to type at this site.
		return c.checkItems(n.Children, newScope(sc))

	case ast.KindYield:
		// A @yield names a slot label and emits its accumulated text; no expression.
		return nil

	case ast.KindCallBlock:
		return c.checkCallBlock(n, sc)
	}
	// Any other node: walk its children for embedded expressions defensively.
	return c.checkChildrenExprs(n, sc)
}

// checkChildrenExprs type-checks any direct expression children of a node whose
// own shape needs no special rule (e.g. an @include source). It is intentionally
// shallow: it evaluates each child as an expression where that is meaningful and
// recurses into statement children.
func (c *checker) checkChildrenExprs(n *ast.Node, sc *scope) error {
	for _, ch := range n.Children {
		if ch == nil {
			continue
		}
		if isStmt(ch.Kind) {
			if err := c.checkStmt(ch, sc); err != nil {
				return err
			}
			continue
		}
		if isExpr(ch.Kind) {
			if _, err := c.exprType(ch, sc); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkPrint checks an interpolation: the expression must be renderable
// (design/type-system.md Section 6.4). A list/map-typed value, or a hookless
// Object, is a check-time render error; under `any` the check is inert.
func (c *checker) checkPrint(n *ast.Node, sc *scope) error {
	t, err := c.exprType(n.Child(0), sc)
	if err != nil {
		return err
	}
	if !c.renderable(t) {
		return errAt(n.Child(0),
			"cannot render a value of type %s as text; use a filter such as join or json",
			t.String())
	}
	return nil
}

// checkSet checks an @set: each value's inferred type must be consistent with
// its target's declared type (a backstop is allowed at the any-boundary); an
// untyped target INFERS its type from the value (Section 5.4). A destructuring
// target binds its slots dynamically here (the runtime enforces arity); a typed
// slot is checked against the inferred element type where known.
func (c *checker) checkSet(n *ast.Node, sc *scope) error {
	count := n.IntCount()
	targets := n.Children[:count]
	values := n.Children[count:]
	// The multi-target form pairs targets to values positionally; the single
	// destructuring form binds one pattern from one value.
	if len(targets) == len(values) {
		for i, tg := range targets {
			if err := c.bindTarget(tg, values[i], sc); err != nil {
				return err
			}
		}
		return nil
	}
	// Single pattern bound to a single value (destructuring): type the value, then
	// bind the pattern's names as any (the runtime drives the actual destructure).
	if len(values) >= 1 {
		if _, err := c.exprType(values[0], sc); err != nil {
			return err
		}
	}
	c.bindPattern(targets, sc)
	return nil
}

// bindTarget binds one @set/@for target name to a value expression: it types the
// value, checks an annotation for consistency, and records the binding. A
// destructuring pattern (KindListPattern/KindMapPattern) binds its names as any.
func (c *checker) bindTarget(tg, value *ast.Node, sc *scope) error {
	// A member-set target (recv.name / recv[key]) assigns through a receiver rather
	// than binding a name. Type the value and the receiver so an undefined receiver
	// or value is still caught, and introduce no new binding.
	if tg.Kind == ast.KindAttr || tg.Kind == ast.KindIndex {
		if _, err := c.exprType(value, sc); err != nil {
			return err
		}
		if _, err := c.exprType(tg, sc); err != nil {
			return err
		}
		return nil
	}
	if tg.Kind != ast.KindTarget {
		// A destructuring pattern target; type the value and bind names as any.
		if value != nil {
			if _, err := c.exprType(value, sc); err != nil {
				return err
			}
		}
		c.bindPatternNames(tg, sc)
		return nil
	}
	vt, err := c.exprType(value, sc)
	if err != nil {
		return err
	}
	if ann := tg.Child(0); ann != nil && ann.Kind == ast.KindType {
		declared := fromAST(ann)
		if err := c.validateType(ann, declared); err != nil {
			return err
		}
		if !c.consistent(vt, declared) {
			return errAt(value,
				"cannot assign a value of type %s to %s declared as %s",
				vt.String(), quoteName(tg.Str), declared.String())
		}
		sc.set(tg.Str, declared)
		return nil
	}
	// No annotation: infer from the value.
	sc.set(tg.Str, vt)
	return nil
}

// bindPattern binds the names of a single set destructuring pattern as any.
func (c *checker) bindPattern(targets []*ast.Node, sc *scope) {
	for _, tg := range targets {
		c.bindPatternNames(tg, sc)
	}
}

// bindPatternNames recursively binds every name introduced by a destructuring
// pattern (list/map slots, optional slots, rest captures, nested patterns) to
// any. Destructured slots are dynamic in this slice (the runtime enforces arity
// and shape); a slot annotation is read where present.
func (c *checker) bindPatternNames(n *ast.Node, sc *scope) {
	if n == nil {
		return
	}
	switch n.Kind {
	case ast.KindTarget:
		t := Any
		if ann := n.Child(0); ann != nil && ann.Kind == ast.KindType {
			t = fromAST(ann)
		}
		sc.set(n.Str, t)
	case ast.KindName:
		sc.set(n.Str, Any)
	case ast.KindMapTarget:
		name := n.Str
		if n.Bool {
			if alias := n.Child(0); alias != nil && alias.Kind == ast.KindName {
				name = alias.Str
			}
		}
		sc.set(name, Any)
	case ast.KindSpread:
		c.bindPatternNames(n.Child(0), sc)
	case ast.KindOptional:
		c.bindPatternNames(n.Child(0), sc)
	case ast.KindListPattern, ast.KindMapPattern:
		for _, ch := range n.Children {
			c.bindPatternNames(ch, sc)
		}
	}
}

// checkCapture checks "@set X [: T] = capture { body }": the body renders in a
// child scope and the bound name takes the declared type, or `string` (a capture
// yields rendered text / a Safe value, which is string-like) when unannotated.
func (c *checker) checkCapture(n *ast.Node, sc *scope) error {
	bodyStart := 0
	declared := String
	if ann := n.Child(0); ann != nil && ann.Kind == ast.KindType {
		declared = fromAST(ann)
		if err := c.validateType(ann, declared); err != nil {
			return err
		}
		bodyStart = 1
	}
	if err := c.checkItems(n.Children[bodyStart:], newScope(sc)); err != nil {
		return err
	}
	sc.set(n.Str, declared)
	return nil
}

// checkIf checks an @if: each clause condition is typed (any expression is a
// valid condition: truthiness is total) and each branch body is checked in a
// child scope refined by any narrowing the condition proves (Section 8.1).
func (c *checker) checkIf(n *ast.Node, sc *scope) error {
	for _, clause := range n.Children {
		if clause.Kind != ast.KindClause {
			continue
		}
		body := clause.Children
		branch := newScope(sc)
		if clause.Bool { // if/elseif: child 0 is the condition, 1.. the body
			cond := clause.Child(0)
			if _, err := c.exprType(cond, sc); err != nil {
				return err
			}
			c.narrowTrue(cond, branch)
			body = clause.Children[1:]
		}
		if err := c.checkItems(body, branch); err != nil {
			return err
		}
	}
	return nil
}

// checkFor checks an @for: the iterand must be iterable (a non-iterable typed
// iterand is the promoted T4 runtime error); the loop target(s) bind the element
// (and key) type, inferred from the iterand or checked against an annotation.
func (c *checker) checkFor(n *ast.Node, sc *scope) error {
	count := int(n.Int & ast.ForTargetCount)
	t1 := n.Child(0)
	var t2 *ast.Node
	idx := 1
	if count == 2 {
		t2 = n.Child(1)
		idx = 2
	}
	iter := n.Child(idx)

	// An optional fused filter clause (KindClause) sits between the iterand and the
	// body; the body is then the next KindBody and the else follows it.
	var filter *ast.Node
	bodyIdx := idx + 1
	if fc := n.Child(bodyIdx); fc != nil && fc.Kind == ast.KindClause {
		filter = fc
		bodyIdx++
	}
	body := n.Child(bodyIdx)

	it, err := c.exprType(iter, sc)
	if err != nil {
		return err
	}
	elem, key, ok := c.iterableElem(it)
	if !ok {
		return errAt(iter, "cannot iterate over a value of type %s", it.String())
	}

	loop := newScope(sc)
	if count == 2 {
		// for k, v in m: target1 is the key, target2 the value.
		if err := c.bindLoopTarget(t1, key, loop); err != nil {
			return err
		}
		if err := c.bindLoopTarget(t2, elem, loop); err != nil {
			return err
		}
	} else {
		if err := c.bindLoopTarget(t1, elem, loop); err != nil {
			return err
		}
	}
	// The fused filter condition is checked in the loop scope, so it may reference
	// the loop target(s) just like the body.
	if filter != nil {
		if _, err := c.exprType(filter.Child(0), loop); err != nil {
			return err
		}
	}
	if err := c.checkItems(body.Children, loop); err != nil {
		return err
	}
	// An @else body (Bool set) runs in the outer scope with no loop bindings.
	if n.Bool {
		if els := n.Child(bodyIdx + 1); els != nil {
			if err := c.checkItems(els.Children, newScope(sc)); err != nil {
				return err
			}
		}
	}
	return nil
}

// bindLoopTarget binds a @for target to the inferred element/key type, checking
// an annotation for agreement (Section 5.5). When the iterand is `any`, the
// inferred type is `any` and an annotation is a claim backstopped at the
// boundary, so it is recorded as the declared type. The target is a
// KindTarget (name + optional type child).
func (c *checker) bindLoopTarget(tg *ast.Node, inferred *Type, sc *scope) error {
	if tg == nil || tg.Kind != ast.KindTarget {
		return nil
	}
	if ann := tg.Child(0); ann != nil && ann.Kind == ast.KindType {
		declared := fromAST(ann)
		if err := c.validateType(ann, declared); err != nil {
			return err
		}
		// The inferred element type must be consistent with the declared loop var
		// type, OR the iterand was any (inferred any), in which case the annotation
		// is a backstopped claim. consistent(any, T) is true, so this covers both.
		if !c.consistent(inferred, declared) {
			return errAt(tg,
				"loop variable %s is declared as %s but the iterand yields %s",
				quoteName(tg.Str), declared.String(), inferred.String())
		}
		sc.set(tg.Str, declared)
		return nil
	}
	sc.set(tg.Str, inferred)
	return nil
}
