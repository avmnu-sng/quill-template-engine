package lex

import "fmt"

// sprintf is a tiny indirection so lexer.go does not import fmt directly; keeps
// the error-formatting call site uniform.
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// peekStatement reports whether the cursor (at line start, on an '@') begins an
// @-led statement keyword at a word boundary (spec 01 Section 1.3 condition 1-2).
// It does NOT verify the head parses (that is the parser's grammar-shape job); the
// lexer only opens the door on @ + keyword + word boundary. Returns the keyword.
func (l *lexer) peekStatement() (kw string, ok bool) {
	// l.in[l.pos] == '@'
	i := l.pos + 1
	start := i
	for i < len(l.in) && isWordByte(l.in[i]) {
		i++
	}
	word := l.in[start:i]
	if !statementKeywords[word] {
		return "", false
	}
	// Word boundary: the keyword must not run straight into more identifier
	// bytes. isWordByte already stopped at the boundary, so this holds by
	// construction; the check guards an empty match ("@" alone).
	if word == "" {
		return "", false
	}
	return word, true
}

// peekBlockClose reports whether the cursor (at line start, on an '@') begins an
// "@}" block close whose line has no other non-whitespace content (spec 01
// Section 1.3: a line whose only non-whitespace content is @}). A trailing trim
// modifier (-/~/+) is permitted between '}' and the newline.
func (l *lexer) peekBlockClose() bool {
	if l.pos+1 >= len(l.in) || l.in[l.pos+1] != '}' {
		return false
	}
	i := l.pos + 2
	// optional close-side trim modifier
	if i < len(l.in) && isCloseTrim(l.in[i]) {
		i++
	}
	// rest of line must be whitespace up to newline or EOF
	for i < len(l.in) {
		c := l.in[i]
		if c == '\n' {
			return true
		}
		if c == ' ' || c == '\t' || c == '\r' {
			i++
			continue
		}
		return false
	}
	return true // EOF after @}
}

// scanInterp handles the "{{" ... "}}" door. It emits OPEN_INTERP (with opening
// trim), the CODE tokens of the expression, then CLOSE_INTERP (with closing
// trim). The closer "}}" is recognized only at bracket depth zero (spec 02 R3).
// An interpolation's "}}" eats NO following newline (spec 02 R5).
func (l *lexer) scanInterp() {
	openLine, openCol := l.line, l.col
	l.advance() // first '{'
	l.advance() // second '{'
	tl := l.takeOpenTrim()
	l.emit(Token{Kind: OPEN_INTERP, Line: openLine, Col: openCol, TrimL: tl})

	if l.scanCode(scanInterpClose) {
		return // ERROR already emitted
	}
	// On return, cursor is just before a depth-zero "}}" (possibly after a trim).
	tr := l.takeCloseTrim()
	closeLine, closeCol := l.line, l.col
	l.advance() // first '}'
	l.advance() // second '}'
	l.emit(Token{Kind: CLOSE_INTERP, Line: closeLine, Col: closeCol, TrimR: tr})
	l.atLineStart = false
}

// scanComment handles "{#" ... "#}". The whole span is consumed and emits no
// token. An unterminated "{#" is a lex error at the opener (spec 01 Section 1.5).
// The "#}" closer eats exactly one immediately-following newline (spec 02 R5),
// unless a '+' keep modifier precedes the '}'.
func (l *lexer) scanComment() (stop bool) {
	openLine, openCol := l.line, l.col
	l.advance() // '{'
	l.advance() // '#'
	for l.pos < len(l.in) {
		if l.in[l.pos] == '#' && l.pos+1 < len(l.in) && l.in[l.pos+1] == '}' {
			l.advance() // '#'
			l.advance() // '}'
			l.eatOneNewline()
			l.atLineStart = true
			return false
		}
		// allow a '+' keep modifier as "#}" -> recognized above; a '+' just
		// before "#}" is handled by checking it here.
		if l.in[l.pos] == '+' && l.pos+2 < len(l.in) && l.in[l.pos+1] == '#' && l.in[l.pos+2] == '}' {
			// "+#}" is not the keep form; keep form is "#}+". Fall through.
		}
		l.advance()
	}
	l.errorf(openLine, openCol, "unterminated comment: %q opened but no matching %q", "{#", "#}")
	return true
}

// scanBlockClose handles "@}" at line start. It emits BLOCK_CLOSE with any close
// trim modifier and eats one following newline (spec 02 R5), unless the modifier
// is '+' (keep).
func (l *lexer) scanBlockClose() {
	line, col := l.line, l.col
	l.advance() // '@'
	l.advance() // '}'
	tr := l.takeCloseTrim()
	l.emit(Token{Kind: BLOCK_CLOSE, Line: line, Col: col, TrimR: tr})
	if tr != TrimKeep {
		l.eatOneNewline()
	}
	l.atLineStart = true
}

// scanStatement handles an @-led statement head (not verbatim). It emits the STMT
// token (keyword without '@'), then scans CODE head tokens. The head ends either
// at a depth-zero '{' (block-bodied: emit BLOCK_OPEN, body is TEXT) or at a
// newline / EOF (line statement: emit STMT_END). The closing '{' may carry an
// opening-side trim modifier on its inner edge ("{-", "{~").
func (l *lexer) scanStatement(kw string) {
	line, col := l.line, l.col
	l.advance() // '@'
	for range kw {
		l.advance() // keyword bytes
	}
	l.emit(Token{Kind: STMT, Text: kw, Line: line, Col: col})

	if l.scanCode(scanStmtHeadEnd) {
		return // ERROR
	}
	// Cursor is at the head terminator: a depth-zero '{', a newline, or EOF.
	if l.pos < len(l.in) && l.in[l.pos] == '{' {
		bLine, bCol := l.line, l.col
		l.advance()             // '{'
		tr := l.takeCloseTrim() // trim on the body-open side, e.g. "{-"
		l.emit(Token{Kind: BLOCK_OPEN, Line: bLine, Col: bCol, TrimR: tr})
		// The block body is TEXT; the opener's trailing newline is a statement
		// boundary and is eaten unless kept (spec 01 Section 1.4).
		if tr != TrimKeep {
			l.eatOneNewline()
		}
		l.atLineStart = true
		return
	}
	// Line statement: terminate at end of line.
	eLine, eCol := l.line, l.col
	l.emit(Token{Kind: STMT_END, Line: eLine, Col: eCol})
	l.eatOneNewline()
	l.atLineStart = true
}

// eatOneNewline consumes exactly one immediately-following newline if present
// (the statement/comment newline-eating asymmetry, spec 02 R5). Leading spaces or
// tabs before that newline are NOT eaten; only the newline itself.
func (l *lexer) eatOneNewline() {
	if l.pos < len(l.in) && l.in[l.pos] == '\n' {
		l.advance()
	}
}
