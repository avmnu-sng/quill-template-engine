package parse

import (
	"strings"
	"testing"
)

// TestParseMacroKwargsTail parses a "**name" kwargs tail on a macro and asserts
// the parameter dumps with the "**" marker, alongside the positional variadic.
func TestParseMacroKwargsTail(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "kwargs only",
			src:  "@macro f(**opts) {\nx\n@}\n",
			want: "Param **opts",
		},
		{
			name: "positional then kwargs",
			src:  "@macro f(name, **opts) {\nx\n@}\n",
			want: "Param **opts",
		},
		{
			name: "variadic then kwargs",
			src:  "@macro f(...xs, **opts) {\nx\n@}\n",
			want: "Param **opts",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDump(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Errorf("dump %q does not contain %q", got, tc.want)
			}
			// A "...name" and a "**name" are distinct markers.
			if strings.Contains(tc.src, "...xs") && !strings.Contains(got, "Param ...xs") {
				t.Errorf("dump %q missing the variadic marker", got)
			}
		})
	}
}

// TestParseParamTailRules rejects a parameter following a tail capture: the
// positional variadic and the kwargs tail each obey a fixed terminal position.
func TestParseParamTailRules(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"@macro f(...xs, y) {\nx\n@}\n", "variadic"},
		{"@macro f(**opts, y) {\nx\n@}\n", "kwargs"},
		{"@macro f(**opts, ...xs) {\nx\n@}\n", "kwargs"},
		{"@macro f(a, ...xs, b, **o) {\nx\n@}\n", "variadic"},
	}
	for _, tc := range cases {
		mustErr(t, tc.src, tc.want)
	}
}

// TestParseKwargsNeedsName rejects a "**" not followed by a name.
func TestParseKwargsNeedsName(t *testing.T) {
	mustErr(t, "@macro f(**) {\nx\n@}\n", "after '**'")
}

// TestParseArrowRejectsKwargs asserts that a "**name" kwargs parameter is not
// accepted on an arrow parameter list: arrows are positional and have no
// named-argument mechanism, so the kwargs tail is a macro/block feature. The
// probes mirror TestParseParamTailRules for the arrow path, including a bare
// kwargs, a kwargs after a positional, a kwargs before a positional, and a double
// kwargs, each of which the macro path governs with tail-order rules.
func TestParseArrowRejectsKwargs(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"positional then kwargs", "{{ (a, **opts) => opts }}"},
		{"kwargs then positional", "{{ (**opts, x) => x }}"},
		{"kwargs only", "{{ (**opts) => opts }}"},
		{"double kwargs", "{{ (**a, **b) => a }}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustErr(t, tc.src, "'**name' kwargs parameter is only allowed on a macro or block")
		})
	}
}
