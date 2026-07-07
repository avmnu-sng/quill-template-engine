package interp

import (
	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// checkSecurity is the sandbox's Phase-1 per-render check (B9): given a
// template's compile-time-collected used callables, it validates every used
// statement keyword, filter, and function against the active policy in one pass
// and returns the first violation as a typed *errors.Security mapped to the
// offending node's source line. It runs once when a sandboxed render begins (or
// when an @sandbox region's body begins), not per node. A nil policy under an
// active sandbox denies everything, since allowlisting is uniform with no
// grandfathering (B6).
//
// Macro names are not policed here: a macro is template-defined, so a bare-name
// callee that resolves to a macro in scope is skipped (only host functions are
// gated). The composition builtins parent/block are gated as functions, so a
// policy must allow them by name to use inheritance helpers (no grandfathering).
func (in *interp) checkSecurity(u usedCallables) error {
	pol := in.eng.Policy()
	for tag, node := range u.tags {
		if !pol.AllowsTag(tag) {
			return errors.SecurityTag(tag).At(node.Src, node.Line)
		}
	}
	for name, node := range u.filters {
		if !pol.AllowsFilter(name) {
			return errors.SecurityFilter(name).At(node.Src, node.Line)
		}
	}
	for name, node := range u.functions {
		// A bare-name callee that is a macro in the current namespace is
		// template-defined and not a policed function (design/escaping-safety
		// Section 6). parent/block are policed as functions like any other.
		if _, isMacro := in.macros[name]; isMacro {
			continue
		}
		if !pol.AllowsFunction(name) {
			return errors.SecurityFunction(name).At(node.Src, node.Line)
		}
	}
	return nil
}

// checkMethodAllowed gates a host method call when the sandbox is active (B10).
// A Safe value and a template-internal shim bypass the check (B14); they are
// trusted by construction. The type name comes from the host ClassName hook so
// the policy and the type-graph speak the same names; an unnamed object matches
// only the literal "object".
func (in *interp) checkMethodAllowed(recv runtime.Value, method string) error {
	if !in.sandboxOn || recv.Kind != runtime.KObject {
		return nil
	}
	if isTrustedShim(recv.Obj) {
		return nil
	}
	typeName := className(recv.Obj)
	pol := in.eng.Policy()
	if !pol.AllowsMethod(typeName, method) {
		if pol.Strict && !pol.Knows(typeName) {
			return errors.SecurityUnknownType(errors.SecMethod, typeName, method)
		}
		return errors.SecurityMethod(typeName, method)
	}
	return nil
}

// checkPropertyAllowed gates a host property/field read when the sandbox is
// active (B11). When a name a.b could be either a property or a method and
// neither is allowed, the PROPERTY error is reported (property is checked at the
// read site, the method path is the fallback) -- the documented property-then-
// method precedence. Trusted shims bypass (B14).
func (in *interp) checkPropertyAllowed(recv runtime.Value, prop string) error {
	if !in.sandboxOn || recv.Kind != runtime.KObject {
		return nil
	}
	if isTrustedShim(recv.Obj) {
		return nil
	}
	typeName := className(recv.Obj)
	pol := in.eng.Policy()
	if !pol.AllowsProperty(typeName, prop) {
		if pol.Strict && !pol.Knows(typeName) {
			return errors.SecurityUnknownType(errors.SecProperty, typeName, prop)
		}
		return errors.SecurityProperty(typeName, prop)
	}
	return nil
}

// checkStringifyAllowed is the string-coercion gate (B12): before a host Object
// is coerced to text -- at an interpolation, in ~ concat, or as a join/replace/
// split argument -- the object's stringify member must be permitted. The gated
// member name is the conventional "Stringify" hook, matched against the policy's
// method allowlist through the type-graph. A Safe
// value, a non-object, and a trusted shim are not gated (B14).
func (in *interp) checkStringifyAllowed(v runtime.Value) error {
	if !in.sandboxOn || v.Kind != runtime.KObject {
		return nil
	}
	if isTrustedShim(v.Obj) {
		return nil
	}
	typeName := className(v.Obj)
	pol := in.eng.Policy()
	if !pol.AllowsMethod(typeName, "Stringify") {
		if pol.Strict && !pol.Knows(typeName) {
			return errors.SecurityUnknownType(errors.SecMethod, typeName, "Stringify")
		}
		return errors.SecurityMethod(typeName, "Stringify")
	}
	return nil
}

// coercingFilters names the stdlib filters that coerce a host Object to text via
// runtime.ToText inside package ext, where the sandbox gate is unreachable. The
// interp-side choke point (evalFilter / execApply) pre-scans their arguments and
// runs checkStringifyAllowed so spec 04 Section 8.3's "string-coercion is gated
// via the Stringify hook" holds at these sites too, not only at an interpolation.
var coercingFilters = map[string]bool{
	"join":    true,
	"replace": true,
	"split":   true,
}

// checkStringifyArgs gates the host-object string-coercion the coercing filters
// (join/replace/split) perform on their arguments. ext cannot reach the interp's
// policy, so the interp validates here before invoking the filter: any host
// Object reachable in an argument -- a scalar arg, a sequence element, or a map
// key/value -- must have its Stringify member allowed, mirroring the
// interpolation and ~ concat gates (B12). Filters not in coercingFilters are
// untouched, so an arrow argument to map/filter/etc. is not mistaken for a
// coercion (that path is gated by checkArrowArgs instead).
func (in *interp) checkStringifyArgs(filter string, args []runtime.Value) error {
	if !in.sandboxOn || !coercingFilters[filter] {
		return nil
	}
	for _, a := range args {
		if err := in.checkStringifyDeep(a); err != nil {
			return err
		}
	}
	return nil
}

// checkStringifyArg applies the string-coercion gate (B12) to one argument
// value under the same filter-name key as checkStringifyArgs. The filter fast
// call runs it on the piped value, so a host filter that shadows a coercing
// name AND publishes Fn1 still has its one reachable argument gated -- the
// fast call cannot open a coercion path the general path would have gated.
func (in *interp) checkStringifyArg(filter string, a runtime.Value) error {
	if !in.sandboxOn || !coercingFilters[filter] {
		return nil
	}
	return in.checkStringifyDeep(a)
}

// checkStringifyDeep applies the Stringify gate to v and, when v is a collection,
// to each element and map key/value -- the elements the coercing filters render
// through ToText. A callable Object is skipped: it is not a coercion target and
// is governed by the arrow-gating rule, not the stringify gate.
func (in *interp) checkStringifyDeep(v runtime.Value) error {
	switch v.Kind {
	case runtime.KObject:
		if runtime.IsCallable(v) {
			return nil
		}
		return in.checkStringifyAllowed(v)
	case runtime.KArray:
		if v.Arr == nil {
			return nil
		}
		for _, p := range v.Arr.Pairs() {
			if err := in.checkStringifyDeep(p.Key); err != nil {
				return err
			}
			if err := in.checkStringifyDeep(p.Val); err != nil {
				return err
			}
		}
	}
	return nil
}

// className reports a host Object's registered type name via the ClassName hook
// (the same name the policy and type-graph use), or "object" when the host did
// not name the type.
func className(o runtime.Object) string {
	if c, ok := o.(runtime.ClassNamed); ok {
		return c.ClassName()
	}
	return "object"
}

// isTrustedShim reports whether a host Object is an engine-internal value that
// bypasses member-access and string-coercion checks: the macro/namespace/engine
// shims and arrow closures are produced by the engine, never by untrusted
// template data (B14). A template's own internals are never attribute-accessible
// regardless of the sandbox, which the shim GetField/CallMethod stubs enforce.
func isTrustedShim(o runtime.Object) bool {
	switch o.(type) {
	case *macroRef, *selfRef, *importNS, *engineRef, *arrowClosure:
		return true
	default:
		return false
	}
}

// execSandbox renders an @sandbox region: it forces the sandbox on over the
// body and for any templates included within it, then restores the prior gate
// on exit -- never turning the sandbox off for an already-sandboxed enclosing
// render (B7, B16). When the region (not a global toggle) is what activates the
// sandbox, the Phase-1 check is scoped to the region body's used callables so
// only what the region actually references is validated. If the sandbox was
// already on, the enclosing render already ran the whole-template Phase-1 check,
// so the region just keeps it on for nested includes.
func (in *interp) execSandbox(n *ast.Node, ctx *runtime.Scope) error {
	wasOn := in.sandboxOn
	in.sandboxOn = true
	if !wasOn {
		if err := in.checkSecurity(in.regionUsed(n.Children)); err != nil {
			in.sandboxOn = wasOn
			return err
		}
	}
	err := in.execItems(n.Children, ctx)
	in.sandboxOn = wasOn // restore: re-enabling a nested include never disabled us.
	return err
}

// checkArrowArgs enforces the sandbox's arrow-gating rule (B13): when the
// sandbox is active, any callable value passed as a filter argument must be a
// template-defined arrow (an *arrowClosure), not an arbitrary host callable
// smuggled in through context data. A non-arrow Callable is rejected as a
// disallowed function. Non-callable arguments are untouched, so ordinary
// filters are unaffected.
func (in *interp) checkArrowArgs(n *ast.Node, args []runtime.Value) error {
	for _, a := range args {
		if err := in.checkArrowArg(n, a); err != nil {
			return err
		}
	}
	return nil
}

// checkArrowArg applies the arrow-gating rule (B13) to one argument value. It
// is the per-value form checkArrowArgs loops over, and the filter fast call
// runs it directly on the piped value -- the only value that exists there --
// so both dispatch routes enforce the rule through the same code.
func (in *interp) checkArrowArg(n *ast.Node, a runtime.Value) error {
	if !in.sandboxOn {
		return nil
	}
	if a.Kind != runtime.KObject || !runtime.IsCallable(a) {
		return nil
	}
	if _, ok := a.Obj.(*arrowClosure); !ok {
		return posErr(n, errors.SecurityFunction("(non-template callable)"))
	}
	return nil
}

// regionUsed collects the tags/filters/functions used directly inside an
// @sandbox region body so the Phase-1 check can be scoped to the region rather
// than the whole template, when the region (not a global toggle) is what turns
// the sandbox on (B7). It reuses the template's collectUsed walk over the
// region's child nodes.
func (in *interp) regionUsed(body []*ast.Node) usedCallables {
	u := newUsedCallables()
	for _, c := range body {
		in.root.collectUsed(c, u)
	}
	return u
}
