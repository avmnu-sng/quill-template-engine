package parse

import (
	"github.com/avmnu-sng/quill-template-engine/core/ast"
	"github.com/avmnu-sng/quill-template-engine/core/lex"
)

// parsePostfix is level 17, left-associative: a primary followed by zero or more
// postfix operators applied left to right (spec 02 Section 4, design/expressions
// Section 5).
//
//	Postfix = Primary { "." Name | "?." Name
//	                  | "[" Slice "]" | "?[" Expr "]"
//	                  | "(" Args ")"
//	                  | "|" Name [ "(" Args ")" ] } .
func (p *parser) parsePostfix() *ast.Node {
	recv := p.parsePrimary()
	for {
		switch p.cur().Kind {
		case lex.DOT:
			t := p.advance()
			name := p.memberName()
			n := p.node(ast.KindAttr, t, recv)
			n.Str = name
			recv = n
		case lex.OPTDOT:
			t := p.advance()
			name := p.memberName()
			n := p.node(ast.KindAttr, t, recv)
			n.Str = name
			n.Bool = true // null-safe
			recv = n
		case lex.LBRACKET:
			recv = p.parseSubscript(recv, false)
		case lex.OPTBRACK:
			recv = p.parseSubscript(recv, true)
		case lex.LPAREN:
			t := p.advance()
			call := p.node(ast.KindCall, t, recv)
			p.parseArgsInto(call)
			p.expect(lex.RPAREN, "')' to close call arguments")
			recv = call
		case lex.PIPE:
			recv = p.parseFilter(recv)
		default:
			return recv
		}
	}
}

// memberName reads the identifier after "." / "?.". A word-operator spelling is a
// plain NAME here (spec 02 R2: after "." it is always an identifier), so a host
// field named "in" is reachable as record.in.
func (p *parser) memberName() string {
	t := p.cur()
	if t.Kind != lex.NAME {
		p.fail("expected a member name after '.', found %s", describe(t))
	}
	return p.advance().Text
}

// parseSubscript handles "[" ... "]" and "?[" Expr "]": either an index a[k] or a
// slice a[start:end] (the slice colon form is not valid under "?[", which is index
// only per the grammar). nullSafe marks the "?[" form.
func (p *parser) parseSubscript(recv *ast.Node, nullSafe bool) *ast.Node {
	t := p.advance() // '[' or '?['
	if nullSafe {
		// ?[ Expr ] -- index only.
		key := p.parseExpr()
		p.expect(lex.RBRACKET, "']' to close index")
		n := p.node(ast.KindIndex, t, recv, key)
		n.Bool = true
		return n
	}
	// Slice = Expr | [Expr] ":" [Expr] .
	var start, end *ast.Node
	hasStart := false
	if !p.at(lex.COLON) {
		start = p.parseExpr()
		hasStart = true
	}
	if p.accept(lex.COLON) {
		// slice form
		hasEnd := false
		if !p.at(lex.RBRACKET) {
			end = p.parseExpr()
			hasEnd = true
		}
		p.expect(lex.RBRACKET, "']' to close slice")
		n := p.node(ast.KindSlice, t, recv, start, end)
		if hasStart {
			n.Int |= ast.SliceHasStart
		}
		if hasEnd {
			n.Int |= ast.SliceHasEnd
		}
		return n
	}
	// plain index a[k]
	p.expect(lex.RBRACKET, "']' to close index")
	return p.node(ast.KindIndex, t, recv, start)
}

// parseFilter handles "x | f" and "x | f(args)". The piped value is the implicit
// first argument; explicit args follow (design/expressions Section 5.4, 7). A
// word-operator spelling after "|" is a plain filter name (spec 02 R2).
func (p *parser) parseFilter(recv *ast.Node) *ast.Node {
	t := p.advance() // '|'
	if !p.at(lex.NAME) {
		p.fail("expected a filter name after '|', found %s", describe(p.cur()))
	}
	name := p.advance().Text
	n := p.node(ast.KindFilter, t, recv)
	n.Str = name
	if p.at(lex.LPAREN) {
		p.advance()
		p.parseArgsInto(n)
		p.expect(lex.RPAREN, "')' to close filter arguments")
	}
	return n
}

// parseArgsInto parses a (possibly empty) argument list and appends each Arg node
// to parent. It enforces that a positional argument may not follow a named one
// (design/expressions Section 7). The opening "(" is already consumed; this stops
// at the closing ")".
func (p *parser) parseArgsInto(parent *ast.Node) {
	seenNamed := false
	for !p.at(lex.RPAREN) && !p.at(lex.EOF) {
		arg := p.parseArg()
		if arg.Int == ast.ArgNamed {
			seenNamed = true
		} else if arg.Int == ast.ArgPositional && seenNamed {
			// Only a bare positional after a named argument is an error
			// (design/expressions Section 7). A spread is neither positional nor
			// named, so "f(a: 1, ...xs)" is permitted.
			p.failAt(p.cur(), "a positional argument may not follow a named argument")
		}
		parent.Add(arg)
		if !p.accept(lex.COMMA) {
			break
		}
	}
}

// parseArg parses one argument: named (name: expr), spread (...expr), or
// positional (expr). A named argument is recognized when a NAME is immediately
// followed by ":" (and the NAME is not itself a larger expression).
func (p *parser) parseArg() *ast.Node {
	// Spread.
	if p.at(lex.SPREAD) {
		t := p.advance()
		n := p.node(ast.KindArg, t, p.parseExpr())
		n.Int = ast.ArgSpread
		return n
	}
	// Named: NAME ":" Expr.
	if p.at(lex.NAME) && p.peekAt(1).Kind == lex.COLON {
		nameTok := p.advance()
		p.advance() // ':'
		n := p.node(ast.KindArg, nameTok, p.parseExpr())
		n.Str = nameTok.Text
		n.Int = ast.ArgNamed
		return n
	}
	// Positional.
	t := p.cur()
	n := p.node(ast.KindArg, t, p.parseExpr())
	n.Int = ast.ArgPositional
	return n
}

// parsePrimary parses a primary expression (spec 02 Section 4): a literal, a
// special name, a bare identifier, a grouped expression or arrow, a sequence
// literal, or a mapping literal. Arrow detection (spec 02 R9) happens here: a bare
// NAME followed by "=>" is a single-param arrow; a "(" ... ")" followed by "=>" is
// a parenthesized param list.
func (p *parser) parsePrimary() *ast.Node {
	// parsePrimary is the choke point every nested expression passes through, so
	// the expression-nesting depth guard sits here (see parser.enter/leave).
	p.enter()
	defer p.leave()
	t := p.cur()
	switch t.Kind {
	case lex.INT:
		p.advance()
		n := p.node(ast.KindInt, t)
		n.Int = parseIntLit(p, t)
		return n
	case lex.FLOAT:
		p.advance()
		n := p.node(ast.KindFloat, t)
		n.Float = parseFloatLit(p, t)
		return n
	case lex.STRING:
		return p.parseStringLit(t)
	case lex.TRUE:
		p.advance()
		n := p.node(ast.KindBool, t)
		n.Bool = true
		return n
	case lex.FALSE:
		p.advance()
		return p.node(ast.KindBool, t)
	case lex.NULL:
		p.advance()
		return p.node(ast.KindNull, t)
	case lex.NAME:
		return p.parseNameOrArrow(t)
	case lex.LPAREN:
		return p.parseGroupOrArrow()
	case lex.LBRACKET:
		return p.parseSeq()
	case lex.LBRACE:
		return p.parseMap()
	}
	p.fail("expected an expression, found %s", describe(t))
	return nil
}

// parseNameOrArrow handles a bare identifier, a special engine name, or a
// single-parameter arrow "name => body".
func (p *parser) parseNameOrArrow(t lex.Token) *ast.Node {
	if p.peekAt(1).Kind == lex.ARROW {
		// x => body
		p.advance() // name
		p.advance() // =>
		param := p.node(ast.KindParam, t)
		param.Str = t.Text
		body := p.parseExpr()
		return p.node(ast.KindArrow, t, param, body)
	}
	p.advance()
	switch t.Text {
	case "_self", "_context", "_charset":
		n := p.node(ast.KindSpecialName, t)
		n.Str = t.Text
		return n
	}
	n := p.node(ast.KindName, t)
	n.Str = t.Text
	return n
}

// parseGroupOrArrow parses "(" ... ")" then checks for a trailing "=>": if
// present, the contents are an arrow ParamList; otherwise it is a grouped
// expression (spec 02 R9). To distinguish without backtracking the expression
// parser, we first try to read it as a parameter list when the contents look like
// one and "=>" follows; otherwise we parse a single grouped Expr.
func (p *parser) parseGroupOrArrow() *ast.Node {
	open := p.cur()
	// Look ahead to decide: scan the matching ")" and check the token after it.
	if p.parenIsArrow() {
		return p.parseArrowParenForm(open)
	}
	p.advance() // '('
	inner := p.parseExpr()
	p.expect(lex.RPAREN, "')' to close grouping")
	return inner
}

// parenIsArrow reports whether the "(" at the cursor opens an arrow parameter list
// (it is followed, at the matching ")", by "=>"). It reads the matching-closer
// index from the precomputed match table (parser.buildMatch), so the decision is
// O(1) per '(' rather than a forward rescan; the table shares one depth counter
// across (), [], and {}, so the matching closer is whichever bracket first brings
// depth back to zero, and this is an arrow only when "=>" immediately follows it.
func (p *parser) parenIsArrow() bool {
	c := p.match[p.pos]
	return c >= 0 && c+1 < len(p.toks) && p.toks[c+1].Kind == lex.ARROW
}

// parseArrowParenForm parses "(" [ParamList] ")" "=>" Expr after parenIsArrow has
// confirmed the form. Arrow parameters are positional: an ordinary name, an
// optional ":Type" and "=default", or a trailing "...name" variadic. Arrows carry
// no named-argument mechanism, so a "**name" kwargs tail belongs to a macro or
// block declaration and is rejected here (spec 02 R9, spec 01 arrow forms).
func (p *parser) parseArrowParenForm(open lex.Token) *ast.Node {
	p.advance() // '('
	arrow := p.node(ast.KindArrow, open)
	for !p.at(lex.RPAREN) && !p.at(lex.EOF) {
		param := p.parseParam()
		if param.Int&ast.ParamKwargs != 0 {
			p.failAt(tokAt(param), "a '**name' kwargs parameter is only allowed on a macro or block")
		}
		arrow.Add(param)
		if !p.accept(lex.COMMA) {
			break
		}
	}
	p.expect(lex.RPAREN, "')' to close arrow parameters")
	p.expect(lex.ARROW, "'=>' after arrow parameters")
	body := p.parseExpr()
	arrow.Add(body)
	return arrow
}

// parseParam parses one parameter: a positional variadic "...name", a keyword
// variadic "**name", or "name [:Type] [=def]".
func (p *parser) parseParam() *ast.Node {
	if p.at(lex.SPREAD) {
		t := p.advance()
		if !p.at(lex.NAME) {
			p.fail("expected a name after '...' in a parameter list")
		}
		nameTok := p.advance()
		n := p.node(ast.KindParam, t)
		n.Str = nameTok.Text
		n.Bool = true // variadic
		return n
	}
	if p.at(lex.POW) {
		t := p.advance()
		if !p.at(lex.NAME) {
			p.fail("expected a name after '**' in a parameter list")
		}
		nameTok := p.advance()
		n := p.node(ast.KindParam, t)
		n.Str = nameTok.Text
		n.Int |= ast.ParamKwargs // keyword variadic
		return n
	}
	if !p.at(lex.NAME) {
		p.fail("expected a parameter name, found %s", describe(p.cur()))
	}
	nameTok := p.advance()
	n := p.node(ast.KindParam, nameTok)
	n.Str = nameTok.Text
	if p.accept(lex.COLON) {
		n.Add(p.parseType())
		n.Int |= ast.ParamHasType
	}
	if p.accept(lex.ASSIGN) {
		n.Add(p.parseExpr())
		n.Int |= ast.ParamHasDefault
	}
	return n
}

// parseSeq parses a sequence literal "[ Expr {, Expr} [,] ]" with spread elements.
func (p *parser) parseSeq() *ast.Node {
	open := p.advance() // '['
	list := p.node(ast.KindList, open)
	for !p.at(lex.RBRACKET) && !p.at(lex.EOF) {
		list.Add(p.parseExpr()) // a leading "..." is parsed by parseUnary as KindSpread
		if !p.accept(lex.COMMA) {
			break
		}
	}
	p.expect(lex.RBRACKET, "']' to close sequence literal")
	return list
}

// parseMap parses a mapping literal "{ MapEntry {, MapEntry} [,] }" with the
// shorthand, computed-key, and spread entry forms (spec 02 Section 4, R3 balancing
// handled by the lexer).
func (p *parser) parseMap() *ast.Node {
	open := p.advance() // '{'
	m := p.node(ast.KindMap, open)
	for !p.at(lex.RBRACE) && !p.at(lex.EOF) {
		m.Add(p.parseMapEntry())
		if !p.accept(lex.COMMA) {
			break
		}
	}
	p.expect(lex.RBRACE, "'}' to close mapping literal")
	return m
}

// parseMapEntry parses one entry: spread "...e", computed "(e): v", keyed
// "name: v", or shorthand "name".
func (p *parser) parseMapEntry() *ast.Node {
	t := p.cur()
	if p.at(lex.SPREAD) {
		p.advance()
		e := p.node(ast.KindMapEntry, t, p.parseExpr())
		e.Int = ast.MapEntrySpread
		return e
	}
	if p.at(lex.LPAREN) {
		p.advance()
		key := p.parseExpr()
		p.expect(lex.RPAREN, "')' to close computed map key")
		p.expect(lex.COLON, "':' after a computed map key")
		val := p.parseExpr()
		e := p.node(ast.KindMapEntry, t, key, val)
		e.Int = ast.MapEntryComputed
		return e
	}
	if p.at(lex.NAME) || p.at(lex.STRING) {
		// "name: value" or shorthand "name". A string key is a keyed entry.
		if p.peekAt(1).Kind == lex.COLON {
			keyTok := p.advance()
			p.advance() // ':'
			key := p.node(ast.KindString, keyTok)
			key.Str = mapKeyName(p, keyTok)
			val := p.parseExpr()
			e := p.node(ast.KindMapEntry, t, key, val)
			e.Int = ast.MapEntryKeyed
			return e
		}
		if p.at(lex.NAME) {
			nameTok := p.advance()
			val := p.node(ast.KindName, nameTok)
			val.Str = nameTok.Text
			e := p.node(ast.KindMapEntry, t, val)
			e.Int = ast.MapEntryShorthand
			return e
		}
	}
	p.fail("expected a map entry, found %s", describe(t))
	return nil
}

// mapKeyName returns the bareword/string key text for a keyed map entry.
func mapKeyName(p *parser, t lex.Token) string {
	if t.Kind == lex.STRING {
		s, err := decodeString(t)
		if err != nil {
			p.failAt(t, "%s", err.Error())
		}
		return s
	}
	return t.Text
}
