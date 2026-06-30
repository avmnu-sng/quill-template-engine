package quill

import (
	"github.com/avmnusng/quill-template-engine/cache"
	"github.com/avmnusng/quill-template-engine/check"
	"github.com/avmnusng/quill-template-engine/ext"
	"github.com/avmnusng/quill-template-engine/interp"
	"github.com/avmnusng/quill-template-engine/loader"
	"github.com/avmnusng/quill-template-engine/parse"
	"github.com/avmnusng/quill-template-engine/runtime"
	"github.com/avmnusng/quill-template-engine/sandbox"
	"github.com/avmnusng/quill-template-engine/source"
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
}

// Option configures an Environment at construction.
type Option func(*Environment)

// WithAutoescapeHTML turns the default output strategy to html (off by default).
func WithAutoescapeHTML(on bool) Option {
	return func(e *Environment) { e.autoescapeHTML = on }
}

// WithStrictVariables sets strict-undefined handling (on by default). Setting it
// false enables the lenient migration mode: an undefined read becomes Null and a
// for over a non-iterable becomes an empty loop (spec 04 Section 6).
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

// WithExtensions replaces the callable registry (e.g. to layer host callables
// over the core set). The provided set is used as-is.
func WithExtensions(set *ext.ExtensionSet) Option {
	return func(e *Environment) { e.extensions = set }
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

// New builds an Environment over a Loader with the given options. The core
// stdlib subset is installed, then the engine-bound include/block-family
// callables, then any host extensions supplied via WithExtensions take
// precedence (host callables shadow core, spec 03 Section 1).
func New(ldr loader.Loader, opts ...Option) *Environment {
	e := &Environment{
		loader:          ldr,
		cache:           cache.New(),
		renderCache:     cache.NewRenderCache(),
		extensions:      ext.Core(),
		autoescapeHTML:  false,
		strictVariables: true,
	}
	for _, opt := range opts {
		opt(e)
	}
	e.registerEngineCallables()
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

// Display renders the named template directly into a Sink (the push model),
// avoiding an intermediate full-output string when the caller streams.
func (e *Environment) Display(name string, vars map[string]runtime.Value) (string, error) {
	return e.Render(name, vars)
}
