package lex

import "strings"

// scanVerbatim handles "@verbatim" in its two forms (spec 01 Section 1.6 / spec
// 06): brace-balanced (@verbatim { ... @}) and fenced (@verbatim FENCE ... FENCE).
// The body is copied byte-for-byte into a VERBATIM token and is NEVER scanned for
// any Quill syntax. It emits STMT("verbatim") for symmetry with other statements,
// then the VERBATIM body, then (for the brace form) BLOCK_CLOSE.
func (l *lexer) scanVerbatim() (stop bool) {
	line, col := l.line, l.col
	l.advance() // '@'
	for range "verbatim" {
		l.advance()
	}
	l.emit(Token{Kind: STMT, Text: "verbatim", Line: line, Col: col})

	// Skip horizontal whitespace after the keyword.
	for l.pos < len(l.in) && (l.in[l.pos] == ' ' || l.in[l.pos] == '\t') {
		l.advance()
	}

	if l.pos < len(l.in) && l.in[l.pos] == '{' {
		return l.scanVerbatimBraced()
	}
	return l.scanVerbatimFenced(line, col)
}

// scanVerbatimBraced reads a brace-balanced verbatim body. Inner '{' and '}' are
// tracked by a raw depth counter that does NOT special-case "{{" (spec 06.1); the
// region ends at the '}' of an "@}" that balances the opening '{'. The opening '{'
// is consumed as the depth-1 marker; the closing "@}" emits BLOCK_CLOSE.
func (l *lexer) scanVerbatimBraced() (stop bool) {
	bLine, bCol := l.line, l.col
	l.advance() // opening '{' (depth becomes 1)
	tr := l.takeCloseTrim()
	l.emit(Token{Kind: BLOCK_OPEN, Line: bLine, Col: bCol, TrimR: tr})
	if tr != TrimKeep {
		l.eatOneNewline()
	}

	bodyLine, bodyCol := l.line, l.col
	var b strings.Builder
	depth := 1
	for l.pos < len(l.in) {
		// An "@}" at depth 1 closes the verbatim region.
		if depth == 1 && l.in[l.pos] == '@' && l.pos+1 < len(l.in) && l.in[l.pos+1] == '}' {
			l.emit(Token{Kind: VERBATIM, Text: b.String(), Line: bodyLine, Col: bodyCol})
			cLine, cCol := l.line, l.col
			l.advance() // '@'
			l.advance() // '}'
			ctr := l.takeCloseTrim()
			l.emit(Token{Kind: BLOCK_CLOSE, Line: cLine, Col: cCol, TrimR: ctr})
			if ctr != TrimKeep {
				l.eatOneNewline()
			}
			l.atLineStart = true
			return false
		}
		switch l.in[l.pos] {
		case '{':
			depth++
		case '}':
			depth--
		}
		b.WriteByte(l.in[l.pos])
		l.advance()
	}
	l.errorf(bLine, bCol, "unterminated verbatim: %q opened but no matching %q", "@verbatim {", "@}")
	return true
}

// scanVerbatimFenced reads a fenced verbatim body (the heredoc model, spec 06.2).
// The fence token is the rest of the introducer line after "@verbatim "; the
// region runs from the next line to the first line whose content equals the fence
// token. The fence lines themselves are not part of the body.
func (l *lexer) scanVerbatimFenced(openLine, openCol int) (stop bool) {
	// Read the fence token to end of line.
	fenceStart := l.pos
	for l.pos < len(l.in) && l.in[l.pos] != '\n' {
		l.advance()
	}
	fence := strings.TrimRight(l.in[fenceStart:l.pos], " \t\r")
	if fence == "" {
		l.errorf(openLine, openCol, "verbatim requires a brace body or a fence token")
		return true
	}
	l.eatOneNewline() // consume the introducer line's newline

	bodyLine, bodyCol := l.line, l.col
	var b strings.Builder
	for l.pos < len(l.in) {
		lineStart := l.pos
		// Read one line.
		for l.pos < len(l.in) && l.in[l.pos] != '\n' {
			l.advance()
		}
		raw := l.in[lineStart:l.pos]
		if strings.TrimRight(raw, " \t\r") == fence {
			// Closing fence line; not part of the body.
			l.eatOneNewline()
			l.emit(Token{Kind: VERBATIM, Text: b.String(), Line: bodyLine, Col: bodyCol})
			l.atLineStart = true
			return false
		}
		b.WriteString(raw)
		if l.pos < len(l.in) { // a newline follows
			b.WriteByte('\n')
			l.advance()
		}
	}
	l.errorf(openLine, openCol, "unterminated fenced verbatim: fence %q never closed", fence)
	return true
}
