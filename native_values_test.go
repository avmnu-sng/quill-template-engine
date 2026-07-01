package quill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// TestRenderValuesParity renders from native Go bindings and asserts the output
// matches the equivalent hand-built runtime.Value render byte-for-byte.
func TestRenderValuesParity(t *testing.T) {
	type user struct {
		Name  string   `quill:"name"`
		Admin bool     `quill:"admin"`
		Tags  []string `quill:"tags"`
	}
	body := `{{ user.name | upper }}{{ ", admin" if user.admin }}: {{ user.tags | join("/") }}`

	env := NewWithArray(map[string]string{"t.ql": body})
	native, err := env.RenderValues("t.ql", map[string]any{
		"user": user{Name: "ada", Admin: true, Tags: []string{"x", "y"}},
	})
	if err != nil {
		t.Fatalf("RenderValues: %v", err)
	}

	// Hand-build the equivalent bindings.
	hand := runtime.NewArray()
	hand.SetStr("name", runtime.Str("ada"))
	hand.SetStr("admin", runtime.Bool(true))
	hand.SetStr("tags", runtime.Arr(runtime.NewList(runtime.Str("x"), runtime.Str("y"))))
	built, err := env.Render("t.ql", map[string]runtime.Value{"user": runtime.Arr(hand)})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if native != built {
		t.Fatalf("native render %q != hand-built render %q", native, built)
	}
	if native != "ADA, admin: x/y" {
		t.Fatalf("render = %q, want %q", native, "ADA, admin: x/y")
	}
}

// TestRenderValuesMapsAndSlices passes native maps and slices directly.
func TestRenderValuesMapsAndSlices(t *testing.T) {
	env := NewWithArray(map[string]string{
		"t.ql": `@for k, v in cfg {{{ k }}={{ v }}
@}`,
	})
	out, err := env.RenderValues("t.ql", map[string]any{
		"cfg": map[string]any{"gamma": 3, "alpha": 1, "beta": 2},
	})
	if err != nil {
		t.Fatalf("RenderValues: %v", err)
	}
	// Deterministic sorted key order: alpha, beta, gamma.
	want := "alpha=1\nbeta=2\ngamma=3\n"
	if out != want {
		t.Fatalf("render = %q, want %q", out, want)
	}
}

// TestRenderStringValues marshals native bindings for an ad-hoc body.
func TestRenderStringValues(t *testing.T) {
	env := NewWithArray(nil)
	out, err := env.RenderStringValues("ad-hoc", `{{ nums | join("+") }}`, map[string]any{
		"nums": []int{1, 2, 3, 4},
	})
	if err != nil {
		t.Fatalf("RenderStringValues: %v", err)
	}
	if out != "1+2+3+4" {
		t.Fatalf("render = %q, want 1+2+3+4", out)
	}
}

// TestRenderValuesMixedHandBuilt lets a hand-built runtime.Value ride alongside
// native bindings in the same map.
func TestRenderValuesMixedHandBuilt(t *testing.T) {
	env := NewWithArray(map[string]string{"t.ql": `{{ a }}-{{ b }}`})
	out, err := env.RenderValues("t.ql", map[string]any{
		"a": "native",
		"b": runtime.Str("built"),
	})
	if err != nil {
		t.Fatalf("RenderValues: %v", err)
	}
	if out != "native-built" {
		t.Fatalf("render = %q, want native-built", out)
	}
}

// TestRenderValuesUnsupported surfaces a marshaling error from a binding and
// renders nothing.
func TestRenderValuesUnsupported(t *testing.T) {
	env := NewWithArray(map[string]string{"t.ql": `{{ x }}`})
	out, err := env.RenderValues("t.ql", map[string]any{"x": make(chan int)})
	if err == nil {
		t.Fatalf("RenderValues with chan binding = nil error, want marshaling error")
	}
	if out != "" {
		t.Fatalf("failed RenderValues returned output %q, want empty", out)
	}
	if !strings.Contains(err.Error(), "marshal") {
		t.Fatalf("error %q does not mention marshaling", err.Error())
	}
}

// TestRenderValuesMatchesConformanceFixture renders the native-values
// conformance fixture's template from native Go bindings and asserts the output
// matches the golden expected.out byte-for-byte, tying the FromGo feature to the
// fixture the JSON-data harness renders.
func TestRenderValuesMatchesConformanceFixture(t *testing.T) {
	tmpl, err := os.ReadFile(filepath.Join("testdata", "conformance", "native-values", "template.ql"))
	if err != nil {
		t.Fatalf("read fixture template: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "conformance", "native-values", "expected.out"))
	if err != nil {
		t.Fatalf("read fixture expected.out: %v", err)
	}

	type addr struct {
		City string `quill:"city"`
		Zip  string `quill:"zip"`
	}
	type user struct {
		Name string         `quill:"name"`
		Tags []string       `quill:"tags"`
		Addr addr           `quill:"addr"`
		Meta map[string]int `quill:"meta"`
	}

	env := NewWithArray(map[string]string{"template.ql": string(tmpl)})
	out, err := env.RenderValues("template.ql", map[string]any{
		"user": user{
			Name: "ada",
			Tags: []string{"x", "y"},
			Addr: addr{City: "here", Zip: "00000"},
			Meta: map[string]int{"age": 30, "level": 9},
		},
		"scores": []int{10, 20, 30},
	})
	if err != nil {
		t.Fatalf("RenderValues: %v", err)
	}
	if out != string(want) {
		t.Fatalf("native render:\n%q\nwant:\n%q", out, string(want))
	}
}

// TestRenderValuesNilBindings renders with no bindings.
func TestRenderValuesNilBindings(t *testing.T) {
	env := NewWithArray(map[string]string{"t.ql": `static`})
	out, err := env.RenderValues("t.ql", nil)
	if err != nil {
		t.Fatalf("RenderValues(nil): %v", err)
	}
	if out != "static" {
		t.Fatalf("render = %q, want static", out)
	}
}
