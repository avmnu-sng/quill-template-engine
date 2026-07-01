package parse

import (
	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/lex"
)

// parseIf parses "@if cond { body @} elseif cond { body @} else { body @}"
// (design/control-flow Section 2). The lexer emits one BLOCK_CLOSE per branch
// body followed by the next continuation head (lex/doors.go scanContinuation), so
// each clause body ends at its own '@}'. KindIf's children are KindClause branches:
// each if/elseif clause carries a condition child then body items; a final else
// clause carries only body items (Bool=false).
func (p *parser) parseIf() *ast.Node {
	t := p.expectStmt("if")
	ifNode := p.node(ast.KindIf, t)

	// if clause
	cond := p.parseExpr()
	p.openBody()
	clause := p.node(ast.KindClause, t, cond)
	clause.Bool = true
	clause.Children = append(clause.Children, p.parseBodyItems()...)
	p.closeBlock()
	ifNode.Add(clause)

	// elseif clauses
	for p.at(lex.STMT) && p.cur().Text == "elseif" {
		et := p.advance()
		ec := p.parseExpr()
		p.openBody()
		c := p.node(ast.KindClause, et, ec)
		c.Bool = true
		c.Children = append(c.Children, p.parseBodyItems()...)
		p.closeBlock()
		ifNode.Add(c)
	}

	// else clause
	if p.at(lex.STMT) && p.cur().Text == "else" {
		et := p.advance()
		p.openBody()
		c := p.node(ast.KindClause, et)
		c.Bool = false
		c.Children = append(c.Children, p.parseBodyItems()...)
		p.closeBlock()
		ifNode.Add(c)
	}

	return ifNode
}

// parseFor parses
// "@for t1 [, t2] in iterand [ if cond ] { body } [ @else { body } ] @}"
// (design/control-flow Section 3). Children order: target1, [target2], iterand,
// [filter (KindClause, Bool=true, child 0 is the condition)], body (KindBody),
// [else body (KindBody)]. Int is the target count; Bool marks an else branch.
//
// The optional "if cond" clause between the iterand and the body brace is a
// FUSED loop filter: the iterand is pre-filtered to the elements for which cond
// is truthy, and every loop.* field reflects only the survivors. The condition
// may reference the loop target(s), so it is evaluated per element with the
// targets bound (interp/exec.go execFor).
func (p *parser) parseFor() *ast.Node {
	t := p.expectStmt("for")
	forNode := p.node(ast.KindFor, t)

	t1 := p.parseForTarget()
	forNode.Add(t1)
	count := int64(1)
	if p.accept(lex.COMMA) {
		forNode.Add(p.parseForTarget())
		count = 2
	}
	if !p.isNameWord("in") {
		p.fail("expected 'in' in a for loop, found %s", describe(p.cur()))
	}
	p.advance() // in
	forNode.Add(p.parseExpr())
	forNode.Int = count

	// Optional fused filter clause "if cond" before the body brace. It is a
	// KindClause with Bool=true carrying the condition as child 0, mirroring the
	// if-clause shape so the interpreter reads it uniformly.
	if p.isNameWord("if") {
		ifTok := p.advance()
		filter := p.node(ast.KindClause, ifTok, p.parseExpr())
		filter.Bool = true
		forNode.Add(filter)
	}

	p.openBody()
	body := p.node(ast.KindBody, t)
	body.Children = append(body.Children, p.parseBodyItems()...)
	p.closeBlock()
	forNode.Add(body)

	if p.at(lex.STMT) && p.cur().Text == "else" {
		et := p.advance()
		p.openBody()
		els := p.node(ast.KindBody, et)
		els.Children = append(els.Children, p.parseBodyItems()...)
		p.closeBlock()
		forNode.Add(els)
		forNode.Bool = true
	}
	return forNode
}

// parseForTarget parses a loop target "name [: Type]".
func (p *parser) parseForTarget() *ast.Node {
	if !p.at(lex.NAME) {
		p.fail("expected a loop variable name, found %s", describe(p.cur()))
	}
	nameTok := p.advance()
	tgt := p.node(ast.KindTarget, nameTok)
	tgt.Str = nameTok.Text
	if p.accept(lex.COLON) {
		tgt.Add(p.parseType())
	}
	return tgt
}

// parseSet parses "@set" in three shapes (design/control-flow Sections 4, 5):
//   - capture:  @set NAME [: Type] = capture { body @}
//   - multi:    @set t1, t2 = e1, e2 NL
//   - single:   @set t = e NL  (t may be a destructuring pattern)
func (p *parser) parseSet() *ast.Node {
	t := p.expectStmt("set")

	// Parse the first target. A leading "[" or "{" is a destructuring pattern; a
	// NAME may carry a type annotation (which a destructuring slot may not).
	first, firstName, firstTyped := p.parseSetTarget()

	// Capture form: a single typed-or-untyped NAME target, "=", then "capture {".
	if firstName != "" && p.at(lex.ASSIGN) && p.isNameWordAt(1, "capture") {
		p.advance() // '='
		p.advance() // 'capture'
		cap := p.node(ast.KindCapture, t)
		cap.Str = firstName
		if firstTyped != nil {
			cap.Add(firstTyped)
		}
		p.openBody()
		cap.Children = append(cap.Children, p.parseBodyItems()...)
		p.closeBlock()
		return cap
	}

	setNode := p.node(ast.KindSet, t)
	targets := []*ast.Node{first}
	for p.accept(lex.COMMA) {
		tg, _, _ := p.parseSetTarget()
		targets = append(targets, tg)
	}
	p.expect(lex.ASSIGN, "'=' in a set statement")
	values := []*ast.Node{p.parseExpr()}
	for p.accept(lex.COMMA) {
		values = append(values, p.parseExpr())
	}
	// Count rule: multi-target set requires matching counts; a single target may
	// be a destructuring pattern bound to a single value (design/control-flow 4).
	if len(targets) > 1 && len(targets) != len(values) {
		p.failAt(t, "set has %d targets but %d values", len(targets), len(values))
	}
	setNode.Int = int64(len(targets))
	for _, tg := range targets {
		setNode.Add(tg)
	}
	for _, v := range values {
		setNode.Add(v)
	}
	p.endLine()
	return setNode
}

// parseSetTarget parses one set target. It returns the target node and, when the
// target is a plain (optionally typed) name, the bare name and its type node (for
// the capture-form lookahead). A destructuring pattern returns an empty name.
func (p *parser) parseSetTarget() (node *ast.Node, name string, typ *ast.Node) {
	switch p.cur().Kind {
	case lex.LBRACKET:
		// A sequence target uses the dedicated pattern grammar (parseSeqPattern), not
		// the expression-level parseSeq, so optional "b?" and elided "[, b]" slots
		// parse as targets rather than as a ternary or a syntax error.
		return p.parseSeqPattern(), "", nil
	case lex.LBRACE:
		return p.toTarget(p.parseMap()), "", nil
	case lex.NAME:
		nameTok := p.advance()
		// A member target -- NAME.member or NAME[key], possibly chained -- assigns
		// through a receiver rather than binding a plain name (the mutable-cell
		// form, @set c.value = expr). It is not a bindable name, so it reports an
		// empty name to the capture-form lookahead.
		if p.at(lex.DOT) || p.at(lex.LBRACKET) {
			return p.parseMemberTarget(nameTok), "", nil
		}
		tgt := p.node(ast.KindTarget, nameTok)
		tgt.Str = nameTok.Text
		if p.accept(lex.COLON) {
			ty := p.parseType()
			tgt.Add(ty)
			return tgt, nameTok.Text, ty
		}
		return tgt, nameTok.Text, nil
	}
	p.fail("expected a set target, found %s", describe(p.cur()))
	return nil, "", nil
}

// parseMemberTarget builds a member-assignment target from a leading NAME already
// consumed as nameTok, followed by one or more ".member" / "[key]" steps. The
// result is a KindAttr / KindIndex chain rooted at a KindName, the same shape a
// member READ produces, so the interpreter can evaluate the receiver and assign
// the final member. Only the dotted and subscript forms are targets here; a
// null-safe "?." or a call is not an assignable place.
func (p *parser) parseMemberTarget(nameTok lex.Token) *ast.Node {
	recv := p.node(ast.KindName, nameTok)
	recv.Str = nameTok.Text
	for {
		switch p.cur().Kind {
		case lex.DOT:
			t := p.advance()
			name := p.memberName()
			n := p.node(ast.KindAttr, t, recv)
			n.Str = name
			recv = n
		case lex.LBRACKET:
			t := p.advance()
			key := p.parseExpr()
			p.expect(lex.RBRACKET, "']' to close a member-assignment index")
			n := p.node(ast.KindIndex, t, recv, key)
			recv = n
		default:
			return recv
		}
	}
}

// parseWith parses "@with map [only] { body } @}" (design/control-flow Section 8).
func (p *parser) parseWith() *ast.Node {
	t := p.expectStmt("with")
	w := p.node(ast.KindWith, t, p.parseExpr())
	if p.isNameWord("only") {
		p.advance()
		w.Bool = true
	}
	p.openBody()
	w.Children = append(w.Children, p.parseBodyItems()...)
	p.closeBlock()
	return w
}

// parseApply parses "@apply | f | g { body } @}" (design/control-flow Section 9).
// The filters precede the body items; Int records the filter count.
func (p *parser) parseApply() *ast.Node {
	t := p.expectStmt("apply")
	a := p.node(ast.KindApply, t)
	count := int64(0)
	for p.at(lex.PIPE) {
		ft := p.advance() // '|'
		if !p.at(lex.NAME) {
			p.fail("expected a filter name after '|' in apply, found %s", describe(p.cur()))
		}
		name := p.advance().Text
		f := p.node(ast.KindApplyFilter, ft)
		f.Str = name
		if p.at(lex.LPAREN) {
			p.advance()
			p.parseArgsInto(f)
			p.expect(lex.RPAREN, "')' to close apply filter arguments")
		}
		a.Add(f)
		count++
	}
	if count == 0 {
		p.fail("apply requires at least one '| filter'")
	}
	a.Int = count
	p.openBody()
	a.Children = append(a.Children, p.parseBodyItems()...)
	p.closeBlock()
	return a
}

// parseDo parses "@do expr NL" (design/control-flow Section 6.1).
func (p *parser) parseDo() *ast.Node {
	t := p.expectStmt("do")
	n := p.node(ast.KindDo, t, p.parseExpr())
	p.endLine()
	return n
}

// parseFlush parses "@flush NL".
func (p *parser) parseFlush() *ast.Node {
	t := p.expectStmt("flush")
	n := p.node(ast.KindFlush, t)
	p.endLine()
	return n
}

// parseDeprecated parses `@deprecated "msg" [since "v"] NL`.
func (p *parser) parseDeprecated() *ast.Node {
	t := p.expectStmt("deprecated")
	msgTok := p.expect(lex.STRING, "a quoted deprecation message")
	msg, err := decodeString(msgTok)
	if err != nil {
		p.failAt(msgTok, "%s", err.Error())
	}
	n := p.node(ast.KindDeprecated, t)
	n.Str = msg
	if p.isNameWord("since") {
		p.advance()
		vTok := p.expect(lex.STRING, "a quoted version after 'since'")
		v, err := decodeString(vTok)
		if err != nil {
			p.failAt(vTok, "%s", err.Error())
		}
		ver := p.node(ast.KindString, vTok)
		ver.Str = v
		n.Add(ver)
		n.Bool = true
	}
	p.endLine()
	return n
}

// parseGuard parses `@guard kind("name") { body } [ @else { body } ] @}`
// (design/control-flow Section 10.1).
func (p *parser) parseGuard() *ast.Node {
	t := p.expectStmt("guard")
	if !p.at(lex.NAME) {
		p.fail("expected 'filter', 'function', or 'test' after 'guard'")
	}
	kind := p.advance().Text
	switch kind {
	case "filter", "function", "test":
	default:
		p.failAt(t, "guard kind must be 'filter', 'function', or 'test', found %q", kind)
	}
	p.expect(lex.LPAREN, "'(' after a guard kind")
	nameTok := p.expect(lex.STRING, "a quoted callable name in guard(...)")
	name, err := decodeString(nameTok)
	if err != nil {
		p.failAt(nameTok, "%s", err.Error())
	}
	p.expect(lex.RPAREN, "')' to close guard(...)")

	g := p.node(ast.KindGuard, t)
	g.Str = kind
	nameNode := p.node(ast.KindString, nameTok)
	nameNode.Str = name
	g.Add(nameNode)

	p.openBody()
	g.Children = append(g.Children, p.parseBodyItems()...)
	p.closeBlock()

	if p.at(lex.STMT) && p.cur().Text == "else" {
		et := p.advance()
		p.openBody()
		els := p.node(ast.KindClause, et)
		els.Children = append(els.Children, p.parseBodyItems()...)
		p.closeBlock()
		g.Add(els)
	}
	return g
}

// parseTypes parses "@types { name: T, ... } @}" (design/control-flow 4.6).
func (p *parser) parseTypes() *ast.Node {
	t := p.expectStmt("types")
	p.openBody()
	n := p.node(ast.KindTypes, t)
	for !p.at(lex.BLOCK_CLOSE) && !p.at(lex.EOF) {
		// A @types body is CODE-shaped, but the lexer delivers it as the head's
		// tokens up to BLOCK_OPEN, then the declarations as... the body is TEXT under
		// the lexer's brace-body rule. The types block is special: its body holds
		// declarations, not text. We parse declarations from the token stream which,
		// inside a brace body, arrive as TEXT. To keep this slice focused, @types
		// declarations are parsed from a single TEXT item if present.
		if p.at(lex.TEXT) {
			p.parseTypeDeclsFromText(n, p.advance())
			continue
		}
		p.fail("expected type declarations in a @types block")
	}
	p.closeBlock()
	return n
}

// parseTypeDeclsFromText parses a "@types { ... }" body. Because a brace body is
// TEXT under the lexer's @-default rule, the declarations arrive as one TEXT
// token; we lex that fragment as CODE and read "name: Type [,]" declarations
// (spec 02 TypeDecl). Faults are anchored to the @types head's line.
func (p *parser) parseTypeDeclsFromText(into *ast.Node, text lex.Token) {
	sub := &parser{src: p.src, toks: codeTokens(p, text, text.Text, "in @types block")}
	defer func() {
		if r := recover(); r != nil {
			if se, ok := r.(*errors.Error); ok {
				panic(se)
			}
			p.failAt(text, "in @types block: malformed type declaration")
		}
	}()
	for !sub.at(lex.EOF) {
		if !sub.at(lex.NAME) {
			p.failAt(text, "expected a name in a @types declaration")
		}
		nameTok := sub.advance()
		sub.expect(lex.COLON, "':' after a @types name")
		ty := sub.parseType()
		decl := p.node(ast.KindTypeDecl, text)
		decl.Str = nameTok.Text
		decl.Add(ty)
		into.Add(decl)
		sub.accept(lex.COMMA)
	}
}

// parseEscape parses "@escape strategy { body } @}".
func (p *parser) parseEscape() *ast.Node {
	t := p.expectStmt("escape")
	if !p.at(lex.NAME) {
		p.fail("expected an escape strategy or 'off' after 'escape'")
	}
	n := p.node(ast.KindEscape, t)
	n.Str = p.advance().Text
	p.openBody()
	n.Children = append(n.Children, p.parseBodyItems()...)
	p.closeBlock()
	return n
}

// parseSandbox parses "@sandbox { body } @}".
func (p *parser) parseSandbox() *ast.Node {
	t := p.expectStmt("sandbox")
	n := p.node(ast.KindSandbox, t)
	p.openBody()
	n.Children = append(n.Children, p.parseBodyItems()...)
	p.closeBlock()
	return n
}

// parseLog parses "@log expr NL". The expression is evaluated for its side
// effect on the host logger; it produces no rendered output.
func (p *parser) parseLog() *ast.Node {
	t := p.expectStmt("log")
	n := p.node(ast.KindLog, t, p.parseExpr())
	p.endLine()
	return n
}

// parseTabBlock parses "@tab(n) { body } @}". The level expression is written in
// parentheses after the keyword; the braced body follows. The whole body is
// indented by n levels at render time via the output layer's indent stack.
func (p *parser) parseTabBlock() *ast.Node {
	t := p.expectStmt("tab")
	p.expect(lex.LPAREN, "'(' with the indent level after 'tab'")
	level := p.parseExpr()
	p.expect(lex.RPAREN, "')' to close the tab level")
	n := p.node(ast.KindTabBlock, t, level)
	p.openBody()
	n.Children = append(n.Children, p.parseBodyItems()...)
	p.closeBlock()
	return n
}

// parseLine parses "@line N NL".
func (p *parser) parseLine() *ast.Node {
	t := p.expectStmt("line")
	numTok := p.expect(lex.INT, "a line number after 'line'")
	n := p.node(ast.KindLine, t)
	n.Int = parseIntLit(p, numTok)
	p.endLine()
	return n
}

// parseCache parses "@cache name=expr ... { body } @}" (design/control-flow 10.6).
func (p *parser) parseCache() *ast.Node {
	t := p.expectStmt("cache")
	c := p.node(ast.KindCache, t)
	count := int64(0)
	for p.at(lex.NAME) {
		nameTok := p.advance()
		p.expect(lex.ASSIGN, "'=' after a cache argument name")
		arg := p.node(ast.KindCacheArg, nameTok, p.parseExpr())
		arg.Str = nameTok.Text
		c.Add(arg)
		count++
	}
	c.Int = count
	p.openBody()
	c.Children = append(c.Children, p.parseBodyItems()...)
	p.closeBlock()
	return c
}

// parseVerbatim folds the lexer's STMT("verbatim")/BLOCK_OPEN/VERBATIM/BLOCK_CLOSE
// (or the fenced STMT/VERBATIM) token shape into a single KindVerbatim node whose
// Str is the literal body.
func (p *parser) parseVerbatim() *ast.Node {
	t := p.expectStmt("verbatim")
	// Brace form: BLOCK_OPEN, VERBATIM, BLOCK_CLOSE. Fenced form: VERBATIM only.
	p.accept(lex.BLOCK_OPEN)
	body := ""
	if p.at(lex.VERBATIM) {
		body = p.advance().Text
	}
	p.accept(lex.BLOCK_CLOSE)
	n := p.node(ast.KindVerbatim, t)
	n.Str = body
	return n
}
