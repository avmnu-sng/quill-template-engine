// Package cover measures which parts of a Quill template a set of renders
// actually exercised: which statements and interpolations ran (units), and which
// arms of each branch were taken (branches). It is branch-aware template
// coverage (the analogue of `go tool cover` for .ql templates) aggregated
// across many renders and reported as text, LCOV, or HTML (see docs/coverage.md).
//
// The central type is a Collector. An Environment built with the engine's
// WithCoverage option threads one Collector into the interpreter, which records a
// hit at each coverable point. When no Collector is attached the interpreter's
// coverage field is nil and every hook is a single nil-check, so coverage is
// zero-overhead when disabled and never changes rendered bytes.
//
// A Collector is a thin host-facing wrapper: it exposes only the REPORT side
// (Report and the writers it feeds). The instrumentation core (the region map
// and the record/seed logic the interpreter drives) lives in the internal
// covercore package, so the record-side entry points (formerly Hit / SeedTemplate
// / SeedMacro) are not part of any host-visible surface. The interpreter reaches
// the core through an internal bridge (covercore.CoreOf); a host holding a
// Collector cannot.
//
// The model is two-phase and idempotent. Before (and during) a render each
// template's coverable nodes are SEEDED as zero-count regions by a static AST
// walk, so code WITHIN a rendered template that no render reaches still counts
// against the denominator rather than being silently absent. Rendering only
// increments hit counters. Seeding is keyed by region id, so re-seeding the same
// template across renders is a no-op and hits union across every render on the
// Collector.
//
// Seeding is reachability-gated at TEMPLATE granularity: a template is seeded
// when it is actually entered by a render (the render root and its inheritance
// chain, an executed @include/@embed target, or a macro home whose macro is
// invoked). A template that is only referenced (imported for macros that are
// never called, or an @include whose statement never runs, e.g. inside a
// never-taken @if arm) is never entered and so is ABSENT from the report
// rather than reported at 0%.
//
// The unit of seeding depends on WHY a template was entered. A template entered
// as a render root, an inheritance target, or an executed @include/@embed has its
// top-level body rendered, so the full seed covers the WHOLE module and an untaken
// branch or unreached statement anywhere in it still reports 0. A template entered
// only as a MACRO HOME (its macros invoked via @import/@from) never renders its
// top-level markup, so the macro seed covers only the invoked macro's subtree;
// that template's top-level text and statements are unreachable in the import
// context and are NOT seeded, so they do not appear as spurious uncovered gaps. A
// partial that both renders standalone and exports macros gets whichever seed its
// actual entry warrants, and a later full entry upgrades a macro-home seed. See
// covercore.Core.SeedTemplate and covercore.Core.SeedMacro for the precise
// boundary.
package cover

import "github.com/avmnu-sng/quill-template-engine/internal/covercore"

// RegionKind names the role a region plays at its line:col position (the kind
// component of a region's identity). It is an alias for the internal covercore
// type so the vocabulary is defined once yet remains the host-visible kind of a
// reported Region. Unit kinds answer "did this node run?"; branch-arm kinds
// answer "was this specific arm taken?".
//
// Because RegionKind aliases an internal type, its two methods do not appear in
// this package's godoc: String returns the kind's human-readable name (as shown
// in the verbose text report), and IsBranchArm reports whether the kind is a
// branch arm rather than a unit (Region.Branch exposes the latter for a reported
// region).
type RegionKind = covercore.RegionKind

// The region-kind constants are re-exported from covercore so a host consuming
// Region.Kind can name them (KindInvalid, UnitPrint, IfThen, ...) exactly as
// before, while their single definition lives with the instrumentation core.
const (
	KindInvalid = covercore.KindInvalid

	UnitPrint     = covercore.UnitPrint
	UnitText      = covercore.UnitText
	UnitSet       = covercore.UnitSet
	UnitDo        = covercore.UnitDo
	UnitWith      = covercore.UnitWith
	UnitApply     = covercore.UnitApply
	UnitEscape    = covercore.UnitEscape
	UnitSandbox   = covercore.UnitSandbox
	UnitCache     = covercore.UnitCache
	UnitGuardTag  = covercore.UnitGuardTag
	UnitInclude   = covercore.UnitInclude
	UnitEmbed     = covercore.UnitEmbed
	UnitBlock     = covercore.UnitBlock
	UnitMacro     = covercore.UnitMacro
	UnitIf        = covercore.UnitIf
	UnitFor       = covercore.UnitFor
	UnitLog       = covercore.UnitLog
	UnitTabBlock  = covercore.UnitTabBlock
	UnitProvide   = covercore.UnitProvide
	UnitYield     = covercore.UnitYield
	UnitCallBlock = covercore.UnitCallBlock

	IfThen     = covercore.IfThen
	IfNotTaken = covercore.IfNotTaken
	IfElse     = covercore.IfElse
	ForBody    = covercore.ForBody
	ForEmpty   = covercore.ForEmpty
	TernThen   = covercore.TernThen
	TernElse   = covercore.TernElse
	ElvisLeft  = covercore.ElvisLeft
	ElvisRight = covercore.ElvisRight
	CoalLeft   = covercore.CoalLeft
	CoalRight  = covercore.CoalRight
	GuardYes   = covercore.GuardYes
	GuardNo    = covercore.GuardNo
)

// regionID is the report side's identity for a region: template-name:line:col:kind.
// It is the report snapshot's map key and mirrors covercore.RegionID field for
// field. The report domain keeps its OWN key type (rather than aliasing the
// internal one) so this package's report/writer code and its tests key on a local,
// unexported struct: unkeyed composite literals of it stay vet-clean, and the
// public surface never leaks the internal identity type. Report translates the
// core's snapshot into this key in one place (Collector.Report).
type regionID struct {
	tmpl string
	line int
	col  int
	kind RegionKind
}

func init() {
	// Install the bridge the interpreter uses to reach a Collector's Core. It lives
	// in the internal covercore package, so only in-module callers can invoke it;
	// a host holding a *Collector has no path to the record side.
	covercore.SetCollectorBridge(func(coll any) *covercore.Core {
		c, ok := coll.(*Collector)
		if !ok || c == nil {
			return nil
		}
		return c.core
	})
}

// Collector accumulates coverage across every render on the Environment it is
// attached to. It is the host-facing handle: it wraps the internal instrumentation
// Core (which seeds coverable nodes as zero-count regions and increments a
// region's counter each time the interpreter reaches it) and exposes only the
// report side. A Collector is safe for sequential renders on one Environment;
// concurrent renders should each use their own Collector and be combined with
// MergeReports.
type Collector struct {
	core *covercore.Core
}

// NewCollector returns an empty Collector ready to be passed to the engine's
// WithCoverage option.
func NewCollector() *Collector {
	return &Collector{core: covercore.New()}
}

// Report returns an immutable snapshot of the coverage accumulated so far. Later
// renders on the Collector do not mutate an already-returned Report. It translates
// the core's snapshot (keyed by the internal covercore.RegionID) into this
// package's report key here, the one place the two identity types meet.
func (c *Collector) Report() *Report {
	if c == nil {
		return &Report{}
	}
	coreSnap, src := c.core.Snapshot()
	snap := make(map[regionID]int64, len(coreSnap))
	for id, hits := range coreSnap {
		snap[regionID{tmpl: id.Tmpl, line: id.Line, col: id.Col, kind: id.Kind}] = hits
	}
	return buildReport(snap, src)
}
