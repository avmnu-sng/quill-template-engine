package interp

import (
	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/cover"
	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// execBlockSite renders an @block at its definition site. It resolves the name
// through the merged block table so an override in a more-derived template wins;
// the resolved definition's body is rendered at the most-derived (index 0)
// position of the name's definition chain (spec 01 Section 5.2, design/
// composition Section 2.5).
func (in *interp) execBlockSite(n *ast.Node, ctx *runtime.Scope) error {
	entry, ok := in.blocks[n.Str]
	if !ok {
		// No table entry (e.g. an embed-local block); render the node's own body.
		return in.renderBlockBody(n, ctx)
	}
	return in.renderBlockAt(entry, 0, ctx)
}

// renderBlockAt renders the depth-th definition in a block's chain (0 is the
// most-derived override). parent() inside that body renders depth+1.
func (in *interp) renderBlockAt(entry *blockEntry, depth int, ctx *runtime.Scope) error {
	if depth >= len(entry.chain) {
		return nil
	}
	def := entry.chain[depth]
	savedDepth := in.curBlockDepth
	savedEntry := in.curBlock
	in.curBlock = entry
	in.curBlockDepth = depth
	err := in.renderBlockBody(def.node, ctx)
	in.curBlock = savedEntry
	in.curBlockDepth = savedDepth
	return err
}

// renderBlockBody renders a block node's body. A brace-body block (Int==0) emits
// its items; a shortcut block (Int==1) prints its single value expression (spec
// 01 Section 5.2). A leading KindParams/KindType signature child is skipped.
func (in *interp) renderBlockBody(n *ast.Node, ctx *runtime.Scope) error {
	// Record the block UNIT at the definition actually rendered (anchored under its
	// own template name via the node's Src), so an override counts under the child
	// and a never-overridden parent block under the parent. parent() renders the
	// next definition down and counts it too.
	in.covUnit(n, cover.UnitBlock)
	body := n.Children
	for len(body) > 0 && (body[0].Kind == ast.KindParams || body[0].Kind == ast.KindType) {
		body = body[1:]
	}
	if n.Int == 1 { // shortcut value form: @block title "x"
		if len(body) == 0 {
			return nil
		}
		v, err := in.eval(body[len(body)-1], ctx, false)
		if err != nil {
			return err
		}
		return posErr(n, in.emit(v))
	}
	return in.execItems(body, ctx)
}

// callParent renders the parent's version of the block currently being rendered
// (spec 01 Section 5.2). It is legal only inside an overriding block; it renders
// the next definition down the chain and returns the captured output.
func (in *interp) callParent(n *ast.Node, ctx *runtime.Scope) (runtime.Value, error) {
	if in.curBlock == nil {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"parent() is only valid inside an overriding block"))
	}
	entry := in.curBlock
	depth := in.curBlockDepth + 1
	sub := &captureSink{}
	saved := in.out
	in.out = sub
	err := in.renderBlockAt(entry, depth, ctx)
	in.out = saved
	if err != nil {
		return runtime.Null(), err
	}
	if in.escape != "" {
		return runtime.Safe(sub.b.String()), nil
	}
	return runtime.Str(sub.b.String()), nil
}

// callBlock renders a named block of this template (block("x")) or another
// (block("x", "other.ql")), spec 03 Section 3.2 / 01 Section 5.2. With no second
// argument it renders from the current merged block table.
func (in *interp) callBlock(n *ast.Node, ctx *runtime.Scope) (runtime.Value, error) {
	args, err := in.collectArgs(n, ctx, nil)
	if err != nil {
		return runtime.Null(), err
	}
	if len(args) < 1 {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"block() requires a block name"))
	}
	name, err := runtime.ToText(args[0])
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	sub := &captureSink{}
	saved := in.out
	in.out = sub
	if len(args) >= 2 && !args[1].IsNull() {
		// block(name, other): render the named block of another template.
		other, terr := runtime.ToText(args[1])
		if terr != nil {
			in.out = saved
			return runtime.Null(), posErr(n, terr)
		}
		tmpl, lerr := in.eng.LoadTemplate(other)
		if lerr != nil {
			in.out = saved
			return runtime.Null(), posErr(n, lerr)
		}
		node, ok := tmpl.Block(name)
		if !ok {
			in.out = saved
			return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
				"template %q has no block %q", other, name))
		}
		err = in.renderBlockBody(node, ctx)
	} else if entry, ok := in.blocks[name]; ok {
		err = in.renderBlockAt(entry, 0, ctx)
	} else {
		in.out = saved
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"no block %q", name))
	}
	in.out = saved
	if err != nil {
		return runtime.Null(), err
	}
	if in.escape != "" {
		return runtime.Safe(sub.b.String()), nil
	}
	return runtime.Str(sub.b.String()), nil
}

// loadImport binds a file-scope @import (namespace) or @from (selective) into
// the macro namespace (spec 01 Section 5.4). The imported template's macros
// become callable; @import also binds a namespace name in the context for the
// ns.macro() dotted form. It reports the scope bind a successful @import
// applied, so the static composition memo can replay that one per-render side
// effect; a @from, an unresolvable source, and a failed load all bind nothing
// into the scope and report ok false.
func (in *interp) loadImport(imp *ast.Node, ctx *runtime.Scope) (nsBind, bool) {
	name, ok := in.importSourceName(imp, ctx)
	if !ok {
		return nsBind{}, false
	}
	var src *Template
	if name == "_self" {
		src = in.root
	} else {
		t, err := in.eng.LoadTemplate(name)
		if err != nil {
			return nsBind{}, false
		}
		src = t
	}
	// A macro imported from src may contain literal `matches` patterns; absorb
	// src's Prepare-compiled regexp cache so those reuse one compile too.
	in.absorb(src)
	switch imp.Kind {
	case ast.KindImport:
		// @import src as alias: bind a namespace object and expose nothing by bare
		// name (the dotted form ns.macro() is the access path).
		ctx.Set(imp.Str, runtime.Obj(&importNS{tmpl: src}))
		return nsBind{name: imp.Str, tmpl: src}, true
	case ast.KindFrom:
		// @from src import a, b as c: bind each selected macro under its (aliased)
		// name in the macro namespace.
		for _, item := range imp.Children[1:] {
			if item.Kind != ast.KindFromItem {
				continue
			}
			node, ok := src.Macro(item.Str)
			if !ok {
				continue
			}
			local := item.Str
			if item.Bool {
				local = item.Child(0).Str // the alias KindName
			}
			// A @from into a render whose root declares no macros binds the render's
			// first macro, so it creates the lazily-built namespace map here.
			if in.macros == nil {
				in.macros = map[string]*macroEntry{}
			}
			in.macros[local] = &macroEntry{home: src, node: node}
		}
	}
	return nsBind{}, false
}

// importSourceName extracts the import/from source: a _self special name or a
// string-coerced expression (child 0).
func (in *interp) importSourceName(imp *ast.Node, ctx *runtime.Scope) (string, bool) {
	src := imp.Child(0)
	if src == nil {
		return "", false
	}
	if src.Kind == ast.KindSpecialName && src.Str == "_self" {
		return "_self", true
	}
	v, err := in.eval(src, ctx, false)
	if err != nil {
		return "", false
	}
	name, err := runtime.ToText(v)
	if err != nil {
		return "", false
	}
	return name, true
}

// callMacro invokes a macro from the current namespace by name (spec 01 Section
// 5.3). A macro sees ONLY its parameters, defaults, variadics, the macro
// namespace, and globals -- never the caller's locals.
func (in *interp) callMacro(n *ast.Node, name string, ctx *runtime.Scope) (runtime.Value, error) {
	entry, ok := in.macros[name]
	if !ok {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime, "unknown macro %q", name))
	}
	pos, named, err := in.collectArgsNamed(n, ctx)
	if err != nil {
		return runtime.Null(), err
	}
	return in.invokeMacro(n, entry, pos, named)
}

// callMacroIn invokes a macro defined in template home (the ns.macro() and
// _self.macro() paths).
func (in *interp) callMacroIn(n *ast.Node, home *Template, name string, ctx *runtime.Scope) (runtime.Value, error) {
	node, ok := home.Macro(name)
	if !ok {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"template %q has no macro %q", home.Name, name))
	}
	pos, named, err := in.collectArgsNamed(n, ctx)
	if err != nil {
		return runtime.Null(), err
	}
	return in.invokeMacro(n, &macroEntry{home: home, node: node}, pos, named)
}

// invokeMacro binds positional arguments to the macro's parameters by index,
// then overlays named arguments onto the matching parameter by NAME (applying
// constant defaults and a variadic tail), renders the body in an isolated scope,
// and returns the captured output as a Str (or Safe under escaping). The macro
// namespace of the macro's home template is made visible inside the body so a
// macro can call itself or a sibling by bare name (spec 01 Section 5.3). Named
// args bind by parameter name and may appear in any order; an unknown name, or a
// name that duplicates a parameter already filled positionally, is an error
// (design/expressions.md Section 7).
func (in *interp) invokeMacro(n *ast.Node, entry *macroEntry, args []runtime.Value, named []namedArg) (runtime.Value, error) {
	// Consume the caller() frame a @call staged for THIS invocation up front, so it
	// is cleared on every exit path (including an argument-binding error) and never
	// leaks into a later ordinary macro call.
	activeCaller := in.pendingCaller
	in.pendingCaller = nil

	params := entry.node.Child(0) // KindParams
	scope := runtime.NewScope()

	// Map each named argument to its parameter. A "**name" kwargs tail, when
	// present, absorbs any named argument that matches no declared parameter into a
	// mapping (symmetric with the "...name" positional variadic); without one, an
	// unmatched name is a typo and is rejected up front so it never silently lands
	// in the wrong slot or falls through to a default.
	paramIndex := map[string]int{}
	var variadicName, kwargsName string
	for i, p := range params.Children {
		paramIndex[p.Str] = i
		switch {
		case p.Bool:
			variadicName = p.Str
		case p.Int&ast.ParamKwargs != 0:
			kwargsName = p.Str
		}
	}
	namedByParam := map[string]runtime.Value{}
	kwargs := runtime.NewArray()
	for _, na := range named {
		_, declared := paramIndex[na.name]
		toTail := na.name == variadicName || na.name == kwargsName
		// A named argument matching no ordinary parameter flows into the kwargs tail
		// when one is declared; otherwise it is an unknown-parameter error. The tail's
		// own name and the positional variadic name are never bindable by a caller.
		if !declared || toTail {
			if kwargsName == "" || toTail {
				return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
					"macro %q has no parameter %q", entry.node.Str, na.name))
			}
			if _, dup := kwargs.GetStr(na.name); dup {
				return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
					"duplicate named argument %q", na.name))
			}
			kwargs.SetStr(na.name, na.val)
			continue
		}
		if _, dup := namedByParam[na.name]; dup {
			return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
				"duplicate named argument %q", na.name))
		}
		namedByParam[na.name] = na.val
	}

	pi := 0
	for _, p := range params.Children {
		if p.Int&ast.ParamKwargs != 0 { // "**name" binds the collected excess named args
			scope.Set(p.Str, runtime.Arr(kwargs))
			continue
		}
		if p.Bool { // variadic ...rest captures the remaining positional args as a list
			rest := runtime.NewArray()
			j := int64(0)
			for ; pi < len(args); pi++ {
				rest.SetInt(j, args[pi])
				j++
			}
			scope.Set(p.Str, runtime.Arr(rest))
			continue
		}
		// Positional binding takes the parameter's slot first; a named arg for the
		// SAME parameter is then a double-bind error.
		if pi < len(args) {
			if _, dup := namedByParam[p.Str]; dup {
				return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
					"parameter %q given both positionally and by name", p.Str))
			}
			scope.Set(p.Str, args[pi])
			pi++
			continue
		}
		// Then a named argument bound by parameter name, regardless of order.
		if nv, ok := namedByParam[p.Str]; ok {
			scope.Set(p.Str, nv)
			continue
		}
		// Apply a constant default if present (Int has ParamHasDefault).
		if p.Int&ast.ParamHasDefault != 0 {
			defChild := p.Child(0)
			if p.Int&ast.ParamHasType != 0 {
				defChild = p.Child(1) // type is child 0, default child 1
			}
			dv, err := in.eval(defChild, scope, false)
			if err != nil {
				return runtime.Null(), err
			}
			scope.Set(p.Str, dv)
			continue
		}
		scope.Set(p.Str, runtime.Null())
	}

	// Coverage: the macro body is being invoked, so record its unit at the macro
	// node (anchored under the home template's name via the node's own Src), and
	// seed only the invoked macro's subtree in its home so an imported macro's home
	// counts even when never rendered as a root -- WITHOUT seeding the home's
	// top-level body, which an import never renders and would otherwise report as an
	// uncovered gap. Both are no-ops when coverage is off. A home that is also
	// entered as a render root / @include / @extends target is fully seeded
	// separately by covSeed, and that full seed takes precedence.
	in.covSeedMacro(entry.home, entry.node)
	in.covUnit(entry.node, cover.UnitMacro)

	// Render the macro body in a child interp that sees the home template's macro
	// namespace (so recursion / sibling calls by bare name work), with the macro's
	// home as root for _self. The caller() binding is set for THIS invocation only
	// (from a staged @call frame, or cleared for an ordinary call) and restored
	// afterward, so caller() reaches the macro the @call names but not the macros it
	// transitively calls (design/composition, call blocks).
	saved := in.swapMacroHome(entry.home)
	savedCaller := in.caller
	in.caller = activeCaller
	body := macroBody(entry.node)
	out, err := in.captureItems(body, scope)
	in.caller = savedCaller
	in.restoreMacroHome(saved)
	if err != nil {
		return runtime.Null(), err
	}
	if in.escape != "" {
		return runtime.Safe(out), nil
	}
	return runtime.Str(out), nil
}

// macroBody returns the body items of a macro node (after the KindParams child
// and an optional return KindType).
func macroBody(n *ast.Node) []*ast.Node {
	body := n.Children
	if len(body) > 0 && body[0].Kind == ast.KindParams {
		body = body[1:]
	}
	if len(body) > 0 && body[0].Kind == ast.KindType {
		body = body[1:]
	}
	return body
}

// swapMacroHome makes the home template's macro namespace visible for a macro
// body render and returns the previous state for restoration. The macros of the
// home template are merged over the current set so a sibling/self call resolves.
func (in *interp) swapMacroHome(home *Template) macroHomeState {
	prevMacros := in.macros
	prevRoot := in.root
	merged := map[string]*macroEntry{}
	for k, v := range prevMacros {
		merged[k] = v
	}
	for _, name := range home.macroOrder {
		node, _ := home.Macro(name)
		merged[name] = &macroEntry{home: home, node: node}
	}
	in.macros = merged
	in.root = home
	return macroHomeState{macros: prevMacros, root: prevRoot}
}

func (in *interp) restoreMacroHome(s macroHomeState) {
	in.macros = s.macros
	in.root = s.root
}

type macroHomeState struct {
	macros map[string]*macroEntry
	root   *Template
}

// execInclude renders an @include statement (spec 01 Section 5.6). It resolves
// the source (a candidate list selects the first existing), builds the child
// context per with/only, tolerates a miss under ignore-missing, and splices the
// rendered output.
func (in *interp) execInclude(n *ast.Node, ctx *runtime.Scope) error {
	out, err := in.renderInclude(n, ctx)
	if err != nil {
		return err
	}
	return posErr(n, in.emitString(out))
}

// renderInclude does the include work and returns the rendered string. It is
// shared by the @include statement and could back the include() function.
func (in *interp) renderInclude(n *ast.Node, ctx *runtime.Scope) (string, error) {
	flags := n.Int
	srcExpr := n.Child(0)
	var withExpr *ast.Node
	if flags&ast.IncWith != 0 {
		withExpr = n.Child(1) // the with-map child follows the source
	}

	name, found, err := in.resolveCandidates(srcExpr, ctx)
	if err != nil {
		return "", err
	}
	if !found {
		if flags&ast.IncIgnoreMissing != 0 {
			return "", nil
		}
		return "", posErr(n, errors.New(errors.KindRuntime,
			"included template not found"))
	}

	tmpl, err := in.eng.LoadTemplate(name)
	if err != nil {
		if flags&ast.IncIgnoreMissing != 0 {
			return "", nil
		}
		return "", posErr(n, err)
	}

	// Build the child context: "only" starts empty, otherwise inherit the current
	// context; a with-map adds/overrides vars (spec 01 Section 5.6).
	var childCtx *runtime.Scope
	if flags&ast.IncOnly != 0 {
		childCtx = runtime.NewScope()
	} else {
		childCtx = ctx.Child()
	}
	if withExpr != nil {
		wv, err := in.eval(withExpr, ctx, false)
		if err != nil {
			return "", err
		}
		if wv.Kind == runtime.KArray && wv.Arr != nil {
			for _, p := range wv.Arr.Pairs() {
				key, err := runtime.ToText(p.Key)
				if err != nil {
					return "", err
				}
				childCtx.Set(key, p.Val)
			}
		}
	}

	// Render the included template in a fresh sub-interp so its inheritance,
	// blocks, and macros do not leak into the includer (design/composition 5.6).
	// The sandbox gate propagates INTO the include: an include inside an active
	// sandbox stays sandboxed, and that never turns the gate off for the enclosing
	// render (B16). The sub-interp then runs its own Phase-1 check.
	sub := newInterp(in.eng, tmpl, &captureSink{})
	sub.escape = in.escape
	sub.sandboxOn = sub.sandboxOn || in.sandboxOn
	// Share the parent render's slot state so a @provide in the partial feeds the
	// parent's @yield region and a self-contained partial's own @yield is resolved
	// by the single top-level resolveSlots. The partial writes yield placeholders
	// with the shared token into its captured stream; that stream is spliced into
	// the parent output, and the labels the partial reserved are merged back so the
	// top-level pass substitutes them (design/composition, named accumulating slots).
	sub.shareSlotsFrom(in)
	cs := sub.out.(*captureSink)
	if err := sub.renderTemplate(tmpl, childCtx); err != nil {
		return "", err
	}
	sub.mergeYieldedInto(in)
	return cs.b.String(), nil
}

// resolveCandidates evaluates an include/extends source to a template name,
// taking the first existing entry of a candidate list. found is false when no
// candidate exists (the caller decides ignore-missing vs error).
func (in *interp) resolveCandidates(srcExpr *ast.Node, ctx *runtime.Scope) (name string, found bool, err error) {
	v, err := in.eval(srcExpr, ctx, false)
	if err != nil {
		return "", false, err
	}
	if v.Kind == runtime.KArray && v.Arr != nil {
		for _, p := range v.Arr.Pairs() {
			cand, err := runtime.ToText(p.Val)
			if err != nil {
				return "", false, err
			}
			if in.eng.TemplateExists(cand) {
				return cand, true, nil
			}
		}
		return "", false, nil
	}
	name, err = runtime.ToText(v)
	if err != nil {
		return "", false, err
	}
	return name, in.eng.TemplateExists(name), nil
}

// execEmbed renders an @embed: include the embedded template as an anonymous
// child, with the inline @block definitions overriding the embedded template's
// blocks (spec 01 Section 5.5). It is a focused implementation supporting with/
// only/ignore-missing and block overrides.
func (in *interp) execEmbed(n *ast.Node, ctx *runtime.Scope) error {
	flags := n.Int
	name, found, err := in.resolveCandidates(n.Child(0), ctx)
	if err != nil {
		return err
	}
	if !found {
		if flags&ast.IncIgnoreMissing != 0 {
			return nil
		}
		return posErr(n, errors.New(errors.KindRuntime, "embedded template not found"))
	}
	tmpl, err := in.eng.LoadTemplate(name)
	if err != nil {
		return posErr(n, err)
	}

	// Collect the override blocks declared in the embed body.
	overrides := map[string]*ast.Node{}
	var withExpr *ast.Node
	for i, c := range n.Children {
		if i == 0 {
			continue // the source
		}
		switch c.Kind {
		case ast.KindBlock:
			overrides[c.Str] = c
		default:
			if flags&ast.IncWith != 0 && withExpr == nil && c.Kind != ast.KindBlock {
				withExpr = c
			}
		}
	}

	var childCtx *runtime.Scope
	if flags&ast.IncOnly != 0 {
		childCtx = runtime.NewScope()
	} else {
		childCtx = ctx.Child()
	}
	if withExpr != nil {
		wv, err := in.eval(withExpr, ctx, false)
		if err != nil {
			return err
		}
		if wv.Kind == runtime.KArray && wv.Arr != nil {
			for _, p := range wv.Arr.Pairs() {
				key, _ := runtime.ToText(p.Key)
				childCtx.Set(key, p.Val)
			}
		}
	}

	sub := newInterp(in.eng, tmpl, in.out)
	sub.escape = in.escape
	sub.sandboxOn = sub.sandboxOn || in.sandboxOn // embed inherits the active gate (B16)
	// The embedded template writes into the parent sink directly, so share the
	// parent's slot state: a @provide in the embed feeds the parent's @yield and a
	// self-contained embed's own @yield placeholder is resolved by the single
	// top-level resolveSlots (design/composition, named accumulating slots).
	sub.shareSlotsFrom(in)
	// Build the embedded template's chain and block table, then layer the embed's
	// inline overrides on top (most-derived). This build is always fresh -- it
	// calls the raw builders and never consults tmpl's static composition memo --
	// because the override layering below MUTATES the table it builds (a chain
	// prepend and a node overwrite per override), while the memoized tables are
	// shared read-only across every render of tmpl. Routing an embed through the
	// memo would let one embed's overrides leak into unrelated renders.
	chain, err := sub.buildChain(tmpl, childCtx)
	if err != nil {
		return err
	}
	sub.parentChain = chain
	if err := sub.buildBlockTable(chain); err != nil {
		return err
	}
	for name, node := range overrides {
		def := blockDef{owner: tmpl, node: node}
		if e, ok := sub.blocks[name]; ok {
			e.chain = append([]blockDef{def}, e.chain...)
			e.node = node
		} else {
			// An override onto a blockless embedded template records the sub-render's
			// first definition, so it creates the lazily-built block table here.
			if sub.blocks == nil {
				sub.blocks = map[string]*blockEntry{}
			}
			sub.blocks[name] = &blockEntry{owner: tmpl, node: node, chain: []blockDef{def}}
		}
	}
	sub.loadMacros(tmpl, childCtx)
	// Phase-1 check for the embedded chain when the sandbox is active (B9).
	if sub.sandboxOn {
		for _, t := range chain {
			if err := sub.checkSecurity(t.used); err != nil {
				return err
			}
		}
	}
	top := chain[len(chain)-1]
	err = sub.execItems(top.Module.Children, childCtx)
	sub.mergeYieldedInto(in)
	return err
}
