package interp

import (
	"github.com/avmnusng/quill-template-engine/ast"
	"github.com/avmnusng/quill-template-engine/errors"
	"github.com/avmnusng/quill-template-engine/runtime"
)

// eval evaluates an expression node to a runtime Value in the scope ctx. It is
// the expression core of spec 04: every operator routes to a runtime op (Equal,
// Order, Truthy, ToText, GetAttribute), and no operator coerces except ~ and the
// print site (spec 04 Section 4). The allowAbsent flag threads the suppression
// set by ??, default, and is defined over the WHOLE left operand (spec 04 Section
// 8.2): when true, an undefined variable or absent member yields Null instead of
// a strict-undefined error.
func (in *interp) eval(n *ast.Node, ctx *runtime.Context, allowAbsent bool) (runtime.Value, error) {
	switch n.Kind {
	case ast.KindInt:
		return runtime.Int(n.Int), nil
	case ast.KindFloat:
		return runtime.Float(n.Float), nil
	case ast.KindString:
		return runtime.Str(n.Str), nil
	case ast.KindBool:
		return runtime.Bool(n.Bool), nil
	case ast.KindNull:
		return runtime.Null(), nil
	case ast.KindName:
		return in.evalName(n, ctx, allowAbsent)
	case ast.KindSpecialName:
		return in.evalSpecialName(n, ctx)
	case ast.KindList:
		return in.evalList(n, ctx)
	case ast.KindMap:
		return in.evalMap(n, ctx)
	case ast.KindAttr:
		return in.evalAttr(n, ctx, allowAbsent)
	case ast.KindIndex:
		return in.evalIndex(n, ctx, allowAbsent)
	case ast.KindSlice:
		return in.evalSlice(n, ctx)
	case ast.KindCall:
		return in.evalCall(n, ctx)
	case ast.KindFilter:
		return in.evalFilter(n, ctx)
	case ast.KindUnary:
		return in.evalUnary(n, ctx)
	case ast.KindBinary:
		return in.evalBinary(n, ctx)
	case ast.KindLogical:
		return in.evalLogical(n, ctx)
	case ast.KindPower:
		return in.evalPower(n, ctx)
	case ast.KindMembership:
		return in.evalMembership(n, ctx)
	case ast.KindTest:
		return in.evalTest(n, ctx)
	case ast.KindTernary:
		return in.evalTernary(n, ctx)
	case ast.KindCoalesce:
		return in.evalCoalesce(n, ctx)
	case ast.KindElvis:
		return in.evalElvis(n, ctx)
	case ast.KindAssign:
		return in.evalAssign(n, ctx)
	case ast.KindArrow:
		return in.evalArrow(n, ctx)
	default:
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"cannot evaluate %s expression", n.Kind))
	}
}

// evalName resolves a bare identifier in the context. A miss under strict is a
// KindUndefined error listing the available names; under allowAbsent or lenient
// mode it is Null. The reserved name "loop" inside a for body is bound in the
// context like any other variable (see exec_for), so it resolves here.
func (in *interp) evalName(n *ast.Node, ctx *runtime.Context, allowAbsent bool) (runtime.Value, error) {
	if v, ok := ctx.Get(n.Str); ok {
		return v, nil
	}
	// A bare macro name resolves to a macro callable reference so it can be called
	// directly (the macro namespace, spec 01 Section 5.3). The call site detects
	// the macro by name; here we surface a sentinel only when a macro exists.
	if _, ok := in.macros[n.Str]; ok {
		return runtime.Obj(&macroRef{name: n.Str}), nil
	}
	if allowAbsent || !in.eng.StrictVariables() {
		return runtime.Null(), nil
	}
	return runtime.Null(), posErr(n, errors.New(errors.KindUndefined,
		"undefined variable %q (available: %s)", n.Str, joinNames(ctx.Names())))
}

// evalSpecialName resolves _self / _context / _charset (spec 01 Section 1.7).
func (in *interp) evalSpecialName(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	switch n.Str {
	case "_self":
		// _self exposes the current template's macros for the me.macro() path.
		return runtime.Obj(&selfRef{tmpl: in.root}), nil
	case "_context":
		a := runtime.NewArray()
		for _, name := range ctx.Names() {
			v, _ := ctx.Get(name)
			a.SetStr(name, v)
		}
		return runtime.Arr(a), nil
	case "_charset":
		return runtime.Str("UTF-8"), nil
	default:
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"unknown special name %q", n.Str))
	}
}

// evalList builds a sequence *Array, flattening spread elements (spec 02).
func (in *interp) evalList(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	out := runtime.NewArray()
	idx := int64(0)
	for _, el := range n.Children {
		if el.Kind == ast.KindSpread {
			src, err := in.eval(el.Child(0), ctx, false)
			if err != nil {
				return runtime.Null(), err
			}
			if src.Kind == runtime.KArray && src.Arr != nil {
				for _, p := range src.Arr.Pairs() {
					out.SetInt(idx, p.Val)
					idx++
				}
			}
			continue
		}
		v, err := in.eval(el, ctx, false)
		if err != nil {
			return runtime.Null(), err
		}
		out.SetInt(idx, v)
		idx++
	}
	return runtime.Arr(out), nil
}

// evalMap builds a mapping *Array from keyed/shorthand/computed/spread entries.
func (in *interp) evalMap(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	out := runtime.NewArray()
	for _, e := range n.Children {
		switch e.Int {
		case ast.MapEntryKeyed:
			key := e.Child(0) // a KindString
			v, err := in.eval(e.Child(1), ctx, false)
			if err != nil {
				return runtime.Null(), err
			}
			out.SetStr(key.Str, v)
		case ast.MapEntryShorthand:
			name := e.Child(0) // a KindName
			v, err := in.eval(name, ctx, false)
			if err != nil {
				return runtime.Null(), err
			}
			out.SetStr(name.Str, v)
		case ast.MapEntryComputed:
			kv, err := in.eval(e.Child(0), ctx, false)
			if err != nil {
				return runtime.Null(), err
			}
			v, err := in.eval(e.Child(1), ctx, false)
			if err != nil {
				return runtime.Null(), err
			}
			out.SetKey(keyOf(kv), v)
		case ast.MapEntrySpread:
			src, err := in.eval(e.Child(0), ctx, false)
			if err != nil {
				return runtime.Null(), err
			}
			if src.Kind == runtime.KArray && src.Arr != nil {
				for _, p := range src.Arr.Pairs() {
					out.SetKey(p.Key, p.Val)
				}
			}
		}
	}
	return runtime.Arr(out), nil
}

// keyOf coerces a computed map key to an Int or Str key value. A non-int scalar
// becomes its ToText form; this is the access layer's key model (spec 04 6.2).
func keyOf(v runtime.Value) runtime.Value {
	if v.Kind == runtime.KInt {
		return v
	}
	s, err := runtime.ToText(v)
	if err != nil {
		return runtime.Str("")
	}
	return runtime.Str(s)
}

// evalAttr evaluates a.b / a?.b. The null-safe form short-circuits on a Null
// receiver (spec 04 Section 5). The receiver is evaluated with the SAME
// allowAbsent flag so suppression covers the whole chain (spec 04 Section 8.2).
func (in *interp) evalAttr(n *ast.Node, ctx *runtime.Context, allowAbsent bool) (runtime.Value, error) {
	recv, err := in.eval(n.Child(0), ctx, allowAbsent)
	if err != nil {
		return runtime.Null(), err
	}
	if recv.IsNull() {
		// A null receiver short-circuits the chain under ?. and, under the whole-
		// chain suppression of ?? / default / is defined, when an intermediate hop
		// was absent (spec 04 Section 8.2). Both cases yield Null without erroring.
		if n.Bool || allowAbsent {
			return runtime.Null(), nil
		}
	}
	// Sandbox Phase-2: a property read on a host Object is gated by the policy
	// (B11). The property is checked at the read site (property-then-method
	// precedence: a disallowed property reports a property error here before any
	// method fallback could apply). A Safe receiver / trusted shim bypasses.
	if err := in.checkPropertyAllowed(recv, n.Str); err != nil {
		return runtime.Null(), posErr(n, err)
	}
	v, err := runtime.GetAttribute(recv, runtime.Str(n.Str), runtime.AccessDot, allowAbsent)
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	return v, nil
}

// evalIndex evaluates a[k] / a?[k].
func (in *interp) evalIndex(n *ast.Node, ctx *runtime.Context, allowAbsent bool) (runtime.Value, error) {
	recv, err := in.eval(n.Child(0), ctx, allowAbsent)
	if err != nil {
		return runtime.Null(), err
	}
	if recv.IsNull() && (n.Bool || allowAbsent) {
		return runtime.Null(), nil
	}
	key, err := in.eval(n.Child(1), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	v, err := runtime.GetAttribute(recv, keyOf(key), runtime.AccessIndex, allowAbsent)
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	return v, nil
}

// evalSlice evaluates a[start:end] by delegating to the slice filter semantics
// (rune-based on a string, element-based on a collection), spec 03 Section 2.1.
func (in *interp) evalSlice(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	recv, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	var start, end runtime.Value = runtime.Int(0), runtime.Null()
	hasEnd := false
	if n.Int&ast.SliceHasStart != 0 {
		start, err = in.eval(n.Child(1), ctx, false)
		if err != nil {
			return runtime.Null(), err
		}
	}
	if n.Int&ast.SliceHasEnd != 0 {
		end, err = in.eval(n.Child(2), ctx, false)
		if err != nil {
			return runtime.Null(), err
		}
		hasEnd = true
	}
	// Translate [start:end] into the slice filter's (start, length) form.
	args := []runtime.Value{recv, start}
	if hasEnd {
		length := toInt(end) - toInt(start)
		args = append(args, runtime.Int(length))
	}
	res, err := callSliceFilter(in, args)
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	return res, nil
}

// evalUnary applies not / - / +.
func (in *interp) evalUnary(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	v, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	switch n.Str {
	case "not":
		return runtime.Bool(!runtime.Truthy(v)), nil
	case "-":
		return negate(n, v)
	case "+":
		if v.Kind == runtime.KInt || v.Kind == runtime.KFloat {
			return v, nil
		}
		return runtime.Null(), posErr(n, errors.New(errors.KindArithmetic,
			"unary + expects a number, got %s", v.Kind))
	default:
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"unknown unary operator %q", n.Str))
	}
}

func negate(n *ast.Node, v runtime.Value) (runtime.Value, error) {
	switch v.Kind {
	case runtime.KInt:
		return runtime.Int(-v.I), nil
	case runtime.KFloat:
		return runtime.Float(-v.F), nil
	default:
		return runtime.Null(), posErr(n, errors.New(errors.KindArithmetic,
			"unary - expects a number, got %s", v.Kind))
	}
}

// evalLogical short-circuits and / or / xor over the one truthiness rule.
func (in *interp) evalLogical(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	left, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	lt := runtime.Truthy(left)
	switch n.Str {
	case "and":
		if !lt {
			return runtime.Bool(false), nil
		}
		right, err := in.eval(n.Child(1), ctx, false)
		if err != nil {
			return runtime.Null(), err
		}
		return runtime.Bool(runtime.Truthy(right)), nil
	case "or":
		if lt {
			return runtime.Bool(true), nil
		}
		right, err := in.eval(n.Child(1), ctx, false)
		if err != nil {
			return runtime.Null(), err
		}
		return runtime.Bool(runtime.Truthy(right)), nil
	case "xor":
		right, err := in.eval(n.Child(1), ctx, false)
		if err != nil {
			return runtime.Null(), err
		}
		return runtime.Bool(lt != runtime.Truthy(right)), nil
	default:
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"unknown logical operator %q", n.Str))
	}
}

// evalTernary, evalCoalesce, evalElvis implement c?a:b, a??b, a?:b. The coalesce
// evaluates its left operand under allowAbsent so an undefined chain falls back
// rather than erroring (spec 04 Section 8.2).
func (in *interp) evalTernary(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	cond, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	if runtime.Truthy(cond) {
		return in.eval(n.Child(1), ctx, false)
	}
	return in.eval(n.Child(2), ctx, false)
}

func (in *interp) evalCoalesce(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	left, err := in.eval(n.Child(0), ctx, true) // suppress undefined over the whole left
	if err != nil {
		return runtime.Null(), err
	}
	if !left.IsNull() {
		return left, nil
	}
	return in.eval(n.Child(1), ctx, false)
}

func (in *interp) evalElvis(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	left, err := in.eval(n.Child(0), ctx, true)
	if err != nil {
		return runtime.Null(), err
	}
	if runtime.Truthy(left) {
		return left, nil
	}
	return in.eval(n.Child(1), ctx, false)
}

// evalAssign performs an inline assignment "{{ b = expr }}", binding b and
// yielding the value (spec 01 Section 4.3). Only a plain name target is
// supported inline this slice; destructuring inline is deferred.
func (in *interp) evalAssign(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	target := n.Child(0)
	val, err := in.eval(n.Child(1), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	if target.Kind != ast.KindTarget && target.Kind != ast.KindName {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"inline assignment supports a single name target only"))
	}
	ctx.Set(target.Str, val)
	return val, nil
}

// joinNames renders a name list for an undefined-variable hint.
func joinNames(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	out := ""
	for i, s := range names {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
