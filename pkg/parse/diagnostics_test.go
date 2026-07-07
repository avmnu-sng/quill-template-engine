package parse

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
)

// A syntax fault must carry the offending token's column, rendered as ":line:col"
// in the message (spec 01 Section 1.8). Every lex.Token has an accurate 1-based
// Col; the parser now threads it through failAt.
func TestSyntaxErrorReportsColumn(t *testing.T) {
	_, err := ParseString("t", "{{ a $ b }}")
	if err == nil {
		t.Fatal("expected a syntax error")
	}
	if errors.KindOf(err) != errors.KindSyntax {
		t.Fatalf("kind = %v, want syntax", errors.KindOf(err))
	}
	if !strings.Contains(err.Error(), ":1:") {
		t.Fatalf("error %q lacks a :line:col column", err.Error())
	}
}

// An unterminated interpolation must be reported at the "{{" opener, not at the
// far EOF line. Here the "{{" opens on line 2 and never closes.
func TestUnterminatedInterpReportsOpenerLine(t *testing.T) {
	src := "line one\n{{ a + b\nmore text\nand more\n"
	_, err := ParseString("t", src)
	if err == nil {
		t.Fatal("expected an unterminated-interpolation error")
	}
	qe, ok := err.(*errors.Error)
	if !ok {
		t.Fatalf("error is %T, want *errors.Error", err)
	}
	if qe.Line != 2 {
		t.Fatalf("line = %d, want 2 (the '{{' opener)", qe.Line)
	}
	if !strings.Contains(qe.Msg, "unterminated interpolation") {
		t.Fatalf("message %q is not the unterminated-interpolation error", qe.Msg)
	}
}

// An unclosed block (@if with no @}) must be reported at the block opener, not at
// the far EOF line. Here @if opens on line 2 and never closes.
func TestUnterminatedBlockReportsOpenerLine(t *testing.T) {
	src := "text\n@if cond {\nbody\nmore body\n"
	_, err := ParseString("t", src)
	if err == nil {
		t.Fatal("expected an unclosed-block error")
	}
	qe, ok := err.(*errors.Error)
	if !ok {
		t.Fatalf("error is %T, want *errors.Error", err)
	}
	if qe.Line != 2 {
		t.Fatalf("line = %d, want 2 (the '@if' opener)", qe.Line)
	}
	if !strings.Contains(qe.Msg, "unclosed block") {
		t.Fatalf("message %q is not the unclosed-block error", qe.Msg)
	}
}

// describe must render a delimiter's literal spelling, not its raw enum label:
// "found ')'" rather than "found RPAREN".
func TestDescribeRendersDelimiterSpelling(t *testing.T) {
	_, err := ParseString("t", "{{ ) }}")
	if err == nil {
		t.Fatal("expected a syntax error")
	}
	if !strings.Contains(err.Error(), "')'") {
		t.Fatalf("error %q lacks the ')' spelling", err.Error())
	}
	if strings.Contains(err.Error(), "RPAREN") {
		t.Fatalf("error %q leaked the raw RPAREN enum label", err.Error())
	}
}
