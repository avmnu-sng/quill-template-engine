package compile

import (
	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/check"
	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// stmtInclude lowers a static "@include expr [with map] [only] [ignore missing]"
// by inlining the included partial's statements into this render, mirroring
// interp renderInclude primitive for primitive. The partial renders into a
// fresh builder with indentation suspended (the captureItems shape), under a
// pushed compile frame that models the include child scope, and its captured
// text is emitted raw through the active writer at the include site: the
// analog of renderInclude's captureSink render followed by execInclude's
// emitString. The child scope inherits the active escape strategy, so the
// partial's interpolations escape under the caller's strategy and the spliced
// block is emitted without a second escape, exactly like emitString.
//
// Only a single static string-literal source is inlinable: the partial's
// statements must be known at compile time. A candidate list resolves its
// winner from the loader at render time, so which partial to inline is not
// statically decidable; a dynamic source is likewise unprovable. Both stay
// ErrNotCompilable, and the dispatch gate falls back to the interpreter, which
// resolves them correctly.
//
// A partial that defers a slot inlines like any other: because its statements
// enter this same render, its @provide appends to the render-level slot buffers
// and its @yield reaches the single post-render resolve pass: the compiled
// analog of interp shareSlotsFrom, where there is one slot map because there is
// one Render. reachesSlots therefore forces this render's buffered shape whenever
// an inlinable partial defers a slot, and the include capture stays transparent
// to the @yield guard.
func (c *compiler) stmtInclude(n *ast.Node) error {
	src := n.Child(0)
	if src == nil || src.Kind != ast.KindString {
		return c.notCompilable("@include with a dynamic source", n)
	}
	name := src.Str
	flags := n.Int

	mod, known := c.includeTemplate(name)
	if !known {
		// The partial's statements are not available to inline. Under ignore
		// missing the render is byte-exact to the interpreter's exactly when the
		// target resolves to nothing at render time; the emitted manifest records
		// the assumption so the dispatch gate proves it still holds and otherwise
		// falls back. Without ignore missing the interpreter would raise
		// "included template not found" for an absent target or inline a present
		// one, neither of which the compiled path can reproduce, so it defers.
		if flags&ast.IncIgnoreMissing != 0 {
			c.recordAbsentInclude(name)
			return nil
		}
		return c.notCompilable("@include of a template outside the compile set", n)
	}
	// The interpreter renders the partial through the full renderTemplate, which
	// composes its own inheritance chain, block table, macro namespace, and
	// imports. Inlining the raw body reproduces that only for a composition-free
	// partial; a partial carrying @extends/@block/@macro/@import/@from/@use/@embed
	// would render its composition, not its literal body, so it defers to the
	// interpreter. This also keeps a Unit from misrouting the partial's heads
	// through its own linked block table.
	if partialHasComposition(mod) {
		return c.notCompilable("@include of a partial that composes other templates", n)
	}
	if c.includeInlining(name) {
		return c.notCompilable("recursive @include (cycle?)", n)
	}
	// The interpreter loads the partial through LoadTemplate, which runs the
	// gradual type checker and the literal-regex validation; a partial that
	// fails either would make the interpreter's include raise that load error
	// (or, under ignore missing, tolerate it), so a partial the gates reject is
	// deferred to the interpreter, which reproduces the exact outcome.
	if err := check.Check(mod, c.opts.Types); err != nil {
		return c.notCompilable("@include of a partial the load-time gates reject", n)
	}
	if err := checkLiteralRegexps(mod); err != nil {
		return c.notCompilable("@include of a partial the load-time gates reject", n)
	}

	// The with-map is evaluated in the CALLER scope before the child frame is
	// pushed, exactly as renderInclude evaluates withExpr against ctx and only
	// then builds childCtx.
	mapVar := "runtime.Null()"
	if flags&ast.IncWith != 0 {
		mv, err := c.expr(n.Child(1), false)
		if err != nil {
			return err
		}
		mapVar = c.spill(mv)
		// The spilled with-map temp is referenced only when a name resolves
		// through the with-frame, so a target that reads none would leave it
		// declared-and-unused. Discard it, mirroring the render header's qw/qNames
		// guards, so every with-map include lowers to compiling Go.
		c.linef("_ = %s", mapVar)
	}

	body := mod.Children
	binds := c.scanBinds(body)
	if err := c.checkBindNames(binds, n); err != nil {
		return err
	}

	c.openf("{")
	// "only" cuts the scope chain at a fresh root, the analog of renderInclude's
	// childCtx = runtime.NewScope(); a plain include inherits the caller frames,
	// the analog of ctx.Child(), so a bare-name read falls through to the outer
	// frames and emits runtime.ShareValue at the cross-frame boundary.
	kind := frameWith
	if flags&ast.IncOnly != 0 {
		kind = frameWithOnly
	}
	f := c.pushFrame(kind, binds)
	f.withVar = mapVar

	partialSrc := c.includeSource(name, mod)
	c.pushSrc(partialSrc)
	// The interpreter renders the partial through a fresh sub-interp whose in.root
	// is the partial, so a render-root-keyed lowering inside it (a @cache) keys
	// under the partial's name. Push the partial as the render root for the same
	// reason the loop-change floor rises: this inline is a sub-render boundary.
	c.pushRoot(partialSrc)
	c.pushInclude(name)
	savedCond := c.condDepth
	c.condDepth = 0
	// The interpreter renders the partial through a fresh sub-interpreter whose
	// loop.changed memory stack starts empty, so a loop.changed inside the partial
	// cannot reach a caller @for across this boundary. Raising the floor to the
	// current loop depth makes currentChangedLoop hide every enclosing caller loop
	// from the inlined body, so a partial that opens no @for of its own reproduces
	// the interpreter's "only available inside a for loop" error; the partial's own
	// @for still pushes above the floor and tracks changes normally.
	savedFloor := c.changedFloor
	c.changedFloor = len(c.loops)
	// The partial's captured output is spliced RAW into this render's stream, so
	// a @provide inside it appends to the same render-level slot buffers and a
	// top-level @yield reaches the finished output like a top-level @yield here:
	// the capture is transparent to the @yield guard, which only rejects a @yield
	// whose placeholder would be folded into a consumed value.
	sb, err := c.captureInto(body, false)
	c.changedFloor = savedFloor
	c.condDepth = savedCond
	c.popInclude()
	c.popRoot()
	c.popSrc()
	c.popFrame()
	if err != nil {
		c.closeb()
		return err
	}
	// The captured block is spliced raw through the active writer, positioned at
	// the include site, byte-identical to execInclude's emitString(out).
	c.emitWrite(sb+".String()", func(e string) string { return c.qposE(e, n.Line) })
	c.closeb()
	return nil
}

// partialHasComposition reports whether an included partial carries a
// composition head the interpreter's renderTemplate would resolve differently
// from a straight body render: an @extends chain, a @block/@macro definition, an
// @import/@from namespace, an @use trait, or a nested @embed. A nested @include
// is not one of these; it lowers through stmtInclude on its own terms. The
// walk descends the whole subtree so a head buried inside an @if or @for arm is
// caught too.
func partialHasComposition(n *ast.Node) bool {
	if n == nil {
		return false
	}
	switch n.Kind {
	case ast.KindExtends, ast.KindBlock, ast.KindMacro,
		ast.KindImport, ast.KindFrom, ast.KindUse, ast.KindEmbed:
		return true
	}
	for _, ch := range n.Children {
		if partialHasComposition(ch) {
			return true
		}
	}
	return false
}

// includeTemplate resolves a static include name to the partial module whose
// statements get inlined, reporting whether it is available to inline. The
// entry template is never inlined into itself through the include map: a
// self-include is caught as a cycle by the include stack instead.
func (c *compiler) includeTemplate(name string) (*ast.Node, bool) {
	mod, ok := c.includeTemplates[name]
	if !ok || mod == nil || mod.Kind != ast.KindModule {
		return nil, false
	}
	return mod, true
}

// includeSource returns the source anchor for an inlined partial, registered
// once per name so a partial included at several sites shares one qSrc variable
// and one manifest source entry, and error positions inside the inlined body
// cite the partial's own template name and line.
func (c *compiler) includeSource(name string, mod *ast.Node) *source.Source {
	if s, ok := c.includeSrcs[name]; ok {
		return s
	}
	s := moduleSource(name, mod)
	c.includeSrcs[name] = s
	return s
}

// pushInclude marks name as an actively inlining include so a nested include of
// the same template is rejected as a cycle.
func (c *compiler) pushInclude(name string) {
	c.includeStack = append(c.includeStack, name)
}

// popInclude unwinds the innermost active include.
func (c *compiler) popInclude() {
	c.includeStack = c.includeStack[:len(c.includeStack)-1]
}

// includeInlining reports whether name is already being inlined at an enclosing
// include site, so a direct or transitive self-include is a typed subset
// rejection instead of unbounded compile-time expansion.
func (c *compiler) includeInlining(name string) bool {
	for _, n := range c.includeStack {
		if n == name {
			return true
		}
	}
	return false
}

// recordAbsentInclude notes that an ignore-missing @include resolved to a
// compile-time-absent target, so the emitted manifest can carry the name for
// the dispatch gate to prove still absent before serving the compiled render.
func (c *compiler) recordAbsentInclude(name string) {
	if c.absentIncludes == nil {
		c.absentIncludes = map[string]struct{}{}
	}
	c.absentIncludes[name] = struct{}{}
}
