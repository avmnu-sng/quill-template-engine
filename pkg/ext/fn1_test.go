package ext

import (
	"context"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// fn1Audited is the audited arity-1 fast-call set: exactly the core filters
// registerCoreFilters/registerStringFilters wire an Fn1 for. The exactness
// test below fails on any drift in either direction, so widening the set is
// a deliberate, reviewed act rather than a side effect of a registration.
var fn1Audited = []string{
	"upper", "lower", "trim", "capitalize", "title",
	"length", "first", "last", "reverse", "keys", "raw",
}

// TestFn1AuditedSetExact pins the Fn1-carrying core registrations to the
// audited list: every audited name has Fn1, and no other registered filter
// does. In particular the sandbox choke-point names (join/replace/split and
// the higher-order collection filters) must stay on the general path.
func TestFn1AuditedSetExact(t *testing.T) {
	s := Core()
	want := map[string]bool{}
	for _, name := range fn1Audited {
		want[name] = true
	}
	for name, f := range s.filters {
		if want[name] && f.Fn1 == nil {
			t.Errorf("audited filter %q lost its Fn1", name)
		}
		if !want[name] && f.Fn1 != nil {
			t.Errorf("filter %q carries Fn1 but is not in the audited set", name)
		}
	}
	for _, name := range fn1Audited {
		if _, ok := s.filters[name]; !ok {
			t.Errorf("audited filter %q is not registered", name)
		}
	}
}

// TestFn1ChokePointsHaveNoFn1 asserts the interpreter's name-keyed sandbox
// choke points resolve to filters without a fast call, so the string-coercion
// gate's argument scan can never be skipped for them.
func TestFn1ChokePointsHaveNoFn1(t *testing.T) {
	s := Core()
	for _, name := range []string{"join", "replace", "split", "map", "filter", "sort", "reduce", "find"} {
		f, ok := s.Filter(name)
		if !ok {
			t.Fatalf("choke-point filter %q is not registered", name)
		}
		if f.Fn1 != nil {
			t.Errorf("choke-point filter %q carries Fn1", name)
		}
	}
}

// TestAddFilterFast1PanicsOnChokePoint asserts the registration-time guard: a
// core registration that puts Fn1 on a sandbox choke-point name must panic
// instead of silently opening a gate bypass.
func TestAddFilterFast1PanicsOnChokePoint(t *testing.T) {
	fn1 := func(ctx context.Context, v runtime.Value) (runtime.Value, error) { return v, nil }
	fn := func(ctx context.Context, args []runtime.Value) (runtime.Value, error) { return arg(args, 0), nil }

	func() {
		defer func() {
			if recover() == nil {
				t.Error("addFilterFast1 accepted Fn1 on choke-point name join")
			}
		}()
		addFilterFast1(NewSet(), &Filter{Name: "join", Fn: fn, Fn1: fn1})
	}()

	// A choke-point name WITHOUT Fn1 and a non-choke name WITH Fn1 both
	// register fine; the guard keys on the combination only.
	s := NewSet()
	addFilterFast1(s, &Filter{Name: "sort", Fn: fn})
	addFilterFast1(s, &Filter{Name: "upper", Fn: fn, Fn1: fn1})
	if !s.HasFilter("sort") || !s.HasFilter("upper") {
		t.Error("legitimate registrations did not land")
	}
}

// fn1Battery is the adversarial value set the equivalence and immutability
// tests run every audited filter over: every scalar kind, empty and non-empty
// strings, safe strings, lists, maps, nesting, and a coercion-hostile object.
func fn1Battery() []runtime.Value {
	list := runtime.NewList(runtime.Str("b"), runtime.Str("a"), runtime.Str("c"))
	mp := runtime.NewArray()
	mp.SetStr("k2", runtime.Int(2))
	mp.SetStr("k1", runtime.Int(1))
	nested := runtime.NewList(
		runtime.Arr(runtime.NewList(runtime.Int(1))),
		runtime.Arr(runtime.NewList(runtime.Int(2), runtime.Int(3))),
	)
	return []runtime.Value{
		runtime.Null(),
		runtime.Bool(true),
		runtime.Bool(false),
		runtime.Int(42),
		runtime.Int(-7),
		runtime.Float(3.5),
		runtime.Str(""),
		runtime.Str("hello World"),
		runtime.Str(" \tpadded\n "),
		runtime.Str("caf\u00e9 ol\u00e9"),
		runtime.Safe("<b>safe</b>"),
		runtime.Arr(runtime.NewArray()),
		runtime.Arr(list),
		runtime.Arr(mp),
		runtime.Arr(nested),
		runtime.Obj(&fn1Opaque{}),
	}
}

// fn1Opaque is a host object with no Stringify hook, so ToText-based filters
// error on it -- exercising the error arm of the Fn/Fn1 equivalence.
type fn1Opaque struct{}

// GetField exposes no fields, returning (null, false).
func (*fn1Opaque) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }

// CallMethod exposes no methods; the tests never dispatch on it.
func (*fn1Opaque) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), nil
}

// valuesDeepEqual compares two values structurally: kind, scalar payload, and
// for arrays the full ordered key/value pair list, recursively. Objects
// compare by identity, matching how the engine treats host handles.
func valuesDeepEqual(a, b runtime.Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case runtime.KArray:
		if (a.AsArray() == nil) != (b.AsArray() == nil) {
			return false
		}
		if a.AsArray() == nil {
			return true
		}
		ap, bp := a.AsArray().Pairs(), b.AsArray().Pairs()
		if len(ap) != len(bp) {
			return false
		}
		for i := range ap {
			if !valuesDeepEqual(ap[i].Key, bp[i].Key) || !valuesDeepEqual(ap[i].Val, bp[i].Val) {
				return false
			}
		}
		return true
	case runtime.KObject:
		return a.AsObject() == b.AsObject()
	default:
		return a.AsInt() == b.AsInt() && a.AsFloat() == b.AsFloat() && a.AsStr() == b.AsStr() && a.AsBool() == b.AsBool()
	}
}

// deepCopyValue snapshots a value so post-call mutation checks compare
// against the pre-call state rather than shared storage.
func deepCopyValue(v runtime.Value) runtime.Value {
	if v.Kind() != runtime.KArray || v.AsArray() == nil {
		return v
	}
	cp := runtime.NewArray()
	for _, p := range v.AsArray().Pairs() {
		cp.SetKey(p.Key, deepCopyValue(p.Val))
	}
	return runtime.Arr(cp)
}

// TestFn1MatchesFnOnBattery asserts the two dispatch routes of every audited
// filter are observably identical: for each battery value, Fn1(v) and
// Fn([]Value{v}) return the same value (structurally) and the same error
// text. The engine picks between the routes by arity proof alone, so any
// divergence here would be user-visible nondeterminism.
func TestFn1MatchesFnOnBattery(t *testing.T) {
	s := Core()
	for _, name := range fn1Audited {
		f, ok := s.Filter(name)
		if !ok || f.Fn1 == nil {
			t.Fatalf("audited filter %q missing or without Fn1", name)
		}
		for i, v := range fn1Battery() {
			gotFast, errFast := f.Fn1(context.Background(), deepCopyValue(v))
			gotSlow, errSlow := f.Fn(context.Background(), []runtime.Value{deepCopyValue(v)})
			if (errFast == nil) != (errSlow == nil) {
				t.Errorf("%s[%d]: error mismatch: fast=%v slow=%v", name, i, errFast, errSlow)
				continue
			}
			if errFast != nil {
				if errFast.Error() != errSlow.Error() {
					t.Errorf("%s[%d]: error text mismatch: fast=%q slow=%q", name, i, errFast, errSlow)
				}
				continue
			}
			if !valuesDeepEqual(gotFast, gotSlow) {
				t.Errorf("%s[%d]: value mismatch: fast=%v slow=%v", name, i, gotFast, gotSlow)
			}
		}
	}
}

// fn1Selecting names the audited filters that SELECT an existing element of
// the input rather than constructing a result: they return the element value
// itself on both dispatch routes, so element-level sharing is expected there
// and is governed by the engine's copy-on-write discipline downstream.
var fn1Selecting = map[string]bool{"first": true, "last": true}

// TestFn1DoesNotAliasOrMutateInput asserts every audited Fn1 honors the
// host-callable immutability contract without an argument slice to hide
// behind: the input value is byte-identical after the call; a CONSTRUCTED
// array result never shares storage with an array input (mutating the result
// leaves the input untouched); and a SELECTING filter aliases exactly the
// element the general path would return, no more and no less.
func TestFn1DoesNotAliasOrMutateInput(t *testing.T) {
	s := Core()
	for _, name := range fn1Audited {
		f, _ := s.Filter(name)
		if f == nil || f.Fn1 == nil {
			t.Fatalf("audited filter %q missing or without Fn1", name)
		}
		for i, v := range fn1Battery() {
			snapshot := deepCopyValue(v)
			res, err := f.Fn1(context.Background(), v)
			if !valuesDeepEqual(v, snapshot) {
				t.Errorf("%s[%d]: Fn1 mutated its input", name, i)
			}
			if err != nil || res.Kind() != runtime.KArray || res.AsArray() == nil {
				continue
			}
			if fn1Selecting[name] {
				resSlow, errSlow := f.Fn(context.Background(), []runtime.Value{v})
				if errSlow != nil || resSlow.Kind() != runtime.KArray || resSlow.AsArray() != res.AsArray() {
					t.Errorf("%s[%d]: fast and general routes select different storage", name, i)
				}
				continue
			}
			if v.Kind() == runtime.KArray && v.AsArray() != nil && res.AsArray() == v.AsArray() {
				t.Errorf("%s[%d]: result aliases the input array", name, i)
				continue
			}
			res.AsArray().SetStr("fn1-probe", runtime.Int(999))
			if !valuesDeepEqual(v, snapshot) {
				t.Errorf("%s[%d]: mutating the result changed the input", name, i)
			}
		}
	}
}
