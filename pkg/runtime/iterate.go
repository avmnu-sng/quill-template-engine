package runtime

import (
	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
)

// EnsureTraversable returns the key/value Pairs to iterate over in a for loop,
// in insertion order (spec 04 Sections 6, 8.5). Only an *Array and an Iterable
// Object are traversable.
//
// Iterating a Null, a scalar, or any non-iterable is a KindIteration ERROR, NOT
// a silent empty loop, so a missing collection cannot silently emit an empty
// body (spec 04 Section 8.5). The lenient flag (off by default) restores the
// empty-loop behavior; under lenient a non-iterable yields zero pairs and no
// error. The explicit "empty is fine" idiom is for x in (coll ?? []).
func EnsureTraversable(v Value, lenient bool) ([]Pair, error) {
	switch v.kind {
	case KArray:
		if v.arr == nil {
			return nil, nil
		}
		return v.arr.Pairs(), nil
	case KObject:
		if it, ok := v.obj.(Iterable); ok {
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
		"cannot iterate over %s; wrap with ?? [] to allow emptiness", v.kind)
}

// IsSequence reports whether v is list-shaped: a list *Array (an empty *Array
// counts), backing the `is sequence` test (spec 04 Sections 6, 7). Non-arrays
// are not sequences.
func IsSequence(v Value) bool {
	return v.kind == KArray && v.arr != nil && v.arr.IsList()
}

// IsMapping reports whether v is map-shaped: a non-list *Array, or any Object
// (spec 04 Sections 6, 7). An empty *Array is a sequence, not a mapping.
func IsMapping(v Value) bool {
	switch v.kind {
	case KArray:
		return v.arr != nil && !v.arr.IsList()
	case KObject:
		return true
	default:
		return false
	}
}
