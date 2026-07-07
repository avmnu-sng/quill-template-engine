// Package lex is Quill's lexer: a byte-oriented, two-mode (TEXT/CODE) scanner
// for the @-sigil default statement form described in spec 01-language-reference
// Section 1, spec 02-grammar Section 2, and design/lexical.md.
//
// The lexer splits a template into TEXT spans (emitted verbatim, byte-for-byte)
// and CODE tokens (interpolations, comments, and statement heads). The single
// load-bearing boundary rule is that a lone '{' or '}' in TEXT is NEVER a
// delimiter: code begins only at the two-byte sigils "{{" and "{#", at an
// @-led statement keyword, and at @verbatim. Everything else is output.
//
// This package is the scanner only. It does not parse expressions or statement
// heads; it classifies bytes into tokens and tracks bracket depth so that "}}"
// is recognized only at depth zero (spec 02 R3). Word-operator versus identifier
// disambiguation is left to the parser per spec 02 R2: every word operator is
// emitted as a NAME token here.
package lex

import "fmt"

// Kind enumerates the lexical token kinds Quill produces. TEXT-mode bytes
// collapse into a single TEXT token; CODE-mode bytes fan out into the operator,
// literal, and name tokens below. Delimiter tokens (the sigils and the statement
// braces) carry the trim modifiers in their own fields, not in distinct kinds.
type Kind uint8

const (
	// EOF is the end-of-input sentinel, always the last token.
	EOF Kind = iota
	// ERROR is a lexical fault (e.g. an unterminated comment or string). Its
	// Text holds an ASCII message and the stream stops after it.
	ERROR

	// TEXT is a maximal run of literal output bytes, with \{ \} \\ escapes
	// already resolved to { } \ in Text.
	TEXT

	// OPEN_INTERP is "{{" with optional opening trim modifier (TrimL).
	OPEN_INTERP
	// CLOSE_INTERP is "}}" with optional closing trim modifier (TrimR).
	CLOSE_INTERP
	// STMT is an @-led statement keyword at line start, e.g. "@for"; Text is
	// the keyword without the '@'. TrimL is unused; statement heads carry their
	// own brace trim on BLOCK_OPEN.
	STMT
	// BLOCK_OPEN is the '{' that opens a statement body, with optional TrimR.
	BLOCK_OPEN
	// BLOCK_CLOSE is "@}", the explicit block close, with optional TrimR.
	BLOCK_CLOSE
	// STMT_END marks the end of a line statement (a newline terminator). It
	// carries no text and lets the parser bound a head without re-scanning.
	STMT_END

	// VERBATIM is the literal body of an @verbatim region, copied byte-for-byte
	// and never scanned. Text is the raw body.
	VERBATIM

	// NAME is an identifier OR a word operator (and/or/not/in/is/...); the
	// parser reclassifies by position (spec 02 R2). It also covers the special
	// names _self/_context/_charset, which the parser recognizes by spelling.
	NAME
	// INT is an integer literal (int64), Text is the normalized digits without
	// '_' separators but keeping any 0x/0b/0o prefix.
	INT
	// FLOAT is a floating literal (float64), Text without '_' separators.
	FLOAT
	// STRING is a string literal. Text holds the raw source bytes including the
	// surrounding quotes/backticks so the parser can apply the form's escape
	// and interpolation rules. Quote records which form it is.
	STRING
	// TRUE is the canonical, case-sensitive "true" literal.
	TRUE
	// FALSE is the canonical, case-sensitive "false" literal.
	FALSE
	// NULL is the canonical "null" literal; it also covers the 'none' alias.
	NULL

	// Punctuation and operators inside CODE. Maximal munch picks the longest.

	// DOT is ".", the attribute-access operator.
	DOT
	// OPTDOT is "?.", the null-safe attribute-access operator.
	OPTDOT
	// COMMA is ",", the element/argument separator.
	COMMA
	// COLON is ":", the map-entry and slice separator.
	COLON
	// LPAREN is "(", the call and grouping opener.
	LPAREN
	// RPAREN is ")", the call and grouping closer.
	RPAREN
	// LBRACKET is "[", the list-literal and index opener.
	LBRACKET
	// RBRACKET is "]", the list-literal and index closer.
	RBRACKET
	// LBRACE is "{", a mapping-literal opener inside CODE.
	LBRACE
	// RBRACE is "}", a mapping-literal closer inside CODE.
	RBRACE
	// OPTBRACK is "?[", the null-safe index opener.
	OPTBRACK
	// PIPE is "|", the filter pipe or type union; the parser disambiguates per R8.
	PIPE
	// ARROW is "=>", the arrow-function separator.
	ARROW
	// TYPEARROW is "->", the return-type marker.
	TYPEARROW
	// ASSIGN is "=", the assignment operator.
	ASSIGN
	// EQ is "==", the equality operator.
	EQ
	// NE is "!=", the inequality operator.
	NE
	// LT is "<", the less-than operator.
	LT
	// GT is ">", the greater-than operator.
	GT
	// LE is "<=", the less-than-or-equal operator.
	LE
	// GE is ">=", the greater-than-or-equal operator.
	GE
	// SPACESHIP is "<=>", the three-way comparison operator.
	SPACESHIP
	// PLUS is "+", addition or unary plus.
	PLUS
	// MINUS is "-", subtraction or unary minus.
	MINUS
	// STAR is "*", the multiplication operator.
	STAR
	// POW is "**", the exponentiation operator.
	POW
	// SLASH is "/", the division operator.
	SLASH
	// FLOORDIV is "//", the floor-division operator.
	FLOORDIV
	// PERCENT is "%", the modulo operator.
	PERCENT
	// TILDE is "~", the string-concatenation operator.
	TILDE
	// RANGE is "..", the range operator.
	RANGE
	// SPREAD is "...", the spread operator.
	SPREAD
	// QUESTION is "?", the ternary-condition marker.
	QUESTION
	// COALESCE is "??", the null-coalescing operator.
	COALESCE
	// ELVIS is "?:", the elvis (truthy-coalescing) operator.
	ELVIS
	// BANG is "!", the logical-not operator.
	BANG
	// AMP is "&", the bitwise-and alias.
	AMP
	// CARET is "^", the bitwise-xor alias.
	CARET
	// ANDAND is "&&", the logical-and alias.
	ANDAND
	// OROR is "||", the logical-or alias.
	OROR
	// BITOR3 is "|||", the bitwise-or alias.
	BITOR3
)

// Trim records a whitespace-control modifier attached to a delimiter side.
type Trim uint8

const (
	// TrimNone is no modifier.
	TrimNone Trim = iota
	// TrimHard is '-': strips all adjacent whitespace including newlines.
	TrimHard
	// TrimLine is '~': strips adjacent spaces, tabs, NUL, and vertical tab, but
	// not newlines.
	TrimLine
	// TrimKeep is '+': closing side only, suppresses the one-newline-eating of a
	// block close or comment close.
	TrimKeep
)

// Quote records which string form a STRING token uses.
type Quote uint8

const (
	// QuoteNone is the zero value for non-STRING tokens.
	QuoteNone Quote = iota
	// QuoteSingle is '...': no interpolation, limited escapes.
	QuoteSingle
	// QuoteDouble is "...": full escapes plus #{ } interpolation.
	QuoteDouble
	// QuoteBacktick is `...`: raw, no escape processing.
	QuoteBacktick
)

// Token is one lexical unit. Line and Col are 1-based positions of the token's
// first byte in the CR/CRLF-normalized source (spec 01 Section 1.8). TrimL/TrimR
// carry whitespace modifiers on delimiter tokens; Quote tags STRING tokens.
type Token struct {
	Kind  Kind
	Text  string
	Line  int
	Col   int
	TrimL Trim
	TrimR Trim
	Quote Quote
}

// String renders a token for test output and debugging in a stable ASCII form.
func (t Token) String() string {
	switch t.Kind {
	case EOF:
		return "EOF"
	case ERROR:
		return fmt.Sprintf("ERROR(%q)", t.Text)
	case TEXT:
		return fmt.Sprintf("TEXT(%q)", t.Text)
	case VERBATIM:
		return fmt.Sprintf("VERBATIM(%q)", t.Text)
	case STMT:
		return fmt.Sprintf("STMT(@%s)", t.Text)
	case STRING:
		return fmt.Sprintf("STRING(%s)", t.Text)
	case NAME, INT, FLOAT:
		return fmt.Sprintf("%s(%s)", t.Kind.label(), t.Text)
	default:
		return t.Kind.label()
	}
}

// label returns a stable name for the kind, used by String and tests.
func (k Kind) label() string {
	switch k {
	case EOF:
		return "EOF"
	case ERROR:
		return "ERROR"
	case TEXT:
		return "TEXT"
	case OPEN_INTERP:
		return "OPEN_INTERP"
	case CLOSE_INTERP:
		return "CLOSE_INTERP"
	case STMT:
		return "STMT"
	case BLOCK_OPEN:
		return "BLOCK_OPEN"
	case BLOCK_CLOSE:
		return "BLOCK_CLOSE"
	case STMT_END:
		return "STMT_END"
	case VERBATIM:
		return "VERBATIM"
	case NAME:
		return "NAME"
	case INT:
		return "INT"
	case FLOAT:
		return "FLOAT"
	case STRING:
		return "STRING"
	case TRUE:
		return "TRUE"
	case FALSE:
		return "FALSE"
	case NULL:
		return "NULL"
	case DOT:
		return "DOT"
	case OPTDOT:
		return "OPTDOT"
	case COMMA:
		return "COMMA"
	case COLON:
		return "COLON"
	case LPAREN:
		return "LPAREN"
	case RPAREN:
		return "RPAREN"
	case LBRACKET:
		return "LBRACKET"
	case RBRACKET:
		return "RBRACKET"
	case LBRACE:
		return "LBRACE"
	case RBRACE:
		return "RBRACE"
	case OPTBRACK:
		return "OPTBRACK"
	case PIPE:
		return "PIPE"
	case ARROW:
		return "ARROW"
	case TYPEARROW:
		return "TYPEARROW"
	case ASSIGN:
		return "ASSIGN"
	case EQ:
		return "EQ"
	case NE:
		return "NE"
	case LT:
		return "LT"
	case GT:
		return "GT"
	case LE:
		return "LE"
	case GE:
		return "GE"
	case SPACESHIP:
		return "SPACESHIP"
	case PLUS:
		return "PLUS"
	case MINUS:
		return "MINUS"
	case STAR:
		return "STAR"
	case POW:
		return "POW"
	case SLASH:
		return "SLASH"
	case FLOORDIV:
		return "FLOORDIV"
	case PERCENT:
		return "PERCENT"
	case TILDE:
		return "TILDE"
	case RANGE:
		return "RANGE"
	case SPREAD:
		return "SPREAD"
	case QUESTION:
		return "QUESTION"
	case COALESCE:
		return "COALESCE"
	case ELVIS:
		return "ELVIS"
	case BANG:
		return "BANG"
	case AMP:
		return "AMP"
	case CARET:
		return "CARET"
	case ANDAND:
		return "ANDAND"
	case OROR:
		return "OROR"
	case BITOR3:
		return "BITOR3"
	default:
		return fmt.Sprintf("Kind(%d)", uint8(k))
	}
}

// String exposes the kind label for %v formatting in tests.
func (k Kind) String() string { return k.label() }
