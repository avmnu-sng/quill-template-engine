package quill

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// TestSourceFunction covers the source() function returning a template's raw,
// unparsed text (spec 03 Section 3.2), and the ignore_missing form.
func TestSourceFunction(t *testing.T) {
	tmpls := map[string]string{
		"frag.ql": "RAW {{ x }} BODY",
		"main.ql": `{{ source("frag.ql") }}`,
	}
	e := NewFromMap(tmpls)
	out, err := e.Render(context.Background(), "main.ql", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "RAW {{ x }} BODY" {
		t.Errorf("source() = %q", out)
	}

	missing := `{{ source("nope.ql", true) }}END`
	out, err = e.RenderString(context.Background(), "m", missing, nil)
	if err != nil || out != "END" {
		t.Errorf("source ignore_missing = %q, err=%v", out, err)
	}

	if _, err := e.RenderString(context.Background(), "m", `{{ source("nope.ql") }}`, nil); err == nil {
		t.Error("missing source without ignore_missing should error")
	}
}

// TestTemplateFromString covers compiling and rendering a string at runtime with
// the live context (spec 03 Section 3.3).
func TestTemplateFromString(t *testing.T) {
	e := NewFromMap(nil)
	out, err := e.RenderString(context.Background(), "m",
		`{{ template_from_string("Hi {{ name | upper }}") }}`,
		map[string]runtime.Value{"name": runtime.Str("ada")})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "Hi ADA" {
		t.Errorf("template_from_string = %q", out)
	}
}

// TestDumpFunction covers dump() producing a Go-native structured render of a
// value (spec 03 Section 3.3).
func TestDumpFunction(t *testing.T) {
	e := NewFromMap(nil)
	out, err := e.RenderString(context.Background(), "m", `{{ dump(n) }}`, map[string]runtime.Value{"n": runtime.Int(7)})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "int(7)" {
		t.Errorf("dump = %q", out)
	}
}

// TestHostConstantsAndEnums covers the host registering constants and an
// enumeration that the constant()/enum()/enum_cases() callables and the
// `is constant` test resolve (spec 03 Sections 3.2, 4).
func TestHostConstantsAndEnums(t *testing.T) {
	exts := ext.Core()
	exts.AddConstant("APP_NAME", runtime.Str("quill"))
	exts.AddEnum("Suit", []runtime.Value{runtime.Str("hearts"), runtime.Str("spades")})

	e := NewFromMap(nil, WithExtensions(exts))
	out, err := e.RenderString(context.Background(), "m",
		`{{ constant("APP_NAME") }} / {{ enum("Suit") }} / {{ enum_cases("Suit") | join(",") }} / {{ "quill" is constant("APP_NAME") }}`,
		nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "quill / hearts / hearts,spades / true"
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

// TestHostFunctionRegistration exercises the extension surface (spec 03 Section
// 3.5): a host registers an application function and calls it from a template.
// Application-specific functions are registered by the host, not shipped as
// engine built-ins.
func TestHostFunctionRegistration(t *testing.T) {
	exts := ext.Core()
	exts.AddFunction(&ext.Function{
		Name: "javaBoxed",
		Fn: func(ctx context.Context, args []runtime.Value) (runtime.Value, error) {
			name, _ := runtime.ToText(args[0])
			boxed := map[string]string{
				"boolean": "Boolean", "int": "Integer", "long": "Long",
				"float": "Float", "double": "Double", "char": "Character",
			}
			if b, ok := boxed[name]; ok {
				return runtime.Str(b), nil
			}
			return runtime.Str(name), nil
		},
	})
	e := NewFromMap(nil, WithExtensions(exts))
	out, err := e.RenderString(context.Background(), "m", `{{ javaBoxed("int") }} {{ javaBoxed("Widget") }}`, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "Integer Widget" {
		t.Errorf("host function = %q", out)
	}
}

// TestEscapeStrategiesEndToEnd covers the escape filter under an autoescape-html
// environment and via explicit strategy selection (spec 03 Section 5.5).
func TestEscapeStrategiesEndToEnd(t *testing.T) {
	e := NewFromMap(nil)
	out, err := e.RenderString(context.Background(), "m",
		`{{ "a b" | escape("url") }} {{ "<x>" | escape("html") }} {{ "x y" | escape("js") }}`,
		nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "a%20b") || !strings.Contains(out, "&lt;x&gt;") || !strings.Contains(out, `\x20`) {
		t.Errorf("escape strategies = %q", out)
	}
}

// TestRandomSeedDeterminism covers WithRandomSeed making random()/shuffle
// reproducible (spec 03 Section 3.2, X15): two environments with the same seed
// produce identical output, and the second positional argument to random() is
// the inclusive max bound, not a seed.
func TestRandomSeedDeterminism(t *testing.T) {
	body := `{{ random(1000) }}|{{ random(10, 20) }}|{{ [1,2,3,4,5] | shuffle | join(",") }}`
	a, err := NewFromMap(nil, WithRandomSeed(99)).RenderString(context.Background(), "m", body, nil)
	if err != nil {
		t.Fatalf("render a: %v", err)
	}
	b, err := NewFromMap(nil, WithRandomSeed(99)).RenderString(context.Background(), "m", body, nil)
	if err != nil {
		t.Fatalf("render b: %v", err)
	}
	if a != b {
		t.Errorf("seeded output not reproducible: %q vs %q", a, b)
	}

	// random(lo, hi) draws within the inclusive bound, never the literal seed.
	for i := 0; i < 50; i++ {
		out, err := NewFromMap(nil).RenderString(context.Background(), "m", `{{ random(10, 20) }}`, nil)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		n, err := strconv.Atoi(out)
		if err != nil || n < 10 || n > 20 {
			t.Fatalf("random(10,20) out of [10,20]: %q", out)
		}
	}
}

// TestKeywordTests covers the spec-spelled keyword tests is true / is null /
// is none reaching their registered predicates (spec 03 Section 4).
func TestKeywordTests(t *testing.T) {
	body := `{{ flag is true }}|{{ x is null }}|{{ y is none }}|{{ 1 is true }}`
	out, err := NewFromMap(nil).RenderString(context.Background(), "m", body, map[string]runtime.Value{
		"flag": runtime.Bool(true),
		"x":    runtime.Null(),
		"y":    runtime.Str("set"),
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "true|true|false|false" {
		t.Errorf("keyword tests = %q", out)
	}
}
