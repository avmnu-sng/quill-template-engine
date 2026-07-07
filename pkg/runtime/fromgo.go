package runtime

import (
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
)

// FromGo marshals a native Go value into a runtime.Value so a host can pass
// ordinary Go data -- scalars, slices, maps, structs, and nested combinations
// of them -- to a render without hand-building the value taxonomy. It is the
// host-facing counterpart to the internal JSON bridge: where Decode turns a
// data file into a Value, FromGo turns a live Go value into one.
//
// The mapping mirrors Quill's value model:
//
//   - nil, and a nil pointer/interface/slice/map, become Null;
//   - a bool becomes Bool; a string becomes Str;
//   - every signed and unsigned integer kind becomes Int, with an unsigned
//     value above the int64 ceiling reported as an error rather than silently
//     wrapping;
//   - float32/float64 become Float;
//   - a slice or array becomes a list-shaped *Array with 0-based integer keys,
//     in element order;
//   - a map becomes an *Array with a deterministic key order regardless of Go's
//     randomized map iteration: a string-keyed map sorts by its string keys,
//     while an integer-keyed map sorts numerically by key value, so a dense
//     0..n-1 int map is list-shaped and iterates in ascending order. A canonical
//     decimal-integer key name goes through the one canonical key model (spec 04
//     Section 6.1), exactly as elsewhere;
//   - a struct becomes an *Array mapping its EXPORTED fields, in declaration
//     order, honoring a `quill:"name"` tag and, absent that, a `json:"name"`
//     tag for the emitted key; a field tagged `-` (under either tag) is skipped,
//     and an embedded (anonymous) struct field is flattened in place;
//   - a pointer or interface is followed to its element;
//   - a runtime.Value passes through unchanged, so a host can mix hand-built and
//     native values freely.
//
// An unsupported kind -- a channel, a function that is not a registered
// Callable, or a complex number -- yields a clear KindRuntime error naming the
// offending Go kind, so the failure is a typed *errors.Error the host can
// branch on rather than a silent wrong render.
func FromGo(v any) (Value, error) {
	if v == nil {
		return Null(), nil
	}
	// A runtime.Value (or a *Array / Object payload) passes through so hosts can
	// interleave native data with hand-built values.
	switch rv := v.(type) {
	case Value:
		return rv, nil
	case *Array:
		if rv == nil {
			return Null(), nil
		}
		return Arr(rv), nil
	case Object:
		if rv == nil {
			return Null(), nil
		}
		return Obj(rv), nil
	}
	// The switch above peeled every dynamic type the passthrough probe can
	// match (Value, *Array, and any Object implementation), so the reflect walk
	// starts with its probe already settled as a miss.
	return fromReflectPass(reflect.ValueOf(v), false)
}

// reflectTypeValue, reflectTypeArray, and reflectTypeObject anchor the
// passthrough gate: a member can only pass through as a finished value when its
// static type is one of these (or implements the Object interface).
var (
	reflectTypeValue  = reflect.TypeOf(Value{})
	reflectTypeArray  = reflect.TypeOf((*Array)(nil))
	reflectTypeObject = reflect.TypeOf((*Object)(nil)).Elem()
)

// canPassThrough reports whether a member of static type t can satisfy the
// passthrough probe, so the probe's boxing (reflect's Interface allocates for
// any member wider than a machine word) is paid only by members that can
// actually carry a finished value. An interface type reports false: an
// interface-kind member routes through the reflect.Interface branch, which
// re-enters FromGo on the dynamic value and applies the same passthrough there,
// keeping the guarantee true at every depth. The checks run in the probe's own
// match order (Value, then *Array, then Object).
func canPassThrough(t reflect.Type) bool {
	if t == reflectTypeValue || t == reflectTypeArray {
		return true
	}
	return t.Kind() != reflect.Interface && t.Implements(reflectTypeObject)
}

// fromReflect is the reflect-driven core of FromGo. It classifies rv's static
// type for the passthrough probe and dispatches; the pointer case re-enters
// here so the classification follows the pointee's concrete type at every
// depth.
func fromReflect(rv reflect.Value) (Value, error) {
	if !rv.IsValid() {
		return Null(), nil
	}
	return fromReflectPass(rv, canPassThrough(rv.Type()))
}

// fromReflectPass is fromReflect with the passthrough classification supplied
// by the caller. Bulk callers -- slice elements, map values, planned struct
// fields -- share one static member type, so they classify once and skip the
// per-member probe entirely for types that can never pass through.
//
// A concretely-typed runtime.Value, *Array, or Object member passes through as
// itself, exactly as the FromGo entry point does for a top-level value: a
// struct field declared `V runtime.Value` (or `A *runtime.Array`, or an Object)
// carries a finished value that must reach the render untouched rather than
// being re-marshalled through its reflected fields.
func fromReflectPass(rv reflect.Value, pass bool) (Value, error) {
	if pass && rv.CanInterface() {
		switch iv := rv.Interface().(type) {
		case Value:
			return iv, nil
		case *Array:
			if iv == nil {
				return Null(), nil
			}
			return Arr(iv), nil
		case Object:
			if iv == nil {
				return Null(), nil
			}
			return Obj(iv), nil
		}
	}
	// An interface-typed member may carry a runtime.Value or Object directly;
	// route it back through FromGo so the passthrough applies at every depth.
	if rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return Null(), nil
		}
		return FromGo(rv.Interface())
	}
	switch rv.Kind() {
	case reflect.Ptr:
		if rv.IsNil() {
			return Null(), nil
		}
		return fromReflect(rv.Elem())
	case reflect.Bool:
		return Bool(rv.Bool()), nil
	case reflect.String:
		return Str(rv.String()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return Int(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := rv.Uint()
		if u > math.MaxInt64 {
			return Null(), errors.New(errors.KindRuntime,
				"cannot marshal Go value: unsigned integer %d overflows int64", u)
		}
		return Int(int64(u)), nil
	case reflect.Float32, reflect.Float64:
		return Float(rv.Float()), nil
	case reflect.Slice, reflect.Array:
		return fromSequence(rv)
	case reflect.Map:
		return fromMap(rv)
	case reflect.Struct:
		return fromStruct(rv)
	default:
		return Null(), errors.New(errors.KindRuntime,
			"cannot marshal Go value of kind %s", rv.Kind())
	}
}

// newArraySized returns an empty *Array whose key slice and value map are
// pre-sized for n entries, so a FromGo build of known size skips the append and
// rehash growth steps of an incremental build.
func newArraySized(n int) *Array {
	return &Array{keys: make([]string, 0, n), vals: make(map[string]Value, n)}
}

// fromSequence marshals a Go slice or array into a list-shaped *Array with
// contiguous 0-based integer keys in element order. A nil slice becomes Null,
// matching the nil-pointer treatment.
func fromSequence(rv reflect.Value) (Value, error) {
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return Null(), nil
	}
	n := rv.Len()
	// Every element shares the sequence's one static element type, so the
	// passthrough classification hoists out of the loop.
	pass := canPassThrough(rv.Type().Elem())
	arr := newArraySized(n)
	for i := 0; i < n; i++ {
		elem, err := fromReflectPass(rv.Index(i), pass)
		if err != nil {
			return Null(), err
		}
		arr.SetInt(int64(i), elem)
	}
	return Arr(arr), nil
}

// fromMap marshals a Go map into a string-keyed *Array with a deterministic key
// order, so two renders of the same map produce byte-identical output despite
// Go's randomized map iteration. A string-keyed map sorts by its string keys; an
// integer-keyed map sorts numerically by key value, so a dense 0..n-1 int map
// marshals list-shaped and iterates in ascending order regardless of digit
// width. The map key type must be a string or an integer -- the two kinds that
// have an unambiguous Quill key spelling; any other key type is a clear error.
func fromMap(rv reflect.Value) (Value, error) {
	if rv.IsNil() {
		return Null(), nil
	}
	// intKeyed is true for a map whose key kind is a signed or unsigned integer,
	// in which case entries sort by numeric key value rather than decimal string.
	intKeyed := isIntegerKeyKind(rv.Type().Key().Kind())
	type entry struct {
		key string
		num int64
		val reflect.Value
	}
	entries := make([]entry, 0, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		k := iter.Key()
		ks, err := mapKeyString(k)
		if err != nil {
			return Null(), err
		}
		e := entry{key: ks, val: iter.Value()}
		if intKeyed {
			// mapKeyString already validated the integer key (including the
			// unsigned-overflow guard), so the canonical decimal parses cleanly.
			e.num, _ = strconv.ParseInt(ks, 10, 64)
		}
		entries = append(entries, e)
	}
	if intKeyed {
		sort.Slice(entries, func(i, j int) bool { return entries[i].num < entries[j].num })
	} else {
		sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	}
	// Every value shares the map's one static element type, so the passthrough
	// classification hoists out of the loop.
	pass := canPassThrough(rv.Type().Elem())
	arr := newArraySized(len(entries))
	for _, e := range entries {
		val, err := fromReflectPass(e.val, pass)
		if err != nil {
			return Null(), err
		}
		arr.SetStr(e.key, val)
	}
	return Arr(arr), nil
}

// isIntegerKeyKind reports whether a reflect.Kind is one of the signed or
// unsigned integer kinds a Go map key may use, so an integer-keyed map sorts by
// numeric key value.
func isIntegerKeyKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	default:
		return false
	}
}

// mapKeyString renders a Go map key as the string form SetStr canonicalizes. A
// string key is used verbatim; an integer key uses its decimal spelling (so it
// lands in an Int slot via the canonical key model). Any other key kind is an
// error, because it has no unambiguous Quill key.
func mapKeyString(k reflect.Value) (string, error) {
	// Follow an interface-typed key to its dynamic value.
	for k.Kind() == reflect.Interface {
		if k.IsNil() {
			return "", errors.New(errors.KindKey,
				"cannot marshal Go map: nil interface key")
		}
		k = k.Elem()
	}
	switch k.Kind() {
	case reflect.String:
		return k.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return canonInt(k.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := k.Uint()
		if u > math.MaxInt64 {
			return "", errors.New(errors.KindKey,
				"cannot marshal Go map: unsigned key %d overflows int64", u)
		}
		return canonInt(int64(u)), nil
	default:
		return "", errors.New(errors.KindKey,
			"cannot marshal Go map with key of kind %s; only string and integer keys are supported", k.Kind())
	}
}

// structPlans caches one immutable *structPlan per struct reflect.Type. FromGo
// re-walks the same host row types on every call (fromGoVars runs per render),
// so tag parsing and embedded-field analysis pay once per type rather than per
// value. Plans are write-once and content-deterministic, so concurrent
// first-use builders of the same type publish interchangeable plans and
// LoadOrStore keeps exactly one.
var structPlans sync.Map // reflect.Type -> *structPlan

// structPlan is the precomputed field walk for one struct type: every decision
// that depends only on the type -- tag names, skip markers, the
// embedded-flattening shape, the passthrough classification -- resolved ahead
// of the per-value marshal.
type structPlan struct {
	fields []fieldPlan
}

// fieldPlan is one emission step of a structPlan.
type fieldPlan struct {
	// index locates the field in its immediate struct.
	index int
	// name is the emitted key, resolved through the quill-then-json tag order.
	name string
	// pass is the passthrough classification of the field's static type.
	pass bool
	// flattenType, when non-nil, is the embedded struct type whose fields this
	// field flattens into its parent after ptrDepth pointer dereferences. It is
	// held as a type rather than an eager *structPlan so a recursively embedded
	// type (a struct embedding a pointer to itself) terminates at build time;
	// the sub-plan resolves lazily per value, exactly where the value walk
	// terminates on a nil pointer.
	flattenType reflect.Type
	// ptrDepth counts the dereferences from the field to the embedded struct.
	ptrDepth int
	// nilFallback marks a flatten field that, when its pointer chain is nil,
	// reverts to an ordinary named emission under name; a flatten field that is
	// unexported or tag-skipped emits nothing on a nil chain.
	nilFallback bool
}

// planFor returns the cached plan for struct type t, building and publishing it
// on first use.
func planFor(t reflect.Type) *structPlan {
	if p, ok := structPlans.Load(t); ok {
		return p.(*structPlan)
	}
	p, _ := structPlans.LoadOrStore(t, buildStructPlan(t))
	return p.(*structPlan)
}

// buildStructPlan derives the emission steps for one struct type. The field
// rules match the documented FromGo struct mapping: exported fields in
// declaration order, quill-then-json tag naming, `-` skips, unexported skips,
// and in-place flattening of untagged embedded structs.
func buildStructPlan(t reflect.Type) *structPlan {
	fields := make([]fieldPlan, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		// An embedded field without an explicit tag name flattens when its
		// pointer-peeled type is a struct; a tagged embedded field maps as a
		// named nested member, matching encoding/json. The flatten decision
		// precedes the unexported skip because an embedded field of an
		// unexported struct type still promotes its own exported members.
		if field.Anonymous && !hasExplicitName(field) {
			et := field.Type
			depth := 0
			for et.Kind() == reflect.Ptr {
				et = et.Elem()
				depth++
			}
			if et.Kind() == reflect.Struct {
				fp := fieldPlan{index: i, flattenType: et, ptrDepth: depth}
				if depth > 0 && field.PkgPath == "" {
					// A nil pointer in the embedded chain reverts the field to
					// the ordinary named path, so the fallback name and probe
					// classification resolve here; an unexported or tag-skipped
					// embedded field stays silent on a nil chain.
					if name, skip := structFieldName(field); !skip {
						fp.name = name
						fp.pass = canPassThrough(field.Type)
						fp.nilFallback = true
					}
				}
				fields = append(fields, fp)
				continue
			}
		}
		if field.PkgPath != "" {
			// Unexported (non-embedded) field: no observable member.
			continue
		}
		name, skip := structFieldName(field)
		if skip {
			continue
		}
		fields = append(fields, fieldPlan{index: i, name: name, pass: canPassThrough(field.Type)})
	}
	return &structPlan{fields: fields}
}

// fromStruct marshals a struct into an *Array mapping its exported fields in
// declaration order. The emitted key for a field is, in order of preference,
// the name from a `quill:"..."` tag, then a `json:"..."` tag, then the field's
// Go name. A field tagged `-` under either tag is skipped, an unexported field
// is skipped, and an embedded (anonymous) struct field is flattened so its
// members appear inline.
func fromStruct(rv reflect.Value) (Value, error) {
	p := planFor(rv.Type())
	arr := newArraySized(len(p.fields))
	if err := p.marshalInto(arr, rv); err != nil {
		return Null(), err
	}
	return Arr(arr), nil
}

// marshalInto walks the planned fields of rv into arr, recursing into embedded
// struct plans so their fields flatten in place under the parent.
func (p *structPlan) marshalInto(arr *Array, rv reflect.Value) error {
	for i := range p.fields {
		f := &p.fields[i]
		fv := rv.Field(f.index)
		if f.flattenType != nil {
			ev := fv
			flat := true
			for d := 0; d < f.ptrDepth; d++ {
				if ev.IsNil() {
					flat = false
					break
				}
				ev = ev.Elem()
			}
			if flat {
				if err := planFor(f.flattenType).marshalInto(arr, ev); err != nil {
					return err
				}
				continue
			}
			if !f.nilFallback {
				continue
			}
			// The nil chain falls through to the named emission below with the
			// original field value, which the probe or the pointer walk resolves
			// exactly as it would a plain named field.
		}
		val, err := fromReflectPass(fv, f.pass)
		if err != nil {
			return err
		}
		arr.SetStr(f.name, val)
	}
	return nil
}

// structFieldName resolves the emitted key for a struct field. A `quill` tag
// wins over a `json` tag; within a tag the name is the segment before the first
// comma (so `json:"id,omitempty"` yields "id"). A tag whose name segment is "-"
// marks the field skipped. Absent any name, the Go field name is used.
func structFieldName(field reflect.StructField) (name string, skip bool) {
	if tag, ok := field.Tag.Lookup("quill"); ok {
		n := tagName(tag)
		if n == "-" {
			return "", true
		}
		if n != "" {
			return n, false
		}
	}
	if tag, ok := field.Tag.Lookup("json"); ok {
		n := tagName(tag)
		if n == "-" {
			return "", true
		}
		if n != "" {
			return n, false
		}
	}
	return field.Name, false
}

// hasExplicitName reports whether a field carries a non-empty name in either
// the quill or json tag, so an embedded field with such a tag is treated as a
// named nested member rather than being flattened.
func hasExplicitName(field reflect.StructField) bool {
	for _, key := range []string{"quill", "json"} {
		if tag, ok := field.Tag.Lookup(key); ok {
			if n := tagName(tag); n != "" && n != "-" {
				return true
			}
		}
	}
	return false
}

// tagName returns the name segment of a struct tag value: the text before the
// first comma, trimmed of surrounding spaces.
func tagName(tag string) string {
	if idx := strings.IndexByte(tag, ','); idx >= 0 {
		tag = tag[:idx]
	}
	return strings.TrimSpace(tag)
}
