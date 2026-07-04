package compile

import (
	"github.com/avmnu-sng/quill-template-engine/ast"
)

// stmtEmbed lowers a static "@embed src [with map] [only] [ignore missing] {
// block overrides }" by flattening the embedded template into this render as an
// anonymous derived member, mirroring interp execEmbed. The embed target is
// linked exactly as a Unit entry -- its @extends chain, @use traits, and merged
// block table -- and the inline @block definitions layer over that table
// most-derived first, the compile analog of execEmbed's chain prepend. The
// linked topmost body then lowers with its block sites resolving through this
// embed-local table, under a child frame that models the with/only child scope,
// so the spliced output is byte-identical to the sub-interp execEmbed runs.
//
// Only a single static string-literal source is flattenable: which template to
// embed must be known at compile time so its chain and blocks can be linked. A
// candidate list resolves its winner from the loader at render time and a
// dynamic source is likewise unprovable, so both stay ErrNotCompilable and the
// dispatch gate falls back to the interpreter, which resolves them correctly.
//
// The embed writes into this render's stream directly, so a @provide inside the
// embedded body appends to the render-level slot buffers and a @yield reaches
// the single post-render resolve pass -- the compile analog of execEmbed's
// shareSlotsFrom, where there is one slot map because there is one Render.
// reachesSlots descends into the flattened body so an embed feeding a parent
// @yield forces this render's buffered shape, and a self-contained embed's own
// @yield resolves in the same pass.
func (c *compiler) stmtEmbed(n *ast.Node) error {
	src := n.Child(0)
	if src == nil || src.Kind != ast.KindString {
		return c.notCompilable("@embed with a dynamic source", n)
	}
	name := src.Str
	flags := n.Int

	if c.embedInlining(name) {
		return c.notCompilable("recursive @embed (cycle?)", n)
	}

	sub, err := c.linkEmbed(name, n)
	if err != nil {
		return err
	}
	if sub == nil {
		// The target is not flattenable (absent, composing beyond the linkable
		// subset, or a render-entry stub). Under ignore missing the render is
		// byte-exact to the interpreter's only when the target resolves to
		// nothing at render time; record the assumption for the dispatch gate
		// and emit nothing, exactly like execEmbed's ignore-missing miss.
		// Without ignore missing the interpreter would embed a present target or
		// raise "embedded template not found" for an absent one, neither of which
		// the compiled path can reproduce here, so it defers.
		if flags&ast.IncIgnoreMissing != 0 && !c.embedTargetPresent(name) {
			c.recordAbsentInclude(name)
			return nil
		}
		return c.notCompilable("@embed of a template outside the flattenable subset", n)
	}

	// Register every linked member source, not just the ones whose block bodies
	// actually inline: the embed target's @extends parents and @use traits must
	// all reach the manifest so the interpreter can load the target (which
	// references them) and the dispatch gate can byte-verify the whole embedded
	// composition. A trait block the overrides shadow never inlines, yet the
	// target still @use-s the trait, so its source must be embedded exactly as
	// Unit registers every member.
	for _, m := range sub.members {
		c.registerSrc(m.src)
	}

	// The with-map is evaluated in the CALLER scope before the child frame is
	// pushed, exactly as execEmbed evaluates the with-expr against ctx and only
	// then builds childCtx.
	mapVar := "runtime.Null()"
	if flags&ast.IncWith != 0 {
		mv, err := c.expr(n.Child(1), false)
		if err != nil {
			return err
		}
		mapVar = c.spill(mv)
	}

	body := sub.topmost.mod.Children

	// Swap in the embed-local linker state so block sites in the flattened body
	// resolve through this embed's table rather than the enclosing unit's, then
	// restore afterward. A Module (c.unit nil) gains a table for the embed's
	// duration; a Unit's own table is stashed and restored.
	savedUnit := c.unit
	savedBlockCtx := c.blockCtx
	savedBlockInline := c.blockInline
	c.unit = sub
	c.blockCtx = nil
	c.blockInline = 0
	defer func() {
		c.unit = savedUnit
		c.blockCtx = savedBlockCtx
		c.blockInline = savedBlockInline
	}()

	binds := c.scanBinds(body)
	if err := c.checkBindNames(binds, n); err != nil {
		return err
	}

	c.openf("{")
	// "only" cuts the scope chain at a fresh root, the analog of execEmbed's
	// childCtx = runtime.NewScope(); a plain embed inherits the caller frames,
	// the analog of ctx.Child(), so a bare-name read falls through to the outer
	// frames and emits runtime.ShareValue at the cross-frame boundary.
	kind := frameWith
	if flags&ast.IncOnly != 0 {
		kind = frameWithOnly
	}
	f := c.pushFrame(kind, binds)
	f.withVar = mapVar

	c.pushSrc(sub.topmost.src)
	// The interpreter renders the embed through a fresh sub-interp whose in.root
	// is the embedded template, so a render-root-keyed lowering inside it keys
	// under the embed's name; this inline is a sub-render boundary, matching the
	// @include floor rules.
	c.pushRoot(sub.topmost.src)
	c.pushEmbed(name)
	savedCond := c.condDepth
	c.condDepth = 0
	savedFloor := c.changedFloor
	c.changedFloor = len(c.loops)
	// The flattened body's output is spliced RAW into this render's stream, so a
	// @provide inside it appends to the same render-level slot buffers and a
	// top-level @yield reaches the finished output like a top-level @yield here:
	// the guard stays off so a self-contained embed's own @yield compiles.
	sb, err := c.captureInto(body, false)
	c.changedFloor = savedFloor
	c.condDepth = savedCond
	c.popEmbed()
	c.popRoot()
	c.popSrc()
	c.popFrame()
	if err != nil {
		c.closeb()
		return err
	}
	c.emitEmbedSplice(sb, n.Line)
	c.closeb()
	return nil
}

// emitEmbedSplice writes a flattened embed body's captured output into the
// parent stream the way execEmbed does: with the active @tab indent SUSPENDED.
// The interpreter renders an embed through a sub-interp whose own indent starts
// empty writing to the shared sink, so the embedded body is emitted raw even
// inside a @tab region, and the parent's line-start cursor is untouched by the
// sub's writes -- so the statement AFTER the embed indents exactly as it would
// have without the embed. A tab-free unit has no indent layer, so the raw
// io.WriteString already matches; otherwise the writer's indent and line-start
// are saved, the indent blanked for the splice, and both restored, reproducing
// the interpreter's frozen parent cursor. This is the one place embed and
// include diverge: include splices through emitString and inherits the caller
// indent, while embed's shared-sink sub-interp does not.
func (c *compiler) emitEmbedSplice(sb string, line int) {
	wrap := func(e string) string { return c.qposE(e, line) }
	if c.tabFree {
		e := c.tmp("qe")
		c.openf("if _, %s := io.WriteString(%s, %s.String()); %s != nil {", e, c.writer(), sb, e)
		c.linef(c.ret(wrap(e)))
		c.closeb()
		return
	}
	w := c.writer()
	si := c.tmp("qs")
	sa := c.tmp("qk")
	c.linef("%s, %s := %s.indent, %s.atLineStart", si, sa, w, w)
	c.linef("%s.indent = \"\"", w)
	e := c.tmp("qe")
	c.openf("if %s := %s.WriteString(%s.String()); %s != nil {", e, w, sb, e)
	c.linef(c.ret(wrap(e)))
	c.closeb()
	c.linef("%s.indent, %s.atLineStart = %s, %s", w, w, si, sa)
}

// linkEmbed links an embed target as an anonymous derived member: it resolves
// the target's static composition through linkUnit (its @extends chain, @use
// traits, and merged block table) and prepends the embed's inline @block
// definitions to that table as the most-derived override of each name, the
// compile analog of execEmbed's chain prepend after buildBlockTable. The
// override nodes are authored in the embed site's own template, so their owner
// carries the embed-site source and an error inside an override body cites that
// template and line, exactly as the override node's own Src drives the
// interpreter's position.
//
// It returns nil (not an error) when the target is not flattenable so the site
// can defer to the interpreter: a target absent from the compile set, a target
// whose composition sits outside linkUnit's provable subset (macros, imports, a
// parent or trait outside the set, a load-gate rejection), or a target that
// links to a deterministic render-entry stub. Only a linker error that is not a
// typed subset rejection is surfaced, because it signals a genuine generation
// fault rather than an unsupported construct.
func (c *compiler) linkEmbed(name string, site *ast.Node) (*unitInfo, error) {
	if _, ok := c.includeTemplate(name); !ok {
		return nil, nil
	}
	sub, err := linkUnit(name, c.includeTemplates, c.opts)
	if err != nil {
		var nce *NotCompilableError
		if asNotCompilable(err, &nce) {
			return nil, nil
		}
		// linkUnit wraps an entry outside the templates map in a plain error; an
		// entry present in includeTemplates never triggers that path, so any
		// remaining error is a real fault worth surfacing.
		return nil, err
	}
	// A deterministic render-entry error (a too-deep chain, a non-traitable
	// @use) the interpreter would raise inside the embed cannot be reproduced
	// byte-exact from this splice point, so the site defers to the interpreter.
	if sub.stub != nil {
		return nil, nil
	}
	for _, ov := range embedOverrides(site) {
		// The override is authored in the embed site's template, so its owner
		// carries the override node's own source; an error inside the override
		// body then cites that template and line, matching the interpreter,
		// which positions through the override node's Src.
		owner := &utpl{name: embedNodeSourceName(ov), src: ov.Src}
		def := unitBlockDef{owner: owner, node: ov}
		if e, ok := sub.blocks[ov.Str]; ok {
			e.chain = append([]unitBlockDef{def}, e.chain...)
		} else {
			sub.blocks[ov.Str] = &unitBlockEntry{chain: []unitBlockDef{def}}
		}
	}
	return sub, nil
}

// embedNodeSourceName returns the template name an override node was authored
// in, for the anonymous owner's name; it is a diagnostic label only, since the
// owner's source drives every generated error position.
func embedNodeSourceName(n *ast.Node) string {
	if n.Src != nil {
		return n.Src.Name()
	}
	return ""
}

// embedOverrides returns the inline @block override nodes of an embed site in
// source order, the definitions execEmbed layers over the embedded template's
// own blocks.
func embedOverrides(n *ast.Node) []*ast.Node {
	var out []*ast.Node
	for i, c := range n.Children {
		if i == 0 {
			continue // the source expression
		}
		if c.Kind == ast.KindBlock {
			out = append(out, c)
		}
	}
	return out
}

// embedTargetPresent reports whether an embed target is present in the compile
// set, so an ignore-missing embed of an ABSENT target lowers to nothing (the
// manifest records the assumption for the dispatch gate) while an ignore-missing
// embed of a present-but-unflattenable target still defers to the interpreter,
// which would embed it.
func (c *compiler) embedTargetPresent(name string) bool {
	_, ok := c.includeTemplate(name)
	return ok
}

// pushEmbed marks name as an actively flattening embed so a nested embed of the
// same template is rejected as a cycle.
func (c *compiler) pushEmbed(name string) {
	c.embedStack = append(c.embedStack, name)
}

// popEmbed unwinds the innermost active embed.
func (c *compiler) popEmbed() {
	c.embedStack = c.embedStack[:len(c.embedStack)-1]
}

// embedInlining reports whether name is already being flattened at an enclosing
// embed site, so a direct or transitive self-embed is a typed subset rejection
// instead of unbounded compile-time expansion.
func (c *compiler) embedInlining(name string) bool {
	for _, n := range c.embedStack {
		if n == name {
			return true
		}
	}
	return false
}
