package runtime

import (
	"math"
	"testing"

	"github.com/avmnusng/quill-template-engine/errors"
)

func TestToTextSpellings(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want string
	}{
		{"null is empty", Null(), ""},
		// Bool renders the literal words, NOT PHP "1"/""
		{"true word", Bool(true), "true"},
		{"false word", Bool(false), "false"},
		{"int", Int(42), "42"},
		{"negative int", Int(-7), "-7"},
		{"big int no separators", Int(1000000), "1000000"},
		// Float shortest round-trippable: 1.0 -> "1", 1.5 -> "1.5"
		{"float whole", Float(1.0), "1"},
		{"float frac", Float(1.5), "1.5"},
		{"float neg", Float(-2.25), "-2.25"},
		{"str verbatim", Str("hello"), "hello"},
		{"str with bytes", Str("a\x00b"), "a\x00b"},
		// Safe unwraps to its content
		{"safe unwraps", Safe("<b>"), "<b>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToText(tt.v)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ToText(%v) = %q, want %q", tt.v, got, tt.want)
			}
		})
	}
}

func TestToTextArrayIsRenderError(t *testing.T) {
	_, err := ToText(Arr(NewList(Int(1))))
	if err == nil {
		t.Fatal("rendering an array must be an error, not the word \"Array\"")
	}
	if errors.KindOf(err) != errors.KindRender {
		t.Fatalf("array render error kind = %v, want render", errors.KindOf(err))
	}
}

func TestToTextObjectHook(t *testing.T) {
	// With a Stringify hook -> its output.
	withHook := Obj(&stringyObj{fieldObj: newFieldObj("T", nil), text: "rendered"})
	got, err := ToText(withHook)
	if err != nil || got != "rendered" {
		t.Fatalf("hook object = %q, %v; want rendered", got, err)
	}
	// Without a hook -> a render error, not ambient __toString.
	noHook := Obj(newFieldObj("T", nil))
	if _, err := ToText(noHook); err == nil {
		t.Fatal("object without stringify hook must be a render error")
	} else if errors.KindOf(err) != errors.KindRender {
		t.Fatalf("kind = %v, want render", errors.KindOf(err))
	}
}

func TestRejectNonFinite(t *testing.T) {
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if RejectNonFinite(f) == nil {
			t.Fatalf("RejectNonFinite(%v) should error", f)
		}
	}
	if RejectNonFinite(1.5) != nil {
		t.Fatal("finite float should pass the boundary guard")
	}
}
