package interp

import (
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// This file implements two of Quill's composition/reuse constructs
// (design/composition): named accumulating content slots (@provide / @yield /
// slot(label)) and call blocks (@call name(args) { body } with a caller()
// callback). The recursive @for form lives in recursive_for.go. All add ways to
// factor and reassemble rendered output; none change the output of an existing
// template.

// yieldCounter makes each render's @yield placeholder token unique, so the
// deferred-resolution string replacement never collides with authored slot text.
var yieldCounter uint64

// newYieldToken returns a render-unique, low-collision placeholder wrapper built
// from a NUL-delimited counter. A @yield emits token+label+token and
// resolveSlots substitutes the label's accumulated content after the render.
func newYieldToken() string {
	n := atomic.AddUint64(&yieldCounter, 1)
	return "\x00\x01QUILL_SLOT_" + strconv.FormatUint(n, 10) + "\x00\x01"
}

// shareSlotsFrom makes this (nested) interp contribute to and read from the
// parent render's slot state instead of its own: the same slot buffers, the same
// render-unique yield token, and the same coverage collector. A @provide in an
// included or embedded partial then appends to the parent's buffer, and a @yield
// writes a placeholder the parent's single top-level resolveSlots backfills. This
// is what lets body partials feed a shell's @yield region and keeps a
// self-contained partial correct in isolation (design/composition, named
// accumulating slots). Labels a nested @yield reserves are merged back into the
// parent by mergeYieldedInto after the sub-render.
func (in *interp) shareSlotsFrom(parent *interp) {
	in.slots = parent.slots
	in.yieldToken = parent.yieldToken
}

// mergeYieldedInto appends the labels this nested interp's @yield statements
// reserved to the parent's yieldedLabels, so the parent's top-level resolveSlots
// substitutes every placeholder the partial emitted into the shared stream.
func (in *interp) mergeYieldedInto(parent *interp) {
	if len(in.yieldedLabels) == 0 {
		return
	}
	parent.yieldedLabels = append(parent.yieldedLabels, in.yieldedLabels...)
}

// execProvide renders an @provide body and APPENDS the result to the named slot
// buffer, creating the buffer on first contribution. It emits nothing at its own
// position: the accumulated content surfaces later at the matching @yield. The
// append order is the render (execution) order across every contributing site, so
// a symbol table or an import list collected from many partials comes out in the
// order the partials ran (deterministic accumulation).
func (in *interp) execProvide(n *ast.Node, ctx *runtime.Scope) error {
	out, err := in.captureItems(n.Children, ctx)
	if err != nil {
		return err
	}
	buf, ok := in.slots[n.Str]
	if !ok {
		buf = &strings.Builder{}
		in.slots[n.Str] = buf
	}
	buf.WriteString(out)
	return nil
}

// execYield reserves a named slot's position in the output and defers its
// content. It writes a render-unique placeholder immediately; resolveSlots, run
// once the whole render completes, replaces it with the label's final accumulated
// text. Deferral is what lets a shell @yield a slot BEFORE the partials that feed
// it (the collect-many-emit-once use case). An unprovided label resolves to the
// empty string, so a shell may reserve a slot no site fed. The placeholder is
// written verbatim so it survives escaping untouched; the resolved content was
// already produced through the active escaper by its @provide bodies.
func (in *interp) execYield(n *ast.Node, ctx *runtime.Scope) error {
	in.yieldedLabels = append(in.yieldedLabels, n.Str)
	return posErr(n, in.emitString(in.yieldToken+n.Str+in.yieldToken))
}

// resolveSlots substitutes every deferred @yield placeholder in the finished
// render output with its label's accumulated slot content. It is called once by
// the top-level Render entry after the walk completes, so a placeholder written
// before its slot was fully provided is backfilled with the complete text. A
// render with no slots leaves the output untouched.
func (in *interp) resolveSlots(out string) string {
	if len(in.yieldedLabels) == 0 {
		return out
	}
	for _, label := range in.yieldedLabels {
		placeholder := in.yieldToken + label + in.yieldToken
		out = strings.ReplaceAll(out, placeholder, in.slotContent(label))
	}
	return out
}

// slotContent returns a slot's accumulated text, or "" when nothing provided it.
func (in *interp) slotContent(label string) string {
	if buf, ok := in.slots[label]; ok {
		return buf.String()
	}
	return ""
}

// callSlot backs the slot(label) function form: it returns a slot's accumulated
// content AS OF THE CALL as a value so it can be piped or assigned. Unlike the
// deferred @yield statement, the function form is immediate -- it captures
// whatever the label holds when the expression evaluates -- so it suits a site
// placed after its @provide contributions. Under an active escape strategy the
// content is already-escaped and wrapped Safe so a downstream print does not
// escape it twice.
func (in *interp) callSlot(n *ast.Node, ctx *runtime.Scope) (runtime.Value, error) {
	args, err := in.collectArgs(n, ctx, nil)
	if err != nil {
		return runtime.Null(), err
	}
	if len(args) != 1 {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"slot() expects exactly one label argument, got %d", len(args)))
	}
	label, err := runtime.ToText(args[0])
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	content := in.slotContent(label)
	if in.escape != "" {
		return runtime.Safe(content), nil
	}
	return runtime.Str(content), nil
}

// callerFrame is one active @call: the block body the macro renders through
// caller(), the lexical scope that body sees, and the caller-parameter names the
// macro passes values back into. It is pushed for the duration of the macro
// invocation and popped afterward, so caller() resolves to the innermost @call.
type callerFrame struct {
	body   []*ast.Node
	ctx    *runtime.Scope
	params []string
}

// execCallBlock renders "@call [(callerParams)] name(args) { body }": it invokes
// macro name with the given arguments and, for the duration of that invocation,
// binds a caller() callable inside the macro body that renders the block. A
// caller(v1, v2) call binds the declared caller parameters positionally in the
// block's scope and renders it, so values round-trip from the macro back into the
// block. The macro's captured return value is emitted at the @call position, so a
// macro that wraps caller() in a header/footer surfaces the whole wrapped output.
func (in *interp) execCallBlock(n *ast.Node, ctx *runtime.Scope) error {
	entry, ok := in.macros[n.Str]
	if !ok {
		return posErr(n, errors.New(errors.KindRuntime, "unknown macro %q", n.Str))
	}
	callerParams := n.Child(0) // KindParams (possibly empty)
	body := n.Children[len(n.Children)-1]

	var paramNames []string
	for _, p := range callerParams.Children {
		paramNames = append(paramNames, p.Str)
	}

	pos, named, err := in.collectArgsNamed(n, ctx)
	if err != nil {
		return err
	}

	// Stage the caller frame so the macro invokeMacro is about to run binds caller()
	// to THIS block. invokeMacro consumes the staged frame, sets it as the body's
	// caller for that one invocation, and restores the prior binding afterward.
	in.pendingCaller = &callerFrame{body: body.Children, ctx: ctx, params: paramNames}
	out, err := in.invokeMacro(n, entry, pos, named)
	if err != nil {
		return err
	}
	return posErr(n, in.emit(out))
}

// callCaller renders the innermost active @call block, backing the caller()
// callable a macro body invokes. Positional arguments bind the block's declared
// caller parameters in order (design/composition, call blocks), so a macro can
// pass a value -- a section title, a row index -- back into the block. Extra
// arguments beyond the declared parameters are ignored; a declared parameter with
// no matching argument binds null. Outside any @call it is a runtime error.
func (in *interp) callCaller(n *ast.Node, ctx *runtime.Scope) (runtime.Value, error) {
	frame := in.caller
	if frame == nil {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"caller() is only valid inside a macro invoked by a @call block"))
	}
	args, err := in.collectArgs(n, ctx, nil)
	if err != nil {
		return runtime.Null(), err
	}

	// The block renders with caller() suspended for its own duration, so caller()
	// used inside the block refers to an ENCLOSING @call rather than recursing into
	// this same block. It renders in a child of its own lexical scope, so the caller
	// parameters are visible without leaking into the call site's context.
	saved := in.caller
	in.caller = nil
	scope := frame.ctx.Child()
	for i, name := range frame.params {
		if i < len(args) {
			scope.Set(name, args[i])
		} else {
			scope.Set(name, runtime.Null())
		}
	}
	out, err := in.captureItems(frame.body, scope)
	in.caller = saved
	if err != nil {
		return runtime.Null(), err
	}
	if in.escape != "" {
		return runtime.Safe(out), nil
	}
	return runtime.Str(out), nil
}
