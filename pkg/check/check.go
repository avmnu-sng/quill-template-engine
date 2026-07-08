package check

import (
	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
)

// Check runs the gradual type checker over a parsed module and returns the first
// type error, or nil when the template is well-typed (or carries no annotations,
// in which case every binding is `any` and the checker is silent). It is the
// package entry point the engine calls at Load/Prepare time, BEFORE any render,
// so an ill-typed template is rejected before a byte is emitted.
//
// reg is the host static-typing registry (Object<...> member shapes and host
// callable signatures); a nil reg means the host registered no static types, so
// Object<...> is opaque-but-known and host callables are dynamic. The checker
// NEVER mutates the AST -- it reads annotations and reports errors only, so it
// cannot change what the interpreter renders (the binding invariant).
func Check(mod *ast.Node, reg *Registry) error {
	c := &checker{reg: reg, macros: map[string]*Signature{}, blocks: map[string]*Signature{}}
	c.indexCallables(mod)
	return c.checkModule(mod)
}

// checker carries the immutable host registry and the per-template tables of
// macro/block signatures, gathered once so a forward call (a macro used above
// its definition) still type-checks.
type checker struct {
	reg    *Registry
	macros map[string]*Signature
	blocks map[string]*Signature
}

// scope is a lexical type environment: a chain of name->Type frames mirroring
// the runtime's block-structured scoping (spec 04 Section 6). A lookup walks
// outward; a binding shadows an outer one. Narrowing (Section 8) pushes a frame
// that refines a name's type along a proven branch.
type scope struct {
	vars   map[string]*Type
	parent *scope
}

func newScope(parent *scope) *scope {
	return &scope{vars: map[string]*Type{}, parent: parent}
}

// lookup resolves a name's static type and whether it is declared anywhere in
// the chain. An undeclared name is `any` with ok=false -- the dynamic floor: the
// checker makes no claim and the strict runtime is the guard.
func (s *scope) lookup(name string) (*Type, bool) {
	for f := s; f != nil; f = f.parent {
		if t, ok := f.vars[name]; ok {
			return t, true
		}
	}
	return Any, false
}

// set binds name to t in the current frame.
func (s *scope) set(name string, t *Type) { s.vars[name] = t }

// errAt builds a positioned KindTypeCheck error over a node.
func errAt(n *ast.Node, format string, args ...any) error {
	e := errors.New(errors.KindTypeCheck, format, args...)
	if n != nil {
		return e.At(n.Src, n.Line)
	}
	return e
}

// absenceError tags a check diagnostic as belonging to the "absence" class: a
// member miss on a typed Object or an undefined-name read. Only these errors are
// suppressed by the whole-chain absence-suppression tools (?? / default /
// `is defined`, spec 04 Section 6); a genuine type error (a bad arithmetic, a
// non-renderable concat) is NOT absence and must surface even under those tools.
// The wrapped error keeps its message and position via Unwrap so callers that do
// surface it (the strict access path) report it unchanged.
type absenceError struct{ err error }

func (a *absenceError) Error() string { return a.err.Error() }
func (a *absenceError) Unwrap() error { return a.err }

// errAbsent builds a positioned absence-class diagnostic (a member/name miss).
func errAbsent(n *ast.Node, format string, args ...any) error {
	return &absenceError{err: errAt(n, format, args...)}
}

// isAbsence reports whether err is an absence-class miss (the only error class
// the absence-suppression tools swallow).
func isAbsence(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*absenceError)
	return ok
}

// indexCallables records the macro and block signatures the template defines, so
// a call to a forward-declared macro/block is checked against its contract.
func (c *checker) indexCallables(mod *ast.Node) {
	var walk func(n *ast.Node)
	walk = func(n *ast.Node) {
		if n == nil {
			return
		}
		switch n.Kind {
		case ast.KindMacro:
			c.macros[n.Str] = c.macroSignature(n)
		case ast.KindBlock:
			c.blocks[n.Str] = c.blockSignature(n)
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(mod)
}

// macroSignature reads a @macro's declared parameter and return types into a
// Signature (design/type-system.md Section 5.2). An unannotated parameter is
// `any`; a parameter with a default is optional; a trailing variadic absorbs the
// rest. An unannotated return is `any`.
func (c *checker) macroSignature(n *ast.Node) *Signature {
	params := n.Child(0) // KindParams
	sig := &Signature{ret: Any}
	if params != nil {
		c.fillParamSig(sig, params)
	}
	// The return type, when present, is the child after KindParams.
	for i := 1; i < n.NumChildren(); i++ {
		if ch := n.Child(i); ch != nil && ch.Kind == ast.KindType {
			sig.ret = fromAST(ch)
			break
		}
	}
	return sig
}

// blockSignature reads a @block's optional input params and return type.
func (c *checker) blockSignature(n *ast.Node) *Signature {
	sig := &Signature{ret: Any}
	for _, ch := range n.Children {
		switch ch.Kind {
		case ast.KindParams:
			c.fillParamSig(sig, ch)
		case ast.KindType:
			sig.ret = fromAST(ch)
		}
	}
	return sig
}

// fillParamSig translates a KindParams node into a Signature's params/optional/
// variadic fields. A KindParam carries its type as a child (when ParamHasType)
// and a default as a child (when ParamHasDefault). A "**name" kwargs tail is
// bound in the body scope as map<string,any> but occupies no positional slot,
// so it is skipped here (a caller reaches it only through named arguments).
func (c *checker) fillParamSig(sig *Signature, params *ast.Node) {
	trailingOptional := 0
	for _, p := range params.Children {
		if p.Kind != ast.KindParam {
			continue
		}
		if p.Int&ast.ParamKwargs != 0 { // "**name" kwargs tail: no positional slot
			continue
		}
		if p.Bool { // variadic ...rest
			sig.variadic = true
			sig.varElem = c.paramType(p)
			continue
		}
		sig.params = append(sig.params, c.paramType(p))
		if p.Int&ast.ParamHasDefault != 0 {
			trailingOptional++
		} else {
			trailingOptional = 0
		}
	}
	sig.optional = trailingOptional
}

// paramType returns a parameter's declared type, or `any` when unannotated. The
// type child, when present (ParamHasType), is the first child.
func (c *checker) paramType(p *ast.Node) *Type {
	if p.Int&ast.ParamHasType != 0 {
		return fromAST(p.Child(0))
	}
	return Any
}
