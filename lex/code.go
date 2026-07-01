package lex

// closer is a depth-zero stop predicate for scanCode. It inspects the cursor when
// bracket depth is zero and reports whether the CODE region ends here (without
// consuming anything). scanCode handles the bytes it does not stop on.
type closer func(l *lexer) bool

// scanInterpClose stops at a depth-zero "}}" (possibly preceded by a close trim
// modifier on the closing side, e.g. "~}}" / "-}}" / "+}}").
func scanInterpClose(l *lexer) bool {
	i := l.pos
	if i < len(l.in) && isCloseTrim(l.in[i]) {
		i++
	}
	return i+1 < len(l.in) && l.in[i] == '}' && l.in[i+1] == '}'
}

// scanStmtHeadEnd stops a statement head at a depth-zero block-open '{', a
// newline, or EOF. Most mapping-literal '{'s sit at depth >= 1 (they follow other
// head tokens) and are never seen here. The one ambiguous case is a head whose
// expression STARTS with a mapping literal -- "@with { x: 1 }", "@set {a} = e",
// "@use 'x' with { b: a }" -- where the map '{' is at depth zero. mapAhead peeks
// the balanced "{ ... }" and reports whether it is a mapping literal (it contains
// a depth-1 ':' or '...', or is empty) rather than a body open; the head scanner
// then keeps the map in CODE and stops only at the true body '{' or the line end.
func scanStmtHeadEnd(l *lexer) bool {
	if l.pos >= len(l.in) {
		return true
	}
	c := l.in[l.pos]
	if c == '\n' {
		return true
	}
	if c == '{' {
		return !l.mapAhead()
	}
	return false
}

// mapAhead reports whether the '{' at the cursor opens a mapping literal (CODE)
// rather than a statement body. It scans the balanced brace span: a depth-1 ':'
// (a keyed entry) or '...' (a spread entry), or an immediately-closing '}' (the
// empty map), marks a mapping literal. A '{' whose body is plain template text
// (the statement body) has none of these at depth 1 before its close, so it is a
// body open. The scan is bounded by the matching '}' and never crosses into the
// statement body it might precede.
func (l *lexer) mapAhead() bool {
	i := l.pos + 1
	// Skip horizontal whitespace after '{'.
	for i < len(l.in) && (l.in[i] == ' ' || l.in[i] == '\t') {
		i++
	}
	if i >= len(l.in) {
		return false
	}
	if l.in[i] == '}' {
		return true // empty mapping literal "{}"
	}
	depth := 1
	for i < len(l.in) && depth > 0 {
		switch l.in[i] {
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			depth--
			if depth == 0 {
				// A balanced "{ ... }" with no inner map marker is a mapping/
				// destructuring literal only when an "=" follows (a "@set {a, b} = e"
				// destructuring target). Otherwise it was the statement body.
				return assignFollows(l.in, i+1)
			}
		case '\'', '"', '`':
			i = skipStringByte(l.in, i)
			continue
		case ':':
			if depth == 1 {
				return true
			}
		case '.':
			if depth == 1 && i+2 < len(l.in) && l.in[i+1] == '.' && l.in[i+2] == '.' {
				return true
			}
		case '\n':
			// A newline inside an unbalanced "{" means this was a body open whose
			// body is on following lines, not a single-line mapping literal.
			return false
		}
		i++
	}
	return false
}

// assignFollows reports whether, after optional horizontal whitespace at index i,
// the next byte is a single '=' (an assignment), distinguishing a destructuring
// target "{a} = e" from a statement body "{ ... }". It excludes "==" so a body
// followed by an equality expression is not misread.
func assignFollows(s string, i int) bool {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i < len(s) && s[i] == '=' && (i+1 >= len(s) || s[i+1] != '=')
}

// skipStringByte returns the index just past a string literal beginning at i in s
// (the opening quote byte is s[i]). It mirrors the lexer's string forms enough to
// skip over braces and colons inside a string during the mapAhead peek.
func skipStringByte(s string, i int) int {
	quote := s[i]
	i++
	for i < len(s) {
		if quote != '`' && s[i] == '\\' && i+1 < len(s) {
			i += 2
			continue
		}
		if s[i] == quote {
			return i + 1
		}
		i++
	}
	return i
}

// scanCode tokenizes CODE bytes, emitting tokens until the stop predicate fires at
// bracket depth zero. It tracks (), [], {} nesting so a "}}" or block-open '{'
// inside a nested literal does not end the region prematurely (spec 02 R3). It
// returns true if a fatal ERROR was emitted.
func (l *lexer) scanCode(stop closer) (stop2 bool) {
	depth := 0
	for l.pos < len(l.in) {
		if depth == 0 && stop(l) {
			return false
		}
		c := l.in[l.pos]

		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			l.advance()
			continue
		case c == '#':
			// Inline comment to end of line (spec 01 Section 1.5 / spec 07.2).
			// Note: a string's '#' is consumed inside scanString, so any '#' the
			// scanner sees here is a real comment introducer.
			for l.pos < len(l.in) && l.in[l.pos] != '\n' {
				l.advance()
			}
			continue
		case c == '\'' || c == '"' || c == '`':
			if l.scanString() {
				return true
			}
			continue
		case isDigit(c):
			l.scanNumber()
			continue
		case isIdentStart(c):
			l.scanName()
			continue
		}

		// Operators and punctuation, maximal munch (spec 01 Section 1.7).
		switch c {
		case '(':
			depth++
			l.op(LPAREN, 1)
		case ')':
			if depth > 0 {
				depth--
			}
			l.op(RPAREN, 1)
		case '[':
			depth++
			// A "?[" pair is consumed at the '?' branch, so this is a lone '['.
			l.op(LBRACKET, 1)
		case ']':
			if depth > 0 {
				depth--
			}
			l.op(RBRACKET, 1)
		case '{':
			depth++
			l.op(LBRACE, 1)
		case '}':
			if depth > 0 {
				depth--
			}
			l.op(RBRACE, 1)
		case '.':
			switch {
			case l.has("..."):
				l.op(SPREAD, 3)
			case l.has(".."):
				l.op(RANGE, 2)
			default:
				l.op(DOT, 1)
			}
		case ',':
			l.op(COMMA, 1)
		case ':':
			l.op(COLON, 1)
		case '|':
			switch {
			case l.has("|||"):
				l.op(BITOR3, 3)
			case l.has("||"):
				l.op(OROR, 2)
			default:
				l.op(PIPE, 1)
			}
		case '=':
			switch {
			case l.has("=="):
				l.op(EQ, 2)
			case l.has("=>"):
				l.op(ARROW, 2)
			default:
				l.op(ASSIGN, 1)
			}
		case '!':
			if l.has("!=") {
				l.op(NE, 2)
			} else {
				l.op(BANG, 1)
			}
		case '<':
			switch {
			case l.has("<=>"):
				l.op(SPACESHIP, 3)
			case l.has("<="):
				l.op(LE, 2)
			default:
				l.op(LT, 1)
			}
		case '>':
			if l.has(">=") {
				l.op(GE, 2)
			} else {
				l.op(GT, 1)
			}
		case '+':
			l.op(PLUS, 1)
		case '-':
			if l.has("->") {
				l.op(TYPEARROW, 2)
			} else {
				l.op(MINUS, 1)
			}
		case '*':
			if l.has("**") {
				l.op(POW, 2)
			} else {
				l.op(STAR, 1)
			}
		case '/':
			if l.has("//") {
				l.op(FLOORDIV, 2)
			} else {
				l.op(SLASH, 1)
			}
		case '%':
			l.op(PERCENT, 1)
		case '~':
			l.op(TILDE, 1)
		case '?':
			switch {
			case l.has("??"):
				l.op(COALESCE, 2)
			case l.has("?:"):
				l.op(ELVIS, 2)
			case l.has("?."):
				l.op(OPTDOT, 2)
			case l.has("?["):
				depth++ // the '[' it opens balances at the matching ']'
				l.op(OPTBRACK, 2)
			default:
				l.op(QUESTION, 1)
			}
		case '&':
			if l.has("&&") {
				l.op(ANDAND, 2)
			} else {
				l.op(AMP, 1)
			}
		case '^':
			l.op(CARET, 1)
		default:
			l.errorf(l.line, l.col, "unexpected byte %q in code", string(c))
			return true
		}
	}
	// Reached EOF inside CODE without satisfying the stop predicate. For a line
	// statement head that is fine (EOF ends the line); scanStmtHeadEnd returns
	// true at EOF so we never get here for heads. An interpolation that hits EOF
	// is unterminated.
	if !stop(l) {
		l.errorf(l.line, l.col, "unterminated interpolation: reached end of input before %q", "}}")
		return true
	}
	return false
}

// op emits a fixed-length operator/punctuation token of n bytes.
func (l *lexer) op(k Kind, n int) {
	line, col := l.line, l.col
	for i := 0; i < n; i++ {
		l.advance()
	}
	l.emit(Token{Kind: k, Line: line, Col: col})
}

// has reports whether the bytes at the cursor match s exactly.
func (l *lexer) has(s string) bool {
	if l.pos+len(s) > len(l.in) {
		return false
	}
	return l.in[l.pos:l.pos+len(s)] == s
}
