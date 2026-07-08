package ext

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// greetExt is a bundle that ships one function, one filter, one test, a
// constant, and an enum, exercising every Bundle family through an embedded
// BaseExtension (so it need not implement the families it does not add -- here
// it happens to add all five, but the embed makes the pattern uniform).
type greetExt struct{ BaseExtension }

func (greetExt) Functions() []*Function {
	return []*Function{NewFunction("greet", func(name string) string { return "hi " + name })}
}

func (greetExt) Filters() []*Filter {
	return []*Filter{NewFilter("exclaim", func(s string) string { return s + "!" })}
}

func (greetExt) Tests() []*Test {
	return []*Test{NewTest("blank", func(s string) bool { return s == "" })}
}

func (greetExt) Constants() map[string]runtime.Value {
	return map[string]runtime.Value{"GREETING": runtime.Str("hello")}
}

func (greetExt) Enums() map[string][]runtime.Value {
	return map[string][]runtime.Value{"Mood": {runtime.Str("happy"), runtime.Str("sad")}}
}

// partialExt embeds BaseExtension and overrides only one family, proving a
// bundle need not implement every method.
type partialExt struct{ BaseExtension }

func (partialExt) Filters() []*Filter {
	return []*Filter{NewFilter("shout", func(s string) string { return s + "!!" })}
}

func TestRegisterFoldsEveryFamily(t *testing.T) {
	s := NewSet()
	s.Register(greetExt{})

	if _, ok := s.Function("greet"); !ok {
		t.Error("function greet not registered")
	}
	if _, ok := s.Filter("exclaim"); !ok {
		t.Error("filter exclaim not registered")
	}
	if _, ok := s.Test("blank"); !ok {
		t.Error("test blank not registered")
	}
	if v, ok := s.Constant("GREETING"); !ok || v.S != "hello" {
		t.Errorf("constant GREETING = %+v ok=%v", v, ok)
	}
	if cases, ok := s.Enum("Mood"); !ok || len(cases) != 2 || cases[0].S != "happy" {
		t.Errorf("enum Mood = %+v ok=%v", cases, ok)
	}
}

func TestRegisterPartialBundle(t *testing.T) {
	s := NewSet()
	s.Register(partialExt{})
	if _, ok := s.Filter("shout"); !ok {
		t.Error("filter shout not registered")
	}
	if _, ok := s.Function("greet"); ok {
		t.Error("partial bundle should not register a function")
	}
}

// TestMergeShadowOrder folds two sets and confirms the later set shadows the
// earlier one on a name collision, across every family.
func TestMergeShadowOrder(t *testing.T) {
	base := NewSet()
	base.AddFilter(&Filter{Name: "who", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Str("base"), nil
	}})
	base.AddConstant("K", runtime.Str("base"))

	over := NewSet()
	over.AddFilter(&Filter{Name: "who", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Str("over"), nil
	}})
	over.AddConstant("K", runtime.Str("over"))
	over.AddFilter(&Filter{Name: "only_over", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Str("x"), nil
	}})

	base.Merge(over)

	f, _ := base.Filter("who")
	got, _ := f.Fn(nil)
	if got.S != "over" {
		t.Errorf("shadow: who = %q, want over", got.S)
	}
	if v, _ := base.Constant("K"); v.S != "over" {
		t.Errorf("shadow: K = %q, want over", v.S)
	}
	if _, ok := base.Filter("only_over"); !ok {
		t.Error("merge dropped only_over")
	}
}

// TestMergeNil confirms merging a nil set is a no-op and returns the receiver.
func TestMergeNil(t *testing.T) {
	s := NewSet()
	s.AddFilter(&Filter{Name: "a", Fn: func([]runtime.Value) (runtime.Value, error) {
		return runtime.Null(), nil
	}})
	if s.Merge(nil) != s {
		t.Error("Merge(nil) should return receiver")
	}
	if !s.HasFilter("a") {
		t.Error("Merge(nil) altered the set")
	}
}

// TestMergeEnumsAreCopied confirms Merge copies the source's enum case list, so a
// later mutation of the source's stored slice does not leak into the merged set.
func TestMergeEnumsAreCopied(t *testing.T) {
	src := NewSet()
	src.AddEnum("E", []runtime.Value{runtime.Str("a"), runtime.Str("b")})

	dst := NewSet()
	dst.Merge(src)

	// Enum returns the internal slice, so this mutates src's stored copy. If Merge
	// aliased src's slice, this write would reach dst; a defensive copy shields it.
	stored, _ := src.Enum("E")
	stored[0] = runtime.Str("mutated")

	got, _ := dst.Enum("E")
	if got[0].S != "a" {
		t.Errorf("merged enum aliased source: %q", got[0].S)
	}
}

// TestRegisterReturnsReceiver confirms Register chains.
func TestRegisterReturnsReceiver(t *testing.T) {
	s := NewSet()
	if s.Register(partialExt{}) != s {
		t.Error("Register should return receiver")
	}
}
