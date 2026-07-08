package ext

import (
	"errors"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// TestNewFilterScalarMarshaling covers the string<->Str round trip through a
// natural func with a single scalar parameter and result.
func TestNewFilterScalarMarshaling(t *testing.T) {
	f := NewFilter("shout", func(s string) string { return strings.ToUpper(s) + "!" })
	if f.Name != "shout" {
		t.Fatalf("name = %q", f.Name)
	}
	out, err := f.Fn([]runtime.Value{runtime.Str("hi")})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Kind() != runtime.KStr || out.AsStr() != "HI!" {
		t.Errorf("got %+v", out)
	}
}

// TestNewFunctionMultiArgWithError covers multiple int args and an optional
// trailing error return, both the success and the returned-error paths.
func TestNewFunctionMultiArgWithError(t *testing.T) {
	div := NewFunction("div", func(a, b int64) (int64, error) {
		if b == 0 {
			return 0, errors.New("divide by zero")
		}
		return a / b, nil
	})

	out, err := div.Fn([]runtime.Value{runtime.Int(10), runtime.Int(2)})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Kind() != runtime.KInt || out.AsInt() != 5 {
		t.Errorf("got %+v", out)
	}

	_, err = div.Fn([]runtime.Value{runtime.Int(1), runtime.Int(0)})
	if err == nil || !strings.Contains(err.Error(), "divide by zero") {
		t.Errorf("expected divide-by-zero error, got %v", err)
	}
}

// TestNewFunctionArity checks the typed arity error for both too-few and
// too-many arguments to a fixed-arity func.
func TestNewFunctionArity(t *testing.T) {
	add := NewFunction("add", func(a, b int64) int64 { return a + b })
	for _, args := range [][]runtime.Value{
		{runtime.Int(1)},
		{runtime.Int(1), runtime.Int(2), runtime.Int(3)},
	} {
		if _, err := add.Fn(args); err == nil || !strings.Contains(err.Error(), "expected 2 argument") {
			t.Errorf("args %d: expected arity error, got %v", len(args), err)
		}
	}
}

// TestNewFunctionArgTypeMismatch checks a clear typed error when an argument's
// runtime kind does not marshal to the declared Go type.
func TestNewFunctionArgTypeMismatch(t *testing.T) {
	length := NewFunction("strlen", func(s string) int64 { return int64(len(s)) })
	_, err := length.Fn([]runtime.Value{runtime.Int(3)})
	if err == nil {
		t.Fatal("expected type error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "argument 1") || !strings.Contains(msg, "expected string") || !strings.Contains(msg, "got int") {
		t.Errorf("unclear error: %q", msg)
	}
}

// TestNewFilterVariadic covers a variadic tail: fixed leading arg plus zero or
// more trailing args of the variadic element type.
func TestNewFilterVariadic(t *testing.T) {
	joinAll := NewFilter("cat", func(sep string, parts ...string) string {
		return strings.Join(parts, sep)
	})

	out, err := joinAll.Fn([]runtime.Value{runtime.Str("-"), runtime.Str("a"), runtime.Str("b"), runtime.Str("c")})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.AsStr() != "a-b-c" {
		t.Errorf("got %q", out.AsStr())
	}

	// Zero variadic args is allowed.
	out, err = joinAll.Fn([]runtime.Value{runtime.Str("-")})
	if err != nil {
		t.Fatalf("call zero-variadic: %v", err)
	}
	if out.AsStr() != "" {
		t.Errorf("got %q", out.AsStr())
	}

	// Fewer than the fixed count is an arity error.
	if _, err := joinAll.Fn(nil); err == nil || !strings.Contains(err.Error(), "at least 1") {
		t.Errorf("expected at-least arity error, got %v", err)
	}
}

// TestNewFilterSliceMarshaling covers []T<->*Array in both directions.
func TestNewFilterSliceMarshaling(t *testing.T) {
	doubled := NewFilter("double_each", func(xs []int64) []int64 {
		out := make([]int64, len(xs))
		for i, x := range xs {
			out[i] = x * 2
		}
		return out
	})

	in := runtime.Arr(runtime.NewList(runtime.Int(1), runtime.Int(2), runtime.Int(3)))
	out, err := doubled.Fn([]runtime.Value{in})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Kind() != runtime.KArray || out.AsArray().Len() != 3 {
		t.Fatalf("got %+v", out)
	}
	got := out.AsArray().Pairs()
	if got[0].Val.AsInt() != 2 || got[1].Val.AsInt() != 4 || got[2].Val.AsInt() != 6 {
		t.Errorf("wrong slice result: %+v", got)
	}
}

// TestNewFunctionValuePassthrough covers runtime.Value as both a parameter and a
// result: the value crosses unchanged.
func TestNewFunctionValuePassthrough(t *testing.T) {
	identity := NewFunction("id", func(v runtime.Value) runtime.Value { return v })
	in := runtime.Arr(runtime.NewList(runtime.Str("x")))
	out, err := identity.Fn([]runtime.Value{in})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Kind() != runtime.KArray || out.AsArray() != in.AsArray() {
		t.Errorf("passthrough altered value: %+v", out)
	}
}

// TestNewFunctionFloatFromInt covers the widening of an Int argument to a float
// parameter, and the Float result marshaling.
func TestNewFunctionFloatFromInt(t *testing.T) {
	half := NewFunction("half", func(x float64) float64 { return x / 2 })
	out, err := half.Fn([]runtime.Value{runtime.Int(5)})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Kind() != runtime.KFloat || out.AsFloat() != 2.5 {
		t.Errorf("got %+v", out)
	}
}

// TestNewTest covers a natural bool-returning test func and its result mapping.
func TestNewTest(t *testing.T) {
	positive := NewTest("positive", func(x int64) bool { return x > 0 })
	if positive.Name != "positive" {
		t.Fatalf("name = %q", positive.Name)
	}
	got, err := positive.Fn([]runtime.Value{runtime.Int(3)})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !got {
		t.Error("expected true")
	}
	got, err = positive.Fn([]runtime.Value{runtime.Int(-1)})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got {
		t.Error("expected false")
	}
}

// TestNewTestWithError covers a test whose func returns (bool, error): a
// returned error surfaces from the Test.Fn.
func TestNewTestWithError(t *testing.T) {
	tst := NewTest("checked", func(s string) (bool, error) {
		if s == "" {
			return false, errors.New("empty")
		}
		return len(s) > 2, nil
	})
	if _, err := tst.Fn([]runtime.Value{runtime.Str("")}); err == nil {
		t.Error("expected error")
	}
	ok, err := tst.Fn([]runtime.Value{runtime.Str("abcd")})
	if err != nil || !ok {
		t.Errorf("got ok=%v err=%v", ok, err)
	}
}

// TestNewFilterPanicsOnBadShape confirms an unsupported func shape is a
// registration-time panic, not a silent misregistration.
func TestNewFilterPanicsOnBadShape(t *testing.T) {
	cases := []struct {
		name string
		fn   any
	}{
		{"not-a-func", 42},
		{"bad-param", func(c complex128) string { return "" }},
		{"bad-result", func(s string) complex128 { return 0 }},
		{"too-many-results", func(s string) (string, string, error) { return "", "", nil }},
		{"second-not-error", func(s string) (string, string) { return "", "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("expected panic for %s", tc.name)
				}
			}()
			NewFilter("bad", tc.fn)
		})
	}
}

// TestNewTestPanicsWithoutBool confirms a test func must return a leading bool.
func TestNewTestPanicsWithoutBool(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic for non-bool test")
		}
	}()
	NewTest("bad", func(s string) string { return s })
}
