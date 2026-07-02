package quill

import (
	"io"
	"log"

	"github.com/avmnu-sng/quill-template-engine/cache"
	"github.com/avmnu-sng/quill-template-engine/check"
	"github.com/avmnu-sng/quill-template-engine/cover"
	"github.com/avmnu-sng/quill-template-engine/ext"
	"github.com/avmnu-sng/quill-template-engine/interp"
	"github.com/avmnu-sng/quill-template-engine/loader"
	"github.com/avmnu-sng/quill-template-engine/parse"
	"github.com/avmnu-sng/quill-template-engine/runtime"
	"github.com/avmnu-sng/quill-template-engine/sandbox"
	"github.com/avmnu-sng/quill-template-engine/source"
)

// Environment is the engine facade: it ties a Loader, a parse cache, and the
// callable registry together and renders templates by name or from a string. It
// implements interp.Engine so the tree-walking renderer can load parents,
// includes, and imports through it.
//
// The defaults match the spec: autoescape is OFF (the source-emission default,
// spec 04 Section 8.1) and strict_variables is ON (the strict-by-default
// undefined policy, spec 04 Section 6). Both are configurable via Option.
type Environment struct {
	loader      loader.Loader
	cache       *cache.Cache
	renderCache *cache.RenderCache
	extensions  *ext.ExtensionSet

	// hostLayers are the host-supplied extension layers gathered by WithExtensions
	// and WithExtension, kept in one ordered list so sets and bundles interleave in
	// the exact order the options were passed. They are folded into the registry in
	// New, after the core stdlib floor and the engine-bound callables, so a later
	// layer shadows an earlier one and every host layer shadows core (spec 03
	// Section 1). Composition is deferred to New so the engine callables
	// (include/block family) are in place first and a host layer can shadow them.
	hostLayers []hostLayer

	autoescapeHTML  bool
	strictVariables bool

	randomSeed    int64
	randomSeedSet bool

	// policy is the host-supplied sandbox security policy, or nil when the host
	// did not configure one. sandboxActive is the global activation gate: when
	// true every render is sandboxed (spec 04 Section 8.3). The @sandbox region
	// and the function-form include's sandboxed flag turn the sandbox on locally
	// regardless of sandboxActive.
	policy        *sandbox.Policy
	sandboxActive bool

	// typeRegistry supplies the gradual type checker's host static-typing surface:
	// the Object<"Name"> member shapes and host callable signatures (spec 04
	// Section 1, design/type-system.md). It is consulted by check.Check at Load
	// time. A nil registry means the host registered no static types, so Object
	// types are opaque-but-known and host callables are checked only dynamically;
	// the checker still enforces every annotation the template itself carries. The
	// checker NEVER changes runtime output -- it only rejects ill-typed templates
	// earlier, so an unannotated template renders byte-identically with or without
	// a registry (the binding invariant).
	typeRegistry *check.Registry

	// coverage is the host-attached template-coverage Collector, or nil when
	// coverage is off (the default). When set, the interpreter seeds each rendered
	// template and records a hit at every coverable point, unioning across every
	// Render on this Environment (package cover, docs/coverage.md). A nil Collector
	// makes every interpreter coverage hook a single nil-check, so an Environment
	// without WithCoverage renders exactly as it does today -- zero overhead when
	// disabled, and instrumentation never changes rendered bytes.
	coverage *cover.Collector

	// tabWidth is the number of spaces one indent level expands to for the tab
	// filter, the tab/space/break indentation functions, and the @tab region. The
	// default is 4 (WithTabWidth). A value below zero clamps to zero.
	tabWidth int

	// logger is the sink the @log statement writes to. It is never nil: when the
	// host configures none it is a logger over io.Discard, so @log always has a
	// valid destination and produces no rendered output.
	logger *log.Logger
}

// Option configures an Environment at construction.
type Option func(*Environment)

// WithAutoescapeHTML turns the default output strategy to html (off by default).
func WithAutoescapeHTML(on bool) Option {
	return func(e *Environment) { e.autoescapeHTML = on }
}

// WithStrictVariables sets strict-undefined handling (on by default). Setting it
// false enables the lenient mode: an undefined read becomes Null and a for over a
// non-iterable becomes an empty loop (spec 04 Section 6).
func WithStrictVariables(on bool) Option {
	return func(e *Environment) { e.strictVariables = on }
}

// WithRandomSeed fixes the seed of the engine's randomness functions (random,
// shuffle), making their output deterministic and reproducible. This is the
// documented determinism mechanism for tests and golden output (spec 03 Section
// 3.2, X15); without it the functions draw from a time-seeded source. It is a
// host/environment knob, distinct from any template-author argument.
func WithRandomSeed(seed int64) Option {
	return func(e *Environment) {
		e.randomSeed = seed
		e.randomSeedSet = true
	}
}

// hostLayer is one host-supplied extension layer: exactly one of set or bundle
// is non-nil. New folds each layer into the registry in slice order, so a single
// list captures the interleaving of WithExtensions and WithExtension options.
type hostLayer struct {
	set    *ext.ExtensionSet
	bundle ext.Extension
}

// WithExtensions layers one or more host callable sets over the core stdlib.
// Each set is folded into the registry in New, after the core floor and the
// engine-bound callables, in the order given, so a later set shadows an earlier
// one and every host set shadows core (host shadows core, spec 03 Section 1).
// Multiple WithExtensions options accumulate in call order. Passing several sets
// in one call is equivalent to passing them across several options.
func WithExtensions(sets ...*ext.ExtensionSet) Option {
	return func(e *Environment) {
		for _, s := range sets {
			e.hostLayers = append(e.hostLayers, hostLayer{set: s})
		}
	}
}

// WithExtension layers one or more Extension bundles over the core stdlib. Each
// bundle is registered (its filters, functions, tests, constants, and enums
// folded in) in New, interleaved with WithExtensions layers in the order the
// options were passed, so shadow order is uniform across sets and bundles: later
// shadows earlier, and every host layer shadows core.
func WithExtension(exts ...ext.Extension) Option {
	return func(e *Environment) {
		for _, x := range exts {
			e.hostLayers = append(e.hostLayers, hostLayer{bundle: x})
		}
	}
}

// WithSandboxPolicy installs the host-supplied sandbox security policy: the
// allowlists for tags, filters, functions, per-type methods and properties, and
// the type-graph the per-type lookups walk (spec 04 Section 8.3). It does not by
// itself turn the sandbox on; activation is global (WithSandboxActive), per
// @sandbox region, or per sandboxed include. A policy is required for any of
// those to permit anything, since allowlisting is uniform with no grandfathering.
func WithSandboxPolicy(p *sandbox.Policy) Option {
	return func(e *Environment) { e.policy = p }
}

// WithSandboxActive turns the sandbox on globally so every render is sandboxed
// (the always-on activation path, design/escaping-safety Section 6.2). Without a
// policy an active sandbox denies everything.
func WithSandboxActive(on bool) Option {
	return func(e *Environment) { e.sandboxActive = on }
}

// WithTypes installs the host static-typing registry the gradual type checker
// consults: the Object<"Name"> member shapes and host callable signatures (spec
// 04 Section 1, design/type-system.md Sections 4.4, 9.1). It does not enable or
// change any runtime behavior; it only sharpens the load-time checker so an
// annotation referencing a host type or a host callable can be verified. With no
// registry the checker still enforces every in-template annotation; Object types
// are then opaque and host calls dynamic.
func WithTypes(reg *check.Registry) Option {
	return func(e *Environment) { e.typeRegistry = reg }
}

// WithCoverage attaches a template-coverage Collector so every Render on this
// Environment records which units and branch arms it exercised, unioning across
// renders (package cover, docs/coverage.md). It mirrors WithAutoescapeHTML /
// WithStrictVariables. WithCoverage(nil) is the same as not passing it: coverage
// stays off and the interpreter pays no per-node cost. Coverage instrumentation
// only reads node positions and increments counters, so a template renders
// byte-identically with or without it (the binding invariant).
func WithCoverage(coll *cover.Collector) Option {
	return func(e *Environment) { e.coverage = coll }
}

// WithTabWidth sets the number of spaces one indent level expands to for the tab
// filter, the tab/space/break indentation functions, and the @tab region (spec
// 03 Section 5.1). The default is 4. A width below zero clamps to zero (an indent
// level then contributes nothing).
func WithTabWidth(spaces int) Option {
	return func(e *Environment) {
		if spaces < 0 {
			spaces = 0
		}
		e.tabWidth = spaces
	}
}

// WithLogger sets the destination the @log statement writes to. The default is a
// discarding logger, so @log is inert until a host attaches a sink. @log produces
// no rendered output regardless of the logger.
func WithLogger(l *log.Logger) Option {
	return func(e *Environment) {
		if l != nil {
			e.logger = l
		}
	}
}

// New builds an Environment over a Loader with the given options. The registry
// is layered bottom-up: the core stdlib is the floor, then the engine-bound
// include/block-family callables, then each host extension set or bundle
// supplied via WithExtensions/WithExtension in option order. A later layer
// shadows an earlier one and every host layer shadows core (host callables
// shadow core, spec 03 Section 1).
func New(ldr loader.Loader, opts ...Option) *Environment {
	e := &Environment{
		loader:          ldr,
		cache:           cache.New(),
		renderCache:     cache.NewRenderCache(),
		extensions:      ext.Core(),
		autoescapeHTML:  false,
		strictVariables: true,
		tabWidth:        4,
		logger:          log.New(io.Discard, "", 0),
	}
	for _, opt := range opts {
		opt(e)
	}
	e.registerEngineCallables()
	// Fold the host layers over the core floor and the engine callables, in option
	// order, so host additions shadow everything (spec 03 Section 1).
	for _, layer := range e.hostLayers {
		switch {
		case layer.set != nil:
			e.extensions.Merge(layer.set)
		case layer.bundle != nil:
			e.extensions.Register(layer.bundle)
		}
	}
	return e
}

// NewWithArray is a convenience constructor over an in-memory template map.
func NewWithArray(templates map[string]string, opts ...Option) *Environment {
	return New(loader.NewArrayLoader(templates), opts...)
}

// Extensions returns the callable registry (interp.Engine).
func (e *Environment) Extensions() *ext.ExtensionSet { return e.extensions }

// StrictVariables reports the undefined-handling policy (interp.Engine).
func (e *Environment) StrictVariables() bool { return e.strictVariables }

// AutoescapeHTML reports the default output strategy (interp.Engine).
func (e *Environment) AutoescapeHTML() bool { return e.autoescapeHTML }

// RandomSeed returns the configured RNG seed and whether one was set (interp.Engine).
func (e *Environment) RandomSeed() (int64, bool) { return e.randomSeed, e.randomSeedSet }

// RenderCache returns the engine's rendered-body cache, backing @cache
// (interp.Engine, spec 01 Section 4.7).
func (e *Environment) RenderCache() *cache.RenderCache { return e.renderCache }

// Policy returns the host-supplied sandbox security policy, or nil (interp.Engine).
func (e *Environment) Policy() *sandbox.Policy { return e.policy }

// SandboxActive reports the global sandbox activation gate (interp.Engine).
func (e *Environment) SandboxActive() bool { return e.sandboxActive }

// Coverage returns the host-attached coverage Collector, or nil when coverage is
// off (interp.Engine). The interpreter copies it into each render's cov field.
func (e *Environment) Coverage() *cover.Collector { return e.coverage }

// TabWidth returns the spaces-per-indent-level width backing the tab filter, the
// tab/space/break functions, and the @tab region (interp.Engine, WithTabWidth).
func (e *Environment) TabWidth() int { return e.tabWidth }

// Logger returns the sink the @log statement writes to (interp.Engine). It is
// never nil; without WithLogger it discards.
func (e *Environment) Logger() *log.Logger { return e.logger }

// LoadTemplate parses (memoized) and prepares the named template (interp.Engine).
func (e *Environment) LoadTemplate(name string) (*interp.Template, error) {
	if mod, ok := e.cache.Get(name); ok {
		return interp.PrepareChecked(name, mod)
	}
	src, err := e.loader.Get(name)
	if err != nil {
		return nil, err
	}
	mod, err := parse.Parse(src)
	if err != nil {
		return nil, err
	}
	// Run the gradual type checker once, at first load, before the module is
	// cached or prepared: an ill-typed template is rejected here, before any
	// render. An unannotated template types as all-any and the check is a no-op,
	// so this never alters rendered output (spec 04 Section 1).
	if err := check.Check(mod, e.typeRegistry); err != nil {
		return nil, err
	}
	e.cache.Put(name, mod)
	return interp.PrepareChecked(name, mod)
}

// TemplateExists reports whether the named template can be loaded (interp.Engine).
func (e *Environment) TemplateExists(name string) bool {
	if _, ok := e.cache.Get(name); ok {
		return true
	}
	return e.loader.Exists(name)
}

// RawSource returns the unparsed source text of the named template, backing the
// source() function (interp.Engine, spec 03 Section 3.2).
func (e *Environment) RawSource(name string) (string, bool) {
	src, err := e.loader.Get(name)
	if err != nil {
		return "", false
	}
	return src.Code(), true
}

// CompileString parses and prepares an ad-hoc template body, backing
// template_from_string (interp.Engine, spec 03 Section 3.3). The body is not
// added to the loader; inheritance/include targets in it still resolve by name.
func (e *Environment) CompileString(name, body string) (*interp.Template, error) {
	mod, err := parse.Parse(source.New(name, body))
	if err != nil {
		return nil, err
	}
	if err := check.Check(mod, e.typeRegistry); err != nil {
		return nil, err
	}
	return interp.PrepareChecked(name, mod)
}

// Render loads the named template and renders it with vars, returning the output.
func (e *Environment) Render(name string, vars map[string]runtime.Value) (string, error) {
	tmpl, err := e.LoadTemplate(name)
	if err != nil {
		return "", err
	}
	return interp.Render(e, tmpl, vars)
}

// RenderString parses an ad-hoc template body (not added to the loader) and
// renders it. Inheritance/include/import targets in the body still resolve
// through the loader by name.
func (e *Environment) RenderString(name, body string, vars map[string]runtime.Value) (string, error) {
	mod, err := parse.Parse(source.New(name, body))
	if err != nil {
		return "", err
	}
	if err := check.Check(mod, e.typeRegistry); err != nil {
		return "", err
	}
	tmpl, err := interp.PrepareChecked(name, mod)
	if err != nil {
		return "", err
	}
	return interp.Render(e, tmpl, vars)
}

// RenderTo loads the named template and renders it directly to w. When the
// template closure uses no deferred-slot construct (@yield, @provide, slot()),
// the output streams to w incrementally with bounded memory; otherwise the
// full output is buffered, its slot placeholders are resolved, and the result
// is written to w in one call (nothing is written on a render error). The
// bytes written equal Render's returned string in both cases. RenderTo neither
// wraps nor flushes w; pass a bufio.Writer for buffered throughput and flush
// it after RenderTo returns (a @flush statement flushes such a writer
// mid-render).
func (e *Environment) RenderTo(w io.Writer, name string, vars map[string]runtime.Value) error {
	tmpl, err := e.LoadTemplate(name)
	if err != nil {
		return err
	}
	return interp.RenderTo(e, tmpl, vars, w)
}

// RenderToValues renders the named template directly to w like RenderTo, but
// from native Go bindings: each value in vars is marshaled through
// runtime.FromGo exactly as RenderValues does.
func (e *Environment) RenderToValues(w io.Writer, name string, vars map[string]any) error {
	rv, err := fromGoVars(vars)
	if err != nil {
		return err
	}
	return e.RenderTo(w, name, rv)
}

// RenderStringTo parses an ad-hoc template body (not added to the loader) and
// renders it directly to w with RenderTo's streaming-vs-buffered behavior.
// Inheritance/include/import targets in the body still resolve through the
// loader by name.
func (e *Environment) RenderStringTo(w io.Writer, name, body string, vars map[string]runtime.Value) error {
	mod, err := parse.Parse(source.New(name, body))
	if err != nil {
		return err
	}
	if err := check.Check(mod, e.typeRegistry); err != nil {
		return err
	}
	tmpl, err := interp.PrepareChecked(name, mod)
	if err != nil {
		return err
	}
	return interp.RenderTo(e, tmpl, vars, w)
}

// Display renders the named template directly into w -- the push model of the
// Template contract, under its traditional name. It is RenderTo: a slot-free
// template closure streams with bounded memory, a slot-using one buffers then
// writes, and the bytes written equal Render's returned string.
func (e *Environment) Display(w io.Writer, name string, vars map[string]runtime.Value) error {
	return e.RenderTo(w, name, vars)
}

// RenderValues renders the named template from native Go bindings: each value in
// vars is marshaled through runtime.FromGo, so a host can pass ordinary Go
// scalars, slices, maps, structs, and nested combinations without hand-building
// runtime.Value bindings. A value that is already a runtime.Value passes
// through unchanged, so hand-built and native bindings mix freely. An
// unsupported Go kind (a channel, a bare function, a complex number) returns the
// typed marshaling error and renders nothing.
func (e *Environment) RenderValues(name string, vars map[string]any) (string, error) {
	rv, err := fromGoVars(vars)
	if err != nil {
		return "", err
	}
	return e.Render(name, rv)
}

// RenderStringValues parses an ad-hoc template body (not added to the loader)
// and renders it from native Go bindings, marshaling each value through
// runtime.FromGo exactly as RenderValues does. Inheritance/include/import
// targets in the body still resolve through the loader by name.
func (e *Environment) RenderStringValues(name, body string, vars map[string]any) (string, error) {
	rv, err := fromGoVars(vars)
	if err != nil {
		return "", err
	}
	return e.RenderString(name, body, rv)
}

// fromGoVars marshals a native binding map into the runtime.Value map the render
// entry points consume, failing on the first value FromGo rejects.
func fromGoVars(vars map[string]any) (map[string]runtime.Value, error) {
	if vars == nil {
		return nil, nil
	}
	out := make(map[string]runtime.Value, len(vars))
	for name, v := range vars {
		rv, err := runtime.FromGo(v)
		if err != nil {
			return nil, err
		}
		out[name] = rv
	}
	return out, nil
}
