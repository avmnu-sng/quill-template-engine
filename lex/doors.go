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
// unless the "#}+" keep modifier follows the '}' (spec 02 R14), which suppresses
// the newline eating. Per spec 01 Section 1.4 only the '+' keep modifier applies
// to a comment close; the '-'/'~' hard/line trims are defined for sigil and
// statement braces, not for "#}", so they are intentionally not handled here.
func (l *lexer) scanComment() (stop bool) {
	openLine, openCol := l.line, l.col
	l.advance() // '{'
	l.advance() // '#'
	for l.pos < len(l.in) {
		if l.in[l.pos] == '#' && l.pos+1 < len(l.in) && l.in[l.pos+1] == '}' {
			l.advance() // '#'
			l.advance() // '}'
			// A trailing '+' keep modifier ("#}+") suppresses the one-newline
			// eating of R5 (spec 02 R14); without it, the comment close eats one
			// immediately-following newline (spec 02 R5).
			// The cursor begins a fresh line only when the close eats a trailing
			// newline. With the "#}+" keep form, or a comment that closes mid-line
			// ("{# c #}x"), no newline is eaten and atLineStart stays false so a
			// following @} on the same physical line remains TEXT (spec 02 R4a).
			if l.pos < len(l.in) && l.in[l.pos] == '+' {
				l.advance() // consume the '+' so it does not leak into TEXT
				l.atLineStart = false
			} else {
				l.atLineStart = l.eatOneNewline()
			}
			return false
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
		// peekBlockClose recognized this @} as a lone-@} line (its only
		// non-whitespace content, spec 02 R4a), so the trailing horizontal
		// whitespace up to the newline is part of the structural line and must be
		// consumed along with the one eaten newline; otherwise it would leak into
		// TEXT and break the byte-exact line layout of R5.
		l.skipTrailingHorizontalWS()
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
		// boundary and is eaten unless kept (spec 01 Section 1.4). The next byte is
		// only a fresh line start if a newline actually followed the '{'. When the
		// body opens on the same physical line (e.g. "@if x { @}"), atLineStart must
		// stay false so a same-line @} is TEXT, not a block close (spec 02 R4a: the
		// close is "a line whose only non-whitespace content is @}").
		if tr != TrimKeep {
			l.atLineStart = l.eatOneNewline()
		} else {
			l.atLineStart = false
		}
		return
	}
	// Line statement: terminate at end of line. The terminator is a newline or EOF;
	// after eating the newline the cursor begins a fresh line. At EOF there is no
	// further content, so the residual atLineStart value is immaterial.
	eLine, eCol := l.line, l.col
	l.emit(Token{Kind: STMT_END, Line: eLine, Col: eCol})
	l.atLineStart = l.eatOneNewline()
}

// eatOneNewline consumes exactly one immediately-following newline if present
// (the statement/comment newline-eating asymmetry, spec 02 R5). Leading spaces or
// tabs before that newline are NOT eaten; only the newline itself. It reports
// whether a newline was actually consumed so callers can decide whether the cursor
// now sits at a fresh line start.
func (l *lexer) eatOneNewline() bool {
	if l.pos < len(l.in) && l.in[l.pos] == '\n' {
		l.advance()
		return true
	}
	return false
}

// skipTrailingHorizontalWS consumes ' ', '\t', and '\r' between the cursor and the
// next newline ONLY when that whitespace run reaches a newline or EOF -- i.e. when
// the rest of the physical line is whitespace-only. It is used by a lone-@} close so
// the structural line's trailing whitespace is dropped rather than leaking into the
// following TEXT (spec 02 R5 byte-exact layout). The newline/EOF guard makes it safe
// for the verbatim close, where @} is recognized by brace depth rather than by being
// alone on its line: any trailing non-whitespace there is preserved untouched.
func (l *lexer) skipTrailingHorizontalWS() {
	i := l.pos
	for i < len(l.in) {
		c := l.in[i]
		if c == ' ' || c == '\t' || c == '\r' {
			i++
			continue
		}
		break
	}
	if i == len(l.in) || l.in[i] == '\n' {
		for l.pos < i {
			l.advance()
		}
	}
}
