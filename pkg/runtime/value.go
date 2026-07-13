// Package runtime is the root of Quill's value system: the sealed Value
// taxonomy, the ordered dual-view *Array, the Context variable scope, the host
// Object interface, and the typed operations (Equal, Order, Truthy,
// Empty, ToText, GetAttribute, iteration). It imports nothing from the lexer,
// parser, or interpreter, so the engine's correctness budget (comparison,
// truthiness, coercion, attribute access) is spent once here and is testable
// in isolation. See spec 04-types-and-semantics.md.
package runtime

// Kind is the tag of the eight-kind Value taxonomy (spec 04 Section 2.1).
type Kind uint8

const (
	// KNull is the absence value; renders to the empty string.
	KNull Kind = iota
	// KBool is true or false.
	KBool
	// KInt is a signed 64-bit integer.
	KInt
	// KFloat is an IEEE-754 double. Non-finite floats never circulate.
	KFloat
	// KStr is a byte string, possibly invalid UTF-8 (for lossless emission).
	KStr
	// KArray is the ordered, dual-view *Array collection.
	KArray
	// KObject is a host value behind the Object interface.
	KObject
	// KSafe is an already-safe-output carrier.
	KSafe
)

// String returns a stable ASCII label for the kind, used in error messages and
// the type tests (is sequence / is mapping route through ToText-free names).
func (k Kind) String() string {
	switch k {
	case KNull:
		return "null"
	case KBool:
		return "bool"
	case KInt:
		return "int"
	case KFloat:
		return "float"
	case KStr:
		return "string"
	case KArray:
		return "array"
	case KObject:
		return "object"
	case KSafe:
		return "safe"
	default:
		return "unknown"
	}
}

// Value is Quill's tagged-union runtime value. It is a small, copyable struct;
// the only reference-typed payloads are *Array (shared by pointer, value-copied
// at boundaries by callers) and the host Object (an interface). The payload
// fields are unexported so the union stays sealed: read them through Kind and
// the typed AsBool/AsInt/AsFloat/AsStr/AsArray/AsObject accessors (each a plain
// field return the compiler inlines), and use the operation functions rather
// than switching on the tag by hand. A given field is only meaningful for the
// matching Kind; an accessor called on the wrong kind returns that field's zero
// value, so callers must gate on Kind first.
//
// Concurrency: a Value is not safe to share across concurrent renders. A Value
// that wraps an *Array carries copy-on-write state that binding and even
// outer-scope reads mutate in place (ShareValue/Own, and Scope.Get and
// Context.Clone, which share-mark arrays on read), so the same Value, or the
// *Array, Context, or Scope it reaches, races when reached from two goroutines
// at once. Build or FromGo-marshal a fresh Value per render, or confine each
// Value to a single goroutine.
type Value struct {
	kind Kind
	b    bool    // KBool
	i    int64   // KInt
	f    float64 // KFloat
	s    string  // KStr, and the wrapped content when KSafe
	arr  *Array  // KArray
	obj  Object  // KObject
}

// Kind returns the tag identifying which payload of v is live.
func (v Value) Kind() Kind { return v.kind }

// AsBool returns the bool payload; meaningful only when Kind is KBool.
func (v Value) AsBool() bool { return v.b }

// AsInt returns the int64 payload; meaningful only when Kind is KInt.
func (v Value) AsInt() int64 { return v.i }

// AsFloat returns the float64 payload; meaningful only when Kind is KFloat.
func (v Value) AsFloat() float64 { return v.f }

// AsStr returns the string payload; meaningful when Kind is KStr, and also
// carries the wrapped content when Kind is KSafe.
func (v Value) AsStr() string { return v.s }

// AsArray returns the *Array payload; meaningful only when Kind is KArray.
func (v Value) AsArray() *Array { return v.arr }

// AsObject returns the host Object payload; meaningful only when Kind is
// KObject.
func (v Value) AsObject() Object { return v.obj }

// Object is the host value protocol (spec 04 Section 2.1). A host type
// implements the subset it supports; the runtime probes capabilities through
// the optional interfaces below (Stringifier, Counter, Iterable, Indexable,
// Equaler, ClassNamed) rather than requiring every method on every host type.
type Object interface {
	// GetField reads a public field or accessor by name. ok is false when the
	// member is absent, which the strict-undefined policy turns into an error
	// (spec 04 Section 7.2).
	GetField(name string) (Value, bool)
	// CallMethod invokes a method by name with the given arguments (the a.b()
	// form, spec 04 Section 7.2).
	CallMethod(name string, args []Value) (Value, error)
}

// Stringifier is the explicit, auditable ToText hook for a host Object. An
// Object without it is a render error, never an ambient best-effort stringify
// (spec 04 Section 5).
type Stringifier interface {
	Stringify() (string, error)
}

// Counter lets a host Object report a length for the length filter (spec 03
// Section 2.2). It is not consulted by is empty; an Object is never empty (see
// Empty).
type Counter interface {
	Count() int
}

// ClassNamed lets a host Object report its registered type name, used by
// is mapping and Object<"Type"> matching.
type ClassNamed interface {
	ClassName() string
}

// Equaler lets a host type override identity equality with a value equal hook
// (spec 04 Section 4.1).
type Equaler interface {
	Equal(other Value) bool
}

// Indexable lets a host Object answer a[k] subscripts (spec 04 Section 7.3).
type Indexable interface {
	GetIndex(key Value) (Value, bool)
}

// Iterable lets a host Object drive a for loop. The pairs are yielded in the
// host's order; an Object that is not Iterable is non-iterable and a for over
// it errors (spec 04 Section 8.5).
type Iterable interface {
	Iterate() []Pair
}

// Pair is one key/value step of an iteration over an *Array or an Iterable
// Object. The Key is always an Int or Str Value, mirroring the *Array key model.
type Pair struct {
	Key Value
	Val Value
}

// ---- Constructors ----------------------------------------------------------

// Null is the singleton-shaped absence value.
func Null() Value { return Value{kind: KNull} }

// Bool wraps a Go bool.
func Bool(b bool) Value { return Value{kind: KBool, b: b} }

// Int wraps an int64.
func Int(i int64) Value { return Value{kind: KInt, i: i} }

// Float wraps a float64. Callers must reject non-finite floats at the value
// boundary (spec 04 Section 2.1); this constructor does not validate so that
// the boundary check has a single, explicit home (see RejectNonFinite).
//
// Non-finite floats are rejected at the boundaries that lift a host or computed
// float64 into a Float: the JSON/host data bridge and both the interpreter and
// compiled-backend arithmetic operators call RejectNonFinite before constructing
// the Float, so any Float that entered through those boundaries is finite. Float
// itself performs no validation: a caller that hand-constructs Float(NaN) or
// Float(Inf) bypasses that guard, and Equal/Order are not total over such
// non-finite values (Equal(Float(NaN), Float(NaN)) is false). Route a raw host
// float64 through RejectNonFinite before calling Float.
func Float(f float64) Value { return Value{kind: KFloat, f: f} }

// Str wraps a byte string.
func Str(s string) Value { return Value{kind: KStr, s: s} }

// Arr wraps an *Array.
func Arr(a *Array) Value { return Value{kind: KArray, arr: a} }

// Obj wraps a host Object.
func Obj(o Object) Value { return Value{kind: KObject, obj: o} }

// Safe wraps already-safe content. Under escaping-off (the default) it is an
// inert passthrough indistinguishable from Str for compare and render (spec 04
// Section 8.2); the value layer only needs Safe to unwrap under ToText and to
// normalize before compare.
func Safe(s string) Value { return Value{kind: KSafe, s: s} }

// ---- Predicates and small accessors ----------------------------------------

// IsNull reports whether v is the Null value.
func (v Value) IsNull() bool { return v.kind == KNull }

// IsScalar reports whether v is one of the scalar kinds (Null, Bool, Int,
// Float, Str). Used by the gradual checker's shallow boundary cast (spec 04
// Section 1.1): a scalar that crossed the any-boundary is trusted.
func (v Value) IsScalar() bool {
	switch v.kind {
	case KNull, KBool, KInt, KFloat, KStr:
		return true
	default:
		return false
	}
}

// normalizeSafe returns the Str view of a Safe value, leaving every other kind
// unchanged. Equal, Order, membership, and the structural *Array recursion all
// normalize through this so Safe is transparent (spec 04 Section 4.1).
func normalizeSafe(v Value) Value {
	if v.kind == KSafe {
		return Value{kind: KStr, s: v.s}
	}
	return v
}
