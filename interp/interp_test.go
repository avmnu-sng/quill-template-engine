package interp

import (
	"strings"
	"testing"

	"github.com/avmnusng/quill-template-engine/ext"
	"github.com/avmnusng/quill-template-engine/parse"
	"github.com/avmnusng/quill-template-engine/runtime"
)

// stubEngine is a minimal Engine for interp unit tests: it holds an in-memory
// template map and the core extension set. It avoids importing the quill facade
// (which would be a cycle) so the interpreter is testable in isolation.
type stubEngine struct {
	tmpls  map[string]string
	exts   *ext.ExtensionSet
	strict bool
	autoht bool
}

func newStub(tmpls map[string]string) *stubEngine {
	return &stubEngine{tmpls: tmpls, exts: ext.Core(), strict: true}
}

func (s *stubEngine) Extensions() *ext.ExtensionSet { return s.exts }
func (s *stubEngine) StrictVariables() bool         { return s.strict }
func (s *stubEngine) AutoescapeHTML() bool          { return s.autoht }
func (s *stubEngine) TemplateExists(name string) bool {
	_, ok := s.tmpls[name]
	return ok
}
func (s *stubEngine) LoadTemplate(name string) (*Template, error) {
	body, ok := s.tmpls[name]
	if !ok {
		return nil, errNotFound(name)
	}
	mod, err := parse.ParseString(name, body)
	if err != nil {
		return nil, err
	}
	return Prepare(name, mod), nil
}

func errNotFound(name string) error { return &notFoundErr{name} }

type notFoundErr struct{ name string }

func (e *notFoundErr) Error() string { return "template " + e.name + " not found" }

// renderStub renders an ad-hoc template against a stub engine.
func renderStub(t *testing.T, eng *stubEngine, body string, vars map[string]runtime.Value) string {
	t.Helper()
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	out, err := Render(eng, Prepare("test", mod), vars)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	return out
}

func TestRenderText(t *testing.T) {
	eng := newStub(nil)
	if got := renderStub(t, eng, "plain { and } text", nil); got != "plain { and } text" {
		t.Errorf("bare braces must be literal: %q", got)
	}
}

// hostUser is a host Object exercising GetField, CallMethod, and Iterable.
type hostUser struct {
	name  string
	roles []string
}

func (u *hostUser) GetField(name string) (runtime.Value, bool) {
	if name == "name" {
		return runtime.Str(u.name), true
	}
	return runtime.Null(), false
}

func (u *hostUser) CallMethod(name string, args []runtime.Value) (runtime.Value, error) {
	if name == "shout" {
		return runtime.Str(strings.ToUpper(u.name)), nil
	}
	return runtime.Null(), errNotFound(name)
}

func (u *hostUser) Iterate() []runtime.Pair {
	out := make([]runtime.Pair, 0, len(u.roles))
	for i, r := range u.roles {
		out = append(out, runtime.Pair{Key: runtime.Int(int64(i)), Val: runtime.Str(r)})
	}
	return out
}

func TestHostObjectFieldAndMethod(t *testing.T) {
	eng := newStub(nil)
	u := runtime.Obj(&hostUser{name: "ada", roles: []string{"admin", "user"}})
	if got := renderStub(t, eng, "{{ u.name }}", map[string]runtime.Value{"u": u}); got != "ada" {
		t.Errorf("field access: %q", got)
	}
	if got := renderStub(t, eng, "{{ u.shout() }}", map[string]runtime.Value{"u": u}); got != "ADA" {
		t.Errorf("method call: %q", got)
	}
	got := renderStub(t, eng, "@for r in u {\n[{{ r }}]\n@}\n", map[string]runtime.Value{"u": u})
	if !strings.Contains(got, "[admin]") || !strings.Contains(got, "[user]") {
		t.Errorf("iterable host object: %q", got)
	}
}

func TestArrayRenderIsError(t *testing.T) {
	eng := newStub(nil)
	mod, _ := parse.ParseString("t", "{{ xs }}")
	_, err := Render(eng, Prepare("t", mod), map[string]runtime.Value{
		"xs": runtime.Arr(runtime.NewList(runtime.Int(1))),
	})
	if err == nil {
		t.Fatal("rendering an array as text must be an error (spec 04 Section 5)")
	}
}

func TestPrepareTables(t *testing.T) {
	mod, err := parse.ParseString("t",
		"@block outer {\n@block inner {\nX\n@}\n@}\n@macro m() {\nY\n@}\n")
	if err != nil {
		t.Fatal(err)
	}
	tmpl := Prepare("t", mod)
	// Nested blocks are flat in the table (design/composition Section 2.4).
	if !tmpl.HasBlock("outer") || !tmpl.HasBlock("inner") {
		t.Errorf("nested blocks not flattened: %v", tmpl.BlockNames())
	}
	if !tmpl.HasMacro("m") {
		t.Error("macro not indexed")
	}
}
