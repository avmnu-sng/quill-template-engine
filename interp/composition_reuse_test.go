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

// --- recursive @for with a fused "if" filter: prune a node and its subtree ---

func TestRecursiveForFilterPrunesNodeAndSubtree(t *testing.T) {
	eng := newStub(nil)
	// hidden(visible=false) carries a visible child; pruning hidden must also drop
	// its descendant, since the descent is reached only through a surviving node.
	buried := nodeVis("buried", true, runtime.Arr(runtime.NewArray()))
	hiddenChildren := runtime.NewArray()
	hiddenChildren.SetInt(0, buried)
	hidden := nodeVis("hidden", false, runtime.Arr(hiddenChildren))
	shown := nodeVis("shown", true, runtime.Arr(runtime.NewArray()))
	rootChildren := runtime.NewArray()
	rootChildren.SetInt(0, hidden)
	rootChildren.SetInt(1, shown)
	root := nodeVis("root", true, runtime.Arr(rootChildren))
	top := runtime.NewArray()
	top.SetInt(0, root)

	body := "@for n in tree recursive if n.visible {\n[{{ n.name }}{{ loop(n.children) }}]\n@}"
	got := renderStub(t, eng, body, map[string]runtime.Value{"tree": runtime.Arr(top)})
	// root survives and shows shown; hidden and its buried child are gone.
	want := "[root[shown]\n]\n"
	if got != want {
		t.Fatalf("recursive filter prune:\n got %q\nwant %q", got, want)
	}
}

func TestRecursiveForFilterCountsSurvivorsInLoopMeta(t *testing.T) {
	eng := newStub(nil)
	// Three top-level nodes, the middle one filtered out: loop.length and
	// loop.last reflect only the two survivors.
	a := nodeVis("a", true, runtime.Arr(runtime.NewArray()))
	b := nodeVis("b", false, runtime.Arr(runtime.NewArray()))
	c := nodeVis("c", true, runtime.Arr(runtime.NewArray()))
	top := runtime.NewArray()
	top.SetInt(0, a)
	top.SetInt(1, b)
	top.SetInt(2, c)
	body := "@for n in tree recursive if n.visible {\n{{ n.name }}:{{ loop.index }}/{{ loop.length }}:{{ loop.last }}{{ loop(n.children) }}\n@}"
	got := renderStub(t, eng, body, map[string]runtime.Value{"tree": runtime.Arr(top)})
	want := "a:1/2:false\nc:2/2:true\n"
	if got != want {
		t.Fatalf("recursive filter meta:\n got %q\nwant %q", got, want)
	}
}

func TestRecursiveForFilterEmptyTakesElse(t *testing.T) {
	eng := newStub(nil)
	// Every top-level node is filtered out, so the @else arm runs.
	a := nodeVis("a", false, runtime.Arr(runtime.NewArray()))
	top := runtime.NewArray()
	top.SetInt(0, a)
	body := "@for n in tree recursive if n.visible {\nx\n@}\n@else {\nnone\n@}"
	got := renderStub(t, eng, body, map[string]runtime.Value{"tree": runtime.Arr(top)})
	if got != "none\n" {
		t.Fatalf("recursive filter else: %q", got)
	}
}

// TestRecursiveForBodyIsPureEmitter pins current-state scoping: a recursive @for
// body is a pure emitter, so a body @set of an outer-scope variable does not
// write back after the loop (unlike the plain @for, whose reassignments persist).
func TestRecursiveForBodyIsPureEmitter(t *testing.T) {
	eng := newStub(nil)
	body := "" +
		"@set marker = \"outer\"\n" +
		"@for n in tree recursive {\n" +
		"@set marker = n.name\n" +
		"{{ loop(n.children) }}\n" +
		"@}\n" +
		"after:{{ marker }}"
	got := renderStub(t, eng, body, map[string]runtime.Value{"tree": tree3()})
	if !strings.HasSuffix(got, "after:outer") {
		t.Fatalf("recursive body write-back leaked; want suffix %q, got %q", "after:outer", got)
	}
}

// --- slots feeding across @include / @embed sub-renders ---

func TestIncludeProvidesFeedParentYield(t *testing.T) {
	eng := newStub(map[string]string{
		"part-a.ql": "@provide imports {\nimport a\n@}\nA\n",
		"part-b.ql": "@provide imports {\nimport b\n@}\nB\n",
	})
	body := "imports:\n@yield imports\nbody:\n@include \"part-a.ql\"\n@include \"part-b.ql\"\n"
	got := renderStub(t, eng, body, nil)
	want := "imports:\nimport a\nimport b\nbody:\nA\nB\n"
	if got != want {
		t.Fatalf("include provides feed yield:\n got %q\nwant %q", got, want)
	}
}

func TestSelfContainedIncludeResolvesOwnSlots(t *testing.T) {
	eng := newStub(map[string]string{
		"note.ql": "top:\n@yield note\n@provide note {\ncollected\n@}\nbottom\n",
	})
	got := renderStub(t, eng, "page\n@include \"note.ql\"\nend", nil)
	// No raw placeholder token leaks; the partial's own @yield is backfilled.
	if strings.Contains(got, "QUILL_SLOT") || strings.Contains(got, "\x00") {
		t.Fatalf("slot placeholder leaked into output: %q", got)
	}
	want := "page\ntop:\ncollected\nbottom\nend"
	if got != want {
		t.Fatalf("self-contained include:\n got %q\nwant %q", got, want)
	}
}

func TestEmbedProvidesFeedYield(t *testing.T) {
	eng := newStub(map[string]string{
		"shell.ql": "tags:\n@yield tags\n@provide tags {\nalpha\n@}\n@provide tags {\nbeta\n@}\n",
	})
	got := renderStub(t, eng, "page\n@embed \"shell.ql\" {\n@}\nend", nil)
	if strings.Contains(got, "QUILL_SLOT") || strings.Contains(got, "\x00") {
		t.Fatalf("embed slot placeholder leaked: %q", got)
	}
	want := "page\ntags:\nalpha\nbeta\nend"
	if got != want {
		t.Fatalf("embed provides feed yield:\n got %q\nwant %q", got, want)
	}
}

// node builds a { name, children } mapping value for the tree tests.
func node(name string, children runtime.Value) runtime.Value {
	m := runtime.NewArray()
	m.SetStr("name", runtime.Str(name))
	m.SetStr("children", children)
	return runtime.Arr(m)
}

// nodeVis builds a { name, visible, children } mapping for the recursive-filter
// tests, where the fused "if" condition reads node.visible.
func nodeVis(name string, visible bool, children runtime.Value) runtime.Value {
	m := runtime.NewArray()
	m.SetStr("name", runtime.Str(name))
	m.SetStr("visible", runtime.Bool(visible))
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
