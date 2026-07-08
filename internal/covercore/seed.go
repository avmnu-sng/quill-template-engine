package covercore

import "github.com/avmnu-sng/quill-template-engine/pkg/ast"

// seedWalk registers every coverable region of a module AST as a zero-count
// entry, so a region that no render reaches still appears in the report (the
// denominator is the whole template, not just what ran). It mirrors the shape of
// the interpreter's own tree walk: the same node kinds the interp records a hit
// for are the ones seeded here, plus the branch arms that hang off @if / @for /
// ternary / elvis / coalesce / @guard. Declaration-only heads (@extends, @import,
// @from, @use, @types, @line, @deprecated) are NOT units and are not seeded as
// such; their expression subtrees are still walked so a ternary inside, say, an
// include source is still measured.
//
// The walk is intentionally exhaustive over both statement bodies and expression
// subtrees: a branch operator can appear anywhere an expression can (a ternary in
// a print, in a filter argument, in a @set value), so every child is visited.
func seedWalk(c *Core, name string, n *ast.Node) {
	if n == nil {
		return
	}

	// Record the node's own unit and any branch arms it introduces.
	seedNode(c, name, n)

	// Recurse into children. Statement bodies and expression subtrees alike are
	// covered by walking every child, so a branch nested anywhere is seeded.
	for _, ch := range n.Children {
		seedWalk(c, name, ch)
	}
}

// seedNode seeds the unit and branch-arm regions a single node contributes,
// without recursing (seedWalk handles recursion). It is the seeding counterpart
// of the interpreter's per-node hit calls, so the two stay in lockstep: a node
// the interp records a hit for is seeded here under the same kind.
func seedNode(c *Core, name string, n *ast.Node) {
	switch n.Kind {
	case ast.KindText, ast.KindVerbatim:
		c.seed(name, n, UnitText)
	case ast.KindPrint:
		c.seed(name, n, UnitPrint)
	case ast.KindSet, ast.KindCapture:
		c.seed(name, n, UnitSet)
	case ast.KindDo:
		c.seed(name, n, UnitDo)
	case ast.KindWith:
		c.seed(name, n, UnitWith)
	case ast.KindApply:
		c.seed(name, n, UnitApply)
	case ast.KindEscape:
		c.seed(name, n, UnitEscape)
	case ast.KindSandbox:
		c.seed(name, n, UnitSandbox)
	case ast.KindCache:
		c.seed(name, n, UnitCache)
	case ast.KindInclude:
		c.seed(name, n, UnitInclude)
	case ast.KindEmbed:
		c.seed(name, n, UnitEmbed)
	case ast.KindBlock:
		c.seed(name, n, UnitBlock)
	case ast.KindMacro:
		c.seed(name, n, UnitMacro)
	case ast.KindLog:
		c.seed(name, n, UnitLog)
	case ast.KindTabBlock:
		c.seed(name, n, UnitTabBlock)
	case ast.KindProvide:
		c.seed(name, n, UnitProvide)
	case ast.KindYield:
		c.seed(name, n, UnitYield)
	case ast.KindCallBlock:
		c.seed(name, n, UnitCallBlock)

	case ast.KindIf:
		c.seed(name, n, UnitIf)
		seedIfArms(c, name, n)
	case ast.KindFor:
		c.seed(name, n, UnitFor)
		c.seed(name, n, ForBody)
		c.seed(name, n, ForEmpty)
	case ast.KindGuard:
		c.seed(name, n, UnitGuardTag)
		c.seed(name, n, GuardYes)
		c.seed(name, n, GuardNo)

	case ast.KindTernary:
		c.seed(name, n, TernThen)
		c.seed(name, n, TernElse)
	case ast.KindElvis:
		c.seed(name, n, ElvisLeft)
		c.seed(name, n, ElvisRight)
	case ast.KindCoalesce:
		c.seed(name, n, CoalLeft)
		c.seed(name, n, CoalRight)
	}
}

// seedIfArms seeds the two arms of each condition-bearing @if/@elseif clause
// (taken and not-taken) and the single arm of a terminal @else. The arms are
// anchored at the CLAUSE position so each clause in a chain gets its own
// line:col, matching how execIf records them.
func seedIfArms(c *Core, name string, ifNode *ast.Node) {
	for _, clause := range ifNode.Children {
		if clause.Kind != ast.KindClause {
			continue
		}
		if clause.Bool { // if / elseif: condition-bearing, two arms
			c.seed(name, clause, IfThen)
			c.seed(name, clause, IfNotTaken)
		} else { // terminal else: one arm
			c.seed(name, clause, IfElse)
		}
	}
}
