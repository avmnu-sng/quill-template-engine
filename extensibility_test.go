package quill

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/check"
	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
	"github.com/avmnu-sng/quill-template-engine/pkg/sandbox"
)

// mathExt is a sample Extension bundle: it embeds ext.BaseExtension and ships a
// function and a filter built with the typed helpers.
type mathExt struct{ ext.BaseExtension }

func (mathExt) Functions() []*ext.Function {
	return []*ext.Function{
		ext.NewFunction("clamp", func(x, lo, hi int64) int64 {
			if x < lo {
				return lo
			}
			if x > hi {
				return hi
			}
			return x
		}),
	}
}

func (mathExt) Filters() []*ext.Filter {
	return []*ext.Filter{ext.NewFilter("times", func(x, n int64) int64 { return x * n })}
}

// TestCustomCallableStructForm renders with a filter registered via the struct
// form (&ext.Filter{Name, Fn}), the full-control path that stays supported.
func TestCustomCallableStructForm(t *testing.T) {
	set := ext.NewExtensionSet()
	set.AddFilter(&ext.Filter{
		Name: "reverse_str",
		Fn: func(args []runtime.Value) (runtime.Value, error) {
			s, _ := runtime.ToText(args[0])
			r := []rune(s)
			for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
				r[i], r[j] = r[j], r[i]
			}
			return runtime.Str(string(r)), nil
		},
	})
	e := NewWithArray(nil, WithExtensions(set))
	out, err := e.RenderString("m", `{{ "abc" | reverse_str }}`, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "cba" {
		t.Errorf("got %q", out)
	}
}

// TestCustomCallableTypedHelper renders with a filter and a function registered
// via the typed helpers, end to end.
func TestCustomCallableTypedHelper(t *testing.T) {
	set := ext.NewExtensionSet()
	set.AddFilter(ext.NewFilter("times", func(x, n int64) int64 { return x * n }))
	set.AddFunction(ext.NewFunction("clamp", func(x, lo, hi int64) int64 {
		switch {
		case x < lo:
			return lo
		case x > hi:
			return hi
		default:
			return x
		}
	}))
	set.AddTest(ext.NewTest("positive", func(x int64) bool { return x > 0 }))

	e := NewWithArray(nil, WithExtensions(set))
	out, err := e.RenderString("m",
		`{{ 3 | times(4) }} {{ clamp(20, 0, 10) }} {{ 5 is positive }}`, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "12 10 true" {
		t.Errorf("got %q", out)
	}
}

// TestExtensionBundleEndToEnd renders using a bundle registered via
// WithExtension, proving the Extension interface path works end to end.
func TestExtensionBundleEndToEnd(t *testing.T) {
	e := NewWithArray(nil, WithExtension(mathExt{}))
	out, err := e.RenderString("m", `{{ 5 | times(3) }} {{ clamp(99, 0, 10) }}`, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "15 10" {
		t.Errorf("got %q", out)
	}
}

// TestComposeShadowOrder composes two sets via WithExtensions and confirms the
// later set shadows the earlier one, and both shadow core.
func TestComposeShadowOrder(t *testing.T) {
	lower := ext.NewExtensionSet()
	lower.AddFilter(&ext.Filter{Name: "tag", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Str("lower"), nil
	}})
	upper := ext.NewExtensionSet()
	upper.AddFilter(&ext.Filter{Name: "tag", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Str("upper"), nil
	}})
	// A host set that overrides a CORE filter (upper), proving host shadows core.
	upper.AddFilter(&ext.Filter{Name: "upper", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Str("SHADOWED"), nil
	}})

	e := NewWithArray(nil, WithExtensions(lower, upper))
	out, err := e.RenderString("m", `{{ "x" | tag }} {{ "y" | upper }}`, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "upper SHADOWED" {
		t.Errorf("shadow order wrong: got %q", out)
	}
}

// TestComposeInterleavedSetsAndBundles confirms WithExtensions and WithExtension
// interleave in option order, so a later bundle shadows an earlier set.
func TestComposeInterleavedSetsAndBundles(t *testing.T) {
	early := ext.NewExtensionSet()
	early.AddFilter(&ext.Filter{Name: "times", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Str("early"), nil
	}})
	// mathExt (a bundle) ships a real "times"; passed after `early`, it shadows it.
	e := NewWithArray(nil, WithExtensions(early), WithExtension(mathExt{}))
	out, err := e.RenderString("m", `{{ 2 | times(3) }}`, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "6" {
		t.Errorf("interleave order wrong: got %q", out)
	}
}

// TestCustomCallableUnderTypeChecker confirms a custom callable with no host
// signature falls to the dynamic (any) floor in the gradual checker: an
// annotated template using it loads and renders without a type error.
func TestCustomCallableUnderTypeChecker(t *testing.T) {
	set := ext.NewExtensionSet()
	set.AddFunction(ext.NewFunction("clamp", func(x, lo, hi int64) int64 {
		if x < lo {
			return lo
		}
		if x > hi {
			return hi
		}
		return x
	}))
	// An empty registry: no host signatures, so clamp is unknown to the checker
	// and must type as any (the dynamic fallback host callables already use).
	reg := check.NewRegistry()
	e := NewWithArray(nil, WithExtensions(set), WithTypes(reg))
	out, err := e.RenderString("m",
		`@set n: int = clamp(20, 0, 10)`+"\n"+`{{ n }}`, nil)
	if err != nil {
		t.Fatalf("render under checker: %v", err)
	}
	if !strings.Contains(out, "10") {
		t.Errorf("got %q", out)
	}
}

// TestCustomCallableSandboxAllow confirms a composed custom filter passes the
// sandbox when the policy allowlists it by name.
func TestCustomCallableSandboxAllow(t *testing.T) {
	set := ext.NewExtensionSet()
	set.AddFilter(ext.NewFilter("times", func(x, n int64) int64 { return x * n }))

	pol := &sandbox.Policy{
		Filters:   map[string]bool{"times": true},
		Functions: map[string]bool{},
		Tags:      map[string]bool{},
		Graph:     sandbox.NewTypeGraph(),
	}
	e := NewWithArray(nil, WithExtensions(set), WithSandboxPolicy(pol), WithSandboxActive(true))
	out, err := e.RenderString("m", `{{ 4 | times(2) }}`, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "8" {
		t.Errorf("got %q", out)
	}
}

// TestCustomCallableSandboxDeny confirms a composed custom filter is denied by
// name when the active sandbox policy does not allowlist it.
func TestCustomCallableSandboxDeny(t *testing.T) {
	set := ext.NewExtensionSet()
	set.AddFilter(ext.NewFilter("times", func(x, n int64) int64 { return x * n }))

	pol := &sandbox.Policy{
		Filters:   map[string]bool{}, // times NOT allowlisted
		Functions: map[string]bool{},
		Tags:      map[string]bool{},
		Graph:     sandbox.NewTypeGraph(),
	}
	e := NewWithArray(nil, WithExtensions(set), WithSandboxPolicy(pol), WithSandboxActive(true))
	_, err := e.RenderString("m", `{{ 4 | times(2) }}`, nil)
	if err == nil {
		t.Fatal("expected a sandbox denial")
	}
	if !strings.Contains(err.Error(), "times") {
		t.Errorf("denial should name the filter: %v", err)
	}
}
