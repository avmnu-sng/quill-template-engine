package ext

import (
	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// This file holds two small reference-typed values (spec 03 Section 3.2):
//
//   - separator(sep): a callable that yields "" on its first call and sep on
//     every call after, for trailing-separator-free joining across a loop.
//   - cell(initial): a mutable single-slot reference whose `value` field is
//     assignable and survives a loop body, for accumulation inside a loop
//     without weakening the default no-leak loop scoping.
//
// Both are host Objects. A separator is a Callable so `sep()` invokes it; a cell
// is a FieldSetter so `@set acc.value = ...` mutates it in place. Because an
// Object circulates by pointer (a loop's scope clone copies the Value, not the
// pointee), a cell mutated inside a loop body is visible after the loop while the
// loop's own name rebindings still do not leak.

// registerRefFunctions installs separator() and cell() onto s. It is called from
// registerStdlibFunctions.
func registerRefFunctions(s *ExtensionSet) {
	s.AddFunction(&Function{Name: "separator", Fn: fnSeparator})
	s.AddFunction(&Function{Name: "cell", Fn: fnCell})
}

// separatorValue is the callable a separator() returns. The first Invoke yields
// the empty string and arms it; every later Invoke yields the separator. It is
// stateful by design, so the same separator woven through a loop emits the glue
// between elements and nothing before the first.
type separatorValue struct {
	sep     string
	emitted bool
}

// GetField exposes no members on a separator; it is used only by calling it.
func (s *separatorValue) GetField(string) (runtime.Value, bool) {
	return runtime.Null(), false
}

// CallMethod rejects method calls: a separator is invoked directly as sep(), not
// through a named method.
func (s *separatorValue) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), errors.New(errors.KindRuntime, "a separator has no methods")
}

// ClassName reports the host type name of a separator value.
func (s *separatorValue) ClassName() string { return "Separator" }

// Invoke yields "" on the first call and the separator on every call after,
// ignoring any arguments. It is the Callable protocol behind sep().
func (s *separatorValue) Invoke([]runtime.Value) (runtime.Value, error) {
	if !s.emitted {
		s.emitted = true
		return runtime.Str(""), nil
	}
	return runtime.Str(s.sep), nil
}

// fnSeparator constructs a separator whose glue is sep (default ",") -- separator()
// or separator(", "). The returned value is callable: the first call yields "",
// each later call yields the glue (spec 03 Section 3.2).
func fnSeparator(args []runtime.Value) (runtime.Value, error) {
	sep := ","
	if len(args) > 0 && !args[0].IsNull() {
		s, err := wantString(args[0])
		if err != nil {
			return runtime.Null(), err
		}
		sep = s
	}
	return runtime.Obj(&separatorValue{sep: sep}), nil
}

// cellValue is the mutable single-slot reference a cell() returns. Its one member
// is `value`; reading it yields the held value, and @set c.value = expr replaces
// it in place. The Object circulates by pointer, so the mutation is visible to
// every holder of the cell, which is what lets an accumulator survive a loop
// body.
type cellValue struct {
	val runtime.Value
}

// GetField exposes the single `value` member; any other name is absent. Reading
// `value` returns the currently held value.
func (c *cellValue) GetField(name string) (runtime.Value, bool) {
	if name == "value" {
		return c.val, true
	}
	return runtime.Null(), false
}

// SetField assigns the `value` member in place. Any other name is a runtime
// error, so a typo does not silently create a second slot.
func (c *cellValue) SetField(name string, v runtime.Value) error {
	if name != "value" {
		return errors.New(errors.KindRuntime, "a cell has only a `value` member, not %q", name)
	}
	c.val = v
	return nil
}

// CallMethod rejects method calls: a cell is read and written through its `value`
// member, not through named methods.
func (c *cellValue) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), errors.New(errors.KindRuntime, "a cell has no methods")
}

// Stringify renders the held value so a bare {{ c }} spells the accumulation.
func (c *cellValue) Stringify() (string, error) {
	return runtime.ToText(c.val)
}

// ClassName reports the host type name of a cell value.
func (c *cellValue) ClassName() string { return "Cell" }

// fnCell constructs a mutable cell holding initial (default null) -- cell() or
// cell(0). The returned value carries an assignable `value` member that survives
// a loop body (spec 03 Section 3.2).
func fnCell(args []runtime.Value) (runtime.Value, error) {
	return runtime.Obj(&cellValue{val: arg(args, 0)}), nil
}
