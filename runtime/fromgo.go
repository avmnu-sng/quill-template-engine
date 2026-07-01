package runtime

import (
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/errors"
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
//   - a map becomes a string-keyed *Array; the keys are stringified and sorted
//     so the resulting key order is deterministic regardless of Go's randomized
//     map iteration. A canonical decimal-integer key name goes through the one
//     canonical key model (spec 04 Section 6.1), exactly as elsewhere;
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
	return fromReflect(reflect.ValueOf(v))
}

// fromReflect is the reflect-driven core of FromGo. It is factored out so the
// recursive cases (slice elements, map values, struct fields) reuse the exact
// same kind dispatch, including the Value/Object passthrough on interface-typed
// members.
func fromReflect(rv reflect.Value) (Value, error) {
	if !rv.IsValid() {
		return Null(), nil
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

// fromSequence marshals a Go slice or array into a list-shaped *Array with
// contiguous 0-based integer keys in element order. A nil slice becomes Null,
// matching the nil-pointer treatment.
func fromSequence(rv reflect.Value) (Value, error) {
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return Null(), nil
	}
	arr := NewArray()
	for i := 0; i < rv.Len(); i++ {
		elem, err := fromReflect(rv.Index(i))
		if err != nil {
			return Null(), err
		}
		arr.SetInt(int64(i), elem)
	}
	return Arr(arr), nil
}

// fromMap marshals a Go map into a string-keyed *Array with a deterministic key
// order: the keys are stringified and sorted, so two renders of the same map
// produce byte-identical output despite Go's randomized map iteration. The map
// key type must be a string or an integer -- the two kinds that have an
// unambiguous Quill key spelling; any other key type is a clear error.
func fromMap(rv reflect.Value) (Value, error) {
	if rv.IsNil() {
		return Null(), nil
	}
	type entry struct {
		key string
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
		entries = append(entries, entry{key: ks, val: iter.Value()})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	arr := NewArray()
	for _, e := range entries {
		val, err := fromReflect(e.val)
		if err != nil {
			return Null(), err
		}
		arr.SetStr(e.key, val)
	}
	return Arr(arr), nil
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

// fromStruct marshals a struct into an *Array mapping its exported fields in
// declaration order. The emitted key for a field is, in order of preference,
// the name from a `quill:"..."` tag, then a `json:"..."` tag, then the field's
// Go name. A field tagged `-` under either tag is skipped, an unexported field
// is skipped, and an embedded (anonymous) struct field is flattened so its
// members appear inline.
func fromStruct(rv reflect.Value) (Value, error) {
	arr := NewArray()
	if err := marshalStructInto(arr, rv); err != nil {
		return Null(), err
	}
	return Arr(arr), nil
}

// marshalStructInto walks a struct's fields into arr, recursing into embedded
// anonymous structs so their fields flatten in place under the parent.
func marshalStructInto(arr *Array, rv reflect.Value) error {
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fv := rv.Field(i)
		// Flatten an embedded anonymous struct (or pointer-to-struct) only when
		// it carries no explicit name tag; a tagged embedded field maps as a
		// nested member under its tag name, matching encoding/json. This is
		// checked before the unexported skip below because an embedded field of
		// an unexported struct type still promotes its own exported members.
		if field.Anonymous && !hasExplicitName(field) {
			ev := fv
			for ev.Kind() == reflect.Ptr {
				if ev.IsNil() {
					ev = reflect.Value{}
					break
				}
				ev = ev.Elem()
			}
			if ev.IsValid() && ev.Kind() == reflect.Struct {
				if err := marshalStructInto(arr, ev); err != nil {
					return err
				}
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
		val, err := fromReflect(fv)
		if err != nil {
			return err
		}
		arr.SetStr(name, val)
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
