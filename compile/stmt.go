package compile

import (
	"bytes"
	"fmt"

	"github.com/avmnu-sng/quill-template-engine/ast"
)

// compileModule lowers the module body after prescanning the root frame and
// running the loop escape analysis the @for lowerings consult.
func (c *compiler) compileModule(mod *ast.Node) error {
	c.an = analyzeLoops(mod)
	binds := bindNames(mod.Children)
	if err := c.checkBindNames(binds, mod); err != nil {
		return err
	}
	c.pushFrame(frameRoot, binds)
	return c.stmtList(mod.Children)
}

// stmtList lowers a run of body items in order.
func (c *compiler) stmtList(items []*ast.Node) error {
	for _, it := range items {
		if err := c.stmtItem(it); err != nil {
			return err
		}
	}
	return nil
}

// stmtItem lowers one body item, mirroring the interpreter's execItem
// dispatch. Constructs outside the compilable subset return the typed
// *NotCompilableError naming the construct.
func (c *compiler) stmtItem(n *ast.Node) error {
	if n == nil {
		return nil
	}
	c.mark(n.Line)
	switch n.Kind {
	case ast.KindText, ast.KindVerbatim:
		c.stmtText(n.Str)
		return nil
	case ast.KindPrint:
		return c.stmtPrint(n)
	case ast.KindIf:
		return c.stmtIf(n)
	case ast.KindFor:
		return c.stmtFor(n)
	case ast.KindSet:
		return c.stmtSet(n)
	case ast.KindCapture:
		return c.stmtCapture(n)
	case ast.KindWith:
		return c.stmtWith(n)
	case ast.KindDo:
		v, err := c.expr(n.Child(0), false)
		if err != nil {
			return err
		}
		c.linef("_ = %s", v)
		return nil
	case ast.KindLog:
		return c.stmtLog(n)
	case ast.KindTabBlock:
		return c.stmtTab(n)
	case ast.KindEscape:
		return c.stmtEscape(n)
	case ast.KindTypes, ast.KindDeprecated, ast.KindLine:
		// Checker-only declarations and line resets emit nothing, exactly like
		// the interpreter.
		return nil
	case ast.KindExtends:
		return c.notCompilable("@extends", n)
	case ast.KindBlock:
		return c.notCompilable("@block", n)
	case ast.KindMacro:
		return c.notCompilable("@macro", n)
	case ast.KindImport:
		return c.notCompilable("@import", n)
	case ast.KindFrom:
		return c.notCompilable("@from", n)
	case ast.KindUse:
		return c.notCompilable("@use", n)
	case ast.KindInclude:
		return c.notCompilable("@include", n)
	case ast.KindEmbed:
		return c.notCompilable("@embed", n)
	case ast.KindProvide:
		return c.notCompilable("@provide", n)
	case ast.KindYield:
		return c.notCompilable("@yield", n)
	case ast.KindCallBlock:
		return c.notCompilable("@call", n)
	case ast.KindCache:
		return c.notCompilable("@cache", n)
	case ast.KindSandbox:
		return c.notCompilable("@sandbox", n)
	case ast.KindApply:
		return c.notCompilable("@apply", n)
	case ast.KindGuard:
		return c.notCompilable("@guard", n)
	case ast.KindFlush:
		return c.notCompilable("@flush", n)
	default:
		return c.notCompilable(fmt.Sprintf("%s statement", n.Kind), n)
	}
}

// stmtText writes a literal text span verbatim; template text is never
// escaped, and write errors surface unpositioned like emitString's.
func (c *compiler) stmtText(s string) {
	e := c.tmp("qe")
	c.openf("if %s := %s.WriteString(%s); %s != nil {", e, c.writer(), q(s), e)
	c.linef(c.ret(e))
	c.closeb()
}

// stmtPrint lowers an interpolation: evaluate, then emit through the active
// escape strategy, with emit errors positioned at the print node.
func (c *compiler) stmtPrint(n *ast.Node) error {
	v, err := c.expr(n.Child(0), false)
	if err != nil {
		return err
	}
	e := c.tmp("qe")
	c.openf("if %s := qemit(%s, %s, %s); %s != nil {", e, c.writer(), q(c.escapeStrategy()), v, e)
	c.linef(c.ret(fmt.Sprintf("qpos(%s, %d)", e, n.Line)))
	c.closeb()
	return nil
}

// stmtIf lowers @if/@elseif/@else as nested condition checks; an if
// introduces no scope frame, so clause bodies bind in the current frame under
// a conditional context.
func (c *compiler) stmtIf(n *ast.Node) error {
	return c.stmtIfClauses(n.Children)
}

func (c *compiler) stmtIfClauses(clauses []*ast.Node) error {
	if len(clauses) == 0 {
		return nil
	}
	cl := clauses[0]
	if !cl.Bool { // terminal else: all children are body
		c.condDepth++
		err := c.stmtList(cl.Children)
		c.condDepth--
		return err
	}
	cond, err := c.expr(cl.Child(0), false)
	if err != nil {
		return err
	}
	c.openf("if runtime.Truthy(%s) {", cond)
	c.condDepth++
	if err := c.stmtList(cl.Children[1:]); err != nil {
		return err
	}
	c.condDepth--
	if len(clauses) > 1 {
		c.ind--
		c.linef("} else {")
		c.ind++
		c.condDepth++
		err := c.stmtIfClauses(clauses[1:])
		c.condDepth--
		if err != nil {
			return err
		}
	}
	c.closeb()
	return nil
}

// stmtLog lowers @log: evaluate the expression and its text coercion for
// effect and error parity; a compiled render has no host logger sink, so the
// text is discarded and no rendered output is produced.
func (c *compiler) stmtLog(n *ast.Node) error {
	v, err := c.expr(n.Child(0), false)
	if err != nil {
		return err
	}
	s := c.tmp("qs")
	e := c.tmp("qe")
	c.linef("%s, %s := runtime.ToText(%s)", s, e, v)
	c.checkErr(e, n.Line)
	c.linef("_ = %s", s)
	return nil
}

// stmtTab lowers a @tab region: the level expression coerces through the
// interpreter's tabLevels rule, the writer's indent grows by level*TabWidth
// spaces for the body, and the prior indent state restores on exit.
func (c *compiler) stmtTab(n *ast.Node) error {
	v, err := c.expr(n.Child(0), false)
	if err != nil {
		return err
	}
	lv := c.tmp("qt")
	e := c.tmp("qe")
	c.linef("%s, %s := qtabLevels(%s)", lv, e, v)
	c.checkErr(e, n.Line)
	w := c.writer()
	si := c.tmp("qs")
	sa := c.tmp("qk")
	c.linef("%s, %s := %s.indent, %s.atLineStart", si, sa, w, w)
	c.openf("if %s > 0 {", lv)
	c.linef("%s.indent += strings.Repeat(\" \", %s*%d)", w, lv, c.tabWidth)
	c.closeb()
	c.linef("%s.atLineStart = true", w)
	if err := c.stmtList(n.Children[1:]); err != nil {
		return err
	}
	c.linef("%s.indent, %s.atLineStart = %s, %s", w, w, si, sa)
	return nil
}

// stmtEscape lowers an @escape region as a compile-time strategy push/pop; an
// unknown strategy word raises the interpreter's runtime error at the region.
func (c *compiler) stmtEscape(n *ast.Node) error {
	strategy, ok := normalizeEscapeStrategy(n.Str)
	if !ok {
		c.openf("if true {")
		c.linef(c.ret(fmt.Sprintf("qpos(qerrors.New(qerrors.KindRuntime, \"unknown escape strategy %%q; expected one of off, html, js, css, \"+\"html_attr, html_attr_relaxed, url\", %s), %d)", q(n.Str), n.Line)))
		c.closeb()
		return nil
	}
	c.strategy = append(c.strategy, strategy)
	err := c.stmtList(n.Children)
	c.strategy = c.strategy[:len(c.strategy)-1]
	return err
}

// normalizeEscapeStrategy maps an @escape strategy word to the stored active
// strategy, mirroring the interpreter's normalizeEscapeStrategy.
func normalizeEscapeStrategy(word string) (string, bool) {
	switch word {
	case "off", "raw":
		return "", true
	case "html", "js", "css", "html_attr", "html_attr_relaxed", "url":
		return word, true
	default:
		return "", false
	}
}

// stmtCapture lowers "@set name = capture { body }": the body renders into a
// fresh writer with indentation suspended, exactly like captureItems, and the
// result binds as Safe under an active strategy, else Str.
func (c *compiler) stmtCapture(n *ast.Node) error {
	body := n.Children
	if len(body) > 0 && body[0] != nil && body[0].Kind == ast.KindType {
		body = body[1:]
	}
	sb := c.tmp("qs")
	cw := c.tmp("qcw")
	c.linef("var %s strings.Builder", sb)
	c.linef("%s := &qWriter{w: &%s, atLineStart: true}", cw, sb)
	c.linef("_ = %s", cw)
	c.writers = append(c.writers, cw)
	err := c.stmtList(body)
	c.writers = c.writers[:len(c.writers)-1]
	if err != nil {
		return err
	}
	if c.escapeStrategy() != "" {
		c.bindName(n.Str, fmt.Sprintf("runtime.Safe(%s.String())", sb), false)
	} else {
		c.bindName(n.Str, fmt.Sprintf("runtime.Str(%s.String())", sb), false)
	}
	return nil
}

// stmtWith lowers "@with map [only] { body }": the body runs in a fresh
// frame whose names resolve from the map value at read time; only cuts the
// chain at this frame like the interpreter's fresh scope.
func (c *compiler) stmtWith(n *ast.Node) error {
	mv, err := c.expr(n.Child(0), false)
	if err != nil {
		return err
	}
	mv = c.spill(mv)
	binds := bindNames(n.Children[1:])
	if err := c.checkBindNames(binds, n); err != nil {
		return err
	}
	c.openf("{")
	kind := frameWith
	if n.Bool {
		kind = frameWithOnly
	}
	f := c.pushFrame(kind, binds)
	f.withVar = mv
	savedCond := c.condDepth
	c.condDepth = 0
	err = c.stmtList(n.Children[1:])
	c.condDepth = savedCond
	c.popFrame()
	c.closeb()
	return err
}

// stmtSet lowers @set in its plain, multi-target, member-assignment, and
// destructuring forms, mirroring execSet's evaluation and binding order.
func (c *compiler) stmtSet(n *ast.Node) error {
	if multiSetPattern(n) {
		// The interpreter misbinds this form today (a recorded engine bug), so
		// the backend cannot reproduce it faithfully; reject instead of
		// emitting silently different bindings.
		return c.notCompilable("destructuring pattern in a multi-target set", n)
	}
	count := int(n.Int)
	targets := n.Children[:count]
	values := n.Children[count:]

	if count == 1 && targets[0] != nil &&
		(targets[0].Kind == ast.KindListPattern || targets[0].Kind == ast.KindMapPattern) {
		v, err := c.expr(values[0], false)
		if err != nil {
			return err
		}
		return c.bindPattern(targets[0], c.spill(v))
	}
	for i, tg := range targets {
		v, err := c.expr(values[i], false)
		if err != nil {
			return err
		}
		if tg.Kind == ast.KindAttr || tg.Kind == ast.KindIndex {
			if err := c.assignMember(tg, c.spill(v)); err != nil {
				return err
			}
			continue
		}
		c.bindName(tg.Str, v, false)
	}
	return nil
}

// assignMember lowers "@set recv.name = v" / "@set recv[key] = v" by
// unrolling the interpreter's ownPath: privatize every shared array from the
// root name down, rebinding each fresh copy under its name or parent slot,
// then write into the owned receiver.
func (c *compiler) assignMember(tg *ast.Node, val string) error {
	recv, err := c.ownPath(tg.Child(0))
	if err != nil {
		return err
	}
	if tg.Kind == ast.KindAttr {
		e := c.tmp("qe")
		c.openf("if %s := runtime.SetMember(%s, %s, %s); %s != nil {", e, recv, q(tg.Str), val, e)
		c.linef(c.ret(fmt.Sprintf("qpos(%s, %d)", e, tg.Line)))
		c.closeb()
		return nil
	}
	key, err := c.expr(tg.Child(1), false)
	if err != nil {
		return err
	}
	e := c.tmp("qe")
	c.openf("if %s := runtime.SetIndex(%s, qkeyOf(%s), %s); %s != nil {", e, recv, key, val, e)
	c.linef(c.ret(fmt.Sprintf("qpos(%s, %d)", e, tg.Line)))
	c.closeb()
	return nil
}

// ownPath lowers the interpreter's ownPath for one assignment path node,
// returning the variable holding the (privatized where needed) receiver.
func (c *compiler) ownPath(n *ast.Node) (string, error) {
	switch n.Kind {
	case ast.KindName:
		cur := c.spill(c.readName(n.Str, n.Line, false))
		o := c.tmp("qt")
		cp := c.tmp("qk")
		c.linef("%s, %s := runtime.Own(%s)", o, cp, cur)
		c.openf("if %s {", cp)
		c.condDepth++
		c.bindName(n.Str, o, true)
		c.linef("%s = %s", cur, o)
		c.condDepth--
		c.closeb()
		return cur, nil
	case ast.KindAttr:
		parent, err := c.ownPath(n.Child(0))
		if err != nil {
			return "", err
		}
		cur := c.tmp("qt")
		e := c.tmp("qe")
		c.linef("%s, %s := runtime.GetAttribute(%s, runtime.Str(%s), runtime.AccessDot, false)", cur, e, parent, q(n.Str))
		c.checkErr(e, n.Line)
		o := c.tmp("qt")
		cp := c.tmp("qk")
		c.linef("%s, %s := runtime.Own(%s)", o, cp, cur)
		c.openf("if %s {", cp)
		e2 := c.tmp("qe")
		c.openf("if %s := runtime.SetMember(%s, %s, %s); %s != nil {", e2, parent, q(n.Str), o, e2)
		c.linef(c.ret(fmt.Sprintf("qpos(%s, %d)", e2, n.Line)))
		c.closeb()
		c.linef("%s = %s", cur, o)
		c.closeb()
		return cur, nil
	case ast.KindIndex:
		parent, err := c.ownPath(n.Child(0))
		if err != nil {
			return "", err
		}
		keyRaw, err := c.expr(n.Child(1), false)
		if err != nil {
			return "", err
		}
		key := c.tmp("qt")
		c.linef("%s := qkeyOf(%s)", key, keyRaw)
		cur := c.tmp("qt")
		e := c.tmp("qe")
		c.linef("%s, %s := runtime.GetAttribute(%s, %s, runtime.AccessIndex, false)", cur, e, parent, key)
		c.checkErr(e, n.Line)
		o := c.tmp("qt")
		cp := c.tmp("qk")
		c.linef("%s, %s := runtime.Own(%s)", o, cp, cur)
		c.openf("if %s {", cp)
		e2 := c.tmp("qe")
		c.openf("if %s := runtime.SetIndex(%s, %s, %s); %s != nil {", e2, parent, key, o, e2)
		c.linef(c.ret(fmt.Sprintf("qpos(%s, %d)", e2, n.Line)))
		c.closeb()
		c.linef("%s = %s", cur, o)
		c.closeb()
		return cur, nil
	default:
		v, err := c.expr(n, false)
		if err != nil {
			return "", err
		}
		return c.spill(v), nil
	}
}

// bindPattern lowers a destructuring bind of the value in val against a
// list or map pattern, mirroring bindListPattern/bindMapPattern.
func (c *compiler) bindPattern(pat *ast.Node, val string) error {
	switch pat.Kind {
	case ast.KindListPattern:
		return c.bindListPattern(pat, val)
	case ast.KindMapPattern:
		return c.bindMapPattern(pat, val)
	default:
		c.openf("if true {")
		c.linef(c.ret(fmt.Sprintf(`qpos(qerrors.New(qerrors.KindRuntime, "unknown destructuring pattern"), %d)`, pat.Line)))
		c.closeb()
		return nil
	}
}

// bindListPattern lowers sequence destructuring with the interpreter's exact
// arity rules: required slots bound positionally, elided slots skipped,
// optional slots null-padded, and a trailing spread capturing the rest.
func (c *compiler) bindListPattern(pat *ast.Node, val string) error {
	c.openf("if %s.Kind != runtime.KArray || %s.Arr == nil {", val, val)
	c.linef(c.ret(fmt.Sprintf(`qpos(qerrors.New(qerrors.KindRuntime, "destructuring expects a sequence"), %d)`, pat.Line)))
	c.closeb()
	ps := c.tmp("qp")
	c.linef("%s := %s.Arr.Pairs()", ps, val)

	fixed := pat.Children
	var tail *ast.Node
	if k := len(fixed); k > 0 && fixed[k-1] != nil && fixed[k-1].Kind == ast.KindSpread {
		tail = fixed[k-1]
		fixed = fixed[:k-1]
	}
	required := 0
	for _, slot := range fixed {
		if slot == nil || slot.Kind != ast.KindOptional {
			required++
		}
	}

	c.openf("if len(%s) < %d {", ps, required)
	c.linef(c.ret(fmt.Sprintf("qpos(qerrors.New(qerrors.KindRuntime, \"sequence destructuring expects at least %%d element(s) but got %%d\", %d, len(%s)), %d)", required, ps, pat.Line)))
	c.closeb()
	if tail == nil {
		c.openf("if len(%s) > %d {", ps, len(fixed))
		c.linef(c.ret(fmt.Sprintf("qpos(qerrors.New(qerrors.KindRuntime, \"sequence destructuring expects %%d element(s) but got %%d\", %d, len(%s)), %d)", len(fixed), ps, pat.Line)))
		c.closeb()
	}

	for i, slot := range fixed {
		if slot == nil {
			continue
		}
		target := slot
		optional := slot.Kind == ast.KindOptional
		if optional {
			target = slot.Child(0)
		}
		if optional {
			c.openf("if %d >= len(%s) {", i, ps)
			c.condDepth++
			c.bindTargetNull(target)
			c.condDepth--
			c.ind--
			c.linef("} else {")
			c.ind++
			c.condDepth++
			if err := c.bindSlot(target, fmt.Sprintf("%s[%d].Val", ps, i), pat); err != nil {
				return err
			}
			c.condDepth--
			c.closeb()
			continue
		}
		if err := c.bindSlot(target, fmt.Sprintf("%s[%d].Val", ps, i), pat); err != nil {
			return err
		}
	}

	if tail != nil {
		start := c.tmp("qi")
		c.linef("%s := %d", start, len(fixed))
		c.openf("if %s > len(%s) {", start, ps)
		c.linef("%s = len(%s)", start, ps)
		c.closeb()
		rest := c.tmp("qa")
		c.linef("%s := runtime.NewArray()", rest)
		p := c.tmp("qp")
		c.openf("for _, %s := range %s[%s:] {", p, ps, start)
		c.linef("%s.SetInt(int64(%s.Len()), %s.Val)", rest, rest, p)
		c.closeb()
		c.bindName(tail.Child(0).Str, fmt.Sprintf("runtime.Arr(%s)", rest), false)
	}
	return nil
}

// bindTargetNull lowers the absent-optional path: a name binds null and a
// nested pattern null-binds every name it introduces, like bindTargetNull.
func (c *compiler) bindTargetNull(target *ast.Node) {
	switch target.Kind {
	case ast.KindName, ast.KindTarget:
		c.bindName(target.Str, "runtime.Null()", false)
	case ast.KindListPattern:
		for _, slot := range target.Children {
			if slot == nil {
				continue
			}
			if slot.Kind == ast.KindSpread {
				c.bindName(slot.Child(0).Str, "runtime.Arr(runtime.NewArray())", false)
				continue
			}
			inner := slot
			if slot.Kind == ast.KindOptional {
				inner = slot.Child(0)
			}
			c.bindTargetNull(inner)
		}
	case ast.KindMapPattern:
		for _, slot := range target.Children {
			if slot == nil || slot.Kind != ast.KindMapTarget {
				continue
			}
			local := slot.Str
			if slot.Bool {
				local = slot.Child(0).Str
			}
			c.bindName(local, "runtime.Null()", false)
		}
	}
}

// bindSlot lowers one bound sequence slot: a plain name binds directly, a
// nested pattern recurses.
func (c *compiler) bindSlot(target *ast.Node, valExpr string, pat *ast.Node) error {
	switch target.Kind {
	case ast.KindName, ast.KindTarget:
		c.bindName(target.Str, valExpr, false)
		return nil
	case ast.KindListPattern, ast.KindMapPattern:
		return c.bindPattern(target, c.spill(valExpr))
	}
	_ = pat
	return nil
}

// bindMapPattern lowers map destructuring: each slot reads the value's member
// by its source key through dotted access with the engine's strictness, then
// binds the alias (or the key) like bindMapPattern.
func (c *compiler) bindMapPattern(pat *ast.Node, val string) error {
	for _, slot := range pat.Children {
		if slot == nil || slot.Kind != ast.KindMapTarget {
			continue
		}
		v := c.tmp("qt")
		e := c.tmp("qe")
		c.linef("%s, %s := runtime.GetAttribute(%s, runtime.Str(%s), runtime.AccessDot, %v)", v, e, val, q(slot.Str), c.lenient)
		c.checkErr(e, pat.Line)
		local := slot.Str
		if slot.Bool {
			local = slot.Child(0).Str
		}
		c.bindName(local, v, false)
	}
	return nil
}

// stmtFor lowers @for with the interpreter's full contract: traversability,
// the fused filter pre-pass, per-iteration loop metadata, the empty else arm
// in the parent frame, the persistent body frame, and copy-back of
// pre-existing names at loop exit.
func (c *compiler) stmtFor(n *ast.Node) error {
	if n.Int&ast.ForRecursive != 0 {
		return c.notCompilable("recursive @for", n)
	}
	count := int(n.Int & ast.ForTargetCount)
	target1 := n.Child(0)
	var target2 *ast.Node
	idx := 1
	if count == 2 {
		target2 = n.Child(1)
		idx = 2
	}
	iterand := n.Child(idx)
	var filter *ast.Node
	bodyIdx := idx + 1
	if fc := n.Child(bodyIdx); fc != nil && fc.Kind == ast.KindClause {
		filter = fc
		bodyIdx++
	}
	body := n.Child(bodyIdx)
	var elseBody *ast.Node
	if n.Bool {
		elseBody = n.Child(bodyIdx + 1)
	}

	iv, err := c.expr(iterand, false)
	if err != nil {
		return err
	}
	it := iterVars{live: c.an.liveFor(n)}
	if it.live {
		// The zero-copy path: a KArray iterand iterates live off the array's
		// insertion-ordered keys with the length snapshotted once at entry (so
		// appends beyond it would stay invisible, matching the snapshot;
		// liveFor's mutation rule already forbids them). Anything else falls
		// back to the materialized pair slice at runtime, keeping the
		// EnsureTraversable error and lenient behavior byte-identical.
		iv = c.spill(iv)
		it.arr = c.tmp("qr")
		it.pairs = c.tmp("qp")
		it.n = c.tmp("qn")
		c.linef("var %s *runtime.Array", it.arr)
		c.linef("var %s []runtime.Pair", it.pairs)
		c.linef("%s := 0", it.n)
		c.openf("if %s.Kind == runtime.KArray && %s.Arr != nil {", iv, iv)
		c.linef("%s = %s.Arr", it.arr, iv)
		c.linef("%s = %s.Len()", it.n, it.arr)
		c.ind--
		c.linef("} else {")
		c.ind++
		e := c.tmp("qe")
		c.linef("var %s error", e)
		c.linef("%s, %s = runtime.EnsureTraversable(%s, %v)", it.pairs, e, iv, c.lenient)
		c.checkErr(e, n.Line)
		c.linef("%s = len(%s)", it.n, it.pairs)
		c.closeb()
	} else {
		it.pairs = c.tmp("qp")
		e := c.tmp("qe")
		c.linef("%s, %s := runtime.EnsureTraversable(%s, %v)", it.pairs, e, iv, c.lenient)
		c.checkErr(e, n.Line)
	}

	// Lower the filter, the loop, and the copy-back into a side buffer so the
	// loop.changed memory locals its body registers can be declared first,
	// mirroring the interpreter's changed frame pushed before the filter runs.
	li := &loopInfo{forNode: n}
	c.loops = append(c.loops, li)
	saved := c.body
	c.body = bytes.Buffer{}
	c.ind++ // the side buffer's code sits inside the loop-scope block

	lowerErr := c.stmtForInner(n, target1, target2, filter, body, elseBody, it)

	c.ind--
	seg := c.body
	c.body = saved
	c.loops = c.loops[:len(c.loops)-1]
	if lowerErr != nil {
		return lowerErr
	}

	c.openf("{")
	for _, site := range li.changed {
		c.linef("var %s runtime.Value", site.prev)
		c.linef("var %s bool", site.seen)
		c.linef("_, _ = %s, %s", site.prev, site.seen)
	}
	c.body.Write(seg.Bytes())
	c.closeb()
	return nil
}

// iterVars names the generated locals one @for lowering iterates over. Every
// loop has a pair-slice local; a live loop additionally carries the array
// local (nil at runtime when the iterand was not a KArray) and the entry-time
// length snapshot local, which is the single length source on both of its
// runtime paths.
type iterVars struct {
	live  bool
	arr   string
	pairs string
	n     string
}

// count renders the Go expression for the iteration count.
func (it iterVars) count() string {
	if it.live {
		return it.n
	}
	return "len(" + it.pairs + ")"
}

// stmtForInner lowers the filter pre-pass, the empty/body split, the
// iteration, and the copy-back, inside the loop-scope block.
func (c *compiler) stmtForInner(n *ast.Node, target1, target2, filter, body, elseBody *ast.Node, it iterVars) error {
	if filter != nil {
		// A fused loop is never live (liveFor), so the filter always works the
		// materialized pair slice.
		if err := c.stmtForFilter(filter, target1, target2, it.pairs); err != nil {
			return err
		}
	}
	c.openf("if %s == 0 {", it.count())
	c.condDepth++
	if elseBody != nil {
		if err := c.stmtList(elseBody.Children); err != nil {
			return err
		}
	}
	c.condDepth--
	c.ind--
	c.linef("} else {")
	c.ind++
	if err := c.stmtForBody(n, target1, target2, body, it); err != nil {
		return err
	}
	c.closeb()
	return nil
}

// stmtForFilter lowers the fused filter clause: the condition evaluates in a
// child frame with the targets bound per candidate, and the survivors replace
// the pair slice, exactly like filterLoopPairs.
func (c *compiler) stmtForFilter(filter *ast.Node, target1, target2 *ast.Node, pairs string) error {
	binds := []string{target1.Str}
	if target2 != nil {
		binds = append(binds, target2.Str)
	}
	// The condition is an expression; only its inline assignments bind in the
	// filter frame.
	exprBinds(filter.Child(0), func(name string) { binds = append(binds, name) })
	if err := c.checkBindNames(binds, filter); err != nil {
		return err
	}
	surv := c.tmp("qp")
	c.linef("%s := make([]runtime.Pair, 0, len(%s))", surv, pairs)
	c.openf("{")
	c.pushFrame(frameFilter, dedupe(binds))
	savedCond := c.condDepth
	c.condDepth = 0
	j := c.tmp("qi")
	c.openf("for %s := 0; %s < len(%s); %s++ {", j, j, pairs, j)
	if target2 != nil {
		c.bindName(target1.Str, fmt.Sprintf("%s[%s].Key", pairs, j), false)
		c.bindName(target2.Str, fmt.Sprintf("%s[%s].Val", pairs, j), false)
	} else {
		c.bindName(target1.Str, fmt.Sprintf("%s[%s].Val", pairs, j), false)
	}
	cond, err := c.expr(filter.Child(0), false)
	if err != nil {
		return err
	}
	c.openf("if runtime.Truthy(%s) {", cond)
	c.linef("%s = append(%s, %s[%s])", surv, surv, pairs, j)
	c.closeb()
	c.closeb()
	c.condDepth = savedCond
	c.popFrame()
	c.closeb()
	c.linef("%s = %s", pairs, surv)
	return nil
}

// stmtForBody lowers the non-empty arm: the persistent loop frame, the
// per-iteration target and loop bindings, the body, and the copy-back.
func (c *compiler) stmtForBody(n *ast.Node, target1, target2, body *ast.Node, it iterVars) error {
	binds := []string{target1.Str}
	if target2 != nil {
		binds = append(binds, target2.Str)
	}
	binds = append(binds, "loop")
	bodyBinds := bindNames(body.Children)
	binds = append(binds, bodyBinds...)
	if err := c.checkBindNames(binds, n); err != nil {
		return err
	}

	// The loop optimizer's decision: a loop proven non-escaping binds no
	// per-iteration loop value at all; anything else materializes it exactly
	// as the interpreter does. A live loop is non-escaping by liveFor's rule.
	inline := c.an.inlineFor(n)

	// The parent loop value resolves in the enclosing chain before iteration,
	// like pre.Get("loop") -- on both paths, because the probe's TIMING is
	// observable: a with-map or root entry named loop that changes mid-loop
	// must not skew an on-demand materialization away from the parent the
	// interpreter bound at entry. An inline loop cannot go through probeName
	// (an enclosing inline loop's value local is never assigned), so it takes
	// the loop-aware probe, which materializes an enclosing inline loop's
	// value from that loop's own entry-time locals.
	var parentLoop string
	if inline {
		parentLoop = c.emitLoopParent(len(c.frames))
	} else {
		parentVal, _ := c.probeName("loop")
		parentLoop = c.spill(parentVal)
	}

	c.openf("{")
	f := c.pushFrame(frameLoop, dedupe(binds))
	savedCond := c.condDepth
	c.condDepth = 0

	li := c.currentLoop()
	li.frame = f
	li.pairsVar = it.pairs
	li.inline = inline
	li.live = it.live
	li.arrVar = it.arr
	li.nVar = it.n
	li.parentVar = parentLoop

	pairs := it.pairs
	i := c.tmp("qi")
	li.iVar = i
	c.openf("for %s := 0; %s < %s; %s++ {", i, i, it.count(), i)
	if it.live {
		c.emitLiveTargets(it, target1, target2, i)
	} else if target2 != nil {
		c.bindName(target1.Str, fmt.Sprintf("%s[%s].Key", pairs, i), false)
		c.bindName(target2.Str, fmt.Sprintf("%s[%s].Val", pairs, i), false)
	} else {
		c.bindName(target1.Str, fmt.Sprintf("%s[%s].Val", pairs, i), false)
	}
	if inline {
		// The interpreter still binds the NAME loop every iteration, so the
		// frame's first-bind order (hints, _context ordering) must list it;
		// only the value allocation is elided.
		b := f.byName["loop"]
		if f.ord != "" {
			c.openf("if !%s {", b.flag)
			c.linef("%s = append(%s, %s)", f.ord, f.ord, q("loop"))
			c.closeb()
		}
		c.linef("%s = true", b.flag)
		b.everBound = true
	} else {
		c.bindName("loop", fmt.Sprintf("runtime.NewLoopValue(%s, %s, %s)", i, pairs, parentLoop), false)
	}
	if err := c.stmtList(body.Children); err != nil {
		return err
	}
	c.closeb()

	c.condDepth = savedCond
	c.emitCopyBack(f, target1, target2, bodyBinds)
	c.popFrame()
	c.closeb()
	return nil
}

// emitLiveTargets lowers a live loop's per-iteration target loads: the entry
// at position i comes from Array.PairAt on the array path and from the
// fallback pair slice otherwise, then binds exactly what the pair-slice
// lowering binds -- the value for a single target, key and value for two.
func (c *compiler) emitLiveTargets(it iterVars, target1, target2 *ast.Node, i string) {
	if target2 != nil {
		kv := c.tmp("qt")
		vv := c.tmp("qt")
		c.linef("var %s, %s runtime.Value", kv, vv)
		c.openf("if %s != nil {", it.arr)
		c.linef("%s, %s = %s.PairAt(%s)", kv, vv, it.arr, i)
		c.ind--
		c.linef("} else {")
		c.ind++
		c.linef("%s, %s = %s[%s].Key, %s[%s].Val", kv, vv, it.pairs, i, it.pairs, i)
		c.closeb()
		c.bindName(target1.Str, kv, false)
		c.bindName(target2.Str, vv, false)
		return
	}
	vv := c.tmp("qt")
	c.linef("var %s runtime.Value", vv)
	c.openf("if %s != nil {", it.arr)
	c.linef("_, %s = %s.PairAt(%s)", vv, it.arr, i)
	c.ind--
	c.linef("} else {")
	c.ind++
	c.linef("%s = %s[%s].Val", vv, it.pairs, i)
	c.closeb()
	c.bindName(target1.Str, vv, false)
}

// emitCopyBack reproduces execFor's exit pass over every pre-existing name:
// a name the body rebound propagates its final value into the parent frame,
// and every other visible binding is re-marked shared, exactly as pre.Set
// over loopCtx.Get would leave things. The loop's own control bindings
// (targets and loop) never propagate.
func (c *compiler) emitCopyBack(loopFrame *frame, target1, target2 *ast.Node, bodyBinds []string) {
	bound := map[string]bool{target1.Str: true, "loop": true}
	if target2 != nil {
		bound[target2.Str] = true
	}

	// Re-mark untouched visible compile bindings (outermost first, like
	// pre.Names order). Names the body rebound are handled below.
	rebound := map[string]bool{}
	for _, name := range bodyBinds {
		if !bound[name] {
			rebound[name] = true
		}
	}
	for i := 0; i < len(c.frames)-1; i++ {
		for _, b := range c.frames[i].order {
			if bound[b.name] || rebound[b.name] || b.name == "loop" {
				continue
			}
			if b.definite {
				c.linef("%s = runtime.ShareValue(%s)", b.val, b.val)
				continue
			}
			c.openf("if %s {", b.flag)
			c.linef("%s = runtime.ShareValue(%s)", b.val, b.val)
			c.closeb()
		}
	}

	// Propagate the body's rebinds of pre-existing names into the parent
	// frame. Persistence follows the interpreter exactly: a name is copied
	// back iff it was visible before the loop, which for a name only the vars
	// map may hold is a runtime membership check.
	parent := c.frames[len(c.frames)-2]
	for _, name := range bodyBinds {
		if bound[name] {
			continue
		}
		bb := loopFrame.byName[name]
		c.emitCopyBackName(name, bb, parent)
	}
}

// emitCopyBackName lowers the copy-back of one body-rebound name into the
// parent frame.
func (c *compiler) emitCopyBackName(name string, bb *binding, parent *frame) {
	// Determine whether the name pre-existed outside the loop. The compile
	// frames below the loop frame answer statically where they can; the vars
	// map answers the rest at runtime.
	var steps []resolveStep
	terminal := false
	top := len(c.frames) - 2 // resolution as seen by the parent frame
	for i := top; i >= 0 && !terminal; i-- {
		f := c.frames[i]
		if b, ok := f.byName[name]; ok {
			steps = append(steps, resolveStep{b: b, cross: i != top})
			if b.definite {
				terminal = true
				break
			}
		}
		switch f.kind {
		case frameWith:
			steps = append(steps, resolveStep{withVar: f.withVar})
		case frameWithOnly:
			steps = append(steps, resolveStep{withVar: f.withVar})
			i = 0
		case frameRoot:
			steps = append(steps, resolveStep{vars: true})
		}
	}

	// The copied value is the body's final binding when the body bound it,
	// else the (re-marked) pre-existing value.
	emitAssign := func() {
		pb := parent.byName[name]
		val := c.tmp("qt")
		c.linef("var %s runtime.Value", val)
		c.openf("if %s {", bb.flag)
		c.linef("%s = %s", val, bb.val)
		c.ind--
		c.linef("} else {")
		c.ind++
		found := c.tmp("qf")
		c.linef("%s := false", found)
		c.emitSteps(name, steps, val, found)
		c.linef("_ = %s", found)
		c.closeb()
		if parent.kind != frameRoot && !pb.definite && parent.ord != "" {
			// The copy-back is a Scope.Set on the parent frame, so a first
			// actual bind there enters the frame's runtime name order (a name
			// already ordered deeper in the chain deduplicates on read). The
			// root needs nothing: a pre-existing name is already in qNames.
			c.openf("if !%s {", pb.flag)
			c.linef("%s = append(%s, %s)", parent.ord, parent.ord, q(name))
			c.closeb()
		}
		c.linef("%s = runtime.ShareValue(%s)", pb.val, val)
		if !pb.definite {
			c.linef("%s = true", pb.flag)
		}
		pb.everBound = true
	}

	if terminal {
		// Statically pre-existing: always copy back.
		emitAssign()
		return
	}
	// Runtime pre-existence: any maybe-binding below, a with-map entry, or a
	// vars entry makes the name pre-existing.
	ex := c.tmp("qf")
	c.linef("%s := false", ex)
	for _, s := range steps {
		switch {
		case s.b != nil:
			c.openf("if %s {", s.b.flag)
			c.linef("%s = true", ex)
			c.closeb()
		case s.withVar != "":
			c.openf("if qwithHas(%s, %s) {", s.withVar, q(name))
			c.linef("%s = true", ex)
			c.closeb()
		case s.vars:
			ok := c.tmp("qk")
			c.openf("if _, %s := vars[%s]; %s {", ok, q(name), ok)
			c.linef("%s = true", ex)
			c.closeb()
		}
	}
	c.openf("if %s {", ex)
	c.condDepth++
	emitAssign()
	c.condDepth--
	c.closeb()
}

// dedupe returns names with duplicates removed, order preserved.
func dedupe(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}
