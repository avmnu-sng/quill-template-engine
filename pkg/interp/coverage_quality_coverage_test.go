package interp

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
	"github.com/avmnu-sng/quill-template-engine/pkg/sandbox"
)

// --- RenderSandboxed (callable.go): the forced-sandbox render entry ------------
//
// RenderSandboxed is Render with the per-render sandbox forced on regardless of
// the engine's global setting, backing the function-form include's
// `sandboxed: true` flag. It is not reachable through the plain Render helper,
// so these tests call it directly (white-box) and assert both an allowed render
// and each denial class (tag / filter / function) it must surface as a
// KindSecurity error mapped through the policy.

// prepSandbox parses body under name and returns the prepared template.
func prepSandbox(t *testing.T, name, body string) *Template {
	t.Helper()
	mod, err := parse.ParseString(name, body)
	if err != nil {
		t.Fatalf("parse %q: %v", name, err)
	}
	return Prepare(name, mod)
}

// TestRenderSandboxedAllows covers the success path: an engine whose GLOBAL
// sandbox is off still renders under RenderSandboxed with the policy enforced,
// and an allowed template produces its ordinary output. The engine's global
// gate being off proves the per-render force -- not the engine setting -- is
// what activates the sandbox.
func TestRenderSandboxedAllows(t *testing.T) {
	eng := newStub(nil)
	eng.policy = sandbox.NewPolicy(
		sandbox.AllowTags("for"),
		sandbox.AllowFilters("upper"),
		sandbox.AllowFunctions("range"),
	)
	// Global sandbox stays OFF; RenderSandboxed forces it on for this render.
	if eng.sandboxOn {
		t.Fatal("precondition: engine global sandbox must be off")
	}
	tmpl := prepSandbox(t, "t", "@for x in 1..2 {\n{{ x | upper }}\n@}\n")
	got, err := RenderSandboxed(context.Background(), eng, tmpl, nil)
	if err != nil {
		t.Fatalf("allowed sandboxed render errored: %v", err)
	}
	if got != "1\n2\n" {
		t.Fatalf("allowed sandboxed output = %q, want %q", got, "1\n2\n")
	}
}

// TestRenderSandboxedDenies covers the failure path for each Phase-1 class:
// a disallowed statement keyword, filter, and function each yield a
// KindSecurity error naming the offending element, even though the engine's
// global sandbox is off. The forced per-render gate is what raises them.
func TestRenderSandboxedDenies(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		class errors.SecurityClass
		elem  string
	}{
		// `if` is not in the tag allowlist.
		{"tag", "@if true {\nX\n@}\n", errors.SecTag, "if"},
		// `lower` is not in the filter allowlist.
		{"filter", "{{ s | lower }}", errors.SecFilter, "lower"},
		// `min` is not in the function allowlist.
		{"function", "{{ min(1, 2) }}", errors.SecFunction, "min"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newStub(nil)
			// Policy allows only `for`/`upper`/`range`; each case uses something else.
			eng.policy = sandbox.NewPolicy(
				sandbox.AllowTags("for"),
				sandbox.AllowFilters("upper"),
				sandbox.AllowFunctions("range"),
			)
			tmpl := prepSandbox(t, "t", tc.body)
			vars := map[string]runtime.Value{"s": runtime.Str("Hi")}
			_, err := RenderSandboxed(context.Background(), eng, tmpl, vars)
			if err == nil {
				t.Fatalf("disallowed %s must be denied, got nil", tc.name)
			}
			var sec *errors.Security
			if !stderrors.As(err, &sec) {
				t.Fatalf("error is not *errors.Security: %v", err)
			}
			if sec.Class != tc.class {
				t.Errorf("class = %v, want %v (err: %v)", sec.Class, tc.class, err)
			}
			if sec.Name != tc.elem {
				t.Errorf("name = %q, want %q", sec.Name, tc.elem)
			}
			if errors.KindOf(err) != errors.KindSecurity {
				t.Errorf("KindOf = %v, want security", errors.KindOf(err))
			}
		})
	}
}

// TestRenderSandboxedNilPolicyDeniesAll covers B6 through RenderSandboxed: a nil
// policy under a forced sandbox denies everything (no grandfathering), so even
// the plainest tag is rejected rather than silently allowed.
func TestRenderSandboxedNilPolicyDeniesAll(t *testing.T) {
	eng := newStub(nil) // eng.policy is nil.
	tmpl := prepSandbox(t, "t", "@for x in xs {\n{{ x }}\n@}\n")
	_, err := RenderSandboxed(context.Background(), eng, tmpl,
		map[string]runtime.Value{"xs": runtime.Arr(runtime.NewList(runtime.Int(1)))})
	if err == nil {
		t.Fatal("nil policy under a forced sandbox must deny everything")
	}
	if errors.KindOf(err) != errors.KindSecurity {
		t.Fatalf("KindOf = %v, want security (err: %v)", errors.KindOf(err), err)
	}
	var sec *errors.Security
	if !stderrors.As(err, &sec) {
		t.Fatalf("error is not *errors.Security: %v", err)
	}
	// The first used callable checked is the `for` tag.
	if sec.Class != errors.SecTag || sec.Name != "for" {
		t.Errorf("got class=%v name=%q, want SecTag/for", sec.Class, sec.Name)
	}
}

// --- bindTargetNull (exec.go): the absent path of a short optional slot --------
//
// bindTargetNull null-binds every name a target introduces when an optional
// slot's source element is missing. The plain-name path is exercised by the
// existing @set tests; these drive the nested branches that were uncovered:
// an optional slot whose target is itself a list/map pattern, including an
// interior spread (empty capture), an elided nested slot, an optional-inside-
// optional, and both the shorthand and renamed map-target forms.

// TestBindTargetNullNestedListWithSpread drives a short source into an optional
// slot whose target is a nested [b, ...rest] list pattern: b binds null and the
// interior spread captures an EMPTY sequence (spec 01 Section 2.1). Present and
// absent supplies are contrasted so the null-bind is attributable to absence.
func TestBindTargetNullNestedListWithSpread(t *testing.T) {
	eng := newStub(nil)
	src := "@set [a, [b, ...rest]?] = xs\n{{ a }}|{{ b }}|{{ rest|json }}"

	// Present: the nested pattern binds from its element.
	got := renderStub(t, eng, src, map[string]runtime.Value{
		"xs": list(runtime.Int(1), list(runtime.Int(7), runtime.Int(8), runtime.Int(9))),
	})
	if got != "1|7|[8,9]" {
		t.Fatalf("present nested spread: got %q want %q", got, "1|7|[8,9]")
	}

	// Absent: the optional nested pattern goes through bindTargetNull -- b is
	// null and the spread tail is an empty sequence, not undefined.
	got = renderStub(t, eng, src, map[string]runtime.Value{
		"xs": list(runtime.Int(1)),
	})
	if got != "1||[]" {
		t.Fatalf("absent nested spread: got %q want %q", got, "1||[]")
	}
}

// TestBindTargetNullNestedListElided drives a short source into an optional slot
// whose target has an interior elided slot ([x, , y]): the absent path skips the
// elided position (binding nothing) and null-binds the named slots.
func TestBindTargetNullNestedListElided(t *testing.T) {
	eng := newStub(nil)
	src := "@set [a, [x, , y]?] = xs\n{{ a }}|{{ x }}|{{ y }}"

	// Absent: x and y are null; the elided slot introduces no name.
	got := renderStub(t, eng, src, map[string]runtime.Value{
		"xs": list(runtime.Int(1)),
	})
	if got != "1||" {
		t.Fatalf("absent elided nested: got %q want %q", got, "1||")
	}

	// Present (three-element inner): the elided middle is skipped, x and y bind.
	got = renderStub(t, eng, src, map[string]runtime.Value{
		"xs": list(runtime.Int(1), list(runtime.Int(4), runtime.Int(5), runtime.Int(6))),
	})
	if got != "1|4|6" {
		t.Fatalf("present elided nested: got %q want %q", got, "1|4|6")
	}
}

// TestBindTargetNullNestedOptionalInOptional drives a short source into an
// optional slot whose target nests ANOTHER optional slot ([[b]?]?): the outer
// absence recurses through bindTargetNull, unwraps the inner KindOptional, and
// null-binds its name.
func TestBindTargetNullNestedOptionalInOptional(t *testing.T) {
	eng := newStub(nil)
	src := "@set [a, [[b]?]?] = xs\n{{ a }}|{{ b }}"

	// Absent outer optional -> inner optional's name b is null.
	got := renderStub(t, eng, src, map[string]runtime.Value{
		"xs": list(runtime.Int(1)),
	})
	if got != "1|" {
		t.Fatalf("absent optional-in-optional: got %q want %q", got, "1|")
	}

	// Present outer, present inner -> b binds the inner list's first element,
	// so the null in the absent case is attributable to absence, not to the
	// pattern shape swallowing the value.
	got = renderStub(t, eng, src, map[string]runtime.Value{
		"xs": list(runtime.Int(1), list(list(runtime.Int(42)))),
	})
	if got != "1|42" {
		t.Fatalf("present optional-in-optional: got %q want %q", got, "1|42")
	}
}

// TestBindTargetNullMapPattern drives a short source into an optional slot whose
// target is a MAP pattern, exercising the KindMapPattern arm of bindTargetNull:
// both the shorthand {name} form and the renamed {role: alias} form null-bind
// their locals (the alias, when present).
func TestBindTargetNullMapPattern(t *testing.T) {
	eng := newStub(nil)
	src := "@set [a, {name, role: alias}?] = xs\n{{ a }}|{{ name }}|{{ alias }}"

	// Absent: name and alias (the renamed local) are both null.
	got := renderStub(t, eng, src, map[string]runtime.Value{
		"xs": list(runtime.Int(1)),
	})
	if got != "1||" {
		t.Fatalf("absent map pattern: got %q want %q", got, "1||")
	}

	// Present: the map pattern binds from the element's members; the alias local
	// carries the `role` member, proving the renamed local is the bound one.
	entry := runtime.NewArray()
	entry.SetStr("name", runtime.Str("ada"))
	entry.SetStr("role", runtime.Str("admin"))
	got = renderStub(t, eng, src, map[string]runtime.Value{
		"xs": list(runtime.Int(1), runtime.Arr(entry)),
	})
	if got != "1|ada|admin" {
		t.Fatalf("present map pattern: got %q want %q", got, "1|ada|admin")
	}
}

// --- absorbForSafe (interp.go): copy-on-write union of pool-safe @for sets -----
//
// absorbForSafe folds each template's pool-safe @for set into the render's
// lookup. The first contributor aliases its map (no allocation); a SECOND
// contributor forces a fresh owned merged map; a THIRD merges into that owned
// map in place. A three-level @extends chain, every level carrying a pool-safe
// loop, drives all three arms in one render (child aliases, parent copies,
// grandparent merges), and the render still produces the exact output -- proving
// the union does not disturb execution.

// TestAbsorbForSafeChainThreeContributors renders a three-level @extends chain
// where the root (child), its parent, and the grandparent each define a
// pool-safe @for. buildChain absorbs each in turn, exercising the alias / copy /
// in-place-merge arms of absorbForSafe. The rendered output is asserted exactly.
func TestAbsorbForSafeChainThreeContributors(t *testing.T) {
	eng := newStub(map[string]string{
		// Grandparent: a free-body pool-safe loop, plus the base `body` block.
		"grand.ql": "G:\n@for g in gs {\n[{{ g }}]\n@}\n@block body {\nbase\n@}\n",
		// Parent: extends grand, overrides `body` with its own pool-safe loop.
		"parent.ql": "@extends \"grand.ql\"\n@block body {\nP:\n@for p in ps {\n({{ p }})\n@}\n@}\n",
	})
	xs := runtime.Arr(runtime.NewList(runtime.Int(1), runtime.Int(2)))
	// Child: extends parent, overrides `body` again with its own pool-safe loop.
	got, err := renderNamed(t, eng, "child.ql",
		"@extends \"parent.ql\"\n@block body {\nC:\n@for c in cs {\n<{{ c }}>\n@}\n@}\n",
		map[string]runtime.Value{"gs": xs, "ps": xs, "cs": xs})
	if err != nil {
		t.Fatalf("three-level chain render error: %v", err)
	}
	// The grandparent's free body runs, then the topmost `body` override (child's).
	want := "G:\n[1]\n[2]\nC:\n<1>\n<2>\n"
	if got != want {
		t.Fatalf("chain output = %q, want %q", got, want)
	}
}

// TestAbsorbForSafeCopyOnWrite pins the copy-on-write contract directly on
// absorbForSafe, white-box, because the end-to-end render cannot observe it: the
// stub re-Prepares a fresh forSafe map per render, so a mutated alias never
// leaks across renders and an output-only assertion would pass even if the copy
// arm mutated the aliased map in place. Here three prepared templates each carry
// one distinct pool-safe @for node; absorbing them in turn must (1) ALIAS the
// first template's own map by identity, then (2) on the second replace it with a
// FRESH owned map holding both nodes while leaving the first template's original
// map untouched, then (3) merge the third IN PLACE into that same owned map. The
// untouched-first-map assertion is the one that fails if the copy arm is
// replaced by an in-place mutation of the alias.
func TestAbsorbForSafeCopyOnWrite(t *testing.T) {
	t1 := prepSandbox(t, "t1", "@for a in as {\n[{{ a }}]\n@}\n")
	t2 := prepSandbox(t, "t2", "@for b in bs {\n({{ b }})\n@}\n")
	t3 := prepSandbox(t, "t3", "@for c in cs {\n<{{ c }}>\n@}\n")

	// Preconditions: each template contributes exactly one pool-safe @for node,
	// and the three node keys are distinct (so a mutation would be observable).
	for name, tm := range map[string]*Template{"t1": t1, "t2": t2, "t3": t3} {
		if len(tm.forSafe) != 1 {
			t.Fatalf("precondition: %s forSafe len = %d, want 1", name, len(tm.forSafe))
		}
	}
	var n1 *ast.Node
	for n := range t1.forSafe {
		n1 = n
	}

	in := &interp{}

	// Arm 1: first contributor is aliased read-only -- same map by identity, and
	// the render does not yet claim ownership.
	in.absorbForSafe(t1)
	if !sameForSafe(in.forSafe, t1.forSafe) {
		t.Fatal("first absorb must alias t1's own map by identity")
	}
	if in.forSafeOwned {
		t.Fatal("first absorb must not claim ownership (no allocation)")
	}

	// Arm 2: second contributor forces a fresh owned map. The union must hold
	// both nodes, ownership flips true, and -- the load-bearing check -- t1's
	// ORIGINAL map must be unchanged (still length 1, still just its own node).
	in.absorbForSafe(t2)
	if !in.forSafeOwned {
		t.Fatal("second absorb must claim ownership of a fresh merged map")
	}
	if sameForSafe(in.forSafe, t1.forSafe) || sameForSafe(in.forSafe, t2.forSafe) {
		t.Fatal("second absorb must allocate a NEW map, not alias either input")
	}
	if len(in.forSafe) != 2 {
		t.Fatalf("after two contributors, merged len = %d, want 2", len(in.forSafe))
	}
	if len(t1.forSafe) != 1 {
		t.Fatalf("copy-on-write violated: t1's map was mutated, len = %d, want 1",
			len(t1.forSafe))
	}
	if _, leaked := t1.forSafe[n1]; !leaked || len(t1.forSafe) != 1 {
		t.Fatal("copy-on-write violated: t1's map no longer holds exactly its own node")
	}

	// Arm 3: third contributor merges in place -- the owned map's identity is
	// preserved and it now holds all three nodes.
	owned := in.forSafe
	in.absorbForSafe(t3)
	if !sameForSafe(in.forSafe, owned) {
		t.Fatal("third absorb must merge IN PLACE, keeping the owned map's identity")
	}
	if len(in.forSafe) != 3 {
		t.Fatalf("after three contributors, merged len = %d, want 3", len(in.forSafe))
	}
}

// sameForSafe reports whether two forSafe maps are the same underlying map by
// identity (not just equal contents), via the map header pointer. A nil pair is
// treated as distinct so the alias checks stay meaningful.
func sameForSafe(a, b map[*ast.Node]bool) bool {
	if a == nil || b == nil {
		return false
	}
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// TestAbsorbForSafeChainNoErrorAcrossRenders is a lighter end-to-end guard: the
// same two-level chain rendered twice must produce identical exact output. It
// cannot see the copy-on-write mutation (the stub re-Prepares per render), so it
// exists only to prove the union leaves ordinary rendering byte-stable; the
// white-box test above carries the mutation contract.
func TestAbsorbForSafeChainNoErrorAcrossRenders(t *testing.T) {
	eng := newStub(map[string]string{
		"base.ql": "B:\n@for b in bs {\n[{{ b }}]\n@}\n@block body {\nx\n@}\n",
	})
	xs := runtime.Arr(runtime.NewList(runtime.Int(9)))
	child := "@extends \"base.ql\"\n@block body {\n@for c in cs {\n<{{ c }}>\n@}\n@}\n"
	want := "B:\n[9]\n<9>\n"

	for i := 0; i < 2; i++ {
		got, err := renderNamed(t, eng, "child.ql", child,
			map[string]runtime.Value{"bs": xs, "cs": xs})
		if err != nil {
			t.Fatalf("render %d error: %v", i, err)
		}
		if got != want {
			t.Fatalf("render %d output = %q, want %q", i, got, want)
		}
	}
}

// --- IsChild (template.go): the "definitely a child" tri-state -----------------

// TestIsChild covers both answers: a template with an @extends is a child, one
// without is not. The child's parent must be present for Prepare, so a stub
// engine serves it (the flag is set at Prepare time from the @extends node, so
// preparing the source directly suffices).
func TestIsChild(t *testing.T) {
	child := prepSandbox(t, "child.ql", "@extends \"base.ql\"\n@block body {\nx\n@}\n")
	if !child.IsChild() {
		t.Error("a template with @extends must report IsChild() == true")
	}

	plain := prepSandbox(t, "plain.ql", "just body\n@block body {\nx\n@}\n")
	if plain.IsChild() {
		t.Error("a template without @extends must report IsChild() == false")
	}

	// A template whose only extends is a candidate list is still a child.
	cand := prepSandbox(t, "cand.ql", "@extends [\"a.ql\", \"b.ql\"]\n")
	if !cand.IsChild() {
		t.Error("a candidate-list @extends must still report IsChild() == true")
	}
}
