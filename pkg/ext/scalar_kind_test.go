package ext

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// TestScalarKindTests covers the scalar-kind predicates: is string / number /
// int / float / bool / callable. Each is a total kind predicate over the value
// domain; number is the union of int and float. They are distinct from the
// eq/ne value comparisons.
func TestScalarKindTests(t *testing.T) {
	arrow := callFn(t, "separator", runtime.Str(",")) // a callable Object
	cases := []struct {
		test string
		val  runtime.Value
		want bool
	}{
		{"string", runtime.Str("x"), true},
		{"string", runtime.Safe("x"), true},
		{"string", runtime.Int(1), false},
		{"number", runtime.Int(1), true},
		{"number", runtime.Float(1.5), true},
		{"number", runtime.Str("1"), false},
		{"number", runtime.Bool(true), false},
		{"int", runtime.Int(1), true},
		{"int", runtime.Float(1.0), false},
		{"float", runtime.Float(1.0), true},
		{"float", runtime.Int(1), false},
		{"bool", runtime.Bool(false), true},
		{"bool", runtime.Bool(true), true},
		{"bool", runtime.Int(1), false},
		{"callable", arrow, true},
		{"callable", runtime.Str("x"), false},
		{"callable", runtime.Int(1), false},
		{"callable", runtime.Null(), false},
	}
	for _, c := range cases {
		t.Run(c.test+"/"+c.val.Kind().String(), func(t *testing.T) {
			if got := callTest(t, c.test, c.val); got != c.want {
				t.Errorf("is %s over %s = %v, want %v", c.test, c.val.Kind(), got, c.want)
			}
		})
	}
}
