package parse

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/lex"
)

// Deeply nested grouping that stays below maxDepth must parse without the former
// O(n^2) parenIsArrow blowup. Before the match-table fix, this class of input
// drove peak RSS toward ~1GB and multi-second CPU that grew quadratically; the
// O(1) table makes it prompt. We assert completion and success, not wall-clock
// numbers (which are flaky).
func TestDeepNestedGroupingParsesWithoutBlowup(t *testing.T) {
	const n = 3000 // safely under maxDepth so the grouping is legitimate
	src := "{{ " + strings.Repeat("(", n) + "1" + strings.Repeat(")", n) + " }}"
	if _, err := ParseString("deep", src); err != nil {
		t.Fatalf("valid deep grouping failed to parse: %v", err)
	}
}

// A malformed deep-paren run (openers with no matching closers) must fail
// promptly with a positioned KindSyntax error rather than hanging. The old
// forward-scanning parenIsArrow made this quadratic; the fix returns quickly.
func TestDeepUnbalancedGroupingReportsSyntaxError(t *testing.T) {
	const n = 3000
	src := "{{ " + strings.Repeat("(", n) + "1 }}"
	_, err := ParseString("deep", src)
	if err == nil {
		t.Fatal("expected a syntax error for unbalanced deep grouping")
	}
	if errors.KindOf(err) != errors.KindSyntax {
		t.Fatalf("kind = %v, want syntax", errors.KindOf(err))
	}
}

// A pathologically deep expression (well past maxDepth) must be rejected by the
// recursion-depth guard with a positioned KindSyntax "nested too deeply" error,
// not crash the process with a Go stack overflow.
func TestExpressionDepthCapRejectsPathologicalNesting(t *testing.T) {
	const n = maxDepth + 500
	src := "{{ " + strings.Repeat("(", n) + "1" + strings.Repeat(")", n) + " }}"
	_, err := ParseString("deep", src)
	if err == nil {
		t.Fatal("expected a depth-cap syntax error")
	}
	if errors.KindOf(err) != errors.KindSyntax {
		t.Fatalf("kind = %v, want syntax", errors.KindOf(err))
	}
	if !strings.Contains(err.Error(), "nested too deeply") {
		t.Fatalf("error %q lacks the depth-cap message", err.Error())
	}
}

// The depth guard also covers structural block nesting so a tower of nested
// blocks cannot exhaust the stack; it must surface the same positioned syntax
// error.
func TestBlockDepthCapRejectsPathologicalNesting(t *testing.T) {
	const n = maxDepth + 100
	src := strings.Repeat("@if x {\n", n) + strings.Repeat("@}\n", n)
	_, err := ParseString("deep", src)
	if err == nil {
		t.Fatal("expected a depth-cap syntax error for nested blocks")
	}
	if errors.KindOf(err) != errors.KindSyntax {
		t.Fatalf("kind = %v, want syntax", errors.KindOf(err))
	}
	if !strings.Contains(err.Error(), "nested too deeply") {
		t.Fatalf("error %q lacks the depth-cap message", err.Error())
	}
}

// parenIsArrow treats (), [], and {} as one interchangeable depth counter, so
// the match table must match a '(' to whichever bracket first brings depth back
// to zero. This mixed case exercises that the O(1) table reproduces the old
// forward scan's cross-kind matching exactly.
func TestMatchTableCrossKindBalancing(t *testing.T) {
	p := &parser{toks: []lex.Token{
		{Kind: lex.LPAREN},   // 0
		{Kind: lex.LBRACKET}, // 1
		{Kind: lex.RPAREN},   // 2
		{Kind: lex.RBRACKET}, // 3
		{Kind: lex.EOF},      // 4
	}}
	p.buildMatch()
	// The '(' at 0 pairs with the ']' at 3 (its depth returns to zero there),
	// mirroring the pre-fix forward scan.
	if got := p.match[0]; got != 3 {
		t.Fatalf("match[0] = %d, want 3", got)
	}
	if got := p.match[1]; got != 2 {
		t.Fatalf("match[1] = %d, want 2", got)
	}
}
