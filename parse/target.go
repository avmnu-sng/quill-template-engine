package parse

import (
	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/lex"
)

// parseSeqPattern parses a sequence-destructuring TARGET directly from tokens --
// a dedicated grammar distinct from the expression-level sequence literal
// (parseSeq), so a trailing "?" marks an optional slot rather than opening a
// ternary, and a bare comma marks an elided slot rather than being a syntax error
// (spec 01 Section 2.1, grammar.md Section 3.2 O32). It is the entry point for the
// "[" form of an @set target and recurses for nested "[" / "{" slots.
//
//	SeqDestruct = "[" [ Head { "," Opt } [ "," Tail ] ] "]" .
//	Head        = Req | Opt        (* required/elided slots precede the optionals *)
//	Req         = Target | (* empty: elided *) .
//	Opt         = Target "?" .
//	Tail        = "..." Name .
//	Target      = Name [ ":" Type ] | SeqDestruct | MapDestruct .
//
// Once an Opt appears, only further Opt and a final Tail may follow (optionals are
// trailing); this is enforced in the loop below, not expressible in the EBNF above
// without duplicating the slot productions.
//
// A "...rest" tail capture, if present, must be the last slot (enforced here so
// the interpreter can rely on its position). An elided slot is recorded as a nil
// child; an optional slot is wrapped in a KindOptional; the existing arity rule
// for required slots is enforced by the interpreter.
//
// Optionals must be trailing: once an optional slot appears, only further optionals
// and a final "...rest" may follow. A required or elided slot after an optional is a
// syntax error -- it would make arity ambiguous (the count guard cannot tell which
// positions are mandatory) and, with positional binding, force the binder past a
// short source. The spec's optional examples are all trailing ("[a, b?]",
// "[head, opt?, ...rest]"), so this only rejects forms the grammar never intended.
func (p *parser) parseSeqPattern() *ast.Node {
	open := p.expect(lex.LBRACKET, "'[' to open a destructuring pattern")
	pat := p.node(ast.KindListPattern, open)
	sawOptional := false
	for !p.at(lex.RBRACKET) && !p.at(lex.EOF) {
		// A tail capture "...name" is legal only as the final slot; parseSeqSlot
		// returns it as a KindSpread and we forbid anything following it.
		slot := p.parseSeqSlot()
		switch {
		case slot == nil: // elided slot
			if sawOptional {
				p.fail("an elided slot cannot follow an optional slot; optionals must be trailing")
			}
		case slot.Kind == ast.KindSpread:
			// A tail may follow optionals; its last-position rule is checked below.
		case slot.Kind == ast.KindOptional:
			sawOptional = true
		default: // required slot: a name target or a nested pattern
			if sawOptional {
				p.fail("a required slot cannot follow an optional slot; optionals must be trailing")
			}
		}
		pat.Add(slot)
		if slot != nil && slot.Kind == ast.KindSpread && !p.at(lex.RBRACKET) {
			p.fail("a tail capture '...name' must be the last slot")
		}
		if !p.accept(lex.COMMA) {
			break
		}
	}
	p.expect(lex.RBRACKET, "']' to close a destructuring pattern")
	return pat
}

// parseSeqSlot parses one slot of a sequence-destructuring pattern. An empty
// position (the cursor is at "," or "]") is an elided slot, returned as a nil
// node so the interpreter advances past the source element without binding. A
// "...name" is a tail capture (KindSpread). Otherwise the slot is a target -- a
// nested "[" / "{" pattern or a (optionally typed) name -- which a trailing "?"
// wraps in a KindOptional to mark it null-paddable when the source is short.
func (p *parser) parseSeqSlot() *ast.Node {
	// Elided slot: an empty position between commas, or a leading "[, b]".
	if p.at(lex.COMMA) || p.at(lex.RBRACKET) {
		return nil
	}
	if p.at(lex.SPREAD) {
		t := p.advance()
		name := p.expect(lex.NAME, "a name after '...' in a tail capture")
		nameNode := p.node(ast.KindName, name)
		nameNode.Str = name.Text
		return p.node(ast.KindSpread, t, nameNode)
	}
	tgt := p.parseTargetSlot()
	if p.accept(lex.QUESTION) {
		return p.node(ast.KindOptional, tokAt(tgt), tgt)
	}
	return tgt
}

// parseTargetSlot parses a single non-spread, non-elided destructuring target: a
// nested sequence "[" or map "{" pattern, or a name with an optional type
// annotation. Nested map patterns reuse the existing mapToPattern conversion (the
// map grammar has no "?"/elided slots, grammar.md Section 3.2 MapDSlot), so a
// nested "{...}" is parsed by the expression-level parseMap and reinterpreted.
func (p *parser) parseTargetSlot() *ast.Node {
	switch p.cur().Kind {
	case lex.LBRACKET:
		return p.parseSeqPattern()
	case lex.LBRACE:
		return p.mapToPattern(p.parseMap())
	case lex.NAME:
		nameTok := p.advance()
		tgt := p.node(ast.KindTarget, nameTok)
		tgt.Str = nameTok.Text
		if p.accept(lex.COLON) {
			tgt.Add(p.parseType())
		}
		return tgt
	}
	p.fail("expected a destructuring target, found %s", describe(p.cur()))
	return nil
}

// toTarget reinterprets an already-parsed expression as an assignment /
// destructuring target (spec 02 R10, design/expressions Section 9). The LHS of
// "=" is parsed as an Expr and converted here, so a sequence literal "[a, b]"
// becomes a list pattern and a mapping literal "{name}" becomes a map pattern.
// This expression-form path covers the flat, nested, and "...rest" forms reachable
// through the expression grammar; the optional ("[a, b?]") and elided ("[, b]")
// slot forms are parsed by the dedicated parseSeqPattern target grammar at the
// @set LHS, because a trailing "?" and a bare-comma elision cannot be expressed by
// the expression parser.
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
// becomes a slot target; a trailing KindSpread element becomes a tail capture.
//
// This is the expression-form path: a sequence literal "[a, b]" reinterpreted as a
// target after the expression parser ran (e.g. "[a, b] = e" inside an
// interpolation). It supports names, nested list/map patterns, and "...name". The
// optional ("[a, b?]") and elided ("[, b]") slot forms are NOT reachable here --
// the expression parser reads a trailing "?" as a ternary and cannot express a
// bare-comma elision -- so those forms are parsed instead by the dedicated
// parseSeqPattern target grammar at the @set LHS (spec 01 Section 2.1).
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
