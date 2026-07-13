package quill

import "github.com/avmnu-sng/quill-template-engine/internal/interp"

// Template is the opaque, read-only handle a host receives from LoadTemplate and
// CompileString. It wraps the interpreter's prepared template (an internal type)
// and exposes only a curated inspection surface (the template's name and its
// block and macro membership) so the render machinery, the AST, and every
// other interpreter internal stay off the public API. A Template is immutable
// once returned and safe to share across concurrent renders; hand it back to the
// Environment (RenderPrepared) to render it without re-loading.
type Template struct {
	// tmpl is the wrapped internal prepared template. It is never nil for a
	// Template a public entry point returns; the unexported accessors below and
	// the render machinery reach through it.
	tmpl *interp.Template
}

// newTemplate wraps an internal prepared template for return across the public
// boundary. A nil internal template yields a nil *Template so a load error's
// (nil, err) return stays a nil handle rather than a non-nil wrapper around nil.
func newTemplate(t *interp.Template) *Template {
	if t == nil {
		return nil
	}
	return &Template{tmpl: t}
}

// internal returns the wrapped prepared template for the render machinery. It is
// the single unwrap point the Environment uses to render a host-held Template
// without exposing the interpreter type.
func (t *Template) internal() *interp.Template { return t.tmpl }

// Name reports the template's name (its loader key, or the ad-hoc name passed to
// CompileString).
func (t *Template) Name() string { return t.tmpl.Name }

// BlockNames lists this template's own block names in declaration order (nested
// blocks flattened, per the composition model). It does not include blocks
// inherited from a parent that this template does not itself define.
func (t *Template) BlockNames() []string { return t.tmpl.BlockNames() }

// HasBlock reports whether this template defines a block with the given name.
func (t *Template) HasBlock(name string) bool { return t.tmpl.HasBlock(name) }

// HasMacro reports whether this template defines a macro with the given name.
func (t *Template) HasMacro(name string) bool { return t.tmpl.HasMacro(name) }
