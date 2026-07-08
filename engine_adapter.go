package quill

import (
	"context"
	"log"

	"github.com/avmnu-sng/quill-template-engine/internal/interp"
	"github.com/avmnu-sng/quill-template-engine/pkg/cache"
	"github.com/avmnu-sng/quill-template-engine/pkg/cover"
	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/sandbox"
)

// engineAdapter is the interpreter's view of an Environment. It carries the
// renderer-internal configuration getters the interp.Engine contract requires
// (the callable registry, the strictness and autoescape options, the sandbox
// policy, coverage, and the load/compile hooks) so those getters are NOT part of
// the Environment's public surface. Every interp entry point the Environment
// calls is handed an engineAdapter{e} instead of the bare *Environment, and the
// same adapter satisfies the Engine interface the needs_environment callables
// (include, source, template_from_string) consume through EngineFromValue. The
// adapter is a thin, allocation-cheap value view over one Environment; it holds
// no state of its own.
type engineAdapter struct{ e *Environment }

// compile-time assertion that the adapter satisfies the interpreter's contract.
var _ interp.Engine = engineAdapter{}

// Extensions returns the callable registry (core plus host).
func (a engineAdapter) Extensions() *ext.Set { return a.e.extensions }

// StrictVariables reports the undefined-handling policy: an undefined read is an
// error (the default) or a silent Null (lenient migration mode).
func (a engineAdapter) StrictVariables() bool { return a.e.strictVariables }

// AutoescapeHTML reports whether the default output strategy is html rather than
// off (the default is off).
func (a engineAdapter) AutoescapeHTML() bool { return a.e.autoescapeHTML }

// RandomSeed returns the configured RNG seed and whether one was set. When set,
// the seedable randomness functions (random, shuffle) become deterministic.
func (a engineAdapter) RandomSeed() (int64, bool) { return a.e.randomSeed, a.e.randomSeedSet }

// RenderCache returns the engine's rendered-body cache, backing @cache.
func (a engineAdapter) RenderCache() *cache.RenderCache { return a.e.renderCache }

// Policy returns the host-supplied sandbox security policy, or nil.
func (a engineAdapter) Policy() *sandbox.Policy { return a.e.policy }

// SandboxActive reports the global sandbox activation gate.
func (a engineAdapter) SandboxActive() bool { return a.e.sandboxActive }

// Coverage returns the host-attached coverage Collector, or nil when coverage is
// off. The interpreter copies it into each render's cov field.
func (a engineAdapter) Coverage() *cover.Collector { return a.e.coverage }

// TabWidth returns the spaces-per-indent-level width backing the tab filter, the
// tab/space/break functions, and the @tab region.
func (a engineAdapter) TabWidth() int { return a.e.tabWidth }

// Logger returns the sink @log writes to. It is never nil; without WithLogger it
// discards.
func (a engineAdapter) Logger() *log.Logger { return a.e.logger }

// LoadTemplate parses and prepares the named template, returning the underlying
// internal Template the interpreter renders. It is the unexported load path
// wrapped for the Engine contract; the public LoadTemplate wraps the same result
// in an opaque *quill.Template.
func (a engineAdapter) LoadTemplate(ctx context.Context, name string) (*interp.Template, error) {
	return a.e.loadTemplate(ctx, name)
}

// TemplateExists reports whether the named template can be loaded.
func (a engineAdapter) TemplateExists(name string) bool { return a.e.templateExists(name) }

// RawSource returns the unparsed source text of the named template, backing the
// source() function. ok is false on a miss.
func (a engineAdapter) RawSource(name string) (string, bool) { return a.e.rawSource(name) }

// CompileString parses and prepares an ad-hoc template body under the given name,
// backing template_from_string. The body is not added to the loader.
func (a engineAdapter) CompileString(ctx context.Context, name, body string) (*interp.Template, error) {
	return a.e.compileString(ctx, name, body)
}
