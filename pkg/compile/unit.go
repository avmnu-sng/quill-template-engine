package compile

import (
	"fmt"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/check"
	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// maxUnitInline bounds compile-time block-body inlining depth. A composition
// that inlines deeper than this is recursive (a block rendering itself through
// its own chain or a block() call), which the interpreter would re-render
// without bound, so the compiler rejects it as a typed subset error instead of
// expanding code forever.
const maxUnitInline = 64

// utpl is one linked member template of a Unit compilation: its parsed module
// plus the composition index the interpreter's Prepare would build, mirrored
// here because compile must not import interp.
type utpl struct {
	name string
	mod  *ast.Node
	src  *source.Source

	// blocks maps a block name to its defining node with nested blocks
	// flattened, blockOrder preserving first-seen order, exactly like the
	// interpreter's Template.index.
	blocks     map[string]*ast.Node
	blockOrder []string

	macros      map[string]*ast.Node
	macroOrder  []string
	extendsNode *ast.Node
	imports     []*ast.Node
	uses        []*ast.Node
}

// index mirrors interp's Template.index: blocks are recorded recursively so a
// nested @block is a flat top-level entry, a later same-name macro or block
// wins, and the last @extends head is the one that counts.
func (t *utpl) index(n *ast.Node) {
	for _, c := range n.Children {
		switch c.Kind {
		case ast.KindBlock:
			if _, seen := t.blocks[c.Str]; !seen {
				t.blockOrder = append(t.blockOrder, c.Str)
			}
			t.blocks[c.Str] = c
			t.index(c)
		case ast.KindMacro:
			if _, seen := t.macros[c.Str]; !seen {
				t.macroOrder = append(t.macroOrder, c.Str)
			}
			t.macros[c.Str] = c
		case ast.KindExtends:
			t.extendsNode = c
		case ast.KindImport, ast.KindFrom:
			t.imports = append(t.imports, c)
		case ast.KindUse:
			t.uses = append(t.uses, c)
		}
	}
}

// traitable mirrors interp's Template.Traitable: a trait has no parent, no
// macros, and no free body content beyond whitespace-only text.
func (t *utpl) traitable() bool {
	if t.extendsNode != nil || len(t.macros) != 0 {
		return false
	}
	for _, c := range t.mod.Children {
		switch c.Kind {
		case ast.KindBlock, ast.KindUse:
			// Allowed: block definitions and nested trait uses.
		case ast.KindText:
			if strings.TrimLeft(strings.TrimSpace(c.Str), "\ufeff") != "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// indexUnitTemplate builds the composition index of one member module.
func indexUnitTemplate(name string, mod *ast.Node) *utpl {
	t := &utpl{
		name:   name,
		mod:    mod,
		src:    moduleSource(name, mod),
		blocks: map[string]*ast.Node{},
		macros: map[string]*ast.Node{},
	}
	t.index(mod)
	return t
}

// unitBlockDef is one definition of a block name: the member template that
// defines it and the defining node, mirroring interp's blockDef.
type unitBlockDef struct {
	owner *utpl
	node  *ast.Node
}

// unitBlockEntry is the resolved definition chain of one block name, most
// derived first, mirroring interp's blockEntry.
type unitBlockEntry struct {
	chain []unitBlockDef
}

// unitBlockCtx is the compile-time equivalent of the interpreter's
// curBlock/curBlockDepth pair while a definition body is inlined.
type unitBlockCtx struct {
	entry *unitBlockEntry
	depth int
}

// unitStub records a composition-build error the interpreter raises at render
// entry, before any output byte: the generated render function returns exactly
// this error, so error text and (absent) partial output stay byte-identical.
// A nil src leaves the error unpositioned, matching the interpreter's
// chain-depth error.
type unitStub struct {
	msg  string
	src  *source.Source
	line int
}

// unitInfo is the static-linker result one Unit compilation lowers against:
// the resolved inheritance chain, the merged block table, every member
// template in link order, and the whole-unit lowering decisions (the tab-free
// writer split and the composition-bind union the frame prescans consume).
type unitInfo struct {
	entry   *utpl
	chain   []*utpl
	topmost *utpl
	members []*utpl
	byName  map[string]*utpl
	blocks  map[string]*unitBlockEntry

	tabFree   bool
	usesSlots bool
	compBinds []string
	stub      *unitStub
}

// appendBlockDef mirrors interp's appendBlockDef: the first definition of a
// name is the most-derived override, later ones extend the parent() chain.
func (u *unitInfo) appendBlockDef(name string, def unitBlockDef) {
	if e, ok := u.blocks[name]; ok {
		e.chain = append(e.chain, def)
		return
	}
	u.blocks[name] = &unitBlockEntry{chain: []unitBlockDef{def}}
}

// Unit compiles a multi-template unit to one Go source file: the entry
// template plus every template its static composition references (@extends
// parents, @use traits, and block(name, "other") targets), with the
// most-derived block bodies inlined into the topmost parent's statement list
// at compile time, exactly where the interpreter's merged block table would
// resolve them. parent() lowers to an inline capture of the next definition
// down its chain, and error positions cite the defining member template's
// name and line, so output and error text stay byte-identical to the facade's
// for every compiled construct.
//
// The entry names the template a by-name render serves; templates maps every
// unit member name to its parsed module (extra entries are ignored). The
// composition must be static: a dynamic @extends operand, a candidate list
// whose first candidate is not a member, a member referenced but missing from
// templates, macros or imports on the entry, and every construct outside
// Module's compilable subset return a typed *NotCompilableError naming the
// construct. A composition the interpreter rejects at render entry with a
// deterministic error (a non-traitable @use target, an invalid trait alias, a
// too-deep inheritance chain) compiles to a render function returning exactly
// that error.
//
// The generated manifest embeds every member template's source, so the
// Environment's dispatch gate (quill.WithCompiled) byte-verifies the whole
// unit against its loader before serving the compiled render. Byte parity
// carries Module's one documented exception, unseeded randomness.
func Unit(entry string, templates map[string]*ast.Node, opts Options) (*Result, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	u, err := linkUnit(entry, templates, opts)
	if err != nil {
		return nil, err
	}
	c := newCompiler(u.entry.src, opts)
	c.unit = u
	// A Unit's own template set is the include-resolution universe: a static
	// @include in any member inlines a partial from here exactly as a Module
	// inlines from Options.Templates.
	if c.includeTemplates == nil {
		c.includeTemplates = templates
	}
	for _, m := range u.members {
		c.registerSrc(m.src)
	}
	if err := c.compileUnit(); err != nil {
		return nil, err
	}
	return c.finish()
}

// linkUnit resolves the entry's static composition: the inheritance chain,
// the merged block table with traits and aliases, and the block(name, other)
// targets, loading each referenced member exactly once through the facade's
// load-time gates (the gradual type checker and the literal-regex validation).
func linkUnit(entry string, templates map[string]*ast.Node, opts Options) (*unitInfo, error) {
	u := &unitInfo{byName: map[string]*utpl{}, blocks: map[string]*unitBlockEntry{}}

	load := func(name, construct string, from *utpl, at *ast.Node) (*utpl, error) {
		if t, ok := u.byName[name]; ok {
			return t, nil
		}
		mod, ok := templates[name]
		if !ok || mod == nil || mod.Kind != ast.KindModule {
			e := &NotCompilableError{Construct: fmt.Sprintf("%s target %q outside the unit", construct, name)}
			if from != nil {
				e.Template = from.name
			}
			if at != nil {
				e.Line = at.Line
			}
			return nil, e
		}
		// The facade's load-time gates run per member exactly as LoadTemplate
		// would run them on the interpreter path.
		if err := check.Check(mod, opts.Types); err != nil {
			return nil, err
		}
		if err := checkLiteralRegexps(mod); err != nil {
			return nil, err
		}
		t := indexUnitTemplate(name, mod)
		u.byName[name] = t
		u.members = append(u.members, t)
		return t, nil
	}

	et, err := load(entry, "unit entry", nil, nil)
	if err != nil {
		var nce *NotCompilableError
		if asNotCompilable(err, &nce) {
			return nil, fmt.Errorf("compile: unit entry %q is not among the templates", entry)
		}
		return nil, err
	}
	u.entry = et

	// The render's macro namespace is the entry's own macros plus its imports
	// (interp loadMacros); a non-empty namespace changes bare-name call
	// resolution in ways the lowering cannot see, so both are typed subset
	// rejections. Non-entry members' declarations are inert for this render.
	if len(et.macroOrder) > 0 {
		return nil, &NotCompilableError{Construct: "@macro", Template: et.name, Line: et.macros[et.macroOrder[0]].Line}
	}
	if len(et.imports) > 0 {
		imp := et.imports[0]
		construct := "@import"
		if imp.Kind == ast.KindFrom {
			construct = "@from"
		}
		return nil, &NotCompilableError{Construct: construct, Template: et.name, Line: imp.Line}
	}

	// The inheritance chain, mirroring interp buildChain: most-derived first,
	// depth-limited with the interpreter's exact error text.
	u.chain = []*utpl{et}
	cur := et
	for cur.extendsNode != nil && u.stub == nil {
		name, stub, err := staticExtendsTarget(cur, templates)
		if err != nil {
			return nil, err
		}
		if stub != nil {
			u.stub = stub
			break
		}
		parent, err := load(name, "@extends", cur, cur.extendsNode)
		if err != nil {
			return nil, err
		}
		u.chain = append(u.chain, parent)
		if len(u.chain) > 64 {
			u.stub = &unitStub{msg: "inheritance chain too deep (cycle?)"}
			break
		}
		cur = parent
	}
	u.topmost = u.chain[len(u.chain)-1]

	// The merged block table, mirroring interp buildBlockTable: each chain
	// member's own definitions first, then its traits in source order.
	if u.stub == nil {
	table:
		for _, t := range u.chain {
			for _, name := range t.blockOrder {
				u.appendBlockDef(name, unitBlockDef{owner: t, node: t.blocks[name]})
			}
			stub, err := u.mergeTraits(t, load, 0)
			if err != nil {
				return nil, err
			}
			if stub != nil {
				u.stub = stub
				break table
			}
		}
	}

	// Discover block(name, "other") targets so their templates join the unit
	// before lowering decisions (tab-free, prescan union) are made. members
	// grows while walking, which links targets referenced from targets.
	if u.stub == nil {
		for i := 0; i < len(u.members); i++ {
			m := u.members[i]
			var derr error
			discoverBlockCallTargets(m.mod, func(other string, at *ast.Node) {
				if derr != nil {
					return
				}
				if _, ok := templates[other]; !ok {
					// A target outside the templates map may sit in a dropped
					// region the render never reaches; the lowering rejects it
					// if it is actually reachable.
					return
				}
				if _, err := load(other, `function "block"`, m, at); err != nil {
					derr = err
				}
			})
			if derr != nil {
				return nil, derr
			}
		}
	}

	u.tabFree = true
	for _, m := range u.members {
		if hasTabBlock(m.mod) {
			u.tabFree = false
			break
		}
	}
	// A slot construct in any member -- or in a partial an inlined @include from
	// any member pulls in -- forces the buffered path: the render inlines member
	// block bodies and include partials, so a placeholder can enter the stream
	// from any of them, and buffering a slot-free member is byte-invisible.
	for _, m := range u.members {
		if reachesSlots(m.mod, templates) {
			u.usesSlots = true
			break
		}
	}
	u.compBinds = unitCompositionBinds(u.members)
	return u, nil
}

// asNotCompilable reports whether err is a *NotCompilableError, filling target.
func asNotCompilable(err error, target **NotCompilableError) bool {
	nce, ok := err.(*NotCompilableError)
	if ok {
		*target = nce
	}
	return ok
}

// staticExtendsTarget resolves one @extends operand statically: a string
// literal names the parent, a literal candidate list resolves to its first
// candidate when that candidate is a unit member (the dispatch gate proves
// members loadable at render time, so the interpreter picks the same one),
// and an empty candidate list reproduces the interpreter's render error.
// Anything else is outside the provable subset.
func staticExtendsTarget(cur *utpl, templates map[string]*ast.Node) (string, *unitStub, error) {
	op := cur.extendsNode.Child(0)
	dynamic := func() error {
		return &NotCompilableError{Construct: "@extends with a dynamic source", Template: cur.name, Line: cur.extendsNode.Line}
	}
	if op == nil {
		return "", nil, dynamic()
	}
	switch op.Kind {
	case ast.KindString:
		return op.Str, nil, nil
	case ast.KindList:
		if len(op.Children) == 0 {
			return "", &unitStub{msg: "none of the candidate parent templates exist", src: cur.src, line: cur.extendsNode.Line}, nil
		}
		for _, cand := range op.Children {
			if cand == nil || cand.Kind != ast.KindString {
				return "", nil, dynamic()
			}
		}
		first := op.Children[0].Str
		if _, ok := templates[first]; !ok {
			// The interpreter takes the first candidate the loader serves; a
			// first candidate outside the unit could exist there, so which
			// parent wins is unprovable at compile time.
			return "", nil, &NotCompilableError{Construct: "@extends candidate list whose first candidate is outside the unit", Template: cur.name, Line: cur.extendsNode.Line}
		}
		return first, nil, nil
	default:
		return "", nil, dynamic()
	}
}

// aliasPair is one @use rename in source order, so alias validation reports
// deterministically.
type aliasPair struct {
	orig  string
	local string
}

// mergeTraits mirrors interp's mergeTraits over the static table: nested
// traits flatten first, aliases rebind trait blocks, and every render error
// the interpreter raises deterministically becomes a stub. Trait cycles,
// which the interpreter would recurse into without bound, are a typed subset
// rejection at a fixed depth.
func (u *unitInfo) mergeTraits(t *utpl, load func(string, string, *utpl, *ast.Node) (*utpl, error), depth int) (*unitStub, error) {
	if depth > maxUnitInline {
		return nil, &NotCompilableError{Construct: "@use nesting beyond depth 64 (trait cycle?)", Template: t.name}
	}
	for _, use := range t.uses {
		src := use.Child(0)
		if src == nil || src.Kind != ast.KindString {
			return &unitStub{msg: "a use target must be a constant string", src: t.src, line: use.Line}, nil
		}
		trait, err := load(src.Str, "@use", t, use)
		if err != nil {
			return nil, err
		}
		if !trait.traitable() {
			return &unitStub{msg: fmt.Sprintf("template %q cannot be used as a trait", src.Str), src: t.src, line: use.Line}, nil
		}
		stub, err := u.mergeTraits(trait, load, depth+1)
		if err != nil || stub != nil {
			return stub, err
		}
		aliases, aliasList, stub := unitUseAliases(use, t)
		if stub != nil {
			return stub, nil
		}
		for _, name := range trait.blockOrder {
			local := name
			if a, ok := aliases[name]; ok {
				local = a
			}
			u.appendBlockDef(local, unitBlockDef{owner: trait, node: trait.blocks[name]})
		}
		for _, ap := range aliasList {
			if _, ok := trait.blocks[ap.orig]; !ok {
				return &unitStub{msg: fmt.Sprintf("block %q is not defined in trait %q", ap.orig, src.Str), src: t.src, line: use.Line}, nil
			}
		}
	}
	return nil, nil
}

// unitUseAliases mirrors interp's useAliases, additionally returning the
// aliases in source order for deterministic validation.
func unitUseAliases(use *ast.Node, t *utpl) (map[string]string, []aliasPair, *unitStub) {
	aliases := map[string]string{}
	var ordered []aliasPair
	if !use.Bool { // no with-map
		return aliases, ordered, nil
	}
	mapNode := use.Child(1)
	for _, entry := range mapNode.Children {
		switch entry.Int {
		case ast.MapEntryKeyed:
			key := entry.Child(0)   // KindString trait block name
			alias := entry.Child(1) // alias value
			if alias.Kind != ast.KindName && alias.Kind != ast.KindString {
				return nil, nil, &unitStub{msg: "a trait alias must be a bare name or string", src: t.src, line: use.Line}
			}
			aliases[key.Str] = alias.Str
			ordered = append(ordered, aliasPair{orig: key.Str, local: alias.Str})
		case ast.MapEntryShorthand:
			name := entry.Child(0).Str
			aliases[name] = name
			ordered = append(ordered, aliasPair{orig: name, local: name})
		default:
			return nil, nil, &unitStub{msg: "invalid trait alias entry", src: t.src, line: use.Line}
		}
	}
	return aliases, ordered, nil
}

// discoverBlockCallTargets walks one member module for block(name, "other")
// calls with a literal template argument, reporting each target so the linker
// can pin the referenced template into the unit.
func discoverBlockCallTargets(n *ast.Node, report func(other string, at *ast.Node)) {
	if n == nil {
		return
	}
	if n.Kind == ast.KindCall {
		if callee := n.Child(0); callee != nil && callee.Kind == ast.KindName && callee.Str == "block" {
			if _, other, ok := staticBlockCallTarget(n); ok && other != "" {
				report(other, n)
			}
		}
	}
	for _, c := range n.Children {
		discoverBlockCallTargets(c, report)
	}
}

// staticBlockCallTarget recognizes the statically resolvable block() call
// shape: one or two positional string-literal arguments (a literal null second
// argument selects the one-argument form, like the interpreter's IsNull test).
func staticBlockCallTarget(n *ast.Node) (name, other string, ok bool) {
	var args []*ast.Node
	for _, ch := range n.Children {
		if ch.Kind != ast.KindArg {
			continue
		}
		if ch.Int != ast.ArgPositional {
			return "", "", false
		}
		args = append(args, ch.Child(0))
	}
	if len(args) == 0 || len(args) > 2 {
		return "", "", false
	}
	if args[0] == nil || args[0].Kind != ast.KindString {
		return "", "", false
	}
	name = args[0].Str
	if len(args) == 2 && args[1] != nil && args[1].Kind != ast.KindNull {
		if args[1].Kind != ast.KindString {
			return "", "", false
		}
		other = args[1].Str
	}
	return name, other, true
}

// unitCompositionBinds unions the names every block definition body in the
// unit binds in its enclosing scope. Blocks bind in the frame their site sits
// in (execBlockSite passes ctx through), so any frame whose statements contain
// a composition construct prescans this union; a name that never binds at
// runtime keeps its flag false and reads fall through, exactly like a name
// whose bind sites sit in a non-executed branch.
func unitCompositionBinds(members []*utpl) []string {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, m := range members {
		collectBlockBinds(m.mod, add)
	}
	return out
}

// collectBlockBinds feeds add the binds of every @block body under n: the
// statement binds of a brace body, or the inline-assignment binds of a
// shortcut value.
func collectBlockBinds(n *ast.Node, add func(string)) {
	if n == nil {
		return
	}
	if n.Kind == ast.KindBlock {
		body := unitBlockBody(n)
		if n.Int == 1 {
			if len(body) > 0 {
				exprBinds(body[len(body)-1], add)
			}
		} else {
			for _, name := range bindNames(body) {
				add(name)
			}
		}
	}
	for _, c := range n.Children {
		collectBlockBinds(c, add)
	}
}

// unitBlockBody returns a block node's body items after the optional leading
// signature children, mirroring interp's renderBlockBody slicing.
func unitBlockBody(n *ast.Node) []*ast.Node {
	body := n.Children
	for len(body) > 0 && body[0] != nil && (body[0].Kind == ast.KindParams || body[0].Kind == ast.KindType) {
		body = body[1:]
	}
	return body
}

// nodeContainsComposition reports whether the subtree at n contains a block
// site or a parent()/block() call, the constructs that splice foreign block
// bodies (and their binds) into the enclosing frame.
func nodeContainsComposition(n *ast.Node) bool {
	if n == nil {
		return false
	}
	switch n.Kind {
	case ast.KindBlock:
		return true
	case ast.KindCall:
		if callee := n.Child(0); callee != nil && callee.Kind == ast.KindName &&
			(callee.Str == "parent" || callee.Str == "block") {
			return true
		}
	}
	for _, c := range n.Children {
		if nodeContainsComposition(c) {
			return true
		}
	}
	return false
}

// nodesContainComposition reports nodeContainsComposition over a statement list.
func nodesContainComposition(items []*ast.Node) bool {
	for _, it := range items {
		if nodeContainsComposition(it) {
			return true
		}
	}
	return false
}

// scanBinds prescans a statement list for the names it binds in its own frame.
// In a Unit compilation, a list containing a composition construct also
// prescans the whole-unit block-bind union, because an inlined block body
// binds into the frame its site sits in.
func (c *compiler) scanBinds(items []*ast.Node) []string {
	binds := bindNames(items)
	if c.unit != nil && nodesContainComposition(items) {
		binds = dedupe(append(binds, c.unit.compBinds...))
	}
	return binds
}

// compileUnit lowers the linked unit: a stubbed composition emits its exact
// build error, anything else lowers the topmost parent's statement list with
// block sites inlined through the merged table.
func (c *compiler) compileUnit() error {
	u := c.unit
	c.tabFree = u.tabFree
	if u.stub != nil {
		c.tabFree = true
	}
	c.usesSlots = u.usesSlots
	c.setTopWriter()
	if u.stub != nil {
		c.emitStub(u.stub)
		return nil
	}
	mod := u.topmost.mod
	c.an = analyzeUnitLoops(mod, u, c.includeTemplates)
	c.pushSrc(u.topmost.src)
	defer c.popSrc()
	binds := c.scanBinds(mod.Children)
	if err := c.checkBindNames(binds, mod); err != nil {
		return err
	}
	c.pushFrame(frameRoot, binds)
	return c.stmtList(mod.Children)
}

// emitStub lowers a composition-build error: the render function returns the
// interpreter's exact render-entry error before writing any output.
func (c *compiler) emitStub(s *unitStub) {
	c.openf("if true {")
	errExpr := fmt.Sprintf("qerrors.New(qerrors.KindRuntime, \"%%s\", %s)", q(s.msg))
	if s.src != nil {
		c.linef(c.ret(fmt.Sprintf("qpos(%s, %s, %d)", errExpr, c.srcVars[s.src], s.line)))
	} else {
		c.linef(c.ret(errExpr))
	}
	c.closeb()
}

// unitBlockSite lowers one @block statement: a table entry inlines its most
// derived definition, a name with no entry (an embed-local block shape)
// renders its own body without touching the parent() context, exactly like
// execBlockSite.
func (c *compiler) unitBlockSite(n *ast.Node) error {
	if e, ok := c.unit.blocks[n.Str]; ok {
		return c.unitLowerBlockAt(e, 0, n)
	}
	return c.unitLowerBlockBody(n)
}

// unitLowerBlockAt inlines the depth-th definition of a block chain (0 is the
// most-derived override), switching the error-position source to the defining
// template and establishing the parent() context, exactly like renderBlockAt.
func (c *compiler) unitLowerBlockAt(e *unitBlockEntry, depth int, site *ast.Node) error {
	if depth >= len(e.chain) {
		return nil
	}
	if c.blockInline >= maxUnitInline {
		return c.notCompilable("block inlining beyond depth 64 (recursive block composition)", site)
	}
	c.blockInline++
	def := e.chain[depth]
	c.blockCtx = append(c.blockCtx, unitBlockCtx{entry: e, depth: depth})
	c.pushSrc(def.owner.src)
	err := c.unitLowerBlockBody(def.node)
	c.popSrc()
	c.blockCtx = c.blockCtx[:len(c.blockCtx)-1]
	c.blockInline--
	return err
}

// unitLowerBlockBody lowers a block node's body, mirroring renderBlockBody: a
// shortcut block (Int==1) emits its value expression at the block's position,
// a brace body lowers its items in the shared enclosing frame.
func (c *compiler) unitLowerBlockBody(n *ast.Node) error {
	body := unitBlockBody(n)
	if n.Int == 1 {
		if len(body) == 0 {
			return nil
		}
		return c.stmtEmitValue(body[len(body)-1], n)
	}
	return c.stmtList(body)
}

// unitCaptureBlock lowers a captured block render (parent() and block()): the
// body writes into a fresh builder while the qWriter state threads through,
// because the interpreter swaps only the sink and keeps the shared indent and
// line-start cursor live across the capture. The captured text becomes Safe
// under an active escape strategy, else Str, exactly like callParent.
func (c *compiler) unitCaptureBlock(lower func() error) (string, error) {
	sb := c.tmp("qs")
	c.linef("var %s strings.Builder", sb)
	var cw string
	if c.tabFree {
		c.writers = append(c.writers, "&"+sb)
	} else {
		w := c.writer()
		cw = c.tmp("qcw")
		c.linef("%s := &qWriter{w: &%s, indent: %s.indent, atLineStart: %s.atLineStart}", cw, sb, w, w)
		c.linef("_ = %s", cw)
		c.writers = append(c.writers, cw)
	}
	c.captureDepth++
	err := lower()
	c.captureDepth--
	c.writers = c.writers[:len(c.writers)-1]
	if err != nil {
		return "", err
	}
	if !c.tabFree {
		// The interpreter's line-start cursor is shared state that survives
		// the sink swap; copy it back so the next write after the capture
		// indents exactly as the interpreter's would.
		c.linef("%s.atLineStart = %s.atLineStart", c.writer(), cw)
	}
	if c.escapeStrategy() != "" {
		return fmt.Sprintf("runtime.Safe(%s.String())", sb), nil
	}
	return fmt.Sprintf("runtime.Str(%s.String())", sb), nil
}

// unitParentCall lowers parent(): the next definition down the active block
// chain renders into a capture. Outside an overriding block it reproduces the
// interpreter's error; arguments are never evaluated, exactly like callParent.
func (c *compiler) unitParentCall(n *ast.Node) (string, error) {
	if c.inArrow > 0 {
		return "", c.notCompilable(`function "parent" inside an arrow function`, n)
	}
	if len(c.blockCtx) == 0 {
		return c.emitGuardedError(`qerrors.New(qerrors.KindRuntime, "parent() is only valid inside an overriding block")`, n.Line), nil
	}
	top := c.blockCtx[len(c.blockCtx)-1]
	return c.unitCaptureBlock(func() error {
		return c.unitLowerBlockAt(top.entry, top.depth+1, n)
	})
}

// unitBlockCall lowers block(name) and block(name, "other") with literal
// arguments: the named chain (or the other template's own definition) renders
// into a capture, with the interpreter's exact miss errors. A dynamic name or
// template argument selects among templates at render time, which the static
// linker cannot prove, so it is a typed subset rejection.
func (c *compiler) unitBlockCall(n *ast.Node) (string, error) {
	if c.inArrow > 0 {
		return "", c.notCompilable(`function "block" inside an arrow function`, n)
	}
	var args []*ast.Node
	for _, ch := range n.Children {
		if ch.Kind != ast.KindArg {
			continue
		}
		if ch.Int != ast.ArgPositional {
			return "", c.notCompilable(`function "block" with a non-positional argument`, n)
		}
		args = append(args, ch.Child(0))
	}
	if len(args) == 0 {
		return c.emitGuardedError(`qerrors.New(qerrors.KindRuntime, "block() requires a block name")`, n.Line), nil
	}
	if len(args) > 2 {
		return "", c.notCompilable(`function "block" with more than two arguments`, n)
	}
	if args[0] == nil || args[0].Kind != ast.KindString {
		return "", c.notCompilable(`function "block" with a dynamic block name`, n)
	}
	name := args[0].Str
	if len(args) == 2 && args[1] != nil && args[1].Kind != ast.KindNull {
		if args[1].Kind != ast.KindString {
			return "", c.notCompilable(`function "block" with a dynamic template name`, n)
		}
		other := args[1].Str
		t, ok := c.unit.byName[other]
		if !ok {
			return "", c.notCompilable(fmt.Sprintf(`function "block" targeting %q outside the unit`, other), n)
		}
		node, ok := t.blocks[name]
		if !ok {
			return c.emitGuardedError(fmt.Sprintf("qerrors.New(qerrors.KindRuntime, \"template %%q has no block %%q\", %s, %s)", q(other), q(name)), n.Line), nil
		}
		// block(name, other) renders the other template's own definition body
		// without switching the parent() context (renderBlockBody is called
		// directly, so curBlock stays the enclosing block's).
		return c.unitCaptureBlock(func() error {
			c.pushSrc(t.src)
			err := c.unitLowerBlockBody(node)
			c.popSrc()
			return err
		})
	}
	e, ok := c.unit.blocks[name]
	if !ok {
		return c.emitGuardedError(fmt.Sprintf("qerrors.New(qerrors.KindRuntime, \"no block %%q\", %s)", q(name)), n.Line), nil
	}
	return c.unitCaptureBlock(func() error {
		return c.unitLowerBlockAt(e, 0, n)
	})
}

// emitGuardedError lowers an unconditional positioned error return behind an
// if-true guard (so the code after it stays reachable for vet), yielding a
// null value local for expression positions.
func (c *compiler) emitGuardedError(errExpr string, line int) string {
	res := c.tmp("qt")
	c.linef("%s := runtime.Null()", res)
	c.openf("if true {")
	c.linef(c.ret(c.qposE(errExpr, line)))
	c.closeb()
	return res
}
