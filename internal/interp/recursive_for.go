package interp

import (
	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/cover"
	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// recursiveLoop is one active "@for .. recursive" descent: the loop body and
// the loop target name(s). loop(children) inside the body re-enters the body
// over a subtree one depth deeper, so the frame carries everything a re-entry
// needs without re-reading the @for node.
type recursiveLoop struct {
	body    []*ast.Node
	target1 string
	target2 string // "" for the single-target form
	twoTgt  bool
	// filter is the fused "if" condition (the KindClause's child 0) when the loop
	// was written "@for .. recursive if cond", else nil. It is applied at every
	// descent level so a node whose condition is false -- and its whole subtree --
	// is pruned, matching the plain @for's fused-filter semantics.
	filter *ast.Node
}

// curRecursive returns the innermost active recursive-loop frame, or nil when no
// "@for .. recursive" loop is on the stack. evalCall consults it so a bare
// loop(...) call is treated as the descent callable only inside such a loop.
func (in *interp) curRecursive() *recursiveLoop {
	if len(in.recursiveLoops) == 0 {
		return nil
	}
	return in.recursiveLoops[len(in.recursiveLoops)-1]
}

// execRecursiveFor renders a "@for node in tree recursive { ... }" loop. It drains
// the top-level iterand to pairs and renders the body at depth 0, pushing a
// descent frame so loop(children) inside the body can re-enter over a subtree. An
// empty top-level iterand takes the @else body (or nothing), mirroring the plain
// @for empty arm. The loop.* metadata gains depth / depth0 fields on top of the
// usual index / first / last set (design/composition, recursive @for).
func (in *interp) execRecursiveFor(n *ast.Node, ctx *runtime.Scope, target1, target2 *ast.Node, iterand *ast.Node, body, elseBody *ast.Node, filter *ast.Node) error {
	collVal, err := in.eval(iterand, ctx, false)
	if err != nil {
		return err
	}
	pairs, err := runtime.EnsureTraversable(collVal, !in.eng.StrictVariables())
	if err != nil {
		return posErr(n, err)
	}

	frame := &recursiveLoop{
		body:    body.Children,
		target1: target1.Str,
	}
	if target2 != nil {
		frame.target2 = target2.Str
		frame.twoTgt = true
	}
	if filter != nil {
		frame.filter = filter.Child(0)
		// Prune the top level once here so the empty-arm decision reflects the
		// survivors; the top-level renderRecursiveLevel call then runs over these
		// already-pruned pairs (preFiltered) without evaluating the condition twice.
		pairs, err = in.filterRecursivePairs(frame, pairs, ctx)
		if err != nil {
			return err
		}
	}

	if len(pairs) == 0 {
		in.covArm(n, cover.ForEmpty)
		if elseBody != nil {
			return in.execItems(elseBody.Children, ctx)
		}
		return nil
	}
	in.covArm(n, cover.ForBody)
	in.recursiveLoops = append(in.recursiveLoops, frame)
	defer func() { in.recursiveLoops = in.recursiveLoops[:len(in.recursiveLoops)-1] }()

	return in.renderRecursiveLevel(frame, pairs, 0, ctx, true)
}

// renderRecursiveLevel renders the recursive-loop body over one level of pairs at
// the given depth, binding the target(s) and a loop.* mapping (with depth /
// depth0) for each element. It writes directly to the active sink, so the
// top-level call emits in place; a nested level called via loop(children) renders
// into the capture the callable set up.
func (in *interp) renderRecursiveLevel(frame *recursiveLoop, pairs []runtime.Pair, depth0 int, outer *runtime.Scope, preFiltered bool) error {
	if frame.filter != nil && !preFiltered {
		var err error
		pairs, err = in.filterRecursivePairs(frame, pairs, outer)
		if err != nil {
			return err
		}
	}
	loopCtx := outer.Child()
	parentPtr := probeLoopParent(outer)
	for i, p := range pairs {
		// Cancellation checkpoint at the recursive-descent iteration boundary,
		// mirroring the plain @for loop so a deep or wide recursive walk honors the
		// context between elements.
		if err := in.checkCancelled(); err != nil {
			return err
		}
		if frame.twoTgt {
			loopCtx.Set(frame.target1, p.Key)
			loopCtx.Set(frame.target2, p.Val)
		} else {
			loopCtx.Set(frame.target1, p.Val)
		}
		loopCtx.Set("loop", runtime.NewRecursiveLoopValue(i, pairs, depth0, parentPtr))
		if err := in.execItems(frame.body, loopCtx); err != nil {
			return err
		}
	}
	return nil
}

// callRecursiveLoop implements loop(children): it renders the innermost recursive
// loop's body over the given subtree one depth deeper, capturing the output and
// returning it as a value so the body can print it (for example
// "{{ loop(node.children) }}"). A non-traversable argument (a leaf's absent or
// empty children) renders nothing and returns the empty string.
func (in *interp) callRecursiveLoop(n *ast.Node, ctx *runtime.Scope) (runtime.Value, error) {
	frame := in.curRecursive()
	if frame == nil {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"loop() is only valid inside a '@for .. recursive' loop"))
	}
	args, err := in.collectArgs(n, ctx, nil)
	if err != nil {
		return runtime.Null(), err
	}
	if len(args) != 1 {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"loop() expects exactly one children argument, got %d", len(args)))
	}
	child := args[0]
	if child.Kind() != runtime.KArray || child.AsArray() == nil {
		if in.escape != "" {
			return runtime.Safe(""), nil
		}
		return runtime.Str(""), nil
	}
	pairs := child.AsArray().Pairs()

	// The next level's depth0 is the enclosing element's depth0 plus one; read it
	// from the loop mapping in scope so a re-entry deepens correctly.
	depth0 := 1
	if lv, ok := ctx.Get("loop"); ok {
		// Read the enclosing level's depth0 through the generic attribute path so
		// the loop value's representation stays private to loopvalue.go.
		if d, err := runtime.GetAttribute(lv, runtime.Str("depth0"), runtime.AccessDot, true); err == nil && d.Kind() == runtime.KInt {
			depth0 = int(d.AsInt()) + 1
		}
	}

	sub := &captureSink{}
	saved := in.out
	in.out = sub
	err = in.renderRecursiveLevel(frame, pairs, depth0, ctx, false)
	in.out = saved
	if err != nil {
		return runtime.Null(), err
	}
	if in.escape != "" {
		return runtime.Safe(sub.b.String()), nil
	}
	return runtime.Str(sub.b.String()), nil
}

// filterRecursivePairs pre-selects the pairs whose fused filter condition is
// truthy at this descent level, so the level renders only the survivors and their
// loop.* fields count only them. The condition is evaluated in a child scope with
// the loop target(s) bound to each candidate, mirroring the plain @for fused
// filter (filterLoopPairs); a pruned node's subtree is never descended into, since
// loop(children) is reached only from a surviving node's body.
func (in *interp) filterRecursivePairs(frame *recursiveLoop, pairs []runtime.Pair, ctx *runtime.Scope) ([]runtime.Pair, error) {
	scope := ctx.Child()
	survivors := make([]runtime.Pair, 0, len(pairs))
	for _, p := range pairs {
		if frame.twoTgt {
			scope.Set(frame.target1, p.Key)
			scope.Set(frame.target2, p.Val)
		} else {
			scope.Set(frame.target1, p.Val)
		}
		keep, err := in.eval(frame.filter, scope, false)
		if err != nil {
			return nil, err
		}
		if runtime.Truthy(keep) {
			survivors = append(survivors, p)
		}
	}
	return survivors, nil
}
