package compile

import "github.com/avmnu-sng/quill-template-engine/core/ast"

// bindNames prescans a statement list for every name it binds in ITS OWN
// frame, in first-appearance order. The set mirrors where the interpreter's
// Scope.Set/SetOwned would land a binding in the frame that runs the list:
// plain and destructuring @set targets, member-assignment roots (the ownPath
// rebind), @set capture names, inline expression assignments, and the names a
// nested @for would copy back into this frame. Names bound by constructs that
// push their own frame (@for bodies, @with bodies, fused-filter clauses,
// arrow bodies) stay in that frame and are excluded.
func bindNames(items []*ast.Node) []string {
	var order []string
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		order = append(order, name)
	}
	for _, it := range items {
		stmtBinds(it, add)
	}
	return order
}

// stmtBinds feeds add every name statement n binds in the current frame.
func stmtBinds(n *ast.Node, add func(string)) {
	if n == nil {
		return
	}
	switch n.Kind {
	case ast.KindSet:
		if multiSetPattern(n) {
			// The lowering rejects this form as not compilable, so the prescan
			// binds none of its names; scanning them anyway could hand later
			// statements bindings the abort never creates.
			return
		}
		count := n.IntCount()
		for i, tg := range n.Children {
			if i < count {
				targetBinds(tg, add)
				continue
			}
			exprBinds(tg, add)
		}
	case ast.KindCapture:
		add(n.Str)
		for _, it := range n.Children {
			if it.Kind == ast.KindType {
				continue
			}
			stmtBinds(it, add)
		}
	case ast.KindIf:
		for _, cl := range n.Children {
			body := cl.Children
			if cl.Bool {
				exprBinds(cl.Child(0), add)
				body = cl.Children[1:]
			}
			for _, it := range body {
				stmtBinds(it, add)
			}
		}
	case ast.KindFor:
		forBinds(n, add)
	case ast.KindWith:
		// The with body binds in the with frame; only the map expression can
		// bind here.
		exprBinds(n.Child(0), add)
	case ast.KindTabBlock:
		exprBinds(n.Child(0), add)
		for _, it := range n.Children[1:] {
			stmtBinds(it, add)
		}
	case ast.KindEscape:
		for _, it := range n.Children {
			stmtBinds(it, add)
		}
	case ast.KindApply:
		// The apply body captures with the enclosing scope, exactly like
		// captureItems, so a @set inside it copies back into this frame and its
		// names must be scanned here. The leading filter nodes bind no names of
		// their own; only their argument expressions can carry an inline
		// assignment, which exprBinds finds by recursion.
		filterCount := n.IntCount()
		for _, f := range n.Children[:filterCount] {
			exprBinds(f, add)
		}
		for _, it := range n.Children[filterCount:] {
			stmtBinds(it, add)
		}
	case ast.KindProvide:
		// A @provide body captures with the enclosing scope like captureItems,
		// so a @set inside it copies back into this frame and its names must be
		// scanned here, exactly as for @apply and @set...capture.
		for _, it := range n.Children {
			stmtBinds(it, add)
		}
	case ast.KindPrint, ast.KindDo, ast.KindLog:
		exprBinds(n.Child(0), add)
	default:
		// Text, comments, and non-compilable statements bind nothing here;
		// the latter abort compilation before their bindings could matter.
	}
}

// forBinds feeds add the names a @for statement binds in its PARENT frame:
// names its iterand or else-body bind directly, plus the copy-back set (every
// name the body frame binds except the loop's own control bindings).
func forBinds(n *ast.Node, add func(string)) {
	count := int(n.Int & ast.ForTargetCount)
	target1 := n.Child(0)
	var target2 *ast.Node
	idx := 1
	if count == 2 {
		target2 = n.Child(1)
		idx = 2
	}
	exprBinds(n.Child(idx), add)

	bodyIdx := idx + 1
	var filter *ast.Node
	if fc := n.Child(bodyIdx); fc != nil && fc.Kind == ast.KindClause {
		filter = fc
		bodyIdx++
	}
	_ = filter // filter-clause binds land in the filter frame
	body := n.Child(bodyIdx)
	var elseBody *ast.Node
	if n.Bool {
		elseBody = n.Child(bodyIdx + 1)
	}

	if body != nil {
		excluded := map[string]bool{"loop": true}
		if target1 != nil {
			excluded[target1.Str] = true
		}
		if target2 != nil {
			excluded[target2.Str] = true
		}
		for _, name := range bindNames(body.Children) {
			if !excluded[name] {
				add(name)
			}
		}
	}
	if elseBody != nil {
		for _, it := range elseBody.Children {
			stmtBinds(it, add)
		}
	}
}

// hasTabBlock reports whether the subtree at n contains a @tab region. A
// module without one never activates the qWriter indent layer, so its writes
// lower straight to the underlying io.Writer.
func hasTabBlock(n *ast.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == ast.KindTabBlock {
		return true
	}
	for _, ch := range n.Children {
		if hasTabBlock(ch) {
			return true
		}
	}
	return false
}

// multiSetPattern reports whether a @set statement places a destructuring
// pattern among MULTIPLE targets (e.g. "@set [a], b = [1], 2"). The
// interpreter currently misbinds this form (a recorded engine bug), so the
// backend rejects it as outside the compilable subset; both the prescan and
// the lowering consult this so neither path reaches a nameless binding.
func multiSetPattern(n *ast.Node) bool {
	count := n.IntCount()
	if count <= 1 {
		return false
	}
	for i := 0; i < count && i < len(n.Children); i++ {
		tg := n.Children[i]
		if tg != nil && (tg.Kind == ast.KindListPattern || tg.Kind == ast.KindMapPattern) {
			return true
		}
	}
	return false
}

// targetBinds feeds add every name one @set target binds: a plain name, a
// member-assignment root (rebound by the ownPath privatization), or every
// slot of a destructuring pattern.
func targetBinds(tg *ast.Node, add func(string)) {
	if tg == nil {
		return
	}
	switch tg.Kind {
	case ast.KindTarget, ast.KindName:
		add(tg.Str)
	case ast.KindAttr, ast.KindIndex:
		targetBinds(rootOf(tg), add)
		// Index keys along the path may carry inline assignments.
		memberKeyBinds(tg, add)
	case ast.KindListPattern:
		for _, slot := range tg.Children {
			patternSlotBinds(slot, add)
		}
	case ast.KindMapPattern:
		for _, slot := range tg.Children {
			if slot == nil || slot.Kind != ast.KindMapTarget {
				continue
			}
			if slot.Bool {
				add(slot.Child(0).Str)
				continue
			}
			add(slot.Str)
		}
	}
}

// patternSlotBinds feeds add the names one sequence-pattern slot binds.
func patternSlotBinds(slot *ast.Node, add func(string)) {
	if slot == nil {
		return
	}
	switch slot.Kind {
	case ast.KindOptional:
		patternSlotBinds(slot.Child(0), add)
	case ast.KindSpread:
		add(slot.Child(0).Str)
	default:
		targetBinds(slot, add)
	}
}

// rootOf walks a member-assignment path to its root node.
func rootOf(n *ast.Node) *ast.Node {
	for n != nil && (n.Kind == ast.KindAttr || n.Kind == ast.KindIndex) {
		n = n.Child(0)
	}
	return n
}

// memberKeyBinds walks the index keys of a member-assignment path for inline
// expression assignments.
func memberKeyBinds(n *ast.Node, add func(string)) {
	for n != nil && (n.Kind == ast.KindAttr || n.Kind == ast.KindIndex) {
		if n.Kind == ast.KindIndex {
			exprBinds(n.Child(1), add)
		}
		n = n.Child(0)
	}
}

// exprBinds feeds add every name an expression binds in the current frame via
// an inline assignment. Arrow bodies bind in the arrow's own invocation frame
// and are skipped.
func exprBinds(n *ast.Node, add func(string)) {
	if n == nil {
		return
	}
	if n.Kind == ast.KindArrow {
		return
	}
	if n.Kind == ast.KindAssign {
		tg := n.Child(0)
		if tg != nil && (tg.Kind == ast.KindName || tg.Kind == ast.KindTarget) {
			add(tg.Str)
		}
	}
	for _, ch := range n.Children {
		exprBinds(ch, add)
	}
}
