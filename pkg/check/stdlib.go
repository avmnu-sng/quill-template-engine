package check

// builtinSigs is the checker's table of static signatures for the core stdlib
// filters and functions whose typing is monomorphic enough to check arity and
// argument/return types directly (spec 03, design/type-system.md Section 9.1).
// It is consulted AFTER a host registry: a host signature of the same name wins
// (a host may override a core callable), and a name absent from both is checked
// only dynamically (its result is `any`, no arity check).
//
// Deliberately omitted here and handled with generic, element-propagating rules
// in the expression checker (callExpr): the higher-order collection filters
// (map, filter, sort, reduce, find) whose result type depends on a collection's
// element type and an arrow's body type, and `default`/coalescing whose result
// follows the null-coalescing rule. Encoding those as a fixed Signature would
// lose the element propagation that is the strongest ergonomic argument for
// annotating a collection.
//
// Where a filter is genuinely variadic-or-polymorphic in its return (e.g. first,
// last, slice, merge, reverse) the conservative choice is a wide parameter and
// an `any` return, so the checker still verifies the call is shaped like a call
// but never false-rejects a well-typed pipeline.
var builtinFilterSigs = map[string]*Signature{
	// string -> string transforms.
	"upper":      strFilter(),
	"lower":      strFilter(),
	"title":      strFilter(),
	"capitalize": strFilter(),
	"ucfirst":    strFilter(),
	"trim":       {params: []*Type{String, String}, optional: 1, ret: String},
	"nl2br":      strFilter(),
	"spaceless":  strFilter(),
	"striptags":  {params: []*Type{String, Any}, optional: 1, ret: String},
	"url_encode": {params: []*Type{Any}, ret: String},
	"escape":     {params: []*Type{Any, String}, optional: 1, ret: String},
	"e":          {params: []*Type{Any, String}, optional: 1, ret: String},
	"indent":     {params: []*Type{String, Int, String}, optional: 2, ret: String},
	"replace":    {params: []*Type{String, Any}, ret: String},
	"format":     {params: []*Type{String}, variadic: true, varElem: Any, ret: String},

	// numeric.
	"abs":           {params: []*Type{Any}, ret: Any},
	"round":         {params: []*Type{Any, Int, String}, optional: 2, ret: Float},
	"number_format": {params: []*Type{Any, Int, String, String}, optional: 3, ret: String},

	// collection -> scalar / collection.
	"length": {params: []*Type{Any}, ret: Int},
	"join":   {params: []*Type{ListOf(Any), String}, optional: 1, ret: String},
	"keys":   {params: []*Type{Any}, ret: ListOf(Any)},
	"json":   {params: []*Type{Any, Any}, optional: 1, ret: String},

	// tab: indentation filter; int|tab and string|tab(n) per spec 03 Section 5.1.
	// The piped value is wide (int or string) and the optional level is int.
	"tab": {params: []*Type{Any, Int}, optional: 1, ret: String},
}

// builtinFunctionSigs is the table for core FUNCTIONS (call form, no piped
// value). Same precedence and dynamic-fallback rules as the filter table.
var builtinFunctionSigs = map[string]*Signature{
	"range":  {params: []*Type{Int, Int, Int}, optional: 1, ret: ListOf(Int)},
	"max":    {params: []*Type{Any}, variadic: true, varElem: Any, ret: Any},
	"min":    {params: []*Type{Any}, variadic: true, varElem: Any, ret: Any},
	"len":    {params: []*Type{Any}, ret: Int},
	"length": {params: []*Type{Any}, ret: Int},
}

// strFilter is the common (string) => string filter shape.
func strFilter() *Signature { return &Signature{params: []*Type{String}, ret: String} }
