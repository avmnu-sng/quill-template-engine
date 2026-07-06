package lex

import "strings"

// scanName scans an identifier (or a word-operator / keyword spelling, all NAME
// here per spec 02 R2). The boolean and null literals are recognized by exact,
// case-sensitive spelling (spec 01 Section 1.7): true/false/null and the 'none'
// alias for null. Everything else, including True/NULL/_self, is a NAME; the
// parser handles the special names by spelling.
func (l *lexer) scanName() {
	line, col := l.line, l.col
	start := l.pos
	for l.pos < len(l.in) && isWordByte(l.in[l.pos]) {
		l.advance()
	}
	word := l.in[start:l.pos]
	switch word {
	case "true":
		l.emit(Token{Kind: TRUE, Text: word, Line: line, Col: col})
	case "false":
		l.emit(Token{Kind: FALSE, Text: word, Line: line, Col: col})
	case "null", "none":
		l.emit(Token{Kind: NULL, Text: word, Line: line, Col: col})
	default:
		l.emit(Token{Kind: NAME, Text: word, Line: line, Col: col})
	}
}

// scanNumber scans an INT or FLOAT (spec 01 Section 1.7 / spec 07.3): decimal
// with digit-group '_' separators, the 0x/0b/0o integer bases, and decimal floats
// with optional 'e' exponent. The emitted Text has '_' separators removed; base
// prefixes are preserved so the parser can pick the radix. This scanner is
// permissive about digit validity within a base (e.g. it does not reject 0b1234);
// numeric range and digit-set validation is the parser/runtime's job. It only
// fixes the lexical EXTENT of the token.
func (l *lexer) scanNumber() {
	line, col := l.line, l.col
	start := l.pos

	// Base-prefixed integer: 0x / 0b / 0o.
	if l.in[l.pos] == '0' && l.pos+1 < len(l.in) {
		switch l.in[l.pos+1] {
		case 'x', 'X', 'b', 'B', 'o', 'O':
			l.advance() // '0'
			l.advance() // base letter
			for l.pos < len(l.in) && (isHexByte(l.in[l.pos]) || l.in[l.pos] == '_') {
				l.advance()
			}
			l.emitNumber(INT, start, line, col)
			return
		}
	}

	isFloat := false
	for l.pos < len(l.in) && (isDigit(l.in[l.pos]) || l.in[l.pos] == '_') {
		l.advance()
	}
	// Fractional part: a '.' followed by a digit (so a trailing '..' RANGE is not
	// swallowed, and "1.method" attribute access stays an INT then DOT).
	if l.pos+1 < len(l.in) && l.in[l.pos] == '.' && isDigit(l.in[l.pos+1]) {
		isFloat = true
		l.advance() // '.'
		for l.pos < len(l.in) && (isDigit(l.in[l.pos]) || l.in[l.pos] == '_') {
			l.advance()
		}
	}
	// Exponent: e / E, optional sign, digits.
	if l.pos < len(l.in) && (l.in[l.pos] == 'e' || l.in[l.pos] == 'E') {
		j := l.pos + 1
		if j < len(l.in) && (l.in[j] == '+' || l.in[j] == '-') {
			j++
		}
		if j < len(l.in) && isDigit(l.in[j]) {
			isFloat = true
			for l.pos < j {
				l.advance()
			}
			for l.pos < len(l.in) && (isDigit(l.in[l.pos]) || l.in[l.pos] == '_') {
				l.advance()
			}
		}
	}
	if isFloat {
		l.emitNumber(FLOAT, start, line, col)
	} else {
		l.emitNumber(INT, start, line, col)
	}
}

// emitNumber emits a numeric token with '_' separators stripped from the raw
// source slice [start:pos].
func (l *lexer) emitNumber(k Kind, start, line, col int) {
	raw := l.in[start:l.pos]
	text := strings.ReplaceAll(raw, "_", "")
	l.emit(Token{Kind: k, Text: text, Line: line, Col: col})
}

// scanString scans a string literal in one of the three forms (spec 01 Section
// 1.5 / spec 07.1). The token's Text is the RAW source including delimiters; the
// parser applies the per-form escape and interpolation rules. Bracket balancing in
// the surrounding CODE is unaffected by braces INSIDE a string: a '}' or '{' in a
// string is part of the STRING token and never touches the depth counter, because
// the whole literal is consumed here in one shot.
//
// For double-quoted strings, the scanner skips over #{ ... } interpolation
// segments and \-escapes so that a '"' inside an interpolation expression or after
// a backslash does not prematurely end the string. It does NOT tokenize the inner
// expression; that is the parser's job once it splits the raw text.
func (l *lexer) scanString() (stop bool) {
	line, col := l.line, l.col
	quote := l.in[l.pos]
	start := l.pos
	l.advance() // opening delimiter

	switch quote {
	case '`':
		// Raw: no escapes, ends at the next backtick.
		for l.pos < len(l.in) && l.in[l.pos] != '`' {
			l.advance()
		}
		if l.pos >= len(l.in) {
			l.errorf(line, col, "unterminated raw string: missing closing %q", "`")
			return true
		}
		l.advance() // closing '`'
		l.emit(Token{Kind: STRING, Text: l.in[start:l.pos], Line: line, Col: col, Quote: QuoteBacktick})
		return false

	case '\'':
		for l.pos < len(l.in) {
			c := l.in[l.pos]
			if c == '\\' && l.pos+1 < len(l.in) {
				l.advance()
				l.advance()
				continue
			}
			if c == '\'' {
				l.advance()
				l.emit(Token{Kind: STRING, Text: l.in[start:l.pos], Line: line, Col: col, Quote: QuoteSingle})
				return false
			}
			l.advance()
		}
		l.errorf(line, col, "unterminated string: missing closing %q", "'")
		return true

	default: // '"'
		for l.pos < len(l.in) {
			c := l.in[l.pos]
			if c == '\\' && l.pos+1 < len(l.in) {
				l.advance()
				l.advance()
				continue
			}
			// #{ ... } interpolation: skip to the balancing '}' so a '"' inside
			// the embedded expression does not close the string.
			if c == '#' && l.pos+1 < len(l.in) && l.in[l.pos+1] == '{' {
				l.advance() // '#'
				l.advance() // '{'
				braces := 1
				for l.pos < len(l.in) && braces > 0 {
					switch l.in[l.pos] {
					case '{':
						braces++
					case '}':
						braces--
					}
					l.advance()
				}
				continue
			}
			if c == '"' {
				l.advance()
				l.emit(Token{Kind: STRING, Text: l.in[start:l.pos], Line: line, Col: col, Quote: QuoteDouble})
				return false
			}
			l.advance()
		}
		l.errorf(line, col, "unterminated string: missing closing %q", "\"")
		return true
	}
}
