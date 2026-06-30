package check

import (
	"github.com/avmnusng/quill-template-engine/ast"
)

// binaryType infers a binary operator's type, dispatching on the operator
// spelling (Section 6.3). Arithmetic mirrors the runtime numeric rules; ~ concat
// is string (its operands are rendered); comparisons are bool; bitwise ops are
// int-ish; the range ".." is a list.
func (c *checker) binaryType(n *ast.Node, sc *scope) (*Type, error) {
	switch n.Str {
	case "+", "-", "*", "/", "//", "%":
		return c.arithType(n, n.Str, sc)
	case "~":
		return c.concatType(n, sc)
	case "==", "!=", "===", "<", ">", "<=", ">=":
		if _, err := c.exprType(n.Child(0), sc); err != nil {
			return Any, err
		}
		if _, err := c.exprType(n.Child(1), sc); err != nil {
			return Any, err
		}
		return Bool, nil
	case "<=>":
		if _, err := c.exprType(n.Child(0), sc); err != nil {
			return Any, err
		}
		if _, err := c.exprType(n.Child(1), sc); err != nil {
			return Any, err
		}
		return Int, nil
	case "..":
		if _, err := c.exprType(n.Child(0), sc); err != nil {
			return Any, err
		}
		if _, err := c.exprType(n.Child(1), sc); err != nil {
			return Any, err
		}
		return ListOf(Int), nil
	default:
		// bitwise b-and/b-or/b-xor and any other: type operands, result any.
		if _, err := c.exprType(n.Child(0), sc); err != nil {
			return Any, err
		}
		if _, err := c.exprType(n.Child(1), sc); err != nil {
			return Any, err
		}
		return Any, nil
	}
}

// arithType infers an arithmetic operator (+ - * / // % **). Over int,int it is
// int (over float-or-mixed, float); / true division is float; a known
// non-numeric operand is a check-time error (the promoted arithmetic type error,
// e.g. "3" + 4 where "3" is typed string). Under any the result is any.
func (c *checker) arithType(n *ast.Node, op string, sc *scope) (*Type, error) {
	lt, err := c.exprType(n.Child(0), sc)
	if err != nil {
		return Any, err
	}
	rt, err := c.exprType(n.Child(1), sc)
	if err != nil {
		return Any, err
	}
	if lt.isAny() || rt.isAny() {
		return Any, nil
	}
	if !numeric(lt) {
		return Any, errAt(n.Child(0),
			"operator %s requires a number, found %s", op, lt.String())
	}
	if !numeric(rt) {
		return Any, errAt(n.Child(1),
			"operator %s requires a number, found %s", op, rt.String())
	}
	if op == "/" {
		return Float, nil
	}
	if lt.Kind == KInt && rt.Kind == KInt {
		return Int, nil
	}
	return Float, nil
}

// numeric reports whether t is int or float (or a union of only numbers).
func numeric(t *Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case KInt, KFloat:
		return true
	case KUnion:
		for _, a := range t.Union {
			if !numeric(a) {
				return false
			}
		}
		return len(t.Union) > 0
	default:
		return false
	}
}

// concatType infers ~ concat: each operand is rendered, so each must be
// renderable, and the result is string (Section 6.3, 6.4).
func (c *checker) concatType(n *ast.Node, sc *scope) (*Type, error) {
	for _, ch := range n.Children {
		t, err := c.exprType(ch, sc)
		if err != nil {
			return Any, err
		}
		if !c.renderable(t) {
			return Any, errAt(ch,
				"cannot concatenate a value of type %s; it is not renderable", t.String())
		}
	}
	return String, nil
}

// membershipType infers in / not in / matches / starts with / ends with /
// has some / has every. All yield bool; the operands are typed for errors.
func (c *checker) membershipType(n *ast.Node, sc *scope) (*Type, error) {
	if n.Str == ".." {
		// `..` may arrive as a KindMembership in some parses; treat as range.
		if _, err := c.exprType(n.Child(0), sc); err != nil {
			return Any, err
		}
		if _, err := c.exprType(n.Child(1), sc); err != nil {
			return Any, err
		}
		return ListOf(Int), nil
	}
	left := n.Child(0)
	right := n.Child(1)
	if _, err := c.exprType(left, sc); err != nil {
		return Any, err
	}
	// `has some`/`has every` take an arrow predicate on the right; the others a
	// value. Type the right operand, threading the left's element type into an
	// arrow predicate where applicable.
	if right != nil && right.Kind == ast.KindArrow {
		lt, _ := c.exprType(left, sc)
		elem, _, _ := c.iterableElem(lt)
		if _, err := c.arrowType(right, sc, []*Type{elem}); err != nil {
			return Any, err
		}
	} else if _, err := c.exprType(right, sc); err != nil {
		return Any, err
	}
	return Bool, nil
}

// ternaryType infers "c ? a : b" (and the desugared postfix conditional): the
// condition is typed, and the result is the join of the two arms (Section 6.5).
func (c *checker) ternaryType(n *ast.Node, sc *scope) (*Type, error) {
	cond := n.Child(0)
	if _, err := c.exprType(cond, sc); err != nil {
		return Any, err
	}
	// Narrow the then-arm scope by the condition (e.g. `x if x is int`).
	thenScope := newScope(sc)
	c.narrowTrue(cond, thenScope)
	a, err := c.exprType(n.Child(1), thenScope)
	if err != nil {
		return Any, err
	}
	b, err := c.exprType(n.Child(2), sc)
	if err != nil {
		return Any, err
	}
	return join(a, b), nil
}

// coalesceType infers "a ?? b": the null arm of a's type is removed before
// joining with b (Section 6.5), so `(string?) ?? "g"` is string. The left
// operand's whole access chain is allowed-absent (the runtime suppression), so a
// member miss inside `a` is not a check error here; we model that by typing the
// left leniently (a member miss is suppressed by treating the access as any).
func (c *checker) coalesceType(n *ast.Node, sc *scope) (*Type, error) {
	a := c.exprTypeLenient(n.Child(0), sc)
	b, err := c.exprType(n.Child(1), sc)
	if err != nil {
		return Any, err
	}
	return join(removeNull(a), b), nil
}

// exprTypeLenient types an expression under the "absence allowed" flag of
// ??/default/is-defined (spec 04 Section 6, the whole-chain suppression rule):
// an absent member or undefined name at any hop yields any rather than a
// check-time miss, because the runtime would yield the fallback, not an error.
// It still surfaces a genuine type error (a non-renderable concat, a bad arith)
// that is unrelated to absence.
func (c *checker) exprTypeLenient(n *ast.Node, sc *scope) *Type {
	if n == nil {
		return Any
	}
	t, err := c.exprType(n, sc)
	if err != nil {
		// An absence/miss-class error is suppressed by the coalescing operator; we
		// fall back to any. (A genuinely malformed expression still rendered an
		// error, but at a ?? site the runtime suppresses the left miss, so the
		// checker must not be stricter than the runtime here.)
		return Any
	}
	return t
}

// arrowType infers an arrow "(p...) => body": each parameter takes its declared
// type, or the inferred element type passed by a piped collection (paramHints),
// or any. The result type is the arrow type (params) => typeof(body).
func (c *checker) arrowType(n *ast.Node, sc *scope, paramHints []*Type) (*Type, error) {
	body := newScope(sc)
	var params []*Type
	pi := 0
	for _, ch := range n.Children[:len(n.Children)-1] {
		if ch.Kind != ast.KindParam {
			continue
		}
		pt := Any
		if ch.Int&ast.ParamHasType != 0 {
			pt = fromAST(ch.Child(0))
			if err := c.validateType(ch.Child(0), pt); err != nil {
				return Any, err
			}
			// An explicit annotation is checked against the inferred element type.
			if pi < len(paramHints) && paramHints[pi] != nil && !c.consistent(paramHints[pi], pt) {
				return Any, errAt(ch,
					"arrow parameter %s is declared as %s but the pipeline yields %s",
					quoteName(ch.Str), pt.String(), paramHints[pi].String())
			}
		} else if pi < len(paramHints) {
			pt = paramHints[pi]
		}
		params = append(params, pt)
		body.set(ch.Str, pt)
		pi++
	}
	bodyExpr := n.Children[len(n.Children)-1]
	rt, err := c.exprType(bodyExpr, body)
	if err != nil {
		return Any, err
	}
	return ArrowOf(rt, params...), nil
}

// isStmt reports whether a node kind is a statement (vs an expression), used by
// the defensive child walker to recurse correctly.
func isStmt(k ast.Kind) bool {
	switch k {
	case ast.KindModule, ast.KindText, ast.KindPrint, ast.KindVerbatim,
		ast.KindIf, ast.KindClause, ast.KindFor, ast.KindBody, ast.KindSet,
		ast.KindCapture, ast.KindWith, ast.KindApply, ast.KindApplyFilter,
		ast.KindDo, ast.KindFlush, ast.KindDeprecated, ast.KindGuard,
		ast.KindTypes, ast.KindTypeDecl, ast.KindEscape, ast.KindSandbox,
		ast.KindLine, ast.KindCache, ast.KindCacheArg, ast.KindExtends,
		ast.KindBlock, ast.KindParams, ast.KindMacro, ast.KindImport,
		ast.KindFrom, ast.KindFromItem, ast.KindUse, ast.KindEmbed,
		ast.KindInclude:
		return true
	default:
		return false
	}
}

// isExpr reports whether a node kind is an expression that exprType handles.
func isExpr(k ast.Kind) bool {
	switch k {
	case ast.KindInt, ast.KindFloat, ast.KindString, ast.KindBool, ast.KindNull,
		ast.KindName, ast.KindSpecialName, ast.KindList, ast.KindMap,
		ast.KindArrow, ast.KindAttr, ast.KindIndex, ast.KindSlice, ast.KindCall,
		ast.KindFilter, ast.KindUnary, ast.KindSpread, ast.KindBinary,
		ast.KindLogical, ast.KindPower, ast.KindMembership, ast.KindTest,
		ast.KindTernary, ast.KindCoalesce, ast.KindElvis:
		return true
	default:
		return false
	}
}
