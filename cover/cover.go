// Package cover measures which parts of a Quill template a set of renders
// actually exercised: which statements and interpolations ran (units), and which
// arms of each branch were taken (branches). It is branch-aware template
// coverage -- the analogue of `go tool cover` for .ql templates -- aggregated
// across many renders and reported as text, LCOV, or HTML (see docs/coverage.md).
//
// The central type is a Collector. An Environment built with the engine's
// WithCoverage option threads one Collector into the interpreter, which records a
// hit at each coverable point. When no Collector is attached the interpreter's
// coverage field is nil and every hook is a single nil-check, so coverage is
// zero-overhead when disabled and never changes rendered bytes.
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
// invoked). A template that is only referenced -- imported for macros that are
// never called, or an @include whose statement never runs (e.g. inside a
// never-taken @if arm) -- is never entered and so is ABSENT from the report
// rather than reported at 0%. Within a template that IS entered, every region is
// seeded, so an untaken branch or an unreached statement still reports 0. See
// Collector.SeedTemplate for the precise boundary.
package cover

import (
	"fmt"
	"sync"

	"github.com/avmnu-sng/quill-template-engine/ast"
)

// RegionKind names the role a region plays at its line:col position. It is the
// `kind` component of a region's identity, so two branch arms that begin at the
// same position never collide. Unit kinds answer "did this node run?"; branch-arm
// kinds answer "was this specific arm taken?".
type RegionKind uint8

const (
	// KindInvalid is the zero value and never appears in a recorded region.
	KindInvalid RegionKind = iota

	// --- unit kinds (statement / output coverage) ---

	UnitPrint    // an interpolation {{ expr }}
	UnitText     // a literal text or verbatim span
	UnitSet      // @set / @set = capture assignment
	UnitDo       // @do expression-for-effect
	UnitWith     // @with body entered
	UnitApply    // @apply body captured and filtered
	UnitEscape   // @escape region body entered
	UnitSandbox  // @sandbox region body entered
	UnitCache    // @cache region body entered (miss path renders it)
	UnitGuardTag // @guard construct reached (its arms record present/absent)
	UnitInclude  // @include resolved and rendered
	UnitEmbed    // @embed resolved and rendered
	UnitBlock    // @block render site rendered
	UnitMacro    // @macro body invoked at least once
	UnitIf       // @if construct reached (its clause arms record taken/not-taken)
	UnitFor      // @for construct reached (its arms record body/empty)

	// --- branch-arm kinds (branch coverage) ---

	IfThen     // an @if/@elseif clause condition was truthy and its body ran
	IfNotTaken // an @if/@elseif clause condition evaluated false
	IfElse     // a terminal @else clause body ran
	ForBody    // a @for loop entered its body (>=1 pair)
	ForEmpty   // a @for drained to zero pairs (@else body or nothing ran)
	TernThen   // ternary / postfix-if then arm (Child 1) taken
	TernElse   // ternary / postfix-if else arm (Child 2) taken
	ElvisLeft  // elvis a?:b kept the left (truthy)
	ElvisRight // elvis a?:b used the right fallback
	CoalLeft   // coalesce a??b kept the left (non-null)
	CoalRight  // coalesce a??b used the right fallback (left null)
	GuardYes   // @guard callable present: guarded body ran
	GuardNo    // @guard callable absent: @else body ran (or nothing)
)

// isBranchArm reports whether a kind is a branch arm (as opposed to a unit).
// Line/percentage math counts units and arms in separate denominators.
func (k RegionKind) isBranchArm() bool { return k >= IfThen }

// String returns a short stable label used in reports and tests.
func (k RegionKind) String() string {
	if int(k) < len(kindLabels) && kindLabels[k] != "" {
		return kindLabels[k]
	}
	return "invalid"
}

var kindLabels = [...]string{
	UnitPrint:    "Print",
	UnitText:     "Text",
	UnitSet:      "Set",
	UnitDo:       "Do",
	UnitWith:     "With",
	UnitApply:    "Apply",
	UnitEscape:   "Escape",
	UnitSandbox:  "Sandbox",
	UnitCache:    "Cache",
	UnitGuardTag: "Guard",
	UnitInclude:  "Include",
	UnitEmbed:    "Embed",
	UnitBlock:    "Block",
	UnitMacro:    "Macro",
	UnitIf:       "If",
	UnitFor:      "For",

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

// regionID is the stable identity of a region: template-name:line:col:kind. Two
// arms of the same branch point share line:col but differ in kind, so they never
// collide; a unit and its branch arms at the same position likewise differ by
// kind. The struct is comparable, so it is a valid map key.
type regionID struct {
	tmpl string
	line int
	col  int
	kind RegionKind
}

func (id regionID) String() string {
	return fmt.Sprintf("%s:%d:%d:%s", id.tmpl, id.line, id.col, id.kind)
}

// regionData is the recorded state of one region: its hit counter and whether it
// is a branch arm (so the report tallies units and branches separately). Hit is a
// monotonic counter, not a boolean, so LCOV/HTML can show real execution counts.
type regionData struct {
	id  regionID
	hit int64
}

// Collector accumulates coverage across every render on the Environment it is
// attached to. It seeds coverable nodes as zero-count regions (so unreached code
// counts against the denominator) and increments a region's counter each time the
// interpreter reaches it. A Collector is safe for sequential renders on one
// Environment; concurrent renders should each use their own Collector and be
// combined with MergeReports (the internal mutex guards against accidental
// races but is not a substitute for per-goroutine Collectors).
type Collector struct {
	mu sync.Mutex
	// regions maps a region id to its data, unioned across every render. seeded
	// tracks which templates have been statically seeded so re-seeding is a no-op.
	regions map[regionID]*regionData
	seeded  map[string]bool
	// sources records each template's raw source text (captured at seed time) so
	// the HTML report can render annotated source. It is keyed by template name.
	sources map[string]string
}

// NewCollector returns an empty Collector ready to be passed to the engine's
// WithCoverage option.
func NewCollector() *Collector {
	return &Collector{
		regions: map[regionID]*regionData{},
		seeded:  map[string]bool{},
		sources: map[string]string{},
	}
}

// region returns the regionData for an id, creating a zero-count entry when the
// id is new. The caller holds c.mu.
func (c *Collector) region(id regionID) *regionData {
	if rd, ok := c.regions[id]; ok {
		return rd
	}
	rd := &regionData{id: id}
	c.regions[id] = rd
	return rd
}

// Hit records one execution of the region for node n with the given kind. A nil
// node or nil Collector is a no-op, so callers guard only on the Collector being
// present. The region is created on first sight (so a hit without a prior seed
// still counts), which keeps recording independent of seeding order. It is the
// entry the interpreter's coverage hooks call at each coverable dispatch point.
func (c *Collector) Hit(name string, n *ast.Node, kind RegionKind) {
	if c == nil || n == nil {
		return
	}
	c.mu.Lock()
	c.region(regionID{tmpl: name, line: n.Line, col: n.Col, kind: kind}).hit++
	c.mu.Unlock()
}

// seed registers, without incrementing, a region for node n under the given
// kind. It is how the static pre-render walk records a coverable node as reachable
// so an unreached region reports 0 rather than being absent. The caller holds
// c.mu.
func (c *Collector) seed(name string, n *ast.Node, kind RegionKind) {
	if n == nil {
		return
	}
	c.region(regionID{tmpl: name, line: n.Line, col: n.Col, kind: kind})
}

// SeedTemplate statically walks a template's module AST once, registering every
// coverable node (and every branch arm) as a zero-count region under name. It is
// idempotent: a template already seeded is skipped, so calling it before each
// render is cheap and never double-counts. A nil Collector is a no-op.
//
// Boundary: SeedTemplate registers whole-template regions, but the engine only
// calls it for a template that is actually ENTERED at render time -- the render
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
func (c *Collector) SeedTemplate(name string, module *ast.Node) {
	if c == nil || module == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seeded[name] {
		return
	}
	c.seeded[name] = true
	if module.Src != nil {
		c.sources[name] = module.Src.Code()
	}
	seedWalk(c, name, module)
}

// Report returns an immutable snapshot of the coverage accumulated so far. Later
// renders on the Collector do not mutate an already-returned Report.
func (c *Collector) Report() *Report {
	if c == nil {
		return &Report{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := make(map[regionID]int64, len(c.regions))
	for id, rd := range c.regions {
		snap[id] = rd.hit
	}
	src := make(map[string]string, len(c.sources))
	for k, v := range c.sources {
		src[k] = v
	}
	return buildReport(snap, src)
}
