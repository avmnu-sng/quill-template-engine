package parse

import (
	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/lex"
)

// This file implements the expression grammar of spec 02 Section 4 /
// design/expressions.md Section 11. It is written as precedence-climbing
// recursive descent, one method per ladder level, which is exactly the published
// EBNF and agrees with a Pratt table by construction (design/expressions.md
// Section 1). The climb runs from parseExpr (level 1, assignment/arrow) down to
// parsePostfix/parsePrimary (level 17). A left-associative level recurses on its
// right operand at the next-tighter level; a right-associative level recurses on
// itself.

// parseExpr is the entry point: level 1, assignment and arrow.
//
//	Assign = Ternary [ "=" Assign ] .   (right-assoc)
//	Arrow  = ( Name | "(" [ParamList] ")" ) "=>" Expr .
//
// Arrows are detected at the primary level (an identifier or a parenthesized
// param list immediately before "=>", spec 02 R9), so here we only handle the
// assignment tail after a full ternary.
func (p *parser) parseExpr() *ast.Node {
	left := p.parseTernary()
	if p.at(lex.ASSIGN) {
		t := p.advance()
		right := p.parseExpr() // right-associative: c = d = x
		target := p.toTarget(left)
		return p.node(ast.KindAssign, t, target, right)
	}
	return left
}

// parseTernary is level 2: c ? a : b, right-associative, with the no-else form
// c ? a yielding empty (the parser fills an empty-string else, design/expressions
// Section 4.7). The condition is a level-3 coalesce expression.
func (p *parser) parseTernary() *ast.Node {
	cond := p.parseCoalesce()
	if !p.at(lex.QUESTION) {
		return cond
	}
	t := p.advance()
	then := p.parseExpr()
	var els *ast.Node
	if p.accept(lex.COLON) {
		els = p.parseExpr()
	} else {
		// c ? a desugars to c ? a : "" (empty output), design/expressions 4.7.
		els = p.node(ast.KindString, t)
	}
	return p.node(ast.KindTernary, t, cond, then, els)
}

// parseCoalesce is level 3: a ?? b and a ?: b, right-associative. The grammar
// writes these as a left-folding loop, but both are right-associative in intent;
// a right-fold is built by recursion on the RHS.
func (p *parser) parseCoalesce() *ast.Node {
	left := p.parseOr()
	switch p.cur().Kind {
	case lex.COALESCE:
		t := p.advance()
		return p.node(ast.KindCoalesce, t, left, p.parseCoalesce())
	case lex.ELVIS:
		t := p.advance()
		return p.node(ast.KindElvis, t, left, p.parseCoalesce())
	}
	return left
}

// parseOr is level 4: or / ||, left-associative.
func (p *parser) parseOr() *ast.Node {
	left := p.parseXor()
	for p.isNameWord("or") || p.at(lex.OROR) {
		t := p.advance()
		left = p.node(ast.KindLogical, t, left, p.parseXor())
		left.Str = "or"
	}
	return left
}

// parseXor is level 5: xor, left-associative.
func (p *parser) parseXor() *ast.Node {
	left := p.parseAnd()
	for p.isNameWord("xor") {
		t := p.advance()
		left = p.node(ast.KindLogical, t, left, p.parseAnd())
		left.Str = "xor"
	}
	return left
}

// parseAnd is level 6: and / &&, left-associative.
func (p *parser) parseAnd() *ast.Node {
	left := p.parseBitOr()
	for p.isNameWord("and") || p.at(lex.ANDAND) {
		t := p.advance()
		left = p.node(ast.KindLogical, t, left, p.parseBitOr())
		left.Str = "and"
	}
	return left
}

// parseBitOr is level 7: b_or / |||, left-associative.
func (p *parser) parseBitOr() *ast.Node {
	left := p.parseBitXor()
	for p.isNameWord("b_or") || p.at(lex.BITOR3) {
		t := p.advance()
		left = p.binary(t, "b_or", left, p.parseBitXor())
	}
	return left
}

// parseBitXor is level 8: b_xor / ^, left-associative.
func (p *parser) parseBitXor() *ast.Node {
	left := p.parseBitAnd()
	for p.isNameWord("b_xor") || p.at(lex.CARET) {
		t := p.advance()
		left = p.binary(t, "b_xor", left, p.parseBitAnd())
	}
	return left
}

// parseBitAnd is level 9: b_and / &, left-associative.
func (p *parser) parseBitAnd() *ast.Node {
	left := p.parseCmp()
	for p.isNameWord("b_and") || p.at(lex.AMP) {
		t := p.advance()
		left = p.binary(t, "b_and", left, p.parseCmp())
	}
	return left
}

// parseCmp is level 10: comparison / membership / test, NON-ASSOCIATIVE
// (design/expressions Section 2.1). A single comparison/membership/test operator
// may be applied; a chained second one (a == b == c) is a syntax error rather than
// a silent left-fold.
func (p *parser) parseCmp() *ast.Node {
	left := p.parseRange()
	if op, ok := p.cmpOp(); ok {
		left = p.applyCmp(left, op)
		// Non-associative across the whole level: any second level-10 operator of
		// any family (comparison/membership OR a following test) is rejected, so
		// "a == b is even" and "a in b is empty" fail the same way as "a == b == c".
		if _, chained := p.cmpOp(); chained {
			p.fail("comparison operators are non-associative; parenthesize or use 'and' to chain")
		}
		if p.isTestStart() {
			p.fail("comparison operators are non-associative; parenthesize or use 'and' to chain")
		}
		return left
	}
	if p.isTestStart() {
		left = p.parseTest(left)
		if p.isTestStart() {
			p.fail("test operators are non-associative; parenthesize or use 'and' to chain")
		}
		if _, chained := p.cmpOp(); chained {
			p.fail("comparison operators are non-associative; parenthesize or use 'and' to chain")
		}
	}
	return left
}

// cmpKind tags how a level-10 operator builds its node.
type cmpOp struct {
	spelling string
	kind     ast.Kind // KindBinary or KindMembership
	negate   bool     // the "not in" form
}

// cmpOp recognizes a comparison or membership operator at the cursor WITHOUT
// consuming it, returning its descriptor. Word operators are reclassified here
// (spec 02 R2): they are operators only in this infix position.
func (p *parser) cmpOp() (cmpOp, bool) {
	switch p.cur().Kind {
	case lex.EQ:
		return cmpOp{"==", ast.KindBinary, false}, true
	case lex.NE:
		return cmpOp{"!=", ast.KindBinary, false}, true
	case lex.LT:
		return cmpOp{"<", ast.KindBinary, false}, true
	case lex.GT:
		return cmpOp{">", ast.KindBinary, false}, true
	case lex.LE:
		return cmpOp{"<=", ast.KindBinary, false}, true
	case lex.GE:
		return cmpOp{">=", ast.KindBinary, false}, true
	case lex.SPACESHIP:
		return cmpOp{"<=>", ast.KindBinary, false}, true
	case lex.NAME:
		switch p.cur().Text {
		case "in":
			return cmpOp{"in", ast.KindMembership, false}, true
		case "matches":
			return cmpOp{"matches", ast.KindMembership, false}, true
		case "not":
			// "not in" only; a bare "not" here is not an infix operator.
			if p.isNameWordAt(1, "in") {
				return cmpOp{"in", ast.KindMembership, true}, true
			}
		case "starts":
			if p.isNameWordAt(1, "with") {
				return cmpOp{"starts with", ast.KindMembership, false}, true
			}
		case "ends":
			if p.isNameWordAt(1, "with") {
				return cmpOp{"ends with", ast.KindMembership, false}, true
			}
		case "has":
			if p.isNameWordAt(1, "some") {
				return cmpOp{"has some", ast.KindMembership, false}, true
			}
			if p.isNameWordAt(1, "every") {
				return cmpOp{"has every", ast.KindMembership, false}, true
			}
		}
	}
	return cmpOp{}, false
}

// applyCmp consumes the operator cmpOp returned by cmpOp and builds the node over
// left and a fresh right operand at the next-tighter level (range).
func (p *parser) applyCmp(left *ast.Node, op cmpOp) *ast.Node {
	t := p.advance() // first operator token
	// Consume the trailing word of two-word operators.
	switch op.spelling {
	case "starts with", "ends with", "has some", "has every":
		p.advance()
	case "in":
		if op.negate {
			p.advance() // the "in" after "not"
		}
	}
	right := p.parseRange()
	if op.kind == ast.KindMembership {
		n := p.node(ast.KindMembership, t, left, right)
		n.Str = op.spelling
		n.Bool = op.negate
		return n
	}
	return p.binary(t, op.spelling, left, right)
}

// isTestStart reports whether an "is" / "is not" test application begins here.
func (p *parser) isTestStart() bool { return p.isNameWord("is") }

// parseTest parses "subject is [not] testName [arg | (args)]" (spec 02 R7,
// design/expressions Section 8). Test names are up to two greedy NAME words; a
// following "(" begins a parenthesized argument list, not a third name word.
func (p *parser) parseTest(subject *ast.Node) *ast.Node {
	t := p.advance() // "is"
	neg := false
	if p.isNameWord("not") {
		p.advance()
		neg = true
	}
	// A test name is normally a NAME, but the spec-documented tests `is true`,
	// `is null`, and `is none` (spec 03 Section 4) spell their names with the
	// literal keywords, which the lexer tokenizes as TRUE/FALSE/NULL rather than
	// NAME. Accept those keyword tokens here as one-word test names.
	if !p.at(lex.NAME) && !p.at(lex.TRUE) && !p.at(lex.FALSE) && !p.at(lex.NULL) {
		p.fail("expected a test name after 'is'")
	}
	name := p.advance().Text
	// Greedily take a second word unless it is followed by "(" used as a call, or
	// unless the next token cannot continue a test-name pair. A bare second NAME
	// becomes a two-word test only when it is itself a known test continuation; to
	// stay grammar-faithful we take a second NAME when one directly follows and is
	// not consumed as the one-positional argument. design/expressions 8 + spec 02
	// R7: two-word names like "same as", "divisible by" are greedy.
	if p.at(lex.NAME) && twoWordTest(name, p.cur().Text) {
		name += " " + p.advance().Text
	}
	n := p.node(ast.KindTest, t, subject)
	n.Str = name
	n.Bool = neg
	// Optional argument: "(" full args ")", or a single bare Primary positional.
	if p.at(lex.LPAREN) {
		p.advance()
		p.parseArgsInto(n)
		p.expect(lex.RPAREN, "')' to close test arguments")
	} else if p.startsTestArg() {
		arg := p.node(ast.KindArg, p.cur(), p.parsePrimary())
		arg.Int = ast.ArgPositional
		n.Add(arg)
	}
	return n
}

// twoWordTest reports whether first+second forms one of Quill's known two-word
// test names; this keeps "is divisible by 3" greedy while "is defined" stays one
// word and "is not empty" is handled by the negation branch.
func twoWordTest(first, second string) bool {
	switch first {
	case "same":
		return second == "as"
	case "divisible":
		return second == "by"
	}
	return false
}

// startsTestArg reports whether the cursor begins a one-positional test argument
// (a bare Primary). It excludes tokens that begin a new infix operator or close
// the surrounding context.
func (p *parser) startsTestArg() bool {
	switch p.cur().Kind {
	case lex.INT, lex.FLOAT, lex.STRING, lex.TRUE, lex.FALSE, lex.NULL,
		lex.LPAREN, lex.LBRACKET, lex.LBRACE:
		return true
	case lex.NAME:
		// A NAME that is an infix word-operator does not begin an argument.
		switch p.cur().Text {
		case "and", "or", "xor", "in", "is", "matches", "starts", "ends",
			"has", "not", "b_and", "b_or", "b_xor":
			return false
		}
		return true
	}
	return false
}

// parseRange is level 11: a .. b, NON-ASSOCIATIVE.
func (p *parser) parseRange() *ast.Node {
	left := p.parseConcat()
	if p.at(lex.RANGE) {
		t := p.advance()
		left = p.binary(t, "..", left, p.parseConcat())
		if p.at(lex.RANGE) {
			p.fail("range operator '..' is non-associative")
		}
	}
	return left
}

// parseConcat is level 12: a ~ b, left-associative.
func (p *parser) parseConcat() *ast.Node {
	left := p.parseAdd()
	for p.at(lex.TILDE) {
		t := p.advance()
		left = p.binary(t, "~", left, p.parseAdd())
	}
	return left
}

// parseAdd is level 13: + -, left-associative.
func (p *parser) parseAdd() *ast.Node {
	left := p.parseMul()
	for p.at(lex.PLUS) || p.at(lex.MINUS) {
		t := p.advance()
		op := "+"
		if t.Kind == lex.MINUS {
			op = "-"
		}
		left = p.binary(t, op, left, p.parseMul())
	}
	return left
}

// parseMul is level 14: * / // %, left-associative.
func (p *parser) parseMul() *ast.Node {
	left := p.parsePower()
	for {
		var op string
		switch p.cur().Kind {
		case lex.STAR:
			op = "*"
		case lex.SLASH:
			op = "/"
		case lex.FLOORDIV:
			op = "//"
		case lex.PERCENT:
			op = "%"
		default:
			return left
		}
		t := p.advance()
		left = p.binary(t, op, left, p.parsePower())
	}
}

// parsePower is level 15: a ** b, RIGHT-associative, with the right operand
// re-entering at the unary (prefix) level so 2 ** -3 works, and the whole power
// being a possible operand of a unary minus so -1 ** 0 == -(1 ** 0) (spec 02 R6,
// design/expressions Section 3). There is no special case: the AST shape is the
// rule.
//
//	Power = Unary [ "**" Power ] .
func (p *parser) parsePower() *ast.Node {
	base := p.parseUnary()
	if p.at(lex.POW) {
		t := p.advance()
		exp := p.parsePower() // right-assoc; RHS re-enters via Power -> Unary
		return p.node(ast.KindPower, t, base, exp)
	}
	return base
}

// parseUnary is level 16: prefix not / ! / - / + / ..., RIGHT-associative.
//
//	Unary = ( "not" | "!" | "-" | "+" | "..." ) Unary | Postfix .
//
// Crucially, parseUnary recurses on parseUnary (not parsePower) for its operand,
// so "-1 ** 0" parses the unary minus FIRST as the outer node and then... no:
// because parsePower calls parseUnary for its base, "-1 ** 0" is seen by
// parsePower as base = parseUnary() = -(1)? No -- parseUnary's operand recursion
// would greedily eat the "1 ** 0". The published grammar resolves this exactly:
// Power -> Unary on the LEFT means a power's base is a Unary; for "-1 ** 0",
// parseMul -> parsePower -> parseUnary sees the leading "-", and a unary operand
// recurses into parseUnary, whose Postfix is "1"; control returns to parsePower
// which then sees "**" and binds the exponent. The minus therefore wraps the
// whole power. See parseUnary's operand handling below.
func (p *parser) parseUnary() *ast.Node {
	switch {
	case p.isNameWord("not"):
		t := p.advance()
		return p.unary(t, "not", p.parseUnary())
	case p.at(lex.BANG):
		t := p.advance()
		return p.unary(t, "not", p.parseUnary())
	case p.at(lex.MINUS):
		t := p.advance()
		// The operand is a full power chain so "-1 ** 0" binds as -(1 ** 0): the
		// minus wraps the completed power. Recursing into parsePower (not parseUnary)
		// gives the right operand the "** Power" tail, producing the AST-driven fix
		// of spec 02 R6 with no special case.
		return p.unary(t, "-", p.parsePower())
	case p.at(lex.PLUS):
		t := p.advance()
		return p.unary(t, "+", p.parsePower())
	case p.at(lex.SPREAD):
		t := p.advance()
		return p.node(ast.KindSpread, t, p.parseUnary())
	}
	return p.parsePostfix()
}

// binary builds a KindBinary node with the canonical operator spelling.
func (p *parser) binary(t lex.Token, op string, l, r *ast.Node) *ast.Node {
	n := p.node(ast.KindBinary, t, l, r)
	n.Str = op
	return n
}

// unary builds a KindUnary node with the canonical operator spelling.
func (p *parser) unary(t lex.Token, op string, operand *ast.Node) *ast.Node {
	n := p.node(ast.KindUnary, t, operand)
	n.Str = op
	return n
}
