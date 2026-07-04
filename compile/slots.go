package compile

import (
	"fmt"

	"github.com/avmnu-sng/quill-template-engine/ast"
)

// hasSlots reports whether the subtree at n contains a deferred-slot construct:
// an @yield, an @provide, or a slot() call. The render function of a unit that
// contains one buffers its output and resolves the yield placeholders over the
// finished buffer, mirroring interp collectStreamInfo's usesSlots decision.
func hasSlots(n *ast.Node) bool {
	if n == nil {
		return false
	}
	switch n.Kind {
	case ast.KindYield, ast.KindProvide:
		return true
	case ast.KindCall:
		if callee := n.Child(0); callee != nil && callee.Kind == ast.KindName && callee.Str == "slot" {
			return true
		}
	}
	for _, ch := range n.Children {
		if hasSlots(ch) {
			return true
		}
	}
	return false
}

// stmtProvide lowers "@provide label { body }" like execProvide: the body
// renders through the active escape strategy into a fresh builder with
// indentation suspended (the shared captureItems shape), and its text is
// APPENDED to the label's slot buffer in render (execution) order. The provide
// emits nothing at its own position; the accumulated content surfaces later at
// the matching @yield. The body binds in the enclosing compile frame, so a
// @set inside a provide copies back exactly as captureItems renders with ctx
// directly rather than a child. The buffer is created on the label's first
// contribution, so a render that provides nothing allocates no map.
func (c *compiler) stmtProvide(n *ast.Node) error {
	sb, err := c.captureBody(n.Children)
	if err != nil {
		return err
	}
	buf := c.tmp("qsb")
	ok := c.tmp("qso")
	c.openf("if qslots == nil {")
	c.linef("qslots = map[string]*strings.Builder{}")
	c.closeb()
	c.linef("%s, %s := qslots[%s]", buf, ok, q(n.Str))
	c.openf("if !%s {", ok)
	c.linef("%s = &strings.Builder{}", buf)
	c.linef("qslots[%s] = %s", q(n.Str), buf)
	c.closeb()
	c.linef("%s.WriteString(%s.String())", buf, sb)
	return nil
}

// stmtYield lowers "@yield label" like execYield: it records the label in
// render order and writes a render-unique placeholder (qtok+label+qtok)
// verbatim through the active writer. The single post-render resolve pass
// substitutes the label's accumulated slot content for the placeholder, so a
// shell may @yield a slot before the partials that feed it. The placeholder is
// written raw -- not through the escape strategy -- because the resolved
// content was already produced through the active escaper by its @provide
// bodies, exactly as execYield's emitString.
//
// A @yield nested inside a capture context is outside the compilable subset:
// its placeholder would embed a token minted from a counter the compile
// package cannot share with the interpreter's process-global yieldCounter, so
// the leaked numeric would diverge. That class is decidably rejected here.
func (c *compiler) stmtYield(n *ast.Node) error {
	if c.captureDepth > 0 {
		return c.notCompilable("@yield inside a capture/provide body", n)
	}
	c.linef("qyielded = append(qyielded, %s)", q(n.Str))
	c.emitWrite(fmt.Sprintf("qtok+%s+qtok", q(n.Str)), func(e string) string { return c.qposE(e, n.Line) })
	return nil
}

// exprSlot lowers the slot(label) function form like callSlot: it returns the
// label's accumulated content AS OF THE CALL, wrapped Safe under an active
// escape strategy (the content is already-escaped, so a downstream print must
// not escape it twice) else Str. A one-argument arity mismatch reproduces the
// interpreter's runtime error text and position. Unlike the deferred @yield it
// is immediate, so it suits a site placed after its @provide contributions.
func (c *compiler) exprSlot(n *ast.Node) (string, error) {
	args, err := c.collectArgs(n, nil)
	if err != nil {
		return "", err
	}
	c.openf("if len(%s) != 1 {", args)
	c.linef(c.ret(c.qposE(fmt.Sprintf("qerrors.New(qerrors.KindRuntime, \"slot() expects exactly one label argument, got %%d\", len(%s))", args), n.Line)))
	c.closeb()
	label := c.tmp("qsl")
	e := c.tmp("qe")
	c.linef("%s, %s := runtime.ToText(%s[0])", label, e, args)
	c.checkErr(e, n.Line)
	res := c.tmp("qt")
	c.linef("var %s runtime.Value", res)
	content := c.tmp("qsc")
	buf := c.tmp("qsb")
	ok := c.tmp("qso")
	c.linef("var %s string", content)
	c.openf("if %s, %s := qslots[%s]; %s {", buf, ok, label, ok)
	c.linef("%s = %s.String()", content, buf)
	c.closeb()
	if c.escapeStrategy() != "" {
		c.linef("%s = runtime.Safe(%s)", res, content)
	} else {
		c.linef("%s = runtime.Str(%s)", res, content)
	}
	return res, nil
}
