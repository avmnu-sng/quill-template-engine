package runtime

import (
	"strconv"
)

// Array is Quill's one collection: an ordered key slice plus a value map,
// presenting a unified sequence-and-mapping view. Iteration is always in
// insertion order (spec 04 Section 6). Keys are canonicalized so that the
// string "1" and the integer 1 address the same slot, while "01" stays a
// distinct string key (spec 04 Section 6.1).
//
// A key is stored as a Go string under one canonical encoding: an integer key
// k is encoded as the result of strconv.FormatInt(k, 10) and tagged as integer;
// a string key keeps its bytes and is tagged as string. The kind tag is held in
// a parallel map so a Pair can reconstruct the original Int/Str Value during
// iteration without re-parsing.
type Array struct {
	keys []string         // insertion order of canonical key encodings
	vals map[string]Value // canonical key encoding -> value
	ints map[string]bool  // canonical key encoding -> true when the key is an Int
}

// NewArray returns an empty *Array.
func NewArray() *Array {
	return &Array{vals: map[string]Value{}, ints: map[string]bool{}}
}

// NewList builds a list-shaped *Array from values, assigning contiguous integer
// keys starting at 0.
func NewList(vals ...Value) *Array {
	a := NewArray()
	for i, v := range vals {
		a.SetInt(int64(i), v)
	}
	return a
}

// Len returns the number of entries.
func (a *Array) Len() int { return len(a.keys) }

// canonInt encodes an integer key.
func canonInt(k int64) string { return strconv.FormatInt(k, 10) }

// canonicalizeStringKey decides whether a string subscript names an integer
// slot. A canonical decimal-integer literal (matching strconv's round-trip)
// becomes an Int key; everything else -- "01", "1.0", " 1", "+1", "1e3" --
// stays a Str key (spec 04 Section 6.1).
func canonicalizeStringKey(s string) (enc string, isInt bool) {
	if i, ok := parseCanonicalInt(s); ok {
		return canonInt(i), true
	}
	return s, false
}

// parseCanonicalInt reports whether s is the canonical decimal form of an int64
// (the exact bytes strconv.FormatInt would produce). "0" yes; "-3" yes; "01",
// "+1", " 1", "1.0", "1e3", "" no.
//
// The allocation-free looksCanonicalInt gate runs first because this is a hot
// path: every string map subscript flows through here, and calling
// strconv.ParseInt on a non-integer key allocates a discarded *NumError (with a
// cloned copy of the key) every time. Gating it out means only well-formed
// canonical digit strings reach ParseInt, whose success path does not allocate;
// the gate already fixes the spelling, so the prior FormatInt round-trip is
// unnecessary and its allocation is gone too. ParseInt can still fail here only
// when a well-formed digit run overflows int64, and such a subscript correctly
// stays a string key, matching the old round-trip behavior exactly.
func parseCanonicalInt(s string) (int64, bool) {
	if !looksCanonicalInt(s) {
		return 0, false
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return i, true
}

// looksCanonicalInt reports whether s has the exact shape of a canonical decimal
// int64 spelling without allocating: an optional single leading '-', then either
// "0" alone or a nonzero leading digit followed by digits. It rejects every
// non-canonical spelling a subscript must keep as a string key -- "01", "+1",
// "-0", " 1", "1.0", "1e3", "-", "" -- so the string "01" stays distinct from
// the integer 1 (spec 04 Section 6.1).
func looksCanonicalInt(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '-' {
		i = 1
	}
	if i >= len(s) { // a lone "-" is not a number
		return false
	}
	if s[i] == '0' {
		// A canonical zero is exactly "0": no sign, no leading zero, no trailing
		// digits ("-0", "00", "01" are all non-canonical).
		return len(s) == 1
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// SetInt sets the value at an integer key.
func (a *Array) SetInt(k int64, v Value) {
	a.set(canonInt(k), true, v)
}

// SetStr sets the value at a string subscript, canonicalizing it (so SetStr("1")
// targets the same slot as SetInt(1)).
func (a *Array) SetStr(k string, v Value) {
	enc, isInt := canonicalizeStringKey(k)
	a.set(enc, isInt, v)
}

// SetKey sets using a key Value (Int or Str). A non-key kind is a programming
// error here; the access layer rejects bool/float/null subscripts before this
// point (spec 04 Section 6.2), so SetKey treats any non-Int as its Str form.
func (a *Array) SetKey(key, v Value) {
	switch key.Kind {
	case KInt:
		a.SetInt(key.I, v)
	default:
		a.SetStr(key.S, v)
	}
}

func (a *Array) set(enc string, isInt bool, v Value) {
	if _, exists := a.vals[enc]; !exists {
		a.keys = append(a.keys, enc)
	}
	a.vals[enc] = v
	a.ints[enc] = isInt
}

// GetInt reads the value at an integer key.
func (a *Array) GetInt(k int64) (Value, bool) {
	v, ok := a.vals[canonInt(k)]
	return v, ok
}

// GetStr reads the value at a canonicalized string subscript.
func (a *Array) GetStr(k string) (Value, bool) {
	enc, _ := canonicalizeStringKey(k)
	v, ok := a.vals[enc]
	return v, ok
}

// Get reads using a key Value (Int or Str).
func (a *Array) Get(key Value) (Value, bool) {
	switch key.Kind {
	case KInt:
		return a.GetInt(key.I)
	default:
		return a.GetStr(key.S)
	}
}

// Keys returns the keys as Int/Str Values in insertion order.
func (a *Array) Keys() []Value {
	out := make([]Value, 0, len(a.keys))
	for _, enc := range a.keys {
		out = append(out, a.keyValue(enc))
	}
	return out
}

// keyValue reconstructs the original-kind key Value from a canonical encoding.
func (a *Array) keyValue(enc string) Value {
	if a.ints[enc] {
		i, _ := strconv.ParseInt(enc, 10, 64)
		return Int(i)
	}
	return Str(enc)
}

// Pairs returns the entries as key/value Pairs in insertion order, the form the
// for loop consumes.
func (a *Array) Pairs() []Pair {
	out := make([]Pair, 0, len(a.keys))
	for _, enc := range a.keys {
		out = append(out, Pair{Key: a.keyValue(enc), Val: a.vals[enc]})
	}
	return out
}

// IsList reports whether the array is list-shaped: contiguous integer keys
// 0..n-1 in that exact insertion order. An empty array IS a list (and a
// sequence), per spec 04 Section 6 / 7. This predicate backs is sequence; its
// negation (over a non-empty array) backs is mapping.
func (a *Array) IsList() bool {
	for i, enc := range a.keys {
		if !a.ints[enc] {
			return false
		}
		if enc != canonInt(int64(i)) {
			return false
		}
	}
	return true
}

// Clone returns a deep value-copy of the array: the key order and key kinds are
// duplicated, and each element value is itself value-copied (nested *Array
// values copy recursively). This is the copy-on-write boundary the loop,
// include, and rebind sites use so an array is a value type, not a shared
// reference (spec 04 Section 6.3).
func (a *Array) Clone() *Array {
	cp := &Array{
		keys: append([]string(nil), a.keys...),
		vals: make(map[string]Value, len(a.vals)),
		ints: make(map[string]bool, len(a.ints)),
	}
	for k, v := range a.vals {
		cp.vals[k] = CopyValue(v)
	}
	for k, b := range a.ints {
		cp.ints[k] = b
	}
	return cp
}

// CopyValue value-copies a Value at a boundary. Scalars and Safe copy by value;
// an *Array copies deeply; an Object is shared by reference (host objects are
// reference identities, not value-copied -- spec 04 Section 6.3 covers *Array,
// and Object equality is identity by Section 4.1).
func CopyValue(v Value) Value {
	if v.Kind == KArray && v.Arr != nil {
		return Arr(v.Arr.Clone())
	}
	return v
}
