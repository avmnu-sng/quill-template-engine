package parse

import (
	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/lex"
)

// parseExtends parses "@extends expr NL" (design/composition Section 2.1). The
// operand is a string-coerced expression or a candidate list.
func (p *parser) parseExtends() *ast.Node {
	t := p.expectStmt("extends")
	n := p.node(ast.KindExtends, t, p.parseExpr())
	p.endLine()
	return n
}

// parseBlock parses "@block name [sig] { body } @}" or the shortcut value form
// "@block name expr NL" (design/composition Section 2.2). Int tags the form (0
// brace body, 1 shortcut). Optional children: a KindParams (when a "(params)"
// signature is present, Bool set) and a return KindType.
func (p *parser) parseBlock() *ast.Node {
	t := p.expectStmt("block")
	nameTok := p.expect(lex.NAME, "a block name after 'block'")
	b := p.node(ast.KindBlock, t)
	b.Str = nameTok.Text

	// Optional signature: "(" [params] ")" and/or "-> Type".
	if p.at(lex.LPAREN) {
		b.Add(p.parseParamList())
		b.Bool = true
	}
	if p.accept(lex.TYPEARROW) {
		b.Add(p.parseType())
	}

	if p.at(lex.BLOCK_OPEN) {
		// Long brace-body form.
		p.openBody()
		b.Int = 0
		b.Children = append(b.Children, p.parseBodyItems()...)
		p.closeBlock()
		return b
	}
	// Shortcut value form: a single expression printed, closes at end of line.
	b.Int = 1
	b.Add(p.parseExpr())
	p.endLine()
	return b
}

// parseMacro parses "@macro name(params) [-> T] { body } @}" (design/composition
// Section 3.1). Child 0 is a KindParams; an optional return KindType follows; then
// body items.
func (p *parser) parseMacro() *ast.Node {
	t := p.expectStmt("macro")
	nameTok := p.expect(lex.NAME, "a macro name after 'macro'")
	m := p.node(ast.KindMacro, t)
	m.Str = nameTok.Text
	m.Add(p.parseParamList())
	if p.accept(lex.TYPEARROW) {
		m.Add(p.parseType())
	}
	p.openBody()
	m.Children = append(m.Children, p.parseBodyItems()...)
	p.closeBlock()
	return m
}

// parseProvide parses "@provide label { body } @}" (design/composition, named
// accumulating slots). Str is the slot label; the body items follow. The rendered
// body is appended to the label's slot buffer at render time, in execution order.
func (p *parser) parseProvide() *ast.Node {
	t := p.expectStmt("provide")
	labelTok := p.expect(lex.NAME, "a slot label after 'provide'")
	n := p.node(ast.KindProvide, t)
	n.Str = labelTok.Text
	p.openBody()
	n.Children = append(n.Children, p.parseBodyItems()...)
	p.closeBlock()
	return n
}

// parseYield parses "@yield label NL" (design/composition, named accumulating
// slots). Str is the slot label; it emits the accumulated slot content once.
func (p *parser) parseYield() *ast.Node {
	t := p.expectStmt("yield")
	labelTok := p.expect(lex.NAME, "a slot label after 'yield'")
	n := p.node(ast.KindYield, t)
	n.Str = labelTok.Text
	p.endLine()
	return n
}

// parseCallBlock parses "@call [(callerParams)] name(args) { body } @}"
// (design/composition, call-blocks). The optional leading "(p1, p2)" declares the
// caller-block parameters the macro passes back via caller(v1, v2); then the macro
// name and its "(args)" call; then the braced body. Child 0 is a KindParams (the
// caller parameters, empty when the prefix is absent); the macro-call KindArg
// children follow; the final child is the KindBody caller block.
func (p *parser) parseCallBlock() *ast.Node {
	t := p.expectStmt("call")
	n := p.node(ast.KindCallBlock, t)

	// Optional caller-parameter prefix: "@call(p1, p2) name(...)". It is present only
	// when the first "(" is immediately followed by a parameter shape AND a NAME
	// follows the matching ")": "@call(p) name(...)". A bare "@call name(...)" has no
	// prefix. Distinguish by whether a NAME precedes the first "(".
	callerParams := p.node(ast.KindParams, t)
	if p.at(lex.LPAREN) {
		callerParams = p.parseParamList()
	}
	n.Add(callerParams)

	nameTok := p.expect(lex.NAME, "a macro name in a call block")
	n.Str = nameTok.Text
	p.expect(lex.LPAREN, "'(' to open the macro arguments")
	p.parseArgsInto(n)
	p.expect(lex.RPAREN, "')' to close the macro arguments")

	p.openBody()
	body := p.node(ast.KindBody, t)
	body.Children = append(body.Children, p.parseBodyItems()...)
	p.closeBlock()
	n.Add(body)
	return n
}

// parseParamList parses "( [Param {, Param}] )" into a KindParams node. The
// opening paren is required. The two tail captures obey a fixed order at the end
// of the list: an optional positional variadic "...name" may be followed only by
// an optional keyword variadic "**name", and nothing follows the kwargs tail. So
// a declaration ends with at most one "...name" then at most one "**name".
func (p *parser) parseParamList() *ast.Node {
	open := p.expect(lex.LPAREN, "'(' to open a parameter list")
	params := p.node(ast.KindParams, open)
	var sawVariadic, sawKwargs bool
	for !p.at(lex.RPAREN) && !p.at(lex.EOF) {
		param := p.parseParam()
		isKwargs := param.Int&ast.ParamKwargs != 0
		if sawKwargs {
			p.failAt(tokAt(param), "a kwargs '**name' must be the last parameter")
		}
		if sawVariadic && !isKwargs {
			p.failAt(tokAt(param), "a variadic '...name' must be the last positional parameter")
		}
		if param.Bool {
			sawVariadic = true
		}
		if isKwargs {
			sawKwargs = true
		}
		params.Add(param)
		if !p.accept(lex.COMMA) {
			break
		}
	}
	p.expect(lex.RPAREN, "')' to close a parameter list")
	return params
}

// parseImport parses "@import src as alias NL" (design/composition Section 3.3).
// The source is a string expression or the special name _self.
func (p *parser) parseImport() *ast.Node {
	t := p.expectStmt("import")
	src := p.parseImportSrc()
	if !p.isNameWord("as") {
		p.fail("expected 'as' in an import statement, found %s", describe(p.cur()))
	}
	p.advance() // as
	aliasTok := p.expect(lex.NAME, "an alias name after 'as'")
	n := p.node(ast.KindImport, t, src)
	n.Str = aliasTok.Text
	p.endLine()
	return n
}

// parseFrom parses "@from src import a, b as c NL" (design/composition 3.4).
func (p *parser) parseFrom() *ast.Node {
	t := p.expectStmt("from")
	src := p.parseImportSrc()
	if !p.isNameWord("import") {
		p.fail("expected 'import' in a from statement, found %s", describe(p.cur()))
	}
	p.advance() // import
	n := p.node(ast.KindFrom, t, src)
	for {
		nameTok := p.expect(lex.NAME, "an imported macro name")
		item := p.node(ast.KindFromItem, nameTok)
		item.Str = nameTok.Text
		if p.isNameWord("as") {
			p.advance()
			aliasTok := p.expect(lex.NAME, "an alias name after 'as'")
			item.Bool = true
			alias := p.node(ast.KindName, aliasTok)
			alias.Str = aliasTok.Text
			item.Add(alias)
		}
		n.Add(item)
		if !p.accept(lex.COMMA) {
			break
		}
	}
	p.endLine()
	return n
}

// parseImportSrc parses an import/from source: a string-coerced expression or the
// special name _self (spec 02 ImportSrc).
func (p *parser) parseImportSrc() *ast.Node {
	if p.isNameWord("_self") {
		t := p.advance()
		n := p.node(ast.KindSpecialName, t)
		n.Str = "_self"
		return n
	}
	return p.parseExpr()
}

// parseUse parses "@use src [with map] NL" (design/composition Section 4). The
// alias map, when present, is a mapping literal flagged by Bool.
func (p *parser) parseUse() *ast.Node {
	t := p.expectStmt("use")
	n := p.node(ast.KindUse, t, p.parseExpr())
	if p.isNameWord("with") {
		p.advance()
		n.Add(p.parseMap())
		n.Bool = true
	}
	p.endLine()
	return n
}

// parseEmbed parses "@embed src [with map] [only] [ignore missing] { blocks } @}"
// (design/composition Section 5). The include-modifier flags ride in Int.
func (p *parser) parseEmbed() *ast.Node {
	t := p.expectStmt("embed")
	e := p.node(ast.KindEmbed, t, p.parseExpr())
	e.Int = p.parseIncMods(e)
	p.openBody()
	// The body may contain only @block definitions (content-outside-blocks rule);
	// we accept body items and let the checker enforce block-only.
	e.Children = append(e.Children, p.parseBodyItems()...)
	p.closeBlock()
	return e
}

// parseInclude parses "@include expr [with expr] [only] [ignore missing] NL"
// (design/composition Section 6.1).
func (p *parser) parseInclude() *ast.Node {
	t := p.expectStmt("include")
	n := p.node(ast.KindInclude, t, p.parseExpr())
	n.Int = p.parseIncMods(n)
	p.endLine()
	return n
}

// parseIncMods parses the shared include/embed modifiers "[with expr] [only]
// [ignore missing]" and returns the flag bitset, appending a with-map/expr child
// when present (design/composition Section 6.1, spec 02 IncMods).
func (p *parser) parseIncMods(parent *ast.Node) int64 {
	var flags int64
	if p.isNameWord("with") {
		p.advance()
		parent.Add(p.parseExpr())
		flags |= ast.IncWith
	}
	if p.isNameWord("only") {
		p.advance()
		flags |= ast.IncOnly
	}
	if p.isNameWord("ignore") && p.isNameWordAt(1, "missing") {
		p.advance()
		p.advance()
		flags |= ast.IncIgnoreMissing
	}
	return flags
}
