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
	in.buildBlockTable(chain)
	in.loadMacros(tmpl, ctx)

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
// Section 2.5).
func (in *interp) buildBlockTable(chain []*Template) {
	in.blocks = map[string]*blockEntry{}
	for _, t := range chain {
		for _, name := range t.BlockNames() {
			node, _ := t.Block(name)
			def := blockDef{owner: t, node: node}
			if e, ok := in.blocks[name]; ok {
				e.chain = append(e.chain, def)
			} else {
				in.blocks[name] = &blockEntry{owner: t, node: node, chain: []blockDef{def}}
			}
		}
	}
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
	case ast.KindTypes, ast.KindDeprecated, ast.KindLine, ast.KindSandbox, ast.KindCache:
		// Type declarations, deprecation diagnostics, line resets, sandbox, and
		// cache are parsed but their runtime effects are deferred this slice; they
		// emit nothing and (for sandbox/cache) render their body transparently.
		if n.Kind == ast.KindSandbox {
			return in.execItems(n.Children, ctx)
		}
		if n.Kind == ast.KindCache {
			return in.execItems(n.Children[n.Int:], ctx)
		}
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

// bindPattern binds a list-destructuring pattern from a sequence value. Only the
// list form is supported this slice; map destructuring is deferred.
func (in *interp) bindPattern(pat *ast.Node, v runtime.Value, ctx *runtime.Context) error {
	if pat.Kind != ast.KindListPattern {
		return posErr(pat, errors.New(errors.KindRuntime,
			"map destructuring is not implemented in this milestone"))
	}
	if v.Kind != runtime.KArray || v.Arr == nil {
		return posErr(pat, errors.New(errors.KindRuntime,
			"destructuring expects a sequence"))
	}
	ps := v.Arr.Pairs()
	for i, slot := range pat.Children {
		if slot == nil { // elided slot
			continue
		}
		var val runtime.Value = runtime.Null()
		if i < len(ps) {
			val = ps[i].Val
		}
		name := slot.Str
		if slot.Kind == ast.KindName || slot.Kind == ast.KindTarget {
			ctx.Set(name, val)
		}
	}
	return nil
}

// execCapture renders the body to a string-like value and binds it (spec 01
// Section 4.3). Under escaping off it is a plain Str; under escaping on it is a
// Safe value.
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
	if in.escape == "html" {
		ctx.Set(n.Str, runtime.Safe(out))
	} else {
		ctx.Set(n.Str, runtime.Str(out))
	}
	return nil
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
	return posErr(n, in.emit(v))
}

// execEscape sets the active strategy for the region, then restores it (spec 01
// Section 4.7). "off" / "raw" disables escaping; "html" enables the html
// strategy; other strategies are deferred.
func (in *interp) execEscape(n *ast.Node, ctx *runtime.Context) error {
	saved := in.escape
	switch n.Str {
	case "off", "raw", "none":
		in.escape = ""
	case "html":
		in.escape = "html"
	default:
		return posErr(n, errors.New(errors.KindRuntime,
			"escape strategy %q is not implemented; only html and off are available", n.Str))
	}
	err := in.execItems(n.Children, ctx)
	in.escape = saved
	return err
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
