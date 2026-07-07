// Package compiled defines the manifest contract between generated render
// functions (the compile backend's output) and the Environment's compiled
// dispatch (quill.WithCompiled). A generated unit exports a Manifest naming
// its entry template, embedding every member template's source text, carrying
// a Fingerprint of the compile options that shape rendered bytes, and exposing
// the render entry point. The Environment serves a by-name render through the
// manifest's function only when the fingerprint matches its own configuration
// and every member source byte-equals the text its loader currently serves;
// anything unprovable falls back to the tree-walking interpreter, so installing
// a manifest can change render speed but never rendered bytes.
//
// The package is a leaf: it imports only the standard library and the two
// packages a generated render function already depends on (ext and runtime),
// so generated code and the facade can both reference it without cycles.
package compiled

import (
	"io"

	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// RenderCache is the render-time memoization handle a generated render reads to
// back an @cache region: the Environment's rendered-body store, narrowed to the
// two operations a compiled region performs. A hit replays a stored body verbatim
// and skips re-rendering; a miss stores the freshly rendered body under its key.
// The handle is passed per render rather than baked into the unit because the
// store is Environment state a host warms across renders, and the interpreter
// path shares the very same store, so a region compiled once stays coherent with
// interpreted renders of neighboring templates. A nil handle means no store is
// configured, in which case every region is a miss and nothing is memoized,
// matching the interpreter when its engine exposes no cache. The engine-default
// *cache.RenderCache satisfies this interface directly; a host may supply any
// implementation with the same two methods, and the narrow surface keeps
// eviction and tag-invalidation controls off the generated-code boundary.
type RenderCache interface {
	// Get returns the stored body for key and whether one is present, so a
	// compiled @cache region replays a hit and re-renders a miss.
	Get(key string) (body string, ok bool)
	// Put stores a rendered body under key, recording its tags for a later
	// host-driven tag invalidation, exactly as the interpreter's @cache does.
	Put(key string, body string, tags []string)
}

// RenderFunc is the signature of a generated render function: it writes the
// template's output to w, resolves callables through exts, reads top-level
// variables from vars, and memoizes @cache regions through rc (nil when the
// engine exposes no store), returning the first render error.
type RenderFunc func(w io.Writer, exts *ext.ExtensionSet, vars map[string]runtime.Value, rc RenderCache) error

// Fingerprint captures the compile options that shape a generated unit's
// rendered bytes. The Environment dispatches to a compiled unit only when the
// fingerprint equals its own configuration field for field, because each of
// these knobs is burned into the generated code at compile time: the escape
// strategy and undefined-handling are lowered statically, and the tab width
// and random seed ride the generated engine handle.
type Fingerprint struct {
	// AutoescapeHTML records the compiled output strategy: html when true, off
	// when false (the engine's WithAutoescapeHTML option).
	AutoescapeHTML bool
	// LenientVariables records the compiled undefined-handling mode; true is
	// the engine's WithStrictVariables(false) migration mode.
	LenientVariables bool
	// TabWidth records the spaces-per-indent-level width burned into the
	// unit's engine handle (the engine's WithTabWidth option, default 4).
	TabWidth int
	// RandomSeed records the fixed seed of the randomness callables; it is
	// meaningful only when RandomSeedSet is true.
	RandomSeed int64
	// RandomSeedSet distinguishes a deliberate seed of zero from the unseeded
	// engine default. Two unseeded sides fingerprint-match even though a
	// template that actually draws randomness then compares distributionally,
	// never byte-wise -- the same caveat the compile backend documents.
	RandomSeedSet bool
}

// Manifest describes one compiled unit to the Environment's dispatch. A
// generated file exports one Manifest value; a host installs it with
// quill.WithCompiled. Every field is written once at generation time and read
// concurrently afterwards, so a Manifest must not be mutated after install.
type Manifest struct {
	// Entry is the template name the unit renders; a by-name render of this
	// name is eligible for compiled dispatch.
	Entry string
	// Sources maps every member template name to the full source text the
	// unit was compiled from (the text already embedded in the generated file
	// for error positions). Dispatch byte-compares each member against the
	// text the Environment's loader currently serves, so a compiled unit can
	// never render for a template whose source has changed since generation.
	Sources map[string]string
	// Fingerprint records the compile options the unit's bytes depend on.
	Fingerprint Fingerprint
	// UsesLog marks a unit containing an @log statement. A compiled render
	// evaluates @log's expression for effect and error parity but has no
	// logger sink, so dispatch falls back to the interpreter whenever the
	// Environment carries a non-discarding logger, preserving the host-visible
	// log side effects.
	UsesLog bool
	// UsesSlots marks a unit whose render buffers its output internally and
	// resolves deferred-slot placeholders (@yield/@provide/slot()) before
	// returning, so a mid-render error leaves an unresolved placeholder in the
	// partial buffer the generated function writes to w. Streaming dispatch
	// (Environment.RenderTo) must therefore route such a unit through a scratch
	// buffer it discards on error, matching the interpreter's buffered-slots
	// path, which writes nothing to w when the render fails; a raw placeholder
	// must never reach the caller's writer.
	UsesSlots bool
	// AbsentIncludes lists the templates an ignore-missing @include in this unit
	// inlined as rendering nothing because they were absent when the unit was
	// compiled. The generated code splices no output for them, which is
	// byte-exact only while each stays unresolvable; dispatch therefore serves
	// the unit only when every listed name still fails to load, and falls back
	// to the interpreter (which would inline a now present partial) the moment
	// one appears. It parallels Sources, which proves the listed members
	// present, by proving these names absent.
	AbsentIncludes []string
	// Render is the generated render entry point.
	Render RenderFunc
}

// Divergence reports one shadow-verification mismatch: a render whose compiled
// output or error text differs from the interpreter's for the same template
// and variables. The Environment's verify mode (quill.WithCompiledVerify)
// always serves the interpreter's result; the divergence is surfaced to the
// host callback so a deployment can measure trust in a unit before switching
// it to direct compiled dispatch.
type Divergence struct {
	// Template is the entry template name of the diverging unit.
	Template string
	// CompiledOutput is the compiled render's full output, including any
	// partial output written before an error.
	CompiledOutput string
	// InterpOutput is the interpreter's full output for the same render,
	// including any partial output written before an error.
	InterpOutput string
	// CompiledErr is the compiled render's error, or nil.
	CompiledErr error
	// InterpErr is the interpreter's error for the same render, or nil.
	InterpErr error
}
