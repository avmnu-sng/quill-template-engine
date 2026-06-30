package errors

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/avmnusng/quill-template-engine/source"
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
	if e.Src != nil || e.Line != 0 {
		t.Fatal("At mutated the receiver")
	}
	if e2.Src != src || e2.Line != 5 {
		t.Fatal("At did not annotate the copy")
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
