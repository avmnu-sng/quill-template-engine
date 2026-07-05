package interp

import (
	"math"
	"strings"
	"sync"

	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/cover"
	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// renderTemplate renders a template, resolving inheritance first. If the
// template extends a parent, the merged block table is built bottom-up (most-
// derived definitions win) and the TOPMOST parent's body is rendered, with each
// @block site delegating to the resolved definition (spec 01 Section 5.2).
// Otherwise the template's own body is rendered top to bottom. The coverage
// seeding and the Phase-1 sandbox check run per render, over the composed
// chain, whether composeTemplate built it fresh or served it from the memo.
func (in *interp) renderTemplate(tmpl *Template, ctx *runtime.Scope) error {
	chain, err := in.composeTemplate(tmpl, ctx)
	if err != nil {
		return err
	}

	// Coverage seeding: register every coverable region of each template in the
	// inheritance chain as a zero-count region before any output, so unreached
	// code counts against the denominator. Seeding is idempotent per template
	// name, so re-seeding across renders or across a shared parent is a no-op. A
	// nil Collector short-circuits inside covSeed (zero overhead when disabled).
	for _, t := range chain {
		in.covSeed(t)
	}

	// Phase-1 sandbox check (B9): when the sandbox is active for this render,
	// validate every statement keyword, filter, and function used across the
	// template's inheritance chain against the policy in one pass, before any
	// output. Macro names are resolved by now, so a macro callee is correctly
	// skipped. Each template in the chain contributes its own collected set.
	if in.sandboxOn {
		for _, t := range chain {
			if err := in.checkSecurity(t.used); err != nil {
				return err
			}
		}
	}

	// Render the topmost template's body: a non-inheriting template renders
	// itself; an inheriting chain renders the root parent, whose @block sites
	// pull from the merged table.
	top := chain[len(chain)-1]
	return in.execItems(top.Module.Children, ctx)
}

// composeTemplate installs the render-ready composition of tmpl on this interp
// -- the inheritance chain, the merged block table, the macro namespace, the
// literal-regexp lookup, and the @import namespace scope binds -- and returns
// the chain. A Template whose chain proved fully static serves all of that
// from its one-time memo: the shared tables are read-only after build (no
// renderer code writes a block or macro entry past this point, and @embed
// layers its overrides onto a fresh table in a fresh sub-interp), so only the
// @import scope binds -- composition's single per-render side effect -- are
// replayed into ctx. The first render of a static chain builds fresh, then
// publishes the tables it just built; the walk's 64-deep cycle check thus runs
// during the memo build, and a memoized chain needs no re-check. A chain with
// any dynamic member (compStatic false) rebuilds per render, bit-for-bit
// today's path, so render-time expressions keep deciding what @extends and
// @import resolve to.
func (in *interp) composeTemplate(tmpl *Template, ctx *runtime.Scope) ([]*Template, error) {
	if c := tmpl.comp.Load(); c != nil {
		in.parentChain = c.chain
		in.blocks = c.blocks
		in.macros = c.macros
		in.regexps = c.regexps
		for _, b := range c.nsBinds {
			ctx.Set(b.name, runtime.Obj(&importNS{tmpl: b.tmpl}))
		}
		return c.chain, nil
	}
	chain, err := in.buildChain(tmpl, ctx)
	if err != nil {
		return nil, err
	}
	in.parentChain = chain
	if err := in.buildBlockTable(chain); err != nil {
		return nil, err
	}
	binds := in.loadMacros(tmpl, ctx)
	if compositionStatic(chain) {
		tmpl.comp.Store(&composition{
			chain:   chain,
			blocks:  in.blocks,
			macros:  in.macros,
			nsBinds: binds,
			regexps: in.regexps,
		})
	}
	return chain, nil
}

// buildChain resolves the inheritance chain from tmpl up to its topmost ancestor
// (index 0 most-derived, last least-derived). A non-inheriting template yields a
// one-element chain. The extends operand is a string-coerced expression or a
// candidate list (first existing wins), spec 01 Section 5.2.
func (in *interp) buildChain(tmpl *Template, ctx *runtime.Scope) ([]*Template, error) {
	chain := []*Template{tmpl}
	cur := tmpl
	for cur.extendsNode != nil {
		parentName, err := in.resolveExtendsName(cur.extendsNode, ctx)
		if err != nil {
			return nil, err
		}
		parent, err := in.eng.LoadTemplate(parentName)
		if err != nil {
			return nil, posErr(cur.extendsNode, err)
		}
		chain = append(chain, parent)
		in.absorb(parent)
		cur = parent
		if len(chain) > 64 {
			return nil, errors.New(errors.KindRuntime, "inheritance chain too deep (cycle?)")
		}
	}
	return chain, nil
}

// resolveExtendsName evaluates the @extends operand to a template name, handling
// a candidate list (the first existing template wins).
func (in *interp) resolveExtendsName(extends *ast.Node, ctx *runtime.Scope) (string, error) {
	v, err := in.eval(extends.Child(0), ctx, false)
	if err != nil {
		return "", err
	}
	if v.Kind == runtime.KArray && v.Arr != nil {
		for _, p := range v.Arr.Pairs() {
			name, err := runtime.ToText(p.Val)
			if err != nil {
				return "", err
			}
			if in.eng.TemplateExists(name) {
				return name, nil
			}
		}
		return "", posErr(extends, errors.New(errors.KindRuntime,
			"none of the candidate parent templates exist"))
	}
	return runtime.ToText(v)
}

// buildBlockTable merges the chain's block definitions into in.blocks. The chain
// is most-derived first, so the first definition seen for a name is the override
// that wins; the full ordered list of definitions for the name (most-derived
// first) is recorded so parent() can render the next one up (design/composition
// Section 2.5). For each template in the chain its OWN blocks merge before the
// blocks it pulls in via @use, so a template's own definition wins over a trait's
// and parent() reaches the trait version before the extends-parent version (spec
// 01 Section 5.4) -- it returns an error if a @use target is missing or not
// traitable, or an alias names a block the trait does not define. The table
// itself materializes inside appendBlockDef on the first definition, so a
// chain that defines no blocks renders against a nil table.
func (in *interp) buildBlockTable(chain []*Template) error {
	for _, t := range chain {
		// A template's own block definitions take precedence over any trait blocks
		// it uses, so own defs are merged first; traits follow in source order.
		for _, name := range t.BlockNames() {
			node, _ := t.Block(name)
			in.appendBlockDef(name, blockDef{owner: t, node: node})
		}
		if err := in.mergeTraits(t); err != nil {
			return err
		}
	}
	return nil
}

// appendBlockDef records one definition for a block name: it becomes the entry's
// most-derived definition when the name is new, otherwise it is appended to the
// existing definition chain (so parent() walks to it). The first definition of
// the render creates the block table, keeping a blockless render map-free.
func (in *interp) appendBlockDef(name string, def blockDef) {
	if e, ok := in.blocks[name]; ok {
		e.chain = append(e.chain, def)
		return
	}
	if in.blocks == nil {
		in.blocks = map[string]*blockEntry{}
	}
	in.blocks[name] = &blockEntry{owner: def.owner, node: def.node, chain: []blockDef{def}}
}

// mergeTraits pulls the blocks of every template t uses (@use) into the table,
// below t's own definitions and in source order. Aliasing ({trait: alias}) binds
// the trait's block under the alias name, so the using template can override it
// by that name and parent() on the alias reaches the trait's original block.
func (in *interp) mergeTraits(t *Template) error {
	for _, use := range t.uses {
		traitName, ok := in.useTargetName(use)
		if !ok {
			return posErr(use, errors.New(errors.KindRuntime,
				"a use target must be a constant string"))
		}
		trait, err := in.eng.LoadTemplate(traitName)
		if err != nil {
			return posErr(use, err)
		}
		if !trait.Traitable() {
			return posErr(use, errors.New(errors.KindRuntime,
				"template %q cannot be used as a trait", traitName))
		}
		in.absorb(trait)
		// A trait may itself @use other traits; flatten those first so its block
		// table reflects the full bundle (later own blocks still win).
		if err := in.mergeTraits(trait); err != nil {
			return err
		}
		aliases, err := in.useAliases(use)
		if err != nil {
			return err
		}
		for _, name := range trait.BlockNames() {
			node, _ := trait.Block(name)
			local := name
			if a, ok := aliases[name]; ok {
				local = a
			}
			in.appendBlockDef(local, blockDef{owner: trait, node: node})
		}
		// Every alias must name a block the trait actually defines.
		for orig := range aliases {
			if !trait.HasBlock(orig) {
				return posErr(use, errors.New(errors.KindRuntime,
					"block %q is not defined in trait %q", orig, traitName))
			}
		}
	}
	return nil
}

// useTargetName extracts a @use target, which must be a constant string literal
// (no dynamic trait names, spec 01 Section 5.4).
func (in *interp) useTargetName(use *ast.Node) (string, bool) {
	src := use.Child(0)
	if src == nil || src.Kind != ast.KindString {
		return "", false
	}
	return src.Str, true
}

// useAliases reads the optional "with { trait: alias }" rename map of a @use,
// returning a map from the trait's original block name to its local alias.
func (in *interp) useAliases(use *ast.Node) (map[string]string, error) {
	aliases := map[string]string{}
	if !use.Bool { // no with-map
		return aliases, nil
	}
	mapNode := use.Child(1)
	for _, entry := range mapNode.Children {
		switch entry.Int {
		case ast.MapEntryKeyed:
			key := entry.Child(0)   // KindString trait block name
			alias := entry.Child(1) // alias value
			if alias.Kind != ast.KindName && alias.Kind != ast.KindString {
				return nil, posErr(use, errors.New(errors.KindRuntime,
					"a trait alias must be a bare name or string"))
			}
			aliases[key.Str] = alias.Str
		case ast.MapEntryShorthand:
			name := entry.Child(0).Str
			aliases[name] = name
		default:
			return nil, posErr(use, errors.New(errors.KindRuntime,
				"invalid trait alias entry"))
		}
	}
	return aliases, nil
}

// loadMacros populates the macro namespace: the root template's own macros plus
// any brought in by file-scope @import (namespace) and @from (selective). A
// macro's lexical home (for its own visible namespace and globals) is recorded
// (spec 01 Section 5.3). The namespace map materializes only when a macro
// actually binds -- here for declared macros, sized for them, or inside
// loadImport for a selective @from -- so a macro-free render keeps it nil. It
// returns the @import namespace binds it applied, in source order, so a static
// composition build can replay exactly those scope writes per render; @from
// contributes no scope bind (it folds into the macro namespace map, which the
// memo captures whole).
func (in *interp) loadMacros(tmpl *Template, ctx *runtime.Scope) []nsBind {
	if len(tmpl.macroOrder) > 0 {
		in.macros = make(map[string]*macroEntry, len(tmpl.macroOrder))
		for _, name := range tmpl.macroOrder {
			node, _ := tmpl.Macro(name)
			in.macros[name] = &macroEntry{home: tmpl, node: node}
		}
	}
	var binds []nsBind
	for _, imp := range tmpl.imports {
		if b, ok := in.loadImport(imp, ctx); ok {
			binds = append(binds, b)
		}
	}
	return binds
}

// execItems renders a run of body items in order.
func (in *interp) execItems(items []*ast.Node, ctx *runtime.Scope) error {
	for _, item := range items {
		if err := in.execItem(item, ctx); err != nil {
			return err
		}
	}
	return nil
}

// execItem renders one item: text, interpolation, a control statement, or a
// composition head. Composition heads that only declare (macro, import, extends)
// emit nothing; @block renders its resolved definition in place.
func (in *interp) execItem(n *ast.Node, ctx *runtime.Scope) error {
	switch n.Kind {
	case ast.KindText:
		in.covUnit(n, cover.UnitText)
		return in.emitString(n.Str)
	case ast.KindVerbatim:
		in.covUnit(n, cover.UnitText)
		return in.emitString(n.Str)
	case ast.KindPrint:
		in.covUnit(n, cover.UnitPrint)
		return in.execPrint(n, ctx)
	case ast.KindIf:
		in.covUnit(n, cover.UnitIf)
		return in.execIf(n, ctx)
	case ast.KindFor:
		in.covUnit(n, cover.UnitFor)
		return in.execFor(n, ctx)
	case ast.KindSet:
		in.covUnit(n, cover.UnitSet)
		return in.execSet(n, ctx)
	case ast.KindCapture:
		in.covUnit(n, cover.UnitSet)
		return in.execCapture(n, ctx)
	case ast.KindWith:
		in.covUnit(n, cover.UnitWith)
		return in.execWith(n, ctx)
	case ast.KindApply:
		in.covUnit(n, cover.UnitApply)
		return in.execApply(n, ctx)
	case ast.KindDo:
		in.covUnit(n, cover.UnitDo)
		_, err := in.eval(n.Child(0), ctx, false)
		return err
	case ast.KindFlush:
		// A no-op for every string sink (spec 01 Section 4.4); under a streaming
		// RenderTo it flushes a flushable destination writer (see interp.flush).
		return in.flush()
	case ast.KindEscape:
		in.covUnit(n, cover.UnitEscape)
		return in.execEscape(n, ctx)
	case ast.KindGuard:
		in.covUnit(n, cover.UnitGuardTag)
		return in.execGuard(n, ctx)
	case ast.KindBlock:
		// The block UNIT is recorded in renderBlockBody at the resolved definition,
		// so an overriding child block counts under its own template even though the
		// dispatch here is the parent's site node.
		return in.execBlockSite(n, ctx)
	case ast.KindInclude:
		in.covUnit(n, cover.UnitInclude)
		return in.execInclude(n, ctx)
	case ast.KindEmbed:
		in.covUnit(n, cover.UnitEmbed)
		return in.execEmbed(n, ctx)
	case ast.KindExtends, ast.KindMacro, ast.KindImport, ast.KindFrom, ast.KindUse:
		return nil // declarations: no direct output
	case ast.KindCache:
		in.covUnit(n, cover.UnitCache)
		return in.execCache(n, ctx)
	case ast.KindSandbox:
		in.covUnit(n, cover.UnitSandbox)
		return in.execSandbox(n, ctx)
	case ast.KindLog:
		in.covUnit(n, cover.UnitLog)
		return in.execLog(n, ctx)
	case ast.KindTabBlock:
		in.covUnit(n, cover.UnitTabBlock)
		return in.execTabBlock(n, ctx)
	case ast.KindProvide:
		in.covUnit(n, cover.UnitProvide)
		return in.execProvide(n, ctx)
	case ast.KindYield:
		in.covUnit(n, cover.UnitYield)
		return in.execYield(n, ctx)
	case ast.KindCallBlock:
		in.covUnit(n, cover.UnitCallBlock)
		return in.execCallBlock(n, ctx)
	case ast.KindTypes, ast.KindDeprecated, ast.KindLine:
		// Type declarations, deprecation diagnostics, and line resets are parsed but
		// their runtime effects are deferred this slice; they emit nothing.
		return nil
	default:
		return posErr(n, errors.New(errors.KindRuntime,
			"cannot render %s in this milestone", n.Kind))
	}
}

// execPrint evaluates and emits an interpolation.
func (in *interp) execPrint(n *ast.Node, ctx *runtime.Scope) error {
	v, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return err
	}
	return posErr(n, in.emit(v))
}

// execIf renders the first clause whose condition is truthy, else the else
// clause if present (spec 01 Section 4.1). A clause body runs in the current
// scope (an if introduces no new scope).
func (in *interp) execIf(n *ast.Node, ctx *runtime.Scope) error {
	for _, clause := range n.Children {
		if clause.Bool { // if / elseif: child 0 condition, rest body
			cond, err := in.eval(clause.Child(0), ctx, false)
			if err != nil {
				return err
			}
			if runtime.Truthy(cond) {
				// This clause's condition was truthy: its body runs (the then arm).
				in.covArm(clause, cover.IfThen)
				return in.execItems(clause.Children[1:], ctx)
			}
			// The condition evaluated false: record the not-taken arm and fall to the
			// next clause (an elseif condition or the terminal else).
			in.covArm(clause, cover.IfNotTaken)
		} else { // else: all children are body
			in.covArm(clause, cover.IfElse)
			return in.execItems(clause.Children, ctx)
		}
	}
	return nil
}

// nullLoopParent is the shared parent pointee of every loop with no enclosing
// loop value: its loop.parent reads Null, so all such loops point at this one
// read-only package-level Value instead of heap-boxing a fresh Null per loop.
var nullLoopParent = runtime.Null()

// probeLoopParent resolves the enclosing loop's value once at loop entry and
// returns the pointer every iteration's metadata object stores as its parent
// (the pointer keeps runtime's loopInfo in the 64-byte size class). A hit is
// boxed into a dedicated heap Value that stays unwritten for the loop's
// lifetime -- a rebind would leak into already-captured snapshots -- and a
// miss shares nullLoopParent. The box is built by explicit new-and-copy on the
// hit path only: taking the probe local's address would make escape analysis
// heap-allocate it on every call, charging top-level loops too.
func probeLoopParent(pre *runtime.Scope) *runtime.Value {
	v, ok := pre.Get("loop")
	if !ok {
		return &nullLoopParent
	}
	parent := new(runtime.Value)
	*parent = v
	return parent
}

// poolLoopSnapshots is the master switch for gate-scoped loop snapshot pooling.
// It is true in every build; the differential test battery flips it off to
// render the exact same templates through the fresh-Pairs() path and assert the
// two renders are byte-identical, proving pooling is a pure buffer-reuse. It is
// read once per loop and must only be toggled between renders (never during
// one), which the single-threaded byte-diff tests observe.
var poolLoopSnapshots = true

// reuseLoopInfo is the master switch for gate-scoped per-iteration loop-value
// reuse. It is true in every build; the differential battery flips it off to
// render the same templates through the fresh-per-iteration NewLoopValue path
// and assert byte-identical output, proving reuse is a pure allocation elision
// under the escape gate. Like poolLoopSnapshots it is read once per loop and
// only ever toggled between renders. It is a separate knob so the battery can
// isolate loop-value reuse from pair-buffer pooling.
var reuseLoopInfo = true

// pairBufPool recycles []Pair loop snapshot buffers across renders. It is a
// sync.Pool, so it is goroutine-safe (each P holds its own shard, so concurrent
// renders never contend) and it survives render boundaries -- which is where the
// win is: a template rendered repeatedly reuses one steady-state buffer instead
// of allocating the O(rows) snapshot every render. A pointer to the header is
// pooled so a Get/Put pair boxes nothing. GC may drain the pool under pressure,
// bounding the *Array pointers a parked buffer's tail retains.
//
// Within one render the pool still hands distinct buffers to nested loops: an
// enclosing loop holds its buffer (it is not Put back) while an inner loop Gets
// a different one, so the inner never overwrites the slice the outer is ranging
// over. The acquire/release discipline is a defer per loop, so error and panic
// paths return the buffer too.
var pairBufPool = sync.Pool{New: func() any { return new([]runtime.Pair) }}

// acquirePairBuf takes a snapshot buffer from the shared pool, pre-sized to hold
// n pairs. A pooled buffer with enough capacity is reused as-is (truncated to
// zero by PairsInto); one too small, or the pool's fresh zero-length slice, is
// grown to n in a single allocation so the caller's PairsInto never pays the
// append doubling ladder. It returns the pointer the pool owns and the buffer to
// materialize into.
func acquirePairBuf(n int) (*[]runtime.Pair, []runtime.Pair) {
	bp := pairBufPool.Get().(*[]runtime.Pair)
	buf := *bp
	if cap(buf) < n {
		buf = make([]runtime.Pair, 0, n)
	}
	return bp, buf
}

// releasePairBuf returns a snapshot buffer to the shared pool for the next
// render or sibling loop to reuse, storing the grown slice back through the
// pooled pointer so its capacity survives. The buffer is NOT wiped: a parked
// buffer retains the *Array pointers of its tail elements past len until the
// next PairsInto overwrites them or the pool is drained, a bounded retention
// (the largest pooled loop's pair count) that wiping on every release would
// trade for the O(n) cost pooling exists to remove.
func releasePairBuf(bp *[]runtime.Pair, buf []runtime.Pair) {
	*bp = buf
	pairBufPool.Put(bp)
}

// execFor renders a for loop with full loop.* metadata (spec 01 Section 4.2).
// The iterand is drained to pairs; a non-iterable is a runtime error (NOT a
// silent empty loop) unless lenient mode is on. The body runs in a child scope;
// reassignments to pre-existing names persist, body-local sets do not leak.
func (in *interp) execFor(n *ast.Node, ctx *runtime.Scope) error {
	count := int(n.Int & ast.ForTargetCount)
	target1 := n.Child(0)
	var target2 *ast.Node
	idx := 1
	if count == 2 {
		target2 = n.Child(1)
		idx = 2
	}
	iterand := n.Child(idx)

	// A fused filter clause (KindClause) may sit between the iterand and the body.
	// The body is the next KindBody; an optional trailing KindBody is the else.
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

	// The recursive form binds a loop(children) descent callable and loop.depth /
	// loop.depth0. A fused "if" filter, when present, is honored at every descent
	// level so a hidden node and its subtree are pruned uniformly.
	if n.Int&ast.ForRecursive != 0 {
		return in.execRecursiveFor(n, ctx, target1, target2, iterand, body, elseBody, filter)
	}

	collVal, err := in.eval(iterand, ctx, false)
	if err != nil {
		return err
	}

	// Materialize the entry-time pair snapshot. A plain (non-fused) KArray loop
	// whose Prepare-time escape bit proved its loop value never outlives the
	// iteration recycles a pooled buffer via PairsInto: the snapshot is fully
	// materialized exactly as EnsureTraversable's Pairs() would be -- same
	// entries, same order, so iteration and rendered bytes are identical -- but
	// in memory reused across renders and sibling loops. The deferred release
	// returns the buffer on the normal and error returns, so a body error never
	// leaks it; a genuine panic aborts the whole render and merely drops the
	// buffer (the pool refills), which is harmless. A fused loop (its survivors
	// slice is a separate allocation), a KObject Iterable, or any escaping or
	// non-array iterand keeps today's fresh path.
	var pairs []runtime.Pair
	if poolLoopSnapshots && filter == nil && in.forSafe[n] && collVal.Kind == runtime.KArray && collVal.Arr != nil {
		bp, buf := acquirePairBuf(collVal.Arr.Len())
		pairs = collVal.Arr.PairsInto(buf)
		// The pooled path never reruns the filter block, so pairs is final here;
		// deferring the direct call (not a closure) captures it now and avoids a
		// per-loop closure heap allocation.
		defer releasePairBuf(bp, pairs)
	} else {
		pairs, err = runtime.EnsureTraversable(collVal, !in.eng.StrictVariables())
		if err != nil {
			return posErr(n, err)
		}
	}

	// Push a fresh loop.changed(...) memory frame for this loop and pop it when the
	// loop ends, so the innermost loop answers changed(...) and a nested loop keeps
	// its own prior-value memory (spec 01 Section 4.2). The frame is established
	// before the fused filter runs, so a loop.changed(...) call in the filter
	// condition resolves against this loop's own frame -- tracking each candidate
	// element on this loop's own iteration -- rather than the enclosing loop's
	// frame. The filter's call site and any body call site are distinct AST nodes,
	// so they track independently within the shared frame.
	in.loopChanged = append(in.loopChanged, map[*ast.Node]runtime.Value{})
	defer func() { in.loopChanged = in.loopChanged[:len(in.loopChanged)-1] }()

	if filter != nil {
		pairs, err = in.filterLoopPairs(filter, pairs, target1, target2, ctx)
		if err != nil {
			return err
		}
	}
	if len(pairs) == 0 {
		// The collection drained to zero pairs: the empty arm is taken (its @else
		// body, or nothing, runs).
		in.covArm(n, cover.ForEmpty)
		if elseBody != nil {
			return in.execItems(elseBody.Children, ctx)
		}
		return nil
	}
	// At least one pair: the loop body runs (the body arm).
	in.covArm(n, cover.ForBody)

	// Child scope: clone the context so body-local sets do not leak, but copy
	// back reassignments of pre-existing names after the loop (lexical scoping,
	// spec 01 Section 4.2).
	pre := ctx
	loopCtx := pre.Child()
	parentPtr := probeLoopParent(pre)

	// A loop whose Prepare-time escape bit cleared binds ONE reused loop object,
	// advancing only its index each iteration, instead of allocating a fresh
	// metadata object per element (the dominant remaining per-iteration byte term
	// once pair pooling removed the snapshot). This is sound precisely because the
	// escape proof guarantees no earlier step's loop value is still reachable to
	// observe the in-place index advance; a loop the analysis could not clear --
	// one that may capture loop, pass it to a callable, or read it after the step
	// -- keeps the fresh NewLoopValue path and its frozen-snapshot contract. The
	// fused guard is redundant with forSafe (analyzeLoopEscapes never marks a fused
	// loop safe) but kept explicit as the pair path keeps it.
	var cursor *runtime.LoopCursor
	if reuseLoopInfo && filter == nil && in.forSafe[n] {
		cursor = runtime.NewLoopCursor(pairs, parentPtr)
	}

	for i, p := range pairs {
		loopCtx.Set(target1.Str, p.Val)
		if target2 != nil {
			// for k, v: target1 binds the value, target2... per spec the first target
			// is the value in single-target form; in two-target form target1 is the
			// KEY and target2 is the value (for k, v in mapping). Bind accordingly.
			loopCtx.Set(target1.Str, p.Key)
			loopCtx.Set(target2.Str, p.Val)
		}
		if cursor != nil {
			loopCtx.Set("loop", cursor.At(i))
		} else {
			loopCtx.Set("loop", runtime.NewLoopValue(i, pairs, parentPtr))
		}
		if err := in.execItems(body.Children, loopCtx); err != nil {
			return err
		}
	}
	// Propagate reassignments of names that existed before the loop, but NEVER the
	// loop's own control bindings (the target(s) and `loop`). Those are scoped to
	// this loop; copying them back would clobber an enclosing loop's `loop`
	// metadata or target with this inner loop's last value (spec 01 Section 4.2:
	// loop metadata reflects the CURRENT loop after an inner loop returns).
	bound := map[string]bool{target1.Str: true, "loop": true}
	if target2 != nil {
		bound[target2.Str] = true
	}
	for _, name := range pre.Names() {
		if bound[name] {
			continue
		}
		if v, ok := loopCtx.Get(name); ok {
			pre.Set(name, v)
		}
	}
	return nil
}

// filterLoopPairs pre-selects the pairs whose fused filter condition is truthy,
// so the loop body runs only over the survivors and every loop.* field counts
// only them (spec 01 Section 4.2, the @for..if form). The condition is evaluated
// in a child scope with the loop target(s) bound to each candidate element, so it
// may reference the loop variable(s); its own bindings do not leak. A single
// target binds the value; two targets bind the key and value like the loop body.
func (in *interp) filterLoopPairs(filter *ast.Node, pairs []runtime.Pair, target1, target2 *ast.Node, ctx *runtime.Scope) ([]runtime.Pair, error) {
	cond := filter.Child(0)
	scope := ctx.Child()
	survivors := make([]runtime.Pair, 0, len(pairs))
	for _, p := range pairs {
		scope.Set(target1.Str, p.Val)
		if target2 != nil {
			scope.Set(target1.Str, p.Key)
			scope.Set(target2.Str, p.Val)
		}
		keep, err := in.eval(cond, scope, false)
		if err != nil {
			return nil, err
		}
		if runtime.Truthy(keep) {
			survivors = append(survivors, p)
		}
	}
	return survivors, nil
}

// execSet binds one or more targets to one or more values (spec 01 Section 4.3).
// Multi-target and destructuring forms are bound positionally; a type annotation
// is ignored at render time (the checker consumes it).
func (in *interp) execSet(n *ast.Node, ctx *runtime.Scope) error {
	count := int(n.Int)
	targets := n.Children[:count]
	values := n.Children[count:]

	// Single destructuring target with a single value: [a, b] = pair.
	if count == 1 && (targets[0].Kind == ast.KindListPattern || targets[0].Kind == ast.KindMapPattern) {
		v, err := in.eval(values[0], ctx, false)
		if err != nil {
			return err
		}
		return in.bindPattern(targets[0], v, ctx)
	}
	for i, tg := range targets {
		v, err := in.eval(values[i], ctx, false)
		if err != nil {
			return err
		}
		if tg.Kind == ast.KindAttr || tg.Kind == ast.KindIndex {
			if err := in.assignMember(tg, v, ctx); err != nil {
				return err
			}
			continue
		}
		ctx.Set(tg.Str, v)
	}
	return nil
}

// assignMember writes v to a member-set target (@set recv.name = v or
// @set recv[key] = v). It first takes ownership of the receiver path (ownPath):
// any copy-on-write array from the root name down is privatized and rebound, so
// the in-place write lands only on arrays this scope exclusively owns and cannot
// reach an alias a scope-entry boundary left it sharing (proper value semantics,
// spec 04 Section 6.3). A host Object receiver (the mutable-cell path) keeps
// reference identity, so a cell mutation is still visible to every holder and
// survives a loop body, while the loop's own name rebindings do not leak.
func (in *interp) assignMember(tg *ast.Node, v runtime.Value, ctx *runtime.Scope) error {
	recv, err := in.ownPath(tg.Child(0), ctx)
	if err != nil {
		return err
	}
	if tg.Kind == ast.KindAttr {
		if err := runtime.SetMember(recv, tg.Str, v); err != nil {
			return posErr(tg, err)
		}
		return nil
	}
	key, err := in.eval(tg.Child(1), ctx, false)
	if err != nil {
		return err
	}
	if err := runtime.SetIndex(recv, keyOf(key), v); err != nil {
		return posErr(tg, err)
	}
	return nil
}

// ownPath returns the receiver value at an assignment-path node, having
// privatized every shared copy-on-write array from the root name downward and
// rebound each fresh copy under its name (root) or into its owned parent slot
// (intermediate hop). Because it recurses to the root first, ownership flows
// top-down: a node is owned and rebound before its child is read, so a copy at one
// level marks its children shared (cloneShallowCOW) and the next level privatizes
// in turn. A host Object hop passes through by reference (cells persist); a
// scalar receiver passes through for SetMember/SetIndex to reject as before.
// Member targets are always rooted at a name (parse/control.go), so the root is a
// KindName and reads through the strict-undefined eval path unchanged.
func (in *interp) ownPath(node *ast.Node, ctx *runtime.Scope) (runtime.Value, error) {
	switch node.Kind {
	case ast.KindName:
		cur, err := in.eval(node, ctx, false)
		if err != nil {
			return runtime.Null(), err
		}
		if owned, copied := runtime.Own(cur); copied {
			ctx.SetOwned(node.Str, owned)
			return owned, nil
		}
		return cur, nil
	case ast.KindAttr:
		parent, err := in.ownPath(node.Child(0), ctx)
		if err != nil {
			return runtime.Null(), err
		}
		cur, err := runtime.GetAttribute(parent, runtime.Str(node.Str), runtime.AccessDot, false)
		if err != nil {
			return runtime.Null(), posErr(node, err)
		}
		if owned, copied := runtime.Own(cur); copied {
			if err := runtime.SetMember(parent, node.Str, owned); err != nil {
				return runtime.Null(), posErr(node, err)
			}
			return owned, nil
		}
		return cur, nil
	case ast.KindIndex:
		parent, err := in.ownPath(node.Child(0), ctx)
		if err != nil {
			return runtime.Null(), err
		}
		keyv, err := in.eval(node.Child(1), ctx, false)
		if err != nil {
			return runtime.Null(), err
		}
		key := keyOf(keyv)
		cur, err := runtime.GetAttribute(parent, key, runtime.AccessIndex, false)
		if err != nil {
			return runtime.Null(), posErr(node, err)
		}
		if owned, copied := runtime.Own(cur); copied {
			if err := runtime.SetIndex(parent, key, owned); err != nil {
				return runtime.Null(), posErr(node, err)
			}
			return owned, nil
		}
		return cur, nil
	default:
		return in.eval(node, ctx, false)
	}
}

// bindPattern binds a destructuring pattern (spec 01 Sections 2.1, 3.2). A list
// pattern binds slots positionally from a sequence; a map/object pattern binds
// each named slot from the value's member of that key, supporting the rename
// form {key: alias}.
func (in *interp) bindPattern(pat *ast.Node, v runtime.Value, ctx *runtime.Scope) error {
	switch pat.Kind {
	case ast.KindListPattern:
		return in.bindListPattern(pat, v, ctx)
	case ast.KindMapPattern:
		return in.bindMapPattern(pat, v, ctx)
	default:
		return posErr(pat, errors.New(errors.KindRuntime, "unknown destructuring pattern"))
	}
}

// bindListPattern binds a sequence-destructuring pattern positionally (spec 01
// Section 2.1, 3.2). Each fixed slot binds one element by position; a nested
// list/map pattern recurses; an elided slot (a nil child) advances past its source
// element without binding; an optional slot ("b?", a KindOptional) binds the
// element when present and null when the source is short; a trailing "...rest"
// slot captures the remaining elements as a new sequence (possibly empty).
//
// Arity is still enforced for REQUIRED slots: a required slot is a fixed slot that
// is neither optional nor a tail. The supplied count must cover at least the
// required slots; without a tail it must also not exceed the fixed-slot count (a
// generator should not silently drop trailing elements). Optional slots make the
// fixed count an upper, not exact, bound -- the difference between required and
// fixed is null-padded.
func (in *interp) bindListPattern(pat *ast.Node, v runtime.Value, ctx *runtime.Scope) error {
	if v.Kind != runtime.KArray || v.Arr == nil {
		return posErr(pat, errors.New(errors.KindRuntime,
			"destructuring expects a sequence"))
	}
	ps := v.Arr.Pairs()

	// Separate the fixed slots from an optional trailing "...rest" tail. The parser
	// guarantees a KindSpread slot is last, so at most one exists and it is final.
	fixed := pat.Children
	var tail *ast.Node
	if k := len(fixed); k > 0 && fixed[k-1] != nil && fixed[k-1].Kind == ast.KindSpread {
		tail = fixed[k-1]
		fixed = fixed[:k-1]
	}

	// A required slot consumes a mandatory source position; an optional slot ("b?")
	// is null-padded when the source is short. Elided slots (nil) still consume a
	// position, so they count as required for arity.
	required := 0
	for _, slot := range fixed {
		if slot == nil || slot.Kind != ast.KindOptional {
			required++
		}
	}

	// Enforce arity. The supplied count must cover the required slots; without a
	// tail it must also stay within the fixed-slot count (no silent drop).
	if len(ps) < required {
		return posErr(pat, errors.New(errors.KindRuntime,
			"sequence destructuring expects at least %d element(s) but got %d",
			required, len(ps)))
	}
	if tail == nil && len(ps) > len(fixed) {
		return posErr(pat, errors.New(errors.KindRuntime,
			"sequence destructuring expects %d element(s) but got %d",
			len(fixed), len(ps)))
	}

	for i, slot := range fixed {
		if slot == nil { // elided slot: skip its position
			continue
		}
		target := slot
		optional := slot.Kind == ast.KindOptional
		if optional {
			target = slot.Child(0)
		}
		// Guard the source index for BOTH slot kinds. A short source past this slot is
		// the expected case for an optional slot (null-pad) but a genuine arity error
		// for a required slot. This guard is defense in depth: the parser forbids a
		// required/elided slot after an optional (so a short source can only fall here
		// on an optional), but the binder must never index out of range on the grammar
		// the parser accepts -- index-safety here keeps the engine uncrashable.
		if i >= len(ps) {
			if optional {
				// An optional name binds null; an optional nested pattern null-binds
				// every name it introduces.
				in.bindTargetNull(target, ctx)
				continue
			}
			return posErr(pat, errors.New(errors.KindRuntime,
				"sequence destructuring expects at least %d element(s) but got %d",
				required, len(ps)))
		}
		if err := in.bindSlot(target, ps[i].Val, ctx); err != nil {
			return err
		}
	}

	if tail != nil {
		// Collect the elements past the fixed slots into a fresh sequence and bind it
		// to the tail name (KindSpread child 0 is the captured KindName). When optional
		// slots left the source shorter than the fixed-slot count, the tail is empty;
		// clamp the start so the slice never underflows.
		start := len(fixed)
		if start > len(ps) {
			start = len(ps)
		}
		rest := runtime.NewArray()
		for _, p := range ps[start:] {
			rest.SetInt(int64(rest.Len()), p.Val)
		}
		ctx.Set(tail.Child(0).Str, runtime.Arr(rest))
	}
	return nil
}

// bindTargetNull binds every name a target introduces to null. It is the absent
// path for an optional slot whose source element was missing: a plain name binds
// null directly, while a nested list/map pattern null-binds each of its own slots
// (recursively) so the absent shape leaves no name undefined (spec 01 Section 2.1).
func (in *interp) bindTargetNull(target *ast.Node, ctx *runtime.Scope) {
	switch target.Kind {
	case ast.KindName, ast.KindTarget:
		ctx.Set(target.Str, runtime.Null())
	case ast.KindListPattern:
		for _, slot := range target.Children {
			if slot == nil { // elided slot binds nothing
				continue
			}
			inner := slot
			if slot.Kind == ast.KindOptional {
				inner = slot.Child(0)
			}
			if slot.Kind == ast.KindSpread {
				ctx.Set(slot.Child(0).Str, runtime.Arr(runtime.NewArray()))
				continue
			}
			in.bindTargetNull(inner, ctx)
		}
	case ast.KindMapPattern:
		for _, slot := range target.Children {
			if slot.Kind != ast.KindMapTarget {
				continue
			}
			local := slot.Str
			if slot.Bool {
				local = slot.Child(0).Str
			}
			ctx.Set(local, runtime.Null())
		}
	}
}

// bindSlot binds one value to a non-elided, non-tail sequence slot: a plain name
// target binds directly, while a nested list/map pattern recurses. It is shared by
// the required- and optional-slot paths so both bind nested patterns identically.
func (in *interp) bindSlot(target *ast.Node, val runtime.Value, ctx *runtime.Scope) error {
	switch target.Kind {
	case ast.KindName, ast.KindTarget:
		ctx.Set(target.Str, val)
	case ast.KindListPattern, ast.KindMapPattern:
		return in.bindPattern(target, val, ctx)
	}
	return nil
}

// bindMapPattern binds a map/object-destructuring pattern. Each KindMapTarget
// reads the value's member named by its source key (Str) through the same dotted
// access used by a.b, so the right-hand side may be a mapping OR a host object;
// the bound local is the alias when one is present ({key: alias}) and the key
// itself otherwise ({name}). A missing key follows the engine's strictness:
// under strict variables it is an undefined error, under lenient mode it binds
// null (spec 04 Section 6).
func (in *interp) bindMapPattern(pat *ast.Node, v runtime.Value, ctx *runtime.Scope) error {
	allowAbsent := !in.eng.StrictVariables()
	for _, slot := range pat.Children {
		if slot.Kind != ast.KindMapTarget {
			continue
		}
		val, err := runtime.GetAttribute(v, runtime.Str(slot.Str), runtime.AccessDot, allowAbsent)
		if err != nil {
			return posErr(pat, err)
		}
		local := slot.Str
		if slot.Bool { // rename form {key: alias}; the alias is child 0
			local = slot.Child(0).Str
		}
		ctx.Set(local, val)
	}
	return nil
}

// execCapture renders the body to a string-like value and binds it (spec 01
// Section 4.3). Under escaping off it is a plain Str; under any active escape
// strategy it is a Safe value.
func (in *interp) execCapture(n *ast.Node, ctx *runtime.Scope) error {
	// The capture node carries an optional KindType child before the body items;
	// skip it. The body is rendered into a fresh sink.
	body := n.Children
	if len(body) > 0 && body[0].Kind == ast.KindType {
		body = body[1:]
	}
	out, err := in.captureItems(body, ctx)
	if err != nil {
		return err
	}
	if in.escape != "" {
		ctx.Set(n.Str, runtime.Safe(out))
	} else {
		ctx.Set(n.Str, runtime.Str(out))
	}
	return nil
}

// execCache renders an @cache region, memoizing its body under the resolved key
// (spec 01 Section 4.7, design/control-flow Section 10.6). On a cache hit the
// body is emitted from the store and NOT re-rendered; on a miss the body renders
// in a child scope (like capture), is stored under the key with its tags, and is
// emitted. The ttl argument is accepted but is a documented no-op for the
// engine-default in-memory cache. The key is namespaced by the rendering
// template so identical keys in different templates do not collide. The body is
// already-rendered output, so it is spliced verbatim with emitString -- under an
// active escape strategy it was produced through the same escaper as a capture
// and must not be escaped a second time.
func (in *interp) execCache(n *ast.Node, ctx *runtime.Scope) error {
	count := int(n.Int)
	args := n.Children[:count]
	body := n.Children[count:]

	var keyExpr, ttlExpr, tagsExpr *ast.Node
	for _, a := range args {
		switch a.Str {
		case "key":
			keyExpr = a.Child(0)
		case "ttl":
			ttlExpr = a.Child(0)
		case "tags":
			tagsExpr = a.Child(0)
		default:
			return posErr(a, errors.New(errors.KindRuntime,
				"unknown cache argument %q (want key, ttl, or tags)", a.Str))
		}
	}
	if keyExpr == nil {
		return posErr(n, errors.New(errors.KindRuntime, "@cache requires a key"))
	}
	_ = ttlExpr // ttl is a no-op for the non-expiring in-memory cache.

	keyVal, err := in.eval(keyExpr, ctx, false)
	if err != nil {
		return err
	}
	keyText, err := runtime.ToText(keyVal)
	if err != nil {
		return posErr(n, err)
	}
	// Namespace the user key by the rendering template so two templates that both
	// cache under "header" do not share an entry.
	fullKey := in.root.Name + "\x00" + keyText

	rc := in.eng.RenderCache()
	if rc != nil {
		if cached, ok := rc.Get(fullKey); ok {
			return posErr(n, in.emitString(cached))
		}
	}

	// Miss: render the body in a child scope so body-local sets do not leak.
	preLabels, preBytes := in.slotStamp()
	out, err := in.captureItems(body, ctx.Child())
	if err != nil {
		return err
	}
	// A body that interacted with the deferred-slot machinery must NOT be
	// stored: a @yield placeholder embeds this render's unique token, which no
	// later render's resolveSlots could substitute (a replay would emit the raw
	// token), and a @provide is a render-scoped side effect a replay would
	// silently lose. Such a region renders fresh every time; correctness wins
	// over memoization. The slot stamp catches provides from nested includes and
	// embeds too, because they append into this render's shared slot buffers.
	// The token scan runs only once a @yield minted the token: every string
	// contains the empty unminted token, so scanning for it would classify
	// every region as slot-using and silently disable @cache storage.
	postLabels, postBytes := in.slotStamp()
	tok := in.slotOwner.yieldToken
	usedSlots := postLabels != preLabels || postBytes != preBytes ||
		(tok != "" && strings.Contains(out, tok))
	if rc != nil && !usedSlots {
		tags, err := in.evalCacheTags(tagsExpr, ctx)
		if err != nil {
			return err
		}
		rc.Put(fullKey, out, tags)
	}
	return posErr(n, in.emitString(out))
}

// slotStamp summarizes the deferred-slot state as (label count, total buffered
// bytes). Slot buffers only ever grow, so an unchanged stamp across a capture
// proves no @provide ran inside it. The buffers live on the slot owner, so the
// stamp sees contributions from every nested interp of this render.
func (in *interp) slotStamp() (labels, bytes int) {
	own := in.slotOwner
	for _, b := range own.slots {
		bytes += b.Len()
	}
	return len(own.slots), bytes
}

// evalCacheTags evaluates the optional tags expression to a list of strings.
func (in *interp) evalCacheTags(tagsExpr *ast.Node, ctx *runtime.Scope) ([]string, error) {
	if tagsExpr == nil {
		return nil, nil
	}
	v, err := in.eval(tagsExpr, ctx, false)
	if err != nil {
		return nil, err
	}
	if v.Kind != runtime.KArray || v.Arr == nil {
		return nil, nil
	}
	var tags []string
	for _, p := range v.Arr.Pairs() {
		t, err := runtime.ToText(p.Val)
		if err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

// captureItems renders items into a separate sink and returns the produced
// string, used by capture, apply, and the function-form include.
func (in *interp) captureItems(items []*ast.Node, ctx *runtime.Scope) (string, error) {
	sub := &captureSink{}
	saved := in.out
	// A capture renders into its own sink, so the active @tab indentation must not
	// be applied while filling the buffer -- it is applied once, later, when the
	// captured result is emitted at the outer position. Suspend the indent state
	// for the duration of the capture and restore it afterward.
	savedIndent, savedAtLineStart := in.indent, in.atLineStart
	in.indent, in.atLineStart = "", true
	in.out = sub
	err := in.execItems(items, ctx)
	in.out = saved
	in.indent, in.atLineStart = savedIndent, savedAtLineStart
	return sub.b.String(), err
}

// execWith introduces a scope merging the given vars; "only" replaces the
// context entirely for the body (spec 01 Section 4.5).
func (in *interp) execWith(n *ast.Node, ctx *runtime.Scope) error {
	mapVal, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return err
	}
	var scope *runtime.Scope
	if n.Bool { // only
		scope = runtime.NewScope()
	} else {
		scope = ctx.Child()
	}
	if mapVal.Kind == runtime.KArray && mapVal.Arr != nil {
		for _, p := range mapVal.Arr.Pairs() {
			name, err := runtime.ToText(p.Key)
			if err != nil {
				return err
			}
			scope.Set(name, p.Val)
		}
	}
	return in.execItems(n.Children[1:], scope)
}

// execApply captures the body, then pipes it through the filter chain (spec 01
// Section 4.5).
func (in *interp) execApply(n *ast.Node, ctx *runtime.Scope) error {
	filterCount := int(n.Int)
	filters := n.Children[:filterCount]
	body := n.Children[filterCount:]
	captured, err := in.captureItems(body, ctx)
	if err != nil {
		return err
	}
	v := runtime.Str(captured)
	for _, f := range filters {
		filt, ok := in.eng.Extensions().Filter(f.Str)
		if !ok {
			return posErr(f, errors.New(errors.KindRuntime, "unknown filter %q in apply", f.Str))
		}
		args := []runtime.Value{v}
		for _, c := range f.Children {
			if c.Kind != ast.KindArg {
				continue
			}
			av, err := in.eval(c.Child(0), ctx, false)
			if err != nil {
				return err
			}
			args = append(args, av)
		}
		// Sandbox arrow gating (B13): mirror evalFilter so the @apply filter path
		// rejects a smuggled host callable just as the inline `| map(f)` form does.
		// Without this the two filter-application paths enforce the rule
		// inconsistently and `@apply | map(f) {...}` bypasses the gate.
		if err := in.checkArrowArgs(f, args); err != nil {
			return err
		}
		// String-coercion gate (B12) for the coercing filters (join/replace/split):
		// a host object reaching these as an argument would be stringified inside
		// ext without consulting the policy, so gate it here at the choke point.
		if err := in.checkStringifyArgs(f.Str, args); err != nil {
			return posErr(f, err)
		}
		args = in.injectFilter(filt, ctx, args)
		v, err = filt.Fn(args)
		if err != nil {
			return posErr(f, err)
		}
	}
	// Under an active escape strategy the body was already escaped during the
	// capture render (every interpolation inside it flowed through emit), so the
	// filtered result is finished output, not a raw value. Wrap it as Safe so the
	// final emit does not escape it a SECOND time -- mirroring capture/macro/block,
	// which the slice's safeness model (spec 04 Section 8.2) wraps for the same
	// reason. Without this, e.g. an already-escaped "&lt;" would re-escape its "&"
	// to "&amp;". The off strategy (escape == "") leaves v untouched, byte-exact.
	if in.escape != "" && v.Kind != runtime.KSafe {
		text, err := runtime.ToText(v)
		if err != nil {
			return posErr(n, err)
		}
		v = runtime.Safe(text)
	}
	return posErr(n, in.emit(v))
}

// execLog evaluates the @log expression and writes its text form to the host
// logger (WithLogger, default a discarding logger). It produces NO rendered
// output: the value never reaches the output sink. The node is a coverable unit,
// recorded by the caller before this runs, so a @log line that executes counts
// as covered even though it emits nothing (contrast a comment, which the lexer
// consumes and which is never a coverable node).
func (in *interp) execLog(n *ast.Node, ctx *runtime.Scope) error {
	v, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return err
	}
	text, err := runtime.ToText(v)
	if err != nil {
		return posErr(n, err)
	}
	in.eng.Logger().Print(text)
	return nil
}

// execTabBlock renders the region body with n additional levels of indentation
// applied to every non-blank line of its output. One level is TabWidth spaces
// (WithTabWidth, default 4). Nesting is cumulative: the new prefix is appended to
// the enclosing indent and restored on exit, so an inner @tab adds to an outer
// one. A level of zero or below adds nothing. Blank lines stay blank. The
// indentation is applied by the output layer (in.write), so it composes with
// whitespace control and escaping, which run before output reaches the sink.
func (in *interp) execTabBlock(n *ast.Node, ctx *runtime.Scope) error {
	levelVal, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return err
	}
	levels, err := tabLevels(levelVal)
	if err != nil {
		return posErr(n, err)
	}
	saved, savedAtLineStart := in.indent, in.atLineStart
	if levels > 0 {
		in.indent += strings.Repeat(" ", levels*in.eng.TabWidth())
	}
	// The region indents its entire rendered body, so it begins its own
	// line-start context: the first body line receives the indent prefix even
	// when the output cursor arrives mid-line (for example after whitespace
	// control consumes the newline before @tab, or when preceding output did
	// not end in a newline). The saved cursor is restored on exit.
	in.atLineStart = true
	err = in.execItems(n.Children[1:], ctx)
	in.indent, in.atLineStart = saved, savedAtLineStart
	return err
}

// tabLevels coerces a @tab level value to a non-negative count of indent levels.
// An integer is taken directly; a float is truncated toward zero; a level of
// zero or below yields zero (no indentation added). A non-numeric level is a
// runtime error, matching the tab filter's numeric contract.
func tabLevels(v runtime.Value) (int, error) {
	switch v.Kind {
	case runtime.KInt:
		if v.I < 0 {
			return 0, nil
		}
		// int is 32-bit on some targets; clamp so a level past the platform int
		// range cannot wrap negative (which would later panic strings.Repeat).
		if v.I > math.MaxInt {
			return math.MaxInt, nil
		}
		return int(v.I), nil
	case runtime.KFloat:
		if v.F < 0 {
			return 0, nil
		}
		return int(v.F), nil
	default:
		return 0, errors.New(errors.KindRuntime, "@tab level must be a number, got %s", v.Kind)
	}
}

// execEscape sets the active strategy for the region body, then restores the
// prior strategy on exit (spec 01 Section 4.7, 04 Section 8). The save/restore
// of in.escape is the strategy STACK: a nested @escape region composes by
// pushing its strategy and popping back to the enclosing one (the module default
// or an outer region) when the body ends. "off"/"raw"/"none" disable escaping;
// the six named strategies (html, js, css, html_attr, html_attr_relaxed, url)
// each apply their escaper to every interpolated value in the body via emit.
func (in *interp) execEscape(n *ast.Node, ctx *runtime.Scope) error {
	saved := in.escape
	strategy, err := normalizeEscapeStrategy(n.Str)
	if err != nil {
		return posErr(n, err)
	}
	in.escape = strategy
	err = in.execItems(n.Children, ctx)
	in.escape = saved
	return err
}

// normalizeEscapeStrategy maps an @escape region's strategy word to the stored
// active-strategy value: "" means escaping off, otherwise one of the six named
// strategies. off and its synonym raw are the documented off spellings (spec 04
// Section 8.1). An unknown word is a runtime error naming the valid set; the six
// strategies themselves are validated against the shared ext escaper so the
// region and the escape()/e() filter stay in lockstep.
func normalizeEscapeStrategy(word string) (string, error) {
	switch word {
	case "off", "raw":
		return "", nil
	case "html", "js", "css", "html_attr", "html_attr_relaxed", "url":
		return word, nil
	default:
		return "", errors.New(errors.KindRuntime,
			"unknown escape strategy %q; expected one of off, html, js, css, "+
				"html_attr, html_attr_relaxed, url", word)
	}
}

// execGuard selects a branch on whether the named callable is registered (spec
// 01 Section 4.6). The dead branch is not rendered.
func (in *interp) execGuard(n *ast.Node, ctx *runtime.Scope) error {
	name := n.Child(0).Str // the KindString name node
	var present bool
	switch n.Str {
	case "filter":
		present = in.eng.Extensions().HasFilter(name)
	case "function":
		present = in.eng.Extensions().HasFunction(name)
	case "test":
		present = in.eng.Extensions().HasTest(name)
	}
	// Body items follow the name node; an optional trailing KindClause is the else.
	body := n.Children[1:]
	var elseClause *ast.Node
	if k := len(body); k > 0 && body[k-1].Kind == ast.KindClause {
		elseClause = body[k-1]
		body = body[:k-1]
	}
	if present {
		in.covArm(n, cover.GuardYes)
		return in.execItems(body, ctx)
	}
	// The callable is absent: the guard-absent arm is taken (the @else body, or
	// nothing, runs). Recorded even when there is no else so the arm is measured.
	in.covArm(n, cover.GuardNo)
	if elseClause != nil {
		return in.execItems(elseClause.Children, ctx)
	}
	return nil
}

// captureSink is a strings.Builder-backed Sink for nested captures.
type captureSink struct {
	b strings.Builder
}

// WriteString appends s to the capture buffer and reports the bytes written.
func (c *captureSink) WriteString(s string) (int, error) { return c.b.WriteString(s) }
