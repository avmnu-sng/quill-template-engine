package interp

import (
	"strings"
	"sync"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/core/parse"
	"github.com/avmnu-sng/quill-template-engine/cover"
	"github.com/avmnu-sng/quill-template-engine/runtime"
	"github.com/avmnu-sng/quill-template-engine/sandbox"
)

// cachingStub wraps stubEngine with a stable-pointer LoadTemplate, mirroring
// the facade's prepared-template memo: repeated loads of one name return the
// SAME *Template. That stability is the precondition under which the static
// composition memo pays off, and it is what lets these tests observe one
// memoized Template instance across renders, includes, and embeds.
type cachingStub struct {
	*stubEngine
	mu     sync.Mutex
	loaded map[string]*Template
}

func newCachingStub(tmpls map[string]string) *cachingStub {
	return &cachingStub{stubEngine: newStub(tmpls), loaded: map[string]*Template{}}
}

// LoadTemplate returns the pinned Template for name, preparing it on first use.
func (c *cachingStub) LoadTemplate(name string) (*Template, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.loaded[name]; ok {
		return t, nil
	}
	t, err := c.stubEngine.LoadTemplate(name)
	if err != nil {
		return nil, err
	}
	c.loaded[name] = t
	return t, nil
}

// loadPinned loads name through the caching stub, failing the test on error.
func loadPinned(t *testing.T, c *cachingStub, name string) *Template {
	t.Helper()
	tmpl, err := c.LoadTemplate(name)
	if err != nil {
		t.Fatalf("load %q: %v", name, err)
	}
	return tmpl
}

// renderPinned renders an already-loaded Template, failing the test on error.
func renderPinned(t *testing.T, eng Engine, tmpl *Template, vars map[string]runtime.Value) string {
	t.Helper()
	out, err := Render(eng, tmpl, vars)
	if err != nil {
		t.Fatalf("render %q: %v", tmpl.Name, err)
	}
	return out
}

// staticChainFixtures is a three-level @extends chain with two parent() hops,
// an aliased trait, and a block() call reading the aliased entry: every merged
// block-table shape the memo captures (override chains, trait layering under
// an alias, name-based dispatch).
func staticChainFixtures() map[string]string {
	return map[string]string{
		"grand.ql": "G:\n@block body {\ngrand-body\n@}\n[{{ block(\"aliased\") }}]\n",
		"parent.ql": "@extends \"grand.ql\"\n" +
			"@block body {\nP<{{ parent() }}>\n@}\n",
		"trait.ql": "@block extra {\ntrait-extra\n@}\n",
		"child.ql": "@extends \"parent.ql\"\n" +
			"@use \"trait.ql\" with { extra: aliased }\n" +
			"@block body {\nC[{{ parent() }}]\n@}\n",
	}
}

// TestStaticCompositionMemoRepeatRenderStable is the differential heart of the
// composition memo: the first render of a pinned Template runs the fresh build
// (the pre-memo path) and every later render serves the memoized tables, so
// 1,000 repeat renders byte-equal to the first prove the memo replays the
// exact composition the per-render build produced -- across @extends depth,
// parent() hops, trait aliasing, and block() dispatch.
func TestStaticCompositionMemoRepeatRenderStable(t *testing.T) {
	eng := newCachingStub(staticChainFixtures())
	tmpl := loadPinned(t, eng, "child.ql")
	if tmpl.comp.Load() != nil {
		t.Fatal("composition memo populated before any render")
	}
	first := renderPinned(t, eng, tmpl, nil)
	c1 := tmpl.comp.Load()
	if c1 == nil {
		t.Fatal("static chain did not memoize its composition")
	}
	if len(c1.chain) != 3 {
		t.Fatalf("memoized chain length = %d, want 3", len(c1.chain))
	}
	for i := 0; i < 1000; i++ {
		if got := renderPinned(t, eng, tmpl, nil); got != first {
			t.Fatalf("render %d diverged from the first:\n got %q\nwant %q", i, got, first)
		}
	}
	if tmpl.comp.Load() != c1 {
		t.Error("composition memo was rebuilt by a warm render")
	}
	// A second Prepare of the same source renders cold (its own fresh build)
	// and must produce the same bytes the warm memo serves.
	fresh := newCachingStub(staticChainFixtures())
	if got := renderPinned(t, fresh, loadPinned(t, fresh, "child.ql"), nil); got != first {
		t.Errorf("cold rebuild diverged from warm memo:\n got %q\nwant %q", got, first)
	}
}

// importFixtures exercises the macro namespace the memo captures: @import
// namespaces, @from selective binds shadowing the root's own macro, and a
// _context walk pinning the per-render scope-bind replay order.
func importFixtures() map[string]string {
	return map[string]string{
		"lib.ql":  "@macro greet(name) {Hello {{ name }}!\n@}\n@macro shout(x) {{{ x | upper }}\n@}\n",
		"lib2.ql": "@macro greet(name) {Hi {{ name }}.\n@}\n",
		"root.ql": "@import \"lib.ql\" as ns\n" +
			"@import \"lib2.ql\" as ns2\n" +
			"@from \"lib.ql\" import greet\n" +
			"@from \"lib2.ql\" import greet as hi\n" +
			"@macro greet(name) {Own {{ name }}?\n@}\n" +
			"{{ ns.greet(\"a\") }}|{{ ns2.greet(\"b\") }}|{{ greet(\"c\") }}|{{ hi(\"d\") }}\n" +
			"@for k, v in _context {\nk={{ k }}\n@}\n",
	}
}

// TestStaticCompositionMemoImportNamespaces proves the memoized macro table
// resolves @import dotted calls, @from shadowing (an imported greet wins over
// the root's own declaration), and that the @import namespace scope binds are
// replayed into every render's context in source order (the _context walk
// prints the scope names in insertion order, so a replay reorder is a byte
// diff).
func TestStaticCompositionMemoImportNamespaces(t *testing.T) {
	eng := newCachingStub(importFixtures())
	tmpl := loadPinned(t, eng, "root.ql")
	vars := func() map[string]runtime.Value {
		return map[string]runtime.Value{"who": runtime.Str("w")}
	}
	first := renderPinned(t, eng, tmpl, vars())
	for _, want := range []string{
		"Hello c!",          // @from "lib.ql" shadows the root's own greet
		"Hi d.",             // @from alias binds lib2's greet as hi
		"Hello a!", "Hi b.", // both namespace objects dispatch dotted calls
		"k=who\nk=ns\nk=ns2\n", // scope binds in source order, after the vars
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("first render missing %q:\n%q", want, first)
		}
	}
	c1 := tmpl.comp.Load()
	if c1 == nil {
		t.Fatal("static import chain did not memoize")
	}
	if len(c1.nsBinds) != 2 || c1.nsBinds[0].name != "ns" || c1.nsBinds[1].name != "ns2" {
		t.Fatalf("memoized nsBinds = %+v, want [ns ns2]", c1.nsBinds)
	}
	if e := c1.macros["greet"]; e == nil || e.home.Name != "lib.ql" {
		t.Fatalf("memoized greet home = %v, want lib.ql (the @from shadow)", e)
	}
	if e := c1.macros["hi"]; e == nil || e.home.Name != "lib2.ql" {
		t.Fatalf("memoized hi home = %v, want lib2.ql", e)
	}
	for i := 0; i < 1000; i++ {
		if got := renderPinned(t, eng, tmpl, vars()); got != first {
			t.Fatalf("warm import render %d diverged:\n got %q\nwant %q", i, got, first)
		}
	}
}

// TestDynamicCompositionStaysUncached pins the memo gate's dynamic fallbacks:
// a variable @extends operand, a candidate-list @extends, and a _self @import
// all keep the per-render build, so the render-time expression keeps deciding
// what the chain resolves to and the Template never publishes a memo.
func TestDynamicCompositionStaysUncached(t *testing.T) {
	eng := newCachingStub(map[string]string{
		"a.ql":    "A:\n@block body {\na-body\n@}\n",
		"b.ql":    "B:\n@block body {\nb-body\n@}\n",
		"dyn.ql":  "@extends which\n@block body {\nover\n@}\n",
		"cand.ql": "@extends [\"missing.ql\", \"a.ql\"]\n@block body {\ncand\n@}\n",
		"self.ql": "@import _self as me\n@macro m() {M\n@}\n{{ me.m() }}\n",
	})

	dyn := loadPinned(t, eng, "dyn.ql")
	gotA := renderPinned(t, eng, dyn, map[string]runtime.Value{"which": runtime.Str("a.ql")})
	if !strings.HasPrefix(gotA, "A:") {
		t.Fatalf("dynamic extends to a.ql rendered %q", gotA)
	}
	if dyn.comp.Load() != nil {
		t.Fatal("dynamic @extends chain was memoized")
	}
	// The parent flips per render: a memoized chain would keep serving a.ql.
	gotB := renderPinned(t, eng, dyn, map[string]runtime.Value{"which": runtime.Str("b.ql")})
	if !strings.HasPrefix(gotB, "B:") {
		t.Fatalf("dynamic extends did not follow the flipped parent: %q", gotB)
	}
	if dyn.comp.Load() != nil {
		t.Fatal("dynamic @extends chain was memoized on the second render")
	}

	cand := loadPinned(t, eng, "cand.ql")
	if got := renderPinned(t, eng, cand, nil); !strings.HasPrefix(got, "A:") {
		t.Fatalf("candidate-list extends rendered %q", got)
	}
	if cand.comp.Load() != nil {
		t.Fatal("candidate-list @extends chain was memoized")
	}

	self := loadPinned(t, eng, "self.ql")
	if got := renderPinned(t, eng, self, nil); !strings.Contains(got, "M") {
		t.Fatalf("_self import rendered %q", got)
	}
	if self.comp.Load() != nil {
		t.Fatal("_self import was memoized")
	}
}

// TestDynamicParentStaticChildMemoGate covers the chain-member half of the
// gate: a child whose OWN heads are static but whose parent declares a dynamic
// @extends must not memoize, because the grandparent is re-resolved per render.
func TestDynamicParentStaticChildMemoGate(t *testing.T) {
	eng := newCachingStub(map[string]string{
		"top.ql": "T:\n@block body {\ntop\n@}\n",
		"mid.ql": "@extends \"top.ql\"" + "\n@block body {\nmid<{{ parent() }}>\n@}\n",
		// dynmid extends through an expression, so any chain containing it is
		// dynamic even though the templates above and below it are static.
		"dynmid.ql": "@extends target\n@block body {\ndynmid\n@}\n",
		"leafA.ql":  "@extends \"mid.ql\"\n",
		"leafB.ql":  "@extends \"dynmid.ql\"\n",
	})
	leafA := loadPinned(t, eng, "leafA.ql")
	renderPinned(t, eng, leafA, nil)
	if leafA.comp.Load() == nil {
		t.Error("fully static leaf chain did not memoize")
	}
	leafB := loadPinned(t, eng, "leafB.ql")
	renderPinned(t, eng, leafB, map[string]runtime.Value{"target": runtime.Str("top.ql")})
	if leafB.comp.Load() != nil {
		t.Error("chain with a dynamic middle member was memoized")
	}
}

// TestEmbedKeepsCachedTableIntact is the load-bearing embed invariant from the
// mutation audit: @embed layers its overrides by MUTATING the block table it
// builds (chain prepend, node overwrite), so it must always build fresh in its
// sub-interp and never touch the embedded template's memo. The test memoizes
// inner.ql, snapshots its cached table bit-for-bit, embeds it with an
// override, then proves the memo pointer, the table contents, and a plain
// re-render are all unchanged.
func TestEmbedKeepsCachedTableIntact(t *testing.T) {
	eng := newCachingStub(map[string]string{
		"inner.ql": "I<\n@block content {\ninner-default\n@}\n>\n",
		"outer.ql": "before\n@embed \"inner.ql\" {\n@block content {\noverride\n@}\n@}\nafter\n",
	})
	inner := loadPinned(t, eng, "inner.ql")
	plain := renderPinned(t, eng, inner, nil)
	c1 := inner.comp.Load()
	if c1 == nil {
		t.Fatal("inner.ql did not memoize")
	}
	type defSnap struct {
		owner *Template
		node  interface{}
	}
	snapshot := map[string][]defSnap{}
	nodes := map[string]interface{}{}
	for name, e := range c1.blocks {
		nodes[name] = e.node
		for _, d := range e.chain {
			snapshot[name] = append(snapshot[name], defSnap{owner: d.owner, node: d.node})
		}
	}

	out := renderPinned(t, eng, loadPinned(t, eng, "outer.ql"), nil)
	if !strings.Contains(out, "I<\noverride\n>") {
		t.Fatalf("embed override did not apply: %q", out)
	}

	c2 := inner.comp.Load()
	if c2 != c1 {
		t.Fatal("embedding inner.ql replaced its composition memo")
	}
	for name, e := range c2.blocks {
		if nodes[name] != interface{}(e.node) {
			t.Errorf("block %q: cached entry node changed after embed", name)
		}
		if len(e.chain) != len(snapshot[name]) {
			t.Fatalf("block %q: cached chain length changed after embed: %d -> %d",
				name, len(snapshot[name]), len(e.chain))
		}
		for i, d := range e.chain {
			if snapshot[name][i].owner != d.owner || snapshot[name][i].node != interface{}(d.node) {
				t.Errorf("block %q: cached chain[%d] changed after embed", name, i)
			}
		}
	}
	if got := renderPinned(t, eng, inner, nil); got != plain {
		t.Errorf("plain render after embed diverged:\n got %q\nwant %q", got, plain)
	}
}

// TestStaticCompositionMemoConcurrentRenders races cold-start renders of one
// shared Template: the first finishers publish the memo while others still
// build or already serve it, and every output must be identical. Both fixture
// families run -- the extends/trait chain (shared block table, parent()
// dispatch) and the import chain (shared macro table read by swapMacroHome
// merges, replayed nsBinds) -- so under -race this is the memo's
// publication-safety and shared-read-safety proof.
func TestStaticCompositionMemoConcurrentRenders(t *testing.T) {
	raceRenders := func(t *testing.T, fixtures map[string]string, main string,
		vars func() map[string]runtime.Value) {
		t.Helper()
		eng := newCachingStub(fixtures)
		// Pre-resolve every template serially so the caching stub's own map is
		// warm and the race is confined to the composition memo under test.
		for name := range fixtures {
			loadPinned(t, eng, name)
		}
		tmpl := loadPinned(t, eng, main)
		coldEng := newCachingStub(fixtures)
		want := renderPinned(t, coldEng, loadPinned(t, coldEng, main), vars())

		const workers = 16
		const rounds = 25
		var wg sync.WaitGroup
		errs := make(chan string, workers)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < rounds; i++ {
					got, err := Render(eng, tmpl, vars())
					if err != nil {
						errs <- "render error: " + err.Error()
						return
					}
					if got != want {
						errs <- "concurrent render diverged: " + got
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for msg := range errs {
			t.Error(msg)
		}
		if tmpl.comp.Load() == nil {
			t.Error("concurrent renders left no composition memo")
		}
	}
	t.Run("extends-trait-chain", func(t *testing.T) {
		raceRenders(t, staticChainFixtures(), "child.ql",
			func() map[string]runtime.Value { return nil })
	})
	t.Run("import-macro-chain", func(t *testing.T) {
		raceRenders(t, importFixtures(), "root.ql", func() map[string]runtime.Value {
			return map[string]runtime.Value{"who": runtime.Str("w")}
		})
	})
}

// TestSandboxPhaseOneRunsOnMemoizedChain proves the per-render Phase-1 check
// is replayed over the memoized chain, not folded into the memo: a Template
// memoized by an unsandboxed render must still fail under a later restrictive
// policy with exactly the error a cold build reports.
func TestSandboxPhaseOneRunsOnMemoizedChain(t *testing.T) {
	fixtures := map[string]string{
		"base.ql": "@block body {\n@for i in [1, 2] {\n{{ i }}\n@}\n@}\n",
		"page.ql": "@extends \"base.ql\"\n",
	}
	pol := &sandbox.Policy{Tags: map[string]bool{"block": true, "extends": true}}

	eng := newCachingStub(fixtures)
	eng.policy = pol
	tmpl := loadPinned(t, eng, "page.ql")
	renderPinned(t, eng, tmpl, nil) // sandbox off: memoizes the chain
	if tmpl.comp.Load() == nil {
		t.Fatal("page.ql did not memoize")
	}
	eng.sandboxOn = true
	_, warmErr := Render(eng, tmpl, nil)
	if warmErr == nil {
		t.Fatal("sandboxed warm render of a @for-using chain succeeded")
	}

	cold := newCachingStub(fixtures)
	cold.policy = pol
	cold.sandboxOn = true
	_, coldErr := Render(cold, loadPinned(t, cold, "page.ql"), nil)
	if coldErr == nil {
		t.Fatal("sandboxed cold render of a @for-using chain succeeded")
	}
	if warmErr.Error() != coldErr.Error() {
		t.Errorf("Phase-1 error differs on the memoized chain:\nwarm %q\ncold %q", warmErr, coldErr)
	}
}

// TestCoverageSeedingIdenticalOnMemoizedChain proves coverage seeding replays
// per render over the memoized chain: one covered render on a pre-warmed memo
// reports byte-for-byte what one covered render on a cold build reports.
func TestCoverageSeedingIdenticalOnMemoizedChain(t *testing.T) {
	report := func(prewarm bool) string {
		eng := newCachingStub(staticChainFixtures())
		tmpl := loadPinned(t, eng, "child.ql")
		if prewarm {
			renderPinned(t, eng, tmpl, nil) // coverage off: builds the memo only
			if tmpl.comp.Load() == nil {
				t.Fatal("prewarm render did not memoize")
			}
		}
		eng.cov = cover.NewCollector()
		renderPinned(t, eng, tmpl, nil)
		var b strings.Builder
		if err := eng.cov.Report().WriteTextVerbose(&b); err != nil {
			t.Fatalf("coverage report: %v", err)
		}
		return b.String()
	}
	warm := report(true)
	cold := report(false)
	if warm != cold {
		t.Errorf("coverage differs between memoized and cold composition:\nwarm:\n%s\ncold:\n%s", warm, cold)
	}
}

// TestMemoizedIncludeInsideRender proves a nested render (an @include's
// sub-interp) serves the included template's memo too, and that repeated
// includes across renders stay byte-stable.
func TestMemoizedIncludeInsideRender(t *testing.T) {
	eng := newCachingStub(map[string]string{
		"partial.ql": "@extends \"pbase.ql\"\n@block row {\nrow<{{ v }}>\n@}\n",
		"pbase.ql":   "P:\n@block row {\ndefault\n@}\n",
		"host.ql":    "start\n@include \"partial.ql\" with { v: x }\nend\n",
	})
	host := loadPinned(t, eng, "host.ql")
	vars := func() map[string]runtime.Value {
		return map[string]runtime.Value{"x": runtime.Int(7)}
	}
	first := renderPinned(t, eng, host, vars())
	partial := loadPinned(t, eng, "partial.ql")
	if partial.comp.Load() == nil {
		t.Fatal("included partial did not memoize through its sub-render")
	}
	for i := 0; i < 100; i++ {
		if got := renderPinned(t, eng, host, vars()); got != first {
			t.Fatalf("include render %d diverged:\n got %q\nwant %q", i, got, first)
		}
	}
}

// mustParse parses body under name, failing the test on error.
func mustParse(t *testing.T, name, body string) *Template {
	t.Helper()
	mod, err := parse.ParseString(name, body)
	if err != nil {
		t.Fatalf("parse %q: %v", name, err)
	}
	return Prepare(name, mod)
}

// TestStaticCompositionInputsClassification pins the Prepare-time gate: only
// a string-literal @extends operand and string-literal @import/@from sources
// classify as static.
func TestStaticCompositionInputsClassification(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		static bool
	}{
		{"plain", "hello\n", true},
		{"literal extends", "@extends \"base.ql\"\n", true},
		{"literal import", "@import \"lib.ql\" as ns\n", true},
		{"literal from", "@from \"lib.ql\" import m\n", true},
		{"use only", "@use \"trait.ql\"\n@block b {\nx\n@}\n", true},
		{"dynamic extends", "@extends parentName\n", false},
		{"candidate extends", "@extends [\"a.ql\", \"b.ql\"]\n", false},
		{"concat extends", "@extends \"a\" ~ \".ql\"\n", false},
		{"dynamic import", "@import libName as ns\n", false},
		{"self import", "@import _self as me\n", false},
		{"dynamic from", "@from libName import m\n", false},
	}
	for _, tc := range cases {
		tmpl := mustParse(t, "t.ql", tc.body)
		if tmpl.compStatic != tc.static {
			t.Errorf("%s: compStatic = %v, want %v", tc.name, tmpl.compStatic, tc.static)
		}
	}
}
