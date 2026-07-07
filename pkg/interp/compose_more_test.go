package interp

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// renderNamed renders body under name against eng, returning output and error.
func renderNamed(t *testing.T, eng *stubEngine, name, body string, vars map[string]runtime.Value) (string, error) {
	t.Helper()
	mod, err := parse.ParseString(name, body)
	if err != nil {
		t.Fatalf("parse %q: %v", name, err)
	}
	return Render(eng, Prepare(name, mod), vars)
}

// TestIsDefinedTest covers the `is defined` / `is not defined` chain test over a
// bare name, a member, and an index, none of which may throw.
func TestIsDefinedTest(t *testing.T) {
	eng := newStub(nil)
	u := runtime.Obj(&hostUser{name: "ada"})
	xs := runtime.Arr(runtime.NewList(runtime.Int(1)))
	cases := []struct {
		name, body, want string
		vars             map[string]runtime.Value
	}{
		{"absent name is not defined", `{{ "y" if x is defined }}{{ "n" if x is not defined }}`, "n", nil},
		{"present name is defined", `{{ "y" if x is defined }}`, "y",
			map[string]runtime.Value{"x": runtime.Int(1)}},
		{"present member is defined", `{{ "y" if u.name is defined }}`, "y",
			map[string]runtime.Value{"u": u}},
		{"absent member is not defined", `{{ "y" if u.missing is defined }}{{ "n" if u.missing is not defined }}`, "n",
			map[string]runtime.Value{"u": u}},
		{"present index is defined", `{{ "y" if xs[0] is defined }}`, "y",
			map[string]runtime.Value{"xs": xs}},
		{"out-of-range index is not defined", `{{ "n" if xs[9] is not defined }}`, "n",
			map[string]runtime.Value{"xs": xs}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderStub(t, eng, tc.body, tc.vars); got != tc.want {
				t.Fatalf("render = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTestOperators covers named `is` tests over the core test set and the
// negated form, pinning exact truthy output.
func TestTestOperators(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		body, want string
		vars       map[string]runtime.Value
	}{
		{`{{ "y" if 4 is even }}`, "y", nil},
		{`{{ "y" if 3 is odd }}`, "y", nil},
		{`{{ "y" if 3 is not even }}`, "y", nil},
		{`{{ "y" if xs is empty }}`, "y",
			map[string]runtime.Value{"xs": runtime.Arr(runtime.NewArray())}},
		{`{{ "y" if n is null }}`, "y",
			map[string]runtime.Value{"n": runtime.Null()}},
	}
	for _, c := range cases {
		t.Run(c.body, func(t *testing.T) {
			if got := renderStub(t, eng, c.body, c.vars); got != c.want {
				t.Fatalf("%q = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

// TestUnknownTestErrors pins the error message for an unregistered test name.
func TestUnknownTestErrors(t *testing.T) {
	eng := newStub(nil)
	mod, err := parse.ParseString("t", `{{ "y" if x is frobnicated }}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Render(eng, Prepare("t", mod), map[string]runtime.Value{"x": runtime.Int(1)})
	if err == nil || !strings.Contains(err.Error(), "unknown test") {
		t.Fatalf("expected unknown-test error, got %v", err)
	}
}

// TestUndefinedVariableHint checks the strict-undefined error lists the
// available names via joinNames, and the (none) form when the context is empty.
func TestUndefinedVariableHint(t *testing.T) {
	eng := newStub(nil)
	// Non-empty context: the hint lists the available name.
	mod, _ := parse.ParseString("t", `@set present = 1
{{ missing }}`)
	_, err := Render(eng, Prepare("t", mod), nil)
	if err == nil || !strings.Contains(err.Error(), "undefined variable") {
		t.Fatalf("expected undefined error, got %v", err)
	}
	if !strings.Contains(err.Error(), "present") {
		t.Fatalf("hint must list available names, got %q", err.Error())
	}
	// Empty context: the hint reads (none).
	mod2, _ := parse.ParseString("t", `{{ missing }}`)
	_, err = Render(eng, Prepare("t", mod2), nil)
	if err == nil || !strings.Contains(err.Error(), "(none)") {
		t.Fatalf("empty-context hint must be (none), got %v", err)
	}
}

// TestImportMacro covers @import binding a namespace and calling a macro in it.
func TestImportMacro(t *testing.T) {
	eng := newStub(map[string]string{
		"macros.ql": "@macro greet(name) {\nHi {{ name }}\n@}",
	})
	got, err := renderNamed(t, eng, "main", `@import "macros.ql" as m
{{ m.greet("ada") }}`, nil)
	if err != nil {
		t.Fatalf("import render error: %v", err)
	}
	if strings.TrimSpace(got) != "Hi ada" {
		t.Fatalf("import macro = %q, want 'Hi ada'", got)
	}
}

// TestFromImport covers @from importing named macros directly into scope.
func TestFromImport(t *testing.T) {
	eng := newStub(map[string]string{
		"forms.ql": "@macro input(n) {\n<in {{ n }}>\n@}",
	})
	got, err := renderNamed(t, eng, "main", `@from "forms.ql" import input
{{ input("x") }}`, nil)
	if err != nil {
		t.Fatalf("from-import error: %v", err)
	}
	if strings.TrimSpace(got) != "<in x>" {
		t.Fatalf("from import = %q", got)
	}
}

// TestExtendsAndParent covers @extends replacing a block and parent() reaching
// the base block body.
func TestExtendsAndParent(t *testing.T) {
	eng := newStub(map[string]string{
		"base.ql": "TOP\n@block body {\ndefault body\n@}\nBOTTOM",
	})
	// A child block overrides the base block.
	got, err := renderNamed(t, eng, "child.ql", `@extends "base.ql"
@block body {
CHILD BODY
@}`, nil)
	if err != nil {
		t.Fatalf("extends error: %v", err)
	}
	if got != "TOP\nCHILD BODY\nBOTTOM" {
		t.Fatalf("extends = %q", got)
	}
	// parent() reaches the base body.
	got, err = renderNamed(t, eng, "child.ql", `@extends "base.ql"
@block body {
{{ parent() }} + child
@}`, nil)
	if err != nil {
		t.Fatalf("parent error: %v", err)
	}
	if got != "TOP\ndefault body\n + child\nBOTTOM" {
		t.Fatalf("parent() = %q", got)
	}
}

// TestExtendsCandidateList covers @extends with a candidate list, skipping a
// missing template and using the first that resolves.
func TestExtendsCandidateList(t *testing.T) {
	eng := newStub(map[string]string{"real.ql": "REAL"})
	got, err := renderNamed(t, eng, "c.ql", `@extends ["missing.ql", "real.ql"]
`, nil)
	if err != nil {
		t.Fatalf("candidate extends error: %v", err)
	}
	if got != "REAL" {
		t.Fatalf("candidate list = %q, want REAL", got)
	}
}

// TestEmbed covers @embed pulling a base template and overriding a block.
func TestEmbed(t *testing.T) {
	eng := newStub(map[string]string{
		"embedbase.ql": "E<\n@block slot {\ndefault slot\n@}\n>",
	})
	got, err := renderNamed(t, eng, "main", `before
@embed "embedbase.ql" {
@block slot {
FILLED
@}
@}
after`, nil)
	if err != nil {
		t.Fatalf("embed error: %v", err)
	}
	if !strings.Contains(got, "E<\nFILLED\n>") {
		t.Fatalf("embed did not override slot: %q", got)
	}
}

// TestBlockCallForm covers block("name") reading a block's rendered body.
func TestBlockCallForm(t *testing.T) {
	eng := newStub(nil)
	got := renderStub(t, eng, `@block greeting {
Hello
@}
again: {{ block("greeting") }}`, nil)
	if !strings.Contains(got, "again: Hello") {
		t.Fatalf("block() call form = %q", got)
	}
}
