package check

import (
	"strings"
	"testing"

	"github.com/avmnusng/quill-template-engine/errors"
	"github.com/avmnusng/quill-template-engine/parse"
	"github.com/avmnusng/quill-template-engine/source"
)

// checkSrc parses body under name "t.ql" and runs the checker with reg.
func checkSrc(t *testing.T, body string, reg *Registry) error {
	t.Helper()
	mod, err := parse.Parse(source.New("t.ql", body))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return Check(mod, reg)
}

// userRegistry builds a registry with one User host type for the nominal tests.
func userRegistry() *Registry {
	r := NewRegistry()
	r.AddType(&ObjectType{
		Name:      "User",
		Members:   map[string]*Type{"name": String, "age": Int, "active": Bool},
		Methods:   map[string]*Signature{"label": {Params: []*Type{String}, Ret: String}},
		ElemType:  nil,
		Stringify: true,
	})
	return r
}

// TestWellTyped covers templates that must PASS the checker (no error). These
// are the success-path cases: a correct annotated template type-checks and (by
// the binding invariant) renders unchanged.
func TestWellTyped(t *testing.T) {
	cases := []struct {
		name string
		src  string
		reg  *Registry
	}{
		{"untyped is silent", `{{ users }}{{ x.y.z }}{{ a + b }}`, nil},
		{"types-block string render", "@types {\n  title: string\n@}\n{{ title }}", nil},
		{"int arithmetic", "@types {\n  n: int\n@}\n{{ n + 1 }}", nil},
		{"set inferred", "@set c = 1\n{{ c + 2 }}", nil},
		{"set annotated consistent", "@set c: int = 5\n{{ c }}", nil},
		{"set from any backstop", "@types {\n  raw: any\n@}\n@set n: int = raw\n{{ n * 2 }}", nil},
		{"for over typed list", "@types {\n  xs: list<int>\n@}\n@for x in xs {\n{{ x + 1 }}\n@}", nil},
		{"for annotated agrees", "@types {\n  xs: list<int>\n@}\n@for x: int in xs {\n{{ x }}\n@}", nil},
		{"for over map two targets", "@types {\n  m: map<string, int>\n@}\n@for k, v in m {\n{{ k }}{{ v + 1 }}\n@}", nil},
		{"nullable default coalesces", "@types {\n  title: string?\n@}\n{{ title | default(\"x\") }}", nil},
		{"coalesce removes null", "@types {\n  title: string?\n@}\n{{ title ?? \"x\" }}", nil},
		{"narrow is int", "@types {\n  x: int | string\n@}\n{{ x + 1 if x is int }}", nil},
		{"narrow is string", "@types {\n  x: int | string\n@}\n{{ x | upper if x is string }}", nil},
		{"union both renderable", "@types {\n  x: int | string\n@}\n{{ x }}", nil},
		{"macro typed call ok", "@macro g(name: string) {\nHi {{ name }}\n@}\n{{ g(\"ada\") }}", nil},
		{"upper on string", "@types {\n  s: string\n@}\n{{ s | upper }}", nil},
		{"length on list", "@types {\n  xs: list<int>\n@}\n@set c: int = xs | length\n{{ c }}", nil},
		{"join on list", "@types {\n  xs: list<string>\n@}\n{{ xs | join(\", \") }}", nil},
		{"map arrow propagates", "@types {\n  xs: list<int>\n@}\n{{ xs | map((x) => x + 1) | join(\",\") }}", nil},
		{"object member ok", "@types {\n  u: Object<\"User\">\n@}\n{{ u.name | upper }}", userRegistry()},
		{"object int member", "@types {\n  u: Object<\"User\">\n@}\n{{ u.age + 1 }}", userRegistry()},
		{"list of object map", "@types {\n  us: list<Object<\"User\">>\n@}\n{{ us | map((u) => u.name) | join(\",\") }}", userRegistry()},
		{"object method ok", "@types {\n  u: Object<\"User\">\n@}\n{{ u.label(\"hi\") }}", userRegistry()},
		{"nullsafe yields nullable", "@types {\n  u: Object<\"User\">?\n@}\n{{ u?.name ?? \"anon\" }}", userRegistry()},
		{"object opaque without registry", "@types {\n  u: Object<\"Whatever\">\n@}\n{{ u.anything }}", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSrc(t, tc.src, tc.reg); err != nil {
				t.Fatalf("expected well-typed, got error: %v", err)
			}
		})
	}
}

// TestIllTyped covers templates the checker must REJECT, each promoting a
// specific runtime error to load time. The wantSub substring pins the message so
// a diagnostic stays clear and positioned.
func TestIllTyped(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		reg     *Registry
		wantSub string
	}{
		{"string plus int", "@types {\n  s: string\n@}\n{{ s + 1 }}", nil, "requires a number"},
		{"render a list", "@types {\n  xs: list<int>\n@}\n{{ xs }}", nil, "cannot render"},
		{"render a map", "@types {\n  m: map<string, int>\n@}\n{{ m }}", nil, "cannot render"},
		{"for over int", "@types {\n  n: int\n@}\n@for x in n {\n{{ x }}\n@}", nil, "cannot iterate"},
		{"set type mismatch", "@set c: int = \"x\"\n{{ c }}", nil, "cannot assign"},
		{"for annotation disagrees", "@types {\n  xs: list<int>\n@}\n@for x: string in xs {\n{{ x }}\n@}", nil, "declared as"},
		{"unknown object member", "@types {\n  u: Object<\"User\">\n@}\n{{ u.naem }}", userRegistry(), "no member"},
		{"unknown object method", "@types {\n  u: Object<\"User\">\n@}\n{{ u.frob() }}", userRegistry(), "no method"},
		{"unknown host type", "@types {\n  u: Object<\"Ghost\">\n@}\n{{ u }}", userRegistry(), "unknown host type"},
		{"bad map key type", "@types {\n  m: map<bool, int>\n@}\n{{ m }}", nil, "map key type must be"},
		{"macro arity", "@macro g(name: string) {\n{{ name }}\n@}\n{{ g() }}", nil, "at least"},
		{"macro arg type", "@macro g(name: string) {\n{{ name }}\n@}\n{{ g(42) }}", nil, "is expected"},
		{"upper on int", "@types {\n  n: int\n@}\n{{ n | upper }}", nil, "is expected"},
		{"union not narrowed renders", "@types {\n  x: int | list<int>\n@}\n{{ x }}", nil, "cannot render"},
		{"object no member without method", "@types {\n  u: Object<\"User\">\n@}\n{{ u.label }}", userRegistry(), "no member"},
		{"arrow param disagrees", "@types {\n  xs: list<int>\n@}\n{{ xs | map((x: string) => x) }}", nil, "declared as"},
		{"bad default value", "@macro g(name: string = 42) {\n{{ name }}\n@}", nil, "not consistent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSrc(t, tc.src, tc.reg)
			if err == nil {
				t.Fatalf("expected a type error, got none")
			}
			if errors.KindOf(err) != errors.KindTypeCheck {
				t.Fatalf("expected KindTypeCheck, got %v: %v", errors.KindOf(err), err)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
			// Every diagnostic must be positioned at t.ql:line.
			if !strings.Contains(err.Error(), "t.ql:") {
				t.Fatalf("error %q is not positioned", err.Error())
			}
		})
	}
}

// TestUnannotatedNeverErrors fuzzes a spread of dynamic templates that would be
// runtime errors but must be SILENT at check time (the dynamic floor): no
// annotation means all-any, so the checker makes no claim.
func TestUnannotatedNeverErrors(t *testing.T) {
	srcs := []string{
		`{{ a + b }}`,
		`{{ xs }}`,
		`@for x in n {\n{{ x }}\n@}`,
		`{{ s | upper }}`,
		`{{ u.name | upper }}{{ ", admin" if u.isAdmin }}`,
		`@set total = 0\n@for n in nums {\n@set total = total + n\n@}\n{{ total }}`,
		`{{ obj.method(1, 2) }}`,
		`{{ x ?? y ?? "z" }}`,
	}
	for i, s := range srcs {
		s = strings.ReplaceAll(s, `\n`, "\n")
		if err := checkSrc(t, s, nil); err != nil {
			t.Fatalf("case %d: unannotated template must not error, got: %v", i, err)
		}
	}
}
