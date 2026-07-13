package compile_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/internal/compile"
)

// attrFastCases is the differential battery for the inline KArray dotted-read
// fast path: each case renders through the compiled path and must match the
// facade byte-for-byte (output or error text). It walks both arms of the
// generated read: mapping hits take the inline Arr.GetStr branch while every
// miss and every non-Array receiver re-enters runtime.GetAttribute. It also
// exercises the spill-elision edges, where the receiver binding local is
// embedded without a copy and an inline assignment in a sibling operand
// rebinds it between reads.
// A dotted member name is NAME-token-shaped by the grammar, so an
// integer-looking name cannot reach the fast path; the mixed-key fixtures pin
// that the inline GetStr resolves against the same canonical Int/Str key
// namespace the interpreter's getDot uses.
var attrFastCases = []compiledCase{
	// The hit arm: root vars, chains, loop targets, with-maps, and receivers
	// that are not binding locals (literal maps, filter results), which must
	// spill before the adjacent multi-use.
	{name: "attrfast-hit", template: "{{ user.name }}\n", varsJSON: `{"user":{"name":"ada"}}`},
	{name: "attrfast-chain", template: "{{ a.b.c }}\n", varsJSON: `{"a":{"b":{"c":7}}}`},
	{name: "attrfast-loop-target", template: "@for u in users {\n{{ u.name }}<{{ u.email }}>;\n@}\n", varsJSON: `{"users":[{"name":"n1","email":"e1"},{"name":"n2","email":"e2"}]}`},
	{name: "attrfast-with-map", template: "@with {m: {k: 5}} {\n{{ m.k }}\n@}\n"},
	{name: "attrfast-literal-recv", template: "{{ {a: 1}.a }}\n"},
	{name: "attrfast-filter-recv", template: "{{ (rows | first).x }}\n", varsJSON: `{"rows":[{"x":3}]}`},
	{name: "attrfast-iterand", template: "@for x in d.items {\n{{ x }};\n@}\n", varsJSON: `{"d":{"items":[1,2]}}`},
	{name: "attrfast-arrow-body", template: "{{ rows | map(r => r.v) | join(\",\") }}\n", varsJSON: `{"rows":[{"v":1},{"v":2}]}`},
	{name: "attrfast-mixed-keys-hit", template: "{{ m.x }}\n", varsJSON: `{"m":{"1":"a","01":"b","x":"c"}}`},

	// The miss arm: the undefined-key error and its available-keys hint come
	// from the unchanged GetAttribute, including Int/Str key spellings.
	{name: "attrfast-mixed-keys-miss", template: "{{ m.zip }}", varsJSON: `{"m":{"1":"a","01":"b","x":"c"}}`},
	{name: "attrfast-empty-map-miss", template: "{{ m.k }}", varsJSON: `{"m":{}}`},
	{name: "attrfast-chain-miss-pos", template: "before\n{{ a.b.c }}", varsJSON: `{"a":{"b":{}}}`},

	// Non-Array receivers: every kind falls through to GetAttribute's exact
	// error (or the host object's member surface).
	{name: "attrfast-null-recv", template: "{{ maybe.name }}", varsJSON: `{"maybe":null}`},
	{name: "attrfast-int-recv", template: "{{ n.member }}", varsJSON: `{"n":5}`},
	{name: "attrfast-bool-recv", template: "{{ b.x }}", varsJSON: `{"b":true}`},
	{name: "attrfast-str-recv", template: "{{ s.x }}", varsJSON: `{"s":"ab"}`},
	{name: "attrfast-safe-recv", template: "{{ (s | raw).up }}", varsJSON: `{"s":"x"}`},
	{name: "attrfast-object-hit", template: "@set c = cell(5)\n{{ c.value }}\n"},
	{name: "attrfast-object-miss", template: "@set c = cell(5)\n{{ c.nope }}"},

	// Read-then-member-assign ordering: the fast path returns the same value
	// GetAttribute would, so COW privatization stays order-exact.
	{name: "attrfast-cow-read-then-write", template: "@set a = {k: [1,2]}\n@set b = a.k\n@set a.k[0] = 99\n{{ b[0] }},{{ a.k[0] }}\n"},
	{name: "attrfast-cow-write-then-read", template: "@set a = {k: [1,2]}\n@set a.k[0] = 99\n@set b = a.k\n{{ b[0] }},{{ a.k[0] }}\n"},
	{name: "attrfast-cow-loop", template: "@set rows = [{v: [1]}, {v: [2]}]\n@for r in rows {\n@set x = r.v\n@set x[0] = 9\n{{ r.v[0] }};\n@}\n{{ rows[0].v[0] }}\n"},

	// Inline assignment in a sibling operand: the elided receiver is embedded
	// only across the adjacent read statements, so the earlier operand's value
	// is captured before the rebind, exactly as the interpreter evaluates.
	{name: "attrfast-spill-sibling", template: "@set x = {v: 1}\n{{ x.v + (x = {v: 5}).v }} {{ x.v }}\n"},
	{name: "attrfast-spill-list", template: "@set x = {v: 1}\n{{ [x.v, (x = {v: 9}) ? 0 : 0, x.v] | join(\",\") }}\n"},
	{name: "attrfast-spill-concat", template: "@set x = {v: \"a\"}\n{{ x.v ~ ((x = {v: \"b\"}) ? \"\" : \"\") ~ x.v }}\n"},
	{name: "attrfast-spill-recv-assign", template: "{{ (x = {v: 7}).v }} {{ x.v }}\n"},

	// The null-safe and suppression arms keep the wrapper verbatim.
	{name: "attrfast-nullsafe", template: "{{ maybe?.name }}|{{ user?.name }}\n", varsJSON: `{"maybe":null,"user":{"name":"x"}}`},
	{name: "attrfast-suppressed", template: "{{ user.zip ?? \"fb\" }}\n", varsJSON: `{"user":{"name":"a"}}`},
}

// TestAttrFastCompiledParity renders the dotted-read battery through the
// compiled path and asserts byte-equality (output or error text) against the
// facade, holding the inline fast path to the interpreter oracle.
func TestAttrFastCompiledParity(t *testing.T) {
	results := map[string]*compile.Result{}
	for _, cs := range attrFastCases {
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cs.name, err)
		}
		results[cs.name] = res
	}
	got := runCompiled(t, attrFastCases, results)
	for _, cs := range attrFastCases {
		r, ok := got[cs.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", cs.name)
			continue
		}
		wantOut, wantErr := renderInterp(t, cs)
		if wantErr != nil {
			if !r.failed {
				t.Errorf("%s: interp errored (%v) but compiled rendered %q", cs.name, wantErr, r.out)
				continue
			}
			if r.errText != wantErr.Error() {
				t.Errorf("%s: error text mismatch\n got  %q\n want %q", cs.name, r.errText, wantErr.Error())
			}
			continue
		}
		if r.failed {
			t.Errorf("%s: compiled errored (%s) but interp rendered %q", cs.name, r.errText, wantOut)
			continue
		}
		if r.out != wantOut {
			t.Errorf("%s: output mismatch\n got  %q\n want %q", cs.name, r.out, wantOut)
		}
	}
}

// TestAttrFastNilArrParity pins the fast path's nil-Arr fall-through. A KArray
// value holding a nil *Array cannot be spelled in template source or fixture
// JSON, so the scratch process feeds runtime.Arr(nil) through host vars to
// both engines and compares output and error text directly.
func TestAttrFastNilArrParity(t *testing.T) {
	cs := compiledCase{name: "attrfast-nilarr", template: "{{ x.name }}"}
	res, err := compileCase(t, cs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	dir := t.TempDir()
	root := repoRoot(t)
	gomod := "module qnilarr\n\ngo 1.23\n\nrequire github.com/avmnu-sng/quill-template-engine v0.0.0\n\nreplace github.com/avmnu-sng/quill-template-engine => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := os.ReadFile(filepath.Join(root, ".go-version")); err == nil {
		if err := os.WriteFile(filepath.Join(dir, ".go-version"), v, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkg := pkgName(cs.name)
	sub := filepath.Join(dir, pkg)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "gen.go"), res.Source, 0o644); err != nil {
		t.Fatal(err)
	}

	mainSrc := fmt.Sprintf(`package main

import (
	"context"
	"fmt"
	"strings"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"

	gen "qnilarr/%s"
)

func main() {
	exts := quill.NewFromMap(map[string]string{}).Extensions()
	var b strings.Builder
	cerr := gen.%s(context.Background(), &b, exts, map[string]runtime.Value{"x": runtime.Arr(nil)}, nil)

	env := quill.NewFromMap(map[string]string{%q: %q})
	want, werr := env.Render(context.Background(), %q, map[string]runtime.Value{"x": runtime.Arr(nil)})

	switch {
	case (cerr != nil) != (werr != nil):
		fmt.Printf("MISMATCH: compiled err=%%v interp err=%%v", cerr, werr)
	case cerr != nil:
		if cerr.Error() != werr.Error() {
			fmt.Printf("MISMATCH: compiled %%q interp %%q", cerr.Error(), werr.Error())
			return
		}
		fmt.Print("OK")
	default:
		if b.String() != want {
			fmt.Printf("MISMATCH: compiled %%q interp %%q", b.String(), want)
			return
		}
		fmt.Print("OK")
	}
}
`, pkg, res.FuncName, cs.name+".ql", cs.template, cs.name+".ql")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run scratch module: %v\nstderr:\n%s", err, stderr.String())
	}
	if stdout.String() != "OK" {
		t.Fatalf("nil-Arr receiver diverges: %s", stdout.String())
	}
}
