package check

import (
	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
)

// narrowTrue refines the scope for the branch where a condition is true
// (design/type-system.md Section 8.1). It handles the test forms that double as
// type guards: `x is int/string/...` narrows x to that scalar; `x is null/none`
// to null; `x is not null` removes null; `x is iterable/sequence/mapping` to a
// collection. Narrowing only ever sharpens a binding in the branch scope; it
// never touches the outer scope or the runtime. A condition it does not
// understand is ignored (no narrowing), which is always safe.
func (c *checker) narrowTrue(cond *ast.Node, branch *scope) {
	if cond == nil {
		return
	}
	switch cond.Kind {
	case ast.KindTest:
		c.narrowTest(cond, branch)
	case ast.KindLogical:
		// `a and b`: both hold in the true branch, so narrow on each.
		if cond.Str == "and" {
			c.narrowTrue(cond.Child(0), branch)
			c.narrowTrue(cond.Child(1), branch)
		}
	}
}

// narrowTest applies the narrowing of a single `x is name` test in the true
// branch. Only a bare-name subject is narrowed (a member-access subject would
// need flow-sensitive paths beyond this slice).
func (c *checker) narrowTest(cond *ast.Node, branch *scope) {
	subj := cond.Child(0)
	if subj == nil || subj.Kind != ast.KindName {
		return
	}
	cur, ok := branch.lookup(subj.Str)
	if !ok {
		cur = Any
	}
	negated := cond.Bool
	switch cond.Str {
	case "null", "none":
		if negated {
			branch.set(subj.Str, removeNull(cur))
		} else {
			branch.set(subj.Str, Null)
		}
	case "int":
		branch.set(subj.Str, narrowTo(cur, Int, negated))
	case "float":
		branch.set(subj.Str, narrowTo(cur, Float, negated))
	case "string":
		branch.set(subj.Str, narrowTo(cur, String, negated))
	case "bool":
		branch.set(subj.Str, narrowTo(cur, Bool, negated))
	case "iterable", "sequence":
		if !negated {
			branch.set(subj.Str, narrowToCollection(cur, ListOf(Any)))
		}
	case "mapping":
		if !negated {
			branch.set(subj.Str, narrowToCollection(cur, MapOf(Any, Any)))
		}
		// `is defined` does not change the static type (it licenses a read, not a
		// narrowing); other tests leave the type unchanged.
	}
}

// narrowTo refines cur to the target scalar in the positive branch, or removes
// that arm in the negated branch. When cur is any, the positive narrow yields
// the target (the guard proved it); the negative narrow leaves any (we cannot
// subtract from the dynamic top).
func narrowTo(cur, target *Type, negated bool) *Type {
	if negated {
		if cur.isAny() {
			return cur
		}
		return subtractArm(cur, target)
	}
	if cur.isAny() {
		return target
	}
	// Keep only the arms consistent with the target.
	var kept []*Type
	for _, a := range cur.arms() {
		if equalType(a, target) {
			kept = append(kept, a)
		}
	}
	if len(kept) == 0 {
		// The guard proves the target even if the static type did not list it.
		return target
	}
	return unionOf(kept)
}

// narrowToCollection refines cur to a collection in the positive branch. When
// cur is any it yields the dynamic collection shape; otherwise it keeps only the
// list/map arms.
func narrowToCollection(cur, fallback *Type) *Type {
	if cur.isAny() {
		return fallback
	}
	var kept []*Type
	for _, a := range cur.arms() {
		if a.Kind == KList || a.Kind == KMap {
			kept = append(kept, a)
		}
	}
	if len(kept) == 0 {
		return fallback
	}
	return unionOf(kept)
}

// subtractArm removes every arm equal to target from cur's union.
func subtractArm(cur, target *Type) *Type {
	var kept []*Type
	for _, a := range cur.arms() {
		if !equalType(a, target) {
			kept = append(kept, a)
		}
	}
	return unionOf(kept)
}
