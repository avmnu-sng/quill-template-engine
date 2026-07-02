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

	// shared marks an array whose pointer may be reachable from more than one
	// binding or collection slot (the copy-on-write state). A value bound to a
	// name marks its array shared (ShareValue); a shared array is privatized (Own)
	// before an in-place mutation so the write cannot reach any alias, realizing
	// the *Array value-type semantics of spec 04 Section 6.3 lazily. A single-owner
	// array (shared false) mutates in place.
	shared bool
}

// NewArray returns an empty *Array.
func NewArray() *Array {
	return &Array{vals: map[string]Value{}}
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

// smallIntKeyLo / smallIntKeyHi bound the interned canonical key strings. Lists
// and small maps key overwhelmingly on 0..255 (and occasionally -1), so canonInt
// hands back a shared string for those instead of minting a fresh one; the
// strconv small-integer cache already covers 0..99 without allocating, so the
// interning adds coverage for the negative key and 100..255.
const (
	smallIntKeyLo = -1
	smallIntKeyHi = 255
)

var smallIntKeys = func() [smallIntKeyHi - smallIntKeyLo + 1]string {
	var t [smallIntKeyHi - smallIntKeyLo + 1]string
	for k := smallIntKeyLo; k <= smallIntKeyHi; k++ {
		t[k-smallIntKeyLo] = strconv.FormatInt(int64(k), 10)
	}
	return t
}()

// canonInt encodes an integer key, returning an interned string for the common
// small range so a list build and its clones reuse one string per key.
func canonInt(k int64) string {
	if k >= smallIntKeyLo && k <= smallIntKeyHi {
		return smallIntKeys[k-smallIntKeyLo]
	}
	return strconv.FormatInt(k, 10)
}

// isCanonicalIntKey reports whether a stored key encoding is an integer key,
// replacing the former parallel ints map: a key is an Int key exactly when it is
// the canonical decimal spelling of an int64. It is allocation-free -- it never
// calls ParseInt -- by pairing the looksCanonicalInt shape gate with a fits-int64
// bound check (a canonical run of fewer digits than the int64 limit always fits;
// at the limit width it compares lexically). A canonical-looking but overflowing
// run such as "99999999999999999999" is therefore correctly a STRING key, exactly
// as SetStr routes it (spec 04 Section 6.1).
func isCanonicalIntKey(enc string) bool {
	if !looksCanonicalInt(enc) {
		return false
	}
	digits := enc
	bound := "9223372036854775807" // MaxInt64
	if enc[0] == '-' {
		digits = enc[1:]
		bound = "9223372036854775808" // -MinInt64 magnitude
	}
	if len(digits) != len(bound) {
		return len(digits) < len(bound)
	}
	return digits <= bound
}

// fastCanonInt folds a canonical integer key encoding back to its int64 without
// allocating (the encoding was minted by canonInt or accepted by isCanonicalIntKey,
// so it is well-formed and in range). Digits accumulate as a non-positive number
// so the int64 minimum, whose magnitude does not fit as a positive value, is
// representable.
func fastCanonInt(enc string) int64 {
	i := 0
	neg := false
	if enc[0] == '-' {
		neg = true
		i = 1
	}
	var n int64
	for ; i < len(enc); i++ {
		n = n*10 - int64(enc[i]-'0')
	}
	if neg {
		return n
	}
	return -n
}

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
	a.set(canonInt(k), v)
}

// SetStr sets the value at a string subscript, canonicalizing it (so SetStr("1")
// targets the same slot as SetInt(1)).
func (a *Array) SetStr(k string, v Value) {
	enc, _ := canonicalizeStringKey(k)
	a.set(enc, v)
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

func (a *Array) set(enc string, v Value) {
	if _, exists := a.vals[enc]; !exists {
		a.keys = append(a.keys, enc)
	}
	a.vals[enc] = v
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

// keyValue reconstructs the original-kind key Value from a canonical encoding,
// deriving int-ness from the encoding itself (isCanonicalIntKey) rather than a
// stored flag.
func (a *Array) keyValue(enc string) Value {
	if isCanonicalIntKey(enc) {
		return Int(fastCanonInt(enc))
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
		if !isCanonicalIntKey(enc) {
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
	}
	for k, v := range a.vals {
		cp.vals[k] = CopyValue(v)
	}
	return cp
}

// CopyValue value-copies a Value at a boundary. Scalars and Safe copy by value;
// an *Array copies deeply; an Object is shared by reference (host objects are
// reference identities, not value-copied -- spec 04 Section 6.3 covers *Array,
// and Object equality is identity by Section 4.1). It is the eager deep copy,
// retained for callers that need a fully independent tree up front; the render
// path uses the lazy copy-on-write pair ShareValue / Own instead.
func CopyValue(v Value) Value {
	if v.Kind == KArray && v.Arr != nil {
		return Arr(v.Arr.Clone())
	}
	return v
}

// ShareValue marks a KArray value as shared and returns it unchanged; every other
// kind passes through (scalars are values, a host Object keeps reference
// identity). It is the copy-on-write bind primitive: binding a value to a name or
// a collection slot shares the array pointer in O(1) rather than deep-copying it,
// and the first in-place mutation of a shared array privatizes it (Own). Sharing
// each binding independently means two names that come to hold one array both mark
// it shared, so mutating one privatizes and diverges from the other -- the value
// semantics of spec 04 Section 6.3, paid lazily.
func ShareValue(v Value) Value {
	if v.Kind == KArray && v.Arr != nil {
		v.Arr.shared = true
	}
	return v
}

// Own returns a Value whose array the caller may mutate in place without reaching
// any alias: a shared array is replaced by a shallow copy-on-write clone (with its
// shared flag cleared); an unshared array, and every non-array kind, is returned
// unchanged. The bool reports whether a clone was made, so a caller walking an
// assignment path knows to rebind the fresh array under its name or parent slot.
func Own(v Value) (Value, bool) {
	if v.Kind == KArray && v.Arr != nil && v.Arr.shared {
		return Arr(v.Arr.cloneShallowCOW()), true
	}
	return v, false
}

// cloneShallowCOW copies a's key order and value map one level deep and marks
// every nested *Array element shared, so a subsequent mutation one level deeper
// privatizes that child in turn. It is O(len(a)), not O(deep data): the copy-on-
// write cost is paid one path node at a time as writes descend, never as an eager
// whole-tree copy. The returned array is unshared -- the caller owns it.
func (a *Array) cloneShallowCOW() *Array {
	cp := &Array{
		keys: append([]string(nil), a.keys...),
		vals: make(map[string]Value, len(a.vals)),
	}
	for k, v := range a.vals {
		if v.Kind == KArray && v.Arr != nil {
			v.Arr.shared = true
		}
		cp.vals[k] = v
	}
	return cp
}
