// Package sandbox is Quill's host-supplied security policy: five allowlists
// (tags, filters, functions, per-type methods, per-type properties) plus the
// host TYPE-GRAPH that powers the per-type member lookups. It is the engine's
// realization of the spec's SecurityPolicy (spec 04 Section 8.3,
// design/escaping-safety Section 6).
//
// A host builds a Policy with NewPolicy and the functional AllowTags,
// AllowFilters, AllowFunctions, AllowMethods, AllowProperties, Strict, and
// WithTypeGraph options; the Policy's allowlists are otherwise opaque and are
// consulted only through its Allows* / Knows / Strict accessors:
//
//	pol := sandbox.NewPolicy(
//		sandbox.AllowTags("for", "if"),
//		sandbox.AllowFilters("upper"),
//		sandbox.AllowMethods("Entity", "Stringify"),
//		sandbox.Strict(),
//		sandbox.WithTypeGraph(g),
//	)
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
//
// A TypeGraph is built by the host before use: call Declare to add edges, then
// install it in a Policy with WithTypeGraph. After install the graph must be
// treated as read-only; in that state the concurrent reads that happen when
// renders consult the owning Policy (via AllowsMethod/AllowsProperty/Knows) are
// safe. Declare is not safe to call concurrently with itself or with any render
// whose Policy references this graph.
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
// 8.3, design/escaping-safety Section 6.1, B1-B6, B17). It is opaque: build one
// with NewPolicy and the Allow*/Strict/WithTypeGraph options, then consult it
// through its Allows* / Knows / Strict accessors. A nil Policy denies nothing
// because enforcement is gated on the sandbox being active; an active sandbox
// with an empty Policy (NewPolicy()) denies everything (uniform allowlisting).
//
// A Policy is built once (NewPolicy plus its options) and is thereafter immutable
// and safe for unlimited concurrent reads: the interpreter consults the single
// installed Policy across concurrent renders without locking. Do not mutate a
// Policy after installing it with WithSandboxPolicy, and do not call Declare on a
// TypeGraph passed to WithTypeGraph after installation.
type Policy struct {
	// tags is the set of allowed statement keywords (for, if, block, include,
	// macro, ...). A used keyword outside the set is a SecurityTag violation (B1).
	tags map[string]bool
	// filters is the set of allowed filter names (B2).
	filters map[string]bool
	// functions is the set of allowed function names; the range operator `..`
	// counts as the range function (B3, B8).
	functions map[string]bool
	// methods maps a host type name to the method names allowed on it, matched
	// across the type-graph (B4). The method name is matched case-sensitively in
	// the host's real spelling.
	methods map[string]map[string]bool
	// properties maps a host type name to the property/field names allowed on it,
	// matched across the type-graph (B5).
	properties map[string]map[string]bool
	// strict selects strict-versus-lenient member-access reporting (spec 04
	// Section 8.3). In strict mode, a member access on a host type the policy does
	// not know at all (no method or property allowlist entry and absent from the
	// type-graph) reports a distinct unknown-type error (the interp consults
	// Knows). In lenient mode that same access falls through to the ordinary
	// per-member deny. The tag/filter/function floor is uniform in both modes, with
	// no grandfathering (B6).
	strict bool
	// graph is the host type-graph backing the per-type method/property lookups.
	// A nil graph matches each type only by its own name.
	graph *TypeGraph
}

// PolicyOption configures a Policy under construction by NewPolicy.
type PolicyOption func(*Policy)

// NewPolicy builds a Policy with non-nil (empty) allowlist maps, then applies
// the given options in order. With no options it denies everything (uniform
// allowlisting); pass AllowTags, AllowFilters, AllowFunctions, AllowMethods,
// AllowProperties, Strict, and WithTypeGraph to open specific holes.
func NewPolicy(opts ...PolicyOption) *Policy {
	p := &Policy{
		tags:       map[string]bool{},
		filters:    map[string]bool{},
		functions:  map[string]bool{},
		methods:    map[string]map[string]bool{},
		properties: map[string]map[string]bool{},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// AllowTags allows the given statement keywords (B1).
func AllowTags(tags ...string) PolicyOption {
	return func(p *Policy) {
		for _, t := range tags {
			p.tags[t] = true
		}
	}
}

// AllowFilters allows the given filter names (B2).
func AllowFilters(names ...string) PolicyOption {
	return func(p *Policy) {
		for _, n := range names {
			p.filters[n] = true
		}
	}
}

// AllowFunctions allows the given function names (B3).
func AllowFunctions(names ...string) PolicyOption {
	return func(p *Policy) {
		for _, n := range names {
			p.functions[n] = true
		}
	}
}

// AllowMethods allows the given method names on the host type typeName, matched
// across the type-graph (B4). Repeated options for the same type accumulate.
func AllowMethods(typeName string, methods ...string) PolicyOption {
	return func(p *Policy) {
		m := p.methods[typeName]
		if m == nil {
			m = map[string]bool{}
			p.methods[typeName] = m
		}
		for _, name := range methods {
			m[name] = true
		}
	}
}

// AllowProperties allows the given property/field names on the host type
// typeName, matched across the type-graph (B5). Repeated options for the same
// type accumulate.
func AllowProperties(typeName string, props ...string) PolicyOption {
	return func(p *Policy) {
		m := p.properties[typeName]
		if m == nil {
			m = map[string]bool{}
			p.properties[typeName] = m
		}
		for _, name := range props {
			m[name] = true
		}
	}
}

// Strict turns on strict member-access reporting (spec 04 Section 8.3). In strict
// mode a member access on a host type the policy does not know at all (no method
// or property allowlist entry and absent from the type-graph, per Policy.Knows)
// reports a distinct unknown-type error; in the default lenient mode that same
// access falls through to an ordinary per-member deny. The tag/filter/function
// allowlist floor is unaffected either way.
func Strict() PolicyOption {
	return func(p *Policy) { p.strict = true }
}

// WithTypeGraph backs the per-type method/property lookups with g. A policy
// without a type-graph matches each type only by its own name.
func WithTypeGraph(g *TypeGraph) PolicyOption {
	return func(p *Policy) { p.graph = g }
}

// Strict reports whether the policy uses strict member-access reporting. The
// package-level Strict option and this method live in different namespaces, so
// there is no collision.
func (p *Policy) Strict() bool { return p != nil && p.strict }

// AllowsTag reports whether the statement keyword is permitted (B1).
func (p *Policy) AllowsTag(tag string) bool { return p != nil && p.tags[tag] }

// AllowsFilter reports whether the filter name is permitted (B2).
func (p *Policy) AllowsFilter(name string) bool { return p != nil && p.filters[name] }

// AllowsFunction reports whether the function name is permitted (B3).
func (p *Policy) AllowsFunction(name string) bool { return p != nil && p.functions[name] }

// AllowsMethod reports whether method is permitted on the named host type,
// walking the type-graph so an entry on a base type or interface covers a
// registered subtype (B4). Matching is case-sensitive.
func (p *Policy) AllowsMethod(typeName, method string) bool {
	if p == nil {
		return false
	}
	for _, t := range p.graph.ancestors(typeName) {
		if p.methods[t][method] {
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
	for _, t := range p.graph.ancestors(typeName) {
		if p.properties[t][prop] {
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
	for _, t := range p.graph.ancestors(typeName) {
		if len(p.methods[t]) > 0 || len(p.properties[t]) > 0 {
			return true
		}
		if p.graph != nil && len(p.graph.parents[t]) > 0 {
			return true
		}
	}
	return false
}
