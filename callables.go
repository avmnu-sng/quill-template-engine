package quill

import (
	"math/rand"
	"strings"
	"time"

	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/ext"
	"github.com/avmnu-sng/quill-template-engine/interp"
	"github.com/avmnu-sng/quill-template-engine/runtime"
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
	// random/shuffle are re-registered as needs_environment overrides so they can
	// honor a host RNG seed (WithRandomSeed) for deterministic output, the spec's
	// documented test-determinism mechanism (spec 03 Section 3.2, X15). The plain
	// ext forms remain the fallback when no seed is set.
	e.extensions.AddFunction(&ext.Function{
		Name:             "random",
		NeedsEnvironment: true,
		Fn:               randomFn,
	})
	e.extensions.AddFilter(&ext.Filter{
		Name:             "shuffle",
		NeedsEnvironment: true,
		Fn:               shuffleFn,
	})
	// tab is re-registered as a needs_environment override (filter AND function) so
	// its indent level honors the host WithTabWidth (default 4 spaces per level).
	// The plain ext forms remain the standalone default when no engine is present.
	e.extensions.AddFilter(&ext.Filter{
		Name:             "tab",
		NeedsEnvironment: true,
		Fn:               tabFilterFn,
	})
	e.extensions.AddFunction(&ext.Function{
		Name:             "tab",
		NeedsEnvironment: true,
		Fn:               tabFunctionFn,
	})
}

// engineTabWidth returns the host-configured spaces-per-indent-level width from
// the injected engine handle, or the ext default when the handle is missing.
func engineTabWidth(envRef runtime.Value) int {
	if eng, ok := interp.EngineFromValue(envRef); ok {
		return eng.TabWidth()
	}
	return ext.DefaultTabWidth
}

// tabFilterFn is the width-aware tab filter: args are [env, piped, levels?].
func tabFilterFn(args []runtime.Value) (runtime.Value, error) {
	if len(args) == 0 {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"tab did not receive the engine handle")
	}
	return ext.TabWith(engineTabWidth(args[0]), args[1:])
}

// tabFunctionFn is the width-aware tab() function: args are [env, levels?].
func tabFunctionFn(args []runtime.Value) (runtime.Value, error) {
	if len(args) == 0 {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"tab() did not receive the engine handle")
	}
	return ext.TabFnWith(engineTabWidth(args[0]), args[1:])
}

// engineRNG returns a source seeded from the host's configured seed when one is
// set, else a fresh time-seeded source (spec 03 Section 3.2, X15). args[0] is the
// injected engine handle.
func engineRNG(envRef runtime.Value) *rand.Rand {
	if eng, ok := interp.EngineFromValue(envRef); ok {
		if seed, set := eng.RandomSeed(); set {
			return rand.New(rand.NewSource(seed))
		}
	}
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

// randomFn is the seed-aware random(): args are [env, values?, max?].
func randomFn(args []runtime.Value) (runtime.Value, error) {
	if len(args) == 0 {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"random() did not receive the engine handle")
	}
	return ext.RandomWith(engineRNG(args[0]), args[1:])
}

// shuffleFn is the seed-aware shuffle filter: args are [env, collection, seed?].
func shuffleFn(args []runtime.Value) (runtime.Value, error) {
	if len(args) == 0 {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"shuffle did not receive the engine handle")
	}
	return ext.ShuffleWith(engineRNG(args[0]), args[1:])
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
// Go-native structured format (spec 03 Section 3.3). args are [ctx, vars...].
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
// ignore_missing, sandboxed) returning the rendered output as a value (spec 03
// Section 3.2). The runtime prepends the environment handle and the live context
// per the Needs* flags, so args are [env, ctx, template, vars?, with_context?,
// ignore_missing?, sandboxed?]. The sandboxed flag forces the included render
// into the sandbox; a render already inside an active sandbox stays sandboxed
// regardless of the flag (B16, design/escaping-safety Section 6.6).
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
	// sandboxed: true forces this include into the sandbox; an enclosing render
	// that is already sandboxed keeps the include sandboxed even without the flag.
	sandboxed := interp.SandboxActiveFromValue(envRef)
	if len(args) > 6 {
		sandboxed = sandboxed || runtime.Truthy(args[6])
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

	var out string
	if sandboxed {
		out, err = interp.RenderSandboxed(eng, tmpl, vars)
	} else {
		out, err = interp.Render(eng, tmpl, vars)
	}
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Str(out), nil
}
