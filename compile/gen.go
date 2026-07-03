package compile

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/source"
)

// compiler holds one Module compilation's state: the output buffers, the
// compile-time frame stack that models the interpreter's scope chain, the
// callable pre-resolution table, and the compile-time escape-strategy and
// output-writer stacks.
type compiler struct {
	src  *source.Source
	opts Options

	// rootDecls receives the root frame's hoisted binding declarations; body
	// receives every lowered statement. Non-root frame declarations are
	// emitted inline at their block's start.
	rootDecls bytes.Buffer
	body      bytes.Buffer

	ind  int // current indentation depth inside the function body
	tmpN int // monotonically increasing temporary counter

	frames []*frame
	frameN int

	// callables lists the pre-resolved registry lookups in first-use order;
	// callableKey deduplicates them (the map is lookup-only, never iterated,
	// so emission order stays deterministic).
	callables   []*callableRef
	callableKey map[string]*callableRef

	// strategy is the compile-time escape-strategy stack; the last entry is
	// the active strategy ("" means off).
	strategy []string
	// writers is the output-writer variable stack; the last entry names the
	// qWriter the current region writes to (capture bodies push a fresh one).
	writers []string
	// retPrefix is the error-return prefix stack: "" inside the render
	// function, "runtime.Null(), " inside value-returning closures.
	retPrefix []string

	// condDepth counts enclosing conditional constructs, so a bind inside an
	// @if arm stays compile-time "maybe" while an unconditional bind upgrades
	// its binding to definite.
	condDepth int

	// loops is the stack of lexically enclosing @for lowerings, used by
	// loop.changed call sites to attach their per-loop memory locals and by
	// the loop optimizer's inline reads and on-demand materialization.
	loops []*loopInfo

	// an is the module's loop escape analysis, computed before lowering; it
	// decides which loops skip the per-iteration loop-value materialization
	// and which member reads lower to inline loop arithmetic.
	an *loopAnalysis

	// inArrow counts enclosing arrow-body lowerings; loop.changed resolves
	// its loop dynamically there, so it is outside the compilable subset.
	inArrow int

	lenient  bool
	tabWidth int
}

// loopInfo carries the codegen state of one @for being lowered: the memory
// locals its loop.changed call sites need (declared at the loop frame's
// block), the AST node the escape analysis keyed its decision on, and the
// counter and pair-slice locals the optimizer's inline arithmetic reads.
type loopInfo struct {
	changed  []changedSite
	forNode  *ast.Node
	frame    *frame
	iVar     string
	pairsVar string
	// inline marks a loop proven non-escaping: no per-iteration loop value is
	// bound, and scope-enumerating consumers materialize one on demand.
	inline bool
	// parentVar names the local holding the parent loop value probed once at
	// loop entry (the interpreter's pre.Get("loop") timing). Every on-demand
	// materialization of an inline loop's value consumes this local instead of
	// re-probing, so a scope entry named loop that changes mid-loop cannot
	// skew the parent away from what the interpreter bound.
	parentVar string
	// live marks a loop proven mutation-free (loopAnalysis.liveFor): a KArray
	// iterand iterates zero-copy off arrVar with nVar the entry-time length
	// snapshot, and pairsVar holds only the runtime fallback for non-array
	// iterands (nil on the array path).
	live   bool
	arrVar string
	nVar   string
}

// changedSite is one loop.changed(...) call site's per-loop memory locals.
type changedSite struct {
	prev string
	seen string
}

// callableRef is one pre-resolved registry lookup: the ExtensionSet accessor
// method, the callable name, and the generated value/ok variable names. A
// Filter ref also carries the fast-flag variable holding the hoisted "Fn1
// applies" decision, so per-iteration filter sites branch on one bool instead
// of re-testing Fn1 and the Needs* flags.
type callableRef struct {
	method string // "Filter", "Function", or "Test"
	name   string
	val    string
	ok     string
	fast   string // Filter refs only: the hoisted Fn1-dispatch flag variable
}

func newCompiler(src *source.Source, opts Options) *compiler {
	return &compiler{
		src:         src,
		opts:        opts,
		ind:         1,
		callableKey: map[string]*callableRef{},
		strategy:    []string{autoStrategy(opts)},
		writers:     []string{"qw"},
		retPrefix:   []string{""},
		lenient:     opts.LenientVariables,
		tabWidth:    opts.TabWidth,
	}
}

func autoStrategy(opts Options) string {
	if opts.AutoescapeHTML {
		return "html"
	}
	return ""
}

// ---- emission primitives ----------------------------------------------------

// linef writes one line at the current indentation into the body buffer.
// With no args the line is written verbatim, so prebuilt code carrying
// %-verbs inside generated string literals stays intact.
func (c *compiler) linef(format string, args ...any) {
	for i := 0; i < c.ind; i++ {
		c.body.WriteByte('\t')
	}
	if len(args) == 0 {
		c.body.WriteString(format)
	} else {
		fmt.Fprintf(&c.body, format, args...)
	}
	c.body.WriteByte('\n')
}

// openf writes a block-opening line and increases the indentation.
func (c *compiler) openf(format string, args ...any) {
	c.linef(format, args...)
	c.ind++
}

// closeb decreases the indentation and writes the closing brace.
func (c *compiler) closeb() {
	c.ind--
	c.linef("}")
}

// mark writes the line-map marker for template line n.
func (c *compiler) mark(line int) {
	if line > 0 {
		c.linef("//q:l %d", line)
	}
}

// tmp mints a fresh temporary name with the given prefix.
func (c *compiler) tmp(prefix string) string {
	c.tmpN++
	return prefix + strconv.Itoa(c.tmpN)
}

// q quotes s as an ASCII-only Go string literal.
func q(s string) string { return strconv.QuoteToASCII(s) }

// ret builds a return statement carrying the current error-return prefix, so
// the same lowering works inside the render function and inside closures.
func (c *compiler) ret(expr string) string {
	return "return " + c.retPrefix[len(c.retPrefix)-1] + expr
}

// checkErr emits the error check for errVar, positioning it at template line.
func (c *compiler) checkErr(errVar string, line int) {
	c.openf("if %s != nil {", errVar)
	c.linef(c.ret(fmt.Sprintf("qpos(%s, %d)", errVar, line)))
	c.closeb()
}

// writer names the qWriter variable the current region writes to.
func (c *compiler) writer() string { return c.writers[len(c.writers)-1] }

// escapeStrategy names the active escape strategy at this point of lowering.
func (c *compiler) escapeStrategy() string { return c.strategy[len(c.strategy)-1] }

// callable pre-resolves one registry lookup, returning its value and ok
// variable names. Repeated uses of the same kind and name share one lookup.
func (c *compiler) callable(method, name string) (string, string) {
	key := method + "\x00" + name
	if cr, ok := c.callableKey[key]; ok {
		return cr.val, cr.ok
	}
	var prefix string
	switch method {
	case "Filter":
		prefix = "qflt"
	case "Function":
		prefix = "qfun"
	default:
		prefix = "qtst"
	}
	cr := &callableRef{
		method: method,
		name:   name,
		val:    fmt.Sprintf("%s%d", prefix, len(c.callables)),
		ok:     fmt.Sprintf("%sok%d", prefix, len(c.callables)),
	}
	if method == "Filter" {
		cr.fast = fmt.Sprintf("%sfast%d", prefix, len(c.callables))
	}
	c.callables = append(c.callables, cr)
	c.callableKey[key] = cr
	return cr.val, cr.ok
}

// callableFilterFast returns the hoisted fast-flag variable of the named
// filter's pre-resolved ref: true exactly when the resolved filter publishes
// Fn1 and needs no engine injection, the whole arity-known dispatch decision
// evaluated once per render.
func (c *compiler) callableFilterFast(name string) string {
	c.callable("Filter", name)
	return c.callableKey["Filter\x00"+name].fast
}

// ---- compile-time scope model ------------------------------------------------

// frameKind discriminates the interpreter scope frame a compile frame models.
type frameKind int

const (
	frameRoot frameKind = iota
	frameLoop
	frameFilter
	frameWith
	frameWithOnly
	frameArrow
)

// binding is one name bound as a Go local within a frame: the value variable,
// the bound flag variable, and the compile-time definiteness of the binding at
// the current walk position.
type binding struct {
	name string
	val  string
	flag string
	// definite is true once the walk passed an unconditional bind of the
	// name, so reads can use the local directly without the runtime flag.
	definite bool
	// everBound is true once any bind site for the name has been lowered.
	everBound bool
}

// frame is one compile-time scope frame: the frame the interpreter would push
// for the same construct, with its prescanned bindings in source order and a
// runtime order slice recording actual first-bind order.
type frame struct {
	id      int
	kind    frameKind
	order   []*binding
	byName  map[string]*binding
	withVar string // frameWith / frameWithOnly: the with-map value variable
	// ord names the generated []string local recording this frame's names in
	// actual first-bind order, mirroring the interpreter's Scope.order: a name
	// whose first source appearance sits in a non-executed branch must not
	// enter hints or _context until a bind really runs. The root frame uses
	// qNames instead; a frame with no prescanned names has no slice.
	ord string
}

// currentFrame returns the innermost frame.
func (c *compiler) currentFrame() *frame { return c.frames[len(c.frames)-1] }

// pushFrame creates a frame of the given kind, pre-declares a value/flag local
// pair for every name in binds (the frame prescan), and returns it. Root-frame
// declarations go to rootDecls; every other frame declares at the current
// block position.
func (c *compiler) pushFrame(kind frameKind, binds []string) *frame {
	c.frameN++
	f := &frame{id: c.frameN, kind: kind, byName: map[string]*binding{}}
	c.frames = append(c.frames, f)
	for _, name := range binds {
		c.declareBinding(f, name)
	}
	if kind != frameRoot && len(f.order) > 0 {
		// The runtime first-bind order slice; every prescanned name has at
		// least one lowered bind site appending to it, so it is always used.
		f.ord = fmt.Sprintf("qo%d", f.id)
		c.linef("%s := make([]string, 0, %d)", f.ord, len(f.order))
	}
	return f
}

// declareBinding adds one prescanned name to frame f, emitting its hoisted
// value and flag declarations.
func (c *compiler) declareBinding(f *frame, name string) *binding {
	if b, ok := f.byName[name]; ok {
		return b
	}
	b := &binding{
		name: name,
		val:  fmt.Sprintf("qv_%s_%d", name, f.id),
		flag: fmt.Sprintf("qb_%s_%d", name, f.id),
	}
	f.byName[name] = b
	f.order = append(f.order, b)
	if f.kind == frameRoot {
		fmt.Fprintf(&c.rootDecls, "\tvar %s runtime.Value\n", b.val)
		fmt.Fprintf(&c.rootDecls, "\tvar %s bool\n", b.flag)
		fmt.Fprintf(&c.rootDecls, "\t_, _ = %s, %s\n", b.val, b.flag)
		return b
	}
	c.linef("var %s runtime.Value", b.val)
	c.linef("var %s bool", b.flag)
	c.linef("_, _ = %s, %s", b.val, b.flag)
	return b
}

// popFrame discards the innermost frame.
func (c *compiler) popFrame() {
	c.frames = c.frames[:len(c.frames)-1]
}

// checkBindNames rejects any prescanned binding name carrying a non-ASCII
// byte: such a name cannot become a Go local in the ASCII-only generated file,
// so it is a typed subset rejection naming the identifier rather than an
// internal generation failure. n anchors the error to the frame's construct.
func (c *compiler) checkBindNames(binds []string, n *ast.Node) error {
	for _, name := range binds {
		for i := 0; i < len(name); i++ {
			if name[i] >= 0x80 {
				return c.notCompilable(
					fmt.Sprintf("non-ASCII template identifier %s", strconv.QuoteToASCII(name)), n)
			}
		}
	}
	return nil
}

// resolveStep is one link of a name-resolution chain, mirroring one frame of
// the interpreter's Scope.Get walk.
type resolveStep struct {
	b       *binding // non-nil: a frame binding
	cross   bool     // the binding's frame is not the innermost frame
	withVar string   // non-empty: a with-map lookup
	vars    bool     // the root vars-map fallback
}

// resolveChain builds the resolution chain for a bare-name read at the current
// frame stack. terminal reports whether the chain ends in a step that always
// binds (a definite binding), so no miss handling is needed.
func (c *compiler) resolveChain(name string) (steps []resolveStep, terminal bool) {
	top := len(c.frames) - 1
	for i := top; i >= 0; i-- {
		f := c.frames[i]
		if b, ok := f.byName[name]; ok {
			steps = append(steps, resolveStep{b: b, cross: i != top})
			if b.definite {
				return steps, true
			}
		}
		switch f.kind {
		case frameWith:
			steps = append(steps, resolveStep{withVar: f.withVar})
		case frameWithOnly:
			// An only-with frame is a fresh scope root: resolution stops here.
			steps = append(steps, resolveStep{withVar: f.withVar})
			return steps, false
		case frameRoot:
			steps = append(steps, resolveStep{vars: true})
		}
	}
	return steps, false
}

// stepValue renders the value expression one resolution step yields for its
// raw found value in v. Frame-crossing reads and map-backed reads reproduce
// the share marks Scope.Get and the bind-time Scope.Set would apply.
func stepValue(s resolveStep, v string) string {
	if s.b != nil && !s.cross {
		return v
	}
	return fmt.Sprintf("runtime.ShareValue(%s)", v)
}

// readName lowers a bare-name read at template line, returning the value
// expression. Under strict variables a miss returns the interpreter's
// undefined-variable error with the available-names hint; under allowAbsent or
// lenient mode a miss yields null.
func (c *compiler) readName(name string, line int, allowAbsent bool) string {
	steps, terminal := c.resolveChain(name)

	// Fast path: a single definite binding needs no chain.
	if terminal && len(steps) == 1 {
		return stepValue(steps[0], steps[0].b.val)
	}

	val := c.tmp("qt")
	found := c.tmp("qf")
	c.linef("var %s runtime.Value", val)
	c.linef("%s := false", found)
	c.emitSteps(name, steps, val, found)
	if !terminal && !allowAbsent && !c.lenient {
		c.openf("if !%s {", found)
		hint := c.emitHint()
		c.linef(c.ret(fmt.Sprintf("qundef(%s, %s, %d)", q(name), hint, line)))
		c.closeb()
	} else {
		c.linef("_ = %s", found)
	}
	return val
}

// probeName lowers a presence-check read (the interpreter's ctx.Get with no
// strict error), returning the value and found expressions.
func (c *compiler) probeName(name string) (string, string) {
	steps, terminal := c.resolveChain(name)
	if terminal && len(steps) == 1 {
		return stepValue(steps[0], steps[0].b.val), "true"
	}
	val := c.tmp("qt")
	found := c.tmp("qf")
	c.linef("var %s runtime.Value", val)
	c.linef("%s := false", found)
	c.emitSteps(name, steps, val, found)
	// Presence-only callers consume just the found flag; the value local must
	// still compile away cleanly.
	c.linef("_ = %s", val)
	return val, found
}

// emitSteps lowers a resolution chain for name into guarded assignments to
// val/found.
func (c *compiler) emitSteps(name string, steps []resolveStep, val, found string) {
	for _, s := range steps {
		switch {
		case s.b != nil:
			if s.b.definite {
				c.openf("if !%s {", found)
				c.linef("%s = %s", val, stepValue(s, s.b.val))
				c.linef("%s = true", found)
				c.closeb()
				continue
			}
			c.openf("if !%s && %s {", found, s.b.flag)
			c.linef("%s = %s", val, stepValue(s, s.b.val))
			c.linef("%s = true", found)
			c.closeb()
		case s.withVar != "":
			c.openf("if !%s && %s.Kind == runtime.KArray && %s.Arr != nil {", found, s.withVar, s.withVar)
			inner := c.tmp("qt")
			ok := c.tmp("qk")
			c.openf("if %s, %s := %s.Arr.GetStr(%s); %s {", inner, ok, s.withVar, q(name), ok)
			c.linef("%s = runtime.ShareValue(%s)", val, inner)
			c.linef("%s = true", found)
			c.closeb()
			c.closeb()
		case s.vars:
			inner := c.tmp("qt")
			ok := c.tmp("qk")
			c.openf("if !%s {", found)
			c.openf("if %s, %s := vars[%s]; %s {", inner, ok, q(name), ok)
			c.linef("%s = runtime.ShareValue(%s)", val, inner)
			c.linef("%s = true", found)
			c.closeb()
			c.closeb()
		}
	}
}

// emitHint builds the interpreter's available-names hint at an undefined-name
// error site: the root scope's runtime-maintained order plus each inner
// frame's names, deduplicated first-seen like Scope.Names. It returns the name
// of the []string local holding the hint.
func (c *compiler) emitHint() string {
	h := c.tmp("qh")

	// Find the innermost only-with frame: it is a fresh scope root, so the
	// hint starts there rather than at the function's root scope.
	start := 0
	for i := len(c.frames) - 1; i >= 0; i-- {
		if c.frames[i].kind == frameWithOnly {
			start = i
			break
		}
	}
	if start == 0 {
		c.linef("%s := append([]string(nil), qNames...)", h)
	} else {
		c.linef("var %s []string", h)
	}
	for i := start; i < len(c.frames); i++ {
		f := c.frames[i]
		if f.kind == frameWith || f.kind == frameWithOnly {
			n := c.tmp("qn")
			c.openf("for _, %s := range qwithNames(%s) {", n, f.withVar)
			c.linef("%s = qaddName(%s, %s)", h, h, n)
			c.closeb()
		}
		if f.kind == frameRoot {
			// Root bindings maintain qNames at runtime; no static additions.
			continue
		}
		if f.ord == "" {
			continue
		}
		// The frame's runtime order slice lists exactly the actually-bound
		// names in first-bind order, matching the interpreter's Scope.order.
		n := c.tmp("qn")
		c.openf("for _, %s := range %s {", n, f.ord)
		c.linef("%s = qaddName(%s, %s)", h, h, n)
		c.closeb()
	}
	return h
}

// emitContext materializes the live variable bindings as a mapping *Array,
// reproducing the interpreter's _context builder and needs-context injection:
// scope names in Scope.Names order, each holding its innermost value. It
// returns the name of the *runtime.Array local.
func (c *compiler) emitContext() string {
	arr := c.tmp("qcx")
	c.linef("%s := runtime.NewArray()", arr)

	start := 0
	for i := len(c.frames) - 1; i >= 0; i-- {
		if c.frames[i].kind == frameWithOnly {
			start = i
			break
		}
	}
	if start == 0 {
		root := c.frames[0]
		n := c.tmp("qn")
		c.openf("for _, %s := range qNames {", n)
		if len(root.order) > 0 {
			c.openf("switch %s {", n)
			for _, b := range root.order {
				c.linef("case %s:", q(b.name))
				c.ind++
				if b.definite {
					c.linef("%s.SetStr(%s, runtime.ShareValue(%s))", arr, q(b.name), b.val)
				} else {
					c.openf("if %s {", b.flag)
					c.linef("%s.SetStr(%s, runtime.ShareValue(%s))", arr, q(b.name), b.val)
					c.ind--
					c.linef("} else {")
					c.ind++
					c.linef("%s.SetStr(%s, runtime.ShareValue(vars[%s]))", arr, n, n)
					c.closeb()
				}
				c.ind--
			}
			c.linef("default:")
			c.ind++
			c.linef("%s.SetStr(%s, runtime.ShareValue(vars[%s]))", arr, n, n)
			c.ind--
			c.closeb()
		} else {
			c.linef("%s.SetStr(%s, runtime.ShareValue(vars[%s]))", arr, n, n)
		}
		c.closeb()
	}
	for i := start; i < len(c.frames); i++ {
		f := c.frames[i]
		if f.kind == frameRoot {
			continue
		}
		if f.kind == frameWith || f.kind == frameWithOnly {
			p := c.tmp("qp")
			c.openf("if %s.Kind == runtime.KArray && %s.Arr != nil {", f.withVar, f.withVar)
			c.openf("for _, %s := range %s.Arr.Pairs() {", p, f.withVar)
			s := c.tmp("qs")
			c.linef("%s, _ := runtime.ToText(%s.Key)", s, p)
			c.linef("%s.SetStr(%s, runtime.ShareValue(%s.Val))", arr, s, p)
			c.closeb()
			c.closeb()
		}
		if f.ord == "" {
			continue
		}
		// Walk the frame's runtime first-bind order so entries land in the
		// order the interpreter's Scope.Names yields; the switch maps each
		// bound name back to its value local (a name in the slice is bound by
		// construction, so no flag check is needed). An inline loop frame has
		// no live loop value, so its loop entry materializes on demand; when a
		// deeper loop frame follows, that entry is overwritten in place, so
		// the innermost (and only observable) value is the deepest frame's.
		n := c.tmp("qn")
		c.openf("for _, %s := range %s {", n, f.ord)
		c.openf("switch %s {", n)
		for _, b := range f.order {
			c.linef("case %s:", q(b.name))
			c.ind++
			if b.name == "loop" && f.kind == frameLoop {
				if li := c.loopByFrame(f); li != nil && li.inline {
					lv := c.emitLoopValue(li)
					c.linef("%s.SetStr(%s, %s)", arr, q(b.name), lv)
					c.ind--
					continue
				}
			}
			c.linef("%s.SetStr(%s, runtime.ShareValue(%s))", arr, q(b.name), b.val)
			c.ind--
		}
		c.closeb()
		c.closeb()
	}
	return arr
}

// bindName lowers one Scope.Set-equivalent bind of name to the value in expr
// at the current frame: the value is share-marked (unless owned, the SetOwned
// path of a member assignment), the binding's flag is set, and a first bind
// maintains the frame's runtime name order exactly as Scope.bind would (the
// root frame's order is qNames; every other frame keeps its own slice).
func (c *compiler) bindName(name, expr string, owned bool) {
	f := c.currentFrame()
	b, ok := f.byName[name]
	if !ok {
		// The prescan hoists every bindable name; a miss is a compiler bug.
		panic(fmt.Sprintf("compile: unscanned binding %q", name))
	}
	if f.kind == frameRoot && !b.definite {
		ok := c.tmp("qk")
		if b.everBound {
			c.openf("if !%s {", b.flag)
			c.openf("if _, %s := vars[%s]; !%s {", ok, q(name), ok)
			c.linef("qNames = append(qNames, %s)", q(name))
			c.closeb()
			c.closeb()
		} else {
			c.openf("if _, %s := vars[%s]; !%s {", ok, q(name), ok)
			c.linef("qNames = append(qNames, %s)", q(name))
			c.closeb()
		}
	}
	if f.kind != frameRoot && !b.definite && f.ord != "" {
		// A loop or filter frame re-executes its bind sites, so even the first
		// lowered site guards the order append on the runtime bound flag.
		c.openf("if !%s {", b.flag)
		c.linef("%s = append(%s, %s)", f.ord, f.ord, q(name))
		c.closeb()
	}
	if owned {
		c.linef("%s = %s", b.val, expr)
	} else {
		c.linef("%s = runtime.ShareValue(%s)", b.val, expr)
	}
	if !b.definite {
		c.linef("%s = true", b.flag)
	}
	b.everBound = true
	if c.condDepth == 0 {
		b.definite = true
	}
}

// spill materializes expr into a fresh temporary unless it is already a
// single-assignment temporary or an immutable literal, so its value is captured
// before any later statement can rebind the locals it reads. In particular a
// mutable binding local (qv_*) is always copied: an inline assignment in a
// later operand rebinds it, and the interpreter's evaluation order fixes the
// earlier operand's value before that rebind happens.
func (c *compiler) spill(expr string) string {
	if isSpilled(expr) {
		return expr
	}
	t := c.tmp("qt")
	c.linef("%s := %s", t, expr)
	return t
}

// isSpilled reports whether expr is safe to embed later without capturing: a
// fresh single-assignment temporary (a q-prefixed simple identifier that is not
// a mutable qv_* binding local) or a value constructor over a constant literal.
func isSpilled(expr string) bool {
	if expr == "" {
		return false
	}
	// Plain identifiers minted by tmp (qt1, qcx2, ...) hold captured values;
	// binding locals (qv_*) are live and rebindable, so they never count.
	ident := true
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		alnum := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
		if !alnum {
			ident = false
			break
		}
	}
	if ident {
		if len(expr) >= 3 && expr[:3] == "qv_" {
			return false
		}
		return expr[0] == 'q' && expr != "qw"
	}
	switch expr {
	case "runtime.Null()", "runtime.Bool(true)", "runtime.Bool(false)":
		return true
	}
	if len(expr) > 12 && expr[:12] == "runtime.Int(" && isConstBody(expr[12:len(expr)-1]) {
		return true
	}
	if len(expr) > 14 && expr[:14] == "runtime.Float(" && isConstBody(expr[14:len(expr)-1]) {
		return true
	}
	if len(expr) > 12 && expr[:12] == "runtime.Str(" && expr[12] == '"' {
		return true
	}
	return false
}

// isConstBody reports whether s is a numeric literal body (digits, sign,
// decimal point, exponent), so constructor calls over constants skip spilling.
func isConstBody(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch >= '0' && ch <= '9') || ch == '-' || ch == '+' || ch == '.' || ch == 'e' || ch == 'E' {
			continue
		}
		return false
	}
	return true
}

// currentLoop returns the innermost lexically enclosing loop lowering, or nil.
func (c *compiler) currentLoop() *loopInfo {
	if len(c.loops) == 0 {
		return nil
	}
	return c.loops[len(c.loops)-1]
}
