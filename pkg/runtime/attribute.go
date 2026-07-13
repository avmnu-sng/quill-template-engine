package runtime

import (
	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
)

// AccessKind selects the access operator (spec 04 Section 7): dotted a.b versus
// subscript a[k]. The two are distinct, kind-dispatched rules.
type AccessKind uint8

const (
	// AccessDot is the dotted a.b form: kind-dispatched on the receiver.
	AccessDot AccessKind = iota
	// AccessIndex is the a[k] subscript form.
	AccessIndex
)

// GetAttribute resolves a member of recv, kind-dispatched on recv's kind. It is
// the single entry point for both a.b (AccessDot) and a[k] (AccessIndex). The
// allowAbsent flag threads the "absence allowed" suppression set by ??,
// default, and is defined over the WHOLE left chain (spec 04 Section 8.2); when
// set, a miss yields (Null, nil) instead of a strict-undefined error.
//
// Dotted a.b (spec 04 Section 7.2):
//   - *Array: read string key "name"; no property/method fallthrough.
//   - Object: public field name, then accessor get/is/has (in that precedence,
//     no args), then a class constant, via the host GetField hook.
//   - any other kind: an attribute error (it has no .b member).
//
// Subscript a[k] (spec 04 Sections 6.2, 7.3):
//   - the key must be Int or Str; bool/float/null subscripts are KindKey errors.
//   - *Array: by key. Object: through the host index interface.
//
// A miss under strict (allowAbsent false) is a KindUndefined error naming the
// symbol and, for an *Array, listing the available keys.
func GetAttribute(recv, key Value, kind AccessKind, allowAbsent bool) (Value, error) {
	switch kind {
	case AccessDot:
		return getDot(recv, key, allowAbsent)
	case AccessIndex:
		return getIndex(recv, key, allowAbsent)
	default:
		return Null(), errors.New(errors.KindAttribute, "unknown access kind")
	}
}

func getDot(recv, key Value, allowAbsent bool) (Value, error) {
	name := key.s // dotted access always names a string member
	switch recv.kind {
	case KArray:
		// A nil *Array is a valid empty collection everywhere else in the
		// runtime (Truthy, Empty, In, arrayEqual all guard Arr == nil); treat
		// it as an empty array here so a benign empty value never panics.
		if recv.arr == nil {
			return absent(allowAbsent, errors.New(errors.KindUndefined,
				"no key %q (available keys: %s)", name, keyList(recv.arr)))
		}
		if v, ok := recv.arr.GetStr(name); ok {
			return v, nil
		}
		return absent(allowAbsent, errors.New(errors.KindUndefined,
			"no key %q (available keys: %s)", name, keyList(recv.arr)))
	case KObject:
		if v, ok := recv.obj.GetField(name); ok {
			return v, nil
		}
		return absent(allowAbsent, errors.New(errors.KindUndefined,
			"no member %q on object %s", name, objectClass(recv.obj)))
	case KSafe:
		// A Safe normalizes to Str for member access, which has no members.
		return Null(), errors.New(errors.KindAttribute,
			"cannot read member %q of a string", name)
	default:
		return Null(), errors.New(errors.KindAttribute,
			"cannot read member %q of %s", name, recv.kind)
	}
}

func getIndex(recv, key Value, allowAbsent bool) (Value, error) {
	// Only Int and Str keys may subscript; bool/float/null are type errors
	// (spec 04 Section 6.2), regardless of suppression.
	switch key.kind {
	case KInt, KStr:
		// ok
	default:
		return Null(), errors.New(errors.KindKey,
			"cannot subscript with a %s key", key.kind)
	}

	switch recv.kind {
	case KArray:
		// A nil *Array is a valid empty collection (mirrors the Arr == nil
		// guards in truthy.go/iterate.go/compare.go); never dereference it.
		if recv.arr == nil {
			return absent(allowAbsent, errors.New(errors.KindUndefined,
				"no key %s (available keys: %s)", keyText(key), keyList(recv.arr)))
		}
		if v, ok := recv.arr.Get(key); ok {
			return v, nil
		}
		return absent(allowAbsent, errors.New(errors.KindUndefined,
			"no key %s (available keys: %s)", keyText(key), keyList(recv.arr)))
	case KObject:
		if ix, ok := recv.obj.(Indexable); ok {
			if v, found := ix.GetIndex(key); found {
				return v, nil
			}
			return absent(allowAbsent, errors.New(errors.KindUndefined,
				"no index %s on object %s", keyText(key), objectClass(recv.obj)))
		}
		return Null(), errors.New(errors.KindAttribute,
			"object %s does not support subscripting", objectClass(recv.obj))
	default:
		return Null(), errors.New(errors.KindAttribute,
			"cannot subscript %s", recv.kind)
	}
}

// absent applies the suppression rule: under allowAbsent a miss is Null with no
// error; otherwise it is the supplied strict-undefined error.
func absent(allowAbsent bool, err error) (Value, error) {
	if allowAbsent {
		return Null(), nil
	}
	return Null(), err
}

// keyText renders a key Value for an error message.
func keyText(key Value) string {
	s, err := ToText(key)
	if err != nil {
		return key.kind.String()
	}
	if key.kind == KStr {
		return "\"" + s + "\""
	}
	return s
}

// keyList renders an *Array's keys as a comma-separated list for the
// "available keys" hint in a strict-undefined error.
func keyList(a *Array) string {
	if a == nil || a.Len() == 0 {
		return "(none)"
	}
	out := ""
	for i, k := range a.Keys() {
		if i > 0 {
			out += ", "
		}
		out += keyText(k)
	}
	return out
}

// FieldSetter lets a host Object accept an assignment to one of its members, the
// write side of GetField (spec 04 Section 7.2). It backs the mutable-reference
// values (a cell): @set c.value = expr routes through SetMember to this hook. An
// Object without it is immutable, so a member assignment against it is a runtime
// error rather than a silent no-op.
type FieldSetter interface {
	SetField(name string, v Value) error
}

// SetMember assigns v to the named member of recv, the write counterpart of
// GetAttribute's dotted read. It is the single entry point behind a member set
// target (@set recv.name = v). An *Array stores the string key; a host Object
// must implement FieldSetter, otherwise the assignment is a runtime error naming
// the immutable receiver. A non-collection receiver has no members to assign.
func SetMember(recv Value, name string, v Value) error {
	switch recv.kind {
	case KArray:
		if recv.arr == nil {
			return errors.New(errors.KindRuntime,
				"cannot assign member %q of an empty collection", name)
		}
		recv.arr.SetStr(name, v)
		return nil
	case KObject:
		if fs, ok := recv.obj.(FieldSetter); ok {
			return fs.SetField(name, v)
		}
		return errors.New(errors.KindRuntime,
			"object %s does not support member assignment", objectClass(recv.obj))
	default:
		return errors.New(errors.KindRuntime,
			"cannot assign member %q of %s", name, recv.kind)
	}
}

// IndexSetter lets a host Object accept an a[k] = v subscript assignment, the
// write side of Indexable (spec 04 Section 7.3). An Object without it does not
// support subscript assignment, so such an assignment is a runtime error.
type IndexSetter interface {
	SetIndex(key, v Value) error
}

// SetIndex assigns v to the subscript key of recv, the write counterpart of the
// a[k] read. The key must be Int or Str, matching the read side. An *Array stores
// the key; a host Object must implement IndexSetter. It backs a subscript set
// target (@set recv[key] = v).
func SetIndex(recv, key, v Value) error {
	switch key.kind {
	case KInt, KStr:
		// ok
	default:
		return errors.New(errors.KindKey, "cannot subscript with a %s key", key.kind)
	}
	switch recv.kind {
	case KArray:
		if recv.arr == nil {
			return errors.New(errors.KindRuntime,
				"cannot assign into an empty collection")
		}
		recv.arr.SetKey(key, v)
		return nil
	case KObject:
		if is, ok := recv.obj.(IndexSetter); ok {
			return is.SetIndex(key, v)
		}
		return errors.New(errors.KindRuntime,
			"object %s does not support subscript assignment", objectClass(recv.obj))
	default:
		return errors.New(errors.KindRuntime, "cannot subscript-assign %s", recv.kind)
	}
}

// IsDefinedAttribute is the access-chain side of the is defined test: it reports
// presence of a member without ever throwing (spec 04 Section 8.3). It is true
// for a present key even when its stored value is Null.
func IsDefinedAttribute(recv, key Value, kind AccessKind) bool {
	switch recv.kind {
	case KArray:
		// A nil *Array holds no members, so every presence test is false
		// without dereferencing (mirrors the Arr == nil guards elsewhere).
		if recv.arr == nil {
			return false
		}
		if kind == AccessDot {
			_, ok := recv.arr.GetStr(key.s)
			return ok
		}
		if key.kind != KInt && key.kind != KStr {
			return false
		}
		_, ok := recv.arr.Get(key)
		return ok
	case KObject:
		if kind == AccessDot {
			_, ok := recv.obj.GetField(key.s)
			return ok
		}
		if ix, ok := recv.obj.(Indexable); ok {
			_, found := ix.GetIndex(key)
			return found
		}
		return false
	default:
		return false
	}
}
