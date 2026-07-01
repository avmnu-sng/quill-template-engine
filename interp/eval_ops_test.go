package interp

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/parse"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// TestEvalExpressions renders one interpolation per expression node kind and
// pins the exact rendered bytes, exercising the eval dispatch for index, slice,
// logical, elvis, inline-assign, special-name, computed-map-key, and unary.
func TestEvalExpressions(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		name string
		body string
		vars map[string]runtime.Value
		want string
	}{
		{"list index", `{{ [1, 2, 3][1] }}`, nil, "2"},
		{"map index", `{{ {"a": 1, "b": 2}["b"] }}`, nil, "2"},
		{"string slice", `{{ "hello"[1:4] }}`, nil, "ell"},
		{"list slice join", `{{ [10, 20, 30, 40][1:3] | join(",") }}`, nil, "20,30"},
		{"string open-ended slice", `{{ "hello"[2:] }}`, nil, "llo"},

		{"logical and short-circuits false", `{{ a and b }}`,
			map[string]runtime.Value{"a": runtime.Bool(false), "b": runtime.Bool(true)}, "false"},
		{"logical and true", `{{ a and b }}`,
			map[string]runtime.Value{"a": runtime.Bool(true), "b": runtime.Bool(true)}, "true"},
		{"logical or short-circuits true", `{{ a or b }}`,
			map[string]runtime.Value{"a": runtime.Bool(true), "b": runtime.Bool(false)}, "true"},
		{"logical or false", `{{ a or b }}`,
			map[string]runtime.Value{"a": runtime.Bool(false), "b": runtime.Bool(false)}, "false"},
		{"logical xor", `{{ a xor b }}`,
			map[string]runtime.Value{"a": runtime.Bool(true), "b": runtime.Bool(false)}, "true"},

		{"inline assign yields value", `{{ x = 5 }}{{ x + 1 }}`, nil, "56"},
		{"elvis falls back on absent", `{{ v ?: "fallback" }}`, nil, "fallback"},
		{"elvis falls back on falsy", `{{ n ?: "fb" }}`,
			map[string]runtime.Value{"n": runtime.Int(0)}, "fb"},
		{"elvis keeps truthy", `{{ n ?: "fb" }}`,
			map[string]runtime.Value{"n": runtime.Int(7)}, "7"},

		{"special name charset", `{{ _charset }}`, nil, "UTF-8"},

		{"computed and grouped map keys", `{{ {(1 + 1): "x", ("k"): "y"} | length }}`, nil, "2"},

		{"unary minus int", `{{ -n }}`, map[string]runtime.Value{"n": runtime.Int(3)}, "-3"},
		{"unary minus float", `{{ -f }}`, map[string]runtime.Value{"f": runtime.Float(2.5)}, "-2.5"},
		{"unary plus passes number", `{{ +n }}`, map[string]runtime.Value{"n": runtime.Int(4)}, "4"},
		{"unary not", `{{ not n }}`, map[string]runtime.Value{"n": runtime.Int(0)}, "true"},

		{"nullsafe index on null receiver", `{{ (m?["k"]) ?? "none" }}`,
			map[string]runtime.Value{"m": runtime.Null()}, "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderStub(t, eng, tc.body, tc.vars); got != tc.want {
				t.Fatalf("render = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEvalUnaryErrors pins the exact arithmetic-error message for a non-numeric
// unary operand (both - and +).
func TestEvalUnaryErrors(t *testing.T) {
	eng := newStub(nil)
	for _, body := range []string{`{{ -s }}`, `{{ +s }}`} {
		mod, err := parse.ParseString("t", body)
		if err != nil {
			t.Fatalf("parse %q: %v", body, err)
		}
		_, err = Render(eng, Prepare("t", mod), map[string]runtime.Value{"s": runtime.Str("x")})
		if err == nil {
			t.Fatalf("%q: expected an arithmetic error", body)
		}
		if !strings.Contains(err.Error(), "expects a number") {
			t.Fatalf("%q: unexpected error %q", body, err.Error())
		}
	}
}

// TestSpecialContext checks _context reflects the current bindings as a map that
// can be indexed.
func TestSpecialContext(t *testing.T) {
	eng := newStub(nil)
	got := renderStub(t, eng, `@set who = "ada"
{{ _context["who"] }}`, nil)
	if got != "ada" {
		t.Fatalf("_context index = %q, want ada", got)
	}
}

// TestBitwiseAndCompare pins the exact integer output of the bitwise operators
// and the three-way comparison spaceship over ints and strings.
func TestBitwiseAndCompare(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		body, want string
	}{
		{`{{ 6 b_and 3 }}`, "2"},
		{`{{ 6 b_or 1 }}`, "7"},
		{`{{ 6 b_xor 3 }}`, "5"},
		{`{{ 5 <=> 3 }}`, "1"},
		{`{{ 3 <=> 5 }}`, "-1"},
		{`{{ 4 <=> 4 }}`, "0"},
		{`{{ "b" <=> "a" }}`, "1"},
	}
	for _, c := range cases {
		t.Run(c.body, func(t *testing.T) {
			if got := renderStub(t, eng, c.body, nil); got != c.want {
				t.Fatalf("%q = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

// TestMembershipOperators covers in / not in, starts/ends with, and the
// has some / has every quantifiers including the empty-collection identities.
func TestMembershipOperators(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		body, want string
	}{
		{`{{ 2 in [1, 2, 3] }}`, "true"},
		{`{{ 9 in [1, 2, 3] }}`, "false"},
		{`{{ 9 not in [1, 2, 3] }}`, "true"},
		{`{{ "abcdef" starts with "abc" }}`, "true"},
		{`{{ "abcdef" starts with "xyz" }}`, "false"},
		{`{{ "abcdef" ends with "def" }}`, "true"},
		{`{{ "abcdef" ends with "xyz" }}`, "false"},
		{`{{ [1, 2, 3] has some (x) => x > 2 }}`, "true"},
		{`{{ [1, 2, 3] has some (x) => x > 9 }}`, "false"},
		{`{{ [1, 2, 3] has every (x) => x > 0 }}`, "true"},
		{`{{ [1, 2, 3] has every (x) => x > 1 }}`, "false"},
		// empty-collection quantifier identities.
		{`{{ [] has some (x) => true }}`, "false"},
		{`{{ [] has every (x) => false }}`, "true"},
	}
	for _, c := range cases {
		t.Run(c.body, func(t *testing.T) {
			if got := renderStub(t, eng, c.body, nil); got != c.want {
				t.Fatalf("%q = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

// TestQuantifierNonCallable pins the error when the right operand of a quantifier
// is not an arrow predicate.
func TestQuantifierNonCallable(t *testing.T) {
	eng := newStub(nil)
	mod, err := parse.ParseString("t", `{{ [1, 2] has some 3 }}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Render(eng, Prepare("t", mod), nil)
	if err == nil || !strings.Contains(err.Error(), "arrow predicate") {
		t.Fatalf("expected arrow-predicate error, got %v", err)
	}
}

// TestWithStatement covers @with (merges a map into the body scope) and the
// "only" form (which hides outer bindings).
func TestWithStatement(t *testing.T) {
	eng := newStub(nil)
	// Plain @with keeps outer visibility and merges the map names.
	got := renderStub(t, eng, `@set outer = "O"
@with { inner: "I" } {
[{{ outer }}{{ inner }}]
@}`, nil)
	if strings.TrimSpace(got) != "[OI]" {
		t.Fatalf("with plain = %q, want [OI]", got)
	}
	// @with only hides outer bindings; inner still resolves.
	got = renderStub(t, eng, `@set outer = "O"
@with { inner: "I" } only {
[{{ inner }}]
@}`, nil)
	if strings.TrimSpace(got) != "[I]" {
		t.Fatalf("with only = %q, want [I]", got)
	}
}

// TestGuardStatement covers @guard selecting the present/absent branch by
// whether a callable is registered, and the else branch.
func TestGuardStatement(t *testing.T) {
	eng := newStub(nil)
	// "upper" is a core filter, so the present branch renders.
	got := renderStub(t, eng, `@guard filter("upper") {
yes
@} else {
no
@}`, nil)
	if strings.TrimSpace(got) != "yes" {
		t.Fatalf("guard present = %q, want yes", got)
	}
	// an unregistered filter selects the else branch.
	got = renderStub(t, eng, `@guard filter("nope_missing") {
yes
@} else {
no
@}`, nil)
	if strings.TrimSpace(got) != "no" {
		t.Fatalf("guard absent = %q, want no", got)
	}
	// a guard with no else and an absent callable renders nothing.
	got = renderStub(t, eng, `before
@guard function("nope_missing") {
dead
@}
after`, nil)
	if strings.Contains(got, "dead") {
		t.Fatalf("guard absent no-else must not render body: %q", got)
	}
}
