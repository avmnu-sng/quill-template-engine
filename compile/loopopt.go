package compile

import (
	"fmt"

	"github.com/avmnu-sng/quill-template-engine/ast"
)

// This file is the compiled loop optimizer (master plan Step 6): a compile-time
// escape analysis over every @for plus the emission helpers its results drive.
// A loop whose body provably never lets `loop` escape skips the per-iteration
// runtime.NewLoopValue allocation entirely: loop.<field> reads lower to inline
// arithmetic over the loop counter and pair slice, loop.prev/next to direct
// pair indexing with the runtime loopInfo's Null fallbacks, and loop.changed
// keeps its existing per-call-site locals. Any use the analysis cannot prove
// inline-able forces that loop to materialize the fresh per-iteration value
// exactly as the interpreter binds it, so frozen-capture semantics stay
// byte-identical.

// inlineLoopFields is the loop.* field set the backend lowers to arithmetic
// over (i, pairs), mirroring runtime's loopInfo.GetField minus parent (a chain
// step, not a terminal field), depth/depth0 (recursive-only, and recursive
// @for is outside the compilable subset), and changed (recognized as a call).
var inlineLoopFields = map[string]bool{
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

// inlineLoopRead is one approved loop field read: the @for node whose counter
// and pair slice the arithmetic reads (the end of a loop.parent chain when the
// read chases parents) and the terminal field name.
type inlineLoopRead struct {
	target *ast.Node
	field  string
}

// loopAnalysis is the escape-analysis result for one module: which @for nodes
// must materialize the per-iteration loop value, and which Attr/Index nodes
// lower to inline arithmetic instead of a value read.
type loopAnalysis struct {
	// analyzed records every @for node the walk visited, so an unvisited node
	// (a construct the analyzer missed) defensively materializes.
	analyzed map[*ast.Node]bool
	// materialized marks the @for nodes whose body lets `loop` escape, either
	// directly or through the outward parent-link propagation rule.
	materialized map[*ast.Node]bool
	// mutates marks the @for nodes whose body could mutate an array in place
	// (see bodyMutates), which forbids the live zero-copy iteration.
	mutates map[*ast.Node]bool
	// fused marks the @for nodes carrying a fused filter clause, whose
	// survivor pre-pass requires a materialized pair slice.
	fused map[*ast.Node]bool
	// inlineReads maps an approved read node to its inline lowering.
	inlineReads map[*ast.Node]inlineLoopRead
	// fors lists the analyzed @for nodes in source walk order for tests.
	fors []*ast.Node
}

// inlineFor reports whether the @for at n takes the inline lowering. Only a
// node the walk analyzed and proved non-escaping qualifies; anything else
// materializes, which is always semantics-preserving.
func (la *loopAnalysis) inlineFor(n *ast.Node) bool {
	return la != nil && la.analyzed[n] && !la.materialized[n]
}

// liveFor reports whether the @for at n may iterate a KArray iterand live off
// the array's insertion-ordered keys (runtime.Array.PairAt) instead of the
// materialized pair slice. The live path must be observationally equal to the
// interpreter's entry-time snapshot -- runtime.EnsureTraversable copies each
// element Value at loop entry, so an in-place mutation of the iterated array's
// top-level slots mid-loop is invisible to the iteration -- which holds only
// when the compiler can prove the body cannot mutate ANY array in place:
//
//   - The loop must not be fused: the @for..if pre-pass materializes the
//     survivor pair slice, so there is nothing to iterate live.
//   - The body must be mutation-free per bodyMutates: member assignment, @do,
//     and object-receiver method calls are the only template-reachable
//     in-place array mutators (host functions and filters receive Values by
//     the engine contract and are non-mutating, the same assumption the
//     interpreter makes when it hands pairs element Values out), so a body
//     without them cannot change the iterated array between entry and exit.
//   - The loop must also be non-escaping under the escape analysis: a
//     materialized loop constructs runtime.NewLoopValue over the pair slice
//     every iteration, so it needs the snapshot anyway.
//
// A live loop still emits the pairs fallback behind a runtime KArray check,
// because arrayness of the iterand is a runtime property.
func (la *loopAnalysis) liveFor(n *ast.Node) bool {
	return la.inlineFor(n) && !la.fused[n] && !la.mutates[n]
}

// aframeKind discriminates the analyzer's scope-model frames, mirroring the
// frames the lowering pushes for the same constructs.
type aframeKind int

const (
	afRoot aframeKind = iota
	afLoop
	afFilter
	afWith
	afWithOnly
	afArrow
)

// aframe is one analyzer scope frame. bindsLoop marks a non-loop frame that
// may bind the name loop itself (a fused-filter target or condition assign),
// which makes any reference resolving past it ambiguous.
type aframe struct {
	kind      aframeKind
	forNode   *ast.Node
	bindsLoop bool
}

// chainRead is one structurally approved loop.<field> read pending the
// materialization fixpoint: the chain lists the base loop and every loop a
// .parent step chases to, outermost target last.
type chainRead struct {
	node  *ast.Node
	chain []*ast.Node
	field string
}

// loopAnalyzer walks a module with a scope model just rich enough to resolve
// the name loop: which @for body a reference lands in, and whether anything
// between the reference and that body (a with frame, an arrow, a user binding
// of the name loop) makes the resolution unprovable.
type loopAnalyzer struct {
	res        *loopAnalysis
	reads      []chainRead
	parentOf   map[*ast.Node]*ast.Node
	stack      []aframe
	arrowDepth int
}

// analyzeLoops runs the escape analysis over a parsed module and returns the
// per-loop materialization decisions and the approved inline reads.
func analyzeLoops(mod *ast.Node) *loopAnalysis {
	a := &loopAnalyzer{
		res: &loopAnalysis{
			analyzed:     map[*ast.Node]bool{},
			materialized: map[*ast.Node]bool{},
			mutates:      map[*ast.Node]bool{},
			fused:        map[*ast.Node]bool{},
			inlineReads:  map[*ast.Node]inlineLoopRead{},
		},
		parentOf: map[*ast.Node]*ast.Node{},
		stack:    []aframe{{kind: afRoot}},
	}
	a.walkItems(mod.Children)
	a.fixpoint()
	return a.res
}

// fixpoint closes the materialization set under the two coupling rules and
// then approves the surviving chain reads. Rule one is the outward parent-link
// propagation: a materialized loop constructs runtime.NewLoopValue with a
// parent value, so the enclosing loop its parent link resolves to must
// materialize too. Rule two is the chain rule: a loop.parent chain read whose
// chain touches any materialized loop is no longer provably inline, so its
// base loop materializes (and rule one then pulls the rest of its chain).
func (a *loopAnalyzer) fixpoint() {
	res := a.res
	for changed := true; changed; {
		changed = false
		for _, n := range res.fors {
			if !res.materialized[n] {
				continue
			}
			if p := a.parentOf[n]; p != nil && !res.materialized[p] {
				res.materialized[p] = true
				changed = true
			}
		}
		for _, r := range a.reads {
			if res.materialized[r.chain[0]] {
				continue
			}
			for _, l := range r.chain[1:] {
				if res.materialized[l] {
					res.materialized[r.chain[0]] = true
					changed = true
					break
				}
			}
		}
	}
	for _, r := range a.reads {
		if !res.materialized[r.chain[0]] {
			res.inlineReads[r.node] = inlineLoopRead{
				target: r.chain[len(r.chain)-1],
				field:  r.field,
			}
		}
	}
}

// push adds one scope frame.
func (a *loopAnalyzer) push(f aframe) { a.stack = append(a.stack, f) }

// pop removes the innermost scope frame.
func (a *loopAnalyzer) pop() { a.stack = a.stack[:len(a.stack)-1] }

// mark materializes the loop at n.
func (a *loopAnalyzer) mark(n *ast.Node) {
	if n != nil {
		a.res.materialized[n] = true
	}
}

// escapeInnermost materializes the innermost lexically enclosing loop, if any.
// It deliberately ignores scope cuts: over-materializing is always safe, and
// the callers (a rebind of the name loop, a scope-enumerating construct) want
// the conservative answer.
func (a *loopAnalyzer) escapeInnermost() {
	for i := len(a.stack) - 1; i >= 0; i-- {
		if a.stack[i].kind == afLoop {
			a.mark(a.stack[i].forNode)
			return
		}
	}
}

// attributeLoop resolves a reference to the name loop at the current stack:
// the @for it lands on (nil when no loop is reachable, e.g. past an only-with
// cut) plus its stack index, and whether the resolution is provable. Crossing
// a with frame (its map may bind "loop" at runtime), an arrow body (the read
// happens at call time, possibly after the loop advanced or ended), or a
// filter frame that may bind the name loop makes the resolution unclean.
func (a *loopAnalyzer) attributeLoop() (target *ast.Node, idx int, clean bool) {
	clean = true
	for i := len(a.stack) - 1; i >= 0; i-- {
		switch f := a.stack[i]; f.kind {
		case afLoop:
			return f.forNode, i, clean
		case afWithOnly:
			// An only-with frame is a fresh scope root: the name loop cannot
			// resolve past it, so no loop is referenced at all.
			return nil, 0, false
		case afWith, afArrow:
			clean = false
		case afFilter:
			if f.bindsLoop {
				clean = false
			}
		case afRoot:
			return nil, 0, false
		}
	}
	return nil, 0, false
}

// chaseParent resolves one .parent step from the loop frame at fromIdx: the
// next enclosing loop, exactly where the lowering's parent probe would land.
// Anything the probe cannot statically skip (a with frame, a frame that may
// bind the name loop, a scope cut, running out of loops) fails the chase.
func (a *loopAnalyzer) chaseParent(fromIdx int) (target *ast.Node, idx int, ok bool) {
	for i := fromIdx - 1; i >= 0; i-- {
		switch f := a.stack[i]; f.kind {
		case afLoop:
			return f.forNode, i, true
		case afFilter:
			if f.bindsLoop {
				return nil, 0, false
			}
		default:
			return nil, 0, false
		}
	}
	return nil, 0, false
}

// nearestLoop finds the loop the parent link of a new @for resolves to at the
// current stack: the first enclosing loop frame, stopping at an only-with cut.
// With frames and possible user bindings do not stop the search: the runtime
// probe may still fall through to the loop's binding, so propagation must
// reach it (over-propagating only ever materializes more, never breaks).
func (a *loopAnalyzer) nearestLoop() *ast.Node {
	for i := len(a.stack) - 1; i >= 0; i-- {
		switch f := a.stack[i]; f.kind {
		case afLoop:
			return f.forNode
		case afWithOnly:
			return nil
		}
	}
	return nil
}

// walkItems walks a statement list.
func (a *loopAnalyzer) walkItems(items []*ast.Node) {
	for _, it := range items {
		a.walkStmt(it)
	}
}

// walkStmt walks one statement, mirroring the lowering's stmtItem dispatch.
// Constructs outside the compilable subset are skipped: compilation aborts on
// them before any analysis result is consumed.
func (a *loopAnalyzer) walkStmt(n *ast.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
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
		kind := afWith
		if n.Bool {
			kind = afWithOnly
		}
		a.push(aframe{kind: kind})
		a.walkItems(n.Children[1:])
		a.pop()
	case ast.KindTabBlock:
		a.walkExpr(n.Child(0), true)
		a.walkItems(n.Children[1:])
	case ast.KindEscape:
		a.walkItems(n.Children)
	default:
		// Text, checker-only declarations, and not-compilable statements hold
		// no analyzable loop references.
	}
}

// walkFor walks one @for with the interpreter's exact scoping: the iterand
// and the else body resolve in the enclosing frame, the fused filter runs in
// a frame that binds only the targets (never loop), and the body frame binds
// loop itself.
func (a *loopAnalyzer) walkFor(n *ast.Node) {
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

	a.res.analyzed[n] = true
	a.res.fors = append(a.res.fors, n)
	a.parentOf[n] = a.nearestLoop()
	if filter != nil {
		a.res.fused[n] = true
	}
	if body != nil && bodyMutates(body.Children) {
		a.res.mutates[n] = true
	}

	// A target named loop shadows the loop's own binding mid-bind; treat the
	// loop as escaping rather than reasoning about the overwrite order.
	targetsLoop := target1 != nil && target1.Str == "loop" ||
		target2 != nil && target2.Str == "loop"
	if targetsLoop {
		a.mark(n)
	}

	if filter != nil {
		binds := targetsLoop
		exprBinds(filter.Child(0), func(name string) {
			if name == "loop" {
				binds = true
			}
		})
		a.push(aframe{kind: afFilter, bindsLoop: binds})
		a.walkExpr(filter.Child(0), true)
		a.pop()
	}
	if body != nil {
		a.push(aframe{kind: afLoop, forNode: n})
		a.walkItems(body.Children)
		a.pop()
	}
	if elseBody != nil {
		a.walkItems(elseBody.Children)
	}
}

// walkSet walks one @set: a target that binds or rebinds the name loop (a
// plain target, a destructuring slot, or the privatize-and-rebind root of a
// member assignment) escapes the innermost loop; target index keys and the
// value expressions are ordinary expression positions.
func (a *loopAnalyzer) walkSet(n *ast.Node) {
	count := int(n.Int)
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
			if root := rootOf(tg); root != nil && root.Kind == ast.KindName && root.Str == "loop" {
				a.escapeInnermost()
			}
			for p := tg; p != nil && (p.Kind == ast.KindAttr || p.Kind == ast.KindIndex); p = p.Child(0) {
				if p.Kind == ast.KindIndex {
					a.walkExpr(p.Child(1), true)
				}
			}
		case ast.KindListPattern, ast.KindMapPattern:
			targetBinds(tg, func(name string) {
				if name == "loop" {
					a.escapeInnermost()
				}
			})
		}
	}
}

// walkExpr walks one expression. allowInline gates the approved-read
// recognition: inside an is defined operand the lowering decomposes access
// chains into presence probes rather than value reads, so a loop chain there
// is never approved and falls to the escaping default.
func (a *loopAnalyzer) walkExpr(n *ast.Node, allowInline bool) {
	if n == nil {
		return
	}
	switch n.Kind {
	case ast.KindName:
		if n.Str == "loop" {
			// A bare loop in any value position escapes: bound, passed,
			// compared, piped, emitted, or called.
			if target, _, _ := a.attributeLoop(); target != nil {
				a.mark(target)
			}
		}
	case ast.KindSpecialName:
		if n.Str == "_context" {
			// _context enumerates every visible name including loop, and its
			// loop entry must be the SAME object the body's loop reads (object
			// equality is pointer identity), so the innermost loop materializes.
			a.escapeInnermost()
		}
	case ast.KindAttr, ast.KindIndex:
		if allowInline && a.tryChain(n) {
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
			// loop.changed(expr) is recognized syntactically before the
			// receiver exists as a value; only its argument is a value position.
			for _, ch := range n.Children[1:] {
				if ch != nil && ch.Kind == ast.KindArg {
					a.walkExpr(ch.Child(0), allowInline)
				}
			}
			return
		}
		if a.arrowDepth > 0 && callee != nil && callee.Kind == ast.KindName {
			// A registry call lowers with a needs-context injection guard that
			// enumerates the scope; inside an arrow that guard runs at call
			// time, possibly after the loop advanced or ended, so the loop's
			// live binding must exist.
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
				// The key is a literal, never evaluated as a name.
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
		a.push(aframe{kind: afArrow})
		for _, ch := range n.Children {
			a.walkExpr(ch, allowInline)
		}
		a.pop()
		a.arrowDepth--
	case ast.KindParam:
		for _, ch := range n.Children {
			a.walkExpr(ch, allowInline)
		}
	default:
		for _, ch := range n.Children {
			a.walkExpr(ch, allowInline)
		}
	}
}

// tryChain recognizes and records an approved inline read rooted at n: a
// dotted (or string-literal subscript) terminal field from inlineLoopFields
// over zero or more dotted .parent steps over a bare loop, cleanly attributed
// with every .parent step landing on a real enclosing loop. It reports whether
// n was consumed; any structural or attribution failure reports false and the
// caller's generic walk lets the bare loop base apply the escaping default.
func (a *loopAnalyzer) tryChain(n *ast.Node) bool {
	// Peel from the outermost node down to the base name, collecting the
	// member steps outermost-first.
	var fields []string
	cur := n
	for {
		if cur == nil {
			return false
		}
		switch cur.Kind {
		case ast.KindAttr:
			if cur.Bool {
				return false // a null-safe step is outside the approved shape
			}
			fields = append(fields, cur.Str)
			cur = cur.Child(0)
		case ast.KindIndex:
			if cur.Bool || cur != n {
				// Only the terminal step may be a subscript; intermediate
				// steps must be dotted .parent.
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
			// fields[0] is the terminal field; every deeper step must chase a
			// parent link.
			if len(fields) == 0 || !inlineLoopFields[fields[0]] {
				return false
			}
			for _, f := range fields[1:] {
				if f != "parent" {
					return false
				}
			}
			target, idx, clean := a.attributeLoop()
			if target == nil || !clean {
				return false
			}
			chain := []*ast.Node{target}
			for range fields[1:] {
				var ok bool
				target, idx, ok = a.chaseParent(idx)
				if !ok {
					return false
				}
				chain = append(chain, target)
			}
			a.reads = append(a.reads, chainRead{node: n, chain: chain, field: fields[0]})
			return true
		default:
			return false
		}
	}
}

// ---- mutation-safety scan -------------------------------------------------------

// bodyMutates reports whether a loop body's statement list could mutate an
// array in place while the loop iterates. The scan is transitive over the
// whole body subtree -- nested statements, nested loops (including their
// iterands, filters, and else arms, all of which execute during the outer's
// iteration), capture bodies, arrow bodies defined inside, and every argument
// expression -- because a mutator anywhere under the body can run between two
// iterations of THIS loop. The triggers are the only template-reachable
// in-place array mutators: any member-assignment @set (whatever its root
// name resolves to, since aliasing cannot be ruled out statically), any @do
// statement, any method call on an object receiver (a host method may
// mutate host-held state such as a cell's inner array), and any bare-name
// call of attribute (the function spelling of a receiver-method call). Any
// doubt keeps the pair-snapshot path, which is always semantics-preserving.
//
// The scan leans on the non-mutating host-callable contract (see ext.Filter
// and ext.Function): the live path assumes a registered filter or function
// never mutates an argument *Array in place, because a callable's body is
// host Go code the scan cannot see. The same blindness applies to arrow
// indirection: a call of a name bound to an arrow is syntactically just a
// bare-name call, so when the arrow was DEFINED outside the loop body its
// body was never scanned as part of this loop, and a receiver-method call
// hiding inside it goes unseen. Both holes are closed by contract, not by
// analysis -- a host callable that mutates an argument array in place breaks
// live-path loops whose entry-time snapshot used to mask the write.
func bodyMutates(items []*ast.Node) bool {
	for _, it := range items {
		if nodeMutates(it) {
			return true
		}
	}
	return false
}

// nodeMutates reports whether the subtree at n contains a mutation trigger.
// The syntactic loop.changed(expr) form is exempt from the method-call
// trigger: the lowering (see exprCall) recognizes it before the receiver
// exists as a value, so it is loop metadata bookkeeping, not a receiver call;
// only its argument expressions stay in scope for the scan. A bare-name call
// of attribute triggers like a receiver-method call: ext's
// attribute(obj, "name", [args]) is the one FUNCTION spelling that reaches
// Object.CallMethod, so it can mutate host-held state the same way a dotted
// method call can. The name may resolve to something other than the stdlib
// function at runtime, but over-marking only costs the pairs path.
func nodeMutates(n *ast.Node) bool {
	if n == nil {
		return false
	}
	switch n.Kind {
	case ast.KindDo:
		return true
	case ast.KindSet:
		count := int(n.Int)
		for i := 0; i < count && i < len(n.Children); i++ {
			if setTargetMutates(n.Children[i]) {
				return true
			}
		}
	case ast.KindCall:
		callee := n.Child(0)
		if callee != nil && callee.Kind == ast.KindName && callee.Str == "attribute" {
			return true
		}
		if callee != nil && callee.Kind == ast.KindAttr {
			if callee.Str != "changed" || callee.Child(0) == nil ||
				callee.Child(0).Kind != ast.KindName || callee.Child(0).Str != "loop" {
				return true
			}
			for _, ch := range n.Children[1:] {
				if nodeMutates(ch) {
					return true
				}
			}
			return false
		}
	}
	for _, ch := range n.Children {
		if nodeMutates(ch) {
			return true
		}
	}
	return false
}

// setTargetMutates reports whether one @set target is a member assignment: a
// dotted or subscripted path (the in-place write form), directly or inside a
// destructuring pattern slot. Plain-name and pattern-name targets rebind, and
// a rebind never mutates the previously bound array in place.
func setTargetMutates(tg *ast.Node) bool {
	if tg == nil {
		return false
	}
	switch tg.Kind {
	case ast.KindAttr, ast.KindIndex:
		return true
	case ast.KindOptional:
		return setTargetMutates(tg.Child(0))
	case ast.KindListPattern:
		for _, slot := range tg.Children {
			if setTargetMutates(slot) {
				return true
			}
		}
	}
	return false
}

// ---- emission helpers ---------------------------------------------------------

// loopForNode returns the active loop lowering for the @for at n. Approved
// reads are lexically inside their target's body, so the target is always on
// the loop stack; a miss is a compiler bug.
func (c *compiler) loopForNode(n *ast.Node) *loopInfo {
	for i := len(c.loops) - 1; i >= 0; i-- {
		if c.loops[i].forNode == n {
			return c.loops[i]
		}
	}
	panic("compile: inline loop read outside its loop lowering")
}

// loopByFrame returns the active loop lowering owning compile frame f, or nil
// when f is not a loop frame currently being lowered.
func (c *compiler) loopByFrame(f *frame) *loopInfo {
	for i := len(c.loops) - 1; i >= 0; i-- {
		if c.loops[i].frame == f {
			return c.loops[i]
		}
	}
	return nil
}

// emitInlineLoopField lowers one approved loop field read to the exact value
// runtime's loopInfo.GetField computes for (i, pairs), with prev/next
// reproducing the boundary Null fallbacks. A live loop reads its length from
// the entry-time snapshot local and its neighbours through Array.PairAt on the
// array path (the fallback pair slice covers non-array iterands), which is
// value-identical to the pair-slice reads: the body is mutation-free by the
// live rule, so the array still holds the entry-time entries.
func (c *compiler) emitInlineLoopField(ir inlineLoopRead) string {
	li := c.loopForNode(ir.target)
	i, p := li.iVar, li.pairsVar
	length := fmt.Sprintf("len(%s)", p)
	if li.live {
		length = li.nVar
	}
	switch ir.field {
	case "index":
		return fmt.Sprintf("runtime.Int(int64(%s + 1))", i)
	case "index0":
		return fmt.Sprintf("runtime.Int(int64(%s))", i)
	case "revindex":
		return fmt.Sprintf("runtime.Int(int64(%s - %s))", length, i)
	case "revindex0":
		return fmt.Sprintf("runtime.Int(int64(%s - 1 - %s))", length, i)
	case "first":
		return fmt.Sprintf("runtime.Bool(%s == 0)", i)
	case "last":
		return fmt.Sprintf("runtime.Bool(%s == %s-1)", i, length)
	case "length":
		return fmt.Sprintf("runtime.Int(int64(%s))", length)
	case "prev":
		return c.emitLoopNeighbour(li, fmt.Sprintf("%s > 0", i), fmt.Sprintf("%s-1", i))
	case "next":
		return c.emitLoopNeighbour(li, fmt.Sprintf("%s < %s-1", i, length), fmt.Sprintf("%s+1", i))
	default:
		// The analyzer approves only the fields above.
		panic(fmt.Sprintf("compile: unknown inline loop field %q", ir.field))
	}
}

// emitLoopNeighbour lowers a loop.prev / loop.next read: the element at the
// neighbouring position when cond holds, else the runtime loopInfo's Null
// fallback. It returns the value local's name.
func (c *compiler) emitLoopNeighbour(li *loopInfo, cond, at string) string {
	v := c.tmp("qt")
	c.linef("%s := runtime.Null()", v)
	c.openf("if %s {", cond)
	if li.live {
		c.openf("if %s != nil {", li.arrVar)
		c.linef("_, %s = %s.PairAt(%s)", v, li.arrVar, at)
		c.ind--
		c.linef("} else {")
		c.ind++
		c.linef("%s = %s[%s].Val", v, li.pairsVar, at)
		c.closeb()
	} else {
		c.linef("%s = %s[%s].Val", v, li.pairsVar, at)
	}
	c.closeb()
	return v
}

// emitLoopValue materializes an inline loop's metadata object on demand for a
// scope-enumerating consumer (the needs-context injection): the same fresh
// runtime.NewLoopValue the interpreter binds, built only inside the consumer's
// guard so the steady-state iteration stays allocation-free. A live loop has
// no pair slice on its array path, so the loop object's pairs materialize here
// on demand too; the body is mutation-free by the live rule, so Pairs() at any
// iteration equals the entry-time snapshot the interpreter's object holds. The
// parent is NOT resolved here: it is the loop's hoisted entry-time probe
// local, because the interpreter probes pre.Get("loop") once before iterating
// and a consumption-time re-probe could observe a scope entry named loop that
// changed mid-loop. It returns the value expression.
func (c *compiler) emitLoopValue(li *loopInfo) string {
	pairs := li.pairsVar
	if li.live {
		ps := c.tmp("qp")
		c.linef("%s := %s", ps, li.pairsVar)
		c.openf("if %s != nil {", li.arrVar)
		c.linef("%s = %s.Pairs()", ps, li.arrVar)
		c.closeb()
		pairs = ps
	}
	return fmt.Sprintf("runtime.NewLoopValue(%s, %s, %s)", li.iVar, pairs, li.parentVar)
}

// emitLoopParent lowers the entry-time parent probe of one inline loop: the
// exact pre.Get("loop") resolution the interpreter runs once at loop entry,
// walked over the compile frames below top (the loop's own frame is not yet
// pushed when its entry code is emitted). An enclosing loop frame is terminal
// (its loop is always bound at runtime): a materialized one yields its live
// binding, an inline one materializes from its own hoisted entry-time locals.
// With frames and user bindings of the name loop keep their runtime-guarded
// steps. The result local may go unconsumed when nothing in the body
// materializes the loop value, so it is blank-assigned defensively.
func (c *compiler) emitLoopParent(top int) string {
	val := c.tmp("qt")
	found := c.tmp("qf")
	c.linef("var %s runtime.Value", val)
	c.linef("%s := false", found)
	for i := top - 1; i >= 0; i-- {
		f := c.frames[i]
		if b, ok := f.byName["loop"]; ok {
			if f.kind == frameLoop {
				gi := c.loopByFrame(f)
				c.openf("if !%s {", found)
				if gi != nil && gi.inline {
					c.linef("%s = %s", val, c.emitLoopValue(gi))
				} else {
					c.linef("%s = runtime.ShareValue(%s)", val, b.val)
				}
				c.linef("%s = true", found)
				c.closeb()
				c.linef("_, _ = %s, %s", val, found)
				return val
			}
			if b.definite {
				c.openf("if !%s {", found)
				c.linef("%s = runtime.ShareValue(%s)", val, b.val)
				c.linef("%s = true", found)
				c.closeb()
				c.linef("_, _ = %s, %s", val, found)
				return val
			}
			c.openf("if !%s && %s {", found, b.flag)
			c.linef("%s = runtime.ShareValue(%s)", val, b.val)
			c.linef("%s = true", found)
			c.closeb()
		}
		switch f.kind {
		case frameWith, frameWithOnly:
			c.openf("if !%s && %s.Kind == runtime.KArray && %s.Arr != nil {", found, f.withVar, f.withVar)
			inner := c.tmp("qt")
			ok := c.tmp("qk")
			c.openf("if %s, %s := %s.Arr.GetStr(%s); %s {", inner, ok, f.withVar, q("loop"), ok)
			c.linef("%s = runtime.ShareValue(%s)", val, inner)
			c.linef("%s = true", found)
			c.closeb()
			c.closeb()
			if f.kind == frameWithOnly {
				c.linef("_, _ = %s, %s", val, found)
				return val
			}
		case frameRoot:
			inner := c.tmp("qt")
			ok := c.tmp("qk")
			c.openf("if !%s {", found)
			c.openf("if %s, %s := vars[%s]; %s {", inner, ok, q("loop"), ok)
			c.linef("%s = runtime.ShareValue(%s)", val, inner)
			c.linef("%s = true", found)
			c.closeb()
			c.closeb()
		}
	}
	c.linef("_, _ = %s, %s", val, found)
	return val
}
