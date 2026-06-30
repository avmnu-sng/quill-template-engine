package parse

import (
	"strings"
	"testing"

	"github.com/avmnusng/quill-template-engine/errors"
)

func TestSyntaxErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"chained comparison", "{{ a == b == c }}", "non-associative"},
		{"chained range", "{{ 1 .. 2 .. 3 }}", "non-associative"},
		{"chained test", "{{ x is even is odd }}", "non-associative"},
		{"positional after named", "{{ f(a: 1, 2) }}", "positional argument may not follow"},
		{"unclosed interp", "{{ a + }}", "expected an expression"},
		{"missing for in", "@for x xs {\n@}\n", "expected 'in'"},
		{"elseif without if", "@elseif x {\n@}\n", "without a matching"},
		{"bad guard kind", "@guard widget(\"x\") {\n@}\n", "must be 'filter', 'function', or 'test'"},
		{"missing block name", "@block {\n@}\n", "block name"},
		{"set count mismatch", "@set a, b = 1\n", "2 targets but 1 values"},
		{"import without as", `@import "x.ql"` + "\n", "expected 'as'"},
		{"unbalanced block at eof", "@if x {\nbody\n", "to close the block"},
		{"stray block close", "@}\n", "no open block"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mustErr(t, tc.src, tc.want)
		})
	}
}

// A syntax error must carry the 1-based line of the offending token and the
// template name (spec 01 Section 1.8).
func TestErrorCarriesLineAndSource(t *testing.T) {
	src := "line one\nline two\n{{ a == b == c }}\n"
	_, err := ParseString("mytemplate", src)
	if err == nil {
		t.Fatal("expected an error")
	}
	qe, ok := err.(*errors.Error)
	if !ok {
		t.Fatalf("error is %T, want *errors.Error", err)
	}
	if qe.Kind != errors.KindSyntax {
		t.Fatalf("kind = %v, want syntax", qe.Kind)
	}
	if qe.Line != 3 {
		t.Fatalf("line = %d, want 3", qe.Line)
	}
	if qe.Src == nil || qe.Src.Name() != "mytemplate" {
		t.Fatalf("source name not attached: %+v", qe.Src)
	}
	if !strings.Contains(err.Error(), "mytemplate:3") {
		t.Fatalf("error text %q lacks template:line", err.Error())
	}
}

// A lexical fault (e.g. an unterminated string) surfaces as a positioned syntax
// error through the parser.
func TestLexErrorSurfaces(t *testing.T) {
	mustErr(t, "{{ \"unterminated }}", "unterminated string")
}
