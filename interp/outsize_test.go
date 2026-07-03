package interp

import (
	"fmt"
	"io"
	"log"
	"sync"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/cache"
	"github.com/avmnu-sng/quill-template-engine/parse"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// outsizeBody is a slot-free loop template whose output length scales with the
// bound list, so the remembered-output-length hint moves between renders and
// the pre-slot-resolution builder length equals the returned string length.
const outsizeBody = "@for item in items {\n- {{ item }}\n@}\n"

// outsizeVars binds an items list of n identical rows.
func outsizeVars(n int) map[string]runtime.Value {
	vals := make([]runtime.Value, n)
	for i := range vals {
		vals[i] = runtime.Str("row")
	}
	return map[string]runtime.Value{"items": runtime.Arr(runtime.NewList(vals...))}
}

// TestOutGrowHintCapsAndIgnoresNonPositive pins the hint-to-Grow conversion: a
// non-positive remembered length means no pre-Grow (a fresh or empty-output
// template behaves exactly like the unhinted path), an in-range length passes
// through, and anything beyond outHintCap clamps so one huge historical
// render cannot pre-commit unbounded buffers.
func TestOutGrowHintCapsAndIgnoresNonPositive(t *testing.T) {
	cases := []struct {
		last int64
		want int
	}{
		{-5, 0},
		{0, 0},
		{1, 1},
		{4096, 4096},
		{outHintCap - 1, outHintCap - 1},
		{outHintCap, outHintCap},
		{outHintCap + 1, outHintCap},
		{1 << 40, outHintCap},
	}
	for _, c := range cases {
		if got := outGrowHint(c.last); got != c.want {
			t.Errorf("outGrowHint(%d) = %d, want %d", c.last, got, c.want)
		}
	}
}

// TestOutputSizeHintDecaysAndKeepsBytesIdentical drives one shared Template
// through growing and shrinking outputs and checks the two properties the
// hint must hold: every hinted render is byte-identical to a fresh unhinted
// render of the same module (capacity never touches content), and the stored
// hint tracks the LATEST successful output length rather than a running
// maximum, so a template whose outputs shrink stops pre-committing the old
// larger buffer. A failing render must leave the hint untouched, since a
// partial buffer's length describes no complete output.
func TestOutputSizeHintDecaysAndKeepsBytesIdentical(t *testing.T) {
	eng := newStub(nil)
	mod, err := parse.ParseString("hint.ql", outsizeBody)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tmpl := Prepare("hint.ql", mod)

	// fresh renders the same module on a throwaway Template, so its hint is
	// always zero: it is the unhinted oracle every hinted render must match.
	fresh := func(n int) string {
		t.Helper()
		out, ferr := Render(eng, Prepare("hint.ql", mod), outsizeVars(n))
		if ferr != nil {
			t.Fatalf("oracle render of %d items: %v", n, ferr)
		}
		return out
	}

	if got := tmpl.lastOut.Load(); got != 0 {
		t.Fatalf("hint before any render = %d, want 0", got)
	}

	big, err := Render(eng, tmpl, outsizeVars(500))
	if err != nil {
		t.Fatalf("cold render: %v", err)
	}
	if want := fresh(500); big != want {
		t.Fatalf("cold render diverged from oracle:\ngot:  %q\nwant: %q", big, want)
	}
	if got := tmpl.lastOut.Load(); got != int64(len(big)) {
		t.Fatalf("hint after cold render = %d, want %d", got, len(big))
	}

	warm, err := Render(eng, tmpl, outsizeVars(500))
	if err != nil {
		t.Fatalf("warm render: %v", err)
	}
	if warm != big {
		t.Fatalf("warm render diverged from cold render:\ngot:  %q\nwant: %q", warm, big)
	}

	small, err := Render(eng, tmpl, outsizeVars(3))
	if err != nil {
		t.Fatalf("shrunk render: %v", err)
	}
	if want := fresh(3); small != want {
		t.Fatalf("shrunk render under a larger hint diverged from oracle:\ngot:  %q\nwant: %q", small, want)
	}
	if len(small) >= len(big) {
		t.Fatalf("fixture broken: shrunk output (%d bytes) is not smaller than big output (%d bytes)", len(small), len(big))
	}
	if got := tmpl.lastOut.Load(); got != int64(len(small)) {
		t.Fatalf("hint after shrunk render = %d, want %d (latest, not max)", got, len(small))
	}

	// A strict-undefined failure aborts the render; the partial buffer's
	// length is not a completed output and must not overwrite the hint.
	before := tmpl.lastOut.Load()
	if _, err := Render(eng, tmpl, nil); err == nil {
		t.Fatal("render with unbound items unexpectedly succeeded")
	}
	if got := tmpl.lastOut.Load(); got != before {
		t.Fatalf("hint changed across a failed render: %d -> %d", before, got)
	}
}

// TestConcurrentRendersOnSharedTemplateRecordSizesRaceClean races two
// goroutines over ONE shared Template with differently sized outputs, so the
// lastOut atomic is concurrently stored and loaded with constantly disagreeing
// values. Under the race detector this is the hint's concurrency proof; the
// per-iteration byte comparison additionally proves that whichever stale or
// fresh hint a render observes, the rendered bytes never change.
func TestConcurrentRendersOnSharedTemplateRecordSizesRaceClean(t *testing.T) {
	eng := newStub(nil)
	// Pre-build the engine's lazily created state so the goroutines below
	// only ever read the stub.
	eng.rcache = cache.NewRenderCache()
	eng.logger = log.New(io.Discard, "", 0)
	mod, err := parse.ParseString("hint.ql", outsizeBody)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tmpl := Prepare("hint.ql", mod)

	sizes := []int{400, 2}
	want := make([]string, len(sizes))
	for i, n := range sizes {
		out, oerr := Render(eng, Prepare("hint.ql", mod), outsizeVars(n))
		if oerr != nil {
			t.Fatalf("oracle render of %d items: %v", n, oerr)
		}
		want[i] = out
	}

	const iterations = 500
	errCh := make(chan error, len(sizes))
	var wg sync.WaitGroup
	for i, n := range sizes {
		wg.Add(1)
		go func(n int, want string) {
			defer wg.Done()
			vars := outsizeVars(n)
			for j := 0; j < iterations; j++ {
				out, rerr := Render(eng, tmpl, vars)
				if rerr != nil {
					errCh <- fmt.Errorf("render of %d items: %w", n, rerr)
					return
				}
				if out != want {
					errCh <- fmt.Errorf("render of %d items diverged under a racing hint", n)
					return
				}
			}
		}(n, want[i])
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
