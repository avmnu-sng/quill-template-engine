package errors

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// assertSecurity checks the invariants every *Security constructor must satisfy:
// the exact Class/Name/Type triple, that the message contains the expected
// fragments, that Error() renders that same message, that it stays in the engine
// error family (KindOf and Unwrap reach a KindSecurity *Error), and that it is
// catchable as *Security via errors.As. It returns the message for callers that
// want extra assertions.
func assertSecurity(t *testing.T, s *Security, wantClass SecurityClass, wantType, wantName string, wantFrags ...string) string {
	t.Helper()

	if s.Class != wantClass {
		t.Errorf("Class = %v, want %v", s.Class, wantClass)
	}
	if s.Name != wantName {
		t.Errorf("Name = %q, want %q", s.Name, wantName)
	}
	if s.Type != wantType {
		t.Errorf("Type = %q, want %q", s.Type, wantType)
	}

	// Error() must render exactly the wrapped *Error's message.
	if s.Error() != s.err.Error() {
		t.Errorf("Error() = %q, want it to equal wrapped err.Error() %q", s.Error(), s.err.Error())
	}
	msg := s.Error()
	for _, frag := range wantFrags {
		if !strings.Contains(msg, frag) {
			t.Errorf("message %q missing fragment %q", msg, frag)
		}
	}

	// Stays in the engine error family: classifies as KindSecurity.
	if KindOf(s) != KindSecurity {
		t.Errorf("KindOf = %v, want KindSecurity", KindOf(s))
	}

	// Unwrap reaches the wrapped *Error, which is KindSecurity.
	if s.Unwrap() != error(s.err) {
		t.Errorf("Unwrap() = %v, want the wrapped *Error", s.Unwrap())
	}
	var base *Error
	if !stderrors.As(error(s), &base) {
		t.Fatalf("errors.As(&Error{}) did not reach the wrapped *Error")
	}
	if base.Kind != KindSecurity {
		t.Errorf("wrapped *Error.Kind = %v, want KindSecurity", base.Kind)
	}

	// Catchable as *Security.
	var sec *Security
	if !stderrors.As(error(s), &sec) {
		t.Fatalf("errors.As(&Security{}) did not match")
	}
	if sec != s {
		t.Errorf("errors.As returned %p, want the original %p", sec, s)
	}
	return msg
}

func TestSecurityTagBehavior(t *testing.T) {
	s := SecurityTag("for")
	assertSecurity(t, s, SecTag, "", "for",
		`statement "for" is not allowed by the sandbox policy`)

	// Tag/filter/function violations name no host type.
	if s.Type != "" {
		t.Errorf("Type = %q, want empty for a tag violation", s.Type)
	}
	// The kind label appears in the rendered message.
	if !strings.Contains(s.Error(), "quill security error:") {
		t.Errorf("message %q lacks the security kind label", s.Error())
	}
}

func TestSecurityFilterBehavior(t *testing.T) {
	s := SecurityFilter("upcase")
	assertSecurity(t, s, SecFilter, "", "upcase",
		`filter "upcase" is not allowed by the sandbox policy`)
	if s.Type != "" {
		t.Errorf("Type = %q, want empty for a filter violation", s.Type)
	}
}

func TestSecurityFunctionBehavior(t *testing.T) {
	s := SecurityFunction("range")
	assertSecurity(t, s, SecFunction, "", "range",
		`function "range" is not allowed by the sandbox policy`)
	if s.Type != "" {
		t.Errorf("Type = %q, want empty for a function violation", s.Type)
	}
}

func TestSecurityPropertyBehavior(t *testing.T) {
	s := SecurityProperty("Entity", "salary")
	assertSecurity(t, s, SecProperty, "Entity", "salary",
		`property "salary" on type "Entity" is not allowed by the sandbox policy`)
	// A property violation names both the type and the property.
	if s.Type != "Entity" {
		t.Errorf("Type = %q, want %q", s.Type, "Entity")
	}
}

func TestSecurityMethodBehavior(t *testing.T) {
	// Method spelling is preserved case-sensitively per the doc comment.
	s := SecurityMethod("Entity", "DoDanger")
	assertSecurity(t, s, SecMethod, "Entity", "DoDanger",
		`method "DoDanger" on type "Entity" is not allowed by the sandbox policy`)
}

func TestSecurityUnknownTypeBehavior(t *testing.T) {
	// The unknown-type variant carries the dedicated SecUnknownType class while
	// its message names the unregistered type.
	s := SecurityUnknownType("Stranger", "run")
	assertSecurity(t, s, SecUnknownType, "Stranger", "run",
		`type "Stranger" is unknown to the sandbox policy (strict mode); access to "run" is denied`)
}

// TestSecurityAtAttachesPosition covers the *Security.At path: it must copy
// (leaving the receiver unpositioned), attach source+line to the wrapped *Error,
// and preserve the Class/Name/Type triple. The rendered message gains the
// name:line locator.
func TestSecurityAtAttachesPosition(t *testing.T) {
	src := source.New("policy.ql", "a\nb\nc\n")
	s := SecurityFilter("upcase")

	positioned := s.At(src, 3)

	// Receiver is untouched (At returns a copy).
	if s.err.Src() != nil || s.err.Line() != 0 {
		t.Fatalf("At mutated the receiver: src=%v line=%d", s.err.Src(), s.err.Line())
	}
	// The copy carries position and the same identifying fields.
	if positioned.Src() != src {
		t.Errorf("Src() = %v, want %v", positioned.Src(), src)
	}
	if positioned.Line() != 3 {
		t.Errorf("Line() = %d, want 3", positioned.Line())
	}
	if positioned.Class != SecFilter || positioned.Name != "upcase" {
		t.Errorf("class/name lost after At: %v/%q", positioned.Class, positioned.Name)
	}
	// .At goes through *Error.At -> AtPos(.., 0), so no column is rendered.
	if positioned.err.Col() != 0 {
		t.Errorf("At set a nonzero column: %d", positioned.err.Col())
	}
	msg := positioned.Error()
	if !strings.Contains(msg, "policy.ql:3") {
		t.Errorf("message %q lacks the name:line locator", msg)
	}
	if strings.Contains(msg, "policy.ql:3:") {
		t.Errorf("message %q rendered a spurious column", msg)
	}
}

// TestSecurityAtNilReceiver covers the nil-receiver guard in *Security.At.
func TestSecurityAtNilReceiver(t *testing.T) {
	var s *Security
	if got := s.At(source.New("x.ql", ""), 1); got != nil {
		t.Fatalf("nil.At() = %v, want nil", got)
	}
}

// TestSecurityAtPosThroughWrappedError exercises AtPos (with the new column) on
// the *Error wrapped inside a *Security, confirming the column reaches the
// rendered message via the name:line:col form.
func TestSecurityAtPosThroughWrappedError(t *testing.T) {
	src := source.New("policy.ql", "alpha\nbeta\n")
	s := SecurityProperty("Entity", "salary")

	// AtPos on the wrapped *Error attaches the 1-based column.
	positioned := s.err.AtPos(src, 2, 4)
	if positioned.Line() != 2 || positioned.Col() != 4 {
		t.Fatalf("AtPos did not set line/col: line=%d col=%d", positioned.Line(), positioned.Col())
	}
	if !strings.Contains(positioned.Error(), "policy.ql:2:4") {
		t.Errorf("message %q lacks name:line:col", positioned.Error())
	}
	// The original wrapped error is unchanged (AtPos is a copy).
	if s.err.Src() != nil || s.err.Col() != 0 {
		t.Fatalf("AtPos mutated the wrapped receiver: src=%v col=%d", s.err.Src(), s.err.Col())
	}
}

// TestErrorAtPosNilReceiver covers the nil-receiver guard on the wrapped
// *Error's position machinery that (*Security).At delegates through. AtPos on a
// nil *Error must yield nil (rather than panic), and At -- which forwards to
// AtPos with a zero column -- must too.
func TestErrorAtPosNilReceiver(t *testing.T) {
	src := source.New("policy.ql", "x\n")
	var e *Error
	if got := e.AtPos(src, 1, 1); got != nil {
		t.Fatalf("nil.AtPos() = %v, want nil", got)
	}
	if got := e.At(src, 1); got != nil {
		t.Fatalf("nil.At() = %v, want nil", got)
	}
}

// TestSecurityErrorNoPositionForm asserts that a freshly constructed *Security
// (no source attached) renders the bare "quill security error: <msg>" form with
// no trailing location parenthetical -- the default branch of *Error.Error when
// Src is nil and Line is 0.
func TestSecurityErrorNoPositionForm(t *testing.T) {
	s := SecurityTag("for")
	msg := s.Error()
	want := `quill security error: statement "for" is not allowed by the sandbox policy`
	if msg != want {
		t.Errorf("Error() = %q, want exactly %q", msg, want)
	}
	// No location parenthetical is appended when position is unknown.
	if strings.Contains(msg, "(") {
		t.Errorf("message %q rendered a spurious location parenthetical", msg)
	}
}
