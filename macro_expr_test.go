package quill

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// TestMacroKwargsTail exercises the trailing "**name" kwargs parameter: it
// collects excess NAMED call arguments into a mapping, symmetric with the
// "...name" positional variadic. The forwarded mapping preserves insertion
// order and is addressable by key inside the macro body.
func TestMacroKwargsTail(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		want string
	}{
		{
			name: "collects excess named into mapping",
			tmpl: "@macro render_field(name, **opts) {\n" +
				"{{ name }}[{{ opts | keys | join(\",\") }}]\n@}\n" +
				"{{ render_field(\"email\", id: \"e1\", class: \"big\") }}",
			want: "email[id,class]",
		},
		{
			name: "kwargs addressable by key",
			tmpl: "@macro render_field(name, **opts) {\n" +
				"<{{ name }} class=\"{{ opts.class }}\">\n@}\n" +
				"{{ render_field(\"email\", class: \"wide\") }}",
			want: "<email class=\"wide\">",
		},
		{
			name: "empty kwargs is an empty mapping",
			tmpl: "@macro render_field(name, **opts) {\n" +
				"{{ name }}:{{ opts | length }}\n@}\n" +
				"{{ render_field(\"email\") }}",
			want: "email:0",
		},
		{
			name: "positional then kwargs",
			tmpl: "@macro tag(name, kind, **attrs) {\n" +
				"{{ kind }}:{{ name }}:{{ attrs.role }}\n@}\n" +
				"{{ tag(\"a\", \"link\", role: \"nav\") }}",
			want: "link:a:nav",
		},
		{
			name: "positional variadic and kwargs together",
			tmpl: "@macro row(label, ...cells, **attrs) {\n" +
				"{{ label }}|{{ cells | join(\",\") }}|{{ attrs.id }}\n@}\n" +
				"{{ row(\"r\", 1, 2, 3, id: \"x\") }}",
			want: "r|1,2,3|x",
		},
		{
			name: "declared param bound by name is not collected",
			tmpl: "@macro fld(name, label = null, **opts) {\n" +
				"{{ label }}/{{ name }}/{{ opts | keys | join(\",\") }}\n@}\n" +
				"{{ fld(\"email\", label: \"Email\", id: \"e\") }}",
			want: "Email/email/id",
		},
		{
			name: "kwargs forwarded to a nested macro via spread",
			tmpl: "@macro inner(**a) {\n{{ a.k }}\n@}\n" +
				"@macro outer(**opts) {\n{{ inner(...opts) }}\n@}\n" +
				"{{ outer(k: \"deep\") }}",
			want: "deep",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := render(t, tc.tmpl, nil)
			if strings.TrimSpace(got) != tc.want {
				t.Errorf("got %q, want %q", strings.TrimSpace(got), tc.want)
			}
		})
	}
}

// TestMacroKwargsSymmetry proves the kwargs tail is the named-argument mirror of
// the positional variadic: without a kwargs tail an unknown named argument is a
// typo error, and with one the same argument is absorbed silently.
func TestMacroKwargsSymmetry(t *testing.T) {
	e := NewFromMap(nil)

	// No kwargs tail: an unknown named argument is rejected.
	_, err := e.RenderString("t", "@macro f(name) {\n{{ name }}\n@}\n{{ f(\"a\", extra: 1) }}", nil)
	if err == nil {
		t.Fatal("expected an unknown-parameter error without a kwargs tail")
	}
	if !strings.Contains(err.Error(), "no parameter") {
		t.Errorf("error = %q, want an unknown-parameter message", err.Error())
	}

	// With a kwargs tail: the same argument is absorbed.
	got := render(t, "@macro f(name, **rest) {\n{{ name }}:{{ rest.extra }}\n@}\n{{ f(\"a\", extra: 1) }}", nil)
	if strings.TrimSpace(got) != "a:1" {
		t.Errorf("got %q, want %q", strings.TrimSpace(got), "a:1")
	}
}

// TestMacroKwargsDuplicate rejects the same named key supplied twice to a kwargs
// tail, matching the duplicate-named-argument rule for ordinary parameters.
func TestMacroKwargsDuplicate(t *testing.T) {
	e := NewFromMap(nil)
	_, err := e.RenderString("t",
		"@macro f(**opts) {\n{{ opts.k }}\n@}\n{{ f(k: 1, k: 2) }}", nil)
	if err == nil {
		t.Fatal("expected a duplicate-named-argument error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error = %q, want a duplicate message", err.Error())
	}
}

// TestMacroTailNotLast rejects a parameter following a variadic or kwargs tail:
// each tail capture must be the last parameter.
func TestMacroTailNotLast(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		want string
	}{
		{"after variadic", "@macro f(...xs, y) {\n{{ y }}\n@}\n{{ f() }}", "variadic"},
		{"after kwargs", "@macro f(**opts, y) {\n{{ y }}\n@}\n{{ f() }}", "kwargs"},
		{"kwargs before variadic", "@macro f(**opts, ...xs) {\n{{ xs }}\n@}\n{{ f() }}", "kwargs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewFromMap(nil)
			_, err := e.RenderString("t", tc.tmpl, nil)
			if err == nil {
				t.Fatalf("expected a parse error for %q", tc.tmpl)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want a message mentioning %q", err.Error(), tc.want)
			}
		})
	}
}

// TestInlineRegistryTests exercises the value-level `is filter` / `is function`
// / `is test` tests: they take a callable-name string and report registration,
// backed by the same predicates the @guard statement uses.
func TestInlineRegistryTests(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want string
	}{
		{"upper is filter", "{{ \"upper\" is filter }}", "true"},
		{"missing is not filter", "{{ \"nope\" is filter }}", "false"},
		{"range is function", "{{ \"range\" is function }}", "true"},
		{"missing is not function", "{{ \"nope\" is function }}", "false"},
		{"empty is test", "{{ \"empty\" is test }}", "true"},
		{"missing is not test", "{{ \"nope\" is test }}", "false"},
		{"is not filter negation", "{{ \"nope\" is not filter }}", "true"},
		{"is not filter on present", "{{ \"upper\" is not filter }}", "false"},
		{"cross-kind: upper is not a function", "{{ \"upper\" is function }}", "false"},
		{"cross-kind: range is not a filter", "{{ \"range\" is filter }}", "false"},
		{"name from a variable", "@set n = \"lower\"\n{{ n is filter }}", "true"},
		{"non-string subject is absent", "{{ 42 is filter }}", "false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := render(t, tc.expr, nil)
			if strings.TrimSpace(got) != tc.want {
				t.Errorf("got %q, want %q", strings.TrimSpace(got), tc.want)
			}
		})
	}
}

// TestInlineRegistryTestInCondition uses `is filter` inside an @if, the natural
// expression-level complement to the @guard statement.
func TestInlineRegistryTestInCondition(t *testing.T) {
	got := render(t, "@if \"upper\" is filter {\nyes\n@} else {\nno\n@}\n", nil)
	if strings.TrimSpace(got) != "yes" {
		t.Errorf("got %q, want %q", strings.TrimSpace(got), "yes")
	}
	got = render(t, "@if \"nope\" is function {\nyes\n@} else {\nno\n@}\n", nil)
	if strings.TrimSpace(got) != "no" {
		t.Errorf("got %q, want %q", strings.TrimSpace(got), "no")
	}
}

// TestInlineRegistryTestHostShadow proves a host test named "filter"/"function"/
// "test" shadows the built-in registry-existence test, since a registered test
// of the same name wins the lookup.
func TestInlineRegistryTestHostShadow(t *testing.T) {
	set := ext.Core()
	set.AddTest(&ext.Test{
		Name: "filter",
		Fn:   func(args []runtime.Value) (bool, error) { return true, nil },
	})
	e := NewFromMap(nil, WithExtensions(set))
	// "nope" is not a registered filter, but the host "filter" test always
	// returns true, proving the host shadow wins over the built-in predicate.
	out, err := e.RenderString("t", "{{ \"nope\" is filter }}", nil)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if strings.TrimSpace(out) != "true" {
		t.Errorf("host shadow: got %q, want true", strings.TrimSpace(out))
	}
}
