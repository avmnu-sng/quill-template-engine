package parse

import (
	"strings"
	"testing"

	"github.com/avmnusng/quill-template-engine/ast"
	"github.com/avmnusng/quill-template-engine/errors"
)

// parseExprDump parses "{{ expr }}" and returns the Dump of the single inner
// expression, so an expression test asserts against one stable S-expression.
func parseExprDump(t *testing.T, expr string) string {
	t.Helper()
	mod, err := ParseString("t", "{{ "+expr+" }}")
	if err != nil {
		t.Fatalf("parse %q: %v", expr, err)
	}
	print := mod.Child(0)
	if print == nil || print.Kind != ast.KindPrint {
		t.Fatalf("expected a Print node for %q, got %s", expr, ast.Dump(mod))
	}
	return ast.Dump(print.Child(0))
}

// parseDump parses a whole template and returns the module Dump.
func parseDump(t *testing.T, src string) string {
	t.Helper()
	mod, err := ParseString("t", src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return ast.Dump(mod)
}

// mustErr parses src expecting a KindSyntax error whose message contains want,
// and reports the error's line.
func mustErr(t *testing.T, src, want string) {
	t.Helper()
	_, err := ParseString("t", src)
	if err == nil {
		t.Fatalf("expected a syntax error for %q, got none", src)
	}
	if errors.KindOf(err) != errors.KindSyntax {
		t.Fatalf("expected KindSyntax for %q, got %v", src, errors.KindOf(err))
	}
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Fatalf("error for %q = %q, want substring %q", src, err.Error(), want)
	}
}

func TestParseExpressionPrecedence(t *testing.T) {
	tests := []struct {
		expr string
		want string
	}{
		// The power/unary-minus fix (spec 02 R6): one AST rule, no special case.
		{"-1 ** 0", "(Unary - (Power (Int 1) (Int 0)))"},
		{"(-1) ** 2", "(Power (Unary - (Int 1)) (Int 2))"},
		{"2 ** -3", "(Power (Int 2) (Unary - (Int 3)))"},
		{"-2 ** 2", "(Unary - (Power (Int 2) (Int 2)))"},
		{"2 ** 3 ** 2", "(Power (Int 2) (Power (Int 3) (Int 2)))"}, // right-assoc

		// Multiplicative over additive.
		{"1 + 2 * 3", "(Binary + (Int 1) (Binary * (Int 2) (Int 3)))"},
		{"1 * 2 + 3", "(Binary + (Binary * (Int 1) (Int 2)) (Int 3))"},

		// Concat strictly between additive and range.
		{"a + b ~ c + d", "(Binary ~ (Binary + (Name a) (Name b)) (Binary + (Name c) (Name d)))"},
		{"a ~ b .. c", "(Binary .. (Binary ~ (Name a) (Name b)) (Name c))"},

		// Pipe (level 17) binds tighter than arithmetic.
		{"a + b | upper", "(Binary + (Name a) (Filter upper (Name b)))"},
		{"(a + b) | upper", "(Filter upper (Binary + (Name a) (Name b)))"},
		{"x | trim | upper", "(Filter upper (Filter trim (Name x)))"},

		// Comparison below bitwise-and? No: bitwise sits between comparison and
		// logical-and, so b_and binds looser than comparison.
		{"a == b and c", "(Logical and (Binary == (Name a) (Name b)) (Name c))"},
		// bitwise-and (level 9) is looser than comparison (level 10), so == binds first.
		{"a b_and b == c", "(Binary b_and (Name a) (Binary == (Name b) (Name c)))"},

		// Logical ladder: and > xor > or.
		{"a or b and c", "(Logical or (Name a) (Logical and (Name b) (Name c)))"},
		{"a and b or c", "(Logical or (Logical and (Name a) (Name b)) (Name c))"},
		{"a xor b or c", "(Logical or (Logical xor (Name a) (Name b)) (Name c))"},

		// Coalesce/elvis right-assoc, below logical-or.
		{"a or b ?? c", "(Coalesce (Logical or (Name a) (Name b)) (Name c))"},
		{"a ?? b ?? c", "(Coalesce (Name a) (Coalesce (Name b) (Name c)))"},
		{"a ?: b", "(Elvis (Name a) (Name b))"},

		// Ternary right-assoc, below coalesce.
		{"a ?? b ? c : d", "(Ternary (Coalesce (Name a) (Name b)) (Name c) (Name d))"},
		{"a ? b : c ? d : e", "(Ternary (Name a) (Name b) (Ternary (Name c) (Name d) (Name e)))"},

		// Unary not over comparison.
		{"not a == b", "(Binary == (Unary not (Name a)) (Name b))"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			if got := parseExprDump(t, tc.expr); got != tc.want {
				t.Fatalf("%q\n got: %s\nwant: %s", tc.expr, got, tc.want)
			}
		})
	}
}

func TestParsePostfixChain(t *testing.T) {
	tests := []struct{ expr, want string }{
		{"a.b.c", "(Attr .c (Attr .b (Name a)))"},
		{"a?.b", "(Attr ?.b (Name a))"},
		{"a[0]", "(Index (Name a) (Int 0))"},
		{"a?[k]", "(Index nullsafe (Name a) (Name k))"},
		{"a[1:3]", "(Slice (Name a) (Int 1) (Int 3))"},
		{"a[:3]", "(Slice (Name a) _ (Int 3))"},
		{"a[2:]", "(Slice (Name a) (Int 2) _)"},
		{"f(1, 2)", "(Call (Name f) (Arg (Int 1)) (Arg (Int 2)))"},
		{"f(a, key: c)", "(Call (Name f) (Arg (Name a)) (Arg named:key (Name c)))"},
		{"f(...xs)", "(Call (Name f) (Arg spread (Name xs)))"},
		// A spread after a named argument is allowed: only a bare positional after a
		// named argument is an error (design/expressions Section 7).
		{"f(a: 1, ...xs)", "(Call (Name f) (Arg named:a (Int 1)) (Arg spread (Name xs)))"},
		{"x | f(a, b)", "(Filter f (Name x) (Arg (Name a)) (Arg (Name b)))"},
		{"record.in", "(Attr .in (Name record))"}, // word-op as member name (R2)
		{"data | matches_count", "(Filter matches_count (Name data))"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			if got := parseExprDump(t, tc.expr); got != tc.want {
				t.Fatalf("%q\n got: %s\nwant: %s", tc.expr, got, tc.want)
			}
		})
	}
}

func TestParseMembershipAndTests(t *testing.T) {
	tests := []struct{ expr, want string }{
		{"x in xs", "(Membership in (Name x) (Name xs))"},
		{"x not in xs", "(Membership not in (Name x) (Name xs))"},
		{`s matches "^a"`, `(Membership matches (Name s) (String "^a"))`},
		{`name starts with "get"`, `(Membership starts with (Name name) (String "get"))`},
		{`p ends with ".java"`, `(Membership ends with (Name p) (String ".java"))`},
		{"xs has some (x => x > 0)", "(Membership has some (Name xs) (Arrow (Param x) (Binary > (Name x) (Int 0))))"},
		{"x is defined", "(Test defined (Name x))"},
		{"x is not empty", "(Test not empty (Name x))"},
		{"a is same as b", "(Test same as (Name a) (Arg (Name b)))"},
		{"n is divisible by 3", "(Test divisible by (Name n) (Arg (Int 3)))"},
		{"n is divisible_by(n: 3)", "(Test divisible_by (Name n) (Arg named:n (Int 3)))"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			if got := parseExprDump(t, tc.expr); got != tc.want {
				t.Fatalf("%q\n got: %s\nwant: %s", tc.expr, got, tc.want)
			}
		})
	}
}

func TestParseLiteralsAndCollections(t *testing.T) {
	tests := []struct{ expr, want string }{
		{"1_000_000", "(Int 1000000)"},
		{"0xFF", "(Int 255)"},
		{"0b1010", "(Int 10)"},
		{"0o755", "(Int 493)"},
		{"3.14", "(Float 3.14)"},
		{"1e9", "(Float 1e+09)"},
		{"true", "(Bool true)"},
		{"false", "(Bool false)"},
		{"null", "(Null)"},
		{"none", "(Null)"},
		{"_self", "(SpecialName _self)"},
		{`'a\nb'`, `(String "a\nb")`},
		{"`raw\\d+`", `(String "raw\\d+")`},
		{"[1, 2, 3]", "(List (Int 1) (Int 2) (Int 3))"},
		{"[...xs, 4]", "(List (Spread (Name xs)) (Int 4))"},
		{"{a: 1, b: 2}", `(Map (MapEntry keyed (String "a") (Int 1)) (MapEntry keyed (String "b") (Int 2)))`},
		{"{a}", "(Map (MapEntry shorthand (Name a)))"},
		{"{(k): v}", "(Map (MapEntry computed (Name k) (Name v)))"},
		{"{...base, c: 3}", `(Map (MapEntry spread (Name base)) (MapEntry keyed (String "c") (Int 3)))`},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			if got := parseExprDump(t, tc.expr); got != tc.want {
				t.Fatalf("%q\n got: %s\nwant: %s", tc.expr, got, tc.want)
			}
		})
	}
}

func TestParseStringInterpolation(t *testing.T) {
	// "Hello #{name | upper}!" compiles to "Hello " ~ (name | upper) ~ "!".
	got := parseExprDump(t, `"Hello #{name | upper}!"`)
	want := `(Binary ~ (Binary ~ (String "Hello ") (Filter upper (Name name))) (String "!"))`
	if got != want {
		t.Fatalf("interp string\n got: %s\nwant: %s", got, want)
	}
	// A single-quoted string never interpolates.
	if got := parseExprDump(t, `'no #{x} here'`); got != `(String "no #{x} here")` {
		t.Fatalf("single-quote interp leaked: %s", got)
	}
	// A double-quoted interpolation-only string is still a string: "#{x}" compiles
	// to "" ~ x so its static type is string, not the raw expression x.
	if got := parseExprDump(t, `"#{x}"`); got != `(Binary ~ (String "") (Name x))` {
		t.Fatalf("interp-only string lost string typing: %s", got)
	}
}

func TestParseAssignmentAndDestructuring(t *testing.T) {
	tests := []struct{ expr, want string }{
		{"b = 1 + 3", "(Assign (Target b) (Binary + (Int 1) (Int 3)))"},
		{"c = d = 'x'", `(Assign (Target c) (Assign (Target d) (String "x")))`},
		{"[a, b] = pair", "(Assign (ListPattern (Target a) (Target b)) (Name pair))"},
		{"{name} = user", "(Assign (MapPattern (MapTarget name)) (Name user))"},
		{"{key: alias} = config", "(Assign (MapPattern (MapTarget key as (Name alias))) (Name config))"},
		{"[a, b, ...rest] = xs", "(Assign (ListPattern (Target a) (Target b) (Spread (Name rest))) (Name xs))"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			if got := parseExprDump(t, tc.expr); got != tc.want {
				t.Fatalf("%q\n got: %s\nwant: %s", tc.expr, got, tc.want)
			}
		})
	}
}

func TestParseArrowForms(t *testing.T) {
	tests := []struct{ expr, want string }{
		{"x => x * 2", "(Arrow (Param x) (Binary * (Name x) (Int 2)))"},
		{"(a, b) => a <=> b", "(Arrow (Param a) (Param b) (Binary <=> (Name a) (Name b)))"},
		{"() => 0", "(Arrow (Int 0))"},
		{"(x: int) => x", "(Arrow (Param x (Type int)) (Name x))"},
		{"(a)", "(Name a)"}, // grouping, not arrow (R9)
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			if got := parseExprDump(t, tc.expr); got != tc.want {
				t.Fatalf("%q\n got: %s\nwant: %s", tc.expr, got, tc.want)
			}
		})
	}
}
