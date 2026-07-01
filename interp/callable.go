package interp

import (
	"strings"

	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/ext"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// evalCall handles "callee(args)". The callee is one of: a bare function name
// (registered in the function namespace), a bare macro name (the macro
// namespace, spec 01 Section 5.3), a dotted macro reference (forms.input(),
// _self.tree()), or a method call a.b() on a host Object. The parser leaves the
// callee as child 0 and the arguments as KindArg children.
func (in *interp) evalCall(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	callee := n.Child(0)

	// f(...) where f is a bare name: a composition builtin, a function, or a macro.
	if callee.Kind == ast.KindName {
		name := callee.Str
		switch name {
		case "parent":
			return in.callParent(n, ctx)
		case "block":
			return in.callBlock(n, ctx)
		case "caller":
			return in.callCaller(n, ctx)
		case "slot":
			return in.callSlot(n, ctx)
		case "loop":
			// loop(children) is the recursive-descent callable a "@for .. recursive"
			// loop binds; it is recognized only while such a loop is active, so an
			// ordinary loop.* mapping read is unaffected.
			if in.curRecursive() != nil {
				return in.callRecursiveLoop(n, ctx)
			}
		}
		if _, ok := in.macros[name]; ok {
			return in.callMacro(n, name, ctx)
		}
		if fn, ok := in.eng.Extensions().Function(name); ok {
			args, err := in.collectArgs(n, ctx, nil)
			if err != nil {
				return runtime.Null(), err
			}
			return in.invokeFunction(n, fn, ctx, args)
		}
		// A bare name bound in scope to a callable value is invoked directly, so a
		// value produced by separator()/cell() or an arrow bound with @set is called
		// as name(args). A registered function of the same name takes precedence
		// (checked above), keeping the stdlib namespace stable.
		if bound, ok := ctx.Get(name); ok && runtime.IsCallable(bound) {
			args, err := in.collectArgs(n, ctx, nil)
			if err != nil {
				return runtime.Null(), err
			}
			res, err := runtime.Call(bound, args)
			if err != nil {
				return runtime.Null(), posErr(n, err)
			}
			return res, nil
		}
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"unknown function or macro %q", name))
	}

	// loop.changed(expr): a built-in loop method. It is true on the FIRST iteration
	// and whenever expr differs (by typed value equality) from the value it had on
	// the prior iteration, tracked per call site within the innermost loop. It is
	// resolved here, before the receiver is evaluated as a value, so the loop array
	// itself needs no "changed" member.
	if callee.Kind == ast.KindAttr && callee.Str == "changed" &&
		callee.Child(0).Kind == ast.KindName && callee.Child(0).Str == "loop" {
		return in.callLoopChanged(n, ctx)
	}

	// a.b(...) -- either a macro on an imported namespace / _self, or a host
	// method call. Evaluate the receiver; a macroRef/selfRef resolves to a macro.
	if callee.Kind == ast.KindAttr {
		recv, err := in.eval(callee.Child(0), ctx, false)
		if err != nil {
			return runtime.Null(), err
		}
		if mr, ok := recv.Obj.(*selfRef); ok && recv.Kind == runtime.KObject {
			return in.callMacroIn(n, mr.tmpl, callee.Str, ctx)
		}
		if ns, ok := recv.Obj.(*importNS); ok && recv.Kind == runtime.KObject {
			return in.callMacroIn(n, ns.tmpl, callee.Str, ctx)
		}
		// Host method call a.b(args).
		args, err := in.collectArgs(n, ctx, nil)
		if err != nil {
			return runtime.Null(), err
		}
		if recv.Kind == runtime.KObject {
			// Sandbox Phase-2: gate the method by the policy via the host type-graph
			// before invoking it (B10). A trusted shim / Safe receiver bypasses.
			if err := in.checkMethodAllowed(recv, callee.Str); err != nil {
				return runtime.Null(), posErr(n, err)
			}
			res, err := recv.Obj.CallMethod(callee.Str, args)
			if err != nil {
				return runtime.Null(), posErr(n, err)
			}
			return res, nil
		}
		return runtime.Null(), posErr(n, errors.New(errors.KindAttribute,
			"cannot call method %q on %s", callee.Str, recv.Kind))
	}

	return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
		"expression is not callable"))
}

// callLoopChanged implements loop.changed(expr): true on the first iteration and
// whenever expr differs from its value on the prior iteration, tracked per call
// site within the innermost active loop (spec 01 Section 4.2). The call takes
// exactly one positional argument (the watched expression). Outside any loop it
// is a runtime error, matching the undefined `loop` a bare loop.* read would hit.
func (in *interp) callLoopChanged(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	args, err := in.collectArgs(n, ctx, nil)
	if err != nil {
		return runtime.Null(), err
	}
	if len(args) != 1 {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"loop.changed expects exactly one argument, got %d", len(args)))
	}
	if len(in.loopChanged) == 0 {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"loop.changed is only available inside a for loop"))
	}
	cur := args[0]
	frame := in.loopChanged[len(in.loopChanged)-1]
	prev, seen := frame[n]
	frame[n] = cur
	// First iteration for this call site (no prior value), or a value that differs
	// from the prior iteration by typed equality, counts as changed.
	changed := !seen || !runtime.Equal(prev, cur)
	return runtime.Bool(changed), nil
}

// evalFilter handles "x | name(args)" == name(x, args). Host filters shadow core
// ones (the registry resolves that). The piped value is the implicit first user
// argument; injected engine values are prepended per the Needs* flags.
func (in *interp) evalFilter(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	// The piped value is evaluated under allowAbsent for `default`, so an
	// undefined chain piped into default falls back rather than erroring
	// (spec 03 Section 2.7, spec 04 Section 8.2).
	allowAbsent := n.Str == "default"
	piped, err := in.eval(n.Child(0), ctx, allowAbsent)
	if err != nil {
		return runtime.Null(), err
	}
	filt, ok := in.eng.Extensions().Filter(n.Str)
	if !ok {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"unknown filter %q", n.Str))
	}
	args, err := in.collectArgs(n, ctx, []runtime.Value{piped})
	if err != nil {
		return runtime.Null(), err
	}
	// Sandbox arrow gating (B13): a callable passed to a higher-order filter
	// (map/filter/sort/reduce/find) must be a template-defined arrow when the
	// sandbox is active, so an untrusted template cannot route a collection op
	// through an arbitrary host callable smuggled in as a value.
	if err := in.checkArrowArgs(n, args); err != nil {
		return runtime.Null(), err
	}
	// String-coercion gate (B12) for the coercing filters (join/replace/split):
	// these coerce host objects to text inside ext, beyond the policy's reach, so
	// gate any host object in their arguments here at the choke point.
	if err := in.checkStringifyArgs(n.Str, args); err != nil {
		return runtime.Null(), posErr(n, err)
	}
	args = in.injectFilter(filt, ctx, args)
	res, err := filt.Fn(args)
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	return res, nil
}

// evalTest handles "x is name" / "x is not name(arg)". `is defined` is special:
// it flips its operand to existence-check mode and never evaluates the value
// (spec 03 Section 4, spec 04 Section 8.3). Other tests evaluate the operand.
func (in *interp) evalTest(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	if n.Str == "defined" {
		ok := in.isDefined(n.Child(0), ctx)
		if n.Bool {
			ok = !ok
		}
		return runtime.Bool(ok), nil
	}
	subject, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	t, ok := in.eng.Extensions().Test(n.Str)
	if !ok {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"unknown test %q", n.Str))
	}
	// A test takes the subject first, then any explicit argument (children after
	// the subject are KindArg nodes).
	args := []runtime.Value{subject}
	for _, c := range n.Children[1:] {
		if c.Kind != ast.KindArg {
			continue
		}
		av, err := in.eval(c.Child(0), ctx, false)
		if err != nil {
			return runtime.Null(), err
		}
		args = append(args, av)
	}
	res, err := t.Fn(args)
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	result := res
	if n.Bool {
		result = !res
	}
	return runtime.Bool(result), nil
}

// isDefined evaluates the "is defined" question over an access chain without
// throwing: a bare name tests context presence; a.b / a[k] test member presence
// over a receiver evaluated under allowAbsent so an absent intermediate is false
// rather than an error (spec 04 Section 8.2, the whole-chain rule).
func (in *interp) isDefined(n *ast.Node, ctx *runtime.Context) bool {
	switch n.Kind {
	case ast.KindName:
		return ctx.Has(n.Str)
	case ast.KindAttr:
		recv, err := in.eval(n.Child(0), ctx, true)
		if err != nil || recv.IsNull() {
			return false
		}
		return runtime.IsDefinedAttribute(recv, runtime.Str(n.Str), runtime.AccessDot)
	case ast.KindIndex:
		recv, err := in.eval(n.Child(0), ctx, true)
		if err != nil || recv.IsNull() {
			return false
		}
		key, err := in.eval(n.Child(1), ctx, true)
		if err != nil {
			return false
		}
		return runtime.IsDefinedAttribute(recv, keyOf(key), runtime.AccessIndex)
	default:
		// Any other expression: it is defined iff it evaluates without error.
		_, err := in.eval(n, ctx, true)
		return err == nil
	}
}

// collectArgs flattens a call/filter node's KindArg children into a positional
// argument slice, prepended with prefix (the piped value for a filter). Named
// and spread arguments are flattened positionally. Host filters/functions/
// methods expose no parameter-name metadata on the ext surface, so a named arg
// to a host callable can only be passed in source order; macro calls use
// collectArgsNamed instead to bind by parameter name (design/expressions.md
// Section 7).
func (in *interp) collectArgs(n *ast.Node, ctx *runtime.Context, prefix []runtime.Value) ([]runtime.Value, error) {
	args := append([]runtime.Value(nil), prefix...)
	for _, c := range n.Children {
		if c.Kind != ast.KindArg {
			continue
		}
		switch c.Int {
		case ast.ArgPositional, ast.ArgNamed:
			v, err := in.eval(c.Child(0), ctx, false)
			if err != nil {
				return nil, err
			}
			args = append(args, v)
		case ast.ArgSpread:
			v, err := in.eval(c.Child(0), ctx, false)
			if err != nil {
				return nil, err
			}
			if v.Kind == runtime.KArray && v.Arr != nil {
				for _, p := range v.Arr.Pairs() {
					args = append(args, p.Val)
				}
			}
		}
	}
	return args, nil
}

// namedArg is one resolved name:value argument, kept ordered so a duplicate or
// unknown name reports deterministically.
type namedArg struct {
	name string
	val  runtime.Value
}

// collectArgsNamed splits a call's KindArg children into positional values and
// named arguments, so a macro can bind named args BY PARAMETER NAME rather than
// by source position (the silent-misbind that flattening would cause). A spread
// of a mapping contributes named arguments by string key; a spread of a
// sequence contributes positionals. design/expressions.md Section 7 requires
// named args to bind by name and appear in any order.
func (in *interp) collectArgsNamed(n *ast.Node, ctx *runtime.Context) (positional []runtime.Value, named []namedArg, err error) {
	for _, c := range n.Children {
		if c.Kind != ast.KindArg {
			continue
		}
		switch c.Int {
		case ast.ArgPositional:
			v, err := in.eval(c.Child(0), ctx, false)
			if err != nil {
				return nil, nil, err
			}
			positional = append(positional, v)
		case ast.ArgNamed:
			v, err := in.eval(c.Child(0), ctx, false)
			if err != nil {
				return nil, nil, err
			}
			named = append(named, namedArg{name: c.Str, val: v})
		case ast.ArgSpread:
			v, err := in.eval(c.Child(0), ctx, false)
			if err != nil {
				return nil, nil, err
			}
			if v.Kind == runtime.KArray && v.Arr != nil {
				for _, p := range v.Arr.Pairs() {
					if p.Key.Kind == runtime.KStr {
						named = append(named, namedArg{name: p.Key.S, val: p.Val})
					} else {
						positional = append(positional, p.Val)
					}
				}
			}
		}
	}
	return positional, named, nil
}

// injectFilter prepends the engine values a filter's Needs* flags request, in
// the fixed order environment, context, charset, ahead of the piped value and
// user arguments (spec 03 Section 3.6).
func (in *interp) injectFilter(f *ext.Filter, ctx *runtime.Context, args []runtime.Value) []runtime.Value {
	return in.inject(f.NeedsEnvironment, f.NeedsContext, f.NeedsCharset, ctx, args)
}

func (in *interp) invokeFunction(n *ast.Node, f *ext.Function, ctx *runtime.Context, args []runtime.Value) (runtime.Value, error) {
	args = in.inject(f.NeedsEnvironment, f.NeedsContext, f.NeedsCharset, ctx, args)
	res, err := f.Fn(args)
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	return res, nil
}

// inject builds the prepended engine-value slice. The environment is carried as
// a host Object so a callable can reach back into the engine (used by include/
// source/template_from_string); the context is materialized as an *Array of the
// live bindings for needs_context callables (include with_context, dump,
// template_from_string). ctx is the live call-site scope, threaded from
// evalCall/evalFilter, so a needs_context function sees the variables in scope
// where it was called. A nil ctx (no scope available) materializes as an empty
// mapping.
func (in *interp) inject(needsEnv, needsCtx, needsCharset bool, ctx *runtime.Context, args []runtime.Value) []runtime.Value {
	var pre []runtime.Value
	if needsEnv {
		pre = append(pre, runtime.Obj(&engineRef{eng: in.eng, in: in}))
	}
	if needsCtx {
		a := runtime.NewArray()
		if ctx != nil {
			for _, name := range ctx.Names() {
				v, _ := ctx.Get(name)
				a.SetStr(name, v)
			}
		}
		pre = append(pre, runtime.Arr(a))
	}
	if needsCharset {
		pre = append(pre, runtime.Str("UTF-8"))
	}
	if len(pre) == 0 {
		return args
	}
	return append(pre, args...)
}

// callRange is the `..` operator, sharing the range engine with the range
// function (spec 03 Section 3.1).
func callRange(in *interp, low, high runtime.Value) (runtime.Value, error) {
	fn, ok := in.eng.Extensions().Function("range")
	if !ok {
		return runtime.Null(), errors.New(errors.KindRuntime, "range function is not registered")
	}
	return fn.Fn([]runtime.Value{low, high})
}

// callSliceFilter invokes the slice filter for the a[start:end] operator.
func callSliceFilter(in *interp, args []runtime.Value) (runtime.Value, error) {
	f, ok := in.eng.Extensions().Filter("slice")
	if !ok {
		return runtime.Null(), errors.New(errors.KindRuntime, "slice filter is not registered")
	}
	return f.Fn(args)
}

// --- host-object shims used to thread engine state through callables ---

// macroRef is the value a bare macro name resolves to so it can be passed and
// called; the call site handles macroRef before it reaches a method dispatch.
type macroRef struct{ name string }

// GetField exposes no fields on a macro reference, returning (null, false).
func (m *macroRef) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }

// CallMethod rejects method calls on a macro reference; it is resolved at the
// call site, not dispatched as a method.
func (m *macroRef) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), errors.New(errors.KindRuntime, "macro reference is not directly callable")
}

// selfRef exposes a template's macros for the _self import path (me.tree()).
type selfRef struct{ tmpl *Template }

// GetField exposes no fields on a self reference, returning (null, false).
func (s *selfRef) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }

// CallMethod rejects direct method calls; call an imported macro via ns.macro().
func (s *selfRef) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), errors.New(errors.KindRuntime, "use ns.macro() to call an imported macro")
}

// importNS is the namespace value bound by @import "x" as ns; ns.macro() calls a
// macro defined in template x.
type importNS struct{ tmpl *Template }

// GetField exposes no fields on an import namespace, returning (null, false).
func (i *importNS) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }

// CallMethod rejects a bare call on the namespace; invoke ns.macro() to call a
// macro defined in the imported template.
func (i *importNS) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), errors.New(errors.KindRuntime, "use ns.macro() to call an imported macro")
}

// engineRef carries the engine and the live interp into a needs_environment
// callable (the include/block functions registered by the facade reach back
// through it).
type engineRef struct {
	eng Engine
	in  *interp
}

// GetField exposes no fields on an engine handle, returning (null, false).
func (e *engineRef) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }

// CallMethod rejects method calls on the engine handle; it only threads engine
// state into needs_environment callables and is not itself callable.
func (e *engineRef) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), errors.New(errors.KindRuntime, "engine handle is not directly callable")
}

// EngineFromValue recovers the Engine from the host Object the runtime injects
// into a needs_environment callable (the engineRef shim). It lets a callable
// registered outside this package -- e.g. the facade's include() -- reach the
// engine without exporting the shim type.
func EngineFromValue(v runtime.Value) (Engine, bool) {
	if v.Kind != runtime.KObject {
		return nil, false
	}
	if ref, ok := v.Obj.(*engineRef); ok {
		return ref.eng, true
	}
	return nil, false
}

// SandboxActiveFromValue reports whether the render that injected the engine
// handle (the engineRef shim) is currently sandboxed. The function-form include
// uses it to honor B16: a nested include inside an active sandbox stays
// sandboxed even when the call did not pass sandboxed: true.
func SandboxActiveFromValue(v runtime.Value) bool {
	if v.Kind != runtime.KObject {
		return false
	}
	if ref, ok := v.Obj.(*engineRef); ok && ref.in != nil {
		return ref.in.sandboxOn
	}
	return false
}

// RenderSandboxed renders tmpl with the given top-level variables under a forced
// sandbox gate, backing the function-form include's sandboxed: true flag (spec
// 03 Section 3.2, design/escaping-safety Section 6.6). It is Render with the
// per-render sandbox turned on regardless of the engine's global setting; the
// Phase-1 check and runtime gates then enforce the policy for this render.
func RenderSandboxed(eng Engine, tmpl *Template, vars map[string]runtime.Value) (string, error) {
	var b strings.Builder
	in := newInterp(eng, tmpl, &b)
	in.sandboxOn = true
	ctx := runtime.NewContext()
	for k, v := range vars {
		ctx.Set(k, v)
	}
	if err := in.renderTemplate(tmpl, ctx); err != nil {
		return b.String(), err
	}
	return in.resolveSlots(b.String()), nil
}
