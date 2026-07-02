package interp

import (
	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// loopInfo is the loop.* metadata for one iteration, computed on access instead
// of stored in a per-iteration map. index / index0 / first / last / length /
// revindex / revindex0 derive from the pair (i, n); prev / next read the
// neighbouring element from the materialized pairs on demand; parent is the
// enclosing loop's value. This keeps the full loop.* feature set while allocating
// only one small object per iteration rather than a fresh map plus eleven inserts.
//
// A FRESH loopInfo is bound each iteration, which is load-bearing: a captured
// loop (@set snap = loop) must be a frozen snapshot of that step, so reusing and
// mutating one object across iterations is forbidden (spec 01 Section 4.2, and the
// value-type contract of spec 04 Section 6.3). loopInfo is a host Object exposing
// only field access, which is the entire observed contract for loop -- loop.field
// plus the syntactically special-cased loop.changed(...). Indexable is provided so
// loop["index"] resolves like loop.index, and it reports as a mapping.
type loopInfo struct {
	i, n      int
	pairs     []runtime.Pair
	parent    runtime.Value
	recursive bool // a recursive @for level additionally exposes depth / depth0
	depth0    int
}

// newLoopValue binds the loop.* metadata for iteration i over pairs, with parent
// the enclosing loop's value (Null at the top level). n is the pair count, so
// every field -- including first/last/length/revindex and prev/next -- reflects
// the sequence pairs actually holds (already the survivor subset when a fused
// filter ran).
func newLoopValue(i int, pairs []runtime.Pair, parent runtime.Value) runtime.Value {
	return runtime.Obj(&loopInfo{i: i, n: len(pairs), pairs: pairs, parent: parent})
}

// newRecursiveLoopValue binds the loop.* metadata for a recursive @for level,
// which additionally exposes depth (1-based) and depth0 (0-based) for the current
// descent so a template can indent or number nested structures.
func newRecursiveLoopValue(i int, pairs []runtime.Pair, depth0 int, parent runtime.Value) runtime.Value {
	return runtime.Obj(&loopInfo{i: i, n: len(pairs), pairs: pairs, parent: parent, recursive: true, depth0: depth0})
}

// GetField resolves a loop.* field on access. Every field is always defined for
// the loop's kind: a plain loop resolves the ten common fields, a recursive loop
// additionally resolves depth/depth0. An unknown name -- including "changed",
// which is recognized syntactically as a method rather than a field -- reports ok
// false, so a strict read raises undefined exactly as the former mapping did.
func (li *loopInfo) GetField(name string) (runtime.Value, bool) {
	switch name {
	case "index0":
		return runtime.Int(int64(li.i)), true
	case "index":
		return runtime.Int(int64(li.i + 1)), true
	case "revindex0":
		return runtime.Int(int64(li.n - 1 - li.i)), true
	case "revindex":
		return runtime.Int(int64(li.n - li.i)), true
	case "first":
		return runtime.Bool(li.i == 0), true
	case "last":
		return runtime.Bool(li.i == li.n-1), true
	case "length":
		return runtime.Int(int64(li.n)), true
	case "prev":
		if li.i > 0 {
			return li.pairs[li.i-1].Val, true
		}
		return runtime.Null(), true
	case "next":
		if li.i < li.n-1 {
			return li.pairs[li.i+1].Val, true
		}
		return runtime.Null(), true
	case "parent":
		return li.parent, true
	case "depth":
		if li.recursive {
			return runtime.Int(int64(li.depth0 + 1)), true
		}
	case "depth0":
		if li.recursive {
			return runtime.Int(int64(li.depth0)), true
		}
	}
	return runtime.Null(), false
}

// CallMethod reports that loop has no callable members. loop.changed(...) is
// recognized syntactically before any member call, so it never routes here.
func (li *loopInfo) CallMethod(name string, _ []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), errors.New(errors.KindAttribute, "loop has no method %q", name)
}

// GetIndex resolves loop["field"] the same way as loop.field, so a subscript read
// matches dotted access. Only a string subscript names a field; any other key is
// absent.
func (li *loopInfo) GetIndex(key runtime.Value) (runtime.Value, bool) {
	if key.Kind == runtime.KStr {
		return li.GetField(key.S)
	}
	return runtime.Null(), false
}

// ClassName reports "loop" so a read of an unknown field names the loop value in
// its error ("no member ... on object loop") rather than a generic object.
func (li *loopInfo) ClassName() string { return "loop" }
