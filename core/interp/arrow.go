package interp

import (
	"github.com/avmnu-sng/quill-template-engine/core/ast"
	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// arrowClosure is the runtime value an arrow expression evaluates to: the arrow
// node plus the lexical context it captured at definition. It implements
// runtime.Callable so the higher-order stdlib filters (map/filter/sort/reduce/
// find) and the membership quantifiers can apply it without depending on this
// package; the only coupling is the runtime.Callable protocol (spec 03 Sections
// 2.2, 04 Section 4.3).
//
// An arrow closes over its definition scope: a captured variable resolves to the
// value visible where the arrow was written, not where it is invoked. Invoke
// clones that captured context and binds the call arguments to the declared
// parameters before evaluating the body, so an invocation never mutates the
// closed-over scope (spec 04 Section 8 scope rules).
type arrowClosure struct {
	in     *interp
	params []*ast.Node    // the KindParam children, in order
	body   *ast.Node      // the body expression (last child)
	ctx    *runtime.Scope // the captured definition scope
}

// GetField satisfies runtime.Object; an arrow exposes no fields, so it always
// returns (null, false).
func (a *arrowClosure) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }

// CallMethod satisfies runtime.Object; an arrow is invoked positionally through
// Invoke, not by method, so any method call returns a runtime error.
func (a *arrowClosure) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), errors.New(errors.KindRuntime,
		"an arrow function is invoked positionally, not by method")
}

// Invoke binds args to the arrow's parameters in a child scope and evaluates the
// body. Surplus arguments are ignored; a missing argument takes the parameter's
// default expression if present, else Null. A variadic "...name" parameter
// collects the remaining arguments into a list (spec 01 Section 5 parameter
// model, shared with macros).
func (a *arrowClosure) Invoke(args []runtime.Value) (runtime.Value, error) {
	scope := a.ctx.Child()
	for i, p := range a.params {
		if p.Bool { // variadic: collect the rest
			rest := runtime.NewArray()
			idx := int64(0)
			for j := i; j < len(args); j++ {
				rest.SetInt(idx, args[j])
				idx++
			}
			scope.Set(p.Str, runtime.Arr(rest))
			break
		}
		if i < len(args) {
			scope.Set(p.Str, args[i])
			continue
		}
		// Missing argument: a declared default (child tagged by Int bit 1), else Null.
		if p.Int&ast.ParamHasDefault != 0 {
			def := p.Children[len(p.Children)-1]
			v, err := a.in.eval(def, scope, false)
			if err != nil {
				return runtime.Null(), err
			}
			scope.Set(p.Str, v)
			continue
		}
		scope.Set(p.Str, runtime.Null())
	}
	return a.in.eval(a.body, scope, false)
}

// evalArrow builds the closure value for an arrow expression, capturing the
// current scope. The arrow is not invoked here; it becomes a Callable value the
// higher-order filters and quantifiers apply later (spec 03 Section 2.2).
func (in *interp) evalArrow(n *ast.Node, ctx *runtime.Scope) (runtime.Value, error) {
	if len(n.Children) == 0 {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"arrow function has no body"))
	}
	body := n.Children[len(n.Children)-1]
	var params []*ast.Node
	for _, c := range n.Children[:len(n.Children)-1] {
		if c.Kind == ast.KindParam {
			params = append(params, c)
		}
	}
	return runtime.Obj(&arrowClosure{
		in:     in,
		params: params,
		body:   body,
		ctx:    ctx,
	}), nil
}
