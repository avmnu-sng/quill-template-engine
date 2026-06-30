package quill

import (
	"strings"

	"github.com/avmnusng/quill-template-engine/errors"
	"github.com/avmnusng/quill-template-engine/ext"
	"github.com/avmnusng/quill-template-engine/interp"
	"github.com/avmnusng/quill-template-engine/runtime"
)

// registerEngineCallables installs the callables that need the engine handle to
// render other templates (the include function, spec 03 Section 3.2). They are
// registered after ext.Core and before host overrides so a host can still shadow
// them.
func (e *Environment) registerEngineCallables() {
	e.extensions.AddFunction(&ext.Function{
		Name:             "include",
		NeedsEnvironment: true,
		NeedsContext:     true,
		Fn:               includeFn,
	})
	// source(name, ignore_missing?) returns the raw unparsed source of a template
	// (spec 03 Section 3.2). It needs the engine handle to reach the loader.
	e.extensions.AddFunction(&ext.Function{
		Name:             "source",
		NeedsEnvironment: true,
		Fn:               sourceFn,
	})
	// template_from_string(source, name?) compiles a string into a template and
	// renders it with the live context (spec 03 Section 3.3). Security-sensitive;
	// available because the host opted in by constructing this Environment.
	e.extensions.AddFunction(&ext.Function{
		Name:             "template_from_string",
		NeedsEnvironment: true,
		NeedsContext:     true,
		Fn:               templateFromStringFn,
	})
	// dump(...vars) debug-dumps the given values, or the whole context if none, in
	// a Go-native structured format (spec 03 Section 3.3).
	e.extensions.AddFunction(&ext.Function{
		Name:         "dump",
		NeedsContext: true,
		Fn:           dumpFn,
	})
}

// sourceFn returns the raw source of a template; a miss is an error unless
// ignore_missing is truthy (spec 03 Section 3.2). args are [env, name,
// ignore_missing?].
func sourceFn(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 {
		return runtime.Null(), errors.New(errors.KindRuntime, "source() requires a template name")
	}
	eng, ok := interp.EngineFromValue(args[0])
	if !ok {
		return runtime.Null(), errors.New(errors.KindRuntime, "source() did not receive the engine handle")
	}
	name, err := runtime.ToText(args[1])
	if err != nil {
		return runtime.Null(), err
	}
	ignoreMissing := len(args) > 2 && runtime.Truthy(args[2])
	raw, found := eng.RawSource(name)
	if !found {
		if ignoreMissing {
			return runtime.Str(""), nil
		}
		return runtime.Null(), errors.New(errors.KindRuntime, "source template %q not found", name)
	}
	return runtime.Str(raw), nil
}

// templateFromStringFn compiles the given source and renders it with the live
// context (spec 03 Section 3.3). args are [env, ctx, source, name?].
func templateFromStringFn(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 3 {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"template_from_string() requires a source string")
	}
	eng, ok := interp.EngineFromValue(args[0])
	if !ok {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"template_from_string() did not receive the engine handle")
	}
	ctxArr := args[1]
	body, err := runtime.ToText(args[2])
	if err != nil {
		return runtime.Null(), err
	}
	name := "<template_from_string>"
	if len(args) > 3 && !args[3].IsNull() {
		if n, err := runtime.ToText(args[3]); err == nil {
			name = n
		}
	}
	tmpl, err := eng.CompileString(name, body)
	if err != nil {
		return runtime.Null(), err
	}
	vars := map[string]runtime.Value{}
	if ctxArr.Kind == runtime.KArray && ctxArr.Arr != nil {
		for _, p := range ctxArr.Arr.Pairs() {
			key, _ := runtime.ToText(p.Key)
			vars[key] = p.Val
		}
	}
	out, err := interp.Render(eng, tmpl, vars)
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Str(out), nil
}

// dumpFn debug-dumps its arguments (or the whole context if none) in a
// Go-native structured format, NOT PHP var_dump (spec 03 Section 3.3). args are
// [ctx, vars...].
func dumpFn(args []runtime.Value) (runtime.Value, error) {
	if len(args) == 0 {
		return runtime.Str(""), nil
	}
	ctxArr := args[0]
	rest := args[1:]
	if len(rest) == 0 {
		// No explicit args: dump the whole context as a mapping.
		return runtime.Str(ext.Dump(ctxArr)), nil
	}
	var parts []string
	for _, v := range rest {
		parts = append(parts, ext.Dump(v))
	}
	return runtime.Str(strings.Join(parts, "\n")), nil
}

// includeFn is the function-form include: include(template, vars, with_context,
// ...) returning the rendered output as a value (spec 03 Section 3.2). The
// runtime prepends the environment handle and the live context per the Needs*
// flags, so args are [env, ctx, template, vars?, with_context?, ignore_missing?].
func includeFn(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 3 {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"include() requires a template name")
	}
	envRef := args[0]
	ctxArr := args[1]
	nameVal := args[2]

	eng, ok := interp.EngineFromValue(envRef)
	if !ok {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"include() did not receive the engine handle")
	}
	name, err := runtime.ToText(nameVal)
	if err != nil {
		return runtime.Null(), err
	}

	withContext := true
	if len(args) > 4 {
		withContext = runtime.Truthy(args[4])
	}
	ignoreMissing := false
	if len(args) > 5 {
		ignoreMissing = runtime.Truthy(args[5])
	}

	if !eng.TemplateExists(name) {
		if ignoreMissing {
			return runtime.Str(""), nil
		}
		return runtime.Null(), errors.New(errors.KindRuntime,
			"included template %q not found", name)
	}
	tmpl, err := eng.LoadTemplate(name)
	if err != nil {
		if ignoreMissing {
			return runtime.Str(""), nil
		}
		return runtime.Null(), err
	}

	vars := map[string]runtime.Value{}
	if withContext && ctxArr.Kind == runtime.KArray && ctxArr.Arr != nil {
		for _, p := range ctxArr.Arr.Pairs() {
			key, _ := runtime.ToText(p.Key)
			vars[key] = p.Val
		}
	}
	// The explicit vars map (arg 3) overrides context vars.
	if len(args) > 3 && args[3].Kind == runtime.KArray && args[3].Arr != nil {
		for _, p := range args[3].Arr.Pairs() {
			key, _ := runtime.ToText(p.Key)
			vars[key] = p.Val
		}
	}

	out, err := interp.Render(eng, tmpl, vars)
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Str(out), nil
}
