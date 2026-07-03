package quill

import (
	"fmt"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/ext"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// TestFn1HostFilterGetsFreshArgSlice pins the retention-safety contract for
// host filters without Fn1: every invocation receives a freshly built
// argument slice, so a filter that retains its slice and mutates the retained
// copy later cannot corrupt a subsequent call. The shadowing host "upper"
// keeps the engine on the general path for a core-audited name.
func TestFn1HostFilterGetsFreshArgSlice(t *testing.T) {
	var retained []runtime.Value
	set := ext.NewExtensionSet()
	set.AddFilter(&ext.Filter{
		Name: "upper",
		Fn: func(args []runtime.Value) (runtime.Value, error) {
			if retained != nil {
				// Sabotage the PREVIOUS call's slice; a fresh slice per call
				// makes this invisible to the current invocation.
				retained[0] = runtime.Str("corrupted")
				if &retained[0] == &args[0] {
					return runtime.Null(), fmt.Errorf("argument slice was reused across calls")
				}
			}
			retained = args
			s, err := runtime.ToText(args[0])
			if err != nil {
				return runtime.Null(), err
			}
			return runtime.Str("H:" + strings.ToUpper(s)), nil
		},
	})
	e := NewWithArray(nil, WithExtensions(set))
	out, err := e.RenderString("m", "@for x in [\"a\", \"b\", \"c\"] {\n{{ x | upper }}\n@}\n", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "H:A\nH:B\nH:C\n" {
		t.Errorf("got %q", out)
	}
}

// TestFn1ShadowedFilterWithoutFn1TakesGeneralPath asserts a host filter that
// shadows an audited name without publishing Fn1 wins the lookup AND runs on
// the general path: its Fn observes the standard one-element argument slice.
func TestFn1ShadowedFilterWithoutFn1TakesGeneralPath(t *testing.T) {
	argLens := []int{}
	set := ext.NewExtensionSet()
	set.AddFilter(&ext.Filter{
		Name: "upper",
		Fn: func(args []runtime.Value) (runtime.Value, error) {
			argLens = append(argLens, len(args))
			return runtime.Str("shadow"), nil
		},
	})
	e := NewWithArray(nil, WithExtensions(set))
	out, err := e.RenderString("m", `{{ "x" | upper }}`, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "shadow" {
		t.Errorf("shadow did not win: got %q", out)
	}
	if len(argLens) != 1 || argLens[0] != 1 {
		t.Errorf("Fn should run once with the piped value only, got calls %v", argLens)
	}
}

// TestFn1NeedsFlagsDisableFastCall re-registers an audited name with
// NeedsContext set AND an Fn1 trap: the injection flags must force the
// general path, so Fn sees the injected context mapping ahead of the piped
// value and the trap never fires.
func TestFn1NeedsFlagsDisableFastCall(t *testing.T) {
	set := ext.NewExtensionSet()
	set.AddFilter(&ext.Filter{
		Name: "upper",
		Fn: func(args []runtime.Value) (runtime.Value, error) {
			if len(args) != 2 || args[0].Kind != runtime.KArray {
				return runtime.Null(), fmt.Errorf("context injection missing: %d args", len(args))
			}
			got, ok := args[0].Arr.GetStr("who")
			if !ok {
				return runtime.Null(), fmt.Errorf("injected context lacks the in-scope binding")
			}
			s, _ := runtime.ToText(args[1])
			w, _ := runtime.ToText(got)
			return runtime.Str(s + "/" + w), nil
		},
		Fn1: func(v runtime.Value) (runtime.Value, error) {
			return runtime.Null(), fmt.Errorf("Fn1 must not be consulted when Needs flags are set")
		},
		NeedsContext: true,
	})
	e := NewWithArray(nil, WithExtensions(set))
	out, err := e.RenderString("m", "@set who = \"ada\"\n{{ \"x\" | upper }}", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "x/ada" {
		t.Errorf("got %q", out)
	}
}

// probeFilter builds a host filter whose two routes are behaviorally
// identical but record which one dispatched, so the tests can observe the
// engine's arity-known routing decision directly.
func probeFilter(name string, calls *[]string) *ext.Filter {
	return &ext.Filter{
		Name: name,
		Fn: func(args []runtime.Value) (runtime.Value, error) {
			*calls = append(*calls, "general")
			s, err := runtime.ToText(args[0])
			if err != nil {
				return runtime.Null(), err
			}
			return runtime.Str("p:" + s), nil
		},
		Fn1: func(v runtime.Value) (runtime.Value, error) {
			*calls = append(*calls, "fast")
			s, err := runtime.ToText(v)
			if err != nil {
				return runtime.Null(), err
			}
			return runtime.Str("p:" + s), nil
		},
	}
}

// TestFn1SpreadArgKeepsGeneralPath asserts the zero-argument proof is
// syntactic: a bare pipe dispatches fast, while ANY explicit argument --
// including a spread that expands to zero values -- keeps the general path,
// because the fast call must never depend on runtime argument data.
func TestFn1SpreadArgKeepsGeneralPath(t *testing.T) {
	var calls []string
	set := ext.NewExtensionSet()
	set.AddFilter(probeFilter("probe1", &calls))
	e := NewWithArray(nil, WithExtensions(set))

	vars := map[string]runtime.Value{"empty": runtime.Arr(runtime.NewArray())}
	out, err := e.RenderString("m", `{{ "a" | probe1 }};{{ "b" | probe1(...empty) }}`, vars)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "p:a;p:b" {
		t.Errorf("got %q", out)
	}
	if len(calls) != 2 || calls[0] != "fast" || calls[1] != "general" {
		t.Errorf("routing = %v, want [fast general]", calls)
	}
}

// TestFn1MemoResolvesAlternatingFilters exercises the one-entry registry memo
// under its worst case -- alternating filter names -- and under repeats, so a
// stale-memo bug would misroute a call to the wrong filter and change bytes.
func TestFn1MemoResolvesAlternatingFilters(t *testing.T) {
	e := NewWithArray(nil)
	out, err := e.RenderString("m",
		"@for x in [\"aB\", \"cD\"] {\n{{ x | upper }}{{ x | lower }}{{ x | upper }}\n@}\n", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "ABabAB\nCDcdCD\n" {
		t.Errorf("got %q", out)
	}
}

// TestFn1FastAndGeneralRoutesByteEqual is the in-engine differential for
// every audited filter: the bare pipe (fast route) and the same pipe with an
// empty spread argument (general route, identical argument vector) must
// produce byte-identical output -- and byte-identical error text on values
// the filter rejects -- across an adversarial value battery.
func TestFn1FastAndGeneralRoutesByteEqual(t *testing.T) {
	audited := []string{
		"upper", "lower", "trim", "capitalize", "title",
		"length", "first", "last", "reverse", "keys", "raw",
	}
	vars := map[string]runtime.Value{
		"vals": runtime.Arr(runtime.NewList(
			runtime.Null(),
			runtime.Bool(false),
			runtime.Int(-3),
			runtime.Float(2.5),
			runtime.Str(""),
			runtime.Str("  Mixed Case\t"),
			runtime.Safe("<u>safe</u>"),
			runtime.Arr(runtime.NewList(runtime.Str("y"), runtime.Str("x"))),
		)),
		"empty": runtime.Arr(runtime.NewArray()),
	}
	e := NewWithArray(nil)
	for _, name := range audited {
		fastTpl := fmt.Sprintf("@for v in vals {\n[{{ v | %s | join(\",\") }}]\n@}\n", name)
		genTpl := fmt.Sprintf("@for v in vals {\n[{{ v | %s(...empty) | join(\",\") }}]\n@}\n", name)
		fastOut, fastErr := e.RenderString("f-"+name, fastTpl, vars)
		genOut, genErr := e.RenderString("g-"+name, genTpl, vars)
		if (fastErr == nil) != (genErr == nil) {
			t.Errorf("%s: error mismatch: fast=%v general=%v", name, fastErr, genErr)
			continue
		}
		if fastErr != nil {
			// Error text is position-bearing; strip the template name (the
			// only intentional difference between the two renders).
			f := strings.ReplaceAll(fastErr.Error(), "f-"+name, "T")
			g := strings.ReplaceAll(genErr.Error(), "g-"+name, "T")
			if f != g {
				t.Errorf("%s: error text mismatch:\n fast    %q\n general %q", name, fastErr, genErr)
			}
			continue
		}
		if fastOut != genOut {
			t.Errorf("%s: output mismatch:\n fast    %q\n general %q", name, fastOut, genOut)
		}
	}
}
