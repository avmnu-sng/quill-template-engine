package interp

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// --- named accumulating content slots: @provide / @yield / slot() ---

func TestProvideYieldAccumulatesInOrder(t *testing.T) {
	eng := newStub(nil)
	body := "" +
		"@provide imports {import a\n@}\n" +
		"@provide imports {import b\n@}\n" +
		"---\n" +
		"@yield imports\n"
	got := renderStub(t, eng, body, nil)
	// The two provides append in execution order; @yield emits the accumulation.
	want := "---\nimport a\nimport b\n"
	if got != want {
		t.Fatalf("provide/yield accumulate:\n got %q\nwant %q", got, want)
	}
}

func TestYieldDeferredBeforeProvides(t *testing.T) {
	eng := newStub(nil)
	// The @yield appears BEFORE the @provide sites in source; deferral backfills
	// the reserved position with the content the later provides accumulate.
	body := "" +
		"imports:\n" +
		"@yield imports\n" +
		"body\n" +
		"@provide imports {import a\n@}\n" +
		"@provide imports {import b\n@}\n"
	got := renderStub(t, eng, body, nil)
	want := "imports:\nimport a\nimport b\nbody\n"
	if got != want {
		t.Fatalf("deferred yield:\n got %q\nwant %q", got, want)
	}
}

func TestProvideAcrossLoopIterations(t *testing.T) {
	eng := newStub(nil)
	names := runtime.NewArray()
	names.SetInt(0, runtime.Str("x"))
	names.SetInt(1, runtime.Str("y"))
	names.SetInt(2, runtime.Str("z"))
	body := "" +
		"@for n in names {\n" +
		"@provide syms {{{ n }},\n@}\n" +
		"@}\n" +
		"@yield syms\n"
	got := renderStub(t, eng, body, map[string]runtime.Value{"names": runtime.Arr(names)})
	if got != "x,\ny,\nz,\n" {
		t.Fatalf("provide across loop: %q", got)
	}
}

func TestYieldUnprovidedSlotIsEmpty(t *testing.T) {
	eng := newStub(nil)
	got := renderStub(t, eng, "before\n@yield never\nafter", nil)
	if got != "before\nafter" {
		t.Fatalf("unprovided yield: %q", got)
	}
}

func TestProvideEmitsNothingAtSite(t *testing.T) {
	eng := newStub(nil)
	// A @provide body contributes to its slot but emits nothing where it sits.
	got := renderStub(t, eng, "[\n@provide s {hidden\n@}\n]", nil)
	if got != "[\n]" {
		t.Fatalf("provide emits nothing at site: %q", got)
	}
}

func TestSlotFunctionForm(t *testing.T) {
	eng := newStub(nil)
	body := "" +
		"@provide items {a\n@}\n" +
		"@provide items {b\n@}\n" +
		"{{ slot(\"items\") | upper }}"
	got := renderStub(t, eng, body, nil)
	if got != "A\nB\n" {
		t.Fatalf("slot() function form: %q", got)
	}
}

// --- call blocks: @call name(args) { body } with caller() ---

func TestCallBlockCallerRoundTrip(t *testing.T) {
	eng := newStub(nil)
	body := "" +
		"@macro wrap(tag) {<{{ tag }}>{{ caller() }}</{{ tag }}>\n@}\n" +
		"@call wrap(\"p\") {hello\n@}"
	got := renderStub(t, eng, body, nil)
	if got != "<p>hello\n</p>\n" {
		t.Fatalf("call/caller: %q", got)
	}
}

func TestCallBlockCallerValueRoundTrip(t *testing.T) {
	eng := newStub(nil)
	// section passes a computed value back into the caller body via caller(n).
	body := "" +
		"@macro section(title) {== {{ title }} ==\n{{ caller(title | length) }}\n@}\n" +
		"@call(len) section(\"Intro\") {title has {{ len }} chars\n@}"
	got := renderStub(t, eng, body, nil)
	want := "== Intro ==\ntitle has 5 chars\n\n"
	if got != want {
		t.Fatalf("call value round-trip:\n got %q\nwant %q", got, want)
	}
}

func TestCallBlockMultipleCallerParams(t *testing.T) {
	eng := newStub(nil)
	// Two caller parameters round-trip positionally.
	body := "" +
		"@macro row() {{{ caller(1, 2) }}\n@}\n" +
		"@call(a, b) row() {{{ a }}+{{ b }}\n@}"
	got := renderStub(t, eng, body, nil)
	if got != "1+2\n\n" {
		t.Fatalf("multiple caller params: %q", got)
	}
}

func TestCallBlockCallerOutsideCallErrors(t *testing.T) {
	eng := newStub(nil)
	body := "" +
		"@macro m() {{{ caller() }}\n@}\n" +
		"{{ m() }}"
	_, err := renderStubErr(t, eng, body, nil)
	if err == nil || !strings.Contains(err.Error(), "caller() is only valid") {
		t.Fatalf("caller() outside @call should error, got %v", err)
	}
}

func TestCallBlockCallerNotVisibleToTransitiveMacro(t *testing.T) {
	eng := newStub(nil)
	// outer is invoked by @call so caller() works there; inner is a plain call
	// from outer's body and must NOT see the caller binding.
	body := "" +
		"@macro inner() {[{{ caller() }}]\n@}\n" +
		"@macro outer() {{{ caller() }}/{{ inner() }}\n@}\n" +
		"@call outer() {B\n@}"
	_, err := renderStubErr(t, eng, body, nil)
	if err == nil || !strings.Contains(err.Error(), "caller() is only valid") {
		t.Fatalf("transitive caller() should error, got %v", err)
	}
}

func TestCallBlockErrorDoesNotLeakCaller(t *testing.T) {
	eng := newStub(nil)
	// The first @call passes an unknown named argument, so the macro invocation
	// errors before its body renders. A following plain macro call must still see
	// caller() as undefined (the staged frame is cleared on the error path).
	body := "" +
		"@macro bad(x) {ok\n@}\n" +
		"@macro plain() {{{ caller() }}\n@}\n" +
		"@call bad(nope: 1) {blk\n@}\n"
	_, err := renderStubErr(t, eng, body, nil)
	if err == nil {
		t.Fatal("expected an error for the unknown named argument")
	}
	// A separate render exercises the plain macro alone to confirm caller() is
	// undefined there (no leaked frame from a prior faulty @call).
	_, err2 := renderStubErr(t, eng, "@macro plain() {{{ caller() }}\n@}\n{{ plain() }}", nil)
	if err2 == nil || !strings.Contains(err2.Error(), "caller() is only valid") {
		t.Fatalf("caller() must be undefined in a plain macro, got %v", err2)
	}
}

// --- recursive @for: loop(children) descent with loop.depth ---

func TestRecursiveForDescends(t *testing.T) {
	eng := newStub(nil)
	tree := tree3()
	body := "@for node in tree recursive {\n{{ node.name }}({{ loop.depth }}){{ loop(node.children) }}\n@}"
	got := renderStub(t, eng, body, map[string]runtime.Value{"tree": tree})
	// root(1) has children a(2) [with leaf a1(3)] and b(2). Each rendered level
	// keeps the newline before its close, so a level's output ends in "\n".
	want := "root(1)a(2)a1(3)\n\nb(2)\n\n"
	if got != want {
		t.Fatalf("recursive for:\n got %q\nwant %q", got, want)
	}
}

func TestRecursiveForDepthAndDepth0(t *testing.T) {
	eng := newStub(nil)
	tree := tree3()
	body := "@for node in tree recursive {\n{{ node.name }}:{{ loop.depth }}/{{ loop.depth0 }};{{ loop(node.children) }}\n@}"
	got := renderStub(t, eng, body, map[string]runtime.Value{"tree": tree})
	want := "root:1/0;a:2/1;a1:3/2;\n\nb:2/1;\n\n"
	if got != want {
		t.Fatalf("recursive depth/depth0:\n got %q\nwant %q", got, want)
	}
}

func TestRecursiveForLeafEmptyChildren(t *testing.T) {
	eng := newStub(nil)
	leaf := node("only", runtime.Arr(runtime.NewArray()))
	top := runtime.NewArray()
	top.SetInt(0, leaf)
	body := "@for n in tree recursive {\n[{{ n.name }}{{ loop(n.children) }}]\n@}"
	got := renderStub(t, eng, body, map[string]runtime.Value{"tree": runtime.Arr(top)})
	if got != "[only]\n" {
		t.Fatalf("recursive leaf: %q", got)
	}
}

func TestRecursiveForLoopMetaFields(t *testing.T) {
	eng := newStub(nil)
	// A single top-level node exercises first/last/length at depth 0.
	one := node("solo", runtime.Arr(runtime.NewArray()))
	top := runtime.NewArray()
	top.SetInt(0, one)
	body := "@for n in tree recursive {\n{{ n.name }} f={{ loop.first }} l={{ loop.last }} len={{ loop.length }}{{ loop(n.children) }}\n@}"
	got := renderStub(t, eng, body, map[string]runtime.Value{"tree": runtime.Arr(top)})
	if got != "solo f=true l=true len=1\n" {
		t.Fatalf("recursive loop meta: %q", got)
	}
}

func TestRecursiveForEmptyTree(t *testing.T) {
	eng := newStub(nil)
	body := "@for n in tree recursive {\nx\n@}\n@else {\nnone\n@}"
	got := renderStub(t, eng, body, map[string]runtime.Value{"tree": runtime.Arr(runtime.NewArray())})
	if got != "none\n" {
		t.Fatalf("recursive empty tree: %q", got)
	}
}

func TestLoopCallOutsideRecursiveErrors(t *testing.T) {
	eng := newStub(nil)
	// A plain (non-recursive) loop leaves loop() undefined, so loop(x) is an
	// unknown function, not a descent callable.
	items := runtime.NewArray()
	items.SetInt(0, runtime.Str("a"))
	body := "@for x in items {\n{{ loop(x) }}\n@}"
	_, err := renderStubErr(t, eng, body, map[string]runtime.Value{"items": runtime.Arr(items)})
	if err == nil {
		t.Fatal("loop() outside a recursive loop should be an error")
	}
}

// node builds a { name, children } mapping value for the tree tests.
func node(name string, children runtime.Value) runtime.Value {
	m := runtime.NewArray()
	m.SetStr("name", runtime.Str(name))
	m.SetStr("children", children)
	return runtime.Arr(m)
}

// tree3 builds root -> [a -> [a1], b], a three-level tree.
func tree3() runtime.Value {
	a1 := node("a1", runtime.Arr(runtime.NewArray()))
	achildren := runtime.NewArray()
	achildren.SetInt(0, a1)
	a := node("a", runtime.Arr(achildren))
	b := node("b", runtime.Arr(runtime.NewArray()))
	rootChildren := runtime.NewArray()
	rootChildren.SetInt(0, a)
	rootChildren.SetInt(1, b)
	root := node("root", runtime.Arr(rootChildren))
	top := runtime.NewArray()
	top.SetInt(0, root)
	return runtime.Arr(top)
}
