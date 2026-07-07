package quill

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/check"
	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// TestTypesRenderIdentically is the binding invariant of the gradual type
// system (spec 04 Section 1, design/type-system.md Section 11.3): removing every
// annotation yields a program that renders byte-for-byte identically. Here the
// SAME data flows through an annotated and an unannotated form of one template;
// the bytes must match exactly, proving the annotations are observationally
// inert on the success path.
func TestTypesRenderIdentically(t *testing.T) {
	untyped := "@for u in users {\n{{ u.name | upper }}: {{ u.age + 1 }}\n@}"
	typed := "@types {\n  users: list<Object<\"User\">>\n@}\n@for u: Object<\"User\"> in users {\n{{ u.name | upper }}: {{ u.age + 1 }}\n@}"

	reg := check.NewRegistry()
	reg.AddType(&check.ObjectType{
		Name:      "User",
		Members:   map[string]*check.Type{"name": check.String, "age": check.Int},
		Stringify: true,
	})

	users := mkList(
		mkMap("name", runtime.Str("ada"), "age", runtime.Int(40)),
		mkMap("name", runtime.Str("bob"), "age", runtime.Int(31)),
	)
	vars := map[string]runtime.Value{"users": users}

	plain := NewWithArray(nil)
	gotUntyped, err := plain.RenderString("u.ql", untyped, vars)
	if err != nil {
		t.Fatalf("untyped render: %v", err)
	}

	withTypes := NewWithArray(nil, WithTypes(reg))
	gotTyped, err := withTypes.RenderString("t.ql", typed, vars)
	if err != nil {
		t.Fatalf("typed render: %v", err)
	}

	if gotUntyped != gotTyped {
		t.Fatalf("annotations changed output:\nuntyped=%q\n  typed=%q", gotUntyped, gotTyped)
	}
	if !strings.Contains(gotTyped, "ADA: 41") {
		t.Fatalf("unexpected output: %q", gotTyped)
	}
}

// TestCheckTimeRejection proves an ill-typed template is rejected at Load/Render
// (before any byte is emitted) with a positioned KindTypeCheck error.
func TestCheckTimeRejection(t *testing.T) {
	e := NewWithArray(map[string]string{
		"bad.ql": "@types {\n  s: string\n@}\n{{ s + 1 }}",
	})
	out, err := e.Render("bad.ql", map[string]runtime.Value{"s": runtime.Str("x")})
	if err == nil {
		t.Fatalf("expected a type error, got output %q", out)
	}
	if errors.KindOf(err) != errors.KindTypeCheck {
		t.Fatalf("expected KindTypeCheck, got %v: %v", errors.KindOf(err), err)
	}
	if out != "" {
		t.Fatalf("no bytes must be emitted on a check-time rejection, got %q", out)
	}
	if !strings.Contains(err.Error(), "bad.ql:") {
		t.Fatalf("error not positioned: %v", err)
	}
}

// TestUnannotatedUnaffectedByRegistry proves a registry does not change an
// unannotated template's behavior: it still renders the dynamic floor.
func TestUnannotatedUnaffectedByRegistry(t *testing.T) {
	reg := check.NewRegistry()
	reg.AddType(&check.ObjectType{Name: "User", Stringify: true})
	e := NewWithArray(nil, WithTypes(reg))
	out, err := e.RenderString("t.ql", "{{ a + b }}", map[string]runtime.Value{
		"a": runtime.Int(2), "b": runtime.Int(3),
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "5" {
		t.Fatalf("got %q want 5", out)
	}
}
