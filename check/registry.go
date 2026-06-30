package check

// Registry is the host-supplied static-typing surface the checker consults for
// Object<"Name"> member types and host callable signatures (design/type-system.md
// Sections 4.4, 9.1). It is the typing counterpart of the sandbox type-graph:
// the design notes one host registration ideally serves both security and
// typing, but the security graph (sandbox.TypeGraph) records only allow/deny
// edges, not member TYPES, so the checker takes the member shapes here. A nil
// Registry means the host registered no static types: every Object<...> is then
// treated as an opaque known type whose members are `any`, so a template that
// annotates Object<...> still type-checks structurally (renderability, arity,
// arithmetic) while member reads fall to the dynamic floor. This keeps the
// gradual promise -- annotate as much or as little as you like -- at the host
// boundary too.
//
// A Registry is immutable data the host builds before constructing the
// Environment; the checker only reads it.
type Registry struct {
	// types maps a host type name to its declared static shape. A name present
	// here is a "known" Object type; a name absent here but referenced in an
	// annotation is reported as an unknown-host-type error ONLY when the registry
	// is non-empty (the host opted into nominal typing). An empty registry
	// disables that error so the no-host-types case never false-rejects.
	types map[string]*ObjectType

	// signatures maps a host filter/function/test name to its callable signature,
	// so a typed call site can be arity- and argument-checked. A name absent here
	// is checked only dynamically (its result is `any`).
	signatures map[string]*Signature
}

// ObjectType is the static shape of one host Object<"Name"> type: its readable
// members (fields and get/is/has accessors collapsed to the member name per the
// dotted-access rule, spec 04 Section 5), its callable method signatures, its
// iteration element type (nil when not iterable), whether it stringifies, and
// its declared supertype/interface names for nominal consistency.
type ObjectType struct {
	Name       string
	Members    map[string]*Type      // dotted-access member -> type
	Methods    map[string]*Signature // a.m() method -> signature
	ElemType   *Type                 // for-iteration element type; nil when not iterable
	Stringify  bool                  // has a stringify hook (renderable)
	Supertypes []string              // declared base types / interfaces
}

// Signature is the static type of a callable (host filter/function/test or
// macro/block). Params lists the parameter types in order; Optional is the
// count of trailing parameters that may be omitted (those with defaults or the
// variadic itself); Variadic marks a trailing ...rest that absorbs extra
// positional arguments of element type VarElem; Ret is the result type.
type Signature struct {
	Params   []*Type
	Optional int
	Variadic bool
	VarElem  *Type
	Ret      *Type
}

// NewRegistry returns an empty registry the host populates with AddType /
// AddSignature before building the Environment.
func NewRegistry() *Registry {
	return &Registry{
		types:      map[string]*ObjectType{},
		signatures: map[string]*Signature{},
	}
}

// AddType registers (or replaces) a host Object type's static shape.
func (r *Registry) AddType(t *ObjectType) {
	if r.types == nil {
		r.types = map[string]*ObjectType{}
	}
	r.types[t.Name] = t
}

// AddSignature registers (or replaces) a host callable's signature by name.
func (r *Registry) AddSignature(name string, s *Signature) {
	if r.signatures == nil {
		r.signatures = map[string]*Signature{}
	}
	r.signatures[name] = s
}

// nominal reports whether the registry carries any host type declarations. When
// false, an Object<...> annotation is treated as opaque-but-known rather than an
// unknown-type error, so a host that registered no static types is unburdened.
func (r *Registry) nominal() bool { return r != nil && len(r.types) > 0 }

// objectType returns the registered shape for a host type name, or nil.
func (r *Registry) objectType(name string) *ObjectType {
	if r == nil {
		return nil
	}
	return r.types[name]
}

// knowsType reports whether the host type name resolves: either it is
// registered, or the registry is non-nominal (no host types at all), in which
// case every Object<...> is accepted as opaque. A nominal registry that lacks
// the name does NOT know it -- that is the unknown-host-type error site.
func (r *Registry) knowsType(name string) bool {
	if r == nil || !r.nominal() {
		return true
	}
	_, ok := r.types[name]
	return ok
}

// signature returns a host callable's registered signature, or nil when the
// name is unregistered (checked dynamically).
func (r *Registry) signature(name string) *Signature {
	if r == nil {
		return nil
	}
	return r.signatures[name]
}

// memberType resolves a.member's static type on a known Object type, walking the
// declared supertypes so an inherited member is found. ok is false when the type
// is registered but has no such member -- a check-time miss (the static shadow
// of the strict-undefined runtime miss). When the registry is non-nominal the
// caller never reaches here for an Object (it treats members as any).
func (r *Registry) memberType(name, member string) (*Type, bool) {
	ot := r.objectType(name)
	if ot == nil {
		return Any, true
	}
	if t, ok := ot.Members[member]; ok {
		return t, true
	}
	for _, sup := range ot.Supertypes {
		if t, ok := r.memberType(sup, member); ok {
			return t, true
		}
	}
	return nil, false
}

// methodSig resolves a.m()'s signature on a known Object type, walking
// supertypes. ok is false when the type is registered but declares no such
// method.
func (r *Registry) methodSig(name, method string) (*Signature, bool) {
	ot := r.objectType(name)
	if ot == nil {
		return nil, true
	}
	if s, ok := ot.Methods[method]; ok {
		return s, true
	}
	for _, sup := range ot.Supertypes {
		if s, ok := r.methodSig(sup, method); ok {
			return s, true
		}
	}
	return nil, false
}

// iterElem returns the element type a known Object iterates as, and whether it
// is iterable at all. A non-nominal registry reports iterable-as-any so a typed
// for over an Object<...> is not rejected when the host did not declare shapes.
func (r *Registry) iterElem(name string) (*Type, bool) {
	ot := r.objectType(name)
	if ot == nil {
		return Any, true
	}
	if ot.ElemType == nil {
		return nil, false
	}
	return ot.ElemType, true
}

// stringifies reports whether a known Object type renders to text. An
// unregistered/opaque type is assumed renderable (the dynamic floor decides),
// so renderability over-rejection is confined to registered hookless types.
func (r *Registry) stringifies(name string) bool {
	ot := r.objectType(name)
	if ot == nil {
		return true
	}
	return ot.Stringify
}

// subtypeOf reports whether sub is the same as or a declared subtype/interface
// implementor of sup, used by consistency for Object nominal flow.
func (r *Registry) subtypeOf(sub, sup string) bool {
	if sub == sup {
		return true
	}
	ot := r.objectType(sub)
	if ot == nil {
		return false
	}
	for _, s := range ot.Supertypes {
		if r.subtypeOf(s, sup) {
			return true
		}
	}
	return false
}
