// Package covercore holds the engine-internal instrumentation core behind
// template coverage: the region identity model, the RegionKind vocabulary, the
// mutable region map, and the Hit / Seed record-and-seed logic the interpreter
// drives. It is deliberately internal so the record-side entry points (Hit,
// SeedTemplate, SeedMacro) are NOT part of any host-facing surface: hosts see
// only pkg/cover.Collector, which wraps a *Core and exposes the report side.
//
// The split lets pkg/cover keep a small public REPORT API (Report, Summary,
// per-template rollups) while the interpreter (and only the interpreter)
// records hits into the Core. pkg/cover re-exports RegionKind and its constants
// as type aliases so its public report types (Region.Kind) stay unchanged; the
// Core itself never leaves internal/, so a host cannot reach Hit or Seed.
package covercore

import (
	"fmt"
	"sync"

	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
)

// RegionKind names the role a region plays at its line:col position. It is the
// `kind` component of a region's identity, so two branch arms that begin at the
// same position never collide. Unit kinds answer "did this node run?"; branch-arm
// kinds answer "was this specific arm taken?". pkg/cover aliases this type so it
// remains the host-visible kind of a reported Region.
type RegionKind uint8

const (
	// KindInvalid is the zero value and never appears in a recorded region.
	KindInvalid RegionKind = iota

	// Unit kinds record statement / output coverage: whether a node ran.

	// UnitPrint is an interpolation {{ expr }}.
	UnitPrint
	// UnitText is a literal text or verbatim span.
	UnitText
	// UnitSet is a @set or @set = capture assignment.
	UnitSet
	// UnitDo is a @do expression-for-effect.
	UnitDo
	// UnitWith is a @with body entered.
	UnitWith
	// UnitApply is a @apply body captured and filtered.
	UnitApply
	// UnitEscape is a @escape region body entered.
	UnitEscape
	// UnitSandbox is a @sandbox region body entered.
	UnitSandbox
	// UnitCache is a @cache region body entered (miss path renders it).
	UnitCache
	// UnitGuardTag is a @guard construct reached (its arms record present/absent).
	UnitGuardTag
	// UnitInclude is a @include resolved and rendered.
	UnitInclude
	// UnitEmbed is a @embed resolved and rendered.
	UnitEmbed
	// UnitBlock is a @block render site rendered.
	UnitBlock
	// UnitMacro is a @macro body invoked at least once.
	UnitMacro
	// UnitIf is a @if construct reached (its clause arms record taken/not-taken).
	UnitIf
	// UnitFor is a @for construct reached (its arms record body/empty).
	UnitFor
	// UnitLog is a @log statement executed: it evaluated its expression and wrote
	// to the host logger. It is a unit with no branch arms.
	UnitLog
	// UnitTabBlock is a @tab region body entered and indented.
	UnitTabBlock
	// UnitProvide is a @provide body rendered and appended to its slot.
	UnitProvide
	// UnitYield is a @yield that emitted a slot's accumulated content.
	UnitYield
	// UnitCallBlock is a @call block that invoked its macro with a caller() body.
	UnitCallBlock

	// Branch-arm kinds record branch coverage: which specific arm was taken.

	// IfThen marks an @if/@elseif clause condition that was truthy and whose body ran.
	IfThen
	// IfNotTaken marks an @if/@elseif clause condition that evaluated false.
	IfNotTaken
	// IfElse marks a terminal @else clause body that ran.
	IfElse
	// ForBody marks a @for loop that entered its body (>=1 pair).
	ForBody
	// ForEmpty marks a @for that drained to zero pairs (@else body or nothing ran).
	ForEmpty
	// TernThen marks a ternary / postfix-if then arm (Child 1) taken.
	TernThen
	// TernElse marks a ternary / postfix-if else arm (Child 2) taken.
	TernElse
	// ElvisLeft marks an elvis a?:b that kept the left (truthy).
	ElvisLeft
	// ElvisRight marks an elvis a?:b that used the right fallback.
	ElvisRight
	// CoalLeft marks a coalesce a??b that kept the left (non-null).
	CoalLeft
	// CoalRight marks a coalesce a??b that used the right fallback (left null).
	CoalRight
	// GuardYes marks a @guard callable present: the guarded body ran.
	GuardYes
	// GuardNo marks a @guard callable absent: the @else body ran (or nothing).
	GuardNo
)

// IsBranchArm reports whether a kind is a branch arm (as opposed to a unit).
// Line/percentage math counts units and arms in separate denominators.
func (k RegionKind) IsBranchArm() bool { return k >= IfThen }

// String returns a short stable label used in reports and tests.
func (k RegionKind) String() string {
	if int(k) < len(kindLabels) && kindLabels[k] != "" {
		return kindLabels[k]
	}
	return "invalid"
}

var kindLabels = [...]string{
	UnitPrint:     "Print",
	UnitText:      "Text",
	UnitSet:       "Set",
	UnitDo:        "Do",
	UnitWith:      "With",
	UnitApply:     "Apply",
	UnitEscape:    "Escape",
	UnitSandbox:   "Sandbox",
	UnitCache:     "Cache",
	UnitGuardTag:  "Guard",
	UnitInclude:   "Include",
	UnitEmbed:     "Embed",
	UnitBlock:     "Block",
	UnitMacro:     "Macro",
	UnitIf:        "If",
	UnitFor:       "For",
	UnitLog:       "Log",
	UnitTabBlock:  "TabBlock",
	UnitProvide:   "Provide",
	UnitYield:     "Yield",
	UnitCallBlock: "CallBlock",

	IfThen:     "if-then",
	IfNotTaken: "if-else",
	IfElse:     "else",
	ForBody:    "for-body",
	ForEmpty:   "for-empty",
	TernThen:   "ternary-then",
	TernElse:   "ternary-else",
	ElvisLeft:  "elvis-left",
	ElvisRight: "elvis-right",
	CoalLeft:   "coalesce-left",
	CoalRight:  "coalesce-right",
	GuardYes:   "guard-present",
	GuardNo:    "guard-absent",
}

// RegionID is the stable identity of a region: template-name:line:col:kind. Two
// arms of the same branch point share Line:Col but differ in Kind, so they never
// collide; a unit and its branch arms at the same position likewise differ by
// Kind. The struct is comparable, so it is a valid map key. pkg/cover keys its
// report snapshot on this type.
type RegionID struct {
	Tmpl string
	Line int
	Col  int
	Kind RegionKind
}

func (id RegionID) String() string {
	return fmt.Sprintf("%s:%d:%d:%s", id.Tmpl, id.Line, id.Col, id.Kind)
}

// regionData is the recorded state of one region: its hit counter. Hit is a
// monotonic counter, not a boolean, so LCOV/HTML can show real execution counts.
type regionData struct {
	hit int64
}

// macroSeedID identifies one macro subtree that has been seeded under a template
// so SeedMacro is idempotent per macro without marking the whole module seeded.
type macroSeedID struct {
	tmpl string
	line int
	col  int
}

// Core is the instrumentation state behind one Collector: the mutable region map
// unioned across every render, plus the per-template seed bookkeeping and source
// capture. It is the record side of coverage (the interpreter calls Hit and the
// Seed methods on it) and is kept internal so those entry points never reach a
// host. pkg/cover.Collector wraps a *Core and exposes only the report side.
//
// A Core is safe for sequential renders on one Environment; concurrent renders
// should each use their own Collector and be combined with MergeReports (the
// mutex guards against accidental races but is not a substitute for per-goroutine
// Collectors).
type Core struct {
	mu sync.Mutex
	// regions maps a region id to its data, unioned across every render.
	regions map[RegionID]*regionData
	// seededFull tracks templates whose WHOLE module has been statically seeded
	// (SeedTemplate) so a full re-seed is a no-op. seededMacro tracks per-macro
	// subtree seeds (SeedMacro) so re-seeding the same macro is a no-op. The two
	// are separate because a template can be entered first only as a macro home
	// (partial seed) and later as a render root / @include / @extends target (full
	// seed): the macro flag must NOT suppress the later full seed, and vice versa.
	seededFull  map[string]bool
	seededMacro map[macroSeedID]bool
	// sources records each template's raw source text (captured at seed time) so
	// the HTML report can render annotated source. It is keyed by template name.
	sources map[string]string
}

// New returns an empty Core ready to record hits and seeds.
func New() *Core {
	return &Core{
		regions:     map[RegionID]*regionData{},
		seededFull:  map[string]bool{},
		seededMacro: map[macroSeedID]bool{},
		sources:     map[string]string{},
	}
}

// region returns the regionData for an id, creating a zero-count entry when the
// id is new. The caller holds c.mu.
func (c *Core) region(id RegionID) *regionData {
	if rd, ok := c.regions[id]; ok {
		return rd
	}
	rd := &regionData{}
	c.regions[id] = rd
	return rd
}

// Hit records one execution of the region for node n with the given kind. A nil
// node or nil Core is a no-op, so callers guard only on the Core being present.
// The region is created on first sight (so a hit without a prior seed still
// counts), which keeps recording independent of seeding order. It is the entry
// the interpreter's coverage hooks call at each coverable dispatch point.
func (c *Core) Hit(name string, n *ast.Node, kind RegionKind) {
	if c == nil || n == nil {
		return
	}
	c.mu.Lock()
	c.region(RegionID{Tmpl: name, Line: n.Line, Col: n.Col, Kind: kind}).hit++
	c.mu.Unlock()
}

// seed registers, without incrementing, a region for node n under the given
// kind. It is how the static pre-render walk records a coverable node as reachable
// so an unreached region reports 0 rather than being absent. The caller holds
// c.mu.
func (c *Core) seed(name string, n *ast.Node, kind RegionKind) {
	if n == nil {
		return
	}
	c.region(RegionID{Tmpl: name, Line: n.Line, Col: n.Col, Kind: kind})
}

// SeedTemplate statically walks a template's module AST once, registering every
// coverable node (and every branch arm) as a zero-count region under name. It is
// idempotent: a template already seeded is skipped, so calling it before each
// render is cheap and never double-counts. A nil Core is a no-op.
//
// Boundary: SeedTemplate registers whole-template regions, but the engine only
// calls it for a template that is actually ENTERED at render time: the render
// root and its inheritance chain, an executed @include/@embed target, and a
// macro home at the point one of its macros is invoked. Consequently seeding is
// reachability-gated at template granularity: a template that is merely
// referenced but never entered (macros imported yet never called, or an @include
// whose statement never executes because it sits in a never-taken @if arm) is
// never seeded and is ABSENT from the report rather than shown at 0%. This
// deliberately keeps the report to code the render pipeline could reach; it does
// mean an unexercised partial does not, on its own, drag the denominator down.
// Callers that want a referenced-but-unentered template to report 0% must seed
// it explicitly (walk the reference graph and call SeedTemplate for each target).
func (c *Core) SeedTemplate(name string, module *ast.Node) {
	if c == nil || module == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seededFull[name] {
		return
	}
	c.seededFull[name] = true
	if module.Src != nil {
		c.sources[name] = module.Src.Code()
	}
	seedWalk(c, name, module)
}

// SeedMacro seeds only the coverable regions reachable through a macro invocation
// into template name: the invoked macro's own subtree. Unlike SeedTemplate it does
// NOT seed the template's top-level statement body, because a template entered
// solely as a MACRO HOME (its macros invoked via @import / @from) never renders
// its top-level markup: that text and those statements are unreachable in the
// import context, so seeding them would report unreachable code as an uncovered
// gap and distort the percentage (docs/coverage.md 2.2).
//
// It is idempotent per macro: re-invoking the same macro across renders re-seeds
// nothing. A template can be seeded by SeedMacro (for its imported macros) and,
// separately, fully seeded by SeedTemplate if it is ALSO entered as a render root,
// @include, @embed, or @extends target. The two seed maps are independent so a
// macro-home seed never suppresses a later full seed, and a full seed already
// covers every macro subtree so a subsequent SeedMacro is a harmless no-op.
//
// macroNode is the ast.KindMacro node being invoked; a nil Core, module, or node
// is a no-op.
func (c *Core) SeedMacro(name string, module, macroNode *ast.Node) {
	if c == nil || module == nil || macroNode == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A full seed already registered every macro subtree; nothing to add.
	if c.seededFull[name] {
		return
	}
	// Capture source once so the HTML report can annotate a macro-home partial even
	// when only its macros were entered.
	if _, ok := c.sources[name]; !ok && module.Src != nil {
		c.sources[name] = module.Src.Code()
	}
	id := macroSeedID{tmpl: name, line: macroNode.Line, col: macroNode.Col}
	if c.seededMacro[id] {
		return
	}
	c.seededMacro[id] = true
	seedWalk(c, name, macroNode)
}

// Snapshot returns an immutable copy of the accumulated coverage: a region->hits
// map and a per-template source map. Later renders on the Core do not mutate an
// already-returned snapshot. It is the seam pkg/cover.Collector.Report consumes to
// build a Report without reaching into the Core's mutable state.
func (c *Core) Snapshot() (map[RegionID]int64, map[string]string) {
	if c == nil {
		return map[RegionID]int64{}, map[string]string{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := make(map[RegionID]int64, len(c.regions))
	for id, rd := range c.regions {
		snap[id] = rd.hit
	}
	src := make(map[string]string, len(c.sources))
	for k, v := range c.sources {
		src[k] = v
	}
	return snap, src
}
