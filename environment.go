package quill

import (
	"context"
	"io"
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/avmnu-sng/quill-template-engine/internal/interp"
	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/cache"
	"github.com/avmnu-sng/quill-template-engine/pkg/check"
	"github.com/avmnu-sng/quill-template-engine/pkg/compiled"
	"github.com/avmnu-sng/quill-template-engine/pkg/cover"
	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/loader"
	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
	"github.com/avmnu-sng/quill-template-engine/pkg/sandbox"
	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// Environment is the engine facade: it ties a Loader, a parse cache, and the
// callable registry together and renders templates by name or from a string. It
// hands the tree-walking renderer an unexported engineAdapter (engine_adapter.go)
// so the renderer can load parents, includes, and imports through it without the
// interpreter's Engine contract appearing on the Environment's public surface.
//
// The defaults match the spec: autoescape is OFF by default (spec 04
// Section 8.1) and strict_variables is ON (the strict-by-default
// undefined policy, spec 04 Section 6). Both are configurable via Option.
//
// A constructed Environment is safe for concurrent use by multiple goroutines:
// Option values are applied only during New (or NewFromMap), and afterward every
// render and load entry point reads immutable configuration or concurrency-safe
// caches (the parse cache, the render cache, and the mutex-guarded prepared and
// compiled-unit memos), so one Environment can back concurrent renders (for
// example a single Environment per process serving concurrent requests).
// Construction is not itself concurrent; build the Environment fully before
// sharing it across goroutines.
type Environment struct {
	loader      loader.Loader
	cache       *cache.Cache
	renderCache *cache.RenderCache
	extensions  *ext.Set

	// prepared memoizes the prepared *interp.Template per template name so a warm
	// LoadTemplate skips re-running interp.PrepareChecked over an already-indexed
	// module; the parse cache alone still pays the full table build on every load.
	// The memo lives here rather than in package cache because interp imports
	// cache, so a Template-holding entry there would be an import cycle. Each
	// entry pins the parsed module it was prepared from, and a hit requires
	// pointer equality with the parse cache's current module, keeping the memo
	// coherent under any future parse-cache eviction: a re-parsed module can never
	// be served a stale Template. Prepared Templates are immutable after
	// PrepareChecked and shared read-only across renders, so racing duplicate
	// prepares are benign (idempotent, last-write-wins).
	preparedMu sync.RWMutex
	prepared   map[string]preparedEntry

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
	// checker NEVER changes runtime output; it only rejects ill-typed templates
	// earlier, so an unannotated template renders byte-identically with or without
	// a registry (the binding invariant).
	typeRegistry *check.Registry

	// coverage is the host-attached template-coverage Collector, or nil when
	// coverage is off (the default). When set, the interpreter seeds each rendered
	// template and records a hit at every coverable point, unioning across every
	// Render on this Environment (package cover, docs/coverage.md). A nil Collector
	// makes every interpreter coverage hook a single nil-check, so an Environment
	// without WithCoverage renders exactly as it does today: zero overhead when
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

	// compiledUnits maps a template name to its installed compiled unit
	// (WithCompiled). The map is populated while the construction options run
	// and read-only afterwards, so by-name dispatch reads it without locking;
	// each unit carries its own mutex-guarded source-coherence memo. A nil map
	// (no WithCompiled option) keeps every render on the interpreter path.
	compiledUnits map[string]*compiledUnit

	// compiledVerify is the shadow-verification callback (WithCompiledVerify),
	// or nil for direct compiled dispatch. When set, a by-name render that the
	// dispatch gate would serve compiled instead runs BOTH engines, serves the
	// interpreter's result, and reports any output or error-text divergence.
	compiledVerify func(compiled.Divergence)
}

// compiledUnit is one installed manifest plus this Environment's memo of its
// source-coherence verdict. The memo follows the prepared-cache pattern:
// witness pins the parsed module each member's byte-verification ran against,
// and a dispatch trusts the stored verdict only while every member's current
// module is pointer-identical to its witness. A re-parsed module (a parse
// cache eviction, a loader now serving different text) therefore forces a
// fresh byte-comparison, so the compiled path can never serve a render whose
// interpreter counterpart would walk different source.
type compiledUnit struct {
	manifest *compiled.Manifest
	// members lists the manifest's source names in sorted order, giving the
	// witness slice a stable member-to-index mapping.
	members []string

	mu      sync.Mutex
	witness []*ast.Node
	ok      bool
}

// preparedEntry is one memoized prepared template: the Template plus the parsed
// module it was prepared from. The module pointer is the coherence witness:
// LoadTemplate honors the entry only while the parse cache still serves the
// identical *ast.Node, so the memo can never outlive the parse it derives from.
type preparedEntry struct {
	mod  *ast.Node
	tmpl *interp.Template
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
	set    *ext.Set
	bundle ext.Bundle
}

// WithExtensions layers one or more host callable sets over the core stdlib.
// Each set is folded into the registry in New, after the core floor and the
// engine-bound callables, in the order given, so a later set shadows an earlier
// one and every host set shadows core (host shadows core, spec 03 Section 1).
// Multiple WithExtensions options accumulate in call order. Passing several sets
// in one call is equivalent to passing them across several options.
func WithExtensions(sets ...*ext.Set) Option {
	return func(e *Environment) {
		for _, s := range sets {
			e.hostLayers = append(e.hostLayers, hostLayer{set: s})
		}
	}
}

// WithExtension layers one or more Bundle values over the core stdlib. Each
// bundle is registered (its filters, functions, tests, constants, and enums
// folded in) in New, interleaved with WithExtensions layers in the order the
// options were passed, so shadow order is uniform across sets and bundles: later
// shadows earlier, and every host layer shadows core.
func WithExtension(exts ...ext.Bundle) Option {
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

// WithCompiled installs generated compiled units (the compile backend's
// exported manifests) for by-name dispatch. Render, RenderTo, and the
// entry points delegating to them serve a template whose name matches an
// installed manifest through the generated render function instead of the
// interpreter, but only when the dispatch gate proves the bytes cannot
// differ: the manifest's compile-options fingerprint must equal this
// Environment's configuration, no sandbox policy or activation, coverage
// collector, or host type registry may be configured, a unit containing @log
// must not lose lines a non-discarding logger would receive, and every member
// source must byte-equal the text the loader currently serves (verified once
// and re-verified whenever the parse cache serves a re-parsed module). Any
// unprovable condition falls back to the interpreter, so installing a
// manifest changes render cost, never rendered bytes or errors. A host
// callable flagged NeedsEnvironment sees the serving path's own engine
// handle: the interpreter's and the generated code's handles are distinct
// concrete types exposing identical configuration through ext.EngineConfig,
// the injected handle's documented surface. A manifest missing its render
// function, entry name, or entry source is ignored; a later manifest for the
// same entry name replaces an earlier one. Templates
// whose output depends on UNSEEDED randomness are the documented exception:
// compiled and interpreted draws come from independent time-seeded sources,
// so their output compares distributionally, never byte-wise.
func WithCompiled(manifests ...*compiled.Manifest) Option {
	return func(e *Environment) {
		for _, m := range manifests {
			if m == nil || m.Render() == nil || m.Entry() == "" {
				continue
			}
			if _, ok := m.Sources()[m.Entry()]; !ok {
				continue
			}
			if e.compiledUnits == nil {
				e.compiledUnits = map[string]*compiledUnit{}
			}
			members := make([]string, 0, len(m.Sources()))
			for name := range m.Sources() {
				members = append(members, name)
			}
			sort.Strings(members)
			e.compiledUnits[m.Entry()] = &compiledUnit{manifest: m, members: members}
		}
	}
}

// WithCompiledVerify switches the installed compiled units (WithCompiled) from
// direct dispatch to shadow verification: a by-name render the dispatch gate
// would serve compiled instead runs both engines, byte-compares the outputs
// and error text, reports any divergence to the report callback, and always
// serves the interpreter's result. It is the trust-building mode for a new
// unit: production traffic renders exactly as before while every would-be
// compiled render is checked against the authoritative interpreter. Under
// verification a dispatched RenderTo buffers the render instead of streaming,
// since both outputs must exist to compare; the interpreter's bytes
// (including the partial output of an errored render) are then written to w,
// so the writer receives exactly what the streaming paths would have written.
// WithCompiledVerify(nil) is the same as not passing it: direct dispatch
// stays on.
func WithCompiledVerify(report func(compiled.Divergence)) Option {
	return func(e *Environment) { e.compiledVerify = report }
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
		prepared:        map[string]preparedEntry{},
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

// NewFromMap is a convenience constructor over an in-memory template map.
func NewFromMap(templates map[string]string, opts ...Option) *Environment {
	return New(loader.NewArrayLoader(templates), opts...)
}

// The renderer-internal configuration getters the interpreter needs
// (StrictVariables, AutoescapeHTML, RandomSeed, Policy, SandboxActive, Coverage,
// TabWidth, Logger, and the load/compile/exists hooks) are NOT methods on
// *Environment: they live on the unexported engineAdapter (engine_adapter.go),
// which the interp entry points receive instead of the bare Environment. Keeping
// them off the public surface means installing the interpreter as an internal
// package cannot leak its Engine contract into the frozen host API.
//
// Extensions and RenderCache stay public: they return the public *ext.Set and
// *cache.RenderCache types (not interpreter internals) and are the documented
// calling convention for the compiled backend's generated render functions,
// whose signature takes exactly those two values. A host that installs a
// compiled unit renders through Environment.Render (WithCompiled), but a host or
// test that invokes a generated render function directly obtains its registry
// and rendered-body cache from here.

// Extensions returns the callable registry (core stdlib plus every host
// extension layer and the engine-bound include/block-family callables). It is
// the *ext.Set a compiled backend render function is invoked with.
func (e *Environment) Extensions() *ext.Set { return e.extensions }

// RenderCache returns the engine's rendered-body cache, backing @cache (spec 01
// Section 4.7). It is the *cache.RenderCache a compiled backend render function
// is invoked with.
func (e *Environment) RenderCache() *cache.RenderCache { return e.renderCache }

// LoadTemplate parses and prepares the named template and returns an opaque,
// read-only handle. Both steps are memoized: the parse cache pins the module and
// the prepared memo pins the template built from it, so a warm load is two map
// hits. LoadTemplate therefore wraps the SAME underlying template across calls
// for an unchanged template; a template is immutable after prepare and safe to
// share across concurrent renders. Hand the returned handle back to
// RenderPrepared to render it without re-loading. CompileString and the
// RenderString family stay uncached.
func (e *Environment) LoadTemplate(ctx context.Context, name string) (*Template, error) {
	tmpl, err := e.loadTemplate(ctx, name)
	if err != nil {
		return nil, err
	}
	return newTemplate(tmpl), nil
}

// loadTemplate is the unexported load path returning the interpreter's internal
// template for rendering. LoadTemplate wraps its result in an opaque *Template
// for the public boundary, while the engineAdapter and the render machinery use
// it directly.
func (e *Environment) loadTemplate(ctx context.Context, name string) (*interp.Template, error) {
	mod, err := e.loadModule(name)
	if err != nil {
		return nil, err
	}
	return e.prepare(name, mod)
}

// loadModule returns the named template's parsed module through the parse
// cache: a cold load reads the loader, parses, runs the load-time type check,
// and caches; a warm load is one cache hit. It is the shared first half of
// LoadTemplate and the compiled dispatch's coherence anchor: whatever module
// this returns is the module a render of name walks, so byte-checking its
// source text checks exactly what the interpreter would render.
func (e *Environment) loadModule(name string) (*ast.Node, error) {
	if mod, ok := e.cache.Get(name); ok {
		return mod, nil
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
	return mod, nil
}

// prepare returns the memoized prepared Template for name when the memo entry
// was built from this exact module, and runs interp.PrepareChecked then stores
// the result otherwise. The module pointer-equality guard ties the memo's
// staleness class to the parse cache's: whatever module the parse cache serves
// is the module the returned Template was prepared from. A PrepareChecked
// failure (an invalid literal regex) is never memoized, so every load of a
// malformed template reports the identical error the unmemoized path did.
func (e *Environment) prepare(name string, mod *ast.Node) (*interp.Template, error) {
	e.preparedMu.RLock()
	entry, ok := e.prepared[name]
	e.preparedMu.RUnlock()
	if ok && entry.mod == mod {
		return entry.tmpl, nil
	}
	tmpl, err := interp.PrepareChecked(name, mod)
	if err != nil {
		return nil, err
	}
	e.preparedMu.Lock()
	e.prepared[name] = preparedEntry{mod: mod, tmpl: tmpl}
	e.preparedMu.Unlock()
	return tmpl, nil
}

// templateExists reports whether the named template can be loaded. It backs the
// engineAdapter's TemplateExists and the compiled-dispatch coherence checks.
func (e *Environment) templateExists(name string) bool {
	if _, ok := e.cache.Get(name); ok {
		return true
	}
	return e.loader.Exists(name)
}

// rawSource returns the unparsed source text of the named template, backing the
// source() function through the engineAdapter (spec 03 Section 3.2).
func (e *Environment) rawSource(name string) (string, bool) {
	src, err := e.loader.Get(name)
	if err != nil {
		return "", false
	}
	return src.Code(), true
}

// CompileString parses and prepares an ad-hoc template body and returns an
// opaque, read-only handle, backing template_from_string (spec 03 Section 3.3).
// The body is not added to the loader; inheritance/include targets in it still
// resolve by name. Hand the returned handle back to RenderPrepared to render it.
func (e *Environment) CompileString(ctx context.Context, name, body string) (*Template, error) {
	tmpl, err := e.compileString(ctx, name, body)
	if err != nil {
		return nil, err
	}
	return newTemplate(tmpl), nil
}

// compileString is the unexported compile path returning the interpreter's
// internal template. CompileString wraps its result for the public boundary; the
// engineAdapter (backing template_from_string) uses it directly.
func (e *Environment) compileString(ctx context.Context, name, body string) (*interp.Template, error) {
	mod, err := parse.Parse(source.New(name, body))
	if err != nil {
		return nil, err
	}
	if err := check.Check(mod, e.typeRegistry); err != nil {
		return nil, err
	}
	return interp.PrepareChecked(name, mod)
}

// compiledFor returns the installed manifest to serve name's by-name render,
// or nil when the render must take the interpreter path. The gate proves, per
// render, that the compiled unit's bytes are the interpreter's bytes: the
// compile-options fingerprint must equal this Environment's configuration
// (each of those knobs is burned into the generated code), no render-shaping
// feature the generated code cannot honor may be configured (a sandbox policy
// or activation, a coverage collector, a host type registry), a unit
// containing @log must not swallow lines a real logger would receive, and
// every member source must byte-equal the module the render would walk.
// Anything unprovable falls back: dispatch is an optimization, never a
// semantics change. It runs after LoadTemplate, so the entry module is warm.
func (e *Environment) compiledFor(name string) *compiled.Manifest {
	u, ok := e.compiledUnits[name]
	if !ok {
		return nil
	}
	m := u.manifest
	fp := m.Fingerprint()
	if fp.AutoescapeHTML() != e.autoescapeHTML ||
		fp.LenientVariables() != !e.strictVariables ||
		fp.TabWidth() != e.tabWidth ||
		fp.RandomSeed() != e.randomSeed ||
		fp.RandomSeedSet() != e.randomSeedSet {
		return nil
	}
	if e.policy != nil || e.sandboxActive || e.coverage != nil || e.typeRegistry != nil {
		return nil
	}
	if m.UsesLog() && e.logger.Writer() != io.Discard {
		return nil
	}
	if !e.unitCoherent(u) {
		return nil
	}
	// An ignore-missing @include the unit inlined as rendering nothing is
	// byte-exact only while its target still fails to resolve: the moment the
	// loader serves it, the interpreter would inline the partial, so dispatch
	// must fall back. This is the runtime template-exists check the compiled
	// render function cannot make from its stateless signature.
	for _, name := range m.AbsentIncludes() {
		if e.templateExists(name) {
			return nil
		}
	}
	return m
}

// unitCoherent reports whether every member source in the unit's manifest
// byte-equals the source text of the module the parse cache currently serves
// for that member, loading and caching any member not yet parsed. The verdict
// is memoized against the member modules' pointers: a warm dispatch pays one
// cache hit and one pointer compare per member, and any member whose module
// pointer changed (a re-parse after eviction) forces the byte-comparison to
// run again, so a source change can never be served through a stale verdict.
// A member that fails to load yields false without memoizing, letting the
// interpreter path surface the load error exactly as an uninstalled unit
// would.
func (e *Environment) unitCoherent(u *compiledUnit) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	recheck := false
	if u.witness == nil {
		u.witness = make([]*ast.Node, len(u.members))
		recheck = true
	}
	for i, name := range u.members {
		mod, err := e.loadModule(name)
		if err != nil {
			u.witness = nil
			return false
		}
		if u.witness[i] != mod {
			u.witness[i] = mod
			recheck = true
		}
	}
	if !recheck {
		return u.ok
	}
	u.ok = true
	for i, name := range u.members {
		if u.witness[i].Src.Code() != u.manifest.Sources()[name] {
			u.ok = false
			break
		}
	}
	return u.ok
}

// renderShadowed runs one by-name render in shadow-verification mode: the
// interpreter render is authoritative and always served, the compiled render
// runs against the same variables into a private buffer, and any difference
// in output bytes or error text is reported to the WithCompiledVerify
// callback. Running both engines over one vars map is safe under the value
// contract: binding marks argument arrays shared and every template mutation
// privatizes first (copy-on-write), so the first render cannot change what
// the second reads.
func (e *Environment) renderShadowed(ctx context.Context, m *compiled.Manifest, tmpl *interp.Template, vars map[string]runtime.Value) (string, error) {
	interpOut, interpErr := interp.Render(ctx, engineAdapter{e}, tmpl, vars)
	var b strings.Builder
	compErr := m.Render()(ctx, &b, e.extensions, vars, e.renderCache)
	if b.String() != interpOut || !sameErrorText(compErr, interpErr) {
		e.compiledVerify(compiled.Divergence{
			Template:       m.Entry(),
			CompiledOutput: b.String(),
			InterpOutput:   interpOut,
			CompiledErr:    compErr,
			InterpErr:      interpErr,
		})
	}
	return interpOut, interpErr
}

// sameErrorText reports whether two render errors agree for shadow
// verification: both nil, or both non-nil with identical text. Error text is
// the compiled backend's parity contract (typed identity does not survive the
// generated-code boundary), so text equality is the right notion of "same".
func sameErrorText(a, b error) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || a.Error() == b.Error()
}

// Render loads the named template and renders it with vars, returning the
// output. When an installed compiled unit (WithCompiled) passes the dispatch
// gate the render runs through the generated function with identical output
// and error bytes, including the partial output an errored render returns.
func (e *Environment) Render(ctx context.Context, name string, vars map[string]runtime.Value) (string, error) {
	tmpl, err := e.loadTemplate(ctx, name)
	if err != nil {
		return "", err
	}
	if m := e.compiledFor(name); m != nil {
		if e.compiledVerify != nil {
			return e.renderShadowed(ctx, m, tmpl, vars)
		}
		// The compiled path shares the interpreter's warm-render Builder
		// sizing: pre-grow from the Template's remembered output length and
		// store the length back on success, so alternating compiled and
		// interpreted renders keep one coherent hint. Capacity never affects
		// rendered bytes.
		var b strings.Builder
		if hint := tmpl.OutGrowHint(); hint > 0 {
			b.Grow(hint)
		}
		err := m.Render()(ctx, &b, e.extensions, vars, e.renderCache)
		if err == nil {
			tmpl.RecordOutSize(b.Len())
		}
		return b.String(), err
	}
	return interp.Render(ctx, engineAdapter{e}, tmpl, vars)
}

// RenderPrepared renders an already-loaded template handle (from LoadTemplate or
// CompileString) with vars and returns the output, skipping the load step. It is
// the render half of the load/render split: a host that loads once and renders
// many times, or that needs to time rendering alone, holds the opaque handle and
// feeds it here. The output is byte-identical to Render of the same template with
// the same vars; unlike Render it does not consult installed compiled units (the
// handle is rendered by the interpreter), so it is the pure interpreter render of
// exactly the passed template. A nil handle renders nothing.
func (e *Environment) RenderPrepared(ctx context.Context, tmpl *Template, vars map[string]runtime.Value) (string, error) {
	if tmpl == nil {
		return "", nil
	}
	return interp.Render(ctx, engineAdapter{e}, tmpl.internal(), vars)
}

// RenderString parses an ad-hoc template body (not added to the loader) and
// renders it. Inheritance/include/import targets in the body still resolve
// through the loader by name.
func (e *Environment) RenderString(ctx context.Context, name, body string, vars map[string]runtime.Value) (string, error) {
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
	return interp.Render(ctx, engineAdapter{e}, tmpl, vars)
}

// RenderTo loads the named template and renders it directly to w. When the
// template closure uses no deferred-slot construct (@yield, @provide, slot()),
// the output streams to w incrementally with bounded memory; otherwise the
// full output is buffered, its slot placeholders are resolved, and the result
// is written to w in one call (nothing is written on a render error). The
// bytes written equal Render's returned string in both cases. RenderTo neither
// wraps nor flushes w; pass a bufio.Writer for buffered throughput and flush
// it after RenderTo returns (a @flush statement flushes such a writer
// mid-render). An installed compiled unit (WithCompiled) that passes the
// dispatch gate renders through the generated function: a slot-free unit
// streams to w exactly as the interpreter's slot-free path does, while a slots
// unit (Manifest.UsesSlots) buffers into a scratch builder that reaches w only
// on success, matching the interpreter's buffered-slots path which writes
// nothing on error, so a mid-render error never leaks an unresolved
// placeholder to the caller's writer. Under WithCompiledVerify the dispatched
// render buffers both engines' outputs to compare them, then writes the
// interpreter's bytes: for a slot-free unit that includes an errored render's
// partial output (matching the streaming path), and for a slots unit it writes
// nothing on error (matching the buffered path).
func (e *Environment) RenderTo(ctx context.Context, w io.Writer, name string, vars map[string]runtime.Value) error {
	tmpl, err := e.loadTemplate(ctx, name)
	if err != nil {
		return err
	}
	if m := e.compiledFor(name); m != nil {
		if e.compiledVerify != nil {
			// A slot-free unit's interpreter result reaches w even when the
			// render errors: both the interpreter path and direct dispatch stream
			// partial output before a mid-render error, and verification must
			// leave those same bytes on w. A slots unit instead buffers and
			// writes nothing on error, so its placeholder-bearing partial is
			// withheld. The render error outranks a write error because it is the
			// authoritative result the comparison above already served.
			out, rerr := e.renderShadowed(ctx, m, tmpl, vars)
			if rerr != nil && m.UsesSlots() {
				return rerr
			}
			_, werr := io.WriteString(w, out)
			if rerr != nil {
				return rerr
			}
			return werr
		}
		if m.UsesSlots() {
			// A slots unit's generated render writes its partial, unresolved
			// buffer to the writer on error; buffering into a scratch builder and
			// writing it only on success keeps that placeholder-bearing partial
			// off the caller's writer, mirroring the interpreter's buffered-slots
			// branch which writes nothing when the render fails.
			var b strings.Builder
			if rerr := m.Render()(ctx, &b, e.extensions, vars, e.renderCache); rerr != nil {
				return rerr
			}
			_, werr := io.WriteString(w, b.String())
			return werr
		}
		return m.Render()(ctx, w, e.extensions, vars, e.renderCache)
	}
	return interp.RenderTo(ctx, engineAdapter{e}, tmpl, vars, w)
}

// RenderToValues renders the named template directly to w like RenderTo, but
// from native Go bindings: each value in vars is marshaled through
// runtime.FromGo exactly as RenderValues does.
func (e *Environment) RenderToValues(ctx context.Context, w io.Writer, name string, vars map[string]any) error {
	rv, err := fromGoVars(vars)
	if err != nil {
		return err
	}
	return e.RenderTo(ctx, w, name, rv)
}

// RenderStringTo parses an ad-hoc template body (not added to the loader) and
// renders it directly to w with RenderTo's streaming-vs-buffered behavior.
// Inheritance/include/import targets in the body still resolve through the
// loader by name.
func (e *Environment) RenderStringTo(ctx context.Context, w io.Writer, name, body string, vars map[string]runtime.Value) error {
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
	return interp.RenderTo(ctx, engineAdapter{e}, tmpl, vars, w)
}

// RenderValues renders the named template from native Go bindings: each value in
// vars is marshaled through runtime.FromGo, so a host can pass ordinary Go
// scalars, slices, maps, structs, and nested combinations without hand-building
// runtime.Value bindings. A value that is already a runtime.Value passes
// through unchanged, so hand-built and native bindings mix freely. An
// unsupported Go kind (a channel, a bare function, a complex number) returns the
// typed marshaling error and renders nothing.
func (e *Environment) RenderValues(ctx context.Context, name string, vars map[string]any) (string, error) {
	rv, err := fromGoVars(vars)
	if err != nil {
		return "", err
	}
	return e.Render(ctx, name, rv)
}

// RenderStringValues parses an ad-hoc template body (not added to the loader)
// and renders it from native Go bindings, marshaling each value through
// runtime.FromGo exactly as RenderValues does. Inheritance/include/import
// targets in the body still resolve through the loader by name.
func (e *Environment) RenderStringValues(ctx context.Context, name, body string, vars map[string]any) (string, error) {
	rv, err := fromGoVars(vars)
	if err != nil {
		return "", err
	}
	return e.RenderString(ctx, name, body, rv)
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
