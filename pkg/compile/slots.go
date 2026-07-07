package compile

import (
	"fmt"

	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
)

// reachesSlots reports whether rendering the subtree at n emits a deferred-slot
// placeholder into this render's output, either directly (a slot construct in n
// itself) or through a statically inlined @include whose partial defers a slot.
// A slot-using partial that stmtInclude inlines appends to the SAME render-level
// slot state, so the entry's render must buffer and run the resolve pass even
// when the entry template itself has no slot construct; missing that would let a
// partial's yield placeholder stream out unresolved. The walk descends only the
// includes stmtInclude actually inlines (a static string source that resolves to
// a composition-free partial and does not close an include cycle), so an include
// that instead defers to the interpreter -- where its slots resolve on the interp
// path -- never forces the buffered shape here. Over-marking is byte-invisible
// because buffering a slot-free render only defers a straight copy to w.
func reachesSlots(n *ast.Node, includes map[string]*ast.Node) bool {
	return reachesSlotsWalk(n, includes, nil)
}

// reachesSlotsWalk is reachesSlots with the active include-inlining stack that
// bounds the descent at a self-referential include exactly as stmtInclude's
// include stack bounds inlining.
func reachesSlotsWalk(n *ast.Node, includes map[string]*ast.Node, stack []string) bool {
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
	case ast.KindInclude:
		if mod, ok := inlinablePartial(n, includes, stack); ok {
			if reachesSlotsWalk(mod, includes, append(stack, n.Child(0).Str)) {
				return true
			}
		}
	case ast.KindEmbed:
		// A flattened embed splices its target's body into this render, so the
		// target's own @yield/@provide/slot -- and those of its @extends parents
		// and @use traits -- reach the render-level slot state exactly as the
		// inline @block overrides below do. Descending the whole reachable target
		// set forces the buffered shape whenever any of them defers a slot.
		if mod, ok := flattenableEmbed(n, includes, stack); ok {
			for _, m := range embedReachableModules(mod, includes) {
				if reachesSlotsWalk(m, includes, append(stack, n.Child(0).Str)) {
					return true
				}
			}
		}
	}
	for _, ch := range n.Children {
		if reachesSlotsWalk(ch, includes, stack) {
			return true
		}
	}
	return false
}

// inlinablePartial resolves a static @include node to the partial module
// stmtInclude would inline at that site, reporting whether the site is one it
// inlines rather than defers. It mirrors stmtInclude's structural gates -- a
// string-literal source naming a composition-free partial in the include set
// that does not close an include cycle -- so a name-reachability walk over the
// includes visits exactly the partials whose statements enter this render.
func inlinablePartial(n *ast.Node, includes map[string]*ast.Node, stack []string) (*ast.Node, bool) {
	src := n.Child(0)
	if src == nil || src.Kind != ast.KindString {
		return nil, false
	}
	mod, ok := includes[src.Str]
	if !ok || mod == nil || mod.Kind != ast.KindModule {
		return nil, false
	}
	if partialHasComposition(mod) {
		return nil, false
	}
	for _, name := range stack {
		if name == src.Str {
			return nil, false
		}
	}
	return mod, true
}

// flattenableEmbed resolves a static @embed node to the target module
// stmtEmbed flattens at that site, reporting whether the site is one it
// flattens rather than defers. It mirrors stmtEmbed's structural gate -- a
// string-literal source naming a target present in the compile set that does
// not close a self-embed cycle -- so the slot-reachability walk visits exactly
// the targets whose statements enter this render.
func flattenableEmbed(n *ast.Node, includes map[string]*ast.Node, stack []string) (*ast.Node, bool) {
	src := n.Child(0)
	if src == nil || src.Kind != ast.KindString {
		return nil, false
	}
	mod, ok := includes[src.Str]
	if !ok || mod == nil || mod.Kind != ast.KindModule {
		return nil, false
	}
	for _, name := range stack {
		if name == src.Str {
			return nil, false
		}
	}
	return mod, true
}

// embedReachableModules returns the target module plus every module its static
// composition splices into the flattened body: its @extends parents and @use
// traits, followed transitively, resolved through the compile set. A slot in
// any of them surfaces in the embed's output, so the slot-reachability walk
// must cover the whole set. A dynamic or absent head simply contributes no
// extra module, which only ever under-descends into a target the lowering would
// itself defer, so no reachable slot is missed for a flattenable embed.
func embedReachableModules(target *ast.Node, includes map[string]*ast.Node) []*ast.Node {
	var out []*ast.Node
	seen := map[*ast.Node]bool{}
	var visit func(mod *ast.Node)
	visit = func(mod *ast.Node) {
		if mod == nil || seen[mod] {
			return
		}
		seen[mod] = true
		out = append(out, mod)
		for _, name := range compositionHeadTargets(mod) {
			if m, ok := includes[name]; ok && m != nil && m.Kind == ast.KindModule {
				visit(m)
			}
		}
	}
	visit(target)
	return out
}

// compositionHeadTargets returns the string-literal template names an @extends
// or @use head at the top level of a module names, so the reachable-module walk
// follows the same static composition the interpreter's buildChain and
// mergeTraits would. A candidate-list @extends contributes its first literal
// candidate, matching staticExtendsTarget's choice.
func compositionHeadTargets(mod *ast.Node) []string {
	var out []string
	for _, c := range mod.Children {
		switch c.Kind {
		case ast.KindExtends:
			op := c.Child(0)
			if op == nil {
				continue
			}
			switch op.Kind {
			case ast.KindString:
				out = append(out, op.Str)
			case ast.KindList:
				if len(op.Children) > 0 && op.Children[0] != nil && op.Children[0].Kind == ast.KindString {
					out = append(out, op.Children[0].Str)
				}
			}
		case ast.KindUse:
			if src := c.Child(0); src != nil && src.Kind == ast.KindString {
				out = append(out, src.Str)
			}
		}
	}
	return out
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
