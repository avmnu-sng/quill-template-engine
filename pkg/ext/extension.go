package ext

import "github.com/avmnu-sng/quill-template-engine/pkg/runtime"

// Extension is a cohesive bundle of callables and host tables a third party
// ships as a single unit. The engine folds a bundle into an ExtensionSet with
// Register: every filter, function, and test the bundle exposes is added by
// name (later registrations shadow earlier ones), and every constant and
// enumeration is merged into the set's host tables.
//
// A bundle implements only the families it provides. BaseExtension supplies a
// zero-value implementation of every method, so a bundle embeds it and overrides
// just the families it ships:
//
//	type MathExt struct{ ext.BaseExtension }
//	func (MathExt) Functions() []*ext.Function { return []*ext.Function{ ... } }
//
// The four callable-returning methods return the value objects directly, so a
// bundle can build them by hand (the &Filter{Name, Fn} form) or with the typed
// NewFilter/NewFunction/NewTest helpers.
type Extension interface {
	// Filters returns the filters the bundle contributes, or nil.
	Filters() []*Filter
	// Functions returns the functions the bundle contributes, or nil.
	Functions() []*Function
	// Tests returns the tests the bundle contributes, or nil.
	Tests() []*Test
	// Constants returns named constants the constant() function and the
	// `is constant` test resolve (spec 03 Section 3.2), or nil.
	Constants() map[string]runtime.Value
	// Enums returns named host enumerations as their ordered case lists, backing
	// enum() and enum_cases() (spec 03 Section 3.2), or nil.
	Enums() map[string][]runtime.Value
}

// BaseExtension is a zero-value Extension a bundle embeds so it implements only
// the families it ships. Every method returns nil; an embedding type overrides
// just the ones it provides.
type BaseExtension struct{}

// Filters returns nil; embed BaseExtension and override to contribute filters.
func (BaseExtension) Filters() []*Filter { return nil }

// Functions returns nil; embed BaseExtension and override to contribute functions.
func (BaseExtension) Functions() []*Function { return nil }

// Tests returns nil; embed BaseExtension and override to contribute tests.
func (BaseExtension) Tests() []*Test { return nil }

// Constants returns nil; embed BaseExtension and override to contribute constants.
func (BaseExtension) Constants() map[string]runtime.Value { return nil }

// Enums returns nil; embed BaseExtension and override to contribute enumerations.
func (BaseExtension) Enums() map[string][]runtime.Value { return nil }

// Register folds a bundle's callables and host tables into the set. Each filter,
// function, and test is added by name, shadowing any earlier entry of the same
// kind and name (the uniform "later wins" rule AddFilter/AddFunction/AddTest
// already follow). Constants and enumerations are merged into the set's host
// tables the same way. Register returns the receiver so calls chain.
func (s *ExtensionSet) Register(x Extension) *ExtensionSet {
	for _, f := range x.Filters() {
		s.AddFilter(f)
	}
	for _, fn := range x.Functions() {
		s.AddFunction(fn)
	}
	for _, t := range x.Tests() {
		s.AddTest(t)
	}
	for name, v := range x.Constants() {
		s.AddConstant(name, v)
	}
	for name, cases := range x.Enums() {
		s.AddEnum(name, cases)
	}
	return s
}

// Merge folds another set's callables and host tables into the receiver, with
// the other set shadowing the receiver on every name collision (later wins,
// matching Register). A nil other is a no-op. Merge returns the receiver so
// calls chain. This is the composition primitive behind layering several
// extension sets into one registry while preserving shadow order.
func (s *ExtensionSet) Merge(other *ExtensionSet) *ExtensionSet {
	if other == nil {
		return s
	}
	for name, f := range other.filters {
		s.filters[name] = f
	}
	for name, fn := range other.functions {
		s.functions[name] = fn
	}
	for name, t := range other.tests {
		s.tests[name] = t
	}
	for name, v := range other.constants {
		s.constants[name] = v
	}
	for name, cases := range other.enums {
		s.enums[name] = append([]runtime.Value(nil), cases...)
	}
	return s
}
