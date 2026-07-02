// Package ext is Quill's extension surface: the value objects for the three
// callable kinds (Filter, Function, Test) and the ExtensionSet registry that
// holds them. A name resolves to at most one filter, one function, and one test;
// the syntactic position (a pipe, a call, an `is`) selects which family is
// consulted (spec 03 Section 1). Host-registered callables shadow core ones of
// the same kind and name, so a host can override any built-in without editing
// the engine.
//
// The runtime-injection flags (NeedsEnvironment/NeedsContext/NeedsCharset, spec
// 03 Section 3.6) let a callable receive engine values the author never passes.
// The interpreter prepends them, in the fixed order environment, context,
// charset, ahead of the piped value and the user arguments. This slice wires the
// flags the core stdlib subset needs (include/block need environment+context);
// the remaining flags exist so the registration surface is complete.
package ext

import "github.com/avmnu-sng/quill-template-engine/runtime"

// Filter is a callable invoked through the pipe: x | name(args) is name(x, args)
// (spec 03 Section 1). Fn receives the injected engine values (per the Needs*
// flags) first, then the piped value, then the explicit arguments, already
// flattened from positional/named/spread by the interpreter.
type Filter struct {
	Name string
	Fn   func(args []runtime.Value) (runtime.Value, error)

	// Injection flags (spec 03 Section 3.6). When set, the interpreter prepends
	// the named engine value ahead of the piped value and user arguments.
	NeedsEnvironment bool
	NeedsContext     bool
	NeedsCharset     bool
}

// Function is a callable invoked as name(args) with all arguments explicit (spec
// 03 Section 1). The injection flags behave as for Filter; there is no implicit
// piped value.
type Function struct {
	Name string
	Fn   func(args []runtime.Value) (runtime.Value, error)

	NeedsEnvironment bool
	NeedsContext     bool
	NeedsCharset     bool
}

// Test is a callable applied as x is name / x is name(arg) (spec 03 Section 4).
// Fn receives the tested value first, then any explicit argument, and returns a
// boolean. A test never injects engine values in this slice.
type Test struct {
	Name string
	Fn   func(args []runtime.Value) (bool, error)
}

// EngineConfig is the read-only engine-configuration surface a
// needs-environment callable can read off its injected engine handle: the
// knobs the width- and seed-aware stdlib overrides consume (WithTabWidth,
// WithRandomSeed). Defining the interface here, at the consumption side, lets
// any render path -- the interpreter's engine handle or a compiled render's
// options handle -- satisfy it without this package importing either.
type EngineConfig interface {
	// TabWidth returns the number of spaces one indent level expands to for
	// the tab filter and the tab() function (default 4).
	TabWidth() int
	// RandomSeed returns the host-configured RNG seed and whether one was
	// set; unset means a time-seeded source per call.
	RandomSeed() (int64, bool)
}

// EngineConfigFromValue recovers the EngineConfig surface from the host Object
// a render path injects into a needs-environment callable. It reports false
// for a missing or configuration-less handle, in which case the callable falls
// back to its engine-default behavior.
func EngineConfigFromValue(v runtime.Value) (EngineConfig, bool) {
	if v.Kind != runtime.KObject {
		return nil, false
	}
	if cfg, ok := v.Obj.(EngineConfig); ok {
		return cfg, true
	}
	return nil, false
}

// ExtensionSet is the callable registry: three name-keyed maps, one per kind,
// plus the host-supplied constant and enumeration tables the constant/enum
// callables read (spec 03 Sections 3.2, 4). Lookups are by exact name; the
// parser already resolved two-word spellings and aliases to a canonical single
// name where it could, and the registry also holds explicit alias entries the
// stdlib installs.
type ExtensionSet struct {
	filters   map[string]*Filter
	functions map[string]*Function
	tests     map[string]*Test

	// constants holds named host/global constants resolved by the constant()
	// function and the `is constant` test (spec 03 Sections 3.2, 4). enums holds
	// named host enumerations (ordered case lists) backing enum()/enum_cases().
	constants map[string]runtime.Value
	enums     map[string][]runtime.Value
}

// NewExtensionSet returns an empty registry.
func NewExtensionSet() *ExtensionSet {
	return &ExtensionSet{
		filters:   map[string]*Filter{},
		functions: map[string]*Function{},
		tests:     map[string]*Test{},
		constants: map[string]runtime.Value{},
		enums:     map[string][]runtime.Value{},
	}
}

// AddConstant registers a named constant the constant() function and the
// `is constant` test resolve (spec 03 Section 3.2).
func (s *ExtensionSet) AddConstant(name string, v runtime.Value) { s.constants[name] = v }

// Constant looks up a registered constant by name.
func (s *ExtensionSet) Constant(name string) (runtime.Value, bool) {
	v, ok := s.constants[name]
	return v, ok
}

// AddEnum registers a named host enumeration as its ordered case list, backing
// enum() (first case) and enum_cases() (all cases), spec 03 Section 3.2.
func (s *ExtensionSet) AddEnum(name string, cases []runtime.Value) {
	s.enums[name] = append([]runtime.Value(nil), cases...)
}

// Enum looks up a registered enumeration's case list by name.
func (s *ExtensionSet) Enum(name string) ([]runtime.Value, bool) {
	c, ok := s.enums[name]
	return c, ok
}

// AddFilter registers (or shadows) a filter by name. A later registration of the
// same name wins, which is exactly how a host overrides a core filter.
func (s *ExtensionSet) AddFilter(f *Filter) { s.filters[f.Name] = f }

// AddFunction registers (or shadows) a function by name.
func (s *ExtensionSet) AddFunction(f *Function) { s.functions[f.Name] = f }

// AddTest registers (or shadows) a test by name.
func (s *ExtensionSet) AddTest(t *Test) { s.tests[t.Name] = t }

// Filter looks up a filter by name.
func (s *ExtensionSet) Filter(name string) (*Filter, bool) {
	f, ok := s.filters[name]
	return f, ok
}

// Function looks up a function by name.
func (s *ExtensionSet) Function(name string) (*Function, bool) {
	f, ok := s.functions[name]
	return f, ok
}

// Test looks up a test by name.
func (s *ExtensionSet) Test(name string) (*Test, bool) {
	t, ok := s.tests[name]
	return t, ok
}

// HasFilter reports whether a filter with the given name is registered. It backs
// the @guard statement's existence check (spec 01 Section 4.6), selecting a branch
// on registration without evaluating the callable.
func (s *ExtensionSet) HasFilter(name string) bool { _, ok := s.filters[name]; return ok }

// HasFunction reports whether a function with the given name is registered. It
// backs the @guard statement's existence check (spec 01 Section 4.6), selecting a
// branch on registration without evaluating the callable.
func (s *ExtensionSet) HasFunction(name string) bool { _, ok := s.functions[name]; return ok }

// HasTest reports whether a test with the given name is registered. It backs the
// @guard statement's existence check (spec 01 Section 4.6), selecting a branch on
// registration without evaluating the callable.
func (s *ExtensionSet) HasTest(name string) bool { _, ok := s.tests[name]; return ok }

// Clone returns a shallow copy of the registry sharing the callable values but
// with independent maps, so a host can layer additions over a base set without
// mutating it.
func (s *ExtensionSet) Clone() *ExtensionSet {
	cp := NewExtensionSet()
	for k, v := range s.filters {
		cp.filters[k] = v
	}
	for k, v := range s.functions {
		cp.functions[k] = v
	}
	for k, v := range s.tests {
		cp.tests[k] = v
	}
	for k, v := range s.constants {
		cp.constants[k] = v
	}
	for k, v := range s.enums {
		cp.enums[k] = v
	}
	return cp
}
