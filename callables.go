package quill

import (
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
