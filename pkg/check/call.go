package check

import (
	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
)

// callType infers a call "f(args)" or a macro/dotted call. A bare-name callee
// resolves to a template macro signature (checked for arity/types), a host
// function signature (checked), or the dynamic floor (any). A non-name callee
// (a dotted macro `forms.input`, a method on an object) is typed dynamically in
// this slice, since cross-template macro signatures need the loader.
func (c *checker) callType(n *ast.Node, sc *scope) (*Type, error) {
	callee := n.Child(0)
	args := n.Children[1:]

	// Method call a.m(...): type the receiver and, on a known Object, check the
	// method signature; otherwise dynamic.
	if callee != nil && callee.Kind == ast.KindAttr {
		return c.methodCallType(callee, args, sc)
	}

	if callee == nil || callee.Kind != ast.KindName {
		// Type the args for embedded errors, then any.
		if err := c.typeArgs(args, sc); err != nil {
			return Any, err
		}
		return Any, nil
	}

	name := callee.Str
	// A template macro shadows nothing engine-side; check it as a typed callable.
	if sig, ok := c.macros[name]; ok {
		return c.checkCall(n, sig, args, sc)
	}
	// A host function signature (registry first, then the built-in stdlib table).
	if sig := c.functionSig(name); sig != nil {
		return c.checkCall(n, sig, args, sc)
	}
	// Unknown function: dynamic. Type the args for embedded errors.
	if err := c.typeArgs(args, sc); err != nil {
		return Any, err
	}
	return Any, nil
}

// methodCallType types a.m(args). On a known Object<"T"> with a declared method
// it checks the call against the method signature; otherwise the result is any.
func (c *checker) methodCallType(attr *ast.Node, args []*ast.Node, sc *scope) (*Type, error) {
	recv, err := c.exprType(attr.Child(0), sc)
	if err != nil {
		return Any, err
	}
	if recv != nil && recv.kind == KObject && c.reg.nominal() {
		if sig, ok := c.reg.methodSig(recv.name, attr.Str); ok {
			if sig != nil {
				return c.checkCall(attr, sig, args, sc)
			}
		} else {
			return Any, errAt(attr, "type %s has no method %s",
				recv.String(), quoteName(attr.Str))
		}
	}
	if err := c.typeArgs(args, sc); err != nil {
		return Any, err
	}
	return Any, nil
}

// filterType infers "x | f(args)": the piped value is the implicit first
// argument. The higher-order collection filters (map/filter/sort/reduce/find)
// are typed generically so element types propagate through arrows; the rest use
// the registry/built-in signature table or fall to the dynamic floor.
func (c *checker) filterType(n *ast.Node, sc *scope) (*Type, error) {
	name := n.Str
	explicit := n.Children[1:]

	// `default` is a whole-chain absence-suppression tool (spec 04 Section 6): a
	// member miss or undefined name anywhere in the piped operand yields the
	// fallback at runtime, never an error. So type its piped value leniently --
	// mirroring `??` -- swallowing the absence miss while still surfacing a genuine
	// (non-absence) type error.
	if name == "default" {
		piped, err := c.exprTypeLenient(n.Child(0), sc)
		if err != nil {
			return Any, err
		}
		fb := Any
		if len(explicit) >= 1 {
			ft, err := c.exprType(argValue(explicit[0]), sc)
			if err != nil {
				return Any, err
			}
			fb = ft
		}
		return join(removeNull(piped), fb), nil
	}

	piped, err := c.exprType(n.Child(0), sc)
	if err != nil {
		return Any, err
	}

	// The element-propagating higher-order filters get a generic rule.
	if t, handled, err := c.higherOrderFilter(n, name, piped, explicit, sc); handled {
		return t, err
	}

	// A host/built-in signature: prepend the piped value as the first argument.
	if sig := c.filterSig(name); sig != nil {
		return c.checkFilterCall(n, sig, piped, explicit, sc)
	}
	// Unknown filter: dynamic. Type the explicit args for embedded errors.
	if err := c.typeArgs(explicit, sc); err != nil {
		return Any, err
	}
	return Any, nil
}

// higherOrderFilter types map/filter/sort/reduce/find with element propagation
// (design/type-system.md Section 9.3). It returns handled=false for any other
// filter so the caller falls through to the signature table.
func (c *checker) higherOrderFilter(n *ast.Node, name string, piped *Type, explicit []*ast.Node, sc *scope) (*Type, bool, error) {
	elem, _, iterable := c.iterableElem(piped)
	if !piped.isAny() && !iterable {
		// A higher-order filter over a non-collection is a type error, except when
		// the value is any (dynamic). Only flag for the genuinely-collection names.
		switch name {
		case "map", "filter", "sort", "reduce", "find":
			return Any, true, errAt(n.Child(0),
				"filter %s requires a list, found %s", quoteName(name), piped.String())
		}
	}
	switch name {
	case "map":
		// map(list<A>, (A) => B) => list<B>.
		bt := Any
		if len(explicit) >= 1 {
			at, err := c.arrowArg(explicit[0], []*Type{elem}, sc)
			if err != nil {
				return Any, true, err
			}
			bt = at
		}
		return ListOf(bt), true, nil
	case "filter":
		// filter(list<A>, (A) => bool) => list<A>.
		if len(explicit) >= 1 {
			if _, err := c.arrowArg(explicit[0], []*Type{elem}, sc); err != nil {
				return Any, true, err
			}
		}
		if piped.isAny() {
			return Any, true, nil
		}
		return ListOf(elem), true, nil
	case "sort":
		// sort(list<A>, (A, A) => int) => list<A>.
		if len(explicit) >= 1 {
			if _, err := c.arrowArg(explicit[0], []*Type{elem, elem}, sc); err != nil {
				return Any, true, err
			}
		}
		if piped.isAny() {
			return Any, true, nil
		}
		return ListOf(elem), true, nil
	case "reduce":
		// reduce(list<A>, (B, A) => B, B) => B; the accumulator type is the seed's.
		acc := Any
		if len(explicit) >= 2 {
			at, err := c.exprType(argValue(explicit[1]), sc)
			if err != nil {
				return Any, true, err
			}
			acc = at
		}
		if len(explicit) >= 1 {
			if _, err := c.arrowArg(explicit[0], []*Type{acc, elem}, sc); err != nil {
				return Any, true, err
			}
		}
		return acc, true, nil
	case "find":
		// find(list<A>, (A) => bool) => A?.
		if len(explicit) >= 1 {
			if _, err := c.arrowArg(explicit[0], []*Type{elem}, sc); err != nil {
				return Any, true, err
			}
		}
		if piped.isAny() {
			return Any, true, nil
		}
		return join(elem, Null), true, nil
	}
	return Any, false, nil
}

// arrowArg types an argument that is expected to be an arrow, passing the
// inferred parameter hints (the element types from the piped collection). A
// non-arrow argument (a host callable name, a reference) is typed normally and
// its result (the arrow's body type, when an arrow) is returned.
func (c *checker) arrowArg(arg *ast.Node, hints []*Type, sc *scope) (*Type, error) {
	v := argValue(arg)
	if v != nil && v.Kind == ast.KindArrow {
		at, err := c.arrowType(v, sc, hints)
		if err != nil {
			return Any, err
		}
		if at != nil && at.kind == KArrow {
			return at.ret, nil
		}
		return Any, nil
	}
	// Not a literal arrow: type it (any), its body type is unknown.
	if _, err := c.exprType(v, sc); err != nil {
		return Any, err
	}
	return Any, nil
}

// checkCall verifies a function/macro/method call against a signature and
// returns the signature's result type. It checks positional arity (within the
// min/max implied by optionals and a variadic), named arguments against
// parameter availability, and each positional argument's type for consistency
// with its parameter (Section 9.1, 9.4).
func (c *checker) checkCall(n *ast.Node, sig *Signature, args []*ast.Node, sc *scope) (*Type, error) {
	return c.checkArgs(n, sig, nil, args, sc)
}

// checkFilterCall is checkCall with the piped value prepended as the implicit
// first positional argument.
func (c *checker) checkFilterCall(n *ast.Node, sig *Signature, piped *Type, explicit []*ast.Node, sc *scope) (*Type, error) {
	return c.checkArgs(n, sig, piped, explicit, sc)
}

// checkArgs is the shared arity/type checker. pipedFirst, when non-nil, is the
// type of an implicit leading argument (the piped value of a filter) that
// occupies the first parameter slot ahead of the explicit args.
func (c *checker) checkArgs(n *ast.Node, sig *Signature, pipedFirst *Type, args []*ast.Node, sc *scope) (*Type, error) {
	// Collect the positional argument types (the piped value, then each explicit
	// positional). Named and spread args relax the arity check (their pairing is
	// dynamic), so we only enforce strict arity when all explicit args are
	// positional.
	var posTypes []*Type
	var posNodes []*ast.Node
	hasNamedOrSpread := false
	if pipedFirst != nil {
		posTypes = append(posTypes, pipedFirst)
		posNodes = append(posNodes, nil)
	}
	for _, a := range args {
		switch a.Int {
		case ast.ArgNamed, ast.ArgSpread:
			hasNamedOrSpread = true
			if _, err := c.exprType(argValue(a), sc); err != nil {
				return Any, err
			}
		default:
			t, err := c.exprType(argValue(a), sc)
			if err != nil {
				return Any, err
			}
			posTypes = append(posTypes, t)
			posNodes = append(posNodes, a)
		}
	}

	minArgs := len(sig.params) - sig.optional
	maxArgs := len(sig.params)
	if sig.variadic {
		maxArgs = -1 // unbounded
	}

	if !hasNamedOrSpread {
		if len(posTypes) < minArgs {
			return Any, errAt(n, "call expects at least %d argument(s), got %d", minArgs, len(posTypes))
		}
		if maxArgs >= 0 && len(posTypes) > maxArgs {
			return Any, errAt(n, "call expects at most %d argument(s), got %d", maxArgs, len(posTypes))
		}
	}

	// Type-check each positional argument against its parameter.
	for i, at := range posTypes {
		var pt *Type
		if i < len(sig.params) {
			pt = sig.params[i]
		} else if sig.variadic {
			pt = sig.varElem
		} else {
			break
		}
		if !c.consistent(at, pt) {
			node := n
			if i < len(posNodes) && posNodes[i] != nil {
				node = posNodes[i]
			}
			return Any, errAt(node,
				"argument %d has type %s but %s is expected", i+1, at.String(), pt.String())
		}
	}
	return c.sigRet(sig), nil
}

// sigRet returns the signature's result type, defaulting to any.
func (c *checker) sigRet(sig *Signature) *Type {
	if sig.ret == nil {
		return Any
	}
	return sig.ret
}

// typeArgs types each call/filter argument for embedded errors, ignoring the
// result (used on a dynamic callee where no arity check applies).
func (c *checker) typeArgs(args []*ast.Node, sc *scope) error {
	for _, a := range args {
		if _, err := c.exprType(argValue(a), sc); err != nil {
			return err
		}
	}
	return nil
}

// argValue returns a KindArg's value expression (its only/first child).
func argValue(a *ast.Node) *ast.Node {
	if a == nil {
		return nil
	}
	if a.Kind == ast.KindArg {
		return a.Child(0)
	}
	return a
}

// functionSig resolves a function name to a signature: the host registry wins,
// then the built-in stdlib function table.
func (c *checker) functionSig(name string) *Signature {
	if s := c.reg.signature(name); s != nil {
		return s
	}
	return builtinFunctionSigs[name]
}

// filterSig resolves a filter name to a signature: host registry, then built-in.
func (c *checker) filterSig(name string) *Signature {
	if s := c.reg.signature(name); s != nil {
		return s
	}
	return builtinFilterSigs[name]
}
