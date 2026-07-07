package quill

// The pre-plan FromGo marshaler, kept verbatim as a test-only oracle. The lean
// marshaler in runtime/fromgo.go (type-gated passthrough probe, cached struct
// plans, pre-sized arrays) must agree with this per-value reflect walk on every
// marshaled tree AND on every error, byte for byte -- the differential suite in
// fromgo_differential_test.go drives both over a wide type matrix, randomized
// shapes, and the conformance corpus's variables.
//
// The only mechanical differences from the historical source are the oracle
// name prefix and the use of the exported runtime API (strconv.FormatInt stands
// in for the unexported canonInt; the produced key strings are identical).

import (
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// oracleFromGo mirrors runtime.FromGo's entry switch.
func oracleFromGo(v any) (runtime.Value, error) {
	if v == nil {
		return runtime.Null(), nil
	}
	switch rv := v.(type) {
	case runtime.Value:
		return rv, nil
	case *runtime.Array:
		if rv == nil {
			return runtime.Null(), nil
		}
		return runtime.Arr(rv), nil
	case runtime.Object:
		if rv == nil {
			return runtime.Null(), nil
		}
		return runtime.Obj(rv), nil
	}
	return oracleFromReflect(reflect.ValueOf(v))
}

// oracleFromReflect runs the unconditional boxing probe on every member, then
// dispatches on kind -- the exact shape the lean marshaler replaces.
func oracleFromReflect(rv reflect.Value) (runtime.Value, error) {
	if !rv.IsValid() {
		return runtime.Null(), nil
	}
	if rv.CanInterface() {
		switch iv := rv.Interface().(type) {
		case runtime.Value:
			return iv, nil
		case *runtime.Array:
			if iv == nil {
				return runtime.Null(), nil
			}
			return runtime.Arr(iv), nil
		case runtime.Object:
			if iv == nil {
				return runtime.Null(), nil
			}
			return runtime.Obj(iv), nil
		}
	}
	if rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return runtime.Null(), nil
		}
		return oracleFromGo(rv.Interface())
	}
	switch rv.Kind() {
	case reflect.Ptr:
		if rv.IsNil() {
			return runtime.Null(), nil
		}
		return oracleFromReflect(rv.Elem())
	case reflect.Bool:
		return runtime.Bool(rv.Bool()), nil
	case reflect.String:
		return runtime.Str(rv.String()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return runtime.Int(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := rv.Uint()
		if u > math.MaxInt64 {
			return runtime.Null(), errors.New(errors.KindRuntime,
				"cannot marshal Go value: unsigned integer %d overflows int64", u)
		}
		return runtime.Int(int64(u)), nil
	case reflect.Float32, reflect.Float64:
		return runtime.Float(rv.Float()), nil
	case reflect.Slice, reflect.Array:
		return oracleFromSequence(rv)
	case reflect.Map:
		return oracleFromMap(rv)
	case reflect.Struct:
		return oracleFromStruct(rv)
	default:
		return runtime.Null(), errors.New(errors.KindRuntime,
			"cannot marshal Go value of kind %s", rv.Kind())
	}
}

// oracleFromSequence builds the list-shaped array without pre-sizing.
func oracleFromSequence(rv reflect.Value) (runtime.Value, error) {
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return runtime.Null(), nil
	}
	arr := runtime.NewArray()
	for i := 0; i < rv.Len(); i++ {
		elem, err := oracleFromReflect(rv.Index(i))
		if err != nil {
			return runtime.Null(), err
		}
		arr.SetInt(int64(i), elem)
	}
	return runtime.Arr(arr), nil
}

// oracleFromMap sorts entries deterministically, then builds without pre-sizing.
func oracleFromMap(rv reflect.Value) (runtime.Value, error) {
	if rv.IsNil() {
		return runtime.Null(), nil
	}
	intKeyed := oracleIsIntegerKeyKind(rv.Type().Key().Kind())
	type entry struct {
		key string
		num int64
		val reflect.Value
	}
	entries := make([]entry, 0, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		k := iter.Key()
		ks, err := oracleMapKeyString(k)
		if err != nil {
			return runtime.Null(), err
		}
		e := entry{key: ks, val: iter.Value()}
		if intKeyed {
			e.num, _ = strconv.ParseInt(ks, 10, 64)
		}
		entries = append(entries, e)
	}
	if intKeyed {
		sort.Slice(entries, func(i, j int) bool { return entries[i].num < entries[j].num })
	} else {
		sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	}
	arr := runtime.NewArray()
	for _, e := range entries {
		val, err := oracleFromReflect(e.val)
		if err != nil {
			return runtime.Null(), err
		}
		arr.SetStr(e.key, val)
	}
	return runtime.Arr(arr), nil
}

func oracleIsIntegerKeyKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	default:
		return false
	}
}

func oracleMapKeyString(k reflect.Value) (string, error) {
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
		return strconv.FormatInt(k.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := k.Uint()
		if u > math.MaxInt64 {
			return "", errors.New(errors.KindKey,
				"cannot marshal Go map: unsigned key %d overflows int64", u)
		}
		return strconv.FormatInt(int64(u), 10), nil
	default:
		return "", errors.New(errors.KindKey,
			"cannot marshal Go map with key of kind %s; only string and integer keys are supported", k.Kind())
	}
}

func oracleFromStruct(rv reflect.Value) (runtime.Value, error) {
	arr := runtime.NewArray()
	if err := oracleMarshalStructInto(arr, rv); err != nil {
		return runtime.Null(), err
	}
	return runtime.Arr(arr), nil
}

// oracleMarshalStructInto re-derives every tag and embedding decision per value,
// the per-row cost the cached struct plan amortizes away.
func oracleMarshalStructInto(arr *runtime.Array, rv reflect.Value) error {
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fv := rv.Field(i)
		if field.Anonymous && !oracleHasExplicitName(field) {
			ev := fv
			for ev.Kind() == reflect.Ptr {
				if ev.IsNil() {
					ev = reflect.Value{}
					break
				}
				ev = ev.Elem()
			}
			if ev.IsValid() && ev.Kind() == reflect.Struct {
				if err := oracleMarshalStructInto(arr, ev); err != nil {
					return err
				}
				continue
			}
		}
		if field.PkgPath != "" {
			continue
		}
		name, skip := oracleStructFieldName(field)
		if skip {
			continue
		}
		val, err := oracleFromReflect(fv)
		if err != nil {
			return err
		}
		arr.SetStr(name, val)
	}
	return nil
}

func oracleStructFieldName(field reflect.StructField) (name string, skip bool) {
	if tag, ok := field.Tag.Lookup("quill"); ok {
		n := oracleTagName(tag)
		if n == "-" {
			return "", true
		}
		if n != "" {
			return n, false
		}
	}
	if tag, ok := field.Tag.Lookup("json"); ok {
		n := oracleTagName(tag)
		if n == "-" {
			return "", true
		}
		if n != "" {
			return n, false
		}
	}
	return field.Name, false
}

func oracleHasExplicitName(field reflect.StructField) bool {
	for _, key := range []string{"quill", "json"} {
		if tag, ok := field.Tag.Lookup(key); ok {
			if n := oracleTagName(tag); n != "" && n != "-" {
				return true
			}
		}
	}
	return false
}

func oracleTagName(tag string) string {
	if idx := strings.IndexByte(tag, ','); idx >= 0 {
		tag = tag[:idx]
	}
	return strings.TrimSpace(tag)
}
