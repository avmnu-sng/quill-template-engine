package interp

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/cache"
	"github.com/avmnu-sng/quill-template-engine/pkg/cover"
	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
	"github.com/avmnu-sng/quill-template-engine/pkg/sandbox"
)

// stubEngine is a minimal Engine for interp unit tests: it holds an in-memory
// template map and the core extension set. It avoids importing the quill facade
// (which would be a cycle) so the interpreter is testable in isolation.
type stubEngine struct {
	tmpls   map[string]string
	exts    *ext.Set
	strict  bool
	autoht  bool
	seed    int64
	seedSet bool
	rcache  *cache.RenderCache

	policy    *sandbox.Policy
	sandboxOn bool
	cov       *cover.Collector

	tabWidth int
	logger   *log.Logger
}

func newStub(tmpls map[string]string) *stubEngine {
	return &stubEngine{tmpls: tmpls, exts: ext.Core(), strict: true, tabWidth: 4}
}

func (s *stubEngine) Extensions() *ext.Set  { return s.exts }
func (s *stubEngine) StrictVariables() bool { return s.strict }
func (s *stubEngine) AutoescapeHTML() bool  { return s.autoht }
func (s *stubEngine) TemplateExists(name string) bool {
	_, ok := s.tmpls[name]
	return ok
}
func (s *stubEngine) LoadTemplate(ctx context.Context, name string) (*Template, error) {
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

func (s *stubEngine) RawSource(name string) (string, bool) {
	body, ok := s.tmpls[name]
	return body, ok
}
func (s *stubEngine) CompileString(ctx context.Context, name, body string) (*Template, error) {
	mod, err := parse.ParseString(name, body)
	if err != nil {
		return nil, err
	}
	return Prepare(name, mod), nil
}
func (s *stubEngine) RandomSeed() (int64, bool) { return s.seed, s.seedSet }
func (s *stubEngine) RenderCache() *cache.RenderCache {
	if s.rcache == nil {
		s.rcache = cache.NewRenderCache()
	}
	return s.rcache
}
func (s *stubEngine) Policy() *sandbox.Policy    { return s.policy }
func (s *stubEngine) SandboxActive() bool        { return s.sandboxOn }
func (s *stubEngine) Coverage() *cover.Collector { return s.cov }
func (s *stubEngine) TabWidth() int {
	if s.tabWidth == 0 {
		return 4
	}
	return s.tabWidth
}
func (s *stubEngine) Logger() *log.Logger {
	if s.logger == nil {
		s.logger = log.New(io.Discard, "", 0)
	}
	return s.logger
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
	out, err := Render(context.Background(), eng, Prepare("test", mod), vars)
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
	_, err := Render(context.Background(), eng, Prepare("t", mod), map[string]runtime.Value{
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

// TestMatchesOperator covers the regex membership operator over the stdlib RE2
// engine: a successful match, a non-match, RE2 inline flags, an invalid pattern
// (a clear error, not a panic), and a non-string subject (a type error).
func TestMatchesOperator(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		body string
		vars map[string]runtime.Value
		want string
	}{
		{`{{ "err: boom" matches "^err" ? "y" : "n" }}`, nil, "y"},
		{`{{ "ok" matches "^err" ? "y" : "n" }}`, nil, "n"},
		{`{{ "ERR" matches "(?i)^err$" ? "y" : "n" }}`, nil, "y"},
		{`{{ s matches "[0-9]{3}" ? "y" : "n" }}`,
			map[string]runtime.Value{"s": runtime.Str("a123b")}, "y"},
	}
	for _, c := range cases {
		t.Run(c.body, func(t *testing.T) {
			if got := renderStub(t, eng, c.body, c.vars); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}

	// An invalid LITERAL pattern is rejected at Prepare time (spec 01 Section 3,
	// "validated at compile time"), so PrepareChecked errors before any render.
	mod, _ := parse.ParseString("t", `{{ "x" matches "(" }}`)
	if _, err := PrepareChecked("t", mod); err == nil {
		t.Error("invalid literal RE2 pattern must be rejected at compile time")
	}

	// The compile-time check does not depend on branch reachability: a bad literal
	// pattern inside a never-taken branch is still an error.
	mod, _ = parse.ParseString("t", "@if false {\n{{ \"x\" matches \"(\" }}\n@}\ndone\n")
	if _, err := PrepareChecked("t", mod); err == nil {
		t.Error("bad literal pattern in an unreachable branch must still be rejected")
	}

	// A non-string subject is a type error, not a silent coercion (render time).
	mod, _ = parse.ParseString("t", `{{ 42 matches "4" }}`)
	if _, err := Render(context.Background(), eng, Prepare("t", mod), nil); err == nil {
		t.Error("matches over a non-string subject must error")
	}

	// A dynamic (non-literal) bad pattern is not knowable at compile time, so it
	// is a render-time error, not something PrepareChecked rejects.
	mod, _ = parse.ParseString("t", `{{ "x" matches p }}`)
	tmpl, err := PrepareChecked("t", mod)
	if err != nil {
		t.Fatalf("dynamic pattern must not be rejected at compile time: %v", err)
	}
	if _, err := Render(context.Background(), eng, tmpl, map[string]runtime.Value{"p": runtime.Str("(")}); err == nil {
		t.Error("invalid dynamic RE2 pattern must error at render time")
	}
}
