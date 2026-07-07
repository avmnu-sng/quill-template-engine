package lex

import (
	"strings"

	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// TokenStream is the lexer's output: the full token slice plus the source it was
// scanned from. A scan is eager (the whole template is tokenized up front) because
// templates are small and an eager slice is the simplest thing the parser can
// drive with one-token lookahead. The final token is always EOF; a lexical fault
// appears as a single ERROR token immediately before EOF.
type TokenStream struct {
	Src    *source.Source
	Tokens []Token
}

// Lex scans a *source.Source into a TokenStream under the @-sigil default mode.
// The source is assumed already CR/CRLF-normalized to LF (source.New does this).
func Lex(src *source.Source) *TokenStream {
	l := &lexer{src: src, in: src.Code()}
	l.line, l.col = 1, 1
	l.run()
	applyTrims(l.out)
	return &TokenStream{Src: src, Tokens: l.out}
}

// lexer is the scanning state. pos is the byte cursor into in; line/col are the
// 1-based position of the byte at pos. atLineStart tracks whether the cursor is at
// the first non-emitted position of a logical line, which gates the statement and
// block-close doors (those are recognized only at line start, spec 01 Section 1.3).
type lexer struct {
	src         *source.Source
	in          string
	pos         int
	line        int
	col         int
	out         []Token
	atLineStart bool
}

// run drives the top-level TEXT loop. It accumulates literal output into a TEXT
// token and breaks out at each door (sigil, statement head, block close,
// verbatim). It stops on EOF or after emitting an ERROR.
func (l *lexer) run() {
	l.atLineStart = true
	for l.pos < len(l.in) {
		if l.scanText() {
			break // a door signalled a fatal ERROR through scanText's return.
		}
		// Not every door threads its fatal ERROR back through scanText's return
		// value: an interpolation, statement head, or block-close continuation that
		// faults inside scanCode emits the ERROR token, yet the surrounding text
		// loop still returns false. Enforce the single-fault contract (documented on
		// TokenStream) uniformly here -- once any ERROR has been emitted the scan is
		// over, so the byte the fault stopped on is never re-scanned into a stray
		// TEXT token after the ERROR.
		if n := len(l.out); n > 0 && l.out[n-1].Kind == ERROR {
			break
		}
	}
	// EOF always terminates the stream, even after an ERROR, so consumers can rely
	// on a trailing EOF sentinel.
	l.emit(Token{Kind: EOF, Line: l.line, Col: l.col})
}

// scanText consumes one TEXT segment up to (not including) the next door, emits
// the TEXT token if non-empty, then dispatches the door. It returns true if a
// fatal ERROR was emitted (caller must stop). A false return with pos advanced
// means progress was made and the caller should continue.
func (l *lexer) scanText() (stop bool) {
	var b strings.Builder
	startLine, startCol := l.line, l.col

	// lineMark records the builder length at the start of the current logical
	// line, so that when a statement/block-close door fires we can drop only this
	// line's leading horizontal whitespace (the statement-head indentation, which
	// is not emitted under the @-default) while preserving the TEXT of all
	// preceding lines already in the builder.
	lineMark := 0

	flush := func() {
		if b.Len() > 0 {
			l.emit(Token{Kind: TEXT, Text: b.String(), Line: startLine, Col: startCol})
			b.Reset()
		}
	}

	// flushBeforeStatement truncates the builder to the start of the current line,
	// emitting the preceding text as one TEXT token, then resets the builder so the
	// dropped indentation is gone.
	flushBeforeStatement := func() {
		prefix := b.String()[:lineMark]
		b.Reset()
		if len(prefix) > 0 {
			l.emit(Token{Kind: TEXT, Text: prefix, Line: startLine, Col: startCol})
		}
	}

	for l.pos < len(l.in) {
		c := l.in[l.pos]

		// Door 1/2: an interpolation or comment sigil. atSigil is the entire
		// text/code boundary predicate (spec 02 R1): a '{' opens code only when
		// the next byte is '{' or '#'.
		if c == '{' && l.pos+1 < len(l.in) {
			n := l.in[l.pos+1]
			if n == '{' {
				flush()
				l.scanInterp()
				return false
			}
			if n == '#' {
				flush()
				return l.scanComment()
			}
		}

		// Door 3/4: a statement head or block close, recognized only when the
		// cursor is at line start (after optional leading horizontal whitespace,
		// which the predicate skips). The leading whitespace is provisionally part
		// of TEXT; if a statement is found, we rewind only this line's indentation
		// out of the TEXT token, keeping prior lines intact.
		if l.atLineStart && c == '@' {
			if kw, ok := l.peekStatement(); ok {
				flushBeforeStatement()
				if kw == "verbatim" {
					return l.scanVerbatim()
				}
				l.scanStatement(kw)
				return false
			}
			if l.peekBlockClose() {
				flushBeforeStatement()
				l.scanBlockClose()
				return false
			}
		}

		// Escapes in TEXT: \{ \} \\ resolve to { } \ and suppress the sigil
		// predicate for that brace (spec 01 Section 1.2 escape 3). A lone
		// backslash is literal output.
		if c == '\\' && l.pos+1 < len(l.in) {
			switch l.in[l.pos+1] {
			case '{', '}', '\\':
				b.WriteByte(l.in[l.pos+1])
				l.advance() // consume '\'
				l.advance() // consume escaped byte
				continue
			}
		}

		// Track line-start across literal bytes: anything non-newline clears it;
		// a newline sets it for the next byte. Leading horizontal whitespace keeps
		// line-start true so an indented @stmt is still recognized.
		if c == '\n' {
			b.WriteByte(c)
			l.advance()
			l.atLineStart = true
			// The next byte begins a new logical line; record the rewind point.
			lineMark = b.Len()
			continue
		}
		if c == ' ' || c == '\t' {
			b.WriteByte(c)
			l.advance()
			// line-start preserved through horizontal whitespace
			continue
		}
		b.WriteByte(c)
		l.advance()
		l.atLineStart = false
	}
	flush()
	return false
}

// emit appends a token to the output stream.
func (l *lexer) emit(t Token) { l.out = append(l.out, t) }

// advance moves the cursor one byte, maintaining line/col. Newlines bump the line
// and reset the column; every other byte bumps the column (a tab counts as one
// column, spec 01 Section 1.8).
func (l *lexer) advance() {
	if l.pos >= len(l.in) {
		return
	}
	if l.in[l.pos] == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	l.pos++
}

// errorf emits an ERROR token at the given position and is terminal for the scan.
func (l *lexer) errorf(line, col int, format string, args ...any) {
	l.emit(Token{Kind: ERROR, Text: sprintf(format, args...), Line: line, Col: col})
}
