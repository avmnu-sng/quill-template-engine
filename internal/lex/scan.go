package lex

// takeOpenTrim consumes an opening-side trim modifier ('-' hard, '~' line) if one
// sits immediately after an opener ("{{-", "{{~"). '+' is not valid on the open
// side (spec 01 Section 1.4). The '~' here is unambiguously a trim, not the concat
// operator, because it abuts the delimiter (spec 03 Section 3.2 position rule).
func (l *lexer) takeOpenTrim() Trim {
	if l.pos >= len(l.in) {
		return TrimNone
	}
	switch l.in[l.pos] {
	case '-':
		l.advance()
		return TrimHard
	case '~':
		l.advance()
		return TrimLine
	}
	return TrimNone
}

// takeCloseTrim consumes a closing-side trim modifier ('-' hard, '~' line, '+'
// keep) if one sits immediately before a closer ("-}}", "~}}", "+}}", "@}-",
// "{-"). The cursor is left just before the closing bytes.
func (l *lexer) takeCloseTrim() Trim {
	if l.pos >= len(l.in) {
		return TrimNone
	}
	switch l.in[l.pos] {
	case '-':
		l.advance()
		return TrimHard
	case '~':
		l.advance()
		return TrimLine
	case '+':
		l.advance()
		return TrimKeep
	}
	return TrimNone
}

// isCloseTrim reports whether b is a closing-side trim modifier byte.
func isCloseTrim(b byte) bool { return b == '-' || b == '~' || b == '+' }

// isDigit reports an ASCII decimal digit.
func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// isHexByte reports an ASCII byte that can appear in any integer base body
// (covers hex digits; binary/octal digit-set validation is deferred to the
// parser, see scanNumber).
func isHexByte(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// isIdentStart reports whether b can begin an identifier: an ASCII letter, '_', or
// any byte >= 0x80 (the lead byte of a multibyte UTF-8 rune, so Unicode-letter
// identifiers are admitted; full rune-class validation is the parser's concern).
func isIdentStart(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b >= 0x80
}

// isWordByte reports whether b can continue an identifier or keyword: a letter,
// digit, '_', or a UTF-8 continuation/lead byte.
func isWordByte(b byte) bool {
	return isIdentStart(b) || isDigit(b)
}
