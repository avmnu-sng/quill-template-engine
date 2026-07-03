// Package interp is Quill's tree-walking renderer. It evaluates expressions over
// the runtime ops (package runtime), drives the control-flow and composition
// statements, and pushes bytes to an output sink. It implements the render half
// of spec 01 Sections 4-5, spec 04 Sections 4-8, and the stdlib calling surface
// of spec 03, against the AST that package parse produces.
//
// The interpreter is deliberately a tree walker, not a compiler: the AST is
// small and uniform (one Node kind per construct), so a single switch on Kind
// per evaluation step is clear and fast enough for a source generator's
// workload. Correctness lives in package runtime (one equality, one ordering,
// one truthiness, one coercion); this package only sequences those operations
// and manages scope, output, and the Template composition contract.
//
// The engine handle (the Engine interface) lets the interpreter load other
// templates (for @extends/@include/@import) and consult the callable registry
// without importing the top-level facade, which would be a cycle. The facade in
// package quill implements Engine.
package interp

import (
	"io"
	"log"
	"regexp"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/cache"
	"github.com/avmnu-sng/quill-template-engine/cover"
	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/ext"
	"github.com/avmnu-sng/quill-template-engine/runtime"
	"github.com/avmnu-sng/quill-template-engine/sandbox"
)

// Engine is the interpreter's view of the surrounding environment. The quill
// facade implements it. It supplies the callable registry, the strictness and
// autoescape options, and the means to load and prepare another template by name
// (used by inheritance, includes, and imports).
type Engine interface {
	// Extensions returns the callable registry (core plus host).
	Extensions() *ext.ExtensionSet
	// StrictVariables reports whether an undefined read is an error (the default)
	// or a silent Null (lenient migration mode), spec 04 Section 6.
	StrictVariables() bool
	// AutoescapeHTML reports whether the default output strategy is html rather
	// than off; the default is off (spec 04 Section 8.1).
	AutoescapeHTML() bool
	// LoadTemplate parses and prepares the named template, returning its Template
	// (with block and macro tables built). Misses are loader not-found errors.
	LoadTemplate(name string) (*Template, error)
	// TemplateExists reports whether the named template can be loaded, for
	// candidate lists and ignore-missing.
	TemplateExists(name string) bool
	// RawSource returns the unparsed source text of the named template, backing
	// the source() function (spec 03 Section 3.2). ok is false on a miss.
	RawSource(name string) (string, bool)
	// CompileString parses and prepares an ad-hoc template body under the given
	// name, backing template_from_string (spec 03 Section 3.3). The body is not
	// added to the loader.
	CompileString(name, body string) (*Template, error)
	// RandomSeed returns the host-configured RNG seed and whether one was set.
	// When set, the seedable randomness functions (random, shuffle) become
	// deterministic, backing test reproducibility (spec 03 Section 3.2, X15).
	RandomSeed() (int64, bool)
	// RenderCache returns the engine's rendered-body cache, backing the @cache
	// region statement (spec 01 Section 4.7). It is the pluggable cache surface;
	// the engine default is an in-memory store.
	RenderCache() *cache.RenderCache
	// Policy returns the host-supplied sandbox security policy, or nil when none
	// was configured (spec 04 Section 8.3). It is consulted only when the sandbox
	// is active (a global toggle, the @sandbox region, or a sandboxed include).
	Policy() *sandbox.Policy
	// SandboxActive reports whether the sandbox is globally on for every render
	// (the always-on activation path, design/escaping-safety Section 6.2). The
	// @sandbox region and sandboxed includes turn it on locally regardless.
	SandboxActive() bool
	// Coverage returns the host-attached coverage Collector, or nil when coverage
	// is off. When nil the interpreter's cov field is nil and every coverage hook
	// is a single nil-check, the zero-overhead-when-disabled guarantee (package
	// cover, docs/coverage.md). When set, each render seeds its templates and
	// records a hit at every coverable point, unioning across renders.
	Coverage() *cover.Collector
	// TabWidth returns the number of spaces one indent level expands to for the
	// tab filter, the tab/space/break indentation functions, and the @tab region
	// (the host knob WithTabWidth, default 4).
	TabWidth() int
	// Logger returns the host-attached log sink @log writes to. It is never nil;
	// a host that configured none gets a discarding logger, so @log always has a
	// valid destination and produces no rendered output.
	Logger() *log.Logger
}

// Sink is the push-based output target. The interpreter writes rendered bytes as
// it walks; a strings.Builder is the usual sink (see Render).
type Sink interface {
	WriteString(s string) (int, error)
}

// Render renders tmpl with the given top-level variables and returns the output
// string. It is the entry the facade calls; it resolves the inheritance chain,
// builds the merged block table, and walks the root (or the topmost parent).
func Render(eng Engine, tmpl *Template, vars map[string]runtime.Value) (string, error) {
	return renderBuffered(eng, tmpl, vars, false)
}

// outHintCap bounds the pre-Grow renderBuffered takes from a Template's
// remembered output length, so one huge historical render cannot make every
// later render of that template pre-commit an arbitrarily large buffer; an
// output beyond the cap simply resumes the doubling ladder from the cap.
const outHintCap = 1 << 20

// outGrowHint converts a Template's remembered output length into the Builder
// pre-Grow size: the remembered length itself, bounded by outHintCap. A
// non-positive length (no successful buffered render yet, or a render that
// produced nothing) yields zero, meaning the builder starts empty exactly as
// an unhinted render would.
func outGrowHint(last int64) int {
	if last <= 0 {
		return 0
	}
	if last > outHintCap {
		return outHintCap
	}
	return int(last)
}

// renderBuffered is the single buffered render core shared by Render,
// RenderSandboxed, and RenderTo's slot fallback: it renders into a
// strings.Builder and resolves the deferred slot placeholders over the
// finished buffer. On a render error it returns the partial, unresolved buffer
// alongside the error, preserving Render's documented error shape.
//
// The builder is pre-grown from the template's remembered output length and
// the length is stored back after a successful render, so a warm template
// (one the Environment's prepared memo keeps serving) pays a single sized
// allocation instead of the append doubling ladder. Capacity cannot affect
// content, a failed render leaves the hint untouched, and the store is
// last-write-wins by design: concurrent renders of one shared Template each
// record a size that was recently true.
func renderBuffered(eng Engine, tmpl *Template, vars map[string]runtime.Value, sandboxed bool) (string, error) {
	var b strings.Builder
	if hint := outGrowHint(tmpl.lastOut.Load()); hint > 0 {
		b.Grow(hint)
	}
	in := newInterp(eng, tmpl, &b)
	if sandboxed {
		in.sandboxOn = true
	}
	ctx := runtime.NewScopeSized(len(vars))
	for k, v := range vars {
		ctx.Set(k, v)
	}
	if err := in.renderTemplate(tmpl, ctx); err != nil {
		return b.String(), err
	}
	tmpl.lastOut.Store(int64(b.Len()))
	return in.resolveSlots(b.String()), nil
}

// RenderTo renders tmpl with the given top-level variables, writing the output
// to w. When the render's template closure provably contains no deferred-slot
// construct (@yield, @provide, slot()) the output streams to w as it is
// produced, so peak memory is bounded by the deepest capture region rather
// than the total output size; a mid-render error may then leave partial output
// already written. Otherwise it falls back to the buffered render: the full
// output is built and its slot placeholders resolved, then written to w in one
// call, and nothing is written on error. Both paths write bytes identical to
// Render's returned string. RenderTo neither wraps nor flushes w; a caller
// wanting buffered throughput passes a bufio.Writer and flushes it afterward
// (a @flush statement flushes such a writer mid-render).
func RenderTo(eng Engine, tmpl *Template, vars map[string]runtime.Value, w io.Writer) error {
	if renderClosureUsesSlots(eng, tmpl) {
		out, err := renderBuffered(eng, tmpl, vars, false)
		if err != nil {
			return err
		}
		_, werr := io.WriteString(w, out)
		return werr
	}
	sink := newWriterSink(w)
	in := newInterp(eng, tmpl, sink)
	ctx := runtime.NewScopeSized(len(vars))
	for k, v := range vars {
		ctx.Set(k, v)
	}
	if err := in.renderTemplate(tmpl, ctx); err != nil {
		return err
	}
	return sink.err
}

// renderClosureUsesSlots reports whether tmpl or any template statically
// reachable from it (extends parents, use traits, literal include/embed
// candidates, import homes, block(name, other) targets) contains a
// deferred-slot construct. A dynamic reference makes the closure unprovable
// and the answer a conservative true, so RenderTo buffers; a false proves no
// @yield placeholder can enter the stream. A referenced name that does not
// exist is skipped -- the render cannot reach it either -- while a name that
// exists but fails to load counts as unprovable. Every reference resolves
// through eng.LoadTemplate, including one that matches tmpl's own name: an
// ad-hoc template (RenderStringTo) can shadow a loader entry by name, and a
// render-time include loads the loader's version, so the walk must too. The
// walk assumes the loader is stable between it and the render; a loader that
// mutates in that window can defeat the classification.
func renderClosureUsesSlots(eng Engine, tmpl *Template) bool {
	visited := map[string]bool{}
	var walk func(t *Template) bool
	walk = func(t *Template) bool {
		if t.usesSlots || t.hasDynamicRef {
			return true
		}
		for _, name := range t.staticRefs {
			if visited[name] {
				continue
			}
			visited[name] = true
			ref, err := eng.LoadTemplate(name)
			if err != nil {
				if !eng.TemplateExists(name) {
					continue
				}
				return true
			}
			if walk(ref) {
				return true
			}
		}
		return false
	}
	return walk(tmpl)
}

// interp holds one render's mutable state: the engine, the output sink, the
// active escaping strategy, and the inheritance/macro resolution roots. A nested
// render (an include) gets its own interp with a fresh sink, then splices the
// captured output back as a value.
type interp struct {
	eng  Engine
	out  Sink
	root *Template // the template that started this render (for _self, macros)

	// blocks is the merged block table for the current inheritance chain: a block
	// name resolves to the most-derived definition. parentChain lists templates
	// from most-derived to least, so parent() can find the next-up definition.
	// The map is created on the first recorded definition (appendBlockDef and
	// execEmbed's override layering), so a chain that defines no blocks renders
	// against a nil table, which every lookup reads safely.
	blocks      map[string]*blockEntry
	parentChain []*Template

	// escape is the active output strategy: "" means verbatim (off), otherwise
	// one of the six named strategies (html, js, css, html_attr,
	// html_attr_relaxed, url) shared with the escape()/e() filter (spec 04
	// Section 8). It is saved/restored across an @escape region, and that
	// save/restore is the strategy STACK that composes nested regions with the
	// module default.
	escape string

	// macros holds the macro namespace visible to the current render: the root
	// template's own macros plus any imported under a namespace or selectively.
	// It is created when the first macro binds (loadMacros over a template that
	// declares macros, or loadImport's selective @from arm), so a macro-free
	// render keeps a nil namespace that every lookup reads safely.
	macros map[string]*macroEntry

	// curBlock / curBlockDepth track the block being rendered so parent() inside
	// an overriding block can render the next definition up its chain (spec 01
	// Section 5.2).
	curBlock      *blockEntry
	curBlockDepth int

	// regexps merges the literal-`matches` regexp caches of every template that
	// enters this render (the root, its inheritance parents, and macro homes), so
	// matches() reuses one compile per literal pattern instead of recompiling each
	// evaluation. Seeded from each template's Prepare-built table via absorb,
	// which creates the map on the first non-empty table it merges; a render
	// with no literal patterns keeps it nil, and the matches() lookup reads a
	// nil map safely.
	regexps map[*ast.Node]*regexp.Regexp

	// sandboxOn is the active sandbox gate for this render (spec 04 Section 8.3,
	// design/escaping-safety Section 6.2). It starts from the engine's global
	// SandboxActive flag and is forced on -- and restored afterward, never off for
	// an already-sandboxed enclosing render (B16) -- by an @sandbox region and by a
	// sandboxed include. When on, the Phase-1 per-render callable check runs and
	// the runtime member-access / string-coercion gates enforce the policy.
	sandboxOn bool

	// indent is the cumulative indentation prefix applied to every non-blank line
	// of output while one or more @tab regions are active. It is the string form
	// of the indent stack: each @tab(n) region appends n levels' worth of spaces
	// (n * TabWidth) on entry and restores the prior prefix on exit, so nested
	// regions indent cumulatively. It is empty at top level, and the fast path
	// (emitString / emit) checks it with a single length test so output outside any
	// @tab region pays nothing.
	indent string
	// atLineStart tracks whether the output cursor sits at the beginning of a line,
	// so the active indent prefix is applied once per line and blank lines stay
	// blank. It is meaningful only while indent is non-empty.
	atLineStart bool

	// loopChanged is the per-loop state stack backing loop.changed(expr): each
	// active @for pushes a frame when it begins and pops it when it ends, so a
	// loop.changed(...) call resolves against the innermost loop and a nested loop
	// keeps its own prior-value memory. Each frame maps a call site (the
	// loop.changed(...) node) to the value the expression produced on the prior
	// iteration, so a template may call changed on several distinct expressions
	// within one loop and each tracks independently.
	loopChanged []map[*ast.Node]runtime.Value

	// slotOwner is the interp whose slot state this render contributes to: the
	// interp itself for a top-level render, or the top-level ancestor for a
	// nested render (an include or embed) after shareSlotsFrom. Routing every
	// slot access through the owner keeps one shared slots map and one yield
	// token per top-level render while both stay unallocated until a slot
	// construct actually runs.
	slotOwner *interp

	// slots holds the named accumulating content buffers of @provide/@yield: each
	// @provide label appends its rendered body to slots[label] in execution order,
	// and @yield label (or the slot(label) function) surfaces the accumulated
	// content once. The append order is the deterministic render order across every
	// contributing site (design/composition, named accumulating slots). The map
	// lives on the slot owner and is created by the first @provide; every access
	// routes through slotOwner, so a slot-free render keeps it nil.
	slots map[string]*strings.Builder

	// yieldedLabels records every label a deferred @yield reserved, so resolveSlots
	// can substitute each placeholder -- including a label no @provide ever fed,
	// which resolves to the empty string.
	yieldedLabels []string

	// yieldToken is a render-unique placeholder wrapper. A @yield writes
	// yieldToken+label+yieldToken into the output stream immediately; after the whole
	// render completes, resolveSlots replaces each placeholder with the label's final
	// accumulated content. This DEFERRAL is what lets a file shell @yield a slot at
	// the TOP and have partials feed it further down -- the collect-many-emit-once
	// use case (design/composition). The token embeds a per-render counter so slot
	// content, which is authored text, never collides with it. It lives on the
	// slot owner and is minted by yieldTok on the first @yield, so a render that
	// defers nothing keeps it empty; consumers that scan output for it (the
	// @cache slot gate) treat the empty token as "no deferral ran".
	yieldToken string

	// caller is the caller() binding visible in the macro body a @call currently
	// invokes: the block body, its lexical scope, and the caller-parameter names the
	// macro passes values back into. It is set only for the direct macro invocation
	// of a @call and is saved/restored across every macro invocation, so caller() is
	// visible in the macro the @call names but NOT in macros that macro transitively
	// calls (design/composition, call blocks). It is nil outside a @call.
	caller *callerFrame

	// pendingCaller carries the frame a @call has staged for the macro it is about
	// to invoke. invokeMacro consumes it: it becomes the body's caller for the
	// duration of that one invocation. It is nil for an ordinary macro call.
	pendingCaller *callerFrame

	// recursiveLoops is the stack of active "@for .. recursive" descent frames. Each
	// such loop pushes a frame carrying its body, target name, and base scope when it
	// begins and pops it when it ends; loop(children) inside the body re-enters the
	// same body over the given subtree one depth deeper, with loop.depth / loop.depth0
	// reflecting the descent. The stack scopes loop(...) to the innermost recursive
	// loop (design/composition, recursive @for).
	recursiveLoops []*recursiveLoop

	// lastFilterName and lastFilter memoize the most recent filter-registry
	// resolution so consecutive pipes through the same filter -- the loop-body
	// pattern -- skip the registry map lookup. The pair caches resolution only,
	// never dispatch state: the pointer is exactly what Extensions().Filter
	// returned, so host shadowing (decided when the registry was built) is
	// honored, and the registry is fixed for the render because its maps are
	// unsynchronized, making mid-render mutation unsupported regardless of the
	// memo. Nested renders build their own interp and thus their own memo.
	lastFilterName string
	lastFilter     *ext.Filter

	// cov is the coverage Collector for this render, or nil when coverage is off.
	// When nil every coverage hook (in cover.go) is a single nil comparison the
	// branch predictor makes free -- the zero-overhead-when-disabled guarantee. It
	// is copied from the engine's Coverage() at construction and threaded into
	// nested renders (includes, embeds) so a partial's coverage aggregates under
	// its own name.
	cov *cover.Collector
}

// blockEntry is one resolved block: the template that owns the definition and
// the block node. The owner is needed so parent() can walk to the next
// definition up the chain.
type blockEntry struct {
	owner *Template
	node  *ast.Node
	// chain is the ordered list of definitions for this name, most-derived first,
	// so parent() inside the i-th renders the (i+1)-th.
	chain []blockDef
}

type blockDef struct {
	owner *Template
	node  *ast.Node
}

// macroEntry binds a macro name to its definition and the template it was
// declared in (its lexical home, which provides its own macro namespace and
// globals), spec 01 Section 5.3.
type macroEntry struct {
	home *Template
	node *ast.Node
}

// newInterp builds one render's interp. The block, macro, regexp, and slot
// state all start nil and materialize on first use, so a render that never
// touches a feature never allocates its scaffolding; only the fields with
// non-zero defaults are set here.
func newInterp(eng Engine, root *Template, out Sink) *interp {
	autoesc := ""
	if eng.AutoescapeHTML() {
		autoesc = "html"
	}
	in := &interp{
		eng:         eng,
		out:         out,
		root:        root,
		escape:      autoesc,
		sandboxOn:   eng.SandboxActive(),
		atLineStart: true,
		cov:         eng.Coverage(),
	}
	// Every interp starts as its own slot owner; a nested render redirects to
	// its parent's owner via shareSlotsFrom.
	in.slotOwner = in
	in.absorb(root)
	return in
}

// absorb merges a template's literal-`matches` regexp cache into this render's
// lookup, so matches() can find the Prepare-compiled regexp for any literal
// pattern node reachable in the render. It is called as each template enters the
// render (root at construction, parents in buildChain, macro homes in
// loadMacros). The lookup map is created on the first non-empty table merged,
// sized for it, so absorbing pattern-free templates costs nothing. Nodes absent
// from the lookup (dynamic patterns, or a template not yet absorbed) fall back
// to a runtime compile.
func (in *interp) absorb(t *Template) {
	if t == nil || len(t.regexps) == 0 {
		return
	}
	if in.regexps == nil {
		in.regexps = make(map[*ast.Node]*regexp.Regexp, len(t.regexps))
	}
	for n, re := range t.regexps {
		in.regexps[n] = re
	}
}

// emit writes a rendered value to the sink, applying ToText and the active escape
// strategy. A Safe value is never escaped (it is already-safe content); raw text
// under the off strategy is byte-exact (spec 04 Sections 5, 8).
func (in *interp) emit(v runtime.Value) error {
	// Sandbox Phase-2 string-coercion gate (B12): coercing a host Object to text
	// requires its stringify member be permitted by the policy. The gate runs
	// before ToText so a disallowed object never reaches its Stringify hook. A
	// Safe value, a non-object, and a trusted shim are not gated (B14).
	if err := in.checkStringifyAllowed(v); err != nil {
		return err
	}
	text, err := runtime.ToText(v)
	if err != nil {
		return err
	}
	// A Safe value carries already-escaped content and is emitted verbatim under
	// any active strategy. Otherwise, when a strategy is active, the value's text
	// flows through the shared escaper for that strategy (spec 04 Section 8). The
	// code-point strategies (js, css, html_attr, html_attr_relaxed) decode the
	// text as UTF-8 and return a charset error naming the strategy and byte offset
	// on an invalid byte (spec 04 Section 8.2); that error is surfaced here rather
	// than emitting a silent replacement character.
	if in.escape != "" && v.Kind != runtime.KSafe {
		text, err = ext.Escape(in.escape, text)
		if err != nil {
			return err
		}
	}
	return in.write(text)
}

// emitString writes literal template text verbatim. Template TEXT is never
// escaped: it is author-controlled output, not a value (spec 04 Section 8.1).
func (in *interp) emitString(s string) error {
	return in.write(s)
}

// write is the single output choke point. Outside any @tab region (indent
// empty) it forwards s to the sink unchanged, the byte-exact fast path every
// render outside a @tab block takes. Inside a @tab region it prefixes the active
// indent to the start of each non-blank line, tracking the line-start cursor
// across calls so a line split over several writes is indented once and a blank
// line stays blank (no trailing whitespace).
func (in *interp) write(s string) error {
	if in.indent == "" {
		if s != "" {
			in.atLineStart = strings.HasSuffix(s, "\n")
		}
		_, err := in.out.WriteString(s)
		return err
	}
	return in.writeIndented(s)
}

// writeIndented applies the active indent prefix to each non-blank line of s. A
// line receives the prefix the first time non-newline content is written at its
// start; a blank line (an immediate newline at line start) is emitted verbatim
// so it carries no trailing indentation.
func (in *interp) writeIndented(s string) error {
	for len(s) > 0 {
		nl := strings.IndexByte(s, '\n')
		var line string
		var hasNL bool
		if nl < 0 {
			line = s
			s = ""
		} else {
			line = s[:nl]
			s = s[nl+1:]
			hasNL = true
		}
		if in.atLineStart && line != "" {
			if _, err := in.out.WriteString(in.indent); err != nil {
				return err
			}
		}
		if line != "" {
			if _, err := in.out.WriteString(line); err != nil {
				return err
			}
		}
		if hasNL {
			if _, err := in.out.WriteString("\n"); err != nil {
				return err
			}
			in.atLineStart = true
		} else {
			in.atLineStart = false
		}
	}
	return nil
}

// posErr attaches the node's source position to an error that lacks one, so a
// runtime failure names template:line (spec 01 Section 1.8). An error that
// already carries a position is left unchanged.
func posErr(n *ast.Node, err error) error {
	if err == nil {
		return nil
	}
	// A typed *Security error must keep its concrete type so a host can catch it
	// with errors.As and branch on Class; attach position via its own At, which
	// preserves the wrapper, rather than collapsing it into a generic *Error.
	var sec *errors.Security
	if asSecurity(err, &sec) {
		if sec.Src() == nil && sec.Line() == 0 && n != nil {
			return sec.At(n.Src, n.Line)
		}
		return sec
	}
	var qe *errors.Error
	if as(err, &qe) {
		if qe.Src == nil && qe.Line == 0 && n != nil {
			return qe.At(n.Src, n.Line)
		}
		return qe
	}
	if n != nil {
		return errors.Wrap(errors.KindRuntime, err, "%s", err.Error()).At(n.Src, n.Line)
	}
	return err
}

// as is a local errors.As over *errors.Error to avoid importing stdlib errors.
func as(err error, target **errors.Error) bool {
	for err != nil {
		if e, ok := err.(*errors.Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// asSecurity is the local errors.As over *errors.Security: a sandbox violation
// carries a distinct concrete type the host catches, so posErr must recognize
// it before the generic *Error path and keep the wrapper intact.
func asSecurity(err error, target **errors.Security) bool {
	for err != nil {
		if e, ok := err.(*errors.Security); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
