package interp

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// TestSeparatorAcrossLoop covers separator() woven through a @for loop: the first
// element gets no glue, every later element is prefixed by the separator, so a
// list joins without a trailing separator.
func TestSeparatorAcrossLoop(t *testing.T) {
	eng := newStub(nil)
	// The loop body emits the separator then the element; the first separator call
	// is empty, so no glue precedes the first element and ", " precedes the rest.
	body := "@set sep = separator(\", \")\n@for n in [1, 2, 3] {\n{{- sep() }}{{ n -}}\n@}"
	got := renderStub(t, eng, body, nil)
	want := "1, 2, 3"
	if got != want {
		t.Errorf("separator join = %q, want %q", got, want)
	}
}

// TestCellAccumulatorSurvivesLoop covers cell(): a mutable accumulator updated
// with @set acc.value = acc.value + w inside a loop body is visible after the
// loop, proving the cell survives the loop-scope clone while a plain @set inside
// the loop still does not leak.
func TestCellAccumulatorSurvivesLoop(t *testing.T) {
	eng := newStub(nil)
	// local is first introduced INSIDE the loop, so it does not survive the loop
	// scope; the cell's own field mutation does. This shows the cell adds
	// accumulation without weakening the default no-leak loop scoping.
	body := "@set acc = cell(0)\n" +
		"@for w in [1, 2, 3, 4] {\n@set acc.value = acc.value + w\n@set local = w\n@}\n" +
		"sum={{ acc.value }} defined={{ local is defined }}"
	got := renderStub(t, eng, body, nil)
	if !strings.Contains(got, "sum=10") {
		t.Errorf("cell accumulation = %q, want sum=10", got)
	}
	if !strings.Contains(got, "defined=false") {
		t.Errorf("loop-local set should not leak: %q", got)
	}
}

// TestCellStringMember covers a cell holding and mutating a string member.
func TestCellStringMember(t *testing.T) {
	eng := newStub(nil)
	body := "@set c = cell(\"a\")\n@set c.value = c.value ~ \"b\"\n{{ c.value }}"
	got := renderStub(t, eng, body, nil)
	if got != "ab" {
		t.Errorf("cell string member = %q, want %q", got, "ab")
	}
}

// TestMemberSetOnArray covers a member-set target against a mapping bound in
// scope: @set m.k = v assigns the string key in place.
func TestMemberSetOnArray(t *testing.T) {
	eng := newStub(nil)
	m := runtime.NewArray()
	m.SetStr("k", runtime.Int(1))
	body := "@set m.k = 5\n{{ m.k }}"
	got := renderStub(t, eng, body, map[string]runtime.Value{"m": runtime.Arr(m)})
	if got != "5" {
		t.Errorf("member set on array = %q, want %q", got, "5")
	}
}

// TestIndexSetOnArray covers a subscript-set target against a mapping.
func TestIndexSetOnArray(t *testing.T) {
	eng := newStub(nil)
	m := runtime.NewArray()
	m.SetStr("k", runtime.Int(1))
	body := "@set m[\"k\"] = 9\n{{ m[\"k\"] }}"
	got := renderStub(t, eng, body, map[string]runtime.Value{"m": runtime.Arr(m)})
	if got != "9" {
		t.Errorf("index set on array = %q, want %q", got, "9")
	}
}

// TestMemberSetImmutableErrors covers a member-set target against a value that
// does not support assignment: a plain int has no members.
func TestMemberSetImmutableErrors(t *testing.T) {
	eng := newStub(nil)
	mod, err := parse.ParseString("t", "@set n.x = 1\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Render(eng, Prepare("t", mod), map[string]runtime.Value{"n": runtime.Int(1)}); err == nil {
		t.Fatal("expected an error assigning a member of an int")
	}
}

// TestCallBoundCallable covers invoking a callable value bound to a bare name:
// name(args) invokes the callable rather than reporting an unknown function.
func TestCallBoundCallable(t *testing.T) {
	eng := newStub(nil)
	body := "@set sep = separator(\"-\")\n{{ sep() }}{{ sep() }}{{ sep() }}"
	got := renderStub(t, eng, body, nil)
	if got != "--" {
		t.Errorf("bound callable calls = %q, want %q", got, "--")
	}
}
