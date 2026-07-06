package compile

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/avmnu-sng/quill-template-engine/ast"
)

// compileModule lowers the module body after prescanning the root frame and
// running the loop escape analysis the @for lowerings consult. A module with
// no @tab region anywhere never activates the qWriter indent layer, so its
// writes lower straight to the render function's io.Writer.
func (c *compiler) compileModule(mod *ast.Node) error {
	c.an = analyzeLoops(mod, c.includeTemplates)
	c.tabFree = !hasTabBlock(mod)
	// A slot reached through an inlined @include feeds the same render-level slot
	// state as a slot written in this module, so the entry buffers and resolves
	// whenever any inlinable partial defers a slot, not only when the entry does.
	c.usesSlots = reachesSlots(mod, c.includeTemplates)
	c.setTopWriter()
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
		// A Unit resolved the chain at link time; the head emits nothing,
		// exactly like execItem's declaration arm.
		if c.unit != nil {
			return nil
		}
		return c.notCompilable("@extends", n)
	case ast.KindBlock:
		if c.unit != nil {
			return c.unitBlockSite(n)
		}
		return c.notCompilable("@block", n)
	case ast.KindMacro:
		// A non-entry member's macro declaration is inert for this render
		// (only the entry's macros enter the namespace, and an entry with
		// macros was rejected at link time), so a Unit lowers it as the
		// interpreter's declaration no-op.
		if c.unit != nil {
			return nil
		}
		return c.notCompilable("@macro", n)
	case ast.KindImport:
		if c.unit != nil {
			return nil
		}
		return c.notCompilable("@import", n)
	case ast.KindFrom:
		if c.unit != nil {
			return nil
		}
		return c.notCompilable("@from", n)
	case ast.KindUse:
		// A Unit merged trait blocks into the table at link time.
		if c.unit != nil {
			return nil
		}
		return c.notCompilable("@use", n)
	case ast.KindInclude:
		return c.stmtInclude(n)
	case ast.KindEmbed:
		return c.stmtEmbed(n)
	case ast.KindProvide:
		return c.stmtProvide(n)
	case ast.KindYield:
		return c.stmtYield(n)
	case ast.KindCallBlock:
		return c.notCompilable("@call", n)
	case ast.KindCache:
		return c.stmtCache(n)
	case ast.KindSandbox:
		return c.notCompilable("@sandbox", n)
	case ast.KindApply:
		return c.stmtApply(n)
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
	c.emitWrite(q(s), func(e string) string { return e })
}

// stmtPrint lowers an interpolation through the shared value-emission shape.
func (c *compiler) stmtPrint(n *ast.Node) error {
	return c.stmtEmitValue(n.Child(0), n)
}

// stmtEmitValue lowers one emitted value expression: evaluate val, then emit
// through the active escape strategy, with emit errors positioned at pos (the
// print node, or the block node of a shortcut @block). With no active
// strategy the site specializes by kind instead of calling the emit helper
// unconditionally.
func (c *compiler) stmtEmitValue(val, pos *ast.Node) error {
	if c.escapeStrategy() == "" {
		return c.stmtPrintPlain(val, pos)
	}
	v, err := c.expr(val, false)
	if err != nil {
		return err
	}
	c.emitValueLocal(v, pos.Line)
	return nil
}

// emitValueLocal emits an already-lowered value local through the active escape
// strategy, positioning the emit error at line. It is the value-consuming tail
// stmtEmitValue and stmtApply share: both hold a runtime.Value and both finish
// with the interpreter's in.emit under a live strategy. The caller guarantees
// the strategy is active.
func (c *compiler) emitValueLocal(v string, line int) {
	e := c.tmp("qe")
	c.openf("if %s := %s(%s, %s, %s); %s != nil {", e, c.emitFn(), c.writer(), q(c.escapeStrategy()), v, e)
	c.linef(c.ret(c.qposE(e, line)))
	c.closeb()
}

// emitPlainValueLocal emits an already-lowered value local with no active
// strategy, positioning the emit error at line. It is the plain tail
// stmtPrintPlain and stmtApply share: a Str/Safe value writes its bytes
// verbatim, every other kind spells through the emit helper. The caller
// guarantees the strategy is off.
func (c *compiler) emitPlainValueLocal(v string, line int) {
	wrap := func(e string) string { return c.qposE(e, line) }
	v = c.spillAdjacent(v)
	c.openf("if %s.Kind == runtime.KStr || %s.Kind == runtime.KSafe {", v, v)
	c.emitWrite(v+".S", wrap)
	c.ind--
	e := c.tmp("qe")
	c.linef("} else if %s := %s(%s, %s, %s); %s != nil {", e, c.emitFn(), c.writer(), q(""), v, e)
	c.ind++
	c.linef(c.ret(wrap(e)))
	c.closeb()
}

// stmtPrintPlain lowers a print with no active escape strategy, where the
// emit helper reduces to ToText plus a write. A static-Int expression writes
// its strconv.FormatInt rendering directly -- exactly ToText's Int spelling.
// Every other expression takes an inline Str/Safe kind guard writing v.S
// verbatim (ToText returns those kinds' bytes unchanged and no escaping
// applies), falling back to the emit helper for the remaining kinds'
// spellings and their authoritative errors. The guarded value skips the
// general spill through spillAdjacent: its uses sit only in the adjacent
// guard statements emitted here, with no expression lowering between them.
func (c *compiler) stmtPrintPlain(val, pos *ast.Node) error {
	wrap := func(e string) string { return c.qposE(e, pos.Line) }
	if inner, ok := c.staticIntPrint(val); ok {
		c.usesStrconv = true
		c.emitWrite(fmt.Sprintf("strconv.FormatInt(%s, 10)", inner), wrap)
		return nil
	}
	v, err := c.expr(val, false)
	if err != nil {
		return err
	}
	c.emitPlainValueLocal(v, pos.Line)
	return nil
}

// staticIntPrint reports whether the print operand at n is a static-Int
// expression -- an Int literal or an approved inline loop field read of an
// Int-valued field -- returning the int64 Go expression that computes it.
// Such a value's ToText is exactly strconv.FormatInt over that int64, so the
// print site formats the digits without materializing the Value.
func (c *compiler) staticIntPrint(n *ast.Node) (string, bool) {
	if n == nil {
		return "", false
	}
	switch n.Kind {
	case ast.KindInt:
		return strconv.FormatInt(n.Int, 10), true
	case ast.KindAttr, ast.KindIndex:
		if ir, ok := c.an.inlineReads[n]; ok {
			return c.inlineLoopInt(ir)
		}
	}
	return "", false
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
// text is discarded and no rendered output is produced. The unit's manifest
// records the statement (UsesLog) so the Environment's compiled dispatch can
// fall back when a host logger would have received the line.
func (c *compiler) stmtLog(n *ast.Node) error {
	c.usesLog = true
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
		c.linef(c.ret(c.qposE(fmt.Sprintf("qerrors.New(qerrors.KindRuntime, \"unknown escape strategy %%q; expected one of off, html, js, css, \"+\"html_attr, html_attr_relaxed, url\", %s)", q(n.Str)), n.Line)))
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
// result binds as Safe under an active strategy, else Str. A tab-free module
// writes into the strings.Builder directly; any other module wraps it in the
// fresh qWriter whose indent starts suspended.
func (c *compiler) stmtCapture(n *ast.Node) error {
	body := n.Children
	if len(body) > 0 && body[0] != nil && body[0].Kind == ast.KindType {
		body = body[1:]
	}
	sb, err := c.captureBody(body)
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

// captureBody renders body into a fresh strings.Builder with indentation
// suspended -- the shared shape captureItems takes for @set...capture and
// @apply. A tab-free module writes into the builder directly; any other module
// wraps it in a fresh qWriter starting at line-start with an empty indent, so
// the captured text is unindented regardless of the enclosing @tab region. It
// returns the builder local's name; the caller decides how to seed a value
// from its String(). The capture counts toward the @yield guard depth, so a
// @yield reached inside a body whose text becomes a consumed value is rejected.
func (c *compiler) captureBody(body []*ast.Node) (string, error) {
	return c.captureInto(body, true)
}

// captureInto is captureBody with explicit control over the @yield guard. An
// @include splices its captured partial output RAW into the render stream, so a
// @yield inside it reaches the finished buffer exactly like a top-level @yield
// and the single resolve pass backfills it; that capture passes guarded=false
// so the partial's own top-level @yield stays compilable. A value-consuming
// capture (@capture, @apply, @provide) passes guarded=true, because a @yield
// whose placeholder is folded into a consumed value would leak a token the
// resolve pass cannot reach.
func (c *compiler) captureInto(body []*ast.Node, guarded bool) (string, error) {
	sb := c.tmp("qs")
	c.linef("var %s strings.Builder", sb)
	if c.tabFree {
		c.writers = append(c.writers, "&"+sb)
	} else {
		cw := c.tmp("qcw")
		c.linef("%s := &qWriter{w: &%s, atLineStart: true}", cw, sb)
		c.linef("_ = %s", cw)
		c.writers = append(c.writers, cw)
	}
	if guarded {
		c.captureDepth++
	}
	err := c.stmtList(body)
	if guarded {
		c.captureDepth--
	}
	c.writers = c.writers[:len(c.writers)-1]
	if err != nil {
		return "", err
	}
	return sb, nil
}

// stmtApply lowers "@apply | f | g { body }" like execApply: capture the body
// with indentation suspended, seed the piped value as runtime.Str, run the
// filter chain, then emit. The captured text has already flowed through the
// active escape strategy (every interpolation inside the body was emitted, so
// escaped), so under a live strategy a filtered result whose kind is not KSafe
// is wrapped runtime.Safe to keep the final emit from escaping already-escaped
// bytes a second time -- byte-exact to execApply's double-escape guard. The
// sandbox arrow/stringify gates execApply runs are !sandboxOn no-ops on the
// compiled path (the dispatch gate refuses any sandboxed unit), so they lower
// to nothing.
func (c *compiler) stmtApply(n *ast.Node) error {
	filterCount := n.IntCount()
	filters := n.Children[:filterCount]
	body := n.Children[filterCount:]
	sb, err := c.captureBody(body)
	if err != nil {
		return err
	}
	v := c.tmp("qv")
	c.linef("%s := runtime.Str(%s.String())", v, sb)
	for _, f := range filters {
		if err := c.applyFilter(f, v); err != nil {
			return err
		}
	}
	if c.escapeStrategy() != "" {
		text := c.tmp("qt")
		e := c.tmp("qe")
		c.openf("if %s.Kind != runtime.KSafe {", v)
		c.linef("%s, %s := runtime.ToText(%s)", text, e, v)
		c.checkErr(e, n.Line)
		c.linef("%s = runtime.Safe(%s)", v, text)
		c.closeb()
		c.emitValueLocal(v, n.Line)
		return nil
	}
	c.emitPlainValueLocal(v, n.Line)
	return nil
}

// applyFilter lowers one filter in an @apply chain, rebinding the running value
// local v in place. It mirrors exprFilter's dispatch -- registry lookup, the
// zero-explicit-arg Fn1 fast path, Needs* injection -- but the piped value is
// the running local rather than a lowered child expression, the argument slice
// comes from execApply's own non-expanding loop through applyArgs (a spread
// argument stays one array value, unlike the inline filter path), and the
// unknown-filter error text is execApply's "unknown filter %q in apply" variant
// (not exprFilter's "unknown filter %q"), whose timing is observable in
// streamed output so it stays at the call site.
func (c *compiler) applyFilter(f *ast.Node, v string) error {
	fv, fok := c.callable("Filter", f.Str)
	c.openf("if !%s {", fok)
	c.linef(c.ret(c.qposE(fmt.Sprintf("qerrors.New(qerrors.KindRuntime, \"unknown filter %%q in apply\", %s)", q(f.Str)), f.Line)))
	c.closeb()
	res := c.tmp("qt")
	e := c.tmp("qe")
	// The zero-explicit-argument fast path keys off the same syntactic test the
	// interpreter's fast call uses: any KindArg child, spread included, takes
	// the general slice path (staticArgCount returns not-static for a spread and
	// a positive count for any positional, so static && count == 0 holds exactly
	// when no argument child exists). Because the interpreter's fast call and
	// execApply's always-Fn call are byte-equivalent for the audited Fn1 set
	// (ext keeps Fn's zero-extra-arg behavior identical to Fn1), the fast branch
	// reproduces execApply's Fn dispatch exactly.
	if count, static := staticArgCount(f); static && count == 0 {
		ffast := c.callableFilterFast(f.Str)
		c.linef("var %s runtime.Value", res)
		c.linef("var %s error", e)
		c.openf("if %s {", ffast)
		c.linef("%s, %s = %s.Fn1(%s)", res, e, fv, v)
		c.ind--
		c.linef("} else {")
		c.ind++
		args, err := c.applyArgs(f, v)
		if err != nil {
			return err
		}
		c.emitInject(fv, c.callableInject("Filter", f.Str), args)
		c.linef("%s, %s = %s.Fn(%s)", res, e, fv, args)
		c.closeb()
		c.checkErr(e, f.Line)
		c.linef("%s = %s", v, res)
		return nil
	}
	args, err := c.applyArgs(f, v)
	if err != nil {
		return err
	}
	c.emitInject(fv, c.callableInject("Filter", f.Str), args)
	c.linef("%s, %s := %s.Fn(%s)", res, e, fv, args)
	c.checkErr(e, f.Line)
	c.linef("%s = %s", v, res)
	return nil
}

// applyArgs lowers an @apply filter's KindArg children into a positional
// []runtime.Value local seeded with the piped value, replicating execApply's
// own inline loop rather than the inline filter path's collectArgs. The one
// behavioral difference that matters: execApply appends a spread argument as a
// single array value, NOT its expanded elements, so `@apply | f(...xs)` hands
// the filter one array where the inline `| f(...xs)` form hands it xs's members.
// Reusing collectArgs here would expand the spread and diverge from the
// interpreter in both output bytes and error position; this loop keeps the two
// paths byte-identical. A not-compilable argument expression propagates so the
// whole @apply statement returns ErrNotCompilable and the dispatch gate falls
// back to the interpreter.
func (c *compiler) applyArgs(f *ast.Node, piped string) (string, error) {
	args := c.tmp("qa")
	c.linef("%s := []runtime.Value{%s}", args, piped)
	for _, ch := range f.Children {
		if ch.Kind != ast.KindArg {
			continue
		}
		v, err := c.expr(ch.Child(0), false)
		if err != nil {
			return "", err
		}
		c.linef("%s = append(%s, %s)", args, args, v)
	}
	return args, nil
}

// stmtCache lowers "@cache key=... [ttl=...] [tags=...] { body }" like
// execCache: the body's already-rendered output is memoized in the render's
// RenderCache under a template-namespaced key, replayed verbatim on a hit and
// re-rendered on a miss. The mechanism reproduces execCache primitive for
// primitive so the compiled and interpreted paths agree on which render is
// served from the store and on the bytes.
//
//   - The head arguments validate exactly as execCache reads them: an unknown
//     argument name raises the interpreter's runtime error at that argument's
//     position, and a missing key raises "@cache requires a key" at the region.
//     Both are decidable from the static head, so they lower as unconditional
//     raises like an unknown @escape strategy. The ttl expression is never
//     evaluated -- a documented no-op for the non-expiring in-memory cache.
//   - The key evaluates in the CALLER scope, coerces to text, and is namespaced
//     by the RENDER-ROOT template name (root.Name + NUL + user key), matching
//     execCache's in.root.Name: the entry template for a @cache reached through
//     an inlined @block or @use trait, and the partial only for one reached
//     through an inlined @include, exactly as the interpreter's sub-interp keys
//     under its own root there.
//   - A hit (rc non-nil and the key present) splices the stored body raw through
//     the active writer -- the analog of execCache's emitString on a hit -- and
//     skips the body, so a body-local @set never runs and its side effect never
//     surfaces, matching the interpreter's hit path.
//   - A miss renders the body in a child scope (a frameWith over a null map, the
//     analog of ctx.Child()), so a body-local @set does not leak to the caller.
//     The body captures raw like an @include (guarded=false), so a @yield inside
//     it reaches the finished buffer and the single resolve pass backfills it.
//   - The store is gated by the slot-touching rule (commit 4fd0da3): a body that
//     grew a slot buffer or wrote a yield placeholder is never memoized, because
//     a replay would emit a stale render's placeholder or silently drop a
//     render-scoped @provide. The gate reproduces execCache's slotStamp
//     difference plus the render-unique-token scan; a slot-free unit has no slot
//     state, so its gate is statically false and the body is always stored.
//   - Tags evaluate in the CALLER scope on the store path only, matching
//     execCache, and pass through to Put with the exact list-coercion rule.
func (c *compiler) stmtCache(n *ast.Node) error {
	c.usesCache = true
	count := n.IntCount()
	args := n.Children[:count]
	body := n.Children[count:]

	var keyExpr, tagsExpr *ast.Node
	for _, a := range args {
		switch a.Str {
		case "key":
			keyExpr = a.Child(0)
		case "ttl":
			// The ttl is accepted for API symmetry but never evaluated, exactly
			// as execCache leaves ttlExpr unused for the non-expiring cache.
		case "tags":
			tagsExpr = a.Child(0)
		default:
			c.openf("if true {")
			c.linef(c.ret(c.qposE(fmt.Sprintf("qerrors.New(qerrors.KindRuntime, \"unknown cache argument %%q (want key, ttl, or tags)\", %s)", q(a.Str)), a.Line)))
			c.closeb()
			return nil
		}
	}
	if keyExpr == nil {
		c.openf("if true {")
		c.linef(c.ret(c.qposE("qerrors.New(qerrors.KindRuntime, \"@cache requires a key\")", n.Line)))
		c.closeb()
		return nil
	}

	c.openf("{")
	// The key evaluates in the caller scope and coerces to text, positioned at
	// the region like execCache's ToText error.
	kv, err := c.expr(keyExpr, false)
	if err != nil {
		c.closeb()
		return err
	}
	keyText := c.tmp("qck")
	ke := c.tmp("qe")
	c.linef("%s, %s := runtime.ToText(%s)", keyText, ke, kv)
	c.checkErr(ke, n.Line)
	// Namespace the user key by the RENDER ROOT so two templates that both cache
	// under one key do not collide, matching execCache's in.root.Name. The render
	// root is the entry template even for a @cache inlined from a parent @block or
	// an @use trait (the interpreter leaves in.root untouched there), and switches
	// to the partial only for a @cache inlined from an @include, where the
	// interpreter renders under a fresh sub-interp rooted at the partial. srcRef()
	// (the error-position source) would instead switch at every block/trait
	// boundary, colliding two @extends children of one base under the base name.
	fullKey := c.tmp("qcf")
	c.linef("%s := %s.Name() + \"\\x00\" + %s", fullKey, c.rootRef(), keyText)

	hit := c.tmp("qch")
	c.linef("%s := false", hit)
	c.openf("if rc != nil {")
	cached := c.tmp("qcc")
	cok := c.tmp("qco")
	c.openf("if %s, %s := rc.Get(%s); %s {", cached, cok, fullKey, cok)
	c.emitWrite(cached, func(e string) string { return c.qposE(e, n.Line) })
	c.linef("%s = true", hit)
	c.closeb()
	c.closeb()

	c.openf("if !%s {", hit)
	if err := c.cacheMiss(n, body, fullKey, tagsExpr); err != nil {
		c.closeb()
		c.closeb()
		return err
	}
	c.closeb()
	c.closeb()
	return nil
}

// cacheMiss lowers the @cache miss path: render the body in a child scope,
// gate the store on the slot-touching rule, store the fresh body under fullKey
// when the gate allows, and splice it raw through the active writer. It runs
// inside the "if !hit" block stmtCache opened.
func (c *compiler) cacheMiss(n *ast.Node, body []*ast.Node, fullKey string, tagsExpr *ast.Node) error {
	// The slot stamp before the body: label count and total buffered bytes. Only
	// a slots unit carries slot state, so a slot-free unit skips the stamp and
	// its store gate is statically false.
	preLabels, preBytes := "", ""
	if c.usesSlots {
		preLabels, preBytes = c.tmp("qcpl"), c.tmp("qcpb")
		c.linef("%s, %s := qcacheStamp(qslots)", preLabels, preBytes)
	}

	// Render the body in a fresh child frame over a null map, the analog of
	// ctx.Child(): reads fall through to the caller frames and a body-local @set
	// binds only in this frame. The capture is transparent to the @yield guard
	// (guarded=false), so a @yield inside the body reaches the finished buffer
	// like an included partial's @yield, and the resolve pass backfills it.
	binds := c.scanBinds(body)
	if err := c.checkBindNames(binds, n); err != nil {
		return err
	}
	f := c.pushFrame(frameWith, binds)
	f.withVar = "runtime.Null()"
	savedCond := c.condDepth
	c.condDepth = 0
	sb, err := c.captureInto(body, false)
	c.condDepth = savedCond
	c.popFrame()
	if err != nil {
		return err
	}

	// The store gate: a body that grew a slot buffer or wrote a yield placeholder
	// must never be memoized. A slot-free unit has no slot state, so the gate is
	// the constant false and the store always runs.
	used := "false"
	if c.usesSlots {
		used = c.tmp("qcu")
		postLabels, postBytes := c.tmp("qcql"), c.tmp("qcqb")
		c.linef("%s, %s := qcacheStamp(qslots)", postLabels, postBytes)
		// The token scan mirrors execCache: qtok is this render's unique
		// placeholder, so a body containing it wrote a @yield. The token is
		// render-unique, so a stored body can never collide with authored text.
		c.linef("%s := %s != %s || %s != %s || strings.Contains(%s.String(), qtok)",
			used, postLabels, preLabels, postBytes, preBytes, sb)
	}

	c.openf("if rc != nil && !%s {", used)
	tags := "nil"
	if tagsExpr != nil {
		// Tags evaluate in the caller scope on the store path only, matching
		// execCache, and coerce through the same list rule.
		tv, err := c.expr(tagsExpr, false)
		if err != nil {
			c.closeb()
			return err
		}
		te := c.tmp("qct")
		tags = c.tmp("qcg")
		c.linef("%s, %s := qcacheTags(%s)", tags, te, tv)
		// A tag whose element is unrenderable fails here. execCache returns
		// evalCacheTags's error verbatim, without positioning it at the region, so
		// this returns it raw too: a qpos wrap would add a source location the
		// interpreter's error text does not carry.
		c.openf("if %s != nil {", te)
		c.linef(c.ret(te))
		c.closeb()
	}
	c.linef("rc.Put(%s, %s.String(), %s)", fullKey, sb, tags)
	c.closeb()

	// Splice the fresh body raw through the active writer, positioned at the
	// region like execCache's emitString on a miss.
	c.emitWrite(sb+".String()", func(e string) string { return c.qposE(e, n.Line) })
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
	binds := c.scanBinds(n.Children[1:])
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
	count := n.IntCount()
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
		c.linef(c.ret(c.qposE(e, tg.Line)))
		c.closeb()
		return nil
	}
	key, err := c.expr(tg.Child(1), false)
	if err != nil {
		return err
	}
	e := c.tmp("qe")
	c.openf("if %s := runtime.SetIndex(%s, qkeyOf(%s), %s); %s != nil {", e, recv, key, val, e)
	c.linef(c.ret(c.qposE(e, tg.Line)))
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
		c.linef(c.ret(c.qposE(e2, n.Line)))
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
		c.linef(c.ret(c.qposE(e2, n.Line)))
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
		c.linef(c.ret(c.qposE(`qerrors.New(qerrors.KindRuntime, "unknown destructuring pattern")`, pat.Line)))
		c.closeb()
		return nil
	}
}

// bindListPattern lowers sequence destructuring with the interpreter's exact
// arity rules: required slots bound positionally, elided slots skipped,
// optional slots null-padded, and a trailing spread capturing the rest.
func (c *compiler) bindListPattern(pat *ast.Node, val string) error {
	c.openf("if %s.Kind != runtime.KArray || %s.Arr == nil {", val, val)
	c.linef(c.ret(c.qposE(`qerrors.New(qerrors.KindRuntime, "destructuring expects a sequence")`, pat.Line)))
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
	c.linef(c.ret(c.qposE(fmt.Sprintf("qerrors.New(qerrors.KindRuntime, \"sequence destructuring expects at least %%d element(s) but got %%d\", %d, len(%s))", required, ps), pat.Line)))
	c.closeb()
	if tail == nil {
		c.openf("if len(%s) > %d {", ps, len(fixed))
		c.linef(c.ret(c.qposE(fmt.Sprintf("qerrors.New(qerrors.KindRuntime, \"sequence destructuring expects %%d element(s) but got %%d\", %d, len(%s))", len(fixed), ps), pat.Line)))
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
	// filter frame, plus the block-bind union when the condition splices a
	// composition body (a parent() or block() call renders into this frame).
	exprBinds(filter.Child(0), func(name string) { binds = append(binds, name) })
	if c.unit != nil && nodeContainsComposition(filter.Child(0)) {
		binds = append(binds, c.unit.compBinds...)
	}
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
	bodyBinds := c.scanBinds(body.Children)
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
	// the loop-aware probe, whose value local is boxed on demand at each
	// materialization site (emitLoopValue). A materialized loop boxes the
	// probe once here into the *runtime.Value its per-iteration bind stores
	// (loopInfo carries parent as a pointer at the per-loop probe); the boxed
	// pointee stays unwritten for the loop's lifetime, so every captured
	// snapshot keeps reading the entry-time bits.
	var parentLoop string
	if inline {
		parentLoop = c.emitLoopParent(len(c.frames))
	} else {
		parentVal, _ := c.probeName("loop")
		parentLoop = c.emitLoopParentBox(parentVal)
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
