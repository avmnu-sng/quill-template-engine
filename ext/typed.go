package ext

import (
	"reflect"

	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// This file is the typed-registration sugar: NewFilter/NewFunction/NewTest take
// a natural Go func and wrap it in the []runtime.Value-based Fn the engine calls.
// The reflection that inspects the func's signature runs ONCE, here, at
// registration time -- off the render hot path. The wrapper the render loop
// invokes marshals values through pre-resolved converters and does no further
// reflection. The built-in stdlib callables stay hand-written and reflection-free;
// these helpers are an ergonomic alternative to the &Filter{Name, Fn} struct form,
// which remains available for full control.

// runtimeValueType is reflect.Type of runtime.Value, used to pass a Value
// through a natural func unchanged (the passthrough case).
var runtimeValueType = reflect.TypeOf(runtime.Value{})

// errorType is reflect.Type of the error interface, used to detect an optional
// trailing error return.
var errorType = reflect.TypeOf((*error)(nil)).Elem()

// NewFilter wraps a natural Go func as a Filter. fn must be a function whose
// parameters and results marshal between runtime.Value and Go types (see
// marshalIn/marshalOut): string<->Str, int/int64<->Int, float64<->Float,
// bool<->Bool, []T<->*Array, and runtime.Value passthrough. A variadic final
// parameter is supported, as is an optional trailing error result. Arity or
// argument-type mismatches surface as a clear typed error when the filter is
// called. NewFilter panics if fn is not a supported func shape, since that is a
// registration-time programming error, not a template fault.
func NewFilter(name string, fn any) *Filter {
	call := build(name, "filter", fn)
	return &Filter{Name: name, Fn: call}
}

// NewFunction wraps a natural Go func as a Function, with the same marshaling
// rules and registration-time validation as NewFilter.
func NewFunction(name string, fn any) *Function {
	call := build(name, "function", fn)
	return &Function{Name: name, Fn: call}
}

// NewTest wraps a natural Go func as a Test. The func must return a single bool,
// optionally followed by an error. Its parameters marshal exactly as for
// NewFilter. NewTest panics if fn does not return a leading bool.
func NewTest(name string, fn any) *Test {
	call := build(name, "test", fn)
	return &Test{
		Fn: func(args []runtime.Value) (bool, error) {
			out, err := call(args)
			if err != nil {
				return false, err
			}
			return out.Kind == runtime.KBool && out.B, nil
		},
		Name: name,
	}
}

// signature holds the once-resolved shape of a wrapped func: the input
// converters (one per non-variadic parameter, plus the element converter for a
// variadic tail), and how the results marshal back to a Value.
type signature struct {
	name    string
	kind    string // "filter" | "function" | "test", for error text
	fnVal   reflect.Value
	inConvs []func(runtime.Value) (reflect.Value, error)
	// variadic, when non-nil, converts each trailing argument to the variadic
	// element type. When set, inConvs covers the fixed leading parameters only.
	variadic func(runtime.Value) (reflect.Value, error)
	varElem  reflect.Type
	// resultConv marshals the leading (non-error) result back to a Value. It is
	// nil when the func returns only an error, or only nothing; callers treat a
	// nil resultConv as "the result is Null".
	resultConv func(reflect.Value) (runtime.Value, error)
	returnsErr bool
}

// build validates fn at registration time and returns the []runtime.Value-based
// closure the engine calls. It panics on an unsupported func shape.
func build(name, kind string, fn any) func([]runtime.Value) (runtime.Value, error) {
	fv := reflect.ValueOf(fn)
	ft := fv.Type()
	if ft.Kind() != reflect.Func {
		panic("ext: " + kind + " " + name + ": registration value is not a func")
	}

	sig := &signature{name: name, kind: kind, fnVal: fv}

	numIn := ft.NumIn()
	if ft.IsVariadic() {
		fixed := numIn - 1
		sig.inConvs = make([]func(runtime.Value) (reflect.Value, error), fixed)
		for i := 0; i < fixed; i++ {
			sig.inConvs[i] = inConverter(name, kind, ft.In(i))
		}
		sig.varElem = ft.In(numIn - 1).Elem()
		sig.variadic = inConverter(name, kind, sig.varElem)
	} else {
		sig.inConvs = make([]func(runtime.Value) (reflect.Value, error), numIn)
		for i := 0; i < numIn; i++ {
			sig.inConvs[i] = inConverter(name, kind, ft.In(i))
		}
	}

	buildResults(sig, ft, kind)

	return func(args []runtime.Value) (runtime.Value, error) { return sig.invoke(args) }
}

// buildResults validates the result shape and resolves the result converter.
func buildResults(sig *signature, ft reflect.Type, kind string) {
	numOut := ft.NumOut()
	switch numOut {
	case 0:
		// void func: result is Null, no error return.
	case 1:
		if ft.Out(0) == errorType {
			sig.returnsErr = true
		} else {
			sig.resultConv = outConverter(sig.name, kind, ft.Out(0))
		}
	case 2:
		if ft.Out(1) != errorType {
			panic("ext: " + kind + " " + sig.name + ": second result must be error")
		}
		sig.resultConv = outConverter(sig.name, kind, ft.Out(0))
		sig.returnsErr = true
	default:
		panic("ext: " + kind + " " + sig.name + ": func returns too many results")
	}
	if kind == "test" {
		if sig.resultConv == nil || ft.Out(0).Kind() != reflect.Bool {
			panic("ext: test " + sig.name + ": func must return a leading bool")
		}
	}
}

// invoke marshals the incoming values, calls the wrapped func, and marshals the
// result back. Arity and argument-type mismatches return a typed error.
func (sig *signature) invoke(args []runtime.Value) (runtime.Value, error) {
	fixed := len(sig.inConvs)
	if sig.variadic != nil {
		if len(args) < fixed {
			return runtime.Null(), sig.arityErr(len(args))
		}
	} else if len(args) != fixed {
		return runtime.Null(), sig.arityErr(len(args))
	}

	in := make([]reflect.Value, 0, len(args))
	for i, conv := range sig.inConvs {
		rv, err := conv(args[i])
		if err != nil {
			return runtime.Null(), sig.argErr(i, err)
		}
		in = append(in, rv)
	}
	if sig.variadic != nil {
		for i := fixed; i < len(args); i++ {
			rv, err := sig.variadic(args[i])
			if err != nil {
				return runtime.Null(), sig.argErr(i, err)
			}
			in = append(in, rv)
		}
	}

	out := sig.fnVal.Call(in)

	if sig.returnsErr {
		last := out[len(out)-1]
		if !last.IsNil() {
			return runtime.Null(), last.Interface().(error)
		}
	}
	if sig.resultConv == nil {
		return runtime.Null(), nil
	}
	return sig.resultConv(out[0])
}

func (sig *signature) arityErr(got int) error {
	want := len(sig.inConvs)
	if sig.variadic != nil {
		return errors.New(errors.KindRuntime,
			"%s %s: expected at least %d argument(s), got %d", sig.kind, sig.name, want, got)
	}
	return errors.New(errors.KindRuntime,
		"%s %s: expected %d argument(s), got %d", sig.kind, sig.name, want, got)
}

func (sig *signature) argErr(i int, err error) error {
	return errors.New(errors.KindRuntime,
		"%s %s: argument %d: %s", sig.kind, sig.name, i+1, err.Error())
}

// inConverter resolves the once-per-registration converter from a runtime.Value
// to a Go value of type t. It panics on an unsupported parameter type.
func inConverter(name, kind string, t reflect.Type) func(runtime.Value) (reflect.Value, error) {
	if t == runtimeValueType {
		return func(v runtime.Value) (reflect.Value, error) { return reflect.ValueOf(v), nil }
	}
	switch t.Kind() {
	case reflect.String:
		return func(v runtime.Value) (reflect.Value, error) {
			if v.Kind != runtime.KStr && v.Kind != runtime.KSafe {
				return reflect.Value{}, typeErr("string", v)
			}
			return reflect.ValueOf(v.S).Convert(t), nil
		}
	case reflect.Bool:
		return func(v runtime.Value) (reflect.Value, error) {
			if v.Kind != runtime.KBool {
				return reflect.Value{}, typeErr("bool", v)
			}
			return reflect.ValueOf(v.B).Convert(t), nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(v runtime.Value) (reflect.Value, error) {
			if v.Kind != runtime.KInt {
				return reflect.Value{}, typeErr("int", v)
			}
			return reflect.ValueOf(v.I).Convert(t), nil
		}
	case reflect.Float32, reflect.Float64:
		return func(v runtime.Value) (reflect.Value, error) {
			switch v.Kind {
			case runtime.KFloat:
				return reflect.ValueOf(v.F).Convert(t), nil
			case runtime.KInt:
				return reflect.ValueOf(float64(v.I)).Convert(t), nil
			default:
				return reflect.Value{}, typeErr("float", v)
			}
		}
	case reflect.Slice:
		elemConv := inConverter(name, kind, t.Elem())
		return func(v runtime.Value) (reflect.Value, error) {
			if v.Kind != runtime.KArray || v.Arr == nil {
				return reflect.Value{}, typeErr("array", v)
			}
			pairs := v.Arr.Pairs()
			out := reflect.MakeSlice(t, len(pairs), len(pairs))
			for i, p := range pairs {
				ev, err := elemConv(p.Val)
				if err != nil {
					return reflect.Value{}, err
				}
				out.Index(i).Set(ev)
			}
			return out, nil
		}
	default:
		panic("ext: " + kind + " " + name + ": unsupported parameter type " + t.String())
	}
}

// outConverter resolves the once-per-registration converter from a Go result of
// type t back to a runtime.Value. It panics on an unsupported result type.
func outConverter(name, kind string, t reflect.Type) func(reflect.Value) (runtime.Value, error) {
	if t == runtimeValueType {
		return func(rv reflect.Value) (runtime.Value, error) { return rv.Interface().(runtime.Value), nil }
	}
	switch t.Kind() {
	case reflect.String:
		return func(rv reflect.Value) (runtime.Value, error) { return runtime.Str(rv.String()), nil }
	case reflect.Bool:
		return func(rv reflect.Value) (runtime.Value, error) { return runtime.Bool(rv.Bool()), nil }
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(rv reflect.Value) (runtime.Value, error) { return runtime.Int(rv.Int()), nil }
	case reflect.Float32, reflect.Float64:
		return func(rv reflect.Value) (runtime.Value, error) { return runtime.Float(rv.Float()), nil }
	case reflect.Slice:
		elemConv := outConverter(name, kind, t.Elem())
		return func(rv reflect.Value) (runtime.Value, error) {
			arr := runtime.NewArray()
			for i := 0; i < rv.Len(); i++ {
				ev, err := elemConv(rv.Index(i))
				if err != nil {
					return runtime.Null(), err
				}
				arr.SetInt(int64(i), ev)
			}
			return runtime.Arr(arr), nil
		}
	default:
		panic("ext: " + kind + " " + name + ": unsupported result type " + t.String())
	}
}

// typeErr builds the argument-type mismatch error a converter reports at call
// time, naming the wanted Go type and the actual runtime kind.
func typeErr(want string, got runtime.Value) error {
	return errors.New(errors.KindRuntime, "expected %s, got %s", want, got.Kind.String())
}
