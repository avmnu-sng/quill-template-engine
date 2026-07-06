package interp

import (
	"github.com/avmnu-sng/quill-template-engine/core/ast"
	"github.com/avmnu-sng/quill-template-engine/cover"
)

// This file holds the coverage instrumentation hooks the interpreter calls at
// each coverable dispatch point. Every hook is guarded on in.cov being non-nil,
// so when coverage is off (the default) each is a single nil comparison and adds
// no per-node cost on the render hot path -- the zero-overhead-when-disabled
// guarantee (docs/coverage.md Section 6). The hooks only read a node's position
// and increment a counter; they never touch the value pipeline or the output
// sink, so instrumentation cannot change rendered bytes (the binding invariant).

// covSeed statically seeds a template's coverable regions before its body runs,
// so unreached code counts against the denominator. It is idempotent per template
// name (the Collector skips an already-seeded template), so seeding each template
// as it enters a render is cheap and safe to repeat across renders.
func (in *interp) covSeed(t *Template) {
	if in.cov == nil || t == nil {
		return
	}
	in.cov.SeedTemplate(t.Name, t.Module)
}

// covSeedMacro seeds only the invoked macro's subtree in its home template, used
// when a template is entered ONLY as a macro home (its macro invoked via @import /
// @from). Unlike covSeed it does not seed the home's top-level body, which an
// import never renders, so unreachable top-level markup is not reported as an
// uncovered gap. It is idempotent per macro and a nil Collector is a no-op.
func (in *interp) covSeedMacro(home *Template, macroNode *ast.Node) {
	if in.cov == nil || home == nil || macroNode == nil {
		return
	}
	in.cov.SeedMacro(home.Name, home.Module, macroNode)
}

// covUnit records that the interpreter dispatched node n as a coverable unit of
// the given kind. The region is anchored under n's own source name, so a node
// from an included partial or a macro home counts under that template, not the
// render root.
func (in *interp) covUnit(n *ast.Node, kind cover.RegionKind) {
	if in.cov == nil {
		return
	}
	in.cov.Hit(covName(in, n), n, kind)
}

// covArm records that a specific branch arm at node n was taken. It shares the
// unit path but names a branch-arm kind, which the report tallies in the separate
// branch denominator.
func (in *interp) covArm(n *ast.Node, kind cover.RegionKind) {
	if in.cov == nil {
		return
	}
	in.cov.Hit(covName(in, n), n, kind)
}

// covName is the template name a region is keyed under: the node's own source
// name when present, else the render root's name as a fallback for synthetic
// nodes that carry no Src.
func covName(in *interp, n *ast.Node) string {
	if n != nil && n.Src != nil {
		if name := n.Src.Name(); name != "" {
			return name
		}
	}
	if in.root != nil {
		return in.root.Name
	}
	return ""
}
