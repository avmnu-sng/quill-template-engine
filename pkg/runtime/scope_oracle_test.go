package runtime

import (
	"fmt"
	"math/rand"
	"testing"
)

// oracleScope is the flat-map Scope representation this package shipped before
// the ordered-slice frames, kept verbatim as a live oracle: every randomized
// operation in TestScopeMatchesMapOracle runs against both representations and
// the full visible state -- per-frame binding order, values, and the shared
// flag of every reachable *Array -- must match after each step. The slice
// representation is a pure swap, so any divergence is a bug in it.
type oracleScope struct {
	parent *oracleScope
	order  []string
	vars   map[string]Value
}

func newOracleScope() *oracleScope {
	return &oracleScope{vars: map[string]Value{}}
}

func (s *oracleScope) Child() *oracleScope {
	return &oracleScope{parent: s, vars: map[string]Value{}}
}

func (s *oracleScope) Set(name string, v Value) {
	s.bind(name, ShareValue(v))
}

func (s *oracleScope) SetOwned(name string, v Value) {
	s.bind(name, v)
}

func (s *oracleScope) bind(name string, v Value) {
	if _, ok := s.vars[name]; !ok {
		s.order = append(s.order, name)
	}
	s.vars[name] = v
}

func (s *oracleScope) Get(name string) (Value, bool) {
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

func (s *oracleScope) Has(name string) bool {
	_, ok := s.Get(name)
	return ok
}

func (s *oracleScope) Names() []string {
	var frames []*oracleScope
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

// twinGen builds structurally equal value pairs whose *Array pointers are
// disjoint between the two scope stacks, so a shared-flag write on one side
// can never leak to the other; every array pair is recorded for the
// flag-equality sweep after each operation.
type twinGen struct {
	r     *rand.Rand
	pairs [][2]*Array
}

// twin returns one value for the slice scope and one for the oracle scope,
// structurally identical, with fresh unshared arrays on both sides.
func (g *twinGen) twin(depth int) (Value, Value) {
	switch g.r.Intn(6) {
	case 0:
		return Null(), Null()
	case 1:
		b := g.r.Intn(2) == 0
		return Bool(b), Bool(b)
	case 2:
		i := int64(g.r.Intn(100))
		return Int(i), Int(i)
	case 3:
		s := fmt.Sprintf("s%d", g.r.Intn(100))
		return Str(s), Str(s)
	default:
		if depth >= 2 {
			i := int64(g.r.Intn(100))
			return Int(i), Int(i)
		}
		an, ao := NewArray(), NewArray()
		n := g.r.Intn(4)
		for i := 0; i < n; i++ {
			vn, vo := g.twin(depth + 1)
			if g.r.Intn(2) == 0 {
				an.SetInt(int64(i), vn)
				ao.SetInt(int64(i), vo)
			} else {
				key := fmt.Sprintf("k%d", i)
				an.SetStr(key, vn)
				ao.SetStr(key, vo)
			}
		}
		g.pairs = append(g.pairs, [2]*Array{an, ao})
		return Arr(an), Arr(ao)
	}
}

// track records an array pair created outside twin (a copy-on-write clone) so
// its flags join the per-operation sweep.
func (g *twinGen) track(vn, vo Value) {
	if vn.Kind() == KArray && vn.AsArray() != nil && vo.Kind() == KArray && vo.AsArray() != nil {
		g.pairs = append(g.pairs, [2]*Array{vn.AsArray(), vo.AsArray()})
	}
}

// equalTwinValues compares a slice-scope value against its oracle twin:
// kind, scalar payload, and for arrays the shared flag, the key sequence,
// and every element recursively. Twin trees are acyclic by construction.
func equalTwinValues(vn, vo Value) error {
	if vn.Kind() != vo.Kind() {
		return fmt.Errorf("kind %v != oracle %v", vn.Kind(), vo.Kind())
	}
	switch vn.Kind() {
	case KArray:
		an, ao := vn.AsArray(), vo.AsArray()
		if (an == nil) != (ao == nil) {
			return fmt.Errorf("nil array %v != oracle %v", an == nil, ao == nil)
		}
		if an == nil {
			return nil
		}
		if an.shared != ao.shared {
			return fmt.Errorf("shared flag %v != oracle %v", an.shared, ao.shared)
		}
		if len(an.keys) != len(ao.keys) {
			return fmt.Errorf("len %d != oracle %d", len(an.keys), len(ao.keys))
		}
		for i, k := range an.keys {
			if k != ao.keys[i] {
				return fmt.Errorf("key[%d] %q != oracle %q", i, k, ao.keys[i])
			}
			if err := equalTwinValues(an.vals[k], ao.vals[k]); err != nil {
				return fmt.Errorf("elem %q: %w", k, err)
			}
		}
		return nil
	default:
		if vn.AsBool() != vo.AsBool() || vn.AsInt() != vo.AsInt() || vn.AsFloat() != vo.AsFloat() || vn.AsStr() != vo.AsStr() {
			return fmt.Errorf("scalar payload %+v != oracle %+v", vn, vo)
		}
		return nil
	}
}

// frameNames returns one slice frame's own names in insertion order, read
// straight from the representation so verification never calls Get (whose
// cross-frame share-marking would symmetrically re-mark both sides and could
// mask a missing mark in the operation under test).
func frameNames(s *Scope) []string {
	out := make([]string, 0, len(s.entries))
	for i := range s.entries {
		out = append(out, s.entries[i].name)
	}
	return out
}

// checkScopePair compares the two scope stacks frame by frame: same frame
// count, same per-frame name order, twin-equal value (including shared flags)
// under every name, and flag equality across all recorded array pairs.
func checkScopePair(t *testing.T, g *twinGen, sn *Scope, so *oracleScope) {
	t.Helper()
	fn, fo := sn, so
	level := 0
	for fn != nil || fo != nil {
		if (fn == nil) != (fo == nil) {
			t.Fatalf("frame depth diverges at level %d", level)
		}
		names := frameNames(fn)
		if len(names) != len(fo.order) {
			t.Fatalf("level %d: %d names != oracle %d (%v vs %v)",
				level, len(names), len(fo.order), names, fo.order)
		}
		for i, name := range names {
			if name != fo.order[i] {
				t.Fatalf("level %d: name[%d] %q != oracle %q", level, i, name, fo.order[i])
			}
			vn, ok := fn.lookup(name)
			if !ok {
				t.Fatalf("level %d: %q in order but not bound", level, name)
			}
			if err := equalTwinValues(vn, fo.vars[name]); err != nil {
				t.Fatalf("level %d: %q: %v", level, name, err)
			}
		}
		fn, fo = fn.parent, fo.parent
		level++
	}
	for i, p := range g.pairs {
		if p[0].shared != p[1].shared {
			t.Fatalf("array pair %d: shared %v != oracle %v", i, p[0].shared, p[1].shared)
		}
	}
}

// TestScopeMatchesMapOracle drives randomized operation sequences -- binds,
// owned binds, aliasing rebinds, reads, presence checks, Names snapshots,
// frame pushes and pops, and the engine's privatize-then-member-write pattern
// -- against the slice-frame Scope and the retired flat-map representation in
// lockstep. After every operation the entire visible state must match: frame
// count, per-frame insertion order, structural values, and the shared flag of
// every array either side has ever allocated. Name pools wider than
// scopeSpillWidth push frames across the spill boundary, and sized roots
// exercise NewScopeSized, so both the linear and the spilled regime are under
// the oracle on every seed.
func TestScopeMatchesMapOracle(t *testing.T) {
	names := make([]string, 14)
	for i := range names {
		names[i] = fmt.Sprintf("n%02d", i)
	}
	for seed := int64(0); seed < 40; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed%02d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			g := &twinGen{r: r}
			var rootN *Scope
			if r.Intn(2) == 0 {
				rootN = NewScopeSized(r.Intn(2 * scopeSpillWidth))
			} else {
				rootN = NewScope()
			}
			stackN := []*Scope{rootN}
			stackO := []*oracleScope{newOracleScope()}
			top := func() (*Scope, *oracleScope) {
				return stackN[len(stackN)-1], stackO[len(stackO)-1]
			}
			for op := 0; op < 400; op++ {
				sn, so := top()
				name := names[r.Intn(len(names))]
				switch r.Intn(9) {
				case 0, 1: // Fresh bind, the dominant scope operation.
					vn, vo := g.twin(0)
					sn.Set(name, vn)
					so.Set(name, vo)
				case 2: // Aliasing rebind: read one name, bind it under another.
					src := names[r.Intn(len(names))]
					vn, okN := sn.Get(src)
					vo, okO := so.Get(src)
					if okN != okO {
						t.Fatalf("op %d: Get(%q) found %v != oracle %v", op, src, okN, okO)
					}
					if okN {
						sn.Set(name, vn)
						so.Set(name, vo)
					}
				case 3: // Owned bind of a fresh value (a privatized root binding).
					vn, vo := g.twin(0)
					sn.SetOwned(name, vn)
					so.SetOwned(name, vo)
				case 4: // Read: result and presence must match; marking is the side effect under test.
					vn, okN := sn.Get(name)
					vo, okO := so.Get(name)
					if okN != okO {
						t.Fatalf("op %d: Get(%q) found %v != oracle %v", op, name, okN, okO)
					}
					if okN {
						if err := equalTwinValues(vn, vo); err != nil {
							t.Fatalf("op %d: Get(%q): %v", op, name, err)
						}
					}
				case 5: // Presence check (is defined), which reads through Get.
					if hn, ho := sn.Has(name), so.Has(name); hn != ho {
						t.Fatalf("op %d: Has(%q) %v != oracle %v", op, name, hn, ho)
					}
				case 6: // Visible-name snapshot (_context, strict-undefined hints).
					nn, no := sn.Names(), so.Names()
					if len(nn) != len(no) {
						t.Fatalf("op %d: Names len %d != oracle %d (%v vs %v)", op, len(nn), len(no), nn, no)
					}
					for i := range nn {
						if nn[i] != no[i] {
							t.Fatalf("op %d: Names[%d] %q != oracle %q", op, i, nn[i], no[i])
						}
					}
				case 7: // Push or pop a frame, biased toward shallow stacks.
					if len(stackN) > 1 && r.Intn(2) == 0 {
						stackN = stackN[:len(stackN)-1]
						stackO = stackO[:len(stackO)-1]
					} else if len(stackN) < 5 {
						stackN = append(stackN, sn.Child())
						stackO = append(stackO, so.Child())
					}
				case 8: // The member-write pattern: read, privatize (Own), mutate, bind owned.
					vn, okN := sn.Get(name)
					vo, okO := so.Get(name)
					if okN != okO {
						t.Fatalf("op %d: Get(%q) found %v != oracle %v", op, name, okN, okO)
					}
					if !okN || vn.Kind() != KArray || vn.AsArray() == nil {
						continue
					}
					ownedN, clonedN := Own(vn)
					ownedO, clonedO := Own(vo)
					if clonedN != clonedO {
						t.Fatalf("op %d: Own(%q) cloned %v != oracle %v", op, name, clonedN, clonedO)
					}
					g.track(ownedN, ownedO)
					elem := int64(r.Intn(3))
					val := int64(r.Intn(100))
					ownedN.AsArray().SetInt(elem, Int(val))
					ownedO.AsArray().SetInt(elem, Int(val))
					sn.SetOwned(name, ownedN)
					so.SetOwned(name, ownedO)
				}
				checkScopePair(t, g, stackN[len(stackN)-1], stackO[len(stackO)-1])
			}
		})
	}
}
