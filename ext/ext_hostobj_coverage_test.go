package ext

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// TestDateHostObjectProtocol drives the full host-Object protocol on the value
// date() returns: GetField reports no members, CallMethod always errors,
// Stringify renders the default Go layout, and ClassName is "Date". The value is
// obtained through the public function registry, then probed as a runtime.Object.
func TestDateHostObjectProtocol(t *testing.T) {
	// date(0) is the Unix epoch in UTC -> 1970-01-01 00:00:00 in the default layout.
	d := callFn(t, "date", runtime.Int(0))
	if d.Kind != runtime.KObject {
		t.Fatalf("date() = kind %s, want an object", d.Kind)
	}

	// GetField: a date exposes no attributes, so any name is absent and null.
	got, ok := d.Obj.GetField("year")
	if ok {
		t.Errorf("date GetField(year) ok=true, want false (a date has no fields)")
	}
	if got.Kind != runtime.KNull {
		t.Errorf("date GetField absent value = %s, want null", got.Kind)
	}

	// CallMethod: a date has no callable methods; every invocation is a runtime error.
	res, err := d.Obj.CallMethod("format", []runtime.Value{runtime.Str("x")})
	if err == nil {
		t.Fatalf("date CallMethod should error, got %v", res)
	}
	if !strings.Contains(err.Error(), "a date has no methods") {
		t.Errorf("date CallMethod error = %q, want mention of no methods", err.Error())
	}
	if errors.KindOf(err) != errors.KindRuntime {
		t.Errorf("date CallMethod error kind = %s, want KindRuntime", errors.KindOf(err))
	}
	if res.Kind != runtime.KNull {
		t.Errorf("date CallMethod result = %s, want null", res.Kind)
	}

	// Stringify: reached through the public ToText hook, renders the default layout.
	text, err := runtime.ToText(d)
	if err != nil {
		t.Fatalf("date Stringify via ToText: %v", err)
	}
	if text != "1970-01-01 00:00:00" {
		t.Errorf("date Stringify = %q, want %q", text, "1970-01-01 00:00:00")
	}

	// ClassName: reported through the ClassNamed capability interface.
	cn, ok := d.Obj.(runtime.ClassNamed)
	if !ok {
		t.Fatal("date value should implement runtime.ClassNamed")
	}
	if cn.ClassName() != "Date" {
		t.Errorf("date ClassName = %q, want %q", cn.ClassName(), "Date")
	}
}

// TestSeparatorHostObjectProtocol covers the members-and-methods half of a
// separator's host-Object protocol (its Callable half is covered elsewhere):
// GetField exposes nothing, CallMethod errors, and ClassName is "Separator".
func TestSeparatorHostObjectProtocol(t *testing.T) {
	sep := callFn(t, "separator", runtime.Str("; "))

	// GetField: a separator is used only by calling it, so it has no members.
	got, ok := sep.Obj.GetField("sep")
	if ok || got.Kind != runtime.KNull {
		t.Errorf("separator GetField(sep) = (%s, %v), want (null, false)", got.Kind, ok)
	}

	// CallMethod: a separator is invoked directly, never through a named method.
	res, err := sep.Obj.CallMethod("reset", nil)
	if err == nil {
		t.Fatalf("separator CallMethod should error, got %v", res)
	}
	if !strings.Contains(err.Error(), "a separator has no methods") {
		t.Errorf("separator CallMethod error = %q", err.Error())
	}
	if errors.KindOf(err) != errors.KindRuntime {
		t.Errorf("separator CallMethod error kind = %s, want KindRuntime", errors.KindOf(err))
	}

	// ClassName.
	cn, _ := sep.Obj.(runtime.ClassNamed)
	if cn == nil || cn.ClassName() != "Separator" {
		t.Errorf("separator ClassName = %q, want %q", cn.ClassName(), "Separator")
	}
}

// TestCellHostObjectProtocolMembers covers the parts of the cell host-Object
// protocol not already exercised: the GetField miss path (an absent member
// returns (null, false)), CallMethod (always a runtime error), and ClassName.
func TestCellHostObjectProtocolMembers(t *testing.T) {
	c := callFn(t, "cell", runtime.Str("seed"))

	// GetField hit path: `value` yields the held value.
	got, ok := c.Obj.GetField("value")
	if !ok || got.Kind != runtime.KStr || got.S != "seed" {
		t.Errorf("cell GetField(value) = (%v, %v), want (\"seed\", true)", got, ok)
	}

	// GetField miss path: any other member is absent and null.
	got, ok = c.Obj.GetField("missing")
	if ok || got.Kind != runtime.KNull {
		t.Errorf("cell GetField(missing) = (%s, %v), want (null, false)", got.Kind, ok)
	}

	// CallMethod: a cell is read/written through `value`, never through a method.
	res, err := c.Obj.CallMethod("get", nil)
	if err == nil {
		t.Fatalf("cell CallMethod should error, got %v", res)
	}
	if !strings.Contains(err.Error(), "a cell has no methods") {
		t.Errorf("cell CallMethod error = %q", err.Error())
	}
	if errors.KindOf(err) != errors.KindRuntime {
		t.Errorf("cell CallMethod error kind = %s, want KindRuntime", errors.KindOf(err))
	}

	// ClassName.
	cn, _ := c.Obj.(runtime.ClassNamed)
	if cn == nil || cn.ClassName() != "Cell" {
		t.Errorf("cell ClassName = %q, want %q", cn.ClassName(), "Cell")
	}
}

// TestCellStringifyHeldValueKinds confirms Stringify renders whatever the cell
// currently holds, across a couple of value kinds and after a mutation, so the
// bare {{ c }} spelling tracks the accumulation.
func TestCellStringifyHeldValueKinds(t *testing.T) {
	c := callFn(t, "cell", runtime.Int(7))

	// Held int renders via the Stringifier hook.
	text, err := runtime.ToText(c)
	if err != nil || text != "7" {
		t.Fatalf("cell Stringify(int 7) = %q (err %v), want %q", text, err, "7")
	}

	// After mutating the slot, Stringify tracks the new value.
	if err := runtime.SetMember(c, "value", runtime.Bool(true)); err != nil {
		t.Fatalf("SetMember: %v", err)
	}
	text, err = runtime.ToText(c)
	if err != nil || text != "true" {
		t.Errorf("cell Stringify(after set bool) = %q (err %v), want %q", text, err, "true")
	}
}

// TestRegistryHasFunctionAndHasTest covers HasFunction and HasTest on both the
// present and the absent path, driven through the public registration API.
func TestRegistryHasFunctionAndHasTest(t *testing.T) {
	s := NewExtensionSet()

	// Absent before registration.
	if s.HasFunction("greet") {
		t.Error("HasFunction(greet) = true on empty set, want false")
	}
	if s.HasTest("blank") {
		t.Error("HasTest(blank) = true on empty set, want false")
	}

	s.AddFunction(&Function{Name: "greet", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Str("hi"), nil
	}})
	s.AddTest(&Test{Name: "blank", Fn: func([]runtime.Value) (bool, error) {
		return true, nil
	}})

	// Present after registration.
	if !s.HasFunction("greet") {
		t.Error("HasFunction(greet) = false after AddFunction, want true")
	}
	if !s.HasTest("blank") {
		t.Error("HasTest(blank) = false after AddTest, want true")
	}
	// A function name is not a test name and vice versa (the maps are per-kind).
	if s.HasTest("greet") {
		t.Error("HasTest(greet) = true, want false (greet is a function, not a test)")
	}
	if s.HasFunction("blank") {
		t.Error("HasFunction(blank) = true, want false (blank is a test, not a function)")
	}
}

// TestRegistryCloneIndependence confirms Clone copies every family, shares the
// callable values, and gives the copy independent maps so a later addition to
// one set does not leak into the other.
func TestRegistryCloneIndependence(t *testing.T) {
	base := NewExtensionSet()
	base.AddFilter(&Filter{Name: "f", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Str("base-f"), nil
	}})
	base.AddFunction(&Function{Name: "fn", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Str("base-fn"), nil
	}})
	base.AddTest(&Test{Name: "t", Fn: func([]runtime.Value) (bool, error) { return true, nil }})
	base.AddConstant("K", runtime.Str("base-k"))
	base.AddEnum("E", []runtime.Value{runtime.Str("a"), runtime.Str("b")})

	cp := base.Clone()

	// Every family carried over.
	if !cp.HasFilter("f") || !cp.HasFunction("fn") || !cp.HasTest("t") {
		t.Error("Clone dropped a callable family")
	}
	if v, ok := cp.Constant("K"); !ok || v.S != "base-k" {
		t.Errorf("Clone constant K = %+v ok=%v, want base-k", v, ok)
	}
	if cases, ok := cp.Enum("E"); !ok || len(cases) != 2 || cases[0].S != "a" {
		t.Errorf("Clone enum E = %+v ok=%v", cases, ok)
	}

	// The shared filter value is the same pointer and yields the same result.
	orig, _ := base.Filter("f")
	clone, _ := cp.Filter("f")
	if orig != clone {
		t.Error("Clone should share the callable pointer, not deep-copy it")
	}
	got, _ := clone.Fn(nil)
	if got.S != "base-f" {
		t.Errorf("cloned filter f = %q, want base-f", got.S)
	}

	// Independent maps: adding to the clone does not touch the base, and vice versa.
	// Exercise every one of the five maps Clone copies, not just filters/functions,
	// so a future Clone that forgets to fork one map (constants or enums) is caught.
	cp.AddFilter(&Filter{Name: "clone_only", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Null(), nil
	}})
	if base.HasFilter("clone_only") {
		t.Error("adding a filter to the clone leaked into the base set")
	}
	cp.AddTest(&Test{Name: "clone_t", Fn: func([]runtime.Value) (bool, error) { return true, nil }})
	if base.HasTest("clone_t") {
		t.Error("adding a test to the clone leaked into the base set")
	}
	cp.AddConstant("CLONE_K", runtime.Str("clone-k"))
	if _, ok := base.Constant("CLONE_K"); ok {
		t.Error("adding a constant to the clone leaked into the base set")
	}
	cp.AddEnum("CLONE_E", []runtime.Value{runtime.Str("z")})
	if _, ok := base.Enum("CLONE_E"); ok {
		t.Error("adding an enum to the clone leaked into the base set")
	}

	base.AddFunction(&Function{Name: "base_only", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Null(), nil
	}})
	if cp.HasFunction("base_only") {
		t.Error("adding a function to the base leaked into the clone set")
	}
	base.AddConstant("BASE_K", runtime.Str("base-only-k"))
	if _, ok := cp.Constant("BASE_K"); ok {
		t.Error("adding a constant to the base leaked into the clone set")
	}
}

// TestBaseExtensionFiltersNil confirms BaseExtension's Filters (and the sibling
// family methods) return nil, so a bundle that embeds it and overrides nothing
// contributes no filters when folded through Register.
func TestBaseExtensionFiltersNil(t *testing.T) {
	var b BaseExtension
	if b.Filters() != nil {
		t.Errorf("BaseExtension.Filters() = %v, want nil", b.Filters())
	}
	// The sibling family methods are the same zero-value no-op; Register folds a
	// bundle by ranging over each, so every one must be nil for an empty bundle to
	// contribute nothing.
	if b.Functions() != nil {
		t.Errorf("BaseExtension.Functions() = %v, want nil", b.Functions())
	}
	if b.Tests() != nil {
		t.Errorf("BaseExtension.Tests() = %v, want nil", b.Tests())
	}
	if b.Constants() != nil {
		t.Errorf("BaseExtension.Constants() = %v, want nil", b.Constants())
	}
	if b.Enums() != nil {
		t.Errorf("BaseExtension.Enums() = %v, want nil", b.Enums())
	}

	// Registering a do-nothing bundle adds nothing to any family.
	type emptyExt struct{ BaseExtension }
	s := NewExtensionSet()
	s.Register(emptyExt{})
	if s.HasFilter("anything") {
		t.Error("empty bundle should register no filters")
	}
	if s.HasFunction("anything") {
		t.Error("empty bundle should register no functions")
	}
	if s.HasTest("anything") {
		t.Error("empty bundle should register no tests")
	}
	if _, ok := s.Constant("anything"); ok {
		t.Error("empty bundle should register no constants")
	}
	if _, ok := s.Enum("anything"); ok {
		t.Error("empty bundle should register no enums")
	}
}
