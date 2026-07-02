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
// Context remains beside Scope as the raw ordered-map primitive with an eager
// Clone; the interpreter's render path uses Scope.
type Scope struct {
	parent *Scope
	order  []string
	vars   map[string]Value
}

// NewScope returns an empty root scope.
func NewScope() *Scope {
	return &Scope{vars: map[string]Value{}}
}

// Child pushes a fresh frame whose reads fall through to this scope. It copies
// nothing: the child starts empty and shadows on write, which is what makes
// scope entry O(1) regardless of how many bindings are visible.
func (s *Scope) Child() *Scope {
	return &Scope{parent: s, vars: map[string]Value{}}
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

// bind stores name -> v in this frame, recording first-seen order.
func (s *Scope) bind(name string, v Value) {
	if _, ok := s.vars[name]; !ok {
		s.order = append(s.order, name)
	}
	s.vars[name] = v
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
		if v, ok := f.vars[name]; ok {
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
		for _, n := range frames[i].order {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}
