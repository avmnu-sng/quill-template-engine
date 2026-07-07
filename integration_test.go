package quill

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// mkMap builds an *Array map from name/value pairs for test fixtures.
func mkMap(pairs ...any) runtime.Value {
	m := runtime.NewArray()
	for i := 0; i+1 < len(pairs); i += 2 {
		m.SetStr(pairs[i].(string), pairs[i+1].(runtime.Value))
	}
	return runtime.Arr(m)
}

func mkList(vals ...runtime.Value) runtime.Value { return runtime.Arr(runtime.NewList(vals...)) }

// render is a helper that renders body as an ad-hoc template with vars.
func render(t *testing.T, body string, vars map[string]runtime.Value) string {
	t.Helper()
	e := NewWithArray(nil)
	out, err := e.RenderString("test", body, vars)
	if err != nil {
		t.Fatalf("render error: %v\ntemplate:\n%s", err, body)
	}
	return out
}

// TestAnchor renders the spec's anchor example (spec 00 Section 2) with data and
// verifies inheritance + block override + for + upper + postfix-if + default.
func TestAnchor(t *testing.T) {
	tmpls := map[string]string{
		"base.tmpl": "HEADER\n@block body {\ndefault body\n@}\nFOOTER",
		"anchor.ql": "@extends \"base.tmpl\"\n\n@block body {\n@for u in users {\n{{ u.name | upper }}{{ \", admin\" if u.isAdmin }}\n@}\n@}\n",
	}
	e := NewWithArray(tmpls)
	users := mkList(
		mkMap("name", runtime.Str("ada"), "isAdmin", runtime.Bool(true)),
		mkMap("name", runtime.Str("bob"), "isAdmin", runtime.Bool(false)),
	)
	out, err := e.Render("anchor.ql", map[string]runtime.Value{"users": users})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "HEADER") || !strings.Contains(out, "FOOTER") {
		t.Errorf("missing parent frame:\n%s", out)
	}
	if !strings.Contains(out, "ADA, admin") {
		t.Errorf("admin row missing:\n%s", out)
	}
	if !strings.Contains(out, "BOB") || strings.Contains(out, "BOB, admin") {
		t.Errorf("postfix-if wrong for non-admin:\n%s", out)
	}
}

func TestInterpolationAndExpressions(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		vars map[string]runtime.Value
		want string
	}{
		{"int", "{{ 1 + 2 }}", nil, "3"},
		{"int div exact", "{{ 6 / 3 }}", nil, "2"},
		{"float div", "{{ 7 / 2 }}", nil, "3.5"},
		{"concat", "{{ \"a\" ~ 1 ~ true }}", nil, "a1true"},
		{"bool render", "{{ false }}", nil, "false"},
		{"null render", "{{ null }}", nil, ""},
		{"comparison", "{{ 2 < 3 }}", nil, "true"},
		{"equality no coerce", "{{ 1 == \"1\" }}", nil, "false"},
		{"ternary", "{{ 1 ? \"y\" : \"n\" }}", nil, "y"},
		{"coalesce undefined", "{{ missing ?? \"fb\" }}", nil, "fb"},
		{"coalesce chain", "{{ a.b.c ?? \"fb\" }}", nil, "fb"},
		{"truthy zero string", "{{ \"0\" ? \"t\" : \"f\" }}", nil, "t"},
		{"power", "{{ 2 ** 10 }}", nil, "1024"},
		{"neg power", "{{ -1 ** 0 }}", nil, "-1"},
		{"membership", "{{ 2 in [1, 2, 3] }}", nil, "true"},
		{"string interp", "{{ \"hi #{name}\" }}", map[string]runtime.Value{"name": runtime.Str("ada")}, "hi ada"},
		{"index", "{{ xs[1] }}", map[string]runtime.Value{"xs": mkList(runtime.Str("a"), runtime.Str("b"))}, "b"},
		{"slice", "{{ \"hello\"[1:4] }}", nil, "ell"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := render(t, c.tmpl, c.vars); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestControlFlow(t *testing.T) {
	if got := render(t, "@if x > 0 {\npos\n@} elseif x < 0 {\nneg\n@} else {\nzero\n@}\n",
		map[string]runtime.Value{"x": runtime.Int(-5)}); !strings.Contains(got, "neg") {
		t.Errorf("elseif branch: %q", got)
	}
	// for-else over empty.
	if got := render(t, "@for x in items {\n{{ x }}\n@} else {\nempty\n@}\n",
		map[string]runtime.Value{"items": mkList()}); !strings.Contains(got, "empty") {
		t.Errorf("for-else: %q", got)
	}
	// loop metadata.
	got := render(t, "@for x in [10, 20, 30] {\n{{ loop.index }}:{{ x }}:{{ loop.first }}:{{ loop.last }}\n@}\n", nil)
	if !strings.Contains(got, "1:10:true:false") || !strings.Contains(got, "3:30:false:true") {
		t.Errorf("loop metadata: %q", got)
	}
	// key, value over a mapping.
	got = render(t, "@for k, v in m {\n{{ k }}={{ v }}\n@}\n",
		map[string]runtime.Value{"m": mkMap("a", runtime.Int(1), "b", runtime.Int(2))})
	if !strings.Contains(got, "a=1") || !strings.Contains(got, "b=2") {
		t.Errorf("for k,v: %q", got)
	}
}

// TestForNonIterableStrict verifies the strict divergence: a for over a
// non-iterable is a runtime error, NOT a silent empty loop (spec 01 Section 4.2).
func TestForNonIterableStrict(t *testing.T) {
	e := NewWithArray(nil)
	_, err := e.RenderString("t", "@for x in n {\n{{ x }}\n@}\n",
		map[string]runtime.Value{"n": runtime.Int(5)})
	if err == nil {
		t.Fatal("expected an iteration error over a non-iterable")
	}
}

func TestSetAndCapture(t *testing.T) {
	if got := render(t, "@set a = 3\n@set b = a + 4\n{{ b }}", nil); got != "7" {
		t.Errorf("set: %q", got)
	}
	got := render(t, "@set banner = capture {\nLINE {{ n }}\n@}\n[{{ banner }}]", map[string]runtime.Value{"n": runtime.Int(1)})
	if !strings.Contains(got, "LINE 1") {
		t.Errorf("capture: %q", got)
	}
}

func TestWithAndApply(t *testing.T) {
	got := render(t, "@with { x: 1, y: 2 } {\n{{ x }}{{ y }}\n@}\n", nil)
	if !strings.Contains(got, "12") {
		t.Errorf("with: %q", got)
	}
	// only replaces the context.
	got = render(t, "@with { x: 1 } only {\n{{ x }}{{ outer ?? \"-\" }}\n@}\n",
		map[string]runtime.Value{"outer": runtime.Str("O")})
	if !strings.Contains(got, "1-") {
		t.Errorf("with only: %q", got)
	}
	// apply chains filters over the captured body.
	got = render(t, "@apply | upper {\nhello\n@}\n", nil)
	if !strings.Contains(got, "HELLO") {
		t.Errorf("apply: %q", got)
	}
}

func TestStrictUndefined(t *testing.T) {
	e := NewWithArray(nil)
	_, err := e.RenderString("t", "{{ missing }}", nil)
	if err == nil {
		t.Fatal("expected a strict-undefined error")
	}
	// is defined never throws.
	if got := render(t, "{{ missing is defined ? \"y\" : \"n\" }}", nil); got != "n" {
		t.Errorf("is defined: %q", got)
	}
	// lenient mode yields empty.
	le := NewWithArray(nil, WithStrictVariables(false))
	out, err := le.RenderString("t", "[{{ missing }}]", nil)
	if err != nil || out != "[]" {
		t.Errorf("lenient: out=%q err=%v", out, err)
	}
}

func TestInheritanceParent(t *testing.T) {
	tmpls := map[string]string{
		"base.ql":  "@block title {\nBASE\n@}\n",
		"child.ql": "@extends \"base.ql\"\n@block title {\n{{ parent() }}+CHILD\n@}\n",
	}
	e := NewWithArray(tmpls)
	out, err := e.Render("child.ql", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "BASE") || !strings.Contains(out, "+CHILD") {
		t.Errorf("parent(): %q", out)
	}
}

func TestMacroRecursionAndImport(t *testing.T) {
	// Direct recursion by bare name (the macro namespace, spec 01 Section 5.3).
	tmpl := "@macro tree(node) {\n{{ node.label }}\n@for c in (node.children ?? []) {\n{{ tree(c) }}\n@}\n@}\n{{ tree(root) }}"
	root := mkMap("label", runtime.Str("A"), "children", mkList(
		mkMap("label", runtime.Str("B")),
		mkMap("label", runtime.Str("C"), "children", mkList(mkMap("label", runtime.Str("D")))),
	))
	got := render(t, tmpl, map[string]runtime.Value{"root": root})
	for _, label := range []string{"A", "B", "C", "D"} {
		if !strings.Contains(got, label) {
			t.Errorf("recursion missing %q in %q", label, got)
		}
	}

	// @from import: call an imported macro by bare name.
	tmpls := map[string]string{
		"forms.ql": "@macro input(name) {\n<{{ name }}>\n@}\n",
		"page.ql":  "@from \"forms.ql\" import input\n{{ input(\"x\") }}",
	}
	e := NewWithArray(tmpls)
	out, err := e.Render("page.ql", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<x>") {
		t.Errorf("from import: %q", out)
	}

	// @import namespace: call ns.macro().
	tmpls2 := map[string]string{
		"forms.ql": "@macro input(name) {\n[{{ name }}]\n@}\n",
		"page.ql":  "@import \"forms.ql\" as forms\n{{ forms.input(\"y\") }}",
	}
	e2 := NewWithArray(tmpls2)
	out2, err := e2.Render("page.ql", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, "[y]") {
		t.Errorf("import namespace: %q", out2)
	}
}

func TestMacroDefaultsAndVariadic(t *testing.T) {
	got := render(t, "@macro greet(name, greeting: string = \"Hello\") {\n{{ greeting }} {{ name | default(\"guest\") }}\n@}\n{{ greet(\"ada\") }}|{{ greet(\"bob\", \"Hi\") }}", nil)
	if !strings.Contains(got, "Hello ada") || !strings.Contains(got, "Hi bob") {
		t.Errorf("macro defaults: %q", got)
	}
	got = render(t, "@macro all(...xs) {\n{{ xs | join(\",\") }}\n@}\n{{ all(1, 2, 3) }}", nil)
	if !strings.Contains(got, "1,2,3") {
		t.Errorf("variadic: %q", got)
	}
}

func TestInclude(t *testing.T) {
	tmpls := map[string]string{
		"row.ql":  "ROW:{{ user }}",
		"page.ql": "@include \"row.ql\" with { user: \"ada\" }\n@include \"missing.ql\" ignore missing\nEND",
	}
	e := NewWithArray(tmpls)
	out, err := e.Render("page.ql", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ROW:ada") || !strings.Contains(out, "END") {
		t.Errorf("include: %q", out)
	}
	// Candidate list: first existing wins.
	tmpls2 := map[string]string{
		"row.ql":  "ROW:{{ user }}",
		"cand.ql": "@include [\"nope.ql\", \"row.ql\"] with { user: \"z\" }",
	}
	e2 := NewWithArray(tmpls2)
	out, err = e2.Render("cand.ql", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ROW:z") {
		t.Errorf("candidate include: %q", out)
	}
}

func TestIncludeOnly(t *testing.T) {
	tmpls := map[string]string{
		"row.ql":  "{{ outer ?? \"-\" }}:{{ user }}",
		"page.ql": "@include \"row.ql\" with { user: \"a\" } only",
	}
	e := NewWithArray(tmpls)
	out, err := e.Render("page.ql", map[string]runtime.Value{"outer": runtime.Str("O")})
	if err != nil {
		t.Fatal(err)
	}
	if out != "-:a" { // only: the includer's context (outer) is NOT visible
		t.Errorf("include only: %q", out)
	}
}

func TestCoreStdlibInTemplates(t *testing.T) {
	cases := []struct {
		tmpl string
		want string
	}{
		{"{{ \"hi\" | upper }}", "HI"},
		{"{{ \"  x \" | trim }}", "x"},
		{"{{ [3, 1, 2] | sort | join(\",\") }}", "1,2,3"},
		{"{{ [1, 2, 3] | length }}", "3"},
		{"{{ [1, 2, 3] | first }}", "1"},
		{"{{ [1, 2, 3] | last }}", "3"},
		{"{{ [1, 2] | reverse | join(\",\") }}", "2,1"},
		{"{{ range(1, 3) | join(\",\") }}", "1,2,3"},
		{"{{ max(3, 9, 1) }}", "9"},
		{"{{ min([3, 9, 1]) }}", "1"},
		{"{{ 4 is even ? \"e\" : \"o\" }}", "e"},
		{"{{ [] is empty ? \"y\" : \"n\" }}", "y"},
	}
	for _, c := range cases {
		t.Run(c.tmpl, func(t *testing.T) {
			if got := render(t, c.tmpl, nil); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestEscapingOffByDefault verifies autoescape is off by default: an
// interpolation renders verbatim, even HTML metacharacters (spec 04 Section 8.1).
func TestEscapingOffByDefault(t *testing.T) {
	got := render(t, "{{ code }}", map[string]runtime.Value{"code": runtime.Str("List<Map<String,Integer>>")})
	if got != "List<Map<String,Integer>>" {
		t.Errorf("escaping must be OFF by default: %q", got)
	}
}

// TestEscapingHTMLOn verifies the html strategy escapes when enabled, while a
// raw/Safe value stays verbatim (spec 04 Section 8).
func TestEscapingHTMLOn(t *testing.T) {
	e := NewWithArray(nil, WithAutoescapeHTML(true))
	out, err := e.RenderString("t", "{{ v }}", map[string]runtime.Value{"v": runtime.Str("<b>&")})
	if err != nil {
		t.Fatal(err)
	}
	if out != "&lt;b&gt;&amp;" {
		t.Errorf("html escape: %q", out)
	}
	// raw cancels escaping at a single site.
	out, err = e.RenderString("t", "{{ v | raw }}", map[string]runtime.Value{"v": runtime.Str("<b>")})
	if err != nil {
		t.Fatal(err)
	}
	if out != "<b>" {
		t.Errorf("raw under html-on: %q", out)
	}
	// @escape off region under an html-on environment.
	out, err = e.RenderString("t", "@escape off {\n{{ v }}\n@}\n", map[string]runtime.Value{"v": runtime.Str("<b>")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<b>") {
		t.Errorf("@escape off: %q", out)
	}
	// Template TEXT is never escaped, only values.
	out, err = e.RenderString("t", "<div>{{ v }}</div>", map[string]runtime.Value{"v": runtime.Str("&")})
	if err != nil {
		t.Fatal(err)
	}
	if out != "<div>&amp;</div>" {
		t.Errorf("text-vs-value escaping: %q", out)
	}
}

func TestEscapeFilterExplicit(t *testing.T) {
	// Even under the off default, an explicit | escape escapes.
	got := render(t, "{{ v | escape }}", map[string]runtime.Value{"v": runtime.Str("<a>")})
	if got != "&lt;a&gt;" {
		t.Errorf("explicit escape: %q", got)
	}
}

func TestGuard(t *testing.T) {
	got := render(t, "@guard filter(\"upper\") {\nhas\n@} else {\nno\n@}\n", nil)
	if !strings.Contains(got, "has") {
		t.Errorf("guard present: %q", got)
	}
	got = render(t, "@guard filter(\"nonexistent\") {\nhas\n@} else {\nno\n@}\n", nil)
	if !strings.Contains(got, "no") {
		t.Errorf("guard absent: %q", got)
	}
}

func TestParseCaching(t *testing.T) {
	e := NewWithArray(map[string]string{"x.ql": "{{ 1 + 1 }}"})
	out1, _ := e.Render("x.ql", nil)
	out2, _ := e.Render("x.ql", nil) // second render hits the cache
	if out1 != "2" || out2 != "2" {
		t.Errorf("cached render: %q %q", out1, out2)
	}
}
