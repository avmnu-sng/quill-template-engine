package interp

import (
	"github.com/avmnusng/quill-template-engine/ast"
	"github.com/avmnusng/quill-template-engine/errors"
	"github.com/avmnusng/quill-template-engine/ext"
	"github.com/avmnusng/quill-template-engine/runtime"
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
		}
		if _, ok := in.macros[name]; ok {
			return in.callMacro(n, name, ctx)
		}
		if fn, ok := in.eng.Extensions().Function(name); ok {
			args, err := in.collectArgs(n, ctx, nil)
			if err != nil {
				return runtime.Null(), err
			}
			return in.invokeFunction(n, fn, args)
		}
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"unknown function or macro %q", name))
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
// and spread arguments are supported; named args are appended after positionals
// for the simple positional-binding callables of this slice (the core stdlib
// uses positional parameters), preserving named values so a host callable that
// inspects them still receives them in order.
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

// injectFilter prepends the engine values a filter's Needs* flags request, in
// the fixed order environment, context, charset, ahead of the piped value and
// user arguments (spec 03 Section 3.6).
func (in *interp) injectFilter(f *ext.Filter, ctx *runtime.Context, args []runtime.Value) []runtime.Value {
	return in.inject(f.NeedsEnvironment, f.NeedsContext, f.NeedsCharset, ctx, args)
}

func (in *interp) invokeFunction(n *ast.Node, f *ext.Function, args []runtime.Value) (runtime.Value, error) {
	args = in.inject(f.NeedsEnvironment, f.NeedsContext, f.NeedsCharset, nil, args)
	res, err := f.Fn(args)
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	return res, nil
}

// inject builds the prepended engine-value slice. The environment and context
// are carried as host Objects so a callable can reach back into the engine
// (used by include/block). ctx may be nil for a function call, in which case the
// live context for needs_context comes from the call site is unavailable and a
// nil placeholder is passed; the core stdlib functions registered here do not
// set needs_context, and the engine-registered include/block supply it
// explicitly through a bound closure.
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

func (m *macroRef) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }
func (m *macroRef) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), errors.New(errors.KindRuntime, "macro reference is not directly callable")
}

// selfRef exposes a template's macros for the _self import path (me.tree()).
type selfRef struct{ tmpl *Template }

func (s *selfRef) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }
func (s *selfRef) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), errors.New(errors.KindRuntime, "use ns.macro() to call an imported macro")
}

// importNS is the namespace value bound by @import "x" as ns; ns.macro() calls a
// macro defined in template x.
type importNS struct{ tmpl *Template }

func (i *importNS) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }
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

func (e *engineRef) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }
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
