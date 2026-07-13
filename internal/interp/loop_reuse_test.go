package interp

import (
	"context"
	"sync"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// This file is the adversarial battery for gate-scoped per-iteration loop-value
// reuse. A pool-safe @for binds ONE loop metadata object and advances only its
// index each iteration, instead of allocating a fresh object per element. The
// hazard the battery hunts is the exact shape of the first historical
// correctness regression: reusing the object under a loop whose value can be
// captured (@set snap = loop), passed to a callable, or otherwise read after the
// step, which would let a later index advance mutate an already-captured
// snapshot. Reuse is de-risked ONLY by the escape gate (forSafe) proving the
// loop value never outlives its iteration.
//
// Every case renders the SAME template twice, once with reuse on (the shipped
// default) and once with reuseLoopInfo forced off, the fresh-per-iteration
// NewLoopValue path, then asserts the two outputs are byte-identical, the whole
// safety claim: reuse is a pure allocation elision and can never change a
// rendered byte. Cases whose loop escapes ALSO assert the loop is classified
// unsafe (absent from forSafe), so the parity is not an accident of both renders
// taking the fresh path; the reuse would corrupt the capture if the gate let
// it through.

// renderReuse renders body against eng with loop-value reuse ON.
func renderReuse(t *testing.T, eng *stubEngine, body string, vars map[string]runtime.Value) (string, error) {
	t.Helper()
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	reuseLoopInfo = true
	return Render(context.Background(), eng, Prepare("test", mod), vars)
}

// renderFresh renders body against eng with loop-value reuse OFF, the
// fresh-per-iteration NewLoopValue reference path the reused render must match
// byte-for-byte.
func renderFresh(t *testing.T, eng *stubEngine, body string, vars map[string]runtime.Value) (string, error) {
	t.Helper()
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	reuseLoopInfo = false
	out, err := Render(context.Background(), eng, Prepare("test", mod), vars)
	reuseLoopInfo = true
	return out, err
}

// assertReuseParity renders body both ways and fails unless the two outputs and
// error strings match exactly and, when want is non-nil, the reused output
// equals *want. It is the core assertion of the battery: reuse changes no byte.
func assertReuseParity(t *testing.T, eng *stubEngine, body string, vars map[string]runtime.Value, want *string) string {
	t.Helper()
	gotReuse, errReuse := renderReuse(t, eng, body, vars)
	gotFresh, errFresh := renderFresh(t, eng, body, vars)
	if (errReuse == nil) != (errFresh == nil) {
		t.Fatalf("reuse/fresh error mismatch: reuse=%v fresh=%v", errReuse, errFresh)
	}
	if errReuse != nil && errFresh != nil && errReuse.Error() != errFresh.Error() {
		t.Fatalf("reuse/fresh error text diverged:\n reuse: %v\n fresh: %v", errReuse, errFresh)
	}
	if gotReuse != gotFresh {
		t.Fatalf("reuse changed output:\n reuse: %q\n fresh: %q", gotReuse, gotFresh)
	}
	if want != nil && gotReuse != *want {
		t.Fatalf("output mismatch:\n got:  %q\n want: %q", gotReuse, *want)
	}
	return gotReuse
}

// assertLoopUnsafe fails unless the loop at index idx of body is classified
// escaping (absent from forSafe), pinning that a capture keeps the fresh path.
func assertLoopUnsafe(t *testing.T, body string, idx int, why string) {
	t.Helper()
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	tmpl := Prepare("test", mod)
	fors := collectFors(mod)
	if idx >= len(fors) {
		t.Fatalf("loop index %d out of range (%d loops)", idx, len(fors))
	}
	if tmpl.forSafe[fors[idx]] {
		t.Errorf("loop %d must not be pool-safe: %s", idx, why)
	}
}

// TestReuseCaptureViaSetStaysFrozen is the kill case for reuse. A loop that
// captures its value each step via @set snap = loop and reads the PRIOR step's
// index on the next step must classify unsafe, so it keeps the fresh path and
// the capture stays a frozen snapshot. If the gate wrongly reused one object,
// the capture would read the CURRENT index every step and the two renders would
// diverge (fresh: prior index; reused: current index). The template reads the
// prior snapshot at the start of each step after the first, so a frozen capture
// yields 1 then 2, never the live 2 then 3.
func TestReuseCaptureViaSetStaysFrozen(t *testing.T) {
	eng := newStub(nil)
	body := "@for n in [10, 20, 30] {\n" +
		"@if not loop.first {\nwas={{ snap.index }}\n@}\n" +
		"@set snap = loop\n" +
		"@}\n"
	want := "was=1\nwas=2\n"
	assertReuseParity(t, eng, body, nil, &want)
	assertLoopUnsafe(t, body, 0, "captures loop via @set snap = loop")
}

// TestReuseCaptureInCellReadAfterLoop stores the loop value in a cell that
// outlives the loop's child scope and reads its fields after the loop ends. The
// cell holds the object, so the loop escapes and must keep the fresh path;
// otherwise a later index advance (there is none here, but the frozen contract
// is what the classification protects) would surface a different step. The
// capture happens in the last iteration and its frozen index/prev/next/length
// must read that step both ways.
func TestReuseCaptureInCellReadAfterLoop(t *testing.T) {
	eng := newStub(nil)
	body := "@set c = cell(null)\n" +
		"@for x in [5, 6, 7] {\n" +
		"@if loop.last {\n@set c.value = loop\n@}\n" +
		"@}\n" +
		"held: {{ c.value.index }}/{{ c.value.prev }}/{{ c.value.length }}\n"
	want := "held: 3/6/3\n"
	assertReuseParity(t, eng, body, nil, &want)
	assertLoopUnsafe(t, body, 0, "loop stored in a cell read after the loop")
}

// TestReuseCapturesTwoIterationsIndependent captures the loop value in two
// different iterations of one loop and reads both after the loop ends. With a
// reused object both captures would read the SAME final index; the classification
// keeps the loop on the fresh path so each capture is its own frozen step, and
// the two renders must agree that the captures stay index 1 and index 2 with
// their own prev/next.
func TestReuseCapturesTwoIterationsIndependent(t *testing.T) {
	eng := newStub(nil)
	body := "@set first = cell(null)\n@set second = cell(null)\n" +
		"@for n in [7, 8, 9] {\n" +
		"@if loop.index == 1 {\n@set first.value = loop\n@}\n" +
		"@if loop.index == 2 {\n@set second.value = loop\n@}\n" +
		"@}\n" +
		"{{ first.value.index }}/{{ first.value.prev }}/{{ first.value.next }} " +
		"{{ second.value.index }}/{{ second.value.prev }}/{{ second.value.next }}\n"
	want := "1//8 2/7/9\n"
	assertReuseParity(t, eng, body, nil, &want)
	assertLoopUnsafe(t, body, 0, "captures loop in two iterations")
}

// TestReuseLoopPassedToArrowStaysUnsafe binds an arrow whose body references
// loop and applies it after the loop through a cell. A bare loop inside an arrow
// body resolves at call time, possibly after the loop advanced or ended, so the
// analyzer marks the loop escaping and it must keep the fresh path. Both renders
// agree on the out-of-scope read; the point is the classification and parity.
func TestReuseLoopPassedToArrowStaysUnsafe(t *testing.T) {
	eng := newStub(nil)
	body := "@set holder = cell(null)\n" +
		"@for x in [11, 22, 33] {\n" +
		"@set holder.value = () => loop.index ?? \"gone\"\n" +
		"@}\n" +
		"got: {{ holder.value() }}\n"
	assertReuseParity(t, eng, body, nil, nil)
	assertLoopUnsafe(t, body, 0, "loop referenced inside an arrow body")
}

// TestReuseLoopPassedToFunctionStaysUnsafe passes the loop value as a filter
// argument, a bare-loop value position that lets the loop escape into the
// filter. The loop must classify unsafe and keep the fresh path; both renders
// must agree byte-for-byte on the emitted value.
func TestReuseLoopPassedToFunctionStaysUnsafe(t *testing.T) {
	eng := newStub(nil)
	body := "@for x in [1, 2, 3] {\n{{ [loop] | length }}\n@}\n"
	want := "1\n1\n1\n"
	assertReuseParity(t, eng, body, nil, &want)
	assertLoopUnsafe(t, body, 0, "loop passed as a filter argument")
}

// TestReuseNestedLoopsBothPoolSafe renders nested loops that both read only
// loop.* scalars (index/prev/next), so both are pool-safe and both reuse their
// own object. The inner loop must not disturb the outer's object mid-iteration:
// the outer's neighbours after the inner loop must still be the outer's. Reuse
// gives each loop its own cursor, so the interleaving cannot cross.
func TestReuseNestedLoopsBothPoolSafe(t *testing.T) {
	eng := newStub(nil)
	body := "@for a in [1, 2, 3] {\n" +
		"o{{ loop.index }}<{{ loop.prev ?? \"-\" }},{{ loop.next ?? \"-\" }}>[\n" +
		"@for b in [7, 8, 9] {\n" +
		"i{{ loop.index }}<{{ loop.prev ?? \"-\" }},{{ loop.next ?? \"-\" }}>\n" +
		"@}\n" +
		"]o{{ loop.index }}<{{ loop.prev ?? \"-\" }},{{ loop.next ?? \"-\" }}>\n" +
		"@}\n"
	got := assertReuseParity(t, eng, body, nil, nil)
	if got == "" {
		t.Fatal("empty render")
	}
	// The outer neighbours after the inner loop must still be the OUTER loop's,
	// proving the inner cursor did not disturb the outer's object.
	mod, _ := parse.ParseString("test", body)
	tmpl := Prepare("test", mod)
	for i, f := range collectFors(mod) {
		if !tmpl.forSafe[f] {
			t.Errorf("nested loop %d reads only loop.* scalars and should be pool-safe", i)
		}
	}
}

// TestReuseNestedInnerCapturesParentStaysUnsafe captures the OUTER loop from the
// inner body via loop.parent and reads it after both loops end. The inner
// capture reaches the outer object through the parent link, so BOTH loops must
// classify unsafe (the outward propagation rule) and keep the fresh path; a
// reused outer object would let the outer's index advance corrupt the captured
// parent snapshot. Both renders must yield the frozen parent step.
func TestReuseNestedInnerCapturesParentStaysUnsafe(t *testing.T) {
	eng := newStub(nil)
	body := "@set keep = cell(null)\n" +
		"@for a in [10, 20] {\n" +
		"@for b in [1, 2, 3] {\n" +
		"@if loop.parent.index == 2 and loop.index == 3 {\n@set keep.value = loop.parent\n@}\n" +
		"@}\n" +
		"@}\n" +
		"kept: {{ keep.value.index }}/{{ keep.value.prev }}/{{ keep.value.length }}\n"
	want := "kept: 2/10/2\n"
	assertReuseParity(t, eng, body, nil, &want)
	assertLoopUnsafe(t, body, 0, "outer loop reachable via a captured inner loop.parent")
	assertLoopUnsafe(t, body, 1, "inner loop bound via @set keep = loop.parent")
}

// TestReusePrevNextParentReadsPoolSafe renders a plain nested loop that reads
// prev, next, and parent inline as scalars without capturing the loop value, the
// pool-safe shape that reuses its object. Every neighbour and parent read must be
// byte-identical with reuse on and off; the reads copy scalars (or a neighbour
// element) out of the object, retaining nothing.
func TestReusePrevNextParentReadsPoolSafe(t *testing.T) {
	eng := newStub(nil)
	body := "@for a in [1, 2] {\n" +
		"@for b in [7, 8, 9] {\n" +
		"p{{ loop.parent.index }}i{{ loop.index }}<{{ loop.prev ?? \"-\" }},{{ loop.next ?? \"-\" }}>\n" +
		"@}\n" +
		"@}\n"
	want := "p1i1<-,8>\np1i2<7,9>\np1i3<8,->\np2i1<-,8>\np2i2<7,9>\np2i3<8,->\n"
	assertReuseParity(t, eng, body, nil, &want)
	mod, _ := parse.ParseString("test", body)
	tmpl := Prepare("test", mod)
	for i, f := range collectFors(mod) {
		if !tmpl.forSafe[f] {
			t.Errorf("nested loop %d reads only loop.* scalars and should be pool-safe", i)
		}
	}
}

// TestReuseFusedLoopUsesFreshPath renders a fused @for..if. A fused loop is
// never classified pool-safe (its survivors slice is a fresh allocation), and
// execFor gates reuse on filter == nil, so it takes the fresh path regardless.
// The output must be identical with reuse on and off, and the loop must classify
// unsafe.
func TestReuseFusedLoopUsesFreshPath(t *testing.T) {
	eng := newStub(nil)
	body := "@for x in [1, 2, 3, 4, 5, 6] if x % 2 == 0 {\n" +
		"[{{ x }}:{{ loop.index }}/{{ loop.length }}]\n" +
		"@}\n"
	want := "[2:1/3]\n[4:2/3]\n[6:3/3]\n"
	assertReuseParity(t, eng, body, nil, &want)
	assertLoopUnsafe(t, body, 0, "a fused @for..if is never pool-safe")
}

// TestReuseFusedBodyCapturesLoopFrozen renders a fused loop whose body captures
// the loop value into a cell read after the loop. The fused loop is unpooled
// (survivors slice fresh, excluded from forSafe), so the capture is a frozen
// snapshot both ways; the output must be byte-identical.
func TestReuseFusedBodyCapturesLoopFrozen(t *testing.T) {
	eng := newStub(nil)
	body := "@set c = cell(null)\n" +
		"@for x in [10, 20, 30, 40] if x > 15 {\n" +
		"@if loop.index == 2 {\n@set c.value = loop\n@}\n" +
		"@}\n" +
		"snap: {{ c.value.prev }}/{{ c.value.next }}/{{ c.value.index }}/{{ c.value.length }}\n"
	want := "snap: 20/40/2/3\n"
	assertReuseParity(t, eng, body, nil, &want)
	assertLoopUnsafe(t, body, 0, "fused loop whose body captures loop")
}

// TestReuseLoopElementMutationValueSemantics reuses a loop that mutates each
// element array in place; the write privatizes (COW) and must not reach the
// source array, exactly as the fresh path. Reuse touches only the loop metadata
// object, never the pair values, so it cannot change whether a member-write
// privatizes.
func TestReuseLoopElementMutationValueSemantics(t *testing.T) {
	eng := newStub(nil)
	body := "@set rows = [[1, 2], [3, 4], [5, 6]]\n" +
		"@for row in rows {\n@set row[0] = row[0] * 10\n[{{ row[0] }},{{ row[1] }}]\n@}\n" +
		"src: {{ rows[0][0] }}/{{ rows[1][0] }}/{{ rows[2][0] }}\n"
	want := "[10,2]\n[30,4]\n[50,6]\nsrc: 1/3/5\n"
	assertReuseParity(t, eng, body, nil, &want)
	mod, _ := parse.ParseString("test", body)
	tmpl := Prepare("test", mod)
	if !tmpl.forSafe[collectFors(mod)[0]] {
		t.Error("a capture-free mutating loop should be pool-safe")
	}
}

// TestReuseTwoTargetLoopPoolSafe reuses a two-target (key, value) loop over a
// mapping, reading loop.index and both targets, so the reused object's index
// tracks the survivors exactly like the fresh path. The output must be
// byte-identical.
func TestReuseTwoTargetLoopPoolSafe(t *testing.T) {
	eng := newStub(nil)
	body := "@for k, v in {a: 1, b: 2, c: 3} {\n{{ loop.index }}:{{ k }}={{ v }}\n@}\n"
	want := "1:a=1\n2:b=2\n3:c=3\n"
	assertReuseParity(t, eng, body, nil, &want)
	mod, _ := parse.ParseString("test", body)
	tmpl := Prepare("test", mod)
	if !tmpl.forSafe[collectFors(mod)[0]] {
		t.Error("a capture-free two-target loop should be pool-safe")
	}
}

// TestReuseBlockLoopIsPoolSafe pins the composition gain: a self-contained
// KArray loop inside a @block body (the shape the Compose workload renders)
// reuses its object like a top-level loop, and the rendered output is identical
// with reuse on and off.
func TestReuseBlockLoopIsPoolSafe(t *testing.T) {
	eng := newStub(map[string]string{
		"base.ql": "@block items {\n(none)\n@}\n",
		"page.ql": "@extends \"base.ql\"\n@block items {\n@for it in items {\n- {{ it }}:{{ loop.index }}\n@}\n@}\n",
	})
	tmpl, err := eng.LoadTemplate(context.Background(), "page.ql")
	if err != nil {
		t.Fatal(err)
	}
	fors := collectFors(tmpl.Module)
	if len(fors) != 1 {
		t.Fatalf("expected 1 @for in page.ql, got %d", len(fors))
	}
	if !tmpl.forSafe[fors[0]] {
		t.Error("a self-contained loop inside a @block body must be pool-safe")
	}
	items := runtime.Arr(runtime.NewList(runtime.Str("a"), runtime.Str("b"), runtime.Str("c")))
	vars := map[string]runtime.Value{"items": items}
	reuseLoopInfo = true
	gotReuse, errReuse := Render(context.Background(), eng, tmpl, vars)
	reuseLoopInfo = false
	gotFresh, errFresh := Render(context.Background(), eng, tmpl, vars)
	reuseLoopInfo = true
	if errReuse != nil || errFresh != nil {
		t.Fatalf("render errors: reuse=%v fresh=%v", errReuse, errFresh)
	}
	if gotReuse != gotFresh {
		t.Fatalf("reuse changed block-loop output:\n reuse: %q\n fresh: %q", gotReuse, gotFresh)
	}
}

// TestReuseConcurrentRendersRaceClean runs many renders of a pool-safe loop
// concurrently on one shared prepared Template. The reused object is a per-render
// local (built inside execFor), so concurrent renders never share it; under
// -race a shared object would trip the detector, and every goroutine must see
// the identical expected output.
func TestReuseConcurrentRendersRaceClean(t *testing.T) {
	eng := newStub(nil)
	body := "@for x in [1, 2, 3, 4, 5] {\n{{ x }}:{{ loop.index }}/{{ loop.length }} \n@}\n"
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := Prepare("test", mod)
	const want = "1:1/5 \n2:2/5 \n3:3/5 \n4:4/5 \n5:5/5 \n"
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				out, err := Render(context.Background(), eng, tmpl, nil)
				if err != nil {
					t.Errorf("render error: %v", err)
					return
				}
				if out != want {
					t.Errorf("concurrent render output: got %q want %q", out, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}
