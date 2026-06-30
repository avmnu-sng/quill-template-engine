// Package parse turns a Quill template into an ast.Module. It drives the lexer
// (package lex) over the @-sigil default mode, then runs a structural parser over
// the token stream and a Pratt expression parser over CODE token runs.
//
// The expression parser implements the full seventeen-level precedence ladder of
// spec 01 Section 3.1 / spec 02 Section 4 / design/expressions.md, including the
// AST-driven power/unary-minus rule (spec 02 R6) that makes -1 ** 0 == -1 fall
// out of the grammar rather than a special case. The structural parser handles
// the core statement set (spec 01 Section 5.1) per spec 02 Section 3 and
// design/control-flow.md and design/composition.md.
//
// Every syntax fault is an *errors.Error of KindSyntax carrying the offending
// token's line and the template Source (spec 01 Section 1.8), surfaced through a
// panic/recover that unwinds to Parse.
package parse

import (
	"github.com/avmnusng/quill-template-engine/ast"
	"github.com/avmnusng/quill-template-engine/errors"
	"github.com/avmnusng/quill-template-engine/lex"
	"github.com/avmnusng/quill-template-engine/source"
)

// Parse scans and parses a template source into an ast.Module, or returns a
// KindSyntax *errors.Error positioned at the first fault.
func Parse(src *source.Source) (*ast.Node, error) {
	ts := lex.Lex(src)
	p := &parser{src: src, toks: ts.Tokens}
	return p.parse()
}

// ParseString is a convenience wrapper that builds a Source from a name and body.
func ParseString(name, body string) (*ast.Node, error) {
	return Parse(source.New(name, body))
}

// parser holds the structural-parse state: the token slice and a cursor. The
// lexer produced the whole stream up front, so the parser only ever moves forward
// with single-token lookahead (peek) and the occasional two-token lookahead for
// disambiguation (e.g. an arrow param list vs a grouped expression).
type parser struct {
	src  *source.Source
	toks []lex.Token
	pos  int
}

// parse builds the module and converts a thrown syntax fault into an error.
func (p *parser) parse() (mod *ast.Node, err error) {
	defer func() {
		if r := recover(); r != nil {
			if se, ok := r.(*errors.Error); ok {
				err = se
				return
			}
			panic(r)
		}
	}()
	// A lexical fault surfaces as a single ERROR token; report it first so a
	// malformed token stream never reaches the structural grammar.
	for _, t := range p.toks {
		if t.Kind == lex.ERROR {
			p.failAt(t, "%s", t.Text)
		}
	}
	mod = p.parseModule()
	return mod, nil
}

// --- token cursor ---

// cur returns the current token (EOF when past the end; the lexer always appends
// EOF, so this is safe).
func (p *parser) cur() lex.Token {
	if p.pos >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.pos]
}

// peekAt returns the token n positions ahead of the cursor, clamped to EOF.
func (p *parser) peekAt(n int) lex.Token {
	i := p.pos + n
	if i >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[i]
}

// at reports whether the current token has kind k.
func (p *parser) at(k lex.Kind) bool { return p.cur().Kind == k }

// advance consumes and returns the current token.
func (p *parser) advance() lex.Token {
	t := p.cur()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

// accept consumes the current token if it has kind k and reports whether it did.
func (p *parser) accept(k lex.Kind) bool {
	if p.at(k) {
		p.advance()
		return true
	}
	return false
}

// expect consumes a token of kind k or fails with a syntax error naming what was
// expected and what was found.
func (p *parser) expect(k lex.Kind, what string) lex.Token {
	if !p.at(k) {
		p.fail("expected %s but found %s", what, describe(p.cur()))
	}
	return p.advance()
}

// --- expression-position keyword detection ---

// isNameWord reports whether the current NAME token spells word. Word-operators
// are lexed as NAME (spec 02 R2); the parser reclassifies by position here.
func (p *parser) isNameWord(word string) bool {
	return p.cur().Kind == lex.NAME && p.cur().Text == word
}

// isNameWordAt reports whether the token n ahead is a NAME spelling word.
func (p *parser) isNameWordAt(n int, word string) bool {
	t := p.peekAt(n)
	return t.Kind == lex.NAME && t.Text == word
}

// --- error helpers ---

// fail raises a syntax error at the current token's position.
func (p *parser) fail(format string, args ...any) {
	p.failAt(p.cur(), format, args...)
}

// failAt raises a syntax error at a specific token's position, attaching the
// template source and 1-based line so the message locates the fault (spec 01
// Section 1.8). It never returns; it panics with the *errors.Error, caught in
// parse.
func (p *parser) failAt(t lex.Token, format string, args ...any) {
	panic(errors.New(errors.KindSyntax, format, args...).At(p.src, t.Line))
}

// node builds an AST node at the given token's position, in this parser's source.
func (p *parser) node(k ast.Kind, t lex.Token, children ...*ast.Node) *ast.Node {
	n := ast.New(k, t.Line, p.src, children...)
	return n
}

// tokAt synthesizes a token carrying a node's line so failAt can position an
// error against an already-built AST node (e.g. an invalid assignment target).
func tokAt(n *ast.Node) lex.Token {
	if n == nil {
		return lex.Token{}
	}
	return lex.Token{Line: n.Line}
}

// describe renders a token for an error message in a stable, human form.
func describe(t lex.Token) string {
	switch t.Kind {
	case lex.EOF:
		return "end of input"
	case lex.TEXT:
		return "template text"
	case lex.NAME:
		return "name " + quoteASCII(t.Text)
	case lex.STMT:
		return "statement @" + t.Text
	case lex.BLOCK_OPEN:
		return "'{'"
	case lex.BLOCK_CLOSE:
		return "'@}'"
	case lex.STMT_END:
		return "end of statement"
	case lex.CLOSE_INTERP:
		return "'}}'"
	case lex.OPEN_INTERP:
		return "'{{'"
	}
	return t.Kind.String()
}

// quoteASCII wraps s in single quotes for messages without importing strconv.
func quoteASCII(s string) string { return "'" + s + "'" }
