package parse

import "github.com/avmnusng/quill-template-engine/ast"

// toTarget reinterprets an already-parsed expression as an assignment /
// destructuring target (spec 02 R10, design/expressions Section 9). The LHS of
// "=" is parsed as an Expr and converted here, so a sequence literal "[a, b]"
// becomes a list pattern and a mapping literal "{name}" becomes a map pattern.
//
//	Target_ = Name | Seq_ | Map_ .
//	Seq_    = "[" [ TgtSlot {, TgtSlot} [, "..." Name] ] "]" .
//	TgtSlot = [ Target_ [ "?" ] ] .                  (* elided / optional slots *)
//	Map_    = "{" MapTgt {, MapTgt} "}" .
//	MapTgt  = Name | Name ":" Name .                 (* {name} or {key: alias} *)
func (p *parser) toTarget(e *ast.Node) *ast.Node {
	switch e.Kind {
	case ast.KindName:
		// A bare identifier is a simple target; wrap as a KindTarget for a uniform
		// shape with set/for targets.
		tgt := ast.New(ast.KindTarget, e.Line, e.Src)
		tgt.Str = e.Str
		return tgt
	case ast.KindList:
		return p.listToPattern(e)
	case ast.KindMap:
		return p.mapToPattern(e)
	}
	p.failAt(tokAt(e), "invalid assignment target")
	return nil
}

// listToPattern converts a sequence literal into a KindListPattern. Each element
// becomes a slot target; a trailing KindSpread element becomes a tail capture; a
// KindUnary("?")-style optional is not produced by the expression parser, so an
// optional slot "[a, b?]" requires the "?" to have been parsed -- which the
// sequence parser does not do. We therefore detect the optional/elided shapes
// from the element nodes the expression parser produced.
//
// The expression grammar parses "[a, b]" with both elements as plain names; an
// elided slot "[, b]" is not expressible as a sequence-literal element (a bare
// comma), so elision and the "?" optional marker are handled at the dedicated
// target grammar. Here we support the common forms the expression parser yields:
// names, nested list/map patterns, and a trailing "...name".
func (p *parser) listToPattern(list *ast.Node) *ast.Node {
	pat := ast.New(ast.KindListPattern, list.Line, list.Src)
	for i, el := range list.Children {
		if el.Kind == ast.KindSpread {
			if i != len(list.Children)-1 {
				p.failAt(tokAt(el), "a tail capture '...name' must be the last slot")
			}
			pat.Add(el) // tail capture
			continue
		}
		pat.Add(p.toTarget(el))
	}
	return pat
}

// mapToPattern converts a mapping literal into a KindMapPattern of KindMapTarget
// slots: a shorthand "{name}" binds name->name; a keyed "{key: alias}" binds
// key->alias (the alias must be a bare name).
func (p *parser) mapToPattern(m *ast.Node) *ast.Node {
	pat := ast.New(ast.KindMapPattern, m.Line, m.Src)
	for _, entry := range m.Children {
		switch entry.Int {
		case ast.MapEntryShorthand:
			name := entry.Child(0)
			mt := ast.New(ast.KindMapTarget, entry.Line, entry.Src)
			mt.Str = name.Str
			pat.Add(mt)
		case ast.MapEntryKeyed:
			key := entry.Child(0)
			alias := entry.Child(1)
			if alias.Kind != ast.KindName {
				p.failAt(tokAt(alias), "a destructuring alias must be a bare name")
			}
			mt := ast.New(ast.KindMapTarget, entry.Line, entry.Src)
			mt.Str = key.Str
			mt.Bool = true
			aliasNode := ast.New(ast.KindName, alias.Line, alias.Src)
			aliasNode.Str = alias.Str
			mt.Add(aliasNode)
			pat.Add(mt)
		default:
			p.failAt(tokAt(entry), "invalid mapping destructuring entry")
		}
	}
	return pat
}
