package lex

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// seedTemplates is a spread of template fragments covering TEXT, interpolation,
// comments, statement heads, verbatim, trim modifiers, string interpolation,
// numeric and collection literals, arrow functions, and a batch of adversarial
// or unbalanced inputs that must fault cleanly rather than panic. It seeds the
// lexer fuzz target below; package parse keeps its own parallel corpus.
var seedTemplates = []string{
	"",
	"hello world",
	"line one\nline two\n",
	"{{ name }}",
	"{{ user.name | upper }}",
	"{{ a + b * c ** d - e }}",
	"{{ items | join(', ') | upper }}",
	"{{ (a or b) and not c }}",
	"{{ items | map(x => x * 2) | sum }}",
	"{{ \"hi #{name}, welcome\" }}",
	"{{ 'raw #{no interp}' }}",
	"{{ 0x1f + 1_000 + 1.5e10 }}",
	"{{ [1, 2, 3][0] }}",
	"{{ {a: 1, b: 2}['a'] }}",
	"{{ 1 .. 5 }}",
	"{{ a ?? b ?: c }}",
	"{{ obj?.field?[0] }}",
	"{{- trimmed -}}",
	"{{~ line trimmed ~}}",
	"@set x = 1\n",
	"@set a, b = 1, 2\n",
	"@if x {\n  yes\n@}\n",
	"@if x {\n  a\n@} @else {\n  b\n@}\n",
	"@for i in items {\n  {{ i }}\n@}\n",
	"@for k, v in mapping {\n  {{ k }}={{ v }}\n@}\n",
	"@macro greet(name) {\n  Hi {{ name }}\n@}\n",
	"{# a comment #}",
	"@verbatim {\n  {{ not code }}\n@}\n",
	"prefix {{ x }} suffix",
	"escaped \\{ not a sigil \\}",
	// Adversarial / unbalanced inputs: the lexer must fault cleanly, never panic.
	"{{",
	"{#",
	"{{ x",
	"@if x {",
	"{{ ( }}",
	"{{ ) }}",
	"{{ [1, }}",
	"@}",
	"{{ 1 + }}",
	"{{ \"unterminated }}",
	"{{ ((((((((((1)))))))))) }}",
}

// FuzzLex asserts that the lexer never panics and always emits a well-formed
// token stream for arbitrary input: at least a trailing EOF sentinel, positions
// that stay 1-based, and any lexical fault surfaced as a single ERROR token
// sitting immediately before that EOF -- the contract documented on TokenStream.
func FuzzLex(f *testing.F) {
	for _, s := range seedTemplates {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, code string) {
		ts := Lex(source.New("fuzz", code))
		if ts == nil {
			t.Fatal("Lex returned a nil TokenStream")
		}
		toks := ts.Tokens
		if len(toks) == 0 {
			t.Fatal("Lex produced no tokens; expected at least a trailing EOF")
		}
		if last := toks[len(toks)-1]; last.Kind != EOF {
			t.Fatalf("token stream does not end in EOF; last token is %s", last)
		}
		for i, tok := range toks {
			// Positions are documented as 1-based (spec 01 Section 1.8); a token
			// anchored at line/col 0 would point diagnostics nowhere.
			if tok.Line < 1 || tok.Col < 1 {
				t.Fatalf("token %d (%s) has a non-positive position: line=%d col=%d", i, tok, tok.Line, tok.Col)
			}
			// A fault is terminal: the only ERROR permitted is the token right
			// before the final EOF. Any other placement (or a second ERROR)
			// breaks the single-fault contract consumers rely on.
			if tok.Kind == ERROR && i != len(toks)-2 {
				t.Fatalf("ERROR token at index %d is not immediately before EOF (stream length %d)", i, len(toks))
			}
		}
	})
}
