package quill

import (
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/compiled"
	"github.com/avmnu-sng/quill-template-engine/ext"
	"github.com/avmnu-sng/quill-template-engine/loader"
	"github.com/avmnu-sng/quill-template-engine/runtime"
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

// TestCompiledCacheSharesWarmCacheUnderConcurrency drives many concurrent
// by-name renders through a compiled unit that memoizes an @cache region,
// installed with WithCompiled on one Environment so every goroutine shares one
// RenderCache. The compiled Render threads the Environment's store exactly as
// the dispatch passes it, so a hit replays the stored body and a miss stores it;
// under the race detector this proves the shared-cache threading holds no data
// race and every render is byte-identical to the interpreter's, whichever
// goroutine warms the store first. The manifest's Render reproduces the
// generated @cache shape (Get, then a miss render followed by Put) over the
// passed handle, so it dispatches (byte-matching the interpreter) and exercises
// the real environment.go cache-passing path.
func TestCompiledCacheSharesWarmCacheUnderConcurrency(t *testing.T) {
	const src = "@cache key=\"hdr\" {\nbody\n@}\n"
	const want = "body\n"
	tmpls := map[string]string{"t.ql": src}

	manifest := &compiled.Manifest{
		Entry:       "t.ql",
		Sources:     map[string]string{"t.ql": src},
		Fingerprint: defaultFingerprint(),
		Render: func(w io.Writer, _ *ext.ExtensionSet, _ map[string]runtime.Value, rc compiled.RenderCache) error {
			// The generated @cache shape over the shared handle: namespace the key
			// by the entry template, replay a hit, or render the body once and
			// store it on a miss. A nil handle would always miss; the dispatch
			// always supplies the Environment's store.
			key := "t.ql\x00hdr"
			if rc != nil {
				if cached, ok := rc.Get(key); ok {
					_, err := io.WriteString(w, cached)
					return err
				}
			}
			body := "body\n"
			if rc != nil {
				rc.Put(key, body, nil)
			}
			_, err := io.WriteString(w, body)
			return err
		},
	}

	env := NewWithArray(tmpls, WithCompiled(manifest))

	// A tracer proves the gate serves the compiled unit rather than falling back,
	// so the concurrent loop measures the compiled shared-cache path.
	tracer := markerManifest("t.ql", src, defaultFingerprint(), false)
	tenv := NewWithArray(tmpls, WithCompiled(tracer))
	if out, err := tenv.Render("t.ql", nil); err != nil || out != dispatchMarker {
		t.Fatalf("dispatch gate did not serve the compiled unit: out=%q err=%v", out, err)
	}

	interp := NewWithArray(tmpls)
	if out, err := interp.Render("t.ql", nil); err != nil || out != want {
		t.Fatalf("interpreter render drifted from the pinned contract: out=%q err=%v", out, err)
	}

	const workers = 32
	const rounds = 50
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				out, err := env.Render("t.ql", nil)
				if err != nil {
					errs <- err
					return
				}
				if out != want {
					errs <- io.ErrUnexpectedEOF
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent compiled cache render diverged: %v", err)
	}
	if cached, ok := env.RenderCache().Get("t.ql\x00hdr"); !ok || cached != want {
		t.Fatalf("shared cache did not warm under concurrency: got %q ok=%v", cached, ok)
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
