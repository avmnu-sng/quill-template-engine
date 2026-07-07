package parse

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
)

// The anchor (spec 00-overview Section 2) must parse to a well-formed module with
// the expected top-level shape: extends, a block containing a for-loop, and a
// macro. This is the acceptance anchor for the slice.
func TestParseAnchor(t *testing.T) {
	anchor := strings.Join([]string{
		`@extends "base.tmpl"`,
		``,
		`@block body {`,
		`  @for u in users {`,
		`    {{ u.name | upper }}{{ ", admin" if u.isAdmin }}`,
		`  @}`,
		`@}`,
		``,
		`@macro greet(name) {`,
		`  Hello {{ name | default("guest") }}`,
		`@}`,
	}, "\n") + "\n"

	mod, err := ParseString("anchor", anchor)
	if err != nil {
		t.Fatalf("anchor parse failed: %v", err)
	}
	if mod.Kind != ast.KindModule {
		t.Fatalf("root is %s, want Module", mod.Kind)
	}

	// Collect the meaningful (non-text) top-level statements.
	var stmts []*ast.Node
	for _, c := range mod.Children {
		if c.Kind != ast.KindText {
			stmts = append(stmts, c)
		}
	}
	if len(stmts) != 3 {
		t.Fatalf("want 3 top-level statements, got %d: %s", len(stmts), ast.Dump(mod))
	}
	if stmts[0].Kind != ast.KindExtends {
		t.Fatalf("stmt 0 = %s, want Extends", stmts[0].Kind)
	}
	if stmts[1].Kind != ast.KindBlock || stmts[1].Str != "body" {
		t.Fatalf("stmt 1 = %s %q, want Block body", stmts[1].Kind, stmts[1].Str)
	}
	if stmts[2].Kind != ast.KindMacro || stmts[2].Str != "greet" {
		t.Fatalf("stmt 2 = %s %q, want Macro greet", stmts[2].Kind, stmts[2].Str)
	}

	// The block contains the for-loop; the loop holds the postfix-conditional print.
	block := stmts[1]
	var forNode *ast.Node
	for _, c := range block.Children {
		if c.Kind == ast.KindFor {
			forNode = c
		}
	}
	if forNode == nil {
		t.Fatalf("block has no for-loop: %s", ast.Dump(block))
	}
	dump := ast.Dump(forNode)
	if !strings.Contains(dump, "(Filter upper (Attr .name (Name u)))") {
		t.Fatalf("for-loop missing the upper-filtered name: %s", dump)
	}
	if !strings.Contains(dump, `(Ternary (Attr .isAdmin (Name u)) (String ", admin") (String ""))`) {
		t.Fatalf("for-loop missing the desugared postfix conditional: %s", dump)
	}
}

func TestParseIf(t *testing.T) {
	src := "@if a {\nx\n@} elseif b {\ny\n@} else {\nz\n@}\n"
	got := parseDump(t, src)
	// Expect an If with three clauses: if(a), elseif(b), else.
	for _, want := range []string{
		"(If ",
		"(Clause if (Name a)",
		"(Clause if (Name b)",
		"(Clause else",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("if dump missing %q: %s", want, got)
		}
	}
}

func TestParseFor(t *testing.T) {
	tests := []struct{ src, want string }{
		{
			src:  "@for v in seq {\nx\n@}\n",
			want: "targets=1 else=false (Target v) (Name seq) (Body",
		},
		{
			src:  "@for k, v in m {\nx\n@}\n",
			want: "targets=2 else=false (Target k) (Target v) (Name m) (Body",
		},
		{
			src:  "@for x in xs {\na\n@} else {\nb\n@}\n",
			want: "targets=1 else=true",
		},
		{
			src:  "@for u: Object<\"User\"> in users {\nx\n@}\n",
			want: `(Target u (Type Object (String "User")))`,
		},
	}
	for _, tc := range tests {
		got := parseDump(t, tc.src)
		if !strings.Contains(got, tc.want) {
			t.Fatalf("for %q dump missing %q: %s", tc.src, tc.want, got)
		}
	}
}

func TestParseSetForms(t *testing.T) {
	tests := []struct{ src, want string }{
		{"@set name = u.name\n", "(Set targets=1 (Target name) (Attr .name (Name u)))"},
		{"@set a, b = e1, e2\n", "(Set targets=2 (Target a) (Target b) (Name e1) (Name e2))"},
		{"@set count: int = n\n", "(Set targets=1 (Target count (Type int)) (Name n))"},
		{"@set [x, y] = pair\n", "(Set targets=1 (ListPattern (Target x) (Target y)) (Name pair))"},
		{"@set {id, label} = rec\n", "(Set targets=1 (MapPattern (MapTarget id) (MapTarget label)) (Name rec))"},
		// Optional slot "b?" wraps its target in an Optional node.
		{"@set [a, b?] = xs\n", "(Set targets=1 (ListPattern (Target a) (Optional (Target b))) (Name xs))"},
		// A leading elided slot renders as a nil child "_".
		{"@set [, b] = xs\n", "(Set targets=1 (ListPattern _ (Target b)) (Name xs))"},
		// An interior elided slot keeps the surrounding required slots.
		{"@set [a, , c] = xs\n", "(Set targets=1 (ListPattern (Target a) _ (Target c)) (Name xs))"},
		// Optional and elided slots compose with a tail capture.
		{"@set [a, b?, ...rest] = xs\n", "(Set targets=1 (ListPattern (Target a) (Optional (Target b)) (Spread (Name rest))) (Name xs))"},
		// A nested pattern in an optional slot, and a slot type annotation.
		{"@set [a: int, [b, c]?] = xs\n", "(Set targets=1 (ListPattern (Target a (Type int)) (Optional (ListPattern (Target b) (Target c)))) (Name xs))"},
		// A member-set target (the mutable-cell form) parses the receiver chain as
		// the target rather than binding a plain name.
		{"@set c.value = 1\n", "(Set targets=1 (Attr .value (Name c)) (Int 1))"},
		{"@set c.value = c.value + 1\n", "(Set targets=1 (Attr .value (Name c)) (Binary + (Attr .value (Name c)) (Int 1)))"},
		{"@set m[\"k\"] = 1\n", "(Set targets=1 (Index (Name m) (String \"k\")) (Int 1))"},
		{"@set a.b.c = 1\n", "(Set targets=1 (Attr .c (Attr .b (Name a))) (Int 1))"},
	}
	for _, tc := range tests {
		got := parseDump(t, tc.src)
		// The Set node is the first non-empty top item.
		if !strings.Contains(got, tc.want) {
			t.Fatalf("set %q\n got: %s\nwant substring: %s", tc.src, got, tc.want)
		}
	}
}

func TestParseCapture(t *testing.T) {
	src := "@set banner = capture {\nheader for {{ target }}\n@}\n"
	got := parseDump(t, src)
	if !strings.Contains(got, "(Capture banner") {
		t.Fatalf("capture dump missing Capture node: %s", got)
	}
	if !strings.Contains(got, "(Print (Name target))") {
		t.Fatalf("capture body missing the interpolation: %s", got)
	}
}

func TestParseWithAndApply(t *testing.T) {
	with := parseDump(t, "@with { x: 1 } only {\nbody\n@}\n")
	if !strings.Contains(with, "(With ") || !strings.Contains(with, "(Map") {
		t.Fatalf("with dump unexpected: %s", with)
	}
	apply := parseDump(t, "@apply | trim | upper {\nhi\n@}\n")
	if !strings.Contains(apply, "(Apply (ApplyFilter trim) (ApplyFilter upper)") {
		t.Fatalf("apply dump unexpected: %s", apply)
	}
}

func TestParseSimpleStatements(t *testing.T) {
	tests := []struct{ src, want string }{
		{"@do log.append(u)\n", "(Do (Call (Attr .append (Name log)) (Arg (Name u))))"},
		{"@flush\n", "(Flush)"},
		{`@deprecated "old" since "2.0"` + "\n", `(Deprecated "old" (String "2.0"))`},
		{"@line 42\n", "(Line 42)"},
		{"@escape html {\n<p>{{ x }}</p>\n@}\n", "(Escape html"},
		{"@sandbox {\nx\n@}\n", "(Sandbox"},
	}
	for _, tc := range tests {
		got := parseDump(t, tc.src)
		if !strings.Contains(got, tc.want) {
			t.Fatalf("%q\n got: %s\nwant substring: %s", tc.src, got, tc.want)
		}
	}
}

func TestParseGuard(t *testing.T) {
	src := "@guard filter(\"markdown\") {\n{{ body | markdown }}\n@} else {\n{{ body }}\n@}\n"
	got := parseDump(t, src)
	if !strings.Contains(got, `(Guard filter (String "markdown")`) {
		t.Fatalf("guard head unexpected: %s", got)
	}
	if !strings.Contains(got, "(Clause else") {
		t.Fatalf("guard else clause missing: %s", got)
	}
}

func TestParseTypes(t *testing.T) {
	src := "@types {\nx: string, n: int\n@}\n"
	got := parseDump(t, src)
	if !strings.Contains(got, "(TypeDecl x (Type string))") ||
		!strings.Contains(got, "(TypeDecl n (Type int))") {
		t.Fatalf("types dump unexpected: %s", got)
	}
}

func TestParseCache(t *testing.T) {
	src := "@cache key=\"header\" ttl=3600 {\nx\n@}\n"
	got := parseDump(t, src)
	if !strings.Contains(got, `(CacheArg key (String "header"))`) ||
		!strings.Contains(got, "(CacheArg ttl (Int 3600))") {
		t.Fatalf("cache dump unexpected: %s", got)
	}
}

func TestParseComposition(t *testing.T) {
	tests := []struct{ src, want string }{
		{`@extends "base.ql"` + "\n", `(Extends (String "base.ql"))`},
		{`@extends ["a.ql", "b.ql"]` + "\n", `(Extends (List (String "a.ql") (String "b.ql")))`},
		{`@block title "Default"` + "\n", `(Block title (String "Default"))`},
		{"@block summary -> string {\nx\n@}\n", "(Block summary (Type string)"},
		{"@macro greet(name, greeting: string = \"Hi\", ...rest) {\nx\n@}\n",
			`(Macro greet (Params (Param name) (Param greeting (Type string) (String "Hi")) (Param ...rest))`},
		{`@import "forms.ql" as forms` + "\n", `(Import forms (String "forms.ql"))`},
		{`@import _self as me` + "\n", `(Import me (SpecialName _self))`},
		{`@from "forms.ql" import input, label as lbl` + "\n",
			`(From (String "forms.ql") (FromItem input) (FromItem label as (Name lbl)))`},
		{`@use "buttons.ql" with { submit: ok }` + "\n", `(Use (String "buttons.ql") (Map`},
		{`@include "header.ql"` + "\n", `(Include (String "header.ql"))`},
		{`@include "row.ql" with { user: u } only` + "\n", "(Include with,only"},
		{`@include "maybe.ql" ignore missing` + "\n", "(Include ignore-missing"},
		{"@embed \"card.ql\" with { title: t } {\n@block body {\nx\n@}\n@}\n", "(Embed with"},
	}
	for _, tc := range tests {
		got := parseDump(t, tc.src)
		if !strings.Contains(got, tc.want) {
			t.Fatalf("%q\n got: %s\nwant substring: %s", tc.src, got, tc.want)
		}
	}
}

func TestParseVerbatim(t *testing.T) {
	src := "@verbatim {\nMap<String,Integer> m = new HashMap<>() {{\n}};\n@}\n"
	mod, err := ParseString("t", src)
	if err != nil {
		t.Fatalf("verbatim parse: %v", err)
	}
	var v *ast.Node
	for _, c := range mod.Children {
		if c.Kind == ast.KindVerbatim {
			v = c
		}
	}
	if v == nil {
		t.Fatalf("no verbatim node: %s", ast.Dump(mod))
	}
	// The double-brace block is copied byte-for-byte and never scanned.
	if !strings.Contains(v.Str, "new HashMap<>() {{") {
		t.Fatalf("verbatim body not captured literally: %q", v.Str)
	}
}

// The canonical trailing-comma loop idiom (design/control-flow Section 3.4)
// exercises trim modifiers, the postfix conditional, and loop.last in one
// brace-dense template.
func TestParseTrailingCommaIdiom(t *testing.T) {
	src := "[{{- \"\" -}}\n@for v in values {\n  {{- v }}{{ \", \" if not loop.last -}}\n@}\n{{- \"\" -}}]\n"
	got := parseDump(t, src)
	if !strings.Contains(got, "(For ") {
		t.Fatalf("idiom missing for-loop: %s", got)
	}
	// not loop.last desugars into the postfix conditional ternary.
	if !strings.Contains(got, `(Ternary (Unary not (Attr .last (Name loop))) (String ", ") (String ""))`) {
		t.Fatalf("idiom missing the not-loop.last conditional: %s", got)
	}
}

// A nested loop reads the outer loop via loop.parent.loop (design/control-flow 3.4).
func TestParseNestedLoopParent(t *testing.T) {
	src := "@for row in m {\n@for cell in row {\n{{ loop.parent.loop.index }}\n@}\n@}\n"
	got := parseDump(t, src)
	if !strings.Contains(got, "(Attr .index (Attr .loop (Attr .parent (Name loop))))") {
		t.Fatalf("nested loop.parent.loop chain missing: %s", got)
	}
}
