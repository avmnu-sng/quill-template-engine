// Package sandbox is Quill's host-supplied security policy: five allowlists
// (tags, filters, functions, per-type methods, per-type properties) plus the
// host TYPE-GRAPH that powers the per-type member lookups. It is the engine's
// realization of the spec's SecurityPolicy (spec 04 Section 8.3,
// design/escaping-safety Section 6).
//
// The policy is purely declarative data the host builds; the interpreter does
// the enforcement (a per-render check of the statically-collected tags/filters/
// functions, plus runtime member-access checks at each access site). Method-name
// matching is CASE-SENSITIVE, as are property, filter, function, and tag names.
// Allowlisting is uniform: every tag and function is subject to the same
// allowlist with none exempt, so a policy that wants inheritance must list
// extends, use, and block explicitly (B6).
//
// The TYPE-GRAPH drives method/property matching: the host declares each Object
// type's name and its supertypes/interfaces, and a per-type lookup walks that
// declared graph so an allowlist entry on a base type or interface covers every
// registered subtype/implementor (B4). The same graph is intended to back the
// gradual type checker's Object<"Type"> matching, so one host registration
// serves both security and typing.
package sandbox

// TypeGraph records each host Object type's declared supertypes and interfaces,
// so a per-type allowlist entry on a base type or interface covers all
// registered subtypes (B4). It is keyed by the host's registered type name (the
// ClassName a host Object reports). A type with no registered parents matches
// only its own name.
type TypeGraph struct {
	// parents maps a type name to its direct declared supertypes/interfaces. The
	// closure (a type plus all ancestors) is computed on demand by ancestors.
	parents map[string][]string
}

// NewTypeGraph returns an empty graph.
func NewTypeGraph() *TypeGraph { return &TypeGraph{parents: map[string][]string{}} }

// Declare records that typeName directly extends/implements the given supers.
// Repeated calls accumulate, so a host may declare a base and its interfaces in
// separate statements. The graph stores only the declared edges; the transitive
// closure is walked at match time.
func (g *TypeGraph) Declare(typeName string, supers ...string) {
	if g.parents == nil {
		g.parents = map[string][]string{}
	}
	g.parents[typeName] = append(g.parents[typeName], supers...)
}

// ancestors returns typeName followed by every transitive supertype/interface,
// most-derived first, with a visited guard so a cyclic declaration cannot loop.
// The receiver type itself is always first so a direct-name allowlist entry
// wins before any inherited one.
func (g *TypeGraph) ancestors(typeName string) []string {
	out := []string{typeName}
	seen := map[string]bool{typeName: true}
	queue := []string{typeName}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if g == nil {
			break
		}
		for _, p := range g.parents[cur] {
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
			queue = append(queue, p)
		}
	}
	return out
}

// Policy is the host-supplied sandbox security policy: five allowlists and a
// mode flag, plus the type-graph the per-type lookups walk (spec 04 Section
// 8.3, design/escaping-safety Section 6.1, B1-B6, B17). A nil Policy denies
// nothing because enforcement is gated on the sandbox being active; an active
// sandbox with a zero-value Policy denies everything (uniform allowlisting).
type Policy struct {
	// Tags is the set of allowed statement keywords (for, if, block, include,
	// macro, ...). A used keyword outside the set is a SecurityTag violation (B1).
	Tags map[string]bool
	// Filters is the set of allowed filter names (B2).
	Filters map[string]bool
	// Functions is the set of allowed function names; the range operator `..`
	// counts as the range function (B3, B8).
	Functions map[string]bool
	// Methods maps a host type name to the method names allowed on it, matched
	// across the type-graph (B4). The method name is matched case-sensitively in
	// the host's real spelling.
	Methods map[string]map[string]bool
	// Properties maps a host type name to the property/field names allowed on it,
	// matched across the type-graph (B5).
	Properties map[string]map[string]bool
	// Strict selects strict-versus-lenient member-access reporting (spec 04
	// Section 8.3). In strict mode, a member access on a host type the policy does
	// not know at all -- no method or property allowlist entry and absent from the
	// type-graph -- reports a distinct unknown-type error (the interp consults
	// Knows). In lenient mode that same access falls through to the ordinary
	// per-member deny. The tag/filter/function floor is uniform in both modes, with
	// no grandfathering (B6).
	Strict bool
	// Graph is the host type-graph backing the per-type method/property lookups.
	// A nil graph matches each type only by its own name.
	Graph *TypeGraph
}

// AllowsTag reports whether the statement keyword is permitted (B1).
func (p *Policy) AllowsTag(tag string) bool { return p != nil && p.Tags[tag] }

// AllowsFilter reports whether the filter name is permitted (B2).
func (p *Policy) AllowsFilter(name string) bool { return p != nil && p.Filters[name] }

// AllowsFunction reports whether the function name is permitted (B3).
func (p *Policy) AllowsFunction(name string) bool { return p != nil && p.Functions[name] }

// AllowsMethod reports whether method is permitted on the named host type,
// walking the type-graph so an entry on a base type or interface covers a
// registered subtype (B4). Matching is case-sensitive.
func (p *Policy) AllowsMethod(typeName, method string) bool {
	if p == nil {
		return false
	}
	for _, t := range p.Graph.ancestors(typeName) {
		if p.Methods[t][method] {
			return true
		}
	}
	return false
}

// AllowsProperty reports whether the property/field read is permitted on the
// named host type, walking the type-graph as AllowsMethod does (B5).
func (p *Policy) AllowsProperty(typeName, prop string) bool {
	if p == nil {
		return false
	}
	for _, t := range p.Graph.ancestors(typeName) {
		if p.Properties[t][prop] {
			return true
		}
	}
	return false
}

// Knows reports whether the policy has any declared knowledge of the host type:
// a method or property allowlist entry on the type or any of its declared
// ancestors, or an edge in the type-graph. Strict mode uses this to distinguish
// an unregistered/typo type (an unknown-type error) from a known type whose
// specific member is merely not allowlisted (a per-member deny). A nil policy
// knows nothing.
func (p *Policy) Knows(typeName string) bool {
	if p == nil {
		return false
	}
	for _, t := range p.Graph.ancestors(typeName) {
		if len(p.Methods[t]) > 0 || len(p.Properties[t]) > 0 {
			return true
		}
		if p.Graph != nil && len(p.Graph.parents[t]) > 0 {
			return true
		}
	}
	return false
}
