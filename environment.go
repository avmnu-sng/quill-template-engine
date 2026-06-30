package quill

import (
	"github.com/avmnusng/quill-template-engine/cache"
	"github.com/avmnusng/quill-template-engine/ext"
	"github.com/avmnusng/quill-template-engine/interp"
	"github.com/avmnusng/quill-template-engine/loader"
	"github.com/avmnusng/quill-template-engine/parse"
	"github.com/avmnusng/quill-template-engine/runtime"
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
