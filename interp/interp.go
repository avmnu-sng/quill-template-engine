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

	"github.com/avmnusng/quill-template-engine/ast"
	"github.com/avmnusng/quill-template-engine/errors"
	"github.com/avmnusng/quill-template-engine/ext"
	"github.com/avmnusng/quill-template-engine/runtime"
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

	// escape is the active output strategy: "" / "off" means verbatim, "html"
	// means the html escaper (spec 04 Section 8). It is saved/restored across an
	// @escape region.
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
		eng:     eng,
		out:     out,
		root:    root,
		blocks:  map[string]*blockEntry{},
		macros:  map[string]*macroEntry{},
		escape:  autoesc,
		regexps: map[*ast.Node]*regexp.Regexp{},
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
	text, err := runtime.ToText(v)
	if err != nil {
		return err
	}
	if in.escape == "html" && v.Kind != runtime.KSafe {
		text = ext.EscapeHTML(text)
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
