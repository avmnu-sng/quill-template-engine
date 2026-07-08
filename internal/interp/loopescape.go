package interp

import "github.com/avmnu-sng/quill-template-engine/pkg/ast"

// This file is the interpreter's Prepare-time loop-escape analysis: a walk over
// every @for that decides whether its per-iteration loop value (and the pair
// snapshot that value holds) provably never outlives the loop's iteration. A
// loop that clears the analysis may materialize its entry-time pair snapshot
// into a recycled buffer instead of a fresh slice per render, because nothing
// can read that snapshot after the loop advances or ends. A loop the analysis
// cannot clear keeps the fresh-per-render Pairs() path, where the snapshot may
// be captured and read arbitrarily late.
//
// The escape taxonomy is deliberately the exact one the compiled backend's
// escape analysis uses (compile/loopopt.go analyzeLoops), reproduced here as
// interp's own walk because interp must not import compile. The two answers can
// only ever differ by interp being MORE conservative (a false positive costs a
// missed pooling; a false negative would recycle a buffer a live snapshot still
// reads, which is a correctness bug), so every construct the compiled analyzer
// does not model -- includes, embeds, caches, macros, blocks, and every other
// statement outside its subset -- escapes here by default.
//
// Unlike the compiled analyzer this walk records no inline field reads: pooling
// still materializes a full snapshot, so it needs only the yes/no escape
// decision, not the arithmetic-lowering plan. The mutation scan is likewise
// omitted, because a fresh materialized snapshot masks an in-place mutation of
// the iterated array exactly as today's Pairs() slice does; only reachability
// of the snapshot past the loop matters here.

// escFrameKind discriminates the escape walk's scope-model frames, mirroring
// the frames the interpreter's own execution pushes for the same constructs.
type escFrameKind int

const (
	escRoot escFrameKind = iota
	escLoop
	escFilter
	escWith
	escWithOnly
	escArrow
)

// escFrame is one scope frame in the escape walk. forNode names the @for a loop
// frame belongs to; bindsLoop marks a non-loop frame (a fused-filter condition)
// that may itself bind the name loop, making any reference resolving past it
// ambiguous.
type escFrame struct {
	kind      escFrameKind
	forNode   *ast.Node
	bindsLoop bool
}

// escInlineFields is the loop.* field set a dotted (or string-subscript) read
// resolves to a scalar without retaining the loop value, so a body that only
// reads these fields does not let the loop escape. It mirrors the compiled
// backend's inlineLoopFields: parent is a chain step (handled by the peel
// loop), and depth/depth0/changed are recursive-only or call-shaped and never
// reach this set.
var escInlineFields = map[string]bool{
	"index":     true,
	"index0":    true,
	"revindex":  true,
	"revindex0": true,
	"first":     true,
	"last":      true,
	"length":    true,
	"prev":      true,
	"next":      true,
}

// loopEscapeAnalyzer walks a module with a scope model rich enough to resolve
// the name loop -- which @for body a reference lands in, and whether anything
// between the reference and that body (a with frame, an arrow, a user binding
// of loop) makes the resolution unprovable. It accumulates the set of @for
// nodes whose loop value may escape their iteration.
type loopEscapeAnalyzer struct {
	escapes    map[*ast.Node]bool
	analyzed   map[*ast.Node]bool
	fused      map[*ast.Node]bool
	fors       []*ast.Node
	parents    map[*ast.Node]*ast.Node
	stack      []escFrame
	arrowDepth int
}

// analyzeLoopEscapes walks a parsed module and returns the set of @for nodes
// whose per-iteration loop value provably never escapes the loop -- the pool-
// safe loops. A node absent from the returned map (including any @for the walk
// never reached) is treated as escaping, the conservative default.
//
// A fused @for..if is never in the returned set. Pooling recycles the entry-time
// pair snapshot execFor materializes into a shared buffer, but a fused loop
// replaces that snapshot with a freshly allocated survivors slice (filterLoopPairs)
// before iterating, so its loop values never read the pooled buffer -- execFor
// gates pooling on filter == nil for exactly that reason. The escape walk cannot
// even answer the pool-safety question correctly for a fused loop: the filter
// condition is walked in its own frame BEFORE the loop's escLoop frame is pushed
// (walkFor), so an inline `loop = x` bind in the filter escapes no loop
// (escapeInnermost finds none) and leaves the fused loop wrongly safe. Dropping
// every fused loop here keeps forSafe's contract honest and makes it a second,
// independent guard: a future edit that pools on forSafe alone still cannot pool a
// fused loop, so it can never recycle a buffer a survivors-aliasing snapshot reads.
func analyzeLoopEscapes(mod *ast.Node) map[*ast.Node]bool {
	a := &loopEscapeAnalyzer{
		escapes:  map[*ast.Node]bool{},
		analyzed: map[*ast.Node]bool{},
		fused:    map[*ast.Node]bool{},
		parents:  map[*ast.Node]*ast.Node{},
		stack:    []escFrame{{kind: escRoot}},
	}
	a.walkItems(mod.Children)
	a.propagate()
	safe := make(map[*ast.Node]bool, len(a.fors))
	for _, n := range a.fors {
		if !a.escapes[n] && !a.fused[n] {
			safe[n] = true
		}
	}
	return safe
}

// propagate closes the escape set under the outward parent-link rule: an
// escaping loop's per-iteration value carries a pointer to the enclosing loop's
// value, so a captured child snapshot reaches the enclosing loop's pair buffer
// through loop.parent. The enclosing loop its parent link resolves to must
// therefore escape too, or recycling that buffer would clobber the reachable
// snapshot. Iterating to a fixpoint carries the mark up an arbitrarily deep nest.
func (a *loopEscapeAnalyzer) propagate() {
	for changed := true; changed; {
		changed = false
		for _, n := range a.fors {
			if !a.escapes[n] {
				continue
			}
			if p := a.parents[n]; p != nil && !a.escapes[p] {
				a.escapes[p] = true
				changed = true
			}
		}
	}
}

// push adds one scope frame.
func (a *loopEscapeAnalyzer) push(f escFrame) { a.stack = append(a.stack, f) }

// pop removes the innermost scope frame.
func (a *loopEscapeAnalyzer) pop() { a.stack = a.stack[:len(a.stack)-1] }

// mark records that the loop at n may let its value escape.
func (a *loopEscapeAnalyzer) mark(n *ast.Node) {
	if n != nil {
		a.escapes[n] = true
	}
}

// escapeInnermost marks the innermost lexically enclosing loop as escaping, if
// any. It ignores scope cuts deliberately: over-marking is always safe, and the
// callers (a rebind of the name loop, a scope-enumerating construct) want the
// conservative answer.
func (a *loopEscapeAnalyzer) escapeInnermost() {
	for i := len(a.stack) - 1; i >= 0; i-- {
		if a.stack[i].kind == escLoop {
			a.mark(a.stack[i].forNode)
			return
		}
	}
}

// attributeLoop resolves a reference to the name loop at the current stack: the
// @for it lands on (nil when no loop is reachable, e.g. past an only-with cut)
// plus whether the resolution is provable. Crossing a with frame (its map may
// bind loop at runtime), an arrow body (the read happens at call time, possibly
// after the loop advanced or ended), or a filter frame that may bind loop makes
// the resolution unclean.
func (a *loopEscapeAnalyzer) attributeLoop() (target *ast.Node, clean bool) {
	clean = true
	for i := len(a.stack) - 1; i >= 0; i-- {
		switch f := a.stack[i]; f.kind {
		case escLoop:
			return f.forNode, clean
		case escWithOnly:
			return nil, false
		case escWith, escArrow:
			clean = false
		case escFilter:
			if f.bindsLoop {
				clean = false
			}
		case escRoot:
			return nil, false
		}
	}
	return nil, false
}

// nearestLoop finds the loop a new @for's parent link resolves to at the
// current stack: the first enclosing loop frame, stopping at an only-with cut.
// With frames and possible user bindings of loop do not stop the search: the
// runtime probe may still fall through to the loop's binding, so propagation
// must reach it (over-propagating only ever marks more, never breaks).
func (a *loopEscapeAnalyzer) nearestLoop() *ast.Node {
	for i := len(a.stack) - 1; i >= 0; i-- {
		switch f := a.stack[i]; f.kind {
		case escLoop:
			return f.forNode
		case escWithOnly:
			return nil
		}
	}
	return nil
}

// walkItems walks a statement list.
func (a *loopEscapeAnalyzer) walkItems(items []*ast.Node) {
	for _, it := range items {
		a.walkStmt(it)
	}
}

// walkStmt walks one statement, mirroring the interpreter's execItem dispatch
// for the loop-relevant constructs. A statement kind this switch does not name
// escapes the innermost loop conservatively: it is either a scope-enumerating
// or foreign-body construct (include, embed, cache, apply, macro call, block
// site, provide, yield, call block) whose effect on the name loop the walk does
// not model, or a leaf (text, declaration) whose default escape is harmless
// because it references no loop. Marking the innermost loop on every unmodeled
// construct is the conservative floor the compiled analyzer reaches by aborting
// compilation on the same constructs.
func (a *loopEscapeAnalyzer) walkStmt(n *ast.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case ast.KindText, ast.KindVerbatim, ast.KindExtends, ast.KindMacro,
		ast.KindImport, ast.KindFrom, ast.KindUse, ast.KindFlush,
		ast.KindTypes, ast.KindDeprecated, ast.KindLine:
		// Leaves and declarations: no loop reference, nothing to walk.
	case ast.KindPrint, ast.KindDo, ast.KindLog:
		a.walkExpr(n.Child(0), true)
	case ast.KindIf:
		for _, cl := range n.Children {
			body := cl.Children
			if cl.Bool {
				a.walkExpr(cl.Child(0), true)
				body = cl.Children[1:]
			}
			a.walkItems(body)
		}
	case ast.KindFor:
		a.walkFor(n)
	case ast.KindSet:
		a.walkSet(n)
	case ast.KindCapture:
		if n.Str == "loop" {
			a.escapeInnermost()
		}
		for _, it := range n.Children {
			if it != nil && it.Kind == ast.KindType {
				continue
			}
			a.walkStmt(it)
		}
	case ast.KindWith:
		a.walkExpr(n.Child(0), true)
		kind := escWith
		if n.Bool {
			kind = escWithOnly
		}
		a.push(escFrame{kind: kind})
		a.walkItems(n.Children[1:])
		a.pop()
	case ast.KindTabBlock, ast.KindGuard:
		a.walkExpr(n.Child(0), true)
		a.walkItems(n.Children[1:])
	case ast.KindEscape:
		a.walkItems(n.Children)
	case ast.KindSandbox:
		a.walkItems(n.Children)
	case ast.KindBlock:
		a.walkBlock(n)
	default:
		// Every remaining construct (include, embed, cache, apply, provide,
		// yield, call block, and anything added later) is outside the modeled
		// subset: it may render a foreign body under this loop's context or
		// enumerate the scope, so the innermost loop escapes.
		a.escapeInnermost()
	}
}

// walkBlock analyzes a @block: its body renders at a dispatch site the walk
// cannot see (the merged block table resolves it, possibly to a child override,
// under whatever scope the site carries), so a bare loop inside it could capture
// an enclosing loop present at that site. The innermost lexically enclosing loop
// is therefore marked escaping before the body is walked. The body itself is
// then analyzed under a scope-cut frame so a self-contained loop DEFINED inside
// the block still qualifies for pooling (the common case: an override block that
// simply loops over its own data), while a bare loop resolving PAST the block --
// to the unknown dispatch site's loop -- cannot be proven safe. The override
// definition that actually runs is analyzed in ITS own template's Prepare, where
// this same rule applies, so a capturing override never slips through: the loop
// that dispatches the block (a block site inside a @for body) is marked escaping
// by that site's presence, and any same-template enclosing loop is marked here.
func (a *loopEscapeAnalyzer) walkBlock(n *ast.Node) {
	a.escapeInnermost()
	body := n.Children
	for len(body) > 0 && (body[0].Kind == ast.KindParams || body[0].Kind == ast.KindType) {
		body = body[1:]
	}
	a.push(escFrame{kind: escWithOnly})
	if n.Int == 1 {
		// Shortcut value form (@block title "x"): the single trailing expression
		// is the emitted value.
		if len(body) > 0 {
			a.walkExpr(body[len(body)-1], true)
		}
	} else {
		a.walkItems(body)
	}
	a.pop()
}

// walkFor walks one @for with the interpreter's exact scoping: the iterand and
// the else body resolve in the enclosing frame, the fused filter runs in a frame
// that binds only the targets (never loop), and the body frame binds loop.
func (a *loopEscapeAnalyzer) walkFor(n *ast.Node) {
	count := int(n.Int & ast.ForTargetCount)
	target1 := n.Child(0)
	var target2 *ast.Node
	idx := 1
	if count == 2 {
		target2 = n.Child(1)
		idx = 2
	}
	a.walkExpr(n.Child(idx), true)

	bodyIdx := idx + 1
	var filter *ast.Node
	if fc := n.Child(bodyIdx); fc != nil && fc.Kind == ast.KindClause {
		filter = fc
		bodyIdx++
	}
	body := n.Child(bodyIdx)
	var elseBody *ast.Node
	if n.Bool {
		elseBody = n.Child(bodyIdx + 1)
	}

	if a.analyzed[n] {
		// A body reachable at more than one site (a future inlining) revisits
		// its loops; the single per-node decision cannot hold two contexts, so
		// mark escaping. A single-template Prepare walk never revisits a node,
		// so this is a defensive floor, not a hot path.
		a.mark(n)
	} else {
		a.analyzed[n] = true
		a.fors = append(a.fors, n)
	}
	if p := a.nearestLoop(); p != nil {
		a.parents[n] = p
	}

	// A target named loop shadows the loop's own binding mid-bind; treat the
	// loop as escaping rather than reasoning about the overwrite order.
	targetsLoop := target1 != nil && target1.Str == "loop" ||
		target2 != nil && target2.Str == "loop"
	if targetsLoop {
		a.mark(n)
	}

	if filter != nil {
		// A fused loop iterates a freshly allocated survivors slice, never the
		// pooled snapshot buffer, so it is never pool-safe (analyzeLoopEscapes drops
		// it from the returned set). Recorded here rather than via mark(n) so the
		// exclusion does not propagate outward and cost an enclosing loop its pooling.
		a.fused[n] = true
		binds := targetsLoop
		exprBindsLoop(filter.Child(0), &binds)
		a.push(escFrame{kind: escFilter, bindsLoop: binds})
		a.walkExpr(filter.Child(0), true)
		a.pop()
	}
	if body != nil {
		a.push(escFrame{kind: escLoop, forNode: n})
		a.walkItems(body.Children)
		a.pop()
	}
	if elseBody != nil {
		a.walkItems(elseBody.Children)
	}
}

// walkSet walks one @set: a target that binds or rebinds the name loop (a plain
// target, a destructuring slot, or the privatize-and-rebind root of a member
// assignment) escapes the innermost loop; index keys along a member path and
// the value expressions are ordinary expression positions.
func (a *loopEscapeAnalyzer) walkSet(n *ast.Node) {
	count := n.IntCount()
	for i, tg := range n.Children {
		if tg == nil {
			continue
		}
		if i >= count {
			a.walkExpr(tg, true)
			continue
		}
		switch tg.Kind {
		case ast.KindTarget, ast.KindName:
			if tg.Str == "loop" {
				a.escapeInnermost()
			}
		case ast.KindAttr, ast.KindIndex:
			if root := setRootOf(tg); root != nil && root.Kind == ast.KindName && root.Str == "loop" {
				a.escapeInnermost()
			}
			for p := tg; p != nil && (p.Kind == ast.KindAttr || p.Kind == ast.KindIndex); p = p.Child(0) {
				if p.Kind == ast.KindIndex {
					a.walkExpr(p.Child(1), true)
				}
			}
		case ast.KindListPattern, ast.KindMapPattern:
			patternBindsLoop(tg, a)
		}
	}
}

// walkExpr walks one expression. allowInline gates the safe-read recognition:
// inside an is-defined operand access chains lower to presence probes rather
// than value reads, so a loop chain there is never a plain field read and falls
// to the escaping default.
func (a *loopEscapeAnalyzer) walkExpr(n *ast.Node, allowInline bool) {
	if n == nil {
		return
	}
	switch n.Kind {
	case ast.KindName:
		if n.Str == "loop" {
			// A bare loop in any value position lets the loop value escape:
			// bound, passed, compared, piped, emitted, or called.
			if target, _ := a.attributeLoop(); target != nil {
				a.mark(target)
			}
		}
	case ast.KindSpecialName:
		if n.Str == "_context" {
			// _context enumerates every visible name including loop, surfacing
			// the loop value into a structure that outlives the iteration, so
			// the innermost loop escapes.
			a.escapeInnermost()
		}
	case ast.KindAttr, ast.KindIndex:
		if allowInline && a.safeLoopRead(n) {
			return
		}
		a.walkExpr(n.Child(0), allowInline)
		if n.Kind == ast.KindIndex {
			a.walkExpr(n.Child(1), allowInline)
		}
	case ast.KindCall:
		callee := n.Child(0)
		if callee != nil && callee.Kind == ast.KindAttr && callee.Str == "changed" &&
			callee.Child(0) != nil && callee.Child(0).Kind == ast.KindName && callee.Child(0).Str == "loop" {
			// loop.changed(expr) is recognized syntactically before the receiver
			// exists as a value; only its argument is a value position, so the
			// loop.changed receiver never counts as a bare-loop escape.
			for _, ch := range n.Children[1:] {
				if ch != nil && ch.Kind == ast.KindArg {
					a.walkExpr(ch.Child(0), allowInline)
				}
			}
			return
		}
		if a.arrowDepth > 0 && callee != nil && callee.Kind == ast.KindName {
			// A registry call injects a needs-context view of the scope; inside
			// an arrow that injection runs at call time, possibly after the loop
			// advanced or ended, so the loop's live value must still exist.
			a.escapeInnermost()
		}
		for _, ch := range n.Children {
			a.walkExpr(ch, allowInline)
		}
	case ast.KindFilter:
		if a.arrowDepth > 0 {
			// Same deferred needs-context injection reasoning as calls.
			a.escapeInnermost()
		}
		for _, ch := range n.Children {
			a.walkExpr(ch, allowInline)
		}
	case ast.KindArg, ast.KindSpread:
		a.walkExpr(n.Child(0), allowInline)
	case ast.KindMap:
		for _, e := range n.Children {
			if e == nil {
				continue
			}
			switch e.Int {
			case ast.MapEntryKeyed:
				a.walkExpr(e.Child(1), allowInline)
			case ast.MapEntryShorthand, ast.MapEntrySpread:
				a.walkExpr(e.Child(0), allowInline)
			case ast.MapEntryComputed:
				a.walkExpr(e.Child(0), allowInline)
				a.walkExpr(e.Child(1), allowInline)
			}
		}
	case ast.KindTest:
		if n.Str == "defined" {
			a.walkExpr(n.Child(0), false)
			return
		}
		for _, ch := range n.Children {
			a.walkExpr(ch, allowInline)
		}
	case ast.KindAssign:
		if tg := n.Child(0); tg != nil &&
			(tg.Kind == ast.KindName || tg.Kind == ast.KindTarget) && tg.Str == "loop" {
			a.escapeInnermost()
		}
		a.walkExpr(n.Child(1), allowInline)
	case ast.KindArrow:
		a.arrowDepth++
		a.push(escFrame{kind: escArrow})
		for _, ch := range n.Children {
			a.walkExpr(ch, allowInline)
		}
		a.pop()
		a.arrowDepth--
	default:
		for _, ch := range n.Children {
			a.walkExpr(ch, allowInline)
		}
	}
}

// safeLoopRead reports whether n is a scalar-yielding loop.<field> read (over
// zero or more dotted .parent steps) that resolves cleanly to an enclosing
// loop, and if so consumes it so its bare-loop base does not count as an escape.
// Such a read copies a scalar (or a neighbouring element Value) out of the loop
// value and retains no reference to the loop's pair snapshot, so it is pool-
// safe. Any structural or attribution failure returns false and the caller's
// generic walk lets the bare loop base apply the escaping default. It mirrors
// the compiled backend's tryChain minus the inline-lowering bookkeeping.
func (a *loopEscapeAnalyzer) safeLoopRead(n *ast.Node) bool {
	var fields []string
	cur := n
	for {
		if cur == nil {
			return false
		}
		switch cur.Kind {
		case ast.KindAttr:
			if cur.Bool {
				return false // a null-safe step is outside the recognized shape
			}
			fields = append(fields, cur.Str)
			cur = cur.Child(0)
		case ast.KindIndex:
			if cur.Bool || cur != n {
				// Only the terminal step may be a subscript; intermediate steps
				// must be dotted .parent.
				return false
			}
			key := cur.Child(1)
			if key == nil || key.Kind != ast.KindString {
				return false
			}
			fields = append(fields, key.Str)
			cur = cur.Child(0)
		case ast.KindName:
			if cur.Str != "loop" {
				return false
			}
			if len(fields) == 0 || !escInlineFields[fields[0]] {
				return false
			}
			for _, f := range fields[1:] {
				if f != "parent" {
					return false
				}
			}
			target, clean := a.attributeLoop()
			return target != nil && clean
		default:
			return false
		}
	}
}

// setRootOf walks a member-assignment path to its root name node, so a @set
// target rooted at loop can be recognized as a loop rebind.
func setRootOf(n *ast.Node) *ast.Node {
	for n != nil && (n.Kind == ast.KindAttr || n.Kind == ast.KindIndex) {
		n = n.Child(0)
	}
	return n
}

// exprBindsLoop sets *binds when an inline assignment inside a fused-filter
// condition binds the name loop, matching the interpreter's evaluation of that
// condition in a frame that could shadow loop. Arrow bodies bind in their own
// invocation frame and are skipped.
func exprBindsLoop(n *ast.Node, binds *bool) {
	if n == nil || n.Kind == ast.KindArrow {
		return
	}
	if n.Kind == ast.KindAssign {
		if tg := n.Child(0); tg != nil &&
			(tg.Kind == ast.KindName || tg.Kind == ast.KindTarget) && tg.Str == "loop" {
			*binds = true
		}
	}
	for _, ch := range n.Children {
		exprBindsLoop(ch, binds)
	}
}

// patternBindsLoop escapes the innermost loop when a destructuring @set target
// binds the name loop in any slot, mirroring walkSet's plain-target loop rebind
// rule for the destructuring form.
func patternBindsLoop(tg *ast.Node, a *loopEscapeAnalyzer) {
	switch tg.Kind {
	case ast.KindName, ast.KindTarget:
		if tg.Str == "loop" {
			a.escapeInnermost()
		}
	case ast.KindListPattern:
		for _, slot := range tg.Children {
			if slot == nil {
				continue
			}
			inner := slot
			switch slot.Kind {
			case ast.KindOptional, ast.KindSpread:
				inner = slot.Child(0)
			}
			patternBindsLoop(inner, a)
		}
	case ast.KindMapPattern:
		for _, slot := range tg.Children {
			if slot == nil || slot.Kind != ast.KindMapTarget {
				continue
			}
			local := slot.Str
			if slot.Bool {
				local = slot.Child(0).Str
			}
			if local == "loop" {
				a.escapeInnermost()
			}
		}
	}
}
