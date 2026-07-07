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
	"trim":       {Params: []*Type{String, String}, Optional: 1, Ret: String},
	"nl2br":      strFilter(),
	"spaceless":  strFilter(),
	"striptags":  {Params: []*Type{String, Any}, Optional: 1, Ret: String},
	"url_encode": {Params: []*Type{Any}, Ret: String},
	"escape":     {Params: []*Type{Any, String}, Optional: 1, Ret: String},
	"e":          {Params: []*Type{Any, String}, Optional: 1, Ret: String},
	"indent":     {Params: []*Type{String, Int, String}, Optional: 2, Ret: String},
	"replace":    {Params: []*Type{String, Any}, Ret: String},
	"format":     {Params: []*Type{String}, Variadic: true, VarElem: Any, Ret: String},

	// numeric.
	"abs":           {Params: []*Type{Any}, Ret: Any},
	"round":         {Params: []*Type{Any, Int, String}, Optional: 2, Ret: Float},
	"number_format": {Params: []*Type{Any, Int, String, String}, Optional: 3, Ret: String},

	// collection -> scalar / collection.
	"length": {Params: []*Type{Any}, Ret: Int},
	"join":   {Params: []*Type{ListOf(Any), String}, Optional: 1, Ret: String},
	"keys":   {Params: []*Type{Any}, Ret: ListOf(Any)},
	"json":   {Params: []*Type{Any, Any}, Optional: 1, Ret: String},

	// tab: indentation filter; int|tab and string|tab(n) per spec 03 Section 5.1.
	// The piped value is wide (int or string) and the optional level is int.
	"tab": {Params: []*Type{Any, Int}, Optional: 1, Ret: String},
}

// builtinFunctionSigs is the table for core FUNCTIONS (call form, no piped
// value). Same precedence and dynamic-fallback rules as the filter table.
var builtinFunctionSigs = map[string]*Signature{
	"range":  {Params: []*Type{Int, Int, Int}, Optional: 1, Ret: ListOf(Int)},
	"max":    {Params: []*Type{Any}, Variadic: true, VarElem: Any, Ret: Any},
	"min":    {Params: []*Type{Any}, Variadic: true, VarElem: Any, Ret: Any},
	"len":    {Params: []*Type{Any}, Ret: Int},
	"length": {Params: []*Type{Any}, Ret: Int},
}

// strFilter is the common (string) => string filter shape.
func strFilter() *Signature { return &Signature{Params: []*Type{String}, Ret: String} }
