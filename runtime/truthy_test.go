package runtime

import "testing"

func TestTruthy(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want bool
	}{
		// the five falsy shapes
		{"null", Null(), false},
		{"false", Bool(false), false},
		{"int 0", Int(0), false},
		{"float 0.0", Float(0.0), false},
		{"empty str", Str(""), false},
		{"empty array", Arr(NewArray()), false},

		// truthy
		{"true", Bool(true), true},
		{"int 1", Int(1), true},
		{"int -1", Int(-1), true},
		{"float 3.14", Float(3.14), true},
		// THE headline divergence: "0" is truthy (non-empty string)
		{`"0" truthy`, Str("0"), true},
		{`" " truthy`, Str(" "), true},
		{`"false" truthy`, Str("false"), true},
		{"non-empty array", Arr(NewList(Int(1))), true},
		// any object is truthy regardless of state
		{"object always truthy", Obj(newFieldObj("T", nil)), true},
		// Safe takes wrapped truthiness
		{`Safe("") falsy`, Safe(""), false},
		{`Safe("x") truthy`, Safe("x"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Truthy(tt.v); got != tt.want {
				t.Fatalf("Truthy(%v) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}

func TestEmpty(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want bool
	}{
		{"null empty", Null(), true},
		{"empty str", Str(""), true},
		{"non-empty str", Str("x"), false},
		{`"0" not empty`, Str("0"), false},
		{"empty array", Arr(NewArray()), true},
		{"non-empty array", Arr(NewList(Int(1))), false},
		// distinct from truthiness: 0 is falsy but NOT empty
		{"int 0 not empty", Int(0), false},
		{"int 42 not empty", Int(42), false},
		{"float 0 not empty", Float(0.0), false},
		{"bool false not empty", Bool(false), false},
		{"object not empty", Obj(newFieldObj("T", nil)), false},
		{"Safe empty", Safe(""), true},
		{"Safe non-empty", Safe("x"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Empty(tt.v); got != tt.want {
				t.Fatalf("Empty(%v) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}
