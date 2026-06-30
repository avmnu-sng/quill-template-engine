package interp

import (
	"strings"

	"github.com/avmnusng/quill-template-engine/ast"
	"github.com/avmnusng/quill-template-engine/errors"
	"github.com/avmnusng/quill-template-engine/runtime"
)

// renderTemplate renders a template, resolving inheritance first. If the
// template extends a parent, the merged block table is built bottom-up (most-
// derived definitions win) and the TOPMOST parent's body is rendered, with each
// @block site delegating to the resolved definition (spec 01 Section 5.2).
// Otherwise the template's own body is rendered top to bottom.
func (in *interp) renderTemplate(tmpl *Template, ctx *runtime.Context) error {
	chain, err := in.buildChain(tmpl, ctx)
	if err != nil {
		return err
	}
	in.parentChain = chain
	if err := in.buildBlockTable(chain); err != nil {
		return err
	}
	in.loadMacros(tmpl, ctx)

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

// buildChain resolves the inheritance chain from tmpl up to its topmost ancestor
// (index 0 most-derived, last least-derived). A non-inheriting template yields a
// one-element chain. The extends operand is a string-coerced expression or a
// candidate list (first existing wins), spec 01 Section 5.2.
func (in *interp) buildChain(tmpl *Template, ctx *runtime.Context) ([]*Template, error) {
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
func (in *interp) resolveExtendsName(extends *ast.Node, ctx *runtime.Context) (string, error) {
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
// traitable, or an alias names a block the trait does not define.
func (in *interp) buildBlockTable(chain []*Template) error {
	in.blocks = map[string]*blockEntry{}
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
// existing definition chain (so parent() walks to it).
func (in *interp) appendBlockDef(name string, def blockDef) {
	if e, ok := in.blocks[name]; ok {
		e.chain = append(e.chain, def)
		return
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
// (spec 01 Section 5.3).
func (in *interp) loadMacros(tmpl *Template, ctx *runtime.Context) {
	in.macros = map[string]*macroEntry{}
	for _, name := range tmpl.macroOrder {
		node, _ := tmpl.Macro(name)
		in.macros[name] = &macroEntry{home: tmpl, node: node}
	}
	for _, imp := range tmpl.imports {
		in.loadImport(imp, ctx)
	}
}

// execItems renders a run of body items in order.
func (in *interp) execItems(items []*ast.Node, ctx *runtime.Context) error {
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
func (in *interp) execItem(n *ast.Node, ctx *runtime.Context) error {
	switch n.Kind {
	case ast.KindText:
		return in.emitString(n.Str)
	case ast.KindVerbatim:
		return in.emitString(n.Str)
	case ast.KindPrint:
		return in.execPrint(n, ctx)
	case ast.KindIf:
		return in.execIf(n, ctx)
	case ast.KindFor:
		return in.execFor(n, ctx)
	case ast.KindSet:
		return in.execSet(n, ctx)
	case ast.KindCapture:
		return in.execCapture(n, ctx)
	case ast.KindWith:
		return in.execWith(n, ctx)
	case ast.KindApply:
		return in.execApply(n, ctx)
	case ast.KindDo:
		_, err := in.eval(n.Child(0), ctx, false)
		return err
	case ast.KindFlush:
		return nil // documented no-op for a string sink (spec 01 Section 4.4)
	case ast.KindEscape:
		return in.execEscape(n, ctx)
	case ast.KindGuard:
		return in.execGuard(n, ctx)
	case ast.KindBlock:
		return in.execBlockSite(n, ctx)
	case ast.KindInclude:
		return in.execInclude(n, ctx)
	case ast.KindEmbed:
		return in.execEmbed(n, ctx)
	case ast.KindExtends, ast.KindMacro, ast.KindImport, ast.KindFrom, ast.KindUse:
		return nil // declarations: no direct output
	case ast.KindCache:
		return in.execCache(n, ctx)
	case ast.KindSandbox:
		return in.execSandbox(n, ctx)
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
func (in *interp) execPrint(n *ast.Node, ctx *runtime.Context) error {
	v, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return err
	}
	return posErr(n, in.emit(v))
}

// execIf renders the first clause whose condition is truthy, else the else
// clause if present (spec 01 Section 4.1). A clause body runs in the current
// scope (an if introduces no new scope).
func (in *interp) execIf(n *ast.Node, ctx *runtime.Context) error {
	for _, clause := range n.Children {
		if clause.Bool { // if / elseif: child 0 condition, rest body
			cond, err := in.eval(clause.Child(0), ctx, false)
			if err != nil {
				return err
			}
			if runtime.Truthy(cond) {
				return in.execItems(clause.Children[1:], ctx)
			}
		} else { // else: all children are body
			return in.execItems(clause.Children, ctx)
		}
	}
	return nil
}

// execFor renders a for loop with full loop.* metadata (spec 01 Section 4.2).
// The iterand is drained to pairs; a non-iterable is a runtime error (NOT a
// silent empty loop) unless lenient mode is on. The body runs in a child scope;
// reassignments to pre-existing names persist, body-local sets do not leak.
func (in *interp) execFor(n *ast.Node, ctx *runtime.Context) error {
	count := int(n.Int)
	target1 := n.Child(0)
	var target2 *ast.Node
	idx := 1
	if count == 2 {
		target2 = n.Child(1)
		idx = 2
	}
	iterand := n.Child(idx)
	body := n.Child(idx + 1)
	var elseBody *ast.Node
	if n.Bool {
		elseBody = n.Child(idx + 2)
	}

	collVal, err := in.eval(iterand, ctx, false)
	if err != nil {
		return err
	}
	pairs, err := runtime.EnsureTraversable(collVal, !in.eng.StrictVariables())
	if err != nil {
		return posErr(n, err)
	}
	if len(pairs) == 0 {
		if elseBody != nil {
			return in.execItems(elseBody.Children, ctx)
		}
		return nil
	}

	// Child scope: clone the context so body-local sets do not leak, but copy
	// back reassignments of pre-existing names after the loop (lexical scoping,
	// spec 01 Section 4.2).
	pre := ctx
	loopCtx := pre.Clone()
	parentLoop, _ := pre.Get("loop")
	length := len(pairs)
	for i, p := range pairs {
		loopCtx.Set(target1.Str, p.Val)
		if target2 != nil {
			// for k, v: target1 binds the value, target2... per spec the first target
			// is the value in single-target form; in two-target form target1 is the
			// KEY and target2 is the value (for k, v in mapping). Bind accordingly.
			loopCtx.Set(target1.Str, p.Key)
			loopCtx.Set(target2.Str, p.Val)
		}
		loopCtx.Set("loop", loopMeta(i, length, parentLoop))
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

// loopMeta builds the loop.* metadata array for iteration i of length n. All
// fields are always defined (a divergence from Twig), spec 01 Section 4.2.
func loopMeta(i, n int, parent runtime.Value) runtime.Value {
	m := runtime.NewArray()
	m.SetStr("index0", runtime.Int(int64(i)))
	m.SetStr("index", runtime.Int(int64(i+1)))
	m.SetStr("revindex0", runtime.Int(int64(n-1-i)))
	m.SetStr("revindex", runtime.Int(int64(n-i)))
	m.SetStr("first", runtime.Bool(i == 0))
	m.SetStr("last", runtime.Bool(i == n-1))
	m.SetStr("length", runtime.Int(int64(n)))
	if parent.Kind != runtime.KNull {
		m.SetStr("parent", parent)
	} else {
		m.SetStr("parent", runtime.Null())
	}
	return runtime.Arr(m)
}

// execSet binds one or more targets to one or more values (spec 01 Section 4.3).
// Multi-target and destructuring forms are bound positionally; a type annotation
// is ignored at render time (the checker consumes it).
func (in *interp) execSet(n *ast.Node, ctx *runtime.Context) error {
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
		ctx.Set(tg.Str, v)
	}
	return nil
}

// bindPattern binds a destructuring pattern (spec 01 Sections 2.1, 3.2). A list
// pattern binds slots positionally from a sequence; a map/object pattern binds
// each named slot from the value's member of that key, supporting the rename
// form {key: alias}.
func (in *interp) bindPattern(pat *ast.Node, v runtime.Value, ctx *runtime.Context) error {
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
// Section 3.2). Each fixed slot binds one element by position; a nested list/map
// pattern recurses; a trailing "...rest" slot captures the remaining elements as
// a new sequence (possibly empty). Over/under-supply is an error by default: a
// pattern without a tail must match the element count exactly (a generator should
// not silently pad with null nor drop trailing elements). A tail slot makes
// over-supply legal -- the fixed slots are the minimum.
func (in *interp) bindListPattern(pat *ast.Node, v runtime.Value, ctx *runtime.Context) error {
	if v.Kind != runtime.KArray || v.Arr == nil {
		return posErr(pat, errors.New(errors.KindRuntime,
			"destructuring expects a sequence"))
	}
	ps := v.Arr.Pairs()

	// Separate the fixed slots from an optional trailing "...rest" tail. The parser
	// guarantees a KindSpread slot is last (listToPattern), so at most one exists
	// and it is the final child.
	fixed := pat.Children
	var tail *ast.Node
	if k := len(fixed); k > 0 && fixed[k-1] != nil && fixed[k-1].Kind == ast.KindSpread {
		tail = fixed[k-1]
		fixed = fixed[:k-1]
	}

	// Enforce arity. Without a tail the counts must match exactly; with a tail the
	// supplied count must cover at least the fixed slots.
	if tail == nil {
		if len(ps) != len(fixed) {
			return posErr(pat, errors.New(errors.KindRuntime,
				"sequence destructuring expects %d element(s) but got %d",
				len(fixed), len(ps)))
		}
	} else if len(ps) < len(fixed) {
		return posErr(pat, errors.New(errors.KindRuntime,
			"sequence destructuring with a tail expects at least %d element(s) but got %d",
			len(fixed), len(ps)))
	}

	for i, slot := range fixed {
		if slot == nil { // elided slot: skip its position
			continue
		}
		val := ps[i].Val // arity checked above, so the index is in range
		switch slot.Kind {
		case ast.KindName, ast.KindTarget:
			ctx.Set(slot.Str, val)
		case ast.KindListPattern, ast.KindMapPattern:
			if err := in.bindPattern(slot, val, ctx); err != nil {
				return err
			}
		}
	}

	if tail != nil {
		// Collect the elements past the fixed slots into a fresh sequence and bind it
		// to the tail name (KindSpread child 0 is the captured KindName).
		rest := runtime.NewArray()
		for _, p := range ps[len(fixed):] {
			rest.SetInt(int64(rest.Len()), p.Val)
		}
		ctx.Set(tail.Child(0).Str, runtime.Arr(rest))
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
func (in *interp) bindMapPattern(pat *ast.Node, v runtime.Value, ctx *runtime.Context) error {
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
func (in *interp) execCapture(n *ast.Node, ctx *runtime.Context) error {
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
func (in *interp) execCache(n *ast.Node, ctx *runtime.Context) error {
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
	out, err := in.captureItems(body, ctx.Clone())
	if err != nil {
		return err
	}
	if rc != nil {
		tags, err := in.evalCacheTags(tagsExpr, ctx)
		if err != nil {
			return err
		}
		rc.Put(fullKey, out, tags)
	}
	return posErr(n, in.emitString(out))
}

// evalCacheTags evaluates the optional tags expression to a list of strings.
func (in *interp) evalCacheTags(tagsExpr *ast.Node, ctx *runtime.Context) ([]string, error) {
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
func (in *interp) captureItems(items []*ast.Node, ctx *runtime.Context) (string, error) {
	sub := &captureSink{}
	saved := in.out
	in.out = sub
	err := in.execItems(items, ctx)
	in.out = saved
	return sub.b.String(), err
}

// execWith introduces a scope merging the given vars; "only" replaces the
// context entirely for the body (spec 01 Section 4.5).
func (in *interp) execWith(n *ast.Node, ctx *runtime.Context) error {
	mapVal, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return err
	}
	var scope *runtime.Context
	if n.Bool { // only
		scope = runtime.NewContext()
	} else {
		scope = ctx.Clone()
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
func (in *interp) execApply(n *ast.Node, ctx *runtime.Context) error {
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

// execEscape sets the active strategy for the region body, then restores the
// prior strategy on exit (spec 01 Section 4.7, 04 Section 8). The save/restore
// of in.escape is the strategy STACK: a nested @escape region composes by
// pushing its strategy and popping back to the enclosing one (the module default
// or an outer region) when the body ends. "off"/"raw"/"none" disable escaping;
// the six named strategies (html, js, css, html_attr, html_attr_relaxed, url)
// each apply their escaper to every interpolated value in the body via emit.
func (in *interp) execEscape(n *ast.Node, ctx *runtime.Context) error {
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
func (in *interp) execGuard(n *ast.Node, ctx *runtime.Context) error {
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
		return in.execItems(body, ctx)
	}
	if elseClause != nil {
		return in.execItems(elseClause.Children, ctx)
	}
	return nil
}

// captureSink is a strings.Builder-backed Sink for nested captures.
type captureSink struct {
	b strings.Builder
}

func (c *captureSink) WriteString(s string) (int, error) { return c.b.WriteString(s) }
