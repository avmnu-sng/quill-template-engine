package lex

import (
	"strings"
	"testing"

	"github.com/avmnusng/quill-template-engine/source"
)

// tok is a compact expectation for a token: kind plus, where it matters, the
// token text and trim modifiers. Fields left zero are not asserted unless the
// test opts in via the want-text/want-trim flags below. To keep the table
// readable, helpers build the common shapes.
type tok struct {
	kind Kind
	text string
	tl   Trim
	tr   Trim
}

// lexToks runs the lexer and returns the token slice (including the trailing EOF).
func lexToks(t *testing.T, in string) []Token {
	t.Helper()
	return Lex(source.New("t", in)).Tokens
}

// assertToks compares the kinds of the produced tokens against want, and for any
// want entry with a non-empty text or non-zero trim, also checks those fields.
func assertToks(t *testing.T, in string, want []tok) {
	t.Helper()
	got := lexToks(t, in)
	if len(got) != len(want) {
		t.Fatalf("token count: got %d want %d\n got:  %s\n want: %s",
			len(got), len(want), dump(got), dumpWant(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Kind != w.kind {
			t.Fatalf("token %d kind: got %s want %s\n got:  %s\n want: %s",
				i, g.Kind, w.kind, dump(got), dumpWant(want))
		}
		if w.text != "" && g.Text != w.text {
			t.Fatalf("token %d text: got %q want %q", i, g.Text, w.text)
		}
		if w.tl != TrimNone && g.TrimL != w.tl {
			t.Fatalf("token %d TrimL: got %v want %v", i, g.TrimL, w.tl)
		}
		if w.tr != TrimNone && g.TrimR != w.tr {
			t.Fatalf("token %d TrimR: got %v want %v", i, g.TrimR, w.tr)
		}
	}
}

func dump(ts []Token) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = t.String()
	}
	return strings.Join(parts, " ")
}

func dumpWant(ws []tok) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		if w.text != "" {
			parts[i] = w.kind.String() + "(" + w.text + ")"
		} else {
			parts[i] = w.kind.String()
		}
	}
	return strings.Join(parts, " ")
}

// kinds extracts just the kind sequence for terse assertions.
func kinds(ts []Token) []Kind {
	ks := make([]Kind, len(ts))
	for i, t := range ts {
		ks[i] = t.Kind
	}
	return ks
}

// ---------------------------------------------------------------------------
// The text/code boundary: bare braces are literal (spec 02 R1, design/lexical 4).
// ---------------------------------------------------------------------------

func TestBareBracesAreLiteralText(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"java method body", "public DoublyLinkedList() {\n    this.head = null;\n}\n"},
		{"lone close at col 0", "class C {\n}\n"},
		{"brace soup", "} else {\n}\n}\n"},
		{"lone open then space", "{ this is text }"},
		{"lone open then letter", "{x:1}"},
		{"close close pair in text", "literal }} here"},
		{"open after newline", "a\n{\nb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := lexToks(t, c.in)
			// Exactly one TEXT token (verbatim copy) then EOF.
			if len(got) != 2 || got[0].Kind != TEXT || got[1].Kind != EOF {
				t.Fatalf("expected one TEXT + EOF, got %s", dump(got))
			}
			if got[0].Text != c.in {
				t.Fatalf("TEXT not byte-exact:\n got  %q\n want %q", got[0].Text, c.in)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Interpolation door {{ }}.
// ---------------------------------------------------------------------------

func TestInterpolation(t *testing.T) {
	assertToks(t, "{{ u.name | upper }}", []tok{
		{kind: OPEN_INTERP},
		{kind: NAME, text: "u"},
		{kind: DOT},
		{kind: NAME, text: "name"},
		{kind: PIPE},
		{kind: NAME, text: "upper"},
		{kind: CLOSE_INTERP},
		{kind: EOF},
	})
}

func TestInterpolationSurroundedByText(t *testing.T) {
	assertToks(t, "Hi {{ name }}!", []tok{
		{kind: TEXT, text: "Hi "},
		{kind: OPEN_INTERP},
		{kind: NAME, text: "name"},
		{kind: CLOSE_INTERP},
		{kind: TEXT, text: "!"},
		{kind: EOF},
	})
}

// A mapping literal inside an interpolation: the inner '}' must not close the
// interpolation (spec 02 R3, bracket balancing).
func TestInterpolationMappingLiteralBalances(t *testing.T) {
	assertToks(t, "{{ {a: 1, b: 2} | json }}", []tok{
		{kind: OPEN_INTERP},
		{kind: LBRACE},
		{kind: NAME, text: "a"},
		{kind: COLON},
		{kind: INT, text: "1"},
		{kind: COMMA},
		{kind: NAME, text: "b"},
		{kind: COLON},
		{kind: INT, text: "2"},
		{kind: RBRACE},
		{kind: PIPE},
		{kind: NAME, text: "json"},
		{kind: CLOSE_INTERP},
		{kind: EOF},
	})
}

// Interpolation eats no following newline (spec 02 R5).
func TestInterpolationKeepsFollowingNewline(t *testing.T) {
	got := lexToks(t, "{{ x }}\nnext")
	want := []Kind{OPEN_INTERP, NAME, CLOSE_INTERP, TEXT, EOF}
	assertKinds(t, got, want)
	if got[3].Text != "\nnext" {
		t.Fatalf("interpolation should not eat newline, TEXT = %q", got[3].Text)
	}
}

// ---------------------------------------------------------------------------
// Comment door {# #}.
// ---------------------------------------------------------------------------

func TestComment(t *testing.T) {
	got := lexToks(t, "a{# vanishes #}b")
	want := []Kind{TEXT, TEXT, EOF}
	assertKinds(t, got, want)
	if got[0].Text != "a" || got[1].Text != "b" {
		t.Fatalf("comment not consumed: %s", dump(got))
	}
}

func TestCommentEatsOneNewline(t *testing.T) {
	got := lexToks(t, "{# c #}\nrest")
	// comment consumed, its trailing newline eaten -> only "rest" remains.
	want := []Kind{TEXT, EOF}
	assertKinds(t, got, want)
	if got[0].Text != "rest" {
		t.Fatalf("comment newline not eaten, TEXT = %q", got[0].Text)
	}
}

// The "#}+" keep modifier suppresses the one-newline eating of R5 (spec 02 R14):
// the newline survives into TEXT and the '+' itself does not leak into output.
func TestCommentKeepModifier(t *testing.T) {
	got := lexToks(t, "{# c #}+\nrest")
	want := []Kind{TEXT, EOF}
	assertKinds(t, got, want)
	if got[0].Text != "\nrest" {
		t.Fatalf("#}+ should keep newline and drop '+', TEXT = %q", got[0].Text)
	}
}

// A comment that closes mid-line keeps following text on the same line and does
// not treat a same-line @} as a block close (line-start tracking, spec 02 R4a).
func TestCommentMidLineDoesNotOpenLineStart(t *testing.T) {
	got := lexToks(t, "{# c #}@}\n")
	// The @} sits mid physical line (right after the comment), so it is TEXT.
	want := []Kind{TEXT, EOF}
	assertKinds(t, got, want)
	if got[0].Text != "@}\n" {
		t.Fatalf("@} after a mid-line comment must be TEXT, got %q", got[0].Text)
	}
}

func TestCommentDoesNotScanInnerSigils(t *testing.T) {
	got := lexToks(t, "{# {{ not.code }} @if x { #}done")
	want := []Kind{TEXT, EOF}
	assertKinds(t, got, want)
	if got[0].Text != "done" {
		t.Fatalf("comment body scanned: %s", dump(got))
	}
}

func TestUnterminatedComment(t *testing.T) {
	got := lexToks(t, "x {# never closes")
	if got[len(got)-1].Kind != EOF {
		t.Fatalf("want trailing EOF, got %s", dump(got))
	}
	if got[len(got)-2].Kind != ERROR {
		t.Fatalf("want ERROR before EOF, got %s", dump(got))
	}
}

// ---------------------------------------------------------------------------
// The @-anchor: a representative statement-led template.
// ---------------------------------------------------------------------------

func TestAnchorTokenStream(t *testing.T) {
	in := "@for u in users {\n{{ u.name }}\n@}\n"
	assertToks(t, in, []tok{
		{kind: STMT, text: "for"},
		{kind: NAME, text: "u"},
		{kind: NAME, text: "in"}, // word operator lexed as NAME (R2)
		{kind: NAME, text: "users"},
		{kind: BLOCK_OPEN},
		// the body line: {{ u.name }} then its literal newline survives
		{kind: OPEN_INTERP},
		{kind: NAME, text: "u"},
		{kind: DOT},
		{kind: NAME, text: "name"},
		{kind: CLOSE_INTERP},
		{kind: TEXT, text: "\n"},
		{kind: BLOCK_CLOSE},
		{kind: EOF},
	})
}

// The block open '{' eats the opener's trailing newline; the @} eats its own.
// Net effect: one clean body line, no spurious blanks.
func TestStatementNewlineEating(t *testing.T) {
	got := lexToks(t, "@if x {\nBODY\n@}\nAFTER")
	// Expect: STMT if, NAME x, BLOCK_OPEN, TEXT "BODY\n", BLOCK_CLOSE, TEXT "AFTER"
	var texts []string
	for _, tk := range got {
		if tk.Kind == TEXT {
			texts = append(texts, tk.Text)
		}
	}
	if len(texts) != 2 || texts[0] != "BODY\n" || texts[1] != "AFTER" {
		t.Fatalf("newline-eating wrong, TEXT tokens = %q", texts)
	}
}

// A bare keyword line (no '@') is unconditionally TEXT under the @-default
// (spec 02 R4): emitted C "for (...)" needs no escaping.
func TestBareKeywordLineIsText(t *testing.T) {
	in := "for (int i = 0; i < n; i++) {\n}\n"
	got := lexToks(t, in)
	if len(got) != 2 || got[0].Kind != TEXT || got[0].Text != in {
		t.Fatalf("bare keyword line should be one TEXT token, got %s", dump(got))
	}
}

// "@forms" is not "@for": word-boundary check (spec 01 Section 1.3 cond 2).
func TestKeywordWordBoundary(t *testing.T) {
	in := "@forms are text\n"
	got := lexToks(t, in)
	if got[0].Kind != TEXT {
		t.Fatalf("@forms must be TEXT (no keyword boundary), got %s", dump(got))
	}
}

// An indented @-statement is still recognized; the leading whitespace is the
// head's indentation and is not emitted as TEXT.
func TestIndentedStatement(t *testing.T) {
	got := lexToks(t, "  @do x\nrest")
	want := []Kind{STMT, NAME, STMT_END, TEXT, EOF}
	assertKinds(t, got, want)
	if got[3].Text != "rest" {
		t.Fatalf("STMT_END should have eaten the newline, TEXT = %q", got[3].Text)
	}
}

// A line statement (no brace body) ends at the newline with STMT_END.
func TestLineStatement(t *testing.T) {
	assertToks(t, "@extends \"base.tmpl\"\n", []tok{
		{kind: STMT, text: "extends"},
		{kind: STRING, text: "\"base.tmpl\""},
		{kind: STMT_END},
		{kind: EOF},
	})
}

func TestSetLineStatement(t *testing.T) {
	assertToks(t, "@set count = users | length\n", []tok{
		{kind: STMT, text: "set"},
		{kind: NAME, text: "count"},
		{kind: ASSIGN},
		{kind: NAME, text: "users"},
		{kind: PIPE},
		{kind: NAME, text: "length"},
		{kind: STMT_END},
		{kind: EOF},
	})
}

// ---------------------------------------------------------------------------
// Block close errors are not the lexer's job, but @} at a non-line-start must
// still be literal text when it is not the only content.
// ---------------------------------------------------------------------------

func TestBlockCloseRequiresLoneLine(t *testing.T) {
	// "x @}" -- '@}' is not at line start (preceded by non-whitespace), so the
	// whole thing is TEXT; '@}' with trailing junk also stays TEXT.
	got := lexToks(t, "x @}\n")
	if got[0].Kind != TEXT {
		t.Fatalf("@} not alone on its line is TEXT, got %s", dump(got))
	}
}

func TestBlockCloseWithTrailingContentIsText(t *testing.T) {
	got := lexToks(t, "@} trailing\n")
	if got[0].Kind != TEXT {
		t.Fatalf("@} with trailing content is TEXT, got %s", dump(got))
	}
}

// A lone "@}" line with trailing horizontal whitespace before the newline is
// consumed in full: the spaces are part of the structural line and must not leak
// into TEXT (spec 02 R4a recognizes the whole line; R5 eats the newline).
func TestBlockCloseEatsTrailingWhitespace(t *testing.T) {
	got := lexToks(t, "@if x {\nB\n@}  \nAFTER")
	var texts []string
	for _, tk := range got {
		if tk.Kind == TEXT {
			texts = append(texts, tk.Text)
		}
	}
	// Body "B\n" then "AFTER"; no spurious "  \n" between the close and AFTER.
	if len(texts) != 2 || texts[0] != "B\n" || texts[1] != "AFTER" {
		t.Fatalf("@}  \\n should be consumed whole, TEXT tokens = %q", texts)
	}
}

// A same-line "@}" (only whitespace between the block opener '{' and @}) is NOT a
// block close: the close must be alone on its physical line (spec 02 R4a). The
// opener did not eat a newline, so the cursor is not at a fresh line start.
func TestSameLineBlockCloseIsText(t *testing.T) {
	got := lexToks(t, "@if x { @}")
	for _, tk := range got {
		if tk.Kind == BLOCK_CLOSE {
			t.Fatalf("same-line @} must not be a BLOCK_CLOSE, got %s", dump(got))
		}
	}
	// The " @}" after the block opener is the (TEXT) body of the block.
	want := []Kind{STMT, NAME, BLOCK_OPEN, TEXT, EOF}
	assertKinds(t, got, want)
	if got[3].Text != " @}" {
		t.Fatalf("same-line @} body TEXT = %q want %q", got[3].Text, " @}")
	}
}

// The newline form of the same template DOES produce a block close: the opener's
// newline puts @} alone on its own line.
func TestNewlineBlockCloseIsClose(t *testing.T) {
	got := lexToks(t, "@if x {\n@}")
	want := []Kind{STMT, NAME, BLOCK_OPEN, BLOCK_CLOSE, EOF}
	assertKinds(t, got, want)
}

// ---------------------------------------------------------------------------
// Whitespace control modifiers (spec 01 Section 1.4).
// ---------------------------------------------------------------------------

func TestInterpolationTrimModifiers(t *testing.T) {
	cases := []struct {
		in     string
		wantTL Trim
		wantTR Trim
	}{
		{"{{- x -}}", TrimHard, TrimHard},
		{"{{~ x ~}}", TrimLine, TrimLine},
		{"{{- x ~}}", TrimHard, TrimLine},
		{"{{ x +}}", TrimNone, TrimKeep},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := lexToks(t, c.in)
			if got[0].Kind != OPEN_INTERP || got[0].TrimL != c.wantTL {
				t.Fatalf("open trim: got %v want %v (%s)", got[0].TrimL, c.wantTL, dump(got))
			}
			// close is the token before EOF
			cl := got[len(got)-2]
			if cl.Kind != CLOSE_INTERP || cl.TrimR != c.wantTR {
				t.Fatalf("close trim: got %v want %v (%s)", cl.TrimR, c.wantTR, dump(got))
			}
		})
	}
}

func TestBlockCloseKeepNewline(t *testing.T) {
	// "@}+" keeps the following newline (spec 02 R14).
	got := lexToks(t, "@if x {\nB\n@}+\nAFTER")
	var texts []string
	for _, tk := range got {
		if tk.Kind == TEXT {
			texts = append(texts, tk.Text)
		}
		if tk.Kind == BLOCK_CLOSE && tk.TrimR != TrimKeep {
			t.Fatalf("BLOCK_CLOSE TrimR: got %v want TrimKeep", tk.TrimR)
		}
	}
	// With keep, the newline after @}+ survives into "\nAFTER".
	if texts[len(texts)-1] != "\nAFTER" {
		t.Fatalf("@}+ should keep newline, last TEXT = %q", texts[len(texts)-1])
	}
}

func TestBlockOpenTrim(t *testing.T) {
	got := lexToks(t, "@if x {-\nB\n@}")
	for _, tk := range got {
		if tk.Kind == BLOCK_OPEN {
			if tk.TrimR != TrimHard {
				t.Fatalf("BLOCK_OPEN trim: got %v want TrimHard", tk.TrimR)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// String literals and #{ } interpolation (spec 07.1).
// ---------------------------------------------------------------------------

func TestStringForms(t *testing.T) {
	cases := []struct {
		in    string
		quote Quote
	}{
		{`{{ 'single' }}`, QuoteSingle},
		{`{{ "double" }}`, QuoteDouble},
		{"{{ `raw \\d+` }}", QuoteBacktick},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := lexToks(t, c.in)
			if got[1].Kind != STRING || got[1].Quote != c.quote {
				t.Fatalf("string form: got kind %s quote %v (%s)", got[1].Kind, got[1].Quote, dump(got))
			}
		})
	}
}

// A double-quoted string with #{ } interpolation: the inner '}' and a '"' inside
// the embedded expression do not terminate the string (the whole literal is one
// STRING token; the parser later splits it).
func TestStringInterpolationStaysOneToken(t *testing.T) {
	got := lexToks(t, `{{ "Hello #{ name | upper }!" }}`)
	if got[1].Kind != STRING || got[1].Quote != QuoteDouble {
		t.Fatalf("want one double STRING token, got %s", dump(got))
	}
	if got[1].Text != `"Hello #{ name | upper }!"` {
		t.Fatalf("string text not byte-exact: %q", got[1].Text)
	}
	// OPEN_INTERP, STRING, CLOSE_INTERP, EOF
	assertKinds(t, got, []Kind{OPEN_INTERP, STRING, CLOSE_INTERP, EOF})
}

// A quote inside #{ } must not end the outer string.
func TestStringInterpolationWithInnerQuote(t *testing.T) {
	got := lexToks(t, `{{ "a #{ b ~ "x" } c" }}`)
	if got[1].Kind != STRING {
		t.Fatalf("inner quote broke the string: %s", dump(got))
	}
}

func TestStringEscapesDoNotTerminate(t *testing.T) {
	got := lexToks(t, `{{ "a\"b" }}`)
	if got[1].Kind != STRING || got[1].Text != `"a\"b"` {
		t.Fatalf("escaped quote terminated string early: %s", dump(got))
	}
}

func TestUnterminatedString(t *testing.T) {
	got := lexToks(t, `{{ "open `)
	if got[len(got)-2].Kind != ERROR {
		t.Fatalf("want ERROR for unterminated string, got %s", dump(got))
	}
}

// ---------------------------------------------------------------------------
// Number literals (spec 07.3).
// ---------------------------------------------------------------------------

func TestNumberLiterals(t *testing.T) {
	cases := []struct {
		in   string
		kind Kind
		text string
	}{
		{"42", INT, "42"},
		{"1_000_000", INT, "1000000"},
		{"0xFF", INT, "0xFF"},
		{"0b1010", INT, "0b1010"},
		{"0o755", INT, "0o755"},
		{"3.14", FLOAT, "3.14"},
		{"1_0.0_5", FLOAT, "10.05"},
		{"1e9", FLOAT, "1e9"},
		{"1.5e-3", FLOAT, "1.5e-3"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := lexToks(t, "{{ "+c.in+" }}")
			if got[1].Kind != c.kind || got[1].Text != c.text {
				t.Fatalf("number %q: got %s(%q) want %s(%q)", c.in, got[1].Kind, got[1].Text, c.kind, c.text)
			}
		})
	}
}

// "1..3" is INT RANGE INT, not a malformed float (the fractional part requires a
// digit right after the dot).
func TestRangeNotFloat(t *testing.T) {
	assertToks(t, "{{ 1..3 }}", []tok{
		{kind: OPEN_INTERP},
		{kind: INT, text: "1"},
		{kind: RANGE},
		{kind: INT, text: "3"},
		{kind: CLOSE_INTERP},
		{kind: EOF},
	})
}

// ---------------------------------------------------------------------------
// Boolean / null literals, case-sensitive (spec 07.4).
// ---------------------------------------------------------------------------

func TestBoolNullLiterals(t *testing.T) {
	cases := []struct {
		in   string
		kind Kind
	}{
		{"true", TRUE},
		{"false", FALSE},
		{"null", NULL},
		{"none", NULL},
		{"True", NAME},  // capitalized -> identifier
		{"FALSE", NAME}, // capitalized -> identifier
		{"NULL", NAME},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := lexToks(t, "{{ "+c.in+" }}")
			if got[1].Kind != c.kind {
				t.Fatalf("%q: got %s want %s", c.in, got[1].Kind, c.kind)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Maximal-munch operator disambiguation (spec 01 Section 1.7).
// ---------------------------------------------------------------------------

func TestMaximalMunchOperators(t *testing.T) {
	cases := []struct {
		in   string
		want []Kind
	}{
		{"a == b", []Kind{NAME, EQ, NAME}},
		{"a = b", []Kind{NAME, ASSIGN, NAME}},
		{"a // b", []Kind{NAME, FLOORDIV, NAME}},
		{"a / b", []Kind{NAME, SLASH, NAME}},
		{"a ?. b", []Kind{NAME, OPTDOT, NAME}},
		{"a ?? b", []Kind{NAME, COALESCE, NAME}},
		{"a ?: b", []Kind{NAME, ELVIS, NAME}},
		{"a <=> b", []Kind{NAME, SPACESHIP, NAME}},
		{"a <= b", []Kind{NAME, LE, NAME}},
		{"a ** b", []Kind{NAME, POW, NAME}},
		{"a ~ b", []Kind{NAME, TILDE, NAME}},
		{"x -> y", []Kind{NAME, TYPEARROW, NAME}},
		{"x => y", []Kind{NAME, ARROW, NAME}},
		{"a ||| b", []Kind{NAME, BITOR3, NAME}},
		{"a || b", []Kind{NAME, OROR, NAME}},
		{"a | b", []Kind{NAME, PIPE, NAME}},
		{"a && b", []Kind{NAME, ANDAND, NAME}},
		{"...rest", []Kind{SPREAD, NAME}},
		{"a..b", []Kind{NAME, RANGE, NAME}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := lexToks(t, "{{ "+c.in+" }}")
			// strip OPEN_INTERP ... CLOSE_INTERP EOF
			inner := got[1 : len(got)-2]
			assertKinds(t, inner, c.want)
		})
	}
}

// Word operators are emitted as NAME (the parser reclassifies, R2).
func TestWordOperatorsAreNames(t *testing.T) {
	got := lexToks(t, "{{ a and b or not c in d is e matches f }}")
	for _, tk := range got {
		if tk.Kind == EOF || tk.Kind == OPEN_INTERP || tk.Kind == CLOSE_INTERP {
			continue
		}
		if tk.Kind != NAME {
			t.Fatalf("word operator not NAME: %s", tk)
		}
	}
}

// Special names are plain NAMEs at the lexical level (the parser handles them).
func TestSpecialNames(t *testing.T) {
	got := lexToks(t, "{{ _self }}")
	if got[1].Kind != NAME || got[1].Text != "_self" {
		t.Fatalf("_self: got %s", got[1])
	}
}

// ---------------------------------------------------------------------------
// Inline comment inside CODE (spec 07.2).
// ---------------------------------------------------------------------------

func TestInlineCommentInCode(t *testing.T) {
	got := lexToks(t, "{{ a # trailing comment\n~ b }}")
	// '#'..newline is dropped; expression is a ~ b.
	inner := got[1 : len(got)-2]
	assertKinds(t, inner, []Kind{NAME, TILDE, NAME})
}

// A '#' inside a string is literal, not a comment.
func TestHashInStringNotComment(t *testing.T) {
	got := lexToks(t, `{{ "#tag" }}`)
	if got[1].Kind != STRING || got[1].Text != `"#tag"` {
		t.Fatalf("# in string treated as comment: %s", dump(got))
	}
}

// ---------------------------------------------------------------------------
// Sequence and mapping literals inside CODE (spec 07.5).
// ---------------------------------------------------------------------------

func TestSequenceLiteral(t *testing.T) {
	assertToks(t, "{{ [1, 2, 3] }}", []tok{
		{kind: OPEN_INTERP},
		{kind: LBRACKET},
		{kind: INT, text: "1"},
		{kind: COMMA},
		{kind: INT, text: "2"},
		{kind: COMMA},
		{kind: INT, text: "3"},
		{kind: RBRACKET},
		{kind: CLOSE_INTERP},
		{kind: EOF},
	})
}

func TestNestedBracketsBalance(t *testing.T) {
	// nested map inside seq inside interpolation; the deep '}' must not close.
	got := lexToks(t, "{{ [{a: 1}, {b: 2}] }}")
	if got[len(got)-2].Kind != CLOSE_INTERP {
		t.Fatalf("nested brackets did not balance: %s", dump(got))
	}
}

// ---------------------------------------------------------------------------
// Escapes in TEXT: \{ \} \\ (spec 01 Section 1.2 escape 3).
// ---------------------------------------------------------------------------

func TestTextEscapes(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`\{{`, "{{"},          // escaped open suppresses sigil
		{`\}`, "}"},            // escaped close
		{`\\`, "\\"},           // escaped backslash
		{`a\{b`, "a{b"},        // brace escape mid-text
		{`C:\path`, `C:\path`}, // lone backslash before non-brace is literal
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := lexToks(t, c.in)
			if got[0].Kind != TEXT || got[0].Text != c.want {
				t.Fatalf("escape %q -> %q, want %q (%s)", c.in, got[0].Text, c.want, dump(got))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Verbatim regions (spec 06).
// ---------------------------------------------------------------------------

func TestVerbatimBraced(t *testing.T) {
	in := "@verbatim {\nMap<String,Integer> m = new HashMap<>() {{\nput(\"a\", 1);\n}};\n@}\n"
	got := lexToks(t, in)
	// STMT verbatim, BLOCK_OPEN, VERBATIM(body), BLOCK_CLOSE, EOF
	assertKinds(t, got, []Kind{STMT, BLOCK_OPEN, VERBATIM, BLOCK_CLOSE, EOF})
	body := got[2].Text
	if !strings.Contains(body, "{{") || !strings.Contains(body, "}};") {
		t.Fatalf("verbatim body should contain literal braces unscanned: %q", body)
	}
}

func TestVerbatimBracedBalances(t *testing.T) {
	// inner balanced braces must not end the region early.
	in := "@verbatim {\nfunc f() {\n  g()\n}\n@}\nAFTER"
	got := lexToks(t, in)
	assertKinds(t, got, []Kind{STMT, BLOCK_OPEN, VERBATIM, BLOCK_CLOSE, TEXT, EOF})
	if !strings.Contains(got[2].Text, "func f() {") {
		t.Fatalf("verbatim body lost content: %q", got[2].Text)
	}
	if got[4].Text != "AFTER" {
		t.Fatalf("text after verbatim wrong: %q", got[4].Text)
	}
}

func TestVerbatimFenced(t *testing.T) {
	in := "@verbatim ~~~JAVA\nlone } unbalanced { braces\n~~~JAVA\nAFTER"
	got := lexToks(t, in)
	assertKinds(t, got, []Kind{STMT, VERBATIM, TEXT, EOF})
	if got[1].Text != "lone } unbalanced { braces\n" {
		t.Fatalf("fenced body wrong: %q", got[1].Text)
	}
	if got[2].Text != "AFTER" {
		t.Fatalf("text after fence wrong: %q", got[2].Text)
	}
}

func TestUnterminatedVerbatimBraced(t *testing.T) {
	got := lexToks(t, "@verbatim {\nbody never closes")
	if got[len(got)-2].Kind != ERROR {
		t.Fatalf("want ERROR for unterminated verbatim, got %s", dump(got))
	}
}

// ---------------------------------------------------------------------------
// Positions: line and column tracking (spec 01 Section 1.8).
// ---------------------------------------------------------------------------

func TestPositions(t *testing.T) {
	got := lexToks(t, "ab\n{{ x }}")
	// TEXT "ab\n" starts at 1:1; OPEN_INTERP at 2:1; NAME x at 2:4.
	if got[0].Line != 1 || got[0].Col != 1 {
		t.Fatalf("TEXT pos: got %d:%d want 1:1", got[0].Line, got[0].Col)
	}
	if got[1].Line != 2 || got[1].Col != 1 {
		t.Fatalf("OPEN_INTERP pos: got %d:%d want 2:1", got[1].Line, got[1].Col)
	}
	if got[2].Text != "x" || got[2].Line != 2 || got[2].Col != 4 {
		t.Fatalf("NAME pos: got %s at %d:%d want x at 2:4", got[2].Text, got[2].Line, got[2].Col)
	}
}

func TestCRLFNormalizedPositions(t *testing.T) {
	// CRLF normalized to LF by source.New; positions identical to LF input.
	a := lexToks(t, "a\nb\n{{ x }}")
	b := lexToks(t, "a\r\nb\r\n{{ x }}")
	if len(a) != len(b) {
		t.Fatalf("CRLF changed token count: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Line != b[i].Line || a[i].Col != b[i].Col {
			t.Fatalf("token %d position differs LF vs CRLF: %d:%d vs %d:%d",
				i, a[i].Line, a[i].Col, b[i].Line, b[i].Col)
		}
	}
}

// ---------------------------------------------------------------------------
// Brace-dense emitted-source sample: literal braces stay TEXT, only {{ }} is CODE
// (design/lexical Section 4.1 worked example).
// ---------------------------------------------------------------------------

func TestBraceDenseSourceEmission(t *testing.T) {
	in := "class DoublyLinkedList {\n" +
		"{{ 1 | tab }}public void insertNode({{ TYPE }} d) {\n" +
		"{{ 2 | tab }}if (this.head == null) {\n" +
		"{{ 3 | tab }}this.head = node;\n" +
		"{{ 2 | tab }}} else {\n" +
		"{{ 2 | tab }}}\n" +
		"{{ 1 | tab }}}\n" +
		"}\n"
	got := lexToks(t, in)
	// Every CODE region is an interpolation; there must be no STMT, BLOCK_OPEN,
	// or BLOCK_CLOSE token at all -- all braces in the Java are literal TEXT.
	for _, tk := range got {
		switch tk.Kind {
		case STMT, BLOCK_OPEN, BLOCK_CLOSE, STMT_END:
			t.Fatalf("brace-dense source produced a statement token %s: %s", tk, dump(got))
		}
	}
	// Reassemble TEXT + rendered placeholders and confirm the literal closing
	// brace soup survived as TEXT.
	var sb strings.Builder
	for _, tk := range got {
		if tk.Kind == TEXT {
			sb.WriteString(tk.Text)
		}
	}
	if !strings.Contains(sb.String(), "} else {") {
		t.Fatalf("literal brace soup lost from TEXT: %q", sb.String())
	}
}

// assertKinds compares the kind sequence of got against want.
func assertKinds(t *testing.T, got []Token, want []Kind) {
	t.Helper()
	gk := kinds(got)
	if len(gk) != len(want) {
		t.Fatalf("kind count: got %d want %d\n got: %v", len(gk), len(want), dump(got))
	}
	for i := range want {
		if gk[i] != want[i] {
			t.Fatalf("kind %d: got %s want %s\n got: %s", i, gk[i], want[i], dump(got))
		}
	}
}

// ---------------------------------------------------------------------------
// Same-line branch continuations: "@} elseif {" / "@} else {" close one branch
// body and re-open the construct (spec 01 Section 4.1). The lexer emits one
// BLOCK_CLOSE per branch followed by the continuation STMT head.
// ---------------------------------------------------------------------------

func TestBlockCloseContinuation(t *testing.T) {
	in := "@if a {\nx\n@} elseif b {\ny\n@} else {\nz\n@}\n"
	want := []tok{
		{kind: STMT, text: "if"},
		{kind: NAME, text: "a"},
		{kind: BLOCK_OPEN},
		{kind: TEXT, text: "x\n"},
		{kind: BLOCK_CLOSE},
		{kind: STMT, text: "elseif"},
		{kind: NAME, text: "b"},
		{kind: BLOCK_OPEN},
		{kind: TEXT, text: "y\n"},
		{kind: BLOCK_CLOSE},
		{kind: STMT, text: "else"},
		{kind: BLOCK_OPEN},
		{kind: TEXT, text: "z\n"},
		{kind: BLOCK_CLOSE},
		{kind: EOF},
	}
	assertToks(t, in, want)
}

func TestForElseContinuation(t *testing.T) {
	in := "@for x in xs {\na\n@} else {\nb\n@}\n"
	got := lexToks(t, in)
	want := []Kind{STMT, NAME, NAME, NAME, BLOCK_OPEN, TEXT, BLOCK_CLOSE,
		STMT, BLOCK_OPEN, TEXT, BLOCK_CLOSE, EOF}
	gk := kinds(got)
	if len(gk) != len(want) {
		t.Fatalf("count: got %d want %d\n got: %s", len(gk), len(want), dump(got))
	}
	for i := range want {
		if gk[i] != want[i] {
			t.Fatalf("kind %d: got %s want %s\n got: %s", i, gk[i], want[i], dump(got))
		}
	}
	// A lone "@}" (no continuation) still closes as before.
	assertToks(t, "@for x in xs {\na\n@}\n", []tok{
		{kind: STMT, text: "for"}, {kind: NAME, text: "x"}, {kind: NAME, text: "in"},
		{kind: NAME, text: "xs"}, {kind: BLOCK_OPEN}, {kind: TEXT, text: "a\n"},
		{kind: BLOCK_CLOSE}, {kind: EOF},
	})
}

// ---------------------------------------------------------------------------
// Head map-literal disambiguation: a "{" that opens a mapping literal in a
// statement head (a depth-zero "{" containing ":" / "..." / empty, or a "{...}="
// destructuring target) stays CODE, while the body "{" opens the block.
// ---------------------------------------------------------------------------

func TestHeadMapLiteralVsBodyOpen(t *testing.T) {
	// @with { x: 1 } { body } -- the first "{...}" is a map, the second is the body.
	assertToks(t, "@with { x: 1 } {\nbody\n@}\n", []tok{
		{kind: STMT, text: "with"},
		{kind: LBRACE}, {kind: NAME, text: "x"}, {kind: COLON}, {kind: INT, text: "1"}, {kind: RBRACE},
		{kind: BLOCK_OPEN},
		{kind: TEXT, text: "body\n"},
		{kind: BLOCK_CLOSE},
		{kind: EOF},
	})
}

func TestHeadEmptyMapLiteral(t *testing.T) {
	// @with {} only { body } -- "{}" is an empty map, not a body open.
	got := kinds(lexToks(t, "@with {} only {\nb\n@}\n"))
	want := []Kind{STMT, LBRACE, RBRACE, NAME, BLOCK_OPEN, TEXT, BLOCK_CLOSE, EOF}
	if len(got) != len(want) {
		t.Fatalf("count: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kind %d: got %s want %s", i, got[i], want[i])
		}
	}
}

func TestHeadDestructuringTarget(t *testing.T) {
	// @set {id, label} = rec -- the "{...}" is a destructuring target (followed by
	// "="), so it stays CODE rather than opening a body.
	got := kinds(lexToks(t, "@set {id, label} = rec\n"))
	want := []Kind{STMT, LBRACE, NAME, COMMA, NAME, RBRACE, ASSIGN, NAME, STMT_END, EOF}
	if len(got) != len(want) {
		t.Fatalf("count: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kind %d: got %s want %s", i, got[i], want[i])
		}
	}
}
