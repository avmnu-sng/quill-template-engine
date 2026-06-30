package interp

import (
	"github.com/avmnusng/quill-template-engine/ast"
	"github.com/avmnusng/quill-template-engine/errors"
	"github.com/avmnusng/quill-template-engine/runtime"
)

// execBlockSite renders an @block at its definition site. It resolves the name
// through the merged block table so an override in a more-derived template wins;
// the resolved definition's body is rendered at the most-derived (index 0)
// position of the name's definition chain (spec 01 Section 5.2, design/
// composition Section 2.5).
func (in *interp) execBlockSite(n *ast.Node, ctx *runtime.Context) error {
	entry, ok := in.blocks[n.Str]
	if !ok {
		// No table entry (e.g. an embed-local block); render the node's own body.
		return in.renderBlockBody(n, ctx)
	}
	return in.renderBlockAt(entry, 0, ctx)
}

// renderBlockAt renders the depth-th definition in a block's chain (0 is the
// most-derived override). parent() inside that body renders depth+1.
func (in *interp) renderBlockAt(entry *blockEntry, depth int, ctx *runtime.Context) error {
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
func (in *interp) renderBlockBody(n *ast.Node, ctx *runtime.Context) error {
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
func (in *interp) callParent(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
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
	if in.escape == "html" {
		return runtime.Safe(sub.b.String()), nil
	}
	return runtime.Str(sub.b.String()), nil
}

// callBlock renders a named block of this template (block("x")) or another
// (block("x", "other.ql")), spec 03 Section 3.2 / 01 Section 5.2. With no second
// argument it renders from the current merged block table.
func (in *interp) callBlock(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
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
	if in.escape == "html" {
		return runtime.Safe(sub.b.String()), nil
	}
	return runtime.Str(sub.b.String()), nil
}

// loadImport binds a file-scope @import (namespace) or @from (selective) into
// the macro namespace (spec 01 Section 5.4). The imported template's macros
// become callable; @import also binds a namespace name in the context for the
// ns.macro() dotted form.
func (in *interp) loadImport(imp *ast.Node, ctx *runtime.Context) {
	name, ok := in.importSourceName(imp, ctx)
	if !ok {
		return
	}
	var src *Template
	if name == "_self" {
		src = in.root
	} else {
		t, err := in.eng.LoadTemplate(name)
		if err != nil {
			return
		}
		src = t
	}
	switch imp.Kind {
	case ast.KindImport:
		// @import src as alias: bind a namespace object and expose nothing by bare
		// name (the dotted form ns.macro() is the access path).
		ctx.Set(imp.Str, runtime.Obj(&importNS{tmpl: src}))
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
			in.macros[local] = &macroEntry{home: src, node: node}
		}
	}
}

// importSourceName extracts the import/from source: a _self special name or a
// string-coerced expression (child 0).
func (in *interp) importSourceName(imp *ast.Node, ctx *runtime.Context) (string, bool) {
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
func (in *interp) callMacro(n *ast.Node, name string, ctx *runtime.Context) (runtime.Value, error) {
	entry, ok := in.macros[name]
	if !ok {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime, "unknown macro %q", name))
	}
	args, err := in.collectArgs(n, ctx, nil)
	if err != nil {
		return runtime.Null(), err
	}
	return in.invokeMacro(n, entry, args)
}

// callMacroIn invokes a macro defined in template home (the ns.macro() and
// _self.macro() paths).
func (in *interp) callMacroIn(n *ast.Node, home *Template, name string, ctx *runtime.Context) (runtime.Value, error) {
	node, ok := home.Macro(name)
	if !ok {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"template %q has no macro %q", home.Name, name))
	}
	args, err := in.collectArgs(n, ctx, nil)
	if err != nil {
		return runtime.Null(), err
	}
	return in.invokeMacro(n, &macroEntry{home: home, node: node}, args)
}

// invokeMacro binds the positional arguments to the macro's parameters (applying
// constant defaults and a variadic tail), renders the body in an isolated scope,
// and returns the captured output as a Str (or Safe under escaping). The macro
// namespace of the macro's home template is made visible inside the body so a
// macro can call itself or a sibling by bare name (spec 01 Section 5.3).
func (in *interp) invokeMacro(n *ast.Node, entry *macroEntry, args []runtime.Value) (runtime.Value, error) {
	params := entry.node.Child(0) // KindParams
	scope := runtime.NewContext()

	pi := 0
	for _, p := range params.Children {
		if p.Bool { // variadic ...rest captures the remaining args as a list
			rest := runtime.NewArray()
			j := int64(0)
			for ; pi < len(args); pi++ {
				rest.SetInt(j, args[pi])
				j++
			}
			scope.Set(p.Str, runtime.Arr(rest))
			continue
		}
		if pi < len(args) {
			scope.Set(p.Str, args[pi])
			pi++
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

	// Render the macro body in a child interp that sees the home template's macro
	// namespace (so recursion / sibling calls by bare name work), with the macro's
	// home as root for _self.
	saved := in.swapMacroHome(entry.home)
	body := macroBody(entry.node)
	out, err := in.captureItems(body, scope)
	in.restoreMacroHome(saved)
	if err != nil {
		return runtime.Null(), err
	}
	if in.escape == "html" {
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
func (in *interp) execInclude(n *ast.Node, ctx *runtime.Context) error {
	out, err := in.renderInclude(n, ctx)
	if err != nil {
		return err
	}
	return posErr(n, in.emitString(out))
}

// renderInclude does the include work and returns the rendered string. It is
// shared by the @include statement and could back the include() function.
func (in *interp) renderInclude(n *ast.Node, ctx *runtime.Context) (string, error) {
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
	var childCtx *runtime.Context
	if flags&ast.IncOnly != 0 {
		childCtx = runtime.NewContext()
	} else {
		childCtx = ctx.Clone()
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
	sub := newInterp(in.eng, tmpl, &captureSink{})
	sub.escape = in.escape
	cs := sub.out.(*captureSink)
	if err := sub.renderTemplate(tmpl, childCtx); err != nil {
		return "", err
	}
	return cs.b.String(), nil
}

// resolveCandidates evaluates an include/extends source to a template name,
// taking the first existing entry of a candidate list. found is false when no
// candidate exists (the caller decides ignore-missing vs error).
func (in *interp) resolveCandidates(srcExpr *ast.Node, ctx *runtime.Context) (name string, found bool, err error) {
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
func (in *interp) execEmbed(n *ast.Node, ctx *runtime.Context) error {
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

	var childCtx *runtime.Context
	if flags&ast.IncOnly != 0 {
		childCtx = runtime.NewContext()
	} else {
		childCtx = ctx.Clone()
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
	// Build the embedded template's chain and block table, then layer the embed's
	// inline overrides on top (most-derived).
	chain, err := sub.buildChain(tmpl, childCtx)
	if err != nil {
		return err
	}
	sub.parentChain = chain
	sub.buildBlockTable(chain)
	for name, node := range overrides {
		def := blockDef{owner: tmpl, node: node}
		if e, ok := sub.blocks[name]; ok {
			e.chain = append([]blockDef{def}, e.chain...)
			e.node = node
		} else {
			sub.blocks[name] = &blockEntry{owner: tmpl, node: node, chain: []blockDef{def}}
		}
	}
	sub.loadMacros(tmpl, childCtx)
	top := chain[len(chain)-1]
	return sub.execItems(top.Module.Children, childCtx)
}
