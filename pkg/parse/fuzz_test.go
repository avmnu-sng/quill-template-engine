package parse

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
)

// parseSeeds mirrors the lexer corpus (package lex keeps the canonical copy) and
// adds parser-only stressors: deep expression and block nesting near the maxDepth
// guard, and mixed bracket runs that exercise the O(1) bracket-match table and
// the arrow-vs-grouping disambiguation.
var parseSeeds = []string{
	"",
	"hello world",
	"{{ name }}",
	"{{ user.name | upper }}",
	"{{ a + b * c ** d - e }}",
	"{{ -1 ** 0 }}",
	"{{ items | map(x => x * 2) | sum }}",
	"{{ (a, b) => a + b }}",
	"{{ \"hi #{name}, welcome\" }}",
	"{{ 0x1f + 1_000 + 1.5e10 }}",
	"{{ [1, 2, 3][0] }}",
	"{{ {a: 1, b: 2, ...rest}['a'] }}",
	"{{ 1 .. 5 }}",
	"{{ a ?? b ?: c }}",
	"{{ obj?.field?[0] }}",
	"@set x = 1\n",
	"@set a, b = 1, 2\n",
	"@if x {\n  yes\n@} @else {\n  b\n@}\n",
	"@for i in items {\n  {{ i }}\n@}\n",
	"@for k, v in mapping {\n  {{ k }}={{ v }}\n@}\n",
	"@macro greet(name, greeting=\"hi\") {\n  {{ greeting }} {{ name }}\n@}\n",
	"{# a comment #}",
	"@verbatim {\n  {{ not code }}\n@}\n",
	// Adversarial inputs: each must become a positioned KindSyntax error, never
	// a panic or a hang.
	"{{",
	"{{ x",
	"@if x {",
	"{{ ( }}",
	"{{ ) }}",
	"{{ [1, }}",
	"@}",
	"{{ 1 + }}",
	"([)]",
	"{{ ((((((((((1)))))))))) }}",
}

// FuzzParse asserts that the parser never panics or hangs on arbitrary input,
// that every failure is a positioned KindSyntax *errors.Error (never a bare error
// or a different kind), that a nil error always comes with a KindModule root, and
// that parsing is deterministic (identical input yields an identical tree).
//
// The "never panics" invariant has teeth: Parse recovers only *errors.Error and
// re-panics anything else, so an index-out-of-range or nil dereference introduced
// by a future change surfaces here as a fuzz crash.
func FuzzParse(f *testing.F) {
	for _, s := range parseSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, code string) {
		node, err := ParseString("fuzz", code)
		if err != nil {
			if got := errors.KindOf(err); got != errors.KindSyntax {
				t.Fatalf("parse error is not KindSyntax (got %v): %v", got, err)
			}
			return
		}
		if node == nil {
			t.Fatal("Parse returned a nil node with a nil error")
		}
		if node.Kind != ast.KindModule {
			t.Fatalf("successful parse root is %v, want KindModule", node.Kind)
		}
		// Determinism: re-parsing identical input must yield identical success
		// and an identical tree, guarding against map-iteration nondeterminism
		// leaking into AST construction.
		node2, err2 := ParseString("fuzz", code)
		if err2 != nil {
			t.Fatalf("nondeterministic parse: first pass succeeded, second failed: %v", err2)
		}
		if ast.Dump(node) != ast.Dump(node2) {
			t.Fatal("nondeterministic AST: identical input produced differing trees")
		}
	})
}
