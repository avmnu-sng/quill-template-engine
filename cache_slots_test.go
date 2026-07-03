package quill

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/loader"
)

// TestCacheRegionWithYieldRendersFreshEveryTime pins the @cache/slots
// interaction: a cache region containing a deferred @yield must render
// identically on every render of the same Environment. The yield placeholder
// embeds a render-unique token, so storing the body would make a later render
// replay a token its own resolveSlots can never substitute; the engine
// therefore refuses to memoize such a region.
func TestCacheRegionWithYieldRendersFreshEveryTime(t *testing.T) {
	tmpls := map[string]string{
		"main.ql": "@cache key=\"hdr\" {\nhead:\n@yield syms\n@}\n" +
			"@provide syms {\nSYM\n@}\ntail\n",
	}
	env := New(loader.NewArrayLoader(tmpls))
	first, err := env.Render("main.ql", nil)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := env.Render("main.ql", nil)
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if first != second {
		t.Fatalf("cached slot region diverged across renders\nfirst:  %q\nsecond: %q", first, second)
	}
	if strings.Contains(second, "QUILL_SLOT_") {
		t.Fatalf("second render replayed a stale slot placeholder: %q", second)
	}
}

// TestCacheRegionWithProvideKeepsAccumulating pins the @provide side of the
// same rule: a cache region that feeds a slot must re-run on every render, or
// a replay would silently lose the slot content.
func TestCacheRegionWithProvideKeepsAccumulating(t *testing.T) {
	tmpls := map[string]string{
		"main.ql": "@cache key=\"feed\" {\nbody\n@provide syms {\nSYM\n@}\n@}\n" +
			"got:\n@yield syms\n",
	}
	env := New(loader.NewArrayLoader(tmpls))
	first, err := env.Render("main.ql", nil)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := env.Render("main.ql", nil)
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if first != second {
		t.Fatalf("provide-feeding cache region diverged across renders\nfirst:  %q\nsecond: %q", first, second)
	}
	if !strings.Contains(second, "SYM") {
		t.Fatalf("second render lost the provided slot content: %q", second)
	}
}

// TestCacheRegionWithIncludedProvideKeepsAccumulating extends the rule through
// composition: the @provide runs inside an @include nested in the cache
// region, so the uncacheability must be detected through the shared slot
// buffers, not just this template's own statements.
func TestCacheRegionWithIncludedProvideKeepsAccumulating(t *testing.T) {
	tmpls := map[string]string{
		"main.ql": "@cache key=\"deep\" {\n@include \"part.ql\"\n@}\ngot:\n@yield syms\n",
		"part.ql": "partbody\n@provide syms {\nSYM\n@}\n",
	}
	env := New(loader.NewArrayLoader(tmpls))
	first, err := env.Render("main.ql", nil)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := env.Render("main.ql", nil)
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if first != second {
		t.Fatalf("include-provide cache region diverged across renders\nfirst:  %q\nsecond: %q", first, second)
	}
	if !strings.Contains(second, "SYM") {
		t.Fatalf("second render lost the included slot content: %q", second)
	}
}

// TestCacheRegionWithoutSlotsIsServedFromStore proves the replay path of a
// slot-free region end to end: the first render must store the body, and a
// later render must emit the stored bytes instead of re-rendering. The yield
// token of a render with zero slot activity is never minted, and an unguarded
// scan for the empty token matches every string, so a regression here shows up
// as the region silently rendering fresh (the sentinel never appearing).
func TestCacheRegionWithoutSlotsIsServedFromStore(t *testing.T) {
	tmpls := map[string]string{
		"plain.ql": "@cache key=\"p\" {\nbody\n@}\n",
	}
	env := New(loader.NewArrayLoader(tmpls))
	if _, err := env.Render("plain.ql", nil); err != nil {
		t.Fatalf("first render: %v", err)
	}
	if _, ok := env.RenderCache().Get("plain.ql\x00p"); !ok {
		t.Fatal("slot-free cache region was not memoized")
	}
	env.RenderCache().Put("plain.ql\x00p", "SENTINEL\n", nil)
	second, err := env.Render("plain.ql", nil)
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if !strings.Contains(second, "SENTINEL") {
		t.Fatalf("second render did not replay the stored body: %q", second)
	}
}

// TestCacheRegionSlotFreeAmidSlotActivity pins that slot machinery running
// OUTSIDE a cache region never poisons the region's cacheability, in both
// orderings around the region: a @provide that runs before it (slot buffers
// exist, yield token still unminted at region time) and a @yield that runs
// before it (token minted, but the region's own output is token-free). Both
// regions must be memoized.
func TestCacheRegionSlotFreeAmidSlotActivity(t *testing.T) {
	tmpls := map[string]string{
		"pre.ql":  "@provide syms {\nSYM\n@}\n@cache key=\"side\" {\nplain-body\n@}\n@yield syms\n",
		"post.ql": "@yield syms\n@cache key=\"side\" {\nplain-body\n@}\n@provide syms {\nSYM\n@}\n",
	}
	env := New(loader.NewArrayLoader(tmpls))
	for _, name := range []string{"pre.ql", "post.ql"} {
		first, err := env.Render(name, nil)
		if err != nil {
			t.Fatalf("%s first render: %v", name, err)
		}
		if _, ok := env.RenderCache().Get(name + "\x00side"); !ok {
			t.Fatalf("%s: slot-free cache region amid slot activity was not memoized", name)
		}
		second, err := env.Render(name, nil)
		if err != nil {
			t.Fatalf("%s second render: %v", name, err)
		}
		if first != second {
			t.Fatalf("%s diverged across renders\nfirst:  %q\nsecond: %q", name, first, second)
		}
		if !strings.Contains(second, "SYM") {
			t.Fatalf("%s: second render lost the provided slot content: %q", name, second)
		}
	}
}

// TestCacheRegionSlotGating asserts the store contents directly: a slot-free
// region is memoized under its namespaced key, while a slot-using region is
// not stored at all (the render cache keys are root name + NUL + user key).
func TestCacheRegionSlotGating(t *testing.T) {
	tmpls := map[string]string{
		"plain.ql":  "@cache key=\"p\" {\nbody\n@}\n",
		"slotty.ql": "@cache key=\"s\" {\nhead:\n@yield x\n@}\n@provide x {\nX\n@}\n",
	}
	env := New(loader.NewArrayLoader(tmpls))
	if _, err := env.Render("plain.ql", nil); err != nil {
		t.Fatalf("plain render: %v", err)
	}
	if _, ok := env.RenderCache().Get("plain.ql\x00p"); !ok {
		t.Fatal("slot-free cache region was not memoized")
	}
	if _, err := env.Render("slotty.ql", nil); err != nil {
		t.Fatalf("slotty render: %v", err)
	}
	if _, ok := env.RenderCache().Get("slotty.ql\x00s"); ok {
		t.Fatal("slot-using cache region was memoized; it must render fresh every time")
	}
}
