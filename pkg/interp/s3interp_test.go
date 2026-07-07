package interp

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
)

// renderStubResult renders an ad-hoc template and returns the output and error
// (the error-tolerant counterpart of renderStub, used for the overflow/error
// table cases below).
func renderStubResult(t *testing.T, eng *stubEngine, body string) (string, error) {
	t.Helper()
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return Render(eng, Prepare("test", mod), nil)
}

// TestArithIntOverflow covers the uniform int64-overflow rule for + - * (spec 04
// Section 2.1): an overflowing result is a KindArithmetic error naming the op,
// never a silent wrap. Boundary non-overflowing cases must still render.
func TestArithIntOverflow(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		name     string
		body     string
		overflow bool
		want     string // expected output when overflow == false
	}{
		{"add overflow", "{{ 9223372036854775807 + 1 }}", true, ""},
		{"add max ok", "{{ 9223372036854775806 + 1 }}", false, "9223372036854775807"},
		{"sub overflow", "{{ -9223372036854775807 - 2 }}", true, ""},
		{"sub min ok", "{{ -9223372036854775807 - 1 }}", false, "-9223372036854775808"},
		{"mul overflow", "{{ 9223372036854775807 * 2 }}", true, ""},
		{"mul overflow neg", "{{ 4611686018427387904 * 2 }}", true, ""},
		{"mul ok", "{{ 3037000499 * 3037000499 }}", false, "9223372030926249001"},
		{"mul zero ok", "{{ 9223372036854775807 * 0 }}", false, "0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := renderStubResult(t, eng, c.body)
			if c.overflow {
				if err == nil {
					t.Fatalf("expected overflow error, got %q", got)
				}
				if errors.KindOf(err) != errors.KindArithmetic {
					t.Fatalf("want KindArithmetic, got %v (%v)", errors.KindOf(err), err)
				}
				if !strings.Contains(err.Error(), "overflows int64") {
					t.Errorf("error should name overflow: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestPowIntOverflow covers ** for an Int base and non-negative Int exponent: an
// exact integer result, and an overflow as a KindArithmetic error (NOT a
// float-rounded saturated literal). 2**63 and 10**20 are the regression cases.
func TestPowIntOverflow(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		name     string
		body     string
		overflow bool
		want     string
	}{
		{"pow exact", "{{ 2 ** 10 }}", false, "1024"},
		{"pow 62 ok", "{{ 2 ** 62 }}", false, "4611686018427387904"},
		{"pow 63 overflow", "{{ 2 ** 63 }}", true, ""},
		{"pow 10e20 overflow", "{{ 10 ** 20 }}", true, ""},
		{"pow zero exp", "{{ 5 ** 0 }}", false, "1"},
		{"pow one base", "{{ 1 ** 100 }}", false, "1"},
		{"pow neg exp float", "{{ 2 ** -1 }}", false, "0.5"},
		{"pow float base", "{{ 2.0 ** 3 }}", false, "8"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := renderStubResult(t, eng, c.body)
			if c.overflow {
				if err == nil {
					t.Fatalf("expected overflow error, got %q", got)
				}
				if errors.KindOf(err) != errors.KindArithmetic {
					t.Fatalf("want KindArithmetic, got %v (%v)", errors.KindOf(err), err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestFloorDivIntKind verifies // of two Ints stays Int (spec 04 Section 2.1 //
// row): the result must be usable in an Int-only context such as a bitwise op,
// which the prior Float result broke. Float operands still floor to a Float.
func TestFloorDivIntKind(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		name string
		body string
		want string
	}{
		// 7 // 2 == 3 (Int); 3 b_and 1 == 1 proves the Int kind.
		{"int floordiv usable in bitwise", "{{ (7 // 2) b_and 1 }}", "1"},
		{"int floordiv value", "{{ 7 // 2 }}", "3"},
		{"negative floors down", "{{ -7 // 2 }}", "-4"},
		{"negative divisor floors", "{{ 7 // -2 }}", "-4"},
		{"both negative", "{{ -7 // -2 }}", "3"},
		{"exact", "{{ 8 // 2 }}", "4"},
		// A Float operand keeps the floored-Float path.
		{"float operand floors", "{{ 7.0 // 2 }}", "3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := renderStubResult(t, eng, c.body)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestFloorDivOverflow covers the single overflowing // case, MinInt64 // -1.
func TestFloorDivOverflow(t *testing.T) {
	eng := newStub(nil)
	// MinInt64 cannot be written as a positive literal (it overflows the lexer's
	// signed parse), so build it as (-(MaxInt64) - 1) // -1.
	got, err := renderStubResult(t, eng, "{{ (-9223372036854775807 - 1) // -1 }}")
	if err == nil {
		t.Fatalf("expected overflow error, got %q", got)
	}
	if errors.KindOf(err) != errors.KindArithmetic {
		t.Fatalf("want KindArithmetic, got %v (%v)", errors.KindOf(err), err)
	}
}

// TestMacroNamedArgs verifies named macro arguments bind BY PARAMETER NAME and
// may appear in any order (design/expressions.md Section 7), the regression
// being f("X", c: "Z") rendering a=X b=B c=Z rather than mis-binding c onto b.
func TestMacroNamedArgs(t *testing.T) {
	eng := newStub(nil)
	const def = "@macro f(a, b=\"B\", c=\"C\") {\na={{ a }} b={{ b }} c={{ c }}\n@}\n"
	cases := []struct {
		name string
		call string
		want string
	}{
		{"named skips middle", "{{ f(\"X\", c: \"Z\") }}", "a=X b=B c=Z"},
		{"all positional", "{{ f(\"X\", \"Y\", \"Z\") }}", "a=X b=Y c=Z"},
		{"out of order named", "{{ f(\"X\", c: \"Z\", b: \"Y\") }}", "a=X b=Y c=Z"},
		{"named for first too", "{{ f(c: \"Z\", a: \"X\") }}", "a=X b=B c=Z"},
		{"gap fill defaults", "{{ f(\"X\") }}", "a=X b=B c=C"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := renderStub(t, eng, def+c.call, nil)
			if !strings.Contains(got, c.want) {
				t.Errorf("got %q want substring %q", got, c.want)
			}
		})
	}
}

// TestMacroNamedArgErrors verifies an unknown named arg and a positional/named
// double-bind are errors, not silent mis-binds.
func TestMacroNamedArgErrors(t *testing.T) {
	eng := newStub(nil)
	const def = "@macro f(a, b=\"B\") {\n{{ a }}{{ b }}\n@}\n"
	cases := []struct {
		name string
		call string
		frag string
	}{
		{"unknown name", "{{ f(\"X\", zzz: 1) }}", "no parameter"},
		{"double bind", "{{ f(\"X\", \"Y\", a: \"Z\") }}", "both positionally and by name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := renderStubResult(t, eng, def+c.call)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.frag) {
				t.Errorf("error %q should mention %q", err.Error(), c.frag)
			}
		})
	}
}

// TestNestedLoopMetadata verifies an inner @for does not corrupt the outer
// loop's metadata after it returns (spec 01 Section 4.2): loop.index after the
// inner loop must be the OUTER index, and loop.parent.index inside the inner
// loop must track the outer iteration.
func TestNestedLoopMetadata(t *testing.T) {
	eng := newStub(nil)
	// AFTER must read 1 then 2 (the outer index), not 2 then 2.
	got := renderStub(t, eng,
		"@for a in [10,20] {\n@for b in [1,2] {\nx\n@}\nAFTER={{ loop.index }}\n@}\n", nil)
	if c := strings.Count(got, "AFTER=1"); c != 1 {
		t.Errorf("expected exactly one AFTER=1 (outer first iter), got %q", got)
	}
	if c := strings.Count(got, "AFTER=2"); c != 1 {
		t.Errorf("expected exactly one AFTER=2 (outer second iter), got %q", got)
	}

	// loop.parent.index inside the inner loop tracks the outer iteration.
	got2 := renderStub(t, eng,
		"@for a in [10,20] {\n@for b in [1,2] {\n[{{ loop.parent.index }}.{{ loop.index }}]\n@}\n@}\n", nil)
	for _, want := range []string{"[1.1]", "[1.2]", "[2.1]", "[2.2]"} {
		if !strings.Contains(got2, want) {
			t.Errorf("missing %s in %q", want, got2)
		}
	}
}

// TestForReassignWriteback confirms the writeback still propagates a genuine
// user reassignment of a pre-existing variable (the loop-control exclusion must
// not over-exclude).
func TestForReassignWriteback(t *testing.T) {
	eng := newStub(nil)
	got := renderStub(t, eng,
		"@set total = 0\n@for n in [1,2,3] {\n@set total = total + n\n@}\n{{ total }}", nil)
	if !strings.Contains(got, "6") {
		t.Errorf("reassignment of a pre-existing var must persist: %q", got)
	}
	// A loop variable named like the iterand target must NOT leak out.
	got2 := renderStub(t, eng,
		"@for n in [1,2,3] {\nx\n@}\n{{ n is defined }}", nil)
	if !strings.Contains(got2, "false") {
		t.Errorf("loop target must not leak after the loop: %q", got2)
	}
}
