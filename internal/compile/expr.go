package compile

import (
	"fmt"
	"strconv"

	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
)

// expr lowers an expression node, emitting the statements it needs, and
// returns the Go expression holding its value. allowAbsent threads the
// interpreter's whole-chain undefined suppression (set by ??, the default
// filter, and is defined) into name and member reads. The returned expression
// is safe to embed exactly once, immediately; callers that consume it later
// spill it first.
func (c *compiler) expr(n *ast.Node, allowAbsent bool) (string, error) {
	switch n.Kind {
	case ast.KindInt:
		return fmt.Sprintf("runtime.Int(%s)", strconv.FormatInt(n.Int, 10)), nil
	case ast.KindFloat:
		return fmt.Sprintf("runtime.Float(%s)", strconv.FormatFloat(n.Float, 'g', -1, 64)), nil
	case ast.KindString:
		return fmt.Sprintf("runtime.Str(%s)", q(n.Str)), nil
	case ast.KindBool:
		return fmt.Sprintf("runtime.Bool(%v)", n.Bool), nil
	case ast.KindNull:
		return "runtime.Null()", nil
	case ast.KindName:
		return c.readName(n.Str, n.Line, allowAbsent), nil
	case ast.KindSpecialName:
		return c.exprSpecialName(n)
	case ast.KindList:
		return c.exprList(n)
	case ast.KindMap:
		return c.exprMap(n)
	case ast.KindAttr:
		return c.exprAttr(n, allowAbsent)
	case ast.KindIndex:
		return c.exprIndex(n, allowAbsent)
	case ast.KindSlice:
		return c.exprSlice(n)
	case ast.KindCall:
		return c.exprCall(n)
	case ast.KindFilter:
		return c.exprFilter(n)
	case ast.KindUnary:
		return c.exprUnary(n)
	case ast.KindBinary:
		return c.exprBinary(n)
	case ast.KindLogical:
		return c.exprLogical(n)
	case ast.KindPower:
		return c.exprHelper2(n, "qpow")
	case ast.KindMembership:
		return c.exprMembership(n)
	case ast.KindTest:
		return c.exprTest(n)
	case ast.KindTernary:
		return c.exprTernary(n)
	case ast.KindCoalesce:
		return c.exprCoalesce(n, "IsNull")
	case ast.KindElvis:
		return c.exprCoalesce(n, "elvis")
	case ast.KindAssign:
		return c.exprAssign(n)
	case ast.KindArrow:
		return c.exprArrow(n)
	default:
		return "", c.notCompilable(fmt.Sprintf("%s expression", n.Kind), n)
	}
}

// exprSpecialName lowers _context and _charset; _self carries the macro
// surface and is outside the compilable subset.
func (c *compiler) exprSpecialName(n *ast.Node) (string, error) {
	switch n.Str {
	case "_charset":
		return `runtime.Str("UTF-8")`, nil
	case "_context":
		arr := c.emitContext()
		return fmt.Sprintf("runtime.Arr(%s)", arr), nil
	default:
		return "", c.notCompilable(fmt.Sprintf("special name %q", n.Str), n)
	}
}

// exprList lowers a sequence literal, flattening spread elements exactly like
// the interpreter's evalList.
func (c *compiler) exprList(n *ast.Node) (string, error) {
	arr := c.tmp("qa")
	c.linef("%s := runtime.NewArray()", arr)
	hasSpread := false
	for _, el := range n.Children {
		if el != nil && el.Kind == ast.KindSpread {
			hasSpread = true
		}
	}
	if !hasSpread {
		for i, el := range n.Children {
			v, err := c.expr(el, false)
			if err != nil {
				return "", err
			}
			c.linef("%s.SetInt(%d, %s)", arr, i, v)
		}
		return fmt.Sprintf("runtime.Arr(%s)", arr), nil
	}
	idx := c.tmp("qi")
	c.linef("%s := int64(0)", idx)
	for _, el := range n.Children {
		if el.Kind == ast.KindSpread {
			v, err := c.expr(el.Child(0), false)
			if err != nil {
				return "", err
			}
			v = c.spill(v)
			c.openf("if %s.Kind() == runtime.KArray && %s.AsArray() != nil {", v, v)
			p := c.tmp("qp")
			c.openf("for _, %s := range %s.AsArray().Pairs() {", p, v)
			c.linef("%s.SetInt(%s, %s.Val)", arr, idx, p)
			c.linef("%s++", idx)
			c.closeb()
			c.closeb()
			continue
		}
		v, err := c.expr(el, false)
		if err != nil {
			return "", err
		}
		c.linef("%s.SetInt(%s, %s)", arr, idx, v)
		c.linef("%s++", idx)
	}
	return fmt.Sprintf("runtime.Arr(%s)", arr), nil
}

// exprMap lowers a mapping literal with keyed, shorthand, computed, and
// spread entries exactly like the interpreter's evalMap.
func (c *compiler) exprMap(n *ast.Node) (string, error) {
	arr := c.tmp("qa")
	c.linef("%s := runtime.NewArray()", arr)
	for _, e := range n.Children {
		switch e.Int {
		case ast.MapEntryKeyed:
			v, err := c.expr(e.Child(1), false)
			if err != nil {
				return "", err
			}
			c.linef("%s.SetStr(%s, %s)", arr, q(e.Child(0).Str), v)
		case ast.MapEntryShorthand:
			name := e.Child(0)
			v, err := c.expr(name, false)
			if err != nil {
				return "", err
			}
			c.linef("%s.SetStr(%s, %s)", arr, q(name.Str), v)
		case ast.MapEntryComputed:
			kv, err := c.expr(e.Child(0), false)
			if err != nil {
				return "", err
			}
			kv = c.spill(kv)
			v, err := c.expr(e.Child(1), false)
			if err != nil {
				return "", err
			}
			c.linef("%s.SetKey(qkeyOf(%s), %s)", arr, kv, v)
		case ast.MapEntrySpread:
			v, err := c.expr(e.Child(0), false)
			if err != nil {
				return "", err
			}
			v = c.spill(v)
			c.openf("if %s.Kind() == runtime.KArray && %s.AsArray() != nil {", v, v)
			p := c.tmp("qp")
			c.openf("for _, %s := range %s.AsArray().Pairs() {", p, v)
			c.linef("%s.SetKey(%s.Key, %s.Val)", arr, p, p)
			c.closeb()
			c.closeb()
		}
	}
	return fmt.Sprintf("runtime.Arr(%s)", arr), nil
}

// exprAttr lowers a.b / a?.b: the null-safe form and whole-chain suppression
// short-circuit a null receiver to null; otherwise the read routes through
// runtime.GetAttribute exactly like evalAttr. The strict form opens with an
// inline KArray fast path reading through Arr.GetStr (the same canonicalizing
// lookup getDot performs, so key semantics are inherited), and any miss or
// non-Array receiver re-enters the unchanged GetAttribute, which produces the
// byte-identical value or error. The receiver skips the general spill through
// spillAdjacent: every use sits in the adjacent statements emitted here, the
// literal member name needs no evaluation between them, and the hit value is
// captured into its own temporary, so no later lowering can observe or rebind
// the receiver mid-read. A read the loop escape analysis approved lowers to
// inline loop arithmetic instead; such a read is total (every approved field
// is always defined on a live loop), so neither the null-safe form nor absence
// suppression can change its value.
func (c *compiler) exprAttr(n *ast.Node, allowAbsent bool) (string, error) {
	if ir, ok := c.an.inlineReads[n]; ok {
		return c.emitInlineLoopField(ir), nil
	}
	recv, err := c.expr(n.Child(0), allowAbsent)
	if err != nil {
		return "", err
	}
	if !n.Bool && !allowAbsent {
		recv = c.spillAdjacent(recv)
		v := c.tmp("qt")
		hit := c.tmp("qk")
		c.linef("var %s runtime.Value", v)
		c.linef("%s := false", hit)
		c.openf("if %s.Kind() == runtime.KArray && %s.AsArray() != nil {", recv, recv)
		c.linef("%s, %s = %s.AsArray().GetStr(%s)", v, hit, recv, q(n.Str))
		c.closeb()
		c.openf("if !%s {", hit)
		gv := c.tmp("qt")
		e := c.tmp("qe")
		c.linef("%s, %s := runtime.GetAttribute(%s, runtime.Str(%s), runtime.AccessDot, false)", gv, e, recv, q(n.Str))
		c.checkErr(e, n.Line)
		c.linef("%s = %s", v, gv)
		c.closeb()
		return v, nil
	}
	recv = c.spill(recv)
	res := c.tmp("qt")
	c.linef("%s := runtime.Null()", res)
	c.openf("if !%s.IsNull() {", recv)
	v := c.tmp("qt")
	e := c.tmp("qe")
	c.linef("%s, %s := runtime.GetAttribute(%s, runtime.Str(%s), runtime.AccessDot, %v)", v, e, recv, q(n.Str), allowAbsent)
	c.checkErr(e, n.Line)
	c.linef("%s = %s", res, v)
	c.closeb()
	return res, nil
}

// exprIndex lowers a[k] / a?[k] like evalIndex, evaluating the key only when
// the null-receiver short-circuit does not fire. A string-literal subscript
// the loop escape analysis approved is the dotted read and lowers to inline
// loop arithmetic; its literal key needs no evaluation.
func (c *compiler) exprIndex(n *ast.Node, allowAbsent bool) (string, error) {
	if ir, ok := c.an.inlineReads[n]; ok {
		return c.emitInlineLoopField(ir), nil
	}
	recv, err := c.expr(n.Child(0), allowAbsent)
	if err != nil {
		return "", err
	}
	recv = c.spill(recv)
	if !n.Bool && !allowAbsent {
		key, err := c.expr(n.Child(1), false)
		if err != nil {
			return "", err
		}
		key = c.spill(key)
		v := c.tmp("qt")
		e := c.tmp("qe")
		c.linef("%s, %s := runtime.GetAttribute(%s, qkeyOf(%s), runtime.AccessIndex, false)", v, e, recv, key)
		c.checkErr(e, n.Line)
		return v, nil
	}
	res := c.tmp("qt")
	c.linef("%s := runtime.Null()", res)
	c.openf("if !%s.IsNull() {", recv)
	c.condDepth++
	key, err := c.expr(n.Child(1), false)
	if err != nil {
		return "", err
	}
	key = c.spill(key)
	v := c.tmp("qt")
	e := c.tmp("qe")
	c.linef("%s, %s := runtime.GetAttribute(%s, qkeyOf(%s), runtime.AccessIndex, %v)", v, e, recv, key, allowAbsent)
	c.checkErr(e, n.Line)
	c.linef("%s = %s", res, v)
	c.condDepth--
	c.closeb()
	return res, nil
}

// exprSlice lowers a[start:end] through the slice filter in the (start,
// length) form the interpreter's evalSlice uses.
func (c *compiler) exprSlice(n *ast.Node) (string, error) {
	recv, err := c.expr(n.Child(0), false)
	if err != nil {
		return "", err
	}
	recv = c.spill(recv)
	start := "runtime.Int(0)"
	if n.Int&ast.SliceHasStart != 0 {
		start, err = c.expr(n.Child(1), false)
		if err != nil {
			return "", err
		}
		start = c.spill(start)
	}
	end := ""
	if n.Int&ast.SliceHasEnd != 0 {
		end, err = c.expr(n.Child(2), false)
		if err != nil {
			return "", err
		}
		end = c.spill(end)
	}
	fv, fok := c.callable("Filter", "slice")
	c.openf("if !%s {", fok)
	c.linef(c.ret(c.qposE(`qerrors.New(qerrors.KindRuntime, "slice filter is not registered")`, n.Line)))
	c.closeb()
	args := c.tmp("qa")
	c.linef("%s := []runtime.Value{%s, %s}", args, recv, start)
	if end != "" {
		c.linef("%s = append(%s, runtime.Int(qtoi(%s)-qtoi(%s)))", args, args, end, start)
	}
	v := c.tmp("qt")
	e := c.tmp("qe")
	c.linef("%s, %s := %s.Fn(ctx, %s)", v, e, fv, args)
	c.checkErr(e, n.Line)
	return v, nil
}

// exprUnary lowers not / - / +.
func (c *compiler) exprUnary(n *ast.Node) (string, error) {
	v, err := c.expr(n.Child(0), false)
	if err != nil {
		return "", err
	}
	switch n.Str {
	case "not":
		return fmt.Sprintf("runtime.Bool(!runtime.Truthy(%s))", c.spill(v)), nil
	case "-":
		return c.helperCall1("qneg", v, n.Line), nil
	case "+":
		return c.helperCall1("qplus", v, n.Line), nil
	default:
		return "", c.notCompilable(fmt.Sprintf("unary operator %q", n.Str), n)
	}
}

// helperCall1 emits a one-operand helper call with the standard error check.
func (c *compiler) helperCall1(helper, operand string, line int) string {
	v := c.tmp("qt")
	e := c.tmp("qe")
	c.linef("%s, %s := %s(%s)", v, e, helper, operand)
	c.checkErr(e, line)
	return v
}

// exprHelper2 lowers a two-operand node through a generated helper carrying
// the interpreter's exact error text, wrapping errors at this node's line.
func (c *compiler) exprHelper2(n *ast.Node, helper string) (string, error) {
	l, err := c.expr(n.Child(0), false)
	if err != nil {
		return "", err
	}
	l = c.spill(l)
	r, err := c.expr(n.Child(1), false)
	if err != nil {
		return "", err
	}
	v := c.tmp("qt")
	e := c.tmp("qe")
	c.linef("%s, %s := %s(%s, %s)", v, e, helper, l, r)
	c.checkErr(e, n.Line)
	return v, nil
}

// exprBinary lowers the KindBinary operator family: concat, equality,
// ordering, arithmetic, range, and bitwise.
func (c *compiler) exprBinary(n *ast.Node) (string, error) {
	op := n.Str
	if op == "~" {
		return c.exprHelper2(n, "qconcat")
	}
	l, err := c.expr(n.Child(0), false)
	if err != nil {
		return "", err
	}
	l = c.spill(l)
	r, err := c.expr(n.Child(1), false)
	if err != nil {
		return "", err
	}
	r = c.spill(r)
	switch op {
	case "==":
		return fmt.Sprintf("runtime.Bool(runtime.Equal(%s, %s))", l, r), nil
	case "!=":
		return fmt.Sprintf("runtime.Bool(!runtime.Equal(%s, %s))", l, r), nil
	case "<", ">", "<=", ">=", "<=>":
		v := c.tmp("qt")
		e := c.tmp("qe")
		c.linef("%s, %s := qcompare(%s, %s, %s)", v, e, q(op), l, r)
		c.checkErr(e, n.Line)
		return v, nil
	case "+", "-", "*", "/", "//", "%":
		v := c.tmp("qt")
		e := c.tmp("qe")
		c.linef("%s, %s := qarith(%s, %s, %s)", v, e, q(op), l, r)
		c.checkErr(e, n.Line)
		return v, nil
	case "..":
		return c.exprRange(n, l, r)
	case "b_or", "b_and", "b_xor":
		v := c.tmp("qt")
		e := c.tmp("qe")
		c.linef("%s, %s := qbitwise(%s, %s, %s)", v, e, q(op), l, r)
		c.checkErr(e, n.Line)
		return v, nil
	default:
		return "", c.notCompilable(fmt.Sprintf("binary operator %q", op), n)
	}
}

// exprRange lowers the .. operator through the registered range function; its
// errors surface unpositioned exactly like the interpreter's callRange.
func (c *compiler) exprRange(n *ast.Node, l, r string) (string, error) {
	fv, fok := c.callable("Function", "range")
	c.openf("if !%s {", fok)
	c.linef(c.ret(`qerrors.New(qerrors.KindRuntime, "range function is not registered")`))
	c.closeb()
	v := c.tmp("qt")
	e := c.tmp("qe")
	c.linef("%s, %s := %s.Fn(ctx, []runtime.Value{%s, %s})", v, e, fv, l, r)
	c.openf("if %s != nil {", e)
	c.linef(c.ret(e))
	c.closeb()
	return v, nil
}

// exprLogical lowers and/or/xor with the interpreter's short-circuiting and
// its Bool-valued results.
func (c *compiler) exprLogical(n *ast.Node) (string, error) {
	l, err := c.expr(n.Child(0), false)
	if err != nil {
		return "", err
	}
	l = c.spill(l)
	switch n.Str {
	case "and", "or":
		res := c.tmp("qt")
		if n.Str == "and" {
			c.linef("%s := runtime.Bool(false)", res)
			c.openf("if runtime.Truthy(%s) {", l)
		} else {
			c.linef("%s := runtime.Bool(true)", res)
			c.openf("if !runtime.Truthy(%s) {", l)
		}
		c.condDepth++
		r, err := c.expr(n.Child(1), false)
		if err != nil {
			return "", err
		}
		c.linef("%s = runtime.Bool(runtime.Truthy(%s))", res, r)
		c.condDepth--
		c.closeb()
		return res, nil
	case "xor":
		r, err := c.expr(n.Child(1), false)
		if err != nil {
			return "", err
		}
		r = c.spill(r)
		return fmt.Sprintf("runtime.Bool(runtime.Truthy(%s) != runtime.Truthy(%s))", l, r), nil
	default:
		return "", c.notCompilable(fmt.Sprintf("logical operator %q", n.Str), n)
	}
}

// exprMembership lowers in / not in / starts with / ends with / matches /
// has some / has every.
func (c *compiler) exprMembership(n *ast.Node) (string, error) {
	l, err := c.expr(n.Child(0), false)
	if err != nil {
		return "", err
	}
	l = c.spill(l)
	r, err := c.expr(n.Child(1), false)
	if err != nil {
		return "", err
	}
	r = c.spill(r)
	switch n.Str {
	case "in":
		b := c.tmp("qt")
		e := c.tmp("qe")
		c.linef("%s, %s := runtime.In(%s, %s)", b, e, l, r)
		c.checkErr(e, n.Line)
		if n.Bool {
			return fmt.Sprintf("runtime.Bool(!%s)", b), nil
		}
		return fmt.Sprintf("runtime.Bool(%s)", b), nil
	case "starts with", "ends with":
		v := c.tmp("qt")
		e := c.tmp("qe")
		c.linef("%s, %s := qaffix(%s, %s, %v)", v, e, l, r, n.Str == "starts with")
		c.checkErr(e, n.Line)
		return v, nil
	case "matches":
		v := c.tmp("qt")
		e := c.tmp("qe")
		c.linef("%s, %s := qmatches(%s, %s)", v, e, l, r)
		c.checkErr(e, n.Line)
		return v, nil
	case "has some", "has every":
		v := c.tmp("qt")
		e := c.tmp("qe")
		c.linef("%s, %s := qquantify(%s, %s, %s, %v, %v)", v, e, q(n.Str), l, r, n.Str == "has every", c.lenient)
		c.checkErr(e, n.Line)
		return v, nil
	default:
		return "", c.notCompilable(fmt.Sprintf("membership operator %q", n.Str), n)
	}
}

// exprTernary lowers c ? a : b (and the desugared postfix conditional).
func (c *compiler) exprTernary(n *ast.Node) (string, error) {
	res := c.tmp("qt")
	c.linef("var %s runtime.Value", res)
	cond, err := c.expr(n.Child(0), false)
	if err != nil {
		return "", err
	}
	c.openf("if runtime.Truthy(%s) {", cond)
	c.condDepth++
	a, err := c.expr(n.Child(1), false)
	if err != nil {
		return "", err
	}
	c.linef("%s = %s", res, a)
	c.ind--
	c.linef("} else {")
	c.ind++
	b, err := c.expr(n.Child(2), false)
	if err != nil {
		return "", err
	}
	c.linef("%s = %s", res, b)
	c.condDepth--
	c.closeb()
	return res, nil
}

// exprCoalesce lowers ?? (mode "IsNull") and ?: (mode "elvis"): the left
// operand evaluates under whole-chain undefined suppression, and the right
// evaluates only when the fallback fires.
func (c *compiler) exprCoalesce(n *ast.Node, mode string) (string, error) {
	l, err := c.expr(n.Child(0), true)
	if err != nil {
		return "", err
	}
	res := c.tmp("qt")
	c.linef("%s := %s", res, l)
	if mode == "IsNull" {
		c.openf("if %s.IsNull() {", res)
	} else {
		c.openf("if !runtime.Truthy(%s) {", res)
	}
	c.condDepth++
	r, err := c.expr(n.Child(1), false)
	if err != nil {
		return "", err
	}
	c.linef("%s = %s", res, r)
	c.condDepth--
	c.closeb()
	return res, nil
}

// exprAssign lowers the inline assignment "{{ b = expr }}": it binds the name
// in the current frame and yields the value, like evalAssign.
func (c *compiler) exprAssign(n *ast.Node) (string, error) {
	target := n.Child(0)
	val, err := c.expr(n.Child(1), false)
	if err != nil {
		return "", err
	}
	val = c.spill(val)
	if target == nil || (target.Kind != ast.KindTarget && target.Kind != ast.KindName) {
		res := c.tmp("qt")
		c.linef("%s := runtime.Null()", res)
		c.openf("if true {")
		c.linef(c.ret(c.qposE(`qerrors.New(qerrors.KindRuntime, "inline assignment supports a single name target only")`, n.Line)))
		c.closeb()
		return res, nil
	}
	c.bindName(target.Str, val, false)
	return val, nil
}

// exprArrow lowers an arrow function to a Go closure over the enclosing
// locals: the engine's live-lexical capture contract is exactly Go closure
// semantics. The closure satisfies the runtime callable protocol through the
// generated qArrow host object.
func (c *compiler) exprArrow(n *ast.Node) (string, error) {
	if len(n.Children) == 0 {
		res := c.tmp("qt")
		c.linef("%s := runtime.Null()", res)
		c.openf("if true {")
		c.linef(c.ret(c.qposE(`qerrors.New(qerrors.KindRuntime, "arrow function has no body")`, n.Line)))
		c.closeb()
		return res, nil
	}
	body := n.Children[len(n.Children)-1]
	var params []*ast.Node
	for _, ch := range n.Children[:len(n.Children)-1] {
		if ch.Kind == ast.KindParam {
			params = append(params, ch)
		}
	}

	// Prescan the arrow's invocation frame: its parameters plus any inline
	// assignments in the body or parameter defaults.
	var binds []string
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		binds = append(binds, name)
	}
	for _, p := range params {
		add(p.Str)
		for _, ch := range p.Children {
			exprBinds(ch, add)
		}
	}
	exprBinds(body, add)
	if err := c.checkBindNames(binds, n); err != nil {
		return "", err
	}

	res := c.tmp("qt")
	argsVar := c.tmp("qa")
	c.openf("%s := runtime.Obj(&qArrow{fn: func(%s []runtime.Value) (runtime.Value, error) {", res, argsVar)
	c.linef("_ = %s", argsVar)
	c.pushFrame(frameArrow, binds)
	c.retPrefix = append(c.retPrefix, "runtime.Null(), ")
	savedCond := c.condDepth
	c.condDepth = 0
	c.inArrow++

	err := c.emitArrowParams(params, argsVar)
	if err == nil {
		var out string
		out, err = c.expr(body, false)
		if err == nil {
			c.linef("return %s, nil", out)
		}
	}

	c.inArrow--
	c.condDepth = savedCond
	c.retPrefix = c.retPrefix[:len(c.retPrefix)-1]
	c.popFrame()
	c.ind--
	c.linef("}})")
	return res, err
}

// emitArrowParams binds the call arguments to the arrow's parameters exactly
// like arrowClosure.Invoke: positional binding, a variadic tail collecting the
// rest, and per-parameter defaults (else null) for missing arguments.
func (c *compiler) emitArrowParams(params []*ast.Node, argsVar string) error {
	for i, p := range params {
		if p.Bool { // variadic: collect the remaining arguments
			rest := c.tmp("qa")
			c.linef("%s := runtime.NewArray()", rest)
			idx := c.tmp("qi")
			c.linef("%s := int64(0)", idx)
			j := c.tmp("qj")
			c.openf("for %s := %d; %s < len(%s); %s++ {", j, i, j, argsVar, j)
			c.linef("%s.SetInt(%s, %s[%s])", rest, idx, argsVar, j)
			c.linef("%s++", idx)
			c.closeb()
			c.bindName(p.Str, fmt.Sprintf("runtime.Arr(%s)", rest), false)
			return nil
		}
		c.openf("if %d < len(%s) {", i, argsVar)
		c.bindName(p.Str, fmt.Sprintf("%s[%d]", argsVar, i), false)
		c.ind--
		c.linef("} else {")
		c.ind++
		if p.Int&ast.ParamHasDefault != 0 {
			def := p.Children[len(p.Children)-1]
			v, err := c.expr(def, false)
			if err != nil {
				return err
			}
			c.bindName(p.Str, v, false)
		} else {
			c.bindName(p.Str, "runtime.Null()", false)
		}
		c.closeb()
	}
	return nil
}
