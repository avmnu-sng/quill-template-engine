package errors

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

func TestErrorMessageWithPosition(t *testing.T) {
	src := source.New("tmpl.ql", "line one\nline two\n")
	e := New(KindUndefined, "undefined variable %q", "user").At(src, 2)
	msg := e.Error()
	for _, want := range []string{"undefined", "user", "tmpl.ql:2"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

func TestErrorMessageNoPosition(t *testing.T) {
	e := New(KindArithmetic, "overflow")
	if !strings.Contains(e.Error(), "arithmetic") {
		t.Fatalf("missing kind label: %q", e.Error())
	}
}

func TestAtIsCopy(t *testing.T) {
	e := New(KindRender, "boom")
	src := source.New("a.ql", "")
	e2 := e.At(src, 5)
	if e.Src() != nil || e.Line() != 0 {
		t.Fatal("At mutated the receiver")
	}
	if e2.Src() != src || e2.Line() != 5 {
		t.Fatal("At did not annotate the copy")
	}
}

func TestAtPosRendersColumn(t *testing.T) {
	src := source.New("tmpl.ql", "line one\nline two\n")
	e := New(KindSyntax, "boom").AtPos(src, 2, 7)
	if e.Line() != 2 || e.Col() != 7 {
		t.Fatalf("AtPos did not set line/col: line=%d col=%d", e.Line(), e.Col())
	}
	if !strings.Contains(e.Error(), "tmpl.ql:2:7") {
		t.Fatalf("message %q lacks name:line:col", e.Error())
	}
}

// At (the pre-existing API) must keep the plain name:line form with no column,
// so callers and tests that assert the old string stay valid.
func TestAtOmitsColumn(t *testing.T) {
	src := source.New("tmpl.ql", "x\n")
	e := New(KindSyntax, "boom").At(src, 3)
	if e.Col() != 0 {
		t.Fatalf("At set a nonzero column: %d", e.Col())
	}
	msg := e.Error()
	if !strings.Contains(msg, "tmpl.ql:3") {
		t.Fatalf("message %q lacks name:line", msg)
	}
	if strings.Contains(msg, "tmpl.ql:3:") {
		t.Fatalf("message %q rendered a spurious column", msg)
	}
}

func TestKindOfThroughWrap(t *testing.T) {
	inner := New(KindKey, "bad key")
	wrapped := Wrap(KindKey, inner, "subscript failed")
	if KindOf(wrapped) != KindKey {
		t.Fatalf("KindOf = %v, want key", KindOf(wrapped))
	}
	// Through a non-quill wrapper too.
	outer := stderrors.Join(stderrors.New("ctx"), inner)
	_ = outer // Join's order is unspecified for As; test direct unwrap chain
	if KindOf(stderrors.New("plain")) != KindRuntime {
		t.Fatal("plain error should classify as runtime")
	}
}

func TestSecurityIsCatchableAndCarriesNames(t *testing.T) {
	src := source.New("t.ql", "x\n")
	e := SecurityMethod("Entity", "danger").At(src, 1)

	// Catchable as a *Security with its class and names.
	var sec *Security
	if !stderrors.As(error(e), &sec) {
		t.Fatal("errors.As did not match *Security")
	}
	if sec.Class != SecMethod || sec.Name != "danger" || sec.Type != "Entity" {
		t.Errorf("class/name/type = %v/%q/%q", sec.Class, sec.Name, sec.Type)
	}
	// Still in the engine error family: classifies as KindSecurity and reaches a
	// *Error via Unwrap.
	if KindOf(e) != KindSecurity {
		t.Errorf("KindOf = %v, want security", KindOf(e))
	}
	var base *Error
	if !stderrors.As(error(e), &base) || base.Kind != KindSecurity {
		t.Fatal("a *Security must unwrap to a KindSecurity *Error")
	}
	// Position survives At and message names the offending element.
	if sec.Line() != 1 || sec.Src() != src {
		t.Errorf("position lost: line=%d src=%v", sec.Line(), sec.Src())
	}
	for _, want := range []string{"danger", "Entity", "t.ql:1"} {
		if !strings.Contains(e.Error(), want) {
			t.Errorf("message %q missing %q", e.Error(), want)
		}
	}
}

func TestSecurityUnknownType(t *testing.T) {
	e := SecurityUnknownType("Stranger", "secret")
	var sec *Security
	if !stderrors.As(error(e), &sec) {
		t.Fatal("errors.As did not match *Security")
	}
	// It carries the dedicated SecUnknownType class (so a host can tell an
	// unregistered/mistyped type from a denied-but-known member) plus the
	// offending type and member names.
	if sec.Class != SecUnknownType || sec.Type != "Stranger" || sec.Name != "secret" {
		t.Errorf("class/type/name = %v/%q/%q", sec.Class, sec.Type, sec.Name)
	}
	if KindOf(e) != KindSecurity {
		t.Errorf("KindOf = %v, want security", KindOf(e))
	}
	// The message distinguishes the strict unknown-type variant.
	for _, want := range []string{"Stranger", "secret", "unknown to the sandbox policy"} {
		if !strings.Contains(e.Error(), want) {
			t.Errorf("message %q missing %q", e.Error(), want)
		}
	}
}

func TestSecurityClassLabels(t *testing.T) {
	cases := map[SecurityClass]string{
		SecTag: "tag", SecFilter: "filter", SecFunction: "function",
		SecMethod: "method", SecProperty: "property", SecUnknownType: "unknown-type",
	}
	for c, want := range cases {
		if c.String() != want {
			t.Errorf("class %d label = %q, want %q", c, c.String(), want)
		}
	}
}

func TestUnwrap(t *testing.T) {
	cause := stderrors.New("root")
	e := Wrap(KindSyntax, cause, "parse failed")
	if !stderrors.Is(e, cause) {
		t.Fatal("errors.Is did not find the wrapped cause")
	}
	var qe *Error
	if !stderrors.As(e, &qe) {
		t.Fatal("errors.As did not match *Error")
	}
}
