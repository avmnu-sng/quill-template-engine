package quill

import (
	"strings"
	"testing"

	"github.com/avmnusng/quill-template-engine/ext"
	"github.com/avmnusng/quill-template-engine/runtime"
)

// TestSourceFunction covers the source() function returning a template's raw,
// unparsed text (spec 03 Section 3.2), and the ignore_missing form.
func TestSourceFunction(t *testing.T) {
	tmpls := map[string]string{
		"frag.ql": "RAW {{ x }} BODY",
		"main.ql": `{{ source("frag.ql") }}`,
	}
	e := NewWithArray(tmpls)
	out, err := e.Render("main.ql", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if out != "RAW {{ x }} BODY" {
		t.Errorf("source() = %q", out)
	}

	missing := `{{ source("nope.ql", true) }}END`
	out, err = e.RenderString("m", missing, nil)
	if err != nil || out != "END" {
		t.Errorf("source ignore_missing = %q, err=%v", out, err)
	}

	if _, err := e.RenderString("m", `{{ source("nope.ql") }}`, nil); err == nil {
		t.Error("missing source without ignore_missing should error")
	}
}

// TestTemplateFromString covers compiling and rendering a string at runtime with
// the live context (spec 03 Section 3.3).
func TestTemplateFromString(t *testing.T) {
	e := NewWithArray(nil)
	out, err := e.RenderString("m",
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
	e := NewWithArray(nil)
	out, err := e.RenderString("m", `{{ dump(n) }}`, map[string]runtime.Value{"n": runtime.Int(7)})
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

	e := NewWithArray(nil, WithExtensions(exts))
	out, err := e.RenderString("m",
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

// TestHostFunctionRegistration exercises the extension surface the spec
// catalogues as a parity capability (spec 03 Section 3.5): a host registers an
// application function and calls it from a template. This stands in for the
// corpus-specific functions, which are intentionally not engine built-ins.
func TestHostFunctionRegistration(t *testing.T) {
	exts := ext.Core()
	exts.AddFunction(&ext.Function{
		Name: "javaBoxed",
		Fn: func(args []runtime.Value) (runtime.Value, error) {
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
	e := NewWithArray(nil, WithExtensions(exts))
	out, err := e.RenderString("m", `{{ javaBoxed("int") }} {{ javaBoxed("Widget") }}`, nil)
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
	e := NewWithArray(nil)
	out, err := e.RenderString("m",
		`{{ "a b" | escape("url") }} {{ "<x>" | escape("html") }} {{ "x y" | escape("js") }}`,
		nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "a%20b") || !strings.Contains(out, "&lt;x&gt;") || !strings.Contains(out, `\x20`) {
		t.Errorf("escape strategies = %q", out)
	}
}
