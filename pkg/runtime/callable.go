package runtime

import "github.com/avmnu-sng/quill-template-engine/pkg/errors"

// Callable is a runtime value that can be invoked with positional arguments. It
// is the protocol the standard-library higher-order filters (map, filter, sort,
// reduce, find) and the membership quantifiers (has some / has every) use to
// apply an arrow function passed by an author, without the ext package having to
// depend on the interpreter. The interpreter wraps an arrow expression in an
// Object that implements Callable, binding the arrow body to its closure scope;
// ext invokes it through Call below.
//
// Invoke receives the arguments already evaluated and positionally ordered; the
// callee binds them to its declared parameters (extra arguments are ignored,
// missing ones take their default or Null). It returns the body's value.
type Callable interface {
	Invoke(args []Value) (Value, error)
}

// Call invokes a callable Value with args. A value is callable when it is an
// Object implementing Callable (an arrow function, or a host callable Object).
// Anything else is a runtime error so a non-callable passed where an arrow is
// expected fails clearly rather than silently no-op'ing.
func Call(fn Value, args []Value) (Value, error) {
	if fn.kind == KObject {
		if c, ok := fn.obj.(Callable); ok {
			return c.Invoke(args)
		}
	}
	return Null(), errors.New(errors.KindRuntime,
		"value of kind %s is not callable; expected an arrow function", fn.kind)
}

// IsCallable reports whether fn can be invoked by Call.
func IsCallable(fn Value) bool {
	if fn.kind != KObject {
		return false
	}
	_, ok := fn.obj.(Callable)
	return ok
}
