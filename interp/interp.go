// Package interp is Quill's tree-walking renderer. It evaluates expressions over
// the runtime ops (package runtime), drives the control-flow and composition
// statements, and pushes bytes to an output sink. It implements the render half
// of spec 01 Sections 4-5, spec 04 Sections 4-8, and the stdlib calling surface
// of spec 03, against the AST that package parse produces.
//
// The interpreter is deliberately a tree walker, not a compiler: the AST is
// small and uniform (one Node kind per construct), so a single switch on Kind
// per evaluation step is clear and fast enough for a source generator's
// workload. Correctness lives in package runtime (one equality, one ordering,
// one truthiness, one coercion); this package only sequences those operations
// and manages scope, output, and the Template composition contract.
//
// The engine handle (the Engine interface) lets the interpreter load other
// templates (for @extends/@include/@import) and consult the callable registry
// without importing the top-level facade, which would be a cycle. The facade in
// package quill implements Engine.
package interp

import (
	"regexp"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/cache"
	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/ext"
	"github.com/avmnu-sng/quill-template-engine/runtime"
	"github.com/avmnu-sng/quill-template-engine/sandbox"
)

// Engine is the interpreter's view of the surrounding environment. The quill
// facade implements it. It supplies the callable registry, the strictness and
// autoescape options, and the means to load and prepare another template by name
// (used by inheritance, includes, and imports).
type Engine interface {
	// Extensions returns the callable registry (core plus host).
	Extensions() *ext.ExtensionSet
	// StrictVariables reports whether an undefined read is an error (the default)
	// or a silent Null (lenient migration mode), spec 04 Section 6.
	StrictVariables() bool
	// AutoescapeHTML reports whether the default output strategy is html rather
	// than off; the default is off (spec 04 Section 8.1).
	AutoescapeHTML() bool
	// LoadTemplate parses and prepares the named template, returning its Template
	// (with block and macro tables built). Misses are loader not-found errors.
	LoadTemplate(name string) (*Template, error)
	// TemplateExists reports whether the named template can be loaded, for
	// candidate lists and ignore-missing.
	TemplateExists(name string) bool
	// RawSource returns the unparsed source text of the named template, backing
	// the source() function (spec 03 Section 3.2). ok is false on a miss.
	RawSource(name string) (string, bool)
	// CompileString parses and prepares an ad-hoc template body under the given
	// name, backing template_from_string (spec 03 Section 3.3). The body is not
	// added to the loader.
	CompileString(name, body string) (*Template, error)
	// RandomSeed returns the host-configured RNG seed and whether one was set.
	// When set, the seedable randomness functions (random, shuffle) become
	// deterministic, backing test reproducibility (spec 03 Section 3.2, X15).
	RandomSeed() (int64, bool)
	// RenderCache returns the engine's rendered-body cache, backing the @cache
	// region statement (spec 01 Section 4.7). It is the pluggable cache surface;
	// the engine default is an in-memory store.
	RenderCache() *cache.RenderCache
	// Policy returns the host-supplied sandbox security policy, or nil when none
	// was configured (spec 04 Section 8.3). It is consulted only when the sandbox
	// is active (a global toggle, the @sandbox region, or a sandboxed include).
	Policy() *sandbox.Policy
	// SandboxActive reports whether the sandbox is globally on for every render
	// (the always-on activation path, design/escaping-safety Section 6.2). The
	// @sandbox region and sandboxed includes turn it on locally regardless.
	SandboxActive() bool
}

// Sink is the push-based output target. The interpreter writes rendered bytes as
// it walks; a strings.Builder is the usual sink (see Render).
type Sink interface {
	WriteString(s string) (int, error)
}

// Render renders tmpl with the given top-level variables and returns the output
// string. It is the entry the facade calls; it resolves the inheritance chain,
// builds the merged block table, and walks the root (or the topmost parent).
func Render(eng Engine, tmpl *Template, vars map[string]runtime.Value) (string, error) {
	var b strings.Builder
	in := newInterp(eng, tmpl, &b)
	ctx := runtime.NewContext()
	for k, v := range vars {
		ctx.Set(k, v)
	}
	if err := in.renderTemplate(tmpl, ctx); err != nil {
		return b.String(), err
	}
	return b.String(), nil
}

// interp holds one render's mutable state: the engine, the output sink, the
// active escaping strategy, and the inheritance/macro resolution roots. A nested
// render (an include) gets its own interp with a fresh sink, then splices the
// captured output back as a value.
type interp struct {
	eng  Engine
	out  Sink
	root *Template // the template that started this render (for _self, macros)

	// blocks is the merged block table for the current inheritance chain: a block
	// name resolves to the most-derived definition. parentChain lists templates
	// from most-derived to least, so parent() can find the next-up definition.
	blocks      map[string]*blockEntry
	parentChain []*Template

	// escape is the active output strategy: "" means verbatim (off), otherwise
	// one of the six named strategies (html, js, css, html_attr,
	// html_attr_relaxed, url) shared with the escape()/e() filter (spec 04
	// Section 8). It is saved/restored across an @escape region, and that
	// save/restore is the strategy STACK that composes nested regions with the
	// module default.
	escape string

	// macros holds the macro namespace visible to the current render: the root
	// template's own macros plus any imported under a namespace or selectively.
	macros map[string]*macroEntry

	// curBlock / curBlockDepth track the block being rendered so parent() inside
	// an overriding block can render the next definition up its chain (spec 01
	// Section 5.2).
	curBlock      *blockEntry
	curBlockDepth int

	// regexps merges the literal-`matches` regexp caches of every template that
	// enters this render (the root, its inheritance parents, and macro homes), so
	// matches() reuses one compile per literal pattern instead of recompiling each
	// evaluation. Seeded from each template's Prepare-built table via absorb.
	regexps map[*ast.Node]*regexp.Regexp

	// sandboxOn is the active sandbox gate for this render (spec 04 Section 8.3,
	// design/escaping-safety Section 6.2). It starts from the engine's global
	// SandboxActive flag and is forced on -- and restored afterward, never off for
	// an already-sandboxed enclosing render (B16) -- by an @sandbox region and by a
	// sandboxed include. When on, the Phase-1 per-render callable check runs and
	// the runtime member-access / string-coercion gates enforce the policy.
	sandboxOn bool
}

// blockEntry is one resolved block: the template that owns the definition and
// the block node. The owner is needed so parent() can walk to the next
// definition up the chain.
type blockEntry struct {
	owner *Template
	node  *ast.Node
	// chain is the ordered list of definitions for this name, most-derived first,
	// so parent() inside the i-th renders the (i+1)-th.
	chain []blockDef
}

type blockDef struct {
	owner *Template
	node  *ast.Node
}

// macroEntry binds a macro name to its definition and the template it was
// declared in (its lexical home, which provides its own macro namespace and
// globals), spec 01 Section 5.3.
type macroEntry struct {
	home *Template
	node *ast.Node
}

func newInterp(eng Engine, root *Template, out Sink) *interp {
	autoesc := ""
	if eng.AutoescapeHTML() {
		autoesc = "html"
	}
	in := &interp{
		eng:       eng,
		out:       out,
		root:      root,
		blocks:    map[string]*blockEntry{},
		macros:    map[string]*macroEntry{},
		escape:    autoesc,
		regexps:   map[*ast.Node]*regexp.Regexp{},
		sandboxOn: eng.SandboxActive(),
	}
	in.absorb(root)
	return in
}

// absorb merges a template's literal-`matches` regexp cache into this render's
// lookup, so matches() can find the Prepare-compiled regexp for any literal
// pattern node reachable in the render. It is called as each template enters the
// render (root at construction, parents in buildChain, macro homes in
// loadMacros). Nodes absent from the lookup (dynamic patterns, or a template not
// yet absorbed) fall back to a runtime compile.
func (in *interp) absorb(t *Template) {
	if t == nil {
		return
	}
	for n, re := range t.regexps {
		in.regexps[n] = re
	}
}

// emit writes a rendered value to the sink, applying ToText and the active escape
// strategy. A Safe value is never escaped (it is already-safe content); raw text
// under the off strategy is byte-exact (spec 04 Sections 5, 8).
func (in *interp) emit(v runtime.Value) error {
	// Sandbox Phase-2 string-coercion gate (B12): coercing a host Object to text
	// requires its stringify member be permitted by the policy. The gate runs
	// before ToText so a disallowed object never reaches its Stringify hook. A
	// Safe value, a non-object, and a trusted shim are not gated (B14).
	if err := in.checkStringifyAllowed(v); err != nil {
		return err
	}
	text, err := runtime.ToText(v)
	if err != nil {
		return err
	}
	// A Safe value carries already-escaped content and is emitted verbatim under
	// any active strategy. Otherwise, when a strategy is active, the value's text
	// flows through the shared escaper for that strategy (spec 04 Section 8). The
	// code-point strategies (js, css, html_attr, html_attr_relaxed) decode the
	// text as UTF-8 and return a charset error naming the strategy and byte offset
	// on an invalid byte (spec 04 Section 8.2); that error is surfaced here rather
	// than emitting a silent replacement character.
	if in.escape != "" && v.Kind != runtime.KSafe {
		text, err = ext.Escape(in.escape, text)
		if err != nil {
			return err
		}
	}
	_, err = in.out.WriteString(text)
	return err
}

// emitString writes literal template text verbatim. Template TEXT is never
// escaped: it is author-controlled output, not a value (spec 04 Section 8.1).
func (in *interp) emitString(s string) error {
	_, err := in.out.WriteString(s)
	return err
}

// posErr attaches the node's source position to an error that lacks one, so a
// runtime failure names template:line (spec 01 Section 1.8). An error that
// already carries a position is left unchanged.
func posErr(n *ast.Node, err error) error {
	if err == nil {
		return nil
	}
	// A typed *Security error must keep its concrete type so a host can catch it
	// with errors.As and branch on Class; attach position via its own At, which
	// preserves the wrapper, rather than collapsing it into a generic *Error.
	var sec *errors.Security
	if asSecurity(err, &sec) {
		if sec.Src() == nil && sec.Line() == 0 && n != nil {
			return sec.At(n.Src, n.Line)
		}
		return sec
	}
	var qe *errors.Error
	if as(err, &qe) {
		if qe.Src == nil && qe.Line == 0 && n != nil {
			return qe.At(n.Src, n.Line)
		}
		return qe
	}
	if n != nil {
		return errors.Wrap(errors.KindRuntime, err, "%s", err.Error()).At(n.Src, n.Line)
	}
	return err
}

// as is a local errors.As over *errors.Error to avoid importing stdlib errors.
func as(err error, target **errors.Error) bool {
	for err != nil {
		if e, ok := err.(*errors.Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// asSecurity is the local errors.As over *errors.Security: a sandbox violation
// carries a distinct concrete type the host catches, so posErr must recognize
// it before the generic *Error path and keep the wrapper intact.
func asSecurity(err error, target **errors.Security) bool {
	for err != nil {
		if e, ok := err.(*errors.Security); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
