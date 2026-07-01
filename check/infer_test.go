package check

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/errors"
)

// nominalRegistry builds a registry with a small nominal type hierarchy plus a
// host callable signature, exercising member/method resolution, supertype
// walking, iteration element types, stringify hooks, and nominal consistency.
func nominalRegistry() *Registry {
	r := NewRegistry()
	r.AddType(&ObjectType{
		Name:      "Animal",
		Members:   map[string]*Type{"legs": Int},
		Methods:   map[string]*Signature{"speak": {Ret: String}},
		Stringify: true,
	})
	r.AddType(&ObjectType{
		Name:       "Dog",
		Members:    map[string]*Type{"name": String},
		Supertypes: []string{"Animal"},
		Stringify:  true,
	})
	r.AddType(&ObjectType{Name: "Bag", ElemType: String, Stringify: false})
	r.AddSignature("hostfn", &Signature{Params: []*Type{Int}, Ret: String})
	return r
}

// TestInferWellTyped adds success-path cases that exercise the previously
// uncovered inference branches (list/map literals, index, slice, unary, concat,
// membership, narrowing, statements, arrows, registry lookups). Each must PASS.
func TestInferWellTyped(t *testing.T) {
	cases := []struct {
		name string
		src  string
		reg  *Registry
	}{
		// list / map literals infer element/value join.
		{"list literal joins", "@set xs = [1, 2, 3]\n{{ xs | join(\",\") }}", nil},
		{"list spread contributes elem", "@types {\n  xs: list<int>\n@}\n@set ys = [0, ...xs]\n{{ ys | length }}", nil},
		{"map keyed and shorthand", "@set a = 1\n@set m = {k: 2, a}\n{{ m | length }}", nil},
		{"map computed key", "@set m = {(1 + 1): \"x\"}\n{{ m | length }}", nil},
		{"map spread", "@types {\n  base: map<string,int>\n@}\n@set m = {a: 1, ...base}\n{{ m | length }}", nil},

		// index / slice.
		{"list index is elem", "@types {\n  xs: list<int>\n@}\n{{ xs[0] + 1 }}", nil},
		{"map index is val", "@types {\n  m: map<string,int>\n@}\n{{ m[\"k\"] + 1 }}", nil},
		{"string index is string", "@types {\n  s: string\n@}\n{{ s[0] | upper }}", nil},
		{"list slice is list", "@types {\n  xs: list<int>\n@}\n{{ xs[1:2] | join(\",\") }}", nil},
		{"string slice is string", "@types {\n  s: string\n@}\n{{ s[1:3] | upper }}", nil},

		// unary.
		{"unary minus preserves int", "@types {\n  n: int\n@}\n{{ -n + 1 }}", nil},
		{"unary plus preserves float", "@types {\n  f: float\n@}\n{{ +f }}", nil},
		{"unary not is bool", "@types {\n  n: int\n@}\n{{ \"y\" if not n }}", nil},

		// concat renderable operands.
		{"concat of renderables", "@types {\n  n: int\n  s: string\n@}\n{{ n ~ s }}", nil},

		// membership with arrow predicate propagates element type.
		{"has some predicate", "@types {\n  xs: list<int>\n@}\n{{ \"y\" if xs has some (x) => x > 0 }}", nil},
		{"in operator", "@types {\n  xs: list<int>\n@}\n{{ \"y\" if 1 in xs }}", nil},

		// higher-order filters element propagation.
		{"reduce accumulator", "@types {\n  xs: list<int>\n@}\n@set s: int = xs | reduce((a, b) => a + b, 0)\n{{ s }}", nil},
		{"find yields nullable", "@types {\n  xs: list<int>\n@}\n{{ (xs | find((x) => x > 2)) ?? 0 }}", nil},
		{"sort keeps list", "@types {\n  xs: list<int>\n@}\n{{ xs | sort((a, b) => a <=> b) | join(\",\") }}", nil},
		{"filter keeps list", "@types {\n  xs: list<int>\n@}\n{{ xs | filter((x) => x > 0) | join(\",\") }}", nil},

		// narrowing: is not string, is not null, is iterable/mapping.
		{"narrow is not string", "@types {\n  x: int | string\n@}\n{{ x + 1 if x is not string }}", nil},
		{"narrow is iterable then join", "@types {\n  x: int | list<int>\n@}\n{{ (x | join(\",\")) if x is iterable }}", nil},
		{"narrow is mapping then iterate", "@types {\n  x: int | map<string,int>\n@}\n@if x is mapping {\n@for k, v in x {\n{{ v + 1 }}\n@}\n@}", nil},
		{"narrow and both", "@types {\n  x: int | string\n  y: int | string\n@}\n{{ (x + y) if x is int and y is int }}", nil},

		// elvis join.
		{"elvis removes null", "@types {\n  s: string?\n@}\n{{ s ?: \"d\" | upper }}", nil},

		// statements: with, apply, guard, sandbox, escape, cache, capture, do.
		{"with body dynamic names", "@with { a: 1 } {\n{{ a }}\n@}", nil},
		{"with only", "@with { a: 1 } only {\n{{ a }}\n@}", nil},
		{"apply body checked", "@types {\n  s: string\n@}\n@apply | upper {\n{{ s }}\n@}", nil},
		{"guard both branches", "@guard filter(\"x\") {\nok\n@} else {\nno\n@}", nil},
		{"sandbox body", "@types {\n  s: string\n@}\n@sandbox {\n{{ s | upper }}\n@}", nil},
		{"escape body", "@types {\n  s: string\n@}\n@escape html {\n{{ s }}\n@}", nil},
		{"cache args and body", "@types {\n  s: string\n@}\n@cache key=s {\n{{ s | upper }}\n@}", nil},
		{"capture typed", "@set x: string = capture {\nhi\n@}\n{{ x | upper }}", nil},
		{"do expression", "@do 1 + 1\n", nil},

		// block: params bind, return recorded, shortcut value form.
		{"block with params", "@block b(x: int) {\n{{ x + 1 }}\n@}", nil},
		{"block shortcut value", "@block title \"hi\"\n", nil},

		// set: multi-target, destructure list/map.
		{"set multi target", "@set a, b = 1, \"x\"\n{{ a + 1 }}{{ b | upper }}", nil},
		{"set list destructure", "@types {\n  xs: list<int>\n@}\n@set [a, b] = xs\n{{ a }}", nil},
		{"set map destructure", "@types {\n  m: map<string,int>\n@}\n@set {x, y} = m\n{{ x }}", nil},

		// registry: inherited member, method, nominal flow, iterable object.
		{"inherited member via supertype", "@types {\n  d: Object<\"Dog\">\n@}\n{{ d.legs + 1 }}", nominalRegistry()},
		{"object method", "@types {\n  a: Object<\"Animal\">\n@}\n{{ a.speak() | upper }}", nominalRegistry()},
		{"nominal subtype flows to supertype param", "@macro f(a: Object<\"Animal\">) {\n{{ a.legs }}\n@}\n@types {\n  d: Object<\"Dog\">\n@}\n{{ f(d) }}", nominalRegistry()},
		{"iterate object with elem type", "@types {\n  b: Object<\"Bag\">\n@}\n@for x in b {\n{{ x | upper }}\n@}", nominalRegistry()},
		{"host function signature", "{{ hostfn(1) }}", nominalRegistry()},
		{"member on union of objects", "@types {\n  x: Object<\"Dog\"> | Object<\"Animal\">\n@}\n{{ x.legs }}", nominalRegistry()},

		// range and bitwise are typed but permissive.
		{"range join", "@set r = 1..5\n{{ r | join(\",\") }}", nil},

		// fused loop filter: the "if cond" clause is checked in the loop scope, so
		// it may reference the loop target(s) with their inferred types.
		{"for if filter references target", "@types {\n  xs: list<int>\n@}\n@for x in xs if x > 0 {\n{{ x + 1 }}\n@}", nil},
		{"for if filter two targets", "@types {\n  m: map<string,int>\n@}\n@for k, v in m if v > 0 {\n{{ k }}={{ v }}\n@}", nil},
		{"for if filter with else", "@types {\n  xs: list<int>\n@}\n@for x in xs if x > 0 {\n{{ x }}\n@} else {\nnone\n@}", nil},
		{"loop changed method", "@types {\n  xs: list<int>\n@}\n@for x in xs {\n{{ loop.changed(x) }}\n@}", nil},
		{"loop prev next", "@types {\n  xs: list<int>\n@}\n@for x in xs {\n{{ loop.prev ?? 0 }}{{ loop.next ?? 0 }}\n@}", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSrc(t, tc.src, tc.reg); err != nil {
				t.Fatalf("expected well-typed, got error: %v", err)
			}
		})
	}
}

// TestInferIllTyped adds rejection cases for the previously uncovered error
// branches, each pinning the exact message substring and a positioned t.ql line.
func TestInferIllTyped(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		reg     *Registry
		wantSub string
	}{
		// index / subscript kind checks.
		{"list bad subscript kind", "@types {\n  xs: list<int>\n@}\n{{ xs[true] }}", nil, "list subscript must be an int, found bool"},
		{"map bad key kind", "@types {\n  m: map<string,int>\n@}\n{{ m[1] }}", nil, "map key must be string, found int"},
		{"subscript a scalar", "@types {\n  n: int\n@}\n{{ n[0] }}", nil, "cannot subscript a value of type int"},

		// slice bound kind.
		{"slice bad bound", "@types {\n  s: string\n@}\n{{ s[true:] | upper }}", nil, "slice bound must be an int, found bool"},

		// unary numeric.
		{"unary minus on string", "@types {\n  s: string\n@}\n{{ -s }}", nil, "unary - requires a number, found string"},

		// concat non-renderable.
		{"concat a map", "@types {\n  m: map<string,int>\n@}\n{{ m ~ \"x\" }}", nil, "cannot concatenate a value of type map<string, int>"},

		// membership arrow declared param disagrees.
		{"has every arrow disagrees", "@types {\n  xs: list<int>\n@}\n{{ \"y\" if xs has every (x: string) => x is empty }}", nil, "declared as string but the pipeline yields int"},

		// higher-order filter over non-collection.
		{"map over int", "@types {\n  n: int\n@}\n{{ n | map((x) => x) }}", nil, "filter \"map\" requires a list, found int"},

		// list literal render.
		{"render list literal", "{{ [1, 2, 3] }}", nil, "cannot render a value of type list<int>"},

		// block shortcut value non-renderable.
		{"block shortcut non-renderable", "@types {\n  xs: list<int>\n@}\n@block b xs\n", nil, "cannot render a block value of type list<int>"},

		// guard body error surfaces (uses an outer typed var).
		{"guard body arith error", "@types {\n  s: string\n@}\n@guard filter(\"x\") {\n{{ s + 1 }}\n@}", nil, "requires a number"},

		// cache body error surfaces.
		{"cache body arith error", "@types {\n  s: string\n@}\n@cache key=s {\n{{ s + 1 }}\n@}", nil, "requires a number"},

		// sandbox / escape bodies checked for renderability.
		{"sandbox render list", "@types {\n  xs: list<int>\n@}\n@sandbox {\n{{ xs }}\n@}", nil, "cannot render"},
		{"escape render map", "@types {\n  m: map<string,int>\n@}\n@escape html {\n{{ m }}\n@}", nil, "cannot render"},

		// do expression error surfaces.
		{"do arith error", "@types {\n  s: string\n@}\n@do s + 1\n", nil, "requires a number"},

		// block default param inconsistency.
		{"block default type mismatch", "@block b(x: int = \"no\") {\n{{ x }}\n@}", nil, "not consistent"},

		// host signature arity and type.
		{"host fn arg type", "{{ hostfn(\"x\") }}", nominalRegistry(), "argument 1 has type string but int is expected"},
		{"host fn too many", "{{ hostfn(1, 2) }}", nominalRegistry(), "at most"},

		// object iteration when not iterable (no ElemType).
		{"iterate non-iterable object", "@types {\n  a: Object<\"Animal\">\n@}\n@for x in a {\n{{ x }}\n@}", nominalRegistry(), "cannot iterate over a value of type Object<\"Animal\">"},

		// method miss on known object.
		{"unknown method on object", "@types {\n  a: Object<\"Animal\">\n@}\n{{ a.frob() }}", nominalRegistry(), "no method"},
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
			if !strings.Contains(err.Error(), "t.ql:") {
				t.Fatalf("error %q is not positioned", err.Error())
			}
		})
	}
}

// TestValidateTypeAnnotations covers the annotation-validation branches: a
// map with a bad key kind, a nested bad type, an empty Object name, an unknown
// nominal host type, and arrow/union recursion.
func TestValidateTypeAnnotations(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		reg     *Registry
		wantSub string
	}{
		{"bad map key type", "@types {\n  m: map<bool, int>\n@}\n{{ m | length }}", nil, "map key type must be int or string"},
		{"nested bad map key", "@types {\n  m: list<map<float, int>>\n@}\n{{ m | length }}", nil, "map key type must be int or string"},
		{"empty object name", "@macro f(x: Object<\"\">) {\n{{ x }}\n@}", nominalRegistry(), "non-empty type name"},
		{"unknown host type", "@types {\n  g: Object<\"Ghost\">\n@}\n{{ g }}", nominalRegistry(), "unknown host type"},
		{"arrow return bad map key", "@macro f(g: (int) => map<bool, int>) {\n{{ g }}\n@}", nil, "map key type must be int or string"},
		{"union member bad map key", "@types {\n  u: int | map<bool, int>\n@}\n{{ u }}", nil, "map key type must be int or string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSrc(t, tc.src, tc.reg)
			if err == nil {
				t.Fatalf("expected a validation error, got none")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}
