package parse

import (
	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/lex"
)

// This file holds the structural grammar of spec 02 Section 3 / design/
// control-flow.md / design/composition.md: the module top-level, block bodies,
// and the core statement set. The expression parser (expr.go, postfix.go) is
// driven from inside a statement head; a head runs over the CODE tokens between a
// STMT token and the lexer's BLOCK_OPEN (a brace body) or STMT_END (a line
// statement) terminator.

// parseModule builds the KindModule root from the whole token stream.
func (p *parser) parseModule() *ast.Node {
	mod := ast.New(ast.KindModule, 1, p.src)
	mod.Str = p.src.Name()
	for !p.at(lex.EOF) {
		mod.Add(p.parseItem(true))
	}
	return mod
}

// parseItem parses one top-level item or body item. topLevel marks the module
// scope, where composition heads (extends/block/macro/import/from/use/embed) are
// permitted; they are also valid as body items per the grammar (block and macro
// appear in Item), so the flag only gates module-only diagnostics elsewhere.
func (p *parser) parseItem(topLevel bool) *ast.Node {
	switch p.cur().Kind {
	case lex.TEXT:
		t := p.advance()
		n := p.node(ast.KindText, t)
		n.Str = t.Text
		return n
	case lex.VERBATIM:
		t := p.advance()
		n := p.node(ast.KindVerbatim, t)
		n.Str = t.Text
		return n
	case lex.OPEN_INTERP:
		return p.parsePrint()
	case lex.STMT:
		return p.parseStatement(topLevel)
	case lex.BLOCK_CLOSE:
		p.fail("unexpected '@}' with no open block")
	}
	p.fail("unexpected %s", describe(p.cur()))
	return nil
}

// parsePrint parses an interpolation "{{ expr [if/unless cond [else e]] }}". The
// postfix conditional tail is desugared into a ternary (design/expressions 4.7),
// so KindPrint always carries one expression child.
func (p *parser) parsePrint() *ast.Node {
	open := p.advance() // OPEN_INTERP
	expr := p.parseExpr()
	// Postfix conditional tail, reserved in interpolation context only.
	if p.isNameWord("if") || p.isNameWord("unless") {
		unless := p.cur().Text == "unless"
		t := p.advance()
		cond := p.parseExpr()
		empty := p.node(ast.KindString, t)
		var els *ast.Node = empty
		if p.isNameWord("else") {
			p.advance()
			els = p.parseExpr()
		}
		if unless {
			// {{ x unless c }} == c ? "" : x
			expr = p.node(ast.KindTernary, t, cond, empty, expr)
		} else {
			// {{ x if c else y }} == c ? x : y
			expr = p.node(ast.KindTernary, t, cond, expr, els)
		}
	}
	p.expect(lex.CLOSE_INTERP, "'}}' to close interpolation")
	return p.node(ast.KindPrint, open, expr)
}

// parseStatement dispatches on the statement keyword. The lexer emits the keyword
// without its '@' in the STMT token's Text.
func (p *parser) parseStatement(topLevel bool) *ast.Node {
	kw := p.cur().Text
	switch kw {
	case "if":
		return p.parseIf()
	case "for":
		return p.parseFor()
	case "set":
		return p.parseSet()
	case "with":
		return p.parseWith()
	case "apply":
		return p.parseApply()
	case "do":
		return p.parseDo()
	case "flush":
		return p.parseFlush()
	case "deprecated":
		return p.parseDeprecated()
	case "guard":
		return p.parseGuard()
	case "types":
		return p.parseTypes()
	case "escape":
		return p.parseEscape()
	case "sandbox":
		return p.parseSandbox()
	case "line":
		return p.parseLine()
	case "cache":
		return p.parseCache()
	case "verbatim":
		// A bare @verbatim head (the body already arrived as a VERBATIM token via
		// the lexer); the STMT/BLOCK_OPEN/VERBATIM/BLOCK_CLOSE shape is folded into a
		// single KindVerbatim node here.
		return p.parseVerbatim()
	case "extends":
		return p.parseExtends()
	case "block":
		return p.parseBlock()
	case "macro":
		return p.parseMacro()
	case "import":
		return p.parseImport()
	case "from":
		return p.parseFrom()
	case "use":
		return p.parseUse()
	case "embed":
		return p.parseEmbed()
	case "include":
		return p.parseInclude()
	case "elseif", "else":
		p.fail("'@%s' without a matching '@if' or '@for'", kw)
	}
	p.fail("unknown statement '@%s'", kw)
	return nil
}

// --- body and head helpers ---

// expectStmt consumes the leading STMT token for keyword kw (a sanity assertion;
// the dispatcher already matched it) and returns it.
func (p *parser) expectStmt(kw string) lex.Token {
	t := p.cur()
	if t.Kind != lex.STMT || t.Text != kw {
		p.fail("internal: expected statement '@%s'", kw)
	}
	return p.advance()
}

// openBody consumes the BLOCK_OPEN that begins a brace body.
func (p *parser) openBody() {
	p.expect(lex.BLOCK_OPEN, "'{' to open a block body")
}

// endLine consumes the STMT_END that terminates a line statement.
func (p *parser) endLine() {
	p.expect(lex.STMT_END, "end of statement")
}

// parseBodyItems parses body items until the closing BLOCK_CLOSE (or EOF, which a
// caller turns into an unbalanced-block error via closeBlock). It does NOT consume
// the terminator. Branch continuations (elseif/else) arrive as their own STMT head
// AFTER a BLOCK_CLOSE (the lexer emits one '@}' per branch, lex/doors.go
// scanContinuation), so a body simply ends at its BLOCK_CLOSE.
func (p *parser) parseBodyItems() []*ast.Node {
	var items []*ast.Node
	for !p.at(lex.BLOCK_CLOSE) && !p.at(lex.EOF) {
		items = append(items, p.parseItem(false))
	}
	return items
}

// closeBlock consumes the BLOCK_CLOSE that ends a brace body.
func (p *parser) closeBlock() {
	p.expect(lex.BLOCK_CLOSE, "'@}' to close the block")
}
