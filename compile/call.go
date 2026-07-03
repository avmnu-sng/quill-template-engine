package compile

import (
	"fmt"

	"github.com/avmnu-sng/quill-template-engine/ast"
)

// engineBoundFunctions names the registered functions whose behavior depends
// on the live engine itself -- the loader and render surface behind
// interp.EngineFromValue; a compiled render's qEnv handle carries only the
// engine configuration, so calls to them are outside the compilable subset.
var engineBoundFunctions = map[string]string{
	"include":              `engine-bound function "include"`,
	"source":               `engine-bound function "source"`,
	"template_from_string": `engine-bound function "template_from_string"`,
}

// compositionBuiltins names the call-site builtins the interpreter resolves
// before the function registry; they carry the composition surface and are
// outside the compilable subset.
var compositionBuiltins = map[string]string{
	"parent": `function "parent"`,
	"block":  `function "block"`,
	"caller": `function "caller"`,
	"slot":   `function "slot"`,
}

// collectArgs lowers a call/filter node's KindArg children into a positional
// []runtime.Value local, prepended with the given prefix expressions, exactly
// like the interpreter's collectArgs: named arguments flatten in source order
// and spreads expand array elements.
func (c *compiler) collectArgs(n *ast.Node, prefix []string) (string, error) {
	args := c.tmp("qa")
	pre := ""
	for i, p := range prefix {
		if i > 0 {
			pre += ", "
		}
		pre += p
	}
	c.linef("%s := []runtime.Value{%s}", args, pre)
	for _, ch := range n.Children {
		if ch.Kind != ast.KindArg {
			continue
		}
		switch ch.Int {
		case ast.ArgPositional, ast.ArgNamed:
			v, err := c.expr(ch.Child(0), false)
			if err != nil {
				return "", err
			}
			c.linef("%s = append(%s, %s)", args, args, v)
		case ast.ArgSpread:
			v, err := c.expr(ch.Child(0), false)
			if err != nil {
				return "", err
			}
			v = c.spill(v)
			c.openf("if %s.Kind == runtime.KArray && %s.Arr != nil {", v, v)
			p := c.tmp("qp")
			c.openf("for _, %s := range %s.Arr.Pairs() {", p, v)
			c.linef("%s = append(%s, %s.Val)", args, args, p)
			c.closeb()
			c.closeb()
		}
	}
	return args, nil
}

// staticArgCount reports the number of KindArg children and whether that
// count is static (no spread argument).
func staticArgCount(n *ast.Node) (int, bool) {
	count := 0
	for _, ch := range n.Children {
		if ch.Kind != ast.KindArg {
			continue
		}
		if ch.Int == ast.ArgSpread {
			return 0, false
		}
		count++
	}
	return count, true
}

// emitInject applies the Needs* engine-value injection to the args local for
// the resolved callable in cv, mirroring the interpreter's inject: the values
// prepend in the fixed order environment, context, charset. The whole path
// sits behind the ref's hoisted injection flag in inj -- true exactly when
// any Needs* flag is set -- so an injection-free callable leaves args
// untouched on one bool, exactly what qinject would have returned. A
// needs-environment callable receives the compiled render's qEnv handle,
// which carries the Options' engine configuration (tab width, random seed)
// through the ext.EngineConfig surface the width- and seed-aware callables
// consume.
func (c *compiler) emitInject(cv, inj, args string) {
	c.openf("if %s {", inj)
	ctx := c.tmp("qca")
	c.linef("var %s *runtime.Array", ctx)
	c.openf("if %s.NeedsContext {", cv)
	arr := c.emitContext()
	c.linef("%s = %s", ctx, arr)
	c.closeb()
	c.linef("%s = qinject(qEnvVal, %s.NeedsEnvironment, %s.NeedsContext, %s.NeedsCharset, %s, %s)", args, cv, cv, cv, ctx, args)
	c.closeb()
}

// exprFilter lowers "x | name(args)" like evalFilter: the piped value is the
// first argument (evaluated under undefined suppression for the default
// filter), the filter resolves through the registry at the call site, and the
// Needs* injection prepends engine values. A site with zero explicit
// arguments branches on the hoisted fast flag: when the resolved filter
// publishes Fn1 and needs no injection, the call dispatches on the piped
// value alone and no argument slice exists; the else arm is the general path
// verbatim, so a filter without Fn1 (every host filter that does not opt in)
// is lowered exactly as before. The unknown-filter check stays at the call
// site because its timing is observable in streamed output.
func (c *compiler) exprFilter(n *ast.Node) (string, error) {
	piped, err := c.expr(n.Child(0), n.Str == "default")
	if err != nil {
		return "", err
	}
	piped = c.spill(piped)
	fv, fok := c.callable("Filter", n.Str)
	c.openf("if !%s {", fok)
	c.linef(c.ret(fmt.Sprintf("qpos(qerrors.New(qerrors.KindRuntime, \"unknown filter %%q\", %s), %d)", q(n.Str), n.Line)))
	c.closeb()
	v := c.tmp("qt")
	e := c.tmp("qe")
	if count, static := staticArgCount(n); static && count == 0 {
		ffast := c.callableFilterFast(n.Str)
		c.linef("var %s runtime.Value", v)
		c.linef("var %s error", e)
		c.openf("if %s {", ffast)
		c.linef("%s, %s = %s.Fn1(%s)", v, e, fv, piped)
		c.ind--
		c.linef("} else {")
		c.ind++
		args, err := c.collectArgs(n, []string{piped})
		if err != nil {
			return "", err
		}
		c.emitInject(fv, c.callableInject("Filter", n.Str), args)
		c.linef("%s, %s = %s.Fn(%s)", v, e, fv, args)
		c.closeb()
		c.checkErr(e, n.Line)
		return v, nil
	}
	args, err := c.collectArgs(n, []string{piped})
	if err != nil {
		return "", err
	}
	c.emitInject(fv, c.callableInject("Filter", n.Str), args)
	c.linef("%s, %s := %s.Fn(%s)", v, e, fv, args)
	c.checkErr(e, n.Line)
	return v, nil
}

// exprCall lowers "callee(args)" like evalCall: a bare-name callee resolves
// through the function registry, then through a callable value bound in
// scope; a dotted callee is a host method call; loop.changed is recognized
// syntactically against the lexically enclosing loop.
func (c *compiler) exprCall(n *ast.Node) (string, error) {
	callee := n.Child(0)
	if callee == nil {
		return "", c.notCompilable("call with no callee", n)
	}

	if callee.Kind == ast.KindName {
		name := callee.Str
		if construct, ok := compositionBuiltins[name]; ok {
			return "", c.notCompilable(construct, n)
		}
		if construct, ok := engineBoundFunctions[name]; ok {
			return "", c.notCompilable(construct, n)
		}
		return c.exprNameCall(n, name)
	}

	// loop.changed(expr) is recognized syntactically before the receiver is
	// evaluated as a value, exactly like the interpreter.
	if callee.Kind == ast.KindAttr && callee.Str == "changed" &&
		callee.Child(0) != nil && callee.Child(0).Kind == ast.KindName && callee.Child(0).Str == "loop" {
		return c.exprLoopChanged(n)
	}

	if callee.Kind == ast.KindAttr {
		return c.exprMethodCall(n, callee)
	}

	res := c.tmp("qt")
	c.linef("%s := runtime.Null()", res)
	c.openf("if true {")
	c.linef(c.ret(fmt.Sprintf(`qpos(qerrors.New(qerrors.KindRuntime, "expression is not callable"), %d)`, n.Line)))
	c.closeb()
	return res, nil
}

// exprNameCall lowers f(args) for a bare name: the registered function wins,
// then a callable value bound under the name, else the interpreter's unknown
// function-or-macro error.
func (c *compiler) exprNameCall(n *ast.Node, name string) (string, error) {
	res := c.tmp("qt")
	c.linef("var %s runtime.Value", res)
	fv, fok := c.callable("Function", name)
	c.openf("if %s {", fok)
	c.condDepth++
	args, err := c.collectArgs(n, nil)
	if err != nil {
		return "", err
	}
	c.emitInject(fv, c.callableInject("Function", name), args)
	v := c.tmp("qt")
	e := c.tmp("qe")
	c.linef("%s, %s := %s.Fn(%s)", v, e, fv, args)
	c.checkErr(e, n.Line)
	c.linef("%s = %s", res, v)
	c.condDepth--
	c.ind--
	c.linef("} else {")
	c.ind++
	c.condDepth++
	bv, bfound := c.probeName(name)
	bv = c.spill(bv)
	cond := fmt.Sprintf("%s && runtime.IsCallable(%s)", bfound, bv)
	if bfound == "true" {
		cond = fmt.Sprintf("runtime.IsCallable(%s)", bv)
	}
	c.openf("if %s {", cond)
	args, err = c.collectArgs(n, nil)
	if err != nil {
		return "", err
	}
	v2 := c.tmp("qt")
	e2 := c.tmp("qe")
	c.linef("%s, %s := runtime.Call(%s, %s)", v2, e2, bv, args)
	c.checkErr(e2, n.Line)
	c.linef("%s = %s", res, v2)
	c.ind--
	c.linef("} else {")
	c.ind++
	c.linef(c.ret(fmt.Sprintf("qpos(qerrors.New(qerrors.KindRuntime, \"unknown function or macro %%q\", %s), %d)", q(name), n.Line)))
	c.closeb()
	c.condDepth--
	c.closeb()
	return res, nil
}

// exprMethodCall lowers a.b(args): the receiver must be a host object, whose
// CallMethod hook runs with the flattened arguments.
func (c *compiler) exprMethodCall(n *ast.Node, callee *ast.Node) (string, error) {
	recv, err := c.expr(callee.Child(0), false)
	if err != nil {
		return "", err
	}
	recv = c.spill(recv)
	args, err := c.collectArgs(n, nil)
	if err != nil {
		return "", err
	}
	res := c.tmp("qt")
	c.linef("var %s runtime.Value", res)
	c.openf("if %s.Kind == runtime.KObject {", recv)
	v := c.tmp("qt")
	e := c.tmp("qe")
	c.linef("%s, %s := %s.Obj.CallMethod(%s, %s)", v, e, recv, q(callee.Str), args)
	c.checkErr(e, n.Line)
	c.linef("%s = %s", res, v)
	c.ind--
	c.linef("} else {")
	c.ind++
	c.linef(c.ret(fmt.Sprintf("qpos(qerrors.New(qerrors.KindAttribute, \"cannot call method %%q on %%s\", %s, %s.Kind), %d)", q(callee.Str), recv, n.Line)))
	c.closeb()
	return res, nil
}

// exprLoopChanged lowers loop.changed(expr) against the lexically enclosing
// loop's per-call-site memory, matching the interpreter's per-loop frame:
// true on the first iteration and whenever the watched value differs from the
// prior iteration by typed equality.
func (c *compiler) exprLoopChanged(n *ast.Node) (string, error) {
	if c.inArrow > 0 {
		return "", c.notCompilable("loop.changed inside an arrow function", n)
	}
	args, err := c.collectArgs(n, nil)
	if err != nil {
		return "", err
	}
	count, static := staticArgCount(n)
	if !static || count != 1 {
		c.openf("if len(%s) != 1 {", args)
		c.linef(c.ret(fmt.Sprintf("qpos(qerrors.New(qerrors.KindRuntime, \"loop.changed expects exactly one argument, got %%d\", len(%s)), %d)", args, n.Line)))
		c.closeb()
	}
	li := c.currentLoop()
	if li == nil {
		res := c.tmp("qt")
		c.linef("%s := runtime.Null()", res)
		c.linef("_ = %s", args)
		c.openf("if true {")
		c.linef(c.ret(fmt.Sprintf(`qpos(qerrors.New(qerrors.KindRuntime, "loop.changed is only available inside a for loop"), %d)`, n.Line)))
		c.closeb()
		return res, nil
	}
	site := changedSite{prev: c.tmp("qchp"), seen: c.tmp("qchs")}
	li.changed = append(li.changed, site)
	cur := c.tmp("qt")
	c.linef("%s := %s[0]", cur, args)
	ch := c.tmp("qk")
	c.linef("%s := !%s || !runtime.Equal(%s, %s)", ch, site.seen, site.prev, cur)
	c.linef("%s, %s = %s, true", site.prev, site.seen, cur)
	return fmt.Sprintf("runtime.Bool(%s)", ch), nil
}

// exprTest lowers "x is name" / "x is not name(arg)" like evalTest: is
// defined flips to an existence check; the registry-existence tests consult
// HasFilter/HasFunction/HasTest unless a host test shadows them; every other
// test resolves through the registry.
func (c *compiler) exprTest(n *ast.Node) (string, error) {
	if n.Str == "defined" {
		ok, err := c.emitIsDefined(n.Child(0))
		if err != nil {
			return "", err
		}
		if n.Bool {
			return fmt.Sprintf("runtime.Bool(!%s)", ok), nil
		}
		return fmt.Sprintf("runtime.Bool(%s)", ok), nil
	}
	subject, err := c.expr(n.Child(0), false)
	if err != nil {
		return "", err
	}
	subject = c.spill(subject)

	registryKind := ""
	switch n.Str {
	case "filter":
		registryKind = "HasFilter"
	case "function":
		registryKind = "HasFunction"
	case "test":
		registryKind = "HasTest"
	}

	tv, tok := c.callable("Test", n.Str)
	res := c.tmp("qt")
	c.linef("var %s runtime.Value", res)

	emitOrdinary := func() error {
		args := c.tmp("qa")
		c.linef("%s := []runtime.Value{%s}", args, subject)
		for _, ch := range n.Children[1:] {
			if ch.Kind != ast.KindArg {
				continue
			}
			v, err := c.expr(ch.Child(0), false)
			if err != nil {
				return err
			}
			c.linef("%s = append(%s, %s)", args, args, v)
		}
		b := c.tmp("qk")
		e := c.tmp("qe")
		c.linef("%s, %s := %s.Fn(%s)", b, e, tv, args)
		c.checkErr(e, n.Line)
		if n.Bool {
			c.linef("%s = runtime.Bool(!%s)", res, b)
		} else {
			c.linef("%s = runtime.Bool(%s)", res, b)
		}
		return nil
	}

	if registryKind != "" {
		c.openf("if %s {", tok)
		c.condDepth++
		if err := emitOrdinary(); err != nil {
			return "", err
		}
		c.condDepth--
		c.ind--
		c.linef("} else {")
		c.ind++
		name := c.tmp("qs")
		c.linef("%s := \"\"", name)
		c.openf("if %s.Kind == runtime.KStr || %s.Kind == runtime.KSafe {", subject, subject)
		c.linef("%s = %s.S", name, subject)
		c.closeb()
		present := c.tmp("qk")
		c.linef("%s := %s != \"\" && exts.%s(%s)", present, name, registryKind, name)
		if n.Bool {
			c.linef("%s = runtime.Bool(!%s)", res, present)
		} else {
			c.linef("%s = runtime.Bool(%s)", res, present)
		}
		c.closeb()
		return res, nil
	}

	c.openf("if !%s {", tok)
	c.linef(c.ret(fmt.Sprintf("qpos(qerrors.New(qerrors.KindRuntime, \"unknown test %%q\", %s), %d)", q(n.Str), n.Line)))
	c.closeb()
	if err := emitOrdinary(); err != nil {
		return "", err
	}
	return res, nil
}

// emitIsDefined lowers the "is defined" existence question over an access
// chain without throwing, like the interpreter's isDefined: a bare name tests
// scope presence, member chains probe under suppression, and any other
// expression is defined iff it evaluates without error. It returns a bool
// expression.
func (c *compiler) emitIsDefined(n *ast.Node) (string, error) {
	if n == nil {
		return "false", nil
	}
	switch n.Kind {
	case ast.KindName:
		_, found := c.probeName(n.Str)
		return found, nil
	case ast.KindAttr:
		rv, re, err := c.emitSwallowed(n.Child(0))
		if err != nil {
			return "", err
		}
		ok := c.tmp("qk")
		c.linef("%s := false", ok)
		c.openf("if %s == nil && !%s.IsNull() {", re, rv)
		c.linef("%s = runtime.IsDefinedAttribute(%s, runtime.Str(%s), runtime.AccessDot)", ok, rv, q(n.Str))
		c.closeb()
		return ok, nil
	case ast.KindIndex:
		rv, re, err := c.emitSwallowed(n.Child(0))
		if err != nil {
			return "", err
		}
		ok := c.tmp("qk")
		c.linef("%s := false", ok)
		c.openf("if %s == nil && !%s.IsNull() {", re, rv)
		c.condDepth++
		kv, ke, err := c.emitSwallowed(n.Child(1))
		if err != nil {
			return "", err
		}
		c.openf("if %s == nil {", ke)
		c.linef("%s = runtime.IsDefinedAttribute(%s, qkeyOf(%s), runtime.AccessIndex)", ok, rv, kv)
		c.closeb()
		c.condDepth--
		c.closeb()
		return ok, nil
	default:
		rv, re, err := c.emitSwallowed(n)
		if err != nil {
			return "", err
		}
		c.linef("_ = %s", rv)
		return fmt.Sprintf("(%s == nil)", re), nil
	}
}

// emitSwallowed lowers an expression under undefined suppression inside a
// closure whose error is captured rather than returned, so is defined can
// probe a chain without aborting the render. It returns the value and error
// variable names.
func (c *compiler) emitSwallowed(n *ast.Node) (valVar, errVar string, err error) {
	fn := c.tmp("qd")
	c.openf("%s := func() (runtime.Value, error) {", fn)
	c.retPrefix = append(c.retPrefix, "runtime.Null(), ")
	c.condDepth++
	out, lerr := c.expr(n, true)
	if lerr == nil {
		c.linef("return %s, nil", out)
	}
	c.condDepth--
	c.retPrefix = c.retPrefix[:len(c.retPrefix)-1]
	c.ind--
	c.linef("}")
	if lerr != nil {
		return "", "", lerr
	}
	v := c.tmp("qt")
	e := c.tmp("qe")
	c.linef("%s, %s := %s()", v, e, fn)
	return v, e, nil
}
