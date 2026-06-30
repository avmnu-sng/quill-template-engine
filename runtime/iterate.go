package runtime

import (
	"github.com/avmnusng/quill-template-engine/errors"
)

// EnsureTraversable returns the key/value Pairs to iterate over in a for loop,
// in insertion order (spec 04 Sections 6, 8.5). Only an *Array and an Iterable
// Object are traversable.
//
// Iterating a Null, a scalar, or any non-iterable is a KindIteration ERROR, NOT
// a silent empty loop -- the strict divergence from Twig's ensureTraversable
// (spec 04 Section 8.5). The lenient migration flag (off by default) restores
// the empty-loop behavior; under lenient a non-iterable yields zero pairs and
// no error. The explicit "empty is fine" idiom is for x in (coll ?? []).
func EnsureTraversable(v Value, lenient bool) ([]Pair, error) {
	switch v.Kind {
	case KArray:
		if v.Arr == nil {
			return nil, nil
		}
		return v.Arr.Pairs(), nil
	case KObject:
		if it, ok := v.Obj.(Iterable); ok {
			return it.Iterate(), nil
		}
		return nonIterable(v, lenient)
	default:
		return nonIterable(v, lenient)
	}
}

func nonIterable(v Value, lenient bool) ([]Pair, error) {
	if lenient {
		return nil, nil
	}
	return nil, errors.New(errors.KindIteration,
		"cannot iterate over %s; wrap with ?? [] to allow emptiness", v.Kind)
}

// IsSequence reports whether v is list-shaped: a list *Array (an empty *Array
// counts), backing the `is sequence` test (spec 04 Sections 6, 7). Non-arrays
// are not sequences.
func IsSequence(v Value) bool {
	return v.Kind == KArray && v.Arr != nil && v.Arr.IsList()
}

// IsMapping reports whether v is map-shaped: a non-list *Array, or any Object
// (spec 04 Sections 6, 7). An empty *Array is a sequence, not a mapping.
func IsMapping(v Value) bool {
	switch v.Kind {
	case KArray:
		return v.Arr != nil && !v.Arr.IsList()
	case KObject:
		return true
	default:
		return false
	}
}
