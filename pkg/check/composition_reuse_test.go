package check

import "testing"

// TestCheckCompositionReuse covers the composition/reuse statements: the checker
// walks their bodies without error and types the expressions they carry.
func TestCheckCompositionReuse(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"provide and yield", "@provide imports {import a\n@}\n@yield imports\n"},
		{"provide body typed", "@types {\n  n: int\n@}\n@provide s {{{ n + 1 }}\n@}\n@yield s\n"},
		{"call block", "@macro w(t) {<{{ t }}>{{ caller() }}\n@}\n@call w(\"p\") {body\n@}"},
		{"call block with params", "@macro r() {{{ caller(1, 2) }}\n@}\n@call(a, b) r() {{{ a + b }}\n@}"},
		{"recursive for", "@types {\n  tree: any\n@}\n@for n in tree recursive {\n{{ n }}{{ loop(n) }}\n@}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSrc(t, tc.src, nil); err != nil {
				t.Fatalf("checker rejected %q: %v", tc.src, err)
			}
		})
	}
}

// TestCheckCallBlockArgTypeError proves a type error inside a @call macro
// argument is surfaced by the checker.
func TestCheckCallBlockArgTypeError(t *testing.T) {
	// n is a string; using it where an int op applies is an error at the arg.
	src := "@types {\n  n: string\n@}\n@macro m(x: int) {{{ x }}\n@}\n@call m(n + 1) {body\n@}"
	if err := checkSrc(t, src, nil); err == nil {
		t.Fatal("expected a type error for a string in an int arithmetic argument")
	}
}
