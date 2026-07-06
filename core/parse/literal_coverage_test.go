package parse

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/core/ast"
	"github.com/avmnu-sng/quill-template-engine/errors"
)

// stringLitDump parses "{{ <expr> }}" and returns the Dump of the sole inner
// expression, so a decode test asserts against one stable S-expression. It fails
// the test on any parse error (used for the success paths of decodeEscape /
// decodeUnicodeEscape).
func stringLitDump(t *testing.T, expr string) string {
	t.Helper()
	mod, err := ParseString("t", "{{ "+expr+" }}")
	if err != nil {
		t.Fatalf("parse %q: unexpected error: %v", expr, err)
	}
	print := mod.Child(0)
	if print == nil || print.Kind != ast.KindPrint {
		t.Fatalf("expected a Print node for %q, got %s", expr, ast.Dump(mod))
	}
	return ast.Dump(print.Child(0))
}

// TestDecodeEscapeSuccess drives decodeEscape (via decodeString / decodeWith)
// through every escape it accepts, asserting the exact decoded rune. ast.Dump
// Go-quotes the decoded string, so the expected S-expression shows the real
// control/quote characters after decoding, not the source escape.
func TestDecodeEscapeSuccess(t *testing.T) {
	tests := []struct{ expr, want string }{
		// Common escapes shared by single- and double-quoted forms.
		{`"a\nb"`, `(String "a\nb")`},               // \n -> newline
		{`"a\tb"`, `(String "a\tb")`},               // \t -> tab
		{`"q\"q"`, `(String "q\"q")`},               // \" -> literal double quote
		{`"back\\slash"`, `(String "back\\slash")`}, // \\ -> single backslash
		{`'it\'s'`, `(String "it's")`},              // \' -> literal single quote
		{`'a\nb'`, `(String "a\nb")`},               // \n in a single-quoted string
		// Double-quoted-only escapes.
		{`"a\rb"`, `(String "a\rb")`}, // \r -> carriage return (double only)
		// \xHH hex byte escape: \x41 decodes to 'A'.
		{`"\x41"`, `(String "A")`},
		{`'\x41'`, `(String "A")`},                    // \xHH is valid in single-quoted too
		{`"line\x0Abreak"`, `(String "line\nbreak")`}, // \x0A is a newline byte
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			if got := stringLitDump(t, tc.expr); got != tc.want {
				t.Fatalf("decode %q\n got: %s\nwant: %s", tc.expr, got, tc.want)
			}
		})
	}
}

// TestDecodeUnicodeEscapeSuccess drives decodeUnicodeEscape through valid
// \u{...} sequences (double-quoted only), asserting the exact decoded rune.
func TestDecodeUnicodeEscapeSuccess(t *testing.T) {
	tests := []struct{ expr, want string }{
		{`"\u{41}"`, `(String "A")`},               // U+0041 -> 'A'
		{`"\u{1F600}"`, "(String \"\U0001F600\")"}, // an astral code point (emoji)
		{`"\u{a9}"`, "(String \"©\")"},             // U+00A9 (c) copyright sign
		{`"pre\u{41}post"`, `(String "preApost")`}, // decoded rune spliced between literals
		// The maximum valid code point (U+10FFFF). ast.Dump Go-quotes this
		// unprintable rune with the \U form, so the expected text is literal.
		{`"\u{10FFFF}"`, `(String "\U0010ffff")`},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			if got := stringLitDump(t, tc.expr); got != tc.want {
				t.Fatalf("decode %q\n got: %s\nwant: %s", tc.expr, got, tc.want)
			}
		})
	}
}

// TestDecodeEscapeErrors covers the failure branches of decodeEscape: an
// unknown escape letter, a double-only escape used in a single-quoted string,
// and malformed \xHH sequences. Each must surface as a positioned KindSyntax
// error whose message names the specific fault.
func TestDecodeEscapeErrors(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		// An unrecognized escape letter is rejected and the message echoes the
		// two-byte sequence that failed.
		{"unknown escape double", `{{ "bad\q" }}`, `invalid escape sequence "\\q"`},
		{"unknown escape single", `{{ 'bad\q' }}`, `invalid escape sequence "\\q"`},
		// \U (capital) is not a supported escape at all -- there is no \UXXXXXXXX
		// form; only \u{...} exists. It must be rejected, not silently accepted.
		{"capital U not supported", `{{ "\U00000041" }}`, `invalid escape sequence "\\U"`},
		// \r is double-quoted only; in a single-quoted string it is an invalid escape.
		{"carriage return in single", `{{ 'a\rb' }}`, `invalid escape sequence "\\r"`},
		// \xHH needs two hex digits; too few is "incomplete", non-hex is "invalid".
		{"incomplete hex escape", `{{ "\x4" }}`, `incomplete \xHH escape`},
		{"non-hex in hex escape", `{{ "\xZZ" }}`, `invalid \xHH escape`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseString("t", tc.src)
			if err == nil {
				t.Fatalf("expected a syntax error for %q, got none", tc.src)
			}
			if errors.KindOf(err) != errors.KindSyntax {
				t.Fatalf("kind for %q = %v, want KindSyntax", tc.src, errors.KindOf(err))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error for %q = %q, want substring %q", tc.src, err.Error(), tc.want)
			}
		})
	}
}

// TestDecodeUnicodeEscapeErrors covers the failure branches of
// decodeUnicodeEscape: a missing '{', a missing closing '}', non-hex digits, and
// a code point above U+10FFFF. Each is a positioned KindSyntax error.
func TestDecodeUnicodeEscapeErrors(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		// \u not followed by '{' is malformed (there is no \uXXXX form): the
		// hex digits sit where the '{' is required, so it is rejected.
		{"missing brace", `{{ "\u41" }}`, `invalid \u{...} escape`},
		// An opened \u{ with no closing '}' before the string ends is unterminated.
		{"unterminated braces", `{{ "\u{41" }}`, `unterminated \u{...} escape`},
		// Non-hex content inside the braces is an invalid code point.
		{"non-hex code point", `{{ "\u{ZZ}" }}`, `invalid code point in \u{...} escape`},
		// Empty braces \u{} have valid delimiters but no digits: ParseUint("")
		// fails, so it is an invalid code point (not "unterminated"/"invalid \u").
		{"empty braces", `{{ "\u{}" }}`, `invalid code point in \u{...} escape`},
		// A value above the Unicode maximum (U+10FFFF) is rejected.
		{"code point too large", `{{ "\u{110000}" }}`, `invalid code point in \u{...} escape`},
		// One past the maximum (U+110000) sits inside uint32 range yet exceeds the
		// Unicode ceiling: exercises the "v > 0x10FFFF" arm rather than a ParseUint
		// failure, and is the exact boundary just above the largest valid rune.
		{"one past max rune", `{{ "\u{110001}" }}`, `invalid code point in \u{...} escape`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseString("t", tc.src)
			if err == nil {
				t.Fatalf("expected a syntax error for %q, got none", tc.src)
			}
			if errors.KindOf(err) != errors.KindSyntax {
				t.Fatalf("kind for %q = %v, want KindSyntax", tc.src, errors.KindOf(err))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error for %q = %q, want substring %q", tc.src, err.Error(), tc.want)
			}
		})
	}
}

// TestDescribeNamesLiteralTokens pins the contract that describe renders a
// bracket/delimiter token by its literal spelling in single quotes (e.g. ")",
// "]", "}") rather than by its lexer token-kind name (RPAREN, RBRACKET, RBRACE).
// A stray or mismatched delimiter must therefore produce a message a template
// author recognizes. Each want is checked to be present and, for the
// corresponding kind name, absent.
func TestDescribeNamesLiteralTokens(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    string // the literal token spelling that must appear
		notWant string // the token-kind name that must NOT appear
	}{
		// A stray ')' where an expression is expected: describe -> ')'.
		{"stray rparen", `{{ ) }}`, `found ')'`, "RPAREN"},
		// A stray ']' where an expression is expected: describe -> ']'.
		{"stray rbracket", `{{ ] }}`, `found ']'`, "RBRACKET"},
		// A grouping closed by the wrong delimiter '}' names the found ')'-expected
		// and the found '}' both literally.
		{"grouping wrong close", `{{ (a } }}`, `found '}'`, "RBRACE"},
		// A call argument list closed by ']' instead of ')': the found token is ']'.
		{"call wrong close", `{{ f(1] }}`, `found ']'`, "RBRACKET"},
		// A mapping literal closed by ')' instead of '}': the found token is ')'.
		{"map wrong close", `{{ {a: 1) }}`, `found ')'`, "RPAREN"},
		// An index closed by ')' instead of ']': the found token is ')'.
		{"index wrong close", `{{ a[0) }}`, `found ')'`, "RPAREN"},
		// A range with no right operand hits end-of-interpolation: describe -> '}}'.
		{"range missing rhs", `{{ a .. }}`, `found '}}'`, "CLOSE_INTERP"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseString("t", tc.src)
			if err == nil {
				t.Fatalf("expected a syntax error for %q, got none", tc.src)
			}
			if errors.KindOf(err) != errors.KindSyntax {
				t.Fatalf("kind for %q = %v, want KindSyntax", tc.src, errors.KindOf(err))
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("error for %q = %q, want substring %q", tc.src, msg, tc.want)
			}
			if strings.Contains(msg, tc.notWant) {
				t.Fatalf("error for %q = %q must not leak token-kind name %q", tc.src, msg, tc.notWant)
			}
		})
	}
}
