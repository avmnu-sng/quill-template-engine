package parse

import (
	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/lex"
)

// parseType parses a type annotation per spec 02 Section 5:
//
//	Type      = UnionType .
//	UnionType = AtomType { "|" AtomType } [ "?" ] .
//	AtomType  = "any" | "null" | "bool" | "int" | "float" | "string"
//	          | "list" "<" Type ">"
//	          | "map" "<" Type "," Type ">"
//	          | "Object" "<" STRING ">"
//	          | "(" [ TypeList ] ")" "=>" Type
//	          | "(" Type ")" .
//
// The "|" union separator is unambiguous here because a type appears only after
// ":" / "->" / inside "<>" (spec 02 R8), where the filter pipe never occurs.
func (p *parser) parseType() *ast.Node {
	first := p.parseAtomType()
	if !p.at(lex.PIPE) && !p.at(lex.QUESTION) {
		return first
	}
	union := p.node(ast.KindType, p.cur(), first)
	union.Str = "union"
	for p.accept(lex.PIPE) {
		union.Add(p.parseAtomType())
	}
	if p.accept(lex.QUESTION) {
		union.Bool = true // trailing "?" nullable
	}
	return union
}

// parseAtomType parses one atom of a type expression.
func (p *parser) parseAtomType() *ast.Node {
	t := p.cur()
	if t.Kind == lex.LPAREN {
		return p.parseParenType()
	}
	if t.Kind != lex.NAME {
		p.fail("expected a type, found %s", describe(t))
	}
	p.advance()
	n := p.node(ast.KindType, t)
	n.Str = t.Text
	switch t.Text {
	case "list":
		p.expect(lex.LT, "'<' after 'list'")
		n.Add(p.parseType())
		p.expectTypeClose()
	case "map":
		p.expect(lex.LT, "'<' after 'map'")
		n.Add(p.parseType())
		p.expect(lex.COMMA, "',' between map key and value types")
		n.Add(p.parseType())
		p.expectTypeClose()
	case "Object":
		p.expect(lex.LT, "'<' after 'Object'")
		nameTok := p.expect(lex.STRING, "a quoted type name in Object<\"...\">")
		s, err := decodeString(nameTok)
		if err != nil {
			p.failAt(nameTok, "%s", err.Error())
		}
		name := p.node(ast.KindString, nameTok)
		name.Str = s
		n.Add(name)
		p.expectTypeClose()
	}
	return n
}

// parseParenType parses either a grouped type "(T)" or an arrow/callable type
// "(T, ..) => R".
func (p *parser) parseParenType() *ast.Node {
	open := p.advance() // '('
	var params []*ast.Node
	for !p.at(lex.RPAREN) && !p.at(lex.EOF) {
		params = append(params, p.parseType())
		if !p.accept(lex.COMMA) {
			break
		}
	}
	p.expect(lex.RPAREN, "')' to close a parenthesized type")
	if p.accept(lex.ARROW) {
		ret := p.parseType()
		n := p.node(ast.KindType, open, params...)
		n.Str = "arrow"
		n.Add(ret)
		return n
	}
	// Grouped type: must be exactly one inner type.
	if len(params) != 1 {
		p.failAt(open, "a parenthesized type must be a single type or an arrow type '(...) => T'")
	}
	g := p.node(ast.KindType, open, params[0])
	g.Str = "group"
	return g
}

// expectTypeClose consumes the ">" that closes a list<>/map<>/Object<> type.
func (p *parser) expectTypeClose() {
	if !p.at(lex.GT) {
		p.fail("expected '>' to close a generic type, found %s", describe(p.cur()))
	}
	p.advance()
}
