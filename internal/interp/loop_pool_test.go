package interp

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// This file is the adversarial battery for gate-scoped loop snapshot pooling.
// Pooling recycles the []Pair buffer a plain KArray @for materializes at entry,
// behind a Prepare-time escape gate that must prove the loop value never
// outlives the iteration. The hazard the battery hunts is a gate false negative:
// pooling a loop whose snapshot is still reachable after the loop ends, so a
// later loop's PairsInto clobbers the memory the reachable snapshot reads.
//
// Every case renders the SAME template twice, once with pooling on (the
// shipped default) and once with poolLoopSnapshots forced off, and asserts the
// two outputs are byte-identical, which is the whole safety claim: pooling is a
// pure buffer-reuse and can never change a rendered byte. The kill case is
// pool_then_sibling_loop_clobbers, where a snapshot captured out of one loop is
// read AFTER a second sibling loop ran: if the gate wrongly pooled the first
// loop, the sibling's snapshot overwrites the capture and the two renders
// diverge.

// renderPooled renders body against eng with loop snapshot pooling ON.
func renderPooled(t *testing.T, eng *stubEngine, body string, vars map[string]runtime.Value) (string, error) {
	t.Helper()
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	poolLoopSnapshots = true
	return Render(context.Background(), eng, Prepare("test", mod), vars)
}

// renderUnpooled renders body against eng with loop snapshot pooling OFF, the
// fresh-Pairs() reference path the pooled render must match byte-for-byte.
func renderUnpooled(t *testing.T, eng *stubEngine, body string, vars map[string]runtime.Value) (string, error) {
	t.Helper()
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	poolLoopSnapshots = false
	out, err := Render(context.Background(), eng, Prepare("test", mod), vars)
	poolLoopSnapshots = true
	return out, err
}

// assertPoolParity renders body both ways and fails unless the two outputs and
// error strings match exactly and, when want is non-nil, the pooled output
// equals *want. It is the core assertion of the battery: pooling changes no byte.
func assertPoolParity(t *testing.T, eng *stubEngine, body string, vars map[string]runtime.Value, want *string) string {
	t.Helper()
	gotPool, errPool := renderPooled(t, eng, body, vars)
	gotFresh, errFresh := renderUnpooled(t, eng, body, vars)
	if (errPool == nil) != (errFresh == nil) {
		t.Fatalf("pooled/unpooled error mismatch: pooled=%v unpooled=%v", errPool, errFresh)
	}
	if errPool != nil && errFresh != nil && errPool.Error() != errFresh.Error() {
		t.Fatalf("pooled/unpooled error text diverged:\n pooled:   %v\n unpooled: %v", errPool, errFresh)
	}
	if gotPool != gotFresh {
		t.Fatalf("pooling changed output:\n pooled:   %q\n unpooled: %q", gotPool, gotFresh)
	}
	if want != nil && gotPool != *want {
		t.Fatalf("output mismatch:\n got:  %q\n want: %q", gotPool, *want)
	}
	return gotPool
}

// TestPoolSiblingLoopDoesNotClobberCapturedSnapshot is the kill case: a snapshot
// bound out of the first loop (@set snap = loop, plus snap.prev/next which read
// the loop's pair slice) is read AFTER a second sibling loop runs. The first
// loop's loop value escapes via the @set, so the gate MUST NOT pool it; if it
// did, the sibling loop (pooled or not) would recycle the first loop's
// buffer and snap.prev/index/next would read the sibling's data. Rendering both
// ways and finding them identical proves the escaping first loop kept the fresh
// path.
func TestPoolSiblingLoopDoesNotClobberCapturedSnapshot(t *testing.T) {
	eng := newStub(nil)
	// A cell holds the captured loop so it survives the loop's child scope and is
	// still readable after the second sibling loop runs. If the first loop pooled
	// its snapshot, the sibling loop's PairsInto would clobber the [10,20,30]
	// buffer and snap.prev/next/index/length would read the sibling's [1..5] data.
	body := "@set snap = cell(null)\n" +
		"@for a in [10, 20, 30] {\n" +
		"@if loop.index == 2 {\n@set snap.value = loop\n@}\n" +
		"@}\n" +
		"@for b in [1, 2, 3, 4, 5] {\n@}\n" +
		"snap: {{ snap.value.index }}/{{ snap.value.prev }}/{{ snap.value.next }}/{{ snap.value.length }}\n"
	want := "snap: 2/10/30/3\n"
	assertPoolParity(t, eng, body, nil, &want)

	// The first loop must be classified escaping (unpooled); the empty sibling
	// loop is pool-safe. This pins that the parity above is not an accident of
	// both loops taking the same path.
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := Prepare("test", mod)
	fors := collectFors(mod)
	if len(fors) != 2 {
		t.Fatalf("expected 2 @for nodes, got %d", len(fors))
	}
	if tmpl.forSafe[fors[0]] {
		t.Error("first loop captures loop via @set snap = loop; it must NOT be pool-safe")
	}
	if !tmpl.forSafe[fors[1]] {
		t.Error("second sibling loop is capture-free; it should be pool-safe")
	}
}

// TestPoolNestedLoopsDrawDistinctBuffers renders nested pool-safe loops whose
// bodies both read loop.* fields (so both are candidates to pool) and asserts
// the inner loop never reuses the buffer the outer is still ranging over. The
// outer element and its neighbours interleave with the inner element and its
// neighbours; a shared buffer would corrupt the outer's prev/next mid-iteration.
func TestPoolNestedLoopsDrawDistinctBuffers(t *testing.T) {
	eng := newStub(nil)
	body := "@for a in [1, 2, 3] {\n" +
		"o{{ loop.index }}<{{ loop.prev ?? \"-\" }},{{ loop.next ?? \"-\" }}>[\n" +
		"@for b in [7, 8, 9] {\n" +
		"i{{ loop.index }}<{{ loop.prev ?? \"-\" }},{{ loop.next ?? \"-\" }}>\n" +
		"@}\n" +
		"]o{{ loop.index }}<{{ loop.prev ?? \"-\" }},{{ loop.next ?? \"-\" }}>\n" +
		"@}\n"
	got := assertPoolParity(t, eng, body, nil, nil)
	// The outer neighbours after the inner loop must still be the OUTER loop's,
	// proving the inner did not overwrite the outer's live buffer.
	if !strings.Contains(got, "]o2<1,3>") {
		t.Errorf("outer loop.prev/next corrupted by inner loop buffer reuse: %q", got)
	}

	mod, _ := parse.ParseString("test", body)
	tmpl := Prepare("test", mod)
	for i, f := range collectFors(mod) {
		if !tmpl.forSafe[f] {
			t.Errorf("nested loop %d reads only loop.* scalars and should be pool-safe", i)
		}
	}
}

// TestPoolArrowBodyLoopReferenceStaysUnpooled binds an arrow whose BODY
// references loop directly and applies it after the loop through a cell. A bare
// loop in an arrow body is resolved at call time, possibly after the loop ended,
// so the analyzer marks the loop escaping and it must not pool. Both renders
// agree on the (empty) result of reading loop out of scope; the point is the
// classification and the parity, not a meaningful value.
func TestPoolArrowBodyLoopReferenceStaysUnpooled(t *testing.T) {
	eng := newStub(nil)
	body := "@set holder = cell(null)\n" +
		"@for x in [11, 22, 33] {\n" +
		"@set holder.value = () => loop.index ?? \"gone\"\n" +
		"@}\n" +
		"@for y in [1, 2, 3, 4] {\n@}\n" +
		"got: {{ holder.value() }}\n"
	assertPoolParity(t, eng, body, nil, nil)

	mod, _ := parse.ParseString("test", body)
	tmpl := Prepare("test", mod)
	fors := collectFors(mod)
	if tmpl.forSafe[fors[0]] {
		t.Error("loop referenced inside an arrow body must not be pool-safe")
	}
}

// TestPoolCapturedSnapshotReadAcrossSiblingLoop captures the loop into a cell in
// the LAST iteration, reads its prev/next AFTER a wider sibling loop ran, and
// asserts the frozen neighbours survive. The @set snap = loop marks the first
// loop escaping (unpooled), so the sibling loop's PairsInto cannot clobber the
// captured pair slice the snapshot reads through prev/next. A wrongly pooled
// first loop would surface the sibling's neighbours here.
func TestPoolCapturedSnapshotReadAcrossSiblingLoop(t *testing.T) {
	eng := newStub(nil)
	body := "@set c = cell(null)\n" +
		"@for x in [11, 22, 33, 44] {\n" +
		"@if loop.index == 3 {\n@set c.value = loop\n@}\n" +
		"@}\n" +
		"@for y in [100, 200, 300, 400, 500, 600] {\n@}\n" +
		"snap: {{ c.value.prev }}/{{ c.value.next }}/{{ c.value.index }}\n"
	want := "snap: 22/44/3\n"
	assertPoolParity(t, eng, body, nil, &want)

	mod, _ := parse.ParseString("test", body)
	tmpl := Prepare("test", mod)
	if tmpl.forSafe[collectFors(mod)[0]] {
		t.Error("loop captured via @set c.value = loop must not be pool-safe")
	}
}

// TestPoolCellStoringLoopReadAfterLoop stores the loop value itself in a cell
// mid-loop and reads its fields after a later loop; the cell holds the loop
// object, so the loop escapes and must keep its fresh snapshot.
func TestPoolCellStoringLoopReadAfterLoop(t *testing.T) {
	eng := newStub(nil)
	body := "@set c = cell(null)\n" +
		"@for x in [5, 6, 7] {\n" +
		"@if loop.last {\n@set c.value = loop\n@}\n" +
		"@}\n" +
		"@for y in [1, 2, 3, 4, 5, 6] {\n@}\n" +
		"held: {{ c.value.index }}/{{ c.value.prev }}/{{ c.value.length }}\n"
	want := "held: 3/6/3\n"
	assertPoolParity(t, eng, body, nil, &want)

	mod, _ := parse.ParseString("test", body)
	tmpl := Prepare("test", mod)
	if tmpl.forSafe[collectFors(mod)[0]] {
		t.Error("loop stored in a cell must not be pool-safe")
	}
}

// TestPoolFusedLoopStaysUnpooled renders a fused @for..if. The survivor slice is
// a separate allocation the pooling deliberately does not touch, so a fused loop
// must take the fresh path regardless of its escape status.
func TestPoolFusedLoopStaysUnpooled(t *testing.T) {
	eng := newStub(nil)
	body := "@for x in [1, 2, 3, 4, 5, 6] if x % 2 == 0 {\n" +
		"[{{ x }}:{{ loop.index }}/{{ loop.length }}]\n" +
		"@}\n"
	want := "[2:1/3]\n[4:2/3]\n[6:3/3]\n"
	assertPoolParity(t, eng, body, nil, &want)
}

// TestPoolFusedLoopNeverPoolSafe pins the pooling invariant that no fused
// @for..if is ever classified pool-safe. Pooling recycles the entry-time
// snapshot of a plain loop; a fused loop iterates a freshly allocated survivors
// slice, so execFor gates it out with filter == nil. Prepare must ALSO keep
// every fused loop out of forSafe, giving execFor a second, independent guard:
// were forSafe the only gate, a fused loop whose filter binds loop inline
// (walked before the loop's escLoop frame exists, so escapeInnermost marks
// nothing) would read as pool-safe. Each fused shape below must classify unsafe;
// the trailing plain loop proves the check is not vacuously failing every loop.
func TestPoolFusedLoopNeverPoolSafe(t *testing.T) {
	fusedUnsafe := []struct {
		name string
		body string
	}{
		{"plain_filter", "@for x in [1, 2, 3] if x > 1 {\n{{ x }}\n@}\n"},
		{"filter_binds_loop", "@for x in [1, 2, 3] if loop = x {\n{{ x }}\n@}\n"},
		{"inline_loop_fields", "@for x in [1, 2, 3, 4] if x % 2 == 0 {\n{{ loop.index }}/{{ loop.length }}\n@}\n"},
		{"body_captures_loop", "@for x in [1, 2, 3] if x > 1 {\n@set snap = loop\n@}\n"},
	}
	for _, c := range fusedUnsafe {
		mod, err := parse.ParseString("test", c.body)
		if err != nil {
			t.Fatalf("%s: parse error: %v", c.name, err)
		}
		tmpl := Prepare("test", mod)
		fors := collectFors(mod)
		if len(fors) != 1 {
			t.Fatalf("%s: expected 1 @for, got %d", c.name, len(fors))
		}
		if tmpl.forSafe[fors[0]] {
			t.Errorf("%s: a fused @for..if must never be pool-safe (forSafe)", c.name)
		}
	}

	// Contrast: the same iterand without the filter, capture-free, IS pool-safe,
	// so the assertion above discriminates the fused clause, not every loop.
	mod, err := parse.ParseString("test", "@for x in [1, 2, 3] {\n{{ x }}:{{ loop.index }}\n@}\n")
	if err != nil {
		t.Fatal(err)
	}
	tmpl := Prepare("test", mod)
	if !tmpl.forSafe[collectFors(mod)[0]] {
		t.Error("a plain capture-free KArray loop should be pool-safe")
	}
}

// TestPoolFusedBodyCapturesLoopParity renders a fused @for..if whose body
// captures the loop value into a cell that outlives the loop, reads the frozen
// snapshot AFTER a later pool-safe sibling loop runs, and asserts the output is
// byte-identical with pooling forced on and off. The fused loop is unpooled
// (its survivors slice is a fresh allocation and it is excluded from forSafe), so
// the sibling loop's recycled buffer cannot reach the captured snapshot; a
// regression that pooled the fused loop onto its survivors would surface the
// sibling's neighbours here and diverge the two renders.
func TestPoolFusedBodyCapturesLoopParity(t *testing.T) {
	eng := newStub(nil)
	body := "@set c = cell(null)\n" +
		"@for x in [10, 20, 30, 40] if x > 15 {\n" +
		"@if loop.index == 2 {\n@set c.value = loop\n@}\n" +
		"@}\n" +
		"@for y in [1, 2, 3, 4, 5, 6, 7] {\n@}\n" +
		"snap: {{ c.value.prev }}/{{ c.value.next }}/{{ c.value.index }}/{{ c.value.length }}\n"
	want := "snap: 20/40/2/3\n"
	assertPoolParity(t, eng, body, nil, &want)

	mod, _ := parse.ParseString("test", body)
	tmpl := Prepare("test", mod)
	fors := collectFors(mod)
	if tmpl.forSafe[fors[0]] {
		t.Error("fused loop whose body captures loop must not be pool-safe")
	}
	if !tmpl.forSafe[fors[1]] {
		t.Error("the trailing capture-free sibling loop should be pool-safe")
	}
}

// TestPoolLoopElementMutationValueSemantics pools a loop that mutates each
// element array in place; the write privatizes (COW) and must not reach the
// source array, exactly as the fresh path. A recycled buffer holds copies of the
// element Values (same *Array pointers), so the pooling cannot change whether a
// member-write privatizes: the buffer is a materialized snapshot, byte-for-byte
// what Pairs() built.
func TestPoolLoopElementMutationValueSemantics(t *testing.T) {
	eng := newStub(nil)
	body := "@set rows = [[1, 2], [3, 4], [5, 6]]\n" +
		"@for row in rows {\n@set row[0] = row[0] * 10\n[{{ row[0] }},{{ row[1] }}]\n@}\n" +
		"src: {{ rows[0][0] }}/{{ rows[1][0] }}/{{ rows[2][0] }}\n"
	want := "[10,2]\n[30,4]\n[50,6]\nsrc: 1/3/5\n"
	assertPoolParity(t, eng, body, nil, &want)
}

// TestPoolNestedLoopsWithInnerMutation nests two pooled loops where the inner
// mutates its element; the inner draws a DISTINCT buffer from the outer, so the
// mutation and the distinct snapshots never cross. Both loops read loop.* so both
// pool, and the outer's post-inner element read must still be its own.
func TestPoolNestedLoopsWithInnerMutation(t *testing.T) {
	eng := newStub(nil)
	body := "@set grid = [[[1], [2]], [[3], [4]]]\n" +
		"@for outer in grid {\n" +
		"o{{ loop.index }}:\n" +
		"@for cell in outer {\n@set cell[0] = cell[0] + 100\ni{{ loop.index }}={{ cell[0] }}\n@}\n" +
		"end-o{{ loop.index }}\n" +
		"@}\n" +
		"src: {{ grid[0][0][0] }}/{{ grid[1][1][0] }}\n"
	want := "o1:\ni1=101\ni2=102\nend-o1\no2:\ni1=103\ni2=104\nend-o2\nsrc: 1/4\n"
	assertPoolParity(t, eng, body, nil, &want)

	mod, _ := parse.ParseString("test", body)
	tmpl := Prepare("test", mod)
	for i, f := range collectFors(mod) {
		if !tmpl.forSafe[f] {
			t.Errorf("nested mutating loop %d reads only loop.* scalars and should be pool-safe", i)
		}
	}
}

// TestPoolHostIterableStaysUnpooled iterates a host Iterable Object. Only KArray
// iterands pool; a KObject Iterable has no *Array to materialize from, so it must
// keep the Iterate() path.
func TestPoolHostIterableStaysUnpooled(t *testing.T) {
	eng := newStub(nil)
	u := runtime.Obj(&hostUser{name: "ada", roles: []string{"admin", "user", "guest"}})
	body := "@for r in u {\n[{{ r }}:{{ loop.index }}/{{ loop.length }}]\n@}\n"
	want := "[admin:1/3]\n[user:2/3]\n[guest:3/3]\n"
	assertPoolParity(t, eng, body, map[string]runtime.Value{"u": u}, &want)
}

// TestPoolErrorMidLoopReleasesBuffer raises a runtime error partway through a
// pooled loop, then renders a clean loop afterward on the same interp to prove
// the buffer returned to the pool on the error path and the next loop is
// correct. The two renders (error then clean) run on one interp via an include.
func TestPoolErrorMidLoopReleasesBuffer(t *testing.T) {
	eng := newStub(map[string]string{
		"part.ql": "@for x in [1, 2, 3] {\n{{ x.missing }}\n@}\n",
	})
	// The outer template runs a clean pooled loop, then includes a partial whose
	// loop errors mid-iteration; the error must surface identically both ways.
	body := "@for a in [9, 8] {\n{{ a }}\n@}\n@include \"part.ql\"\n"
	gotPool, errPool := renderPooled(t, eng, body, nil)
	gotFresh, errFresh := renderUnpooled(t, eng, body, nil)
	if (errPool == nil) != (errFresh == nil) {
		t.Fatalf("error presence diverged: pooled=%v unpooled=%v", errPool, errFresh)
	}
	if errPool == nil {
		t.Fatalf("expected a mid-loop error, got output %q", gotPool)
	}
	if errPool.Error() != errFresh.Error() {
		t.Fatalf("error text diverged:\n pooled:   %v\n unpooled: %v", errPool, errFresh)
	}
	if gotPool != gotFresh {
		t.Fatalf("partial output before error diverged:\n pooled:   %q\n unpooled: %q", gotPool, gotFresh)
	}
}

// TestPoolLoopBoundaryPrevNext pins loop.prev at the first element and loop.next
// at the last (the Null boundaries) under pooling, since those read the recycled
// buffer's neighbouring slots directly.
func TestPoolLoopBoundaryPrevNext(t *testing.T) {
	eng := newStub(nil)
	body := "@for x in [100, 200, 300] {\n" +
		"({{ loop.prev ?? \"nil\" }}|{{ x }}|{{ loop.next ?? \"nil\" }})\n" +
		"@}\n"
	want := "(nil|100|200)\n(100|200|300)\n(200|300|nil)\n"
	assertPoolParity(t, eng, body, nil, &want)
}

// TestPoolNestedInnerCapturesOuterViaParent captures the OUTER loop from the
// inner body (loop.parent) and reads it after both loops end. The inner capture
// reaches the outer loop's buffer through the parent link, so BOTH loops must
// stay unpooled, per the outward propagation rule. A later sibling loop then runs;
// if either loop had pooled, the captured parent snapshot would be clobbered.
func TestPoolNestedInnerCapturesOuterViaParent(t *testing.T) {
	eng := newStub(nil)
	body := "@set keep = null\n" +
		"@for a in [10, 20] {\n" +
		"@for b in [1, 2, 3] {\n" +
		"@if loop.parent.index == 2 and loop.index == 3 {\n@set keep = loop.parent\n@}\n" +
		"@}\n" +
		"@}\n" +
		"@for c in [7, 8, 9, 10, 11] {\n@}\n" +
		"kept: {{ keep.index }}/{{ keep.prev }}/{{ keep.length }}\n"
	want := "kept: 2/10/2\n"
	assertPoolParity(t, eng, body, nil, &want)

	mod, _ := parse.ParseString("test", body)
	tmpl := Prepare("test", mod)
	fors := collectFors(mod)
	if tmpl.forSafe[fors[0]] {
		t.Error("outer loop reachable via a captured inner loop.parent must not be pool-safe")
	}
	if tmpl.forSafe[fors[1]] {
		t.Error("inner loop bound via @set keep = loop.parent must not be pool-safe")
	}
	if !tmpl.forSafe[fors[2]] {
		t.Error("the trailing capture-free sibling loop should be pool-safe")
	}
}

// TestPoolLoopInsideBlockIsPoolSafe pins the composition gain: a self-contained
// KArray loop inside a @block body (the shape the Compose workload renders)
// is analyzed through the block descent and classified pool-safe, so the block's
// loop recycles a buffer like a top-level loop.
func TestPoolLoopInsideBlockIsPoolSafe(t *testing.T) {
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

	// The rendered output must be identical pooled and unpooled.
	items := runtime.Arr(runtime.NewList(runtime.Str("a"), runtime.Str("b"), runtime.Str("c")))
	vars := map[string]runtime.Value{"items": items}
	poolLoopSnapshots = true
	gotPool, errPool := Render(context.Background(), eng, tmpl, vars)
	poolLoopSnapshots = false
	gotFresh, errFresh := Render(context.Background(), eng, tmpl, vars)
	poolLoopSnapshots = true
	if errPool != nil || errFresh != nil {
		t.Fatalf("render errors: pooled=%v unpooled=%v", errPool, errFresh)
	}
	if gotPool != gotFresh {
		t.Fatalf("pooling changed block-loop output:\n pooled:   %q\n unpooled: %q", gotPool, gotFresh)
	}
}

// TestPoolBlockSiteInsideLoopStaysUnpooled pins the other side of the block
// rule: a loop whose body contains a @block SITE dispatches a body the walk
// cannot see (an override may capture loop), so that loop must not pool.
func TestPoolBlockSiteInsideLoopStaysUnpooled(t *testing.T) {
	mod, err := parse.ParseString("test", "@for x in [1, 2, 3] {\n@block hole {\ndefault\n@}\n@}\n")
	if err != nil {
		t.Fatal(err)
	}
	tmpl := Prepare("test", mod)
	if tmpl.forSafe[collectFors(mod)[0]] {
		t.Error("a loop containing a @block site must not be pool-safe")
	}
}

// TestPoolContextEnumerationStaysUnpooled reads _context inside a loop, which
// surfaces the loop value into an enumerable structure; the loop must not pool.
func TestPoolContextEnumerationStaysUnpooled(t *testing.T) {
	eng := newStub(nil)
	body := "@for x in [1, 2] {\n@set snap = _context\n@}\nlen: {{ snap.x }}\n"
	assertPoolParity(t, eng, body, nil, nil)

	mod, _ := parse.ParseString("test", body)
	tmpl := Prepare("test", mod)
	if tmpl.forSafe[collectFors(mod)[0]] {
		t.Error("a loop whose body reads _context must not be pool-safe")
	}
}

// TestPoolConcurrentRendersRaceClean runs many renders of a pool-safe loop
// concurrently on templates sharing one Environment (stub engine), proving the
// buffer pool is per-interp (per-render) and never shared across goroutines.
// Run under -race, a shared pool would trip the detector; correctness is checked
// by every goroutine seeing the identical expected output.
func TestPoolConcurrentRendersRaceClean(t *testing.T) {
	eng := newStub(nil)
	body := "@for x in [1, 2, 3, 4, 5] {\n{{ x }}:{{ loop.index }}/{{ loop.length }} \n@}\n"
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatal(err)
	}
	// One prepared Template shared by every goroutine, exactly the production
	// shape where a memoized Template is rendered concurrently.
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

// collectFors returns every @for node in the module in source order, so a test
// can assert the pool-safety classification of a specific loop.
func collectFors(mod *ast.Node) []*ast.Node {
	var out []*ast.Node
	var walk func(n *ast.Node)
	walk = func(n *ast.Node) {
		if n == nil {
			return
		}
		if n.Kind == ast.KindFor {
			out = append(out, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(mod)
	return out
}
