package runtime

import "github.com/avmnu-sng/quill-template-engine/errors"

// loopInfo is the loop.* metadata for one iteration -- shared by the tree-walking
// interpreter and the compiled backend so both spend the loop-metadata
// correctness budget once -- computed on access instead
// of stored in a per-iteration map. index / index0 / first / last / length /
// revindex / revindex0 derive from the pair (i, n); prev / next read the
// neighbouring element from the materialized pairs on demand; parent points at
// the enclosing loop's value. This keeps the full loop.* feature set while
// allocating only one small object per iteration rather than a fresh map plus
// eleven inserts.
//
// A FRESH loopInfo is bound each iteration, which is load-bearing: a captured
// loop (@set snap = loop) must be a frozen snapshot of that step, so reusing and
// mutating one object across iterations is forbidden (spec 01 Section 4.2, and the
// value-type contract of spec 04 Section 6.3). loopInfo is a host Object exposing
// only field access, which is the entire observed contract for loop -- loop.field
// plus the syntactically special-cased loop.changed(...). Indexable is provided so
// loop["index"] resolves like loop.index, and it reports as a mapping.
//
// parent is a pointer rather than an inline Value: every iteration of one loop
// shares the same enclosing-loop value, probed exactly once at loop entry, so
// pointing all of the loop's fresh iteration objects at that one probe result
// keeps the struct in the 64-byte allocation size class instead of carrying a
// 64-byte Value copy per iteration into the 128-byte class. The pointee -- a
// per-loop boxed probe result, or a shared read-only Null when no enclosing
// loop exists -- stays unwritten after construction, and GetField("parent")
// copies it out, so a captured snapshot still reads the exact bits the
// entry-time probe produced.
type loopInfo struct {
	i, n      int
	pairs     []Pair
	parent    *Value
	depth0    int
	recursive bool // a recursive @for level additionally exposes depth / depth0
}

// NewLoopValue binds the loop.* metadata for iteration i over pairs, with parent
// pointing at the enclosing loop's value (Null at the top level). The pointee is
// shared by every iteration of the loop and must stay unwritten while any
// iteration's loop value is reachable, which callers get by probing the
// enclosing value once into a dedicated local at loop entry. n is the pair
// count, so every field -- including first/last/length/revindex and prev/next --
// reflects the sequence pairs actually holds (already the survivor subset when a
// fused filter ran).
func NewLoopValue(i int, pairs []Pair, parent *Value) Value {
	return Obj(&loopInfo{i: i, n: len(pairs), pairs: pairs, parent: parent})
}

// NewRecursiveLoopValue binds the loop.* metadata for a recursive @for level,
// which additionally exposes depth (1-based) and depth0 (0-based) for the current
// descent so a template can indent or number nested structures. parent carries
// the same pointer contract as NewLoopValue.
func NewRecursiveLoopValue(i int, pairs []Pair, depth0 int, parent *Value) Value {
	return Obj(&loopInfo{i: i, n: len(pairs), pairs: pairs, parent: parent, recursive: true, depth0: depth0})
}

// GetField resolves a loop.* field on access. Every field is always defined for
// the loop's kind: a plain loop resolves the ten common fields, a recursive loop
// additionally resolves depth/depth0. An unknown name -- including "changed",
// which is recognized syntactically as a method rather than a field -- reports ok
// false, so a strict read raises undefined exactly as the former mapping did.
func (li *loopInfo) GetField(name string) (Value, bool) {
	switch name {
	case "index0":
		return Int(int64(li.i)), true
	case "index":
		return Int(int64(li.i + 1)), true
	case "revindex0":
		return Int(int64(li.n - 1 - li.i)), true
	case "revindex":
		return Int(int64(li.n - li.i)), true
	case "first":
		return Bool(li.i == 0), true
	case "last":
		return Bool(li.i == li.n-1), true
	case "length":
		return Int(int64(li.n)), true
	case "prev":
		if li.i > 0 {
			return li.pairs[li.i-1].Val, true
		}
		return Null(), true
	case "next":
		if li.i < li.n-1 {
			return li.pairs[li.i+1].Val, true
		}
		return Null(), true
	case "parent":
		// Copy the pointee out, so the caller holds the same standalone Value an
		// inline field used to yield; a nil pointer reads as the top-level Null
		// parent, keeping the exported constructors total for host callers.
		if li.parent == nil {
			return Null(), true
		}
		return *li.parent, true
	case "depth":
		if li.recursive {
			return Int(int64(li.depth0 + 1)), true
		}
	case "depth0":
		if li.recursive {
			return Int(int64(li.depth0)), true
		}
	}
	return Null(), false
}

// CallMethod reports that loop has no callable members. loop.changed(...) is
// recognized syntactically before any member call, so it never routes here.
func (li *loopInfo) CallMethod(name string, _ []Value) (Value, error) {
	return Null(), errors.New(errors.KindAttribute, "loop has no method %q", name)
}

// GetIndex resolves loop["field"] the same way as loop.field, so a subscript read
// matches dotted access. Only a string subscript names a field; any other key is
// absent.
func (li *loopInfo) GetIndex(key Value) (Value, bool) {
	if key.Kind == KStr {
		return li.GetField(key.S)
	}
	return Null(), false
}

// ClassName reports "loop" so a read of an unknown field names the loop value in
// its error ("no member ... on object loop") rather than a generic object.
func (li *loopInfo) ClassName() string { return "loop" }
