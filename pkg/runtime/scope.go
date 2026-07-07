package runtime

// Scope is Quill's lexical variable scope, represented as a parent-linked
// frame stack: block, for, macro, with, and capture each push one child frame
// (Child), reads fall through the parent chain, and writes land in the top
// frame, shadowing any outer binding of the same name. Entering a scope is
// O(1) -- no binding is copied -- and leaving one is discarding the child
// frame. Array values bound through Set are marked copy-on-write exactly as
// Context.Set does, so *Array keeps its value semantics (spec 04 Section 6.3)
// across frames. Insertion order is preserved per frame and Names composes
// frames outermost-first, so an "available: ..." list in an undefined-variable
// error and the _context snapshot stay deterministic and match the flat-map
// ordering the engine always had (spec 04 Section 8.1).
//
// A frame stores its bindings in an insertion-ordered entries slice scanned
// linearly: template frames hold a handful of names with the hot ones bound
// first, so a short scan beats a map's hashing and its minimum bucket
// footprint, and the slice doubles as the order record that a separate order
// slice used to carry. A frame that grows past scopeSpillWidth gains a
// name-to-index side map (spill) so lookups in a wide frame -- a root frame
// fed dozens of host vars, a large @with mapping -- stay O(1); the entries
// slice remains the single source of order and of the values themselves.
//
// Context remains beside Scope as the raw ordered-map primitive with an eager
// Clone; the interpreter's render path uses Scope.
type Scope struct {
	parent  *Scope
	entries []scopeEntry
	spill   map[string]int32
}

// scopeEntry is one name binding held inline in a frame's ordered entries
// slice; keeping the Value in the slice (the spill map stores only indexes)
// means order, presence, and value live in one allocation.
type scopeEntry struct {
	name string
	v    Value
}

// scopeSpillWidth is the frame width above which bind builds the name-to-index
// spill map. Measured on the engine's access mix (uniform hits, parent-chain
// misses, hot-name rebinds), the linear scan and the map cross between 8 and
// 10 entries; 8 keeps every typical template frame on the scan while a
// 12-name frame -- the width where a scan-only frame measurably regressed --
// is already indexed.
const scopeSpillWidth = 8

// NewScope returns an empty root scope. It allocates no binding storage; the
// first bind does, so a scope that only reads stays a single small allocation.
func NewScope() *Scope {
	return &Scope{}
}

// NewScopeSized returns an empty root scope pre-sized for n bindings: the
// entries slice is allocated at capacity n up front, and a width past
// scopeSpillWidth builds the spill index eagerly so the binds that follow
// never re-index. Render entrypoints use it to size the root frame from
// len(vars), replacing the bind-time doubling ladder with one allocation.
func NewScopeSized(n int) *Scope {
	s := &Scope{}
	if n > 0 {
		s.entries = make([]scopeEntry, 0, n)
	}
	if n > scopeSpillWidth {
		s.spill = make(map[string]int32, n)
	}
	return s
}

// Child pushes a fresh frame whose reads fall through to this scope. It copies
// nothing: the child starts empty and shadows on write, which is what makes
// scope entry O(1) regardless of how many bindings are visible.
func (s *Scope) Child() *Scope {
	return &Scope{parent: s}
}

// Set binds name in this frame, shadowing any outer binding, recording
// first-seen order. An array value is marked shared (ShareValue) so a later
// mutation through this or any aliasing binding copies on write; scalars and
// host Objects pass through.
func (s *Scope) Set(name string, v Value) {
	s.bind(name, ShareValue(v))
}

// SetOwned binds name in this frame WITHOUT marking its array shared, for a
// value the caller exclusively owns -- the privatized root of a member
// assignment. Marking it shared would force a needless copy-on-write clone on
// the next mutation, making a run of member writes to one array quadratic.
func (s *Scope) SetOwned(name string, v Value) {
	s.bind(name, v)
}

// bind stores name -> v in this frame, recording first-seen order: a name
// already present is overwritten in place at its original position, a new name
// appends. Appending past scopeSpillWidth builds the spill index once; from
// then on the frame binds and looks up through the map.
func (s *Scope) bind(name string, v Value) {
	if s.spill != nil {
		if i, ok := s.spill[name]; ok {
			s.entries[i].v = v
			return
		}
		s.spill[name] = int32(len(s.entries))
		s.entries = append(s.entries, scopeEntry{name: name, v: v})
		return
	}
	for i := range s.entries {
		if s.entries[i].name == name {
			s.entries[i].v = v
			return
		}
	}
	s.entries = append(s.entries, scopeEntry{name: name, v: v})
	if len(s.entries) > scopeSpillWidth {
		m := make(map[string]int32, 2*scopeSpillWidth)
		for i := range s.entries {
			m[s.entries[i].name] = int32(i)
		}
		s.spill = m
	}
}

// lookup returns this frame's own binding for name, without falling through to
// the parent chain and without any share-marking; Get layers both on top.
func (s *Scope) lookup(name string) (Value, bool) {
	if s.spill != nil {
		if i, ok := s.spill[name]; ok {
			return s.entries[i].v, true
		}
		return Value{}, false
	}
	for i := range s.entries {
		if s.entries[i].name == name {
			return s.entries[i].v, true
		}
	}
	return Value{}, false
}

// Get returns the binding for name, reading the innermost frame that binds it.
// The bool is false when no frame binds name; the strict-undefined policy at
// the access layer turns that into an error rather than a silent Null (spec 04
// Section 8.1).
//
// A value found in a frame OTHER than the receiver is share-marked on the way
// out: it has escaped its defining frame, so a member write in the reading
// frame must privatize a copy (copy-on-write) rather than mutate the outer
// binding in place -- otherwise a previously-privatized (unshared) array would
// be written through the live parent chain and the mutation would survive the
// child frame's discard, breaking *Array value semantics (spec 04 Section
// 6.3). This reproduces what the flat-map representation got from re-marking
// every binding at scope entry. A same-frame read skips the mark, so a run of
// member writes to one name in one scope stays linear, not quadratic.
func (s *Scope) Get(name string) (Value, bool) {
	for f := s; f != nil; f = f.parent {
		if v, ok := f.lookup(name); ok {
			if f != s {
				return ShareValue(v), true
			}
			return v, true
		}
	}
	return Value{}, false
}

// Has reports whether any frame binds name. It backs the is defined test on a
// bare identifier, which tests presence and never throws; a name bound to Null
// is present (Has true).
func (s *Scope) Has(name string) bool {
	_, ok := s.Get(name)
	return ok
}

// Names returns the visible names outermost-first, deduplicated so a shadowed
// name appears once at its first-seen (outermost) position. This reproduces
// the insertion order the flat-map representation produced: a scope entry used
// to copy the parent's order and append new names, which is exactly
// outermost-frame names first, then each inner frame's additions.
func (s *Scope) Names() []string {
	var frames []*Scope
	for f := s; f != nil; f = f.parent {
		frames = append(frames, f)
	}
	seen := map[string]bool{}
	var out []string
	for i := len(frames) - 1; i >= 0; i-- {
		for _, e := range frames[i].entries {
			if !seen[e.name] {
				seen[e.name] = true
				out = append(out, e.name)
			}
		}
	}
	return out
}
