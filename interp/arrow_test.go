package interp

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/parse"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// renderStubVars renders an ad-hoc template against the stub engine with vars.
func renderStubVars(t *testing.T, eng *stubEngine, body string, vars map[string]runtime.Value) (string, error) {
	t.Helper()
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return Render(eng, Prepare("test", mod), vars)
}

// TestArrowHigherOrder covers arrow evaluation through the higher-order stdlib
// filters: map, filter, reduce, find, and sort with a spaceship comparator (spec
// 03 Section 2.2). Each case renders an ad-hoc template and joins the result so
// the output is a plain string.
func TestArrowHigherOrder(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"map double", "{{ [1, 2, 3] | map(x => x * 2) | join(',') }}", "2,4,6"},
		{"map with key", "{{ {a: 1, b: 2} | map((v, k) => k ~ v) | join(',') }}", "a1,b2"},
		{"filter even", "{{ [1, 2, 3, 4] | filter(x => x % 2 == 0) | join(',') }}", "2,4"},
		{"reduce sum", "{{ [1, 2, 3, 4] | reduce((acc, x) => acc + x, 0) }}", "10"},
		{"reduce no init", "{{ [1, 2, 3] | reduce((acc, x) => (acc ?? 0) + x) }}", "6"},
		{"find first big", "{{ [1, 5, 3, 9] | find(x => x > 4) }}", "5"},
		{"find none", "{{ [1, 2] | find(x => x > 10) | default('none') }}", "none"},
		{"sort comparator desc", "{{ [3, 1, 2] | sort((a, b) => b <=> a) | join(',') }}", "3,2,1"},
		{"sort default", "{{ [3, 1, 2] | sort | join(',') }}", "1,2,3"},
		{"map closure capture", "{{ [1, 2] | map(x => x + n) | join(',') }}", "11,12"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := renderStubVars(t, eng, c.body, map[string]runtime.Value{"n": runtime.Int(10)})
			if err != nil {
				t.Fatalf("render error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestArrowQuantifiers covers the has some / has every membership quantifiers
// applying an arrow predicate over a collection (spec 04 Section 4.3).
func TestArrowQuantifiers(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"some true", "{{ [1, 2, 3] has some (x => x > 2) }}", "true"},
		{"some false", "{{ [1, 2, 3] has some (x => x > 9) }}", "false"},
		{"every true", "{{ [2, 4, 6] has every (x => x % 2 == 0) }}", "true"},
		{"every false", "{{ [2, 4, 5] has every (x => x % 2 == 0) }}", "false"},
		{"every empty vacuous", "{{ [] has every (x => x > 0) }}", "true"},
		{"some empty false", "{{ [] has some (x => x > 0) }}", "false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := renderStubResult(t, eng, c.body)
			if err != nil {
				t.Fatalf("render error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestArrowNonCallable verifies that applying a higher-order filter to a
// non-arrow argument is a clear runtime error, not a silent no-op.
func TestArrowNonCallable(t *testing.T) {
	eng := newStub(nil)
	_, err := renderStubResult(t, eng, "{{ [1, 2] | map(3) }}")
	if err == nil {
		t.Fatal("expected an error mapping with a non-callable")
	}
	if !strings.Contains(err.Error(), "callable") {
		t.Errorf("error %q should mention callable", err.Error())
	}
	if errors.KindOf(err) != errors.KindRuntime {
		t.Errorf("kind = %v, want runtime", errors.KindOf(err))
	}
}
