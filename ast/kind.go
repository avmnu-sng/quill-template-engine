package ast

// Kind discriminates a Node. The comment on each kind fixes the meaning of that
// node's scalar fields and the order/meaning of its Children, which is the
// contract a tree walker relies on. Kinds are grouped: the module and template
// body; output (text, interpolation); expression operators and primaries; the
// statement set (spec 01 Section 5.1); and the clause/target helper nodes that
// statements hang structure on.
type Kind uint16

const (
	// KindInvalid is the zero value and never appears in a well-formed tree.
	KindInvalid Kind = iota

	// --- Module and body ---

	// KindModule is the root of one parsed template. Children are its TopItems in
	// source order (text, interpolation, statements, composition heads). Str holds
	// the template name (mirrors Src.Name) for convenience.
	KindModule

	// KindText is a literal output span. Str is the verbatim bytes (escapes
	// already resolved by the lexer). No children.
	KindText

	// KindPrint is an interpolation "{{ expr }}". Child 0 is the expression. When
	// a postfix conditional tail is present the parser desugars it into a ternary
	// (KindTernary), so KindPrint always has exactly one expression child.
	KindPrint

	// KindVerbatim is a verbatim region body. Str is the literal bytes. No
	// children. (Comments emit nothing and produce no node.)
	KindVerbatim

	// --- Expression: primaries ---

	// KindInt is an integer literal; Int holds the value.
	KindInt
	// KindFloat is a float literal; Float holds the value.
	KindFloat
	// KindString is a string literal; Str holds the decoded value. For an
	// interpolated double-quoted string the parser emits a KindConcat chain
	// instead, so KindString is always a plain constant.
	KindString
	// KindBool is a boolean literal; Bool holds the value.
	KindBool
	// KindNull is the null literal (also the 'none' alias). No payload.
	KindNull
	// KindName is a bare identifier (context lookup); Str is the name.
	KindName
	// KindSpecialName is a reserved engine name (_self/_context/_charset); Str is
	// the name (spec 01 Section 1.7, R11).
	KindSpecialName

	// KindList is a sequence literal "[...]"; Children are its elements, each a
	// plain expression or a KindSpread.
	KindList
	// KindMap is a mapping literal "{...}"; Children are KindMapEntry nodes.
	KindMap
	// KindMapEntry is one entry of a map literal. Forms (Int tags the form):
	//   0 name:value     -> child 0 key (KindString), child 1 value
	//   1 shorthand {a}  -> child 0 value (KindName); key is the same name
	//   2 computed (e):v -> child 0 key expr, child 1 value
	//   3 spread ...e    -> child 0 spread source
	KindMapEntry

	// KindArrow is an arrow function. The last child is the body expression; the
	// preceding children are KindParam nodes (zero or more).
	KindArrow
	// KindParam is one declared parameter of an arrow or macro. Str is the name.
	// Bool marks a variadic "...name" collecting excess POSITIONAL arguments. The
	// ParamKwargs bit in Int marks a trailing "**name" collecting excess NAMED
	// arguments into a mapping (symmetric with the positional variadic). Child 0
	// (optional) is a type node; child 1 (optional, arrow/macro) is a default-value
	// expression. Int tags which optional children are present: bit 0 = has type,
	// bit 1 = has default, bit 2 = kwargs tail.
	KindParam

	// --- Expression: postfix chain ---

	// KindAttr is "a.b" / "a?.b". Child 0 is the receiver; Str is the member name;
	// Bool marks the null-safe "?." form.
	KindAttr
	// KindIndex is "a[k]" / "a?[k]". Child 0 receiver, child 1 key; Bool marks the
	// null-safe "?[" form.
	KindIndex
	// KindSlice is "a[start:end]". Child 0 receiver. Children 1 and 2 are the
	// optional start and end; a nil child means the bound was elided. Int is a
	// bitmask: bit 0 = has start, bit 1 = has end.
	KindSlice
	// KindCall is "f(args)" applied to child 0 (the callee). The remaining
	// children are KindArg nodes.
	KindCall
	// KindFilter is "x | f" / "x | f(args)". Child 0 is the piped value; Str is the
	// filter name; the remaining children are KindArg nodes (the explicit args,
	// after the implicit piped first argument).
	KindFilter
	// KindArg is one call/filter argument. Forms (Int tags the form):
	//   0 positional -> child 0 value
	//   1 named      -> Str is the name, child 0 value
	//   2 spread     -> child 0 source
	KindArg

	// --- Expression: operators ---

	// KindUnary is a prefix operator (not/-/+); Str is the spelling. Child 0 is the
	// operand. (Spread "..." in argument/element position is KindSpread, not here.)
	KindUnary
	// KindSpread is a prefix "..." in a sequence/argument/parameter context; child
	// 0 is the source expression.
	KindSpread
	// KindBinary is any binary operator that needs no special node: arithmetic,
	// concat, range, comparison, bitwise. Str is the canonical operator spelling
	// (see binop.go); children 0 and 1 are the operands.
	KindBinary
	// KindLogical is and/or/xor; Str is the canonical spelling. Children 0 and 1.
	// Separate from KindBinary so the renderer can short-circuit and/or.
	KindLogical
	// KindPower is "**". Children 0 (base) and 1 (exponent). Separate from
	// KindBinary so the AST shape records the power/unary interaction directly
	// (spec 02 R6).
	KindPower
	// KindMembership is in / not in / matches / starts with / ends with /
	// has some / has every. Str is the canonical spelling; Bool marks the negated
	// "not in" form. Children 0 (needle/subject) and 1 (haystack/pattern/predicate).
	KindMembership
	// KindTest is "x is t" / "x is not t". Child 0 is the subject; Str is the test
	// name (one or two words joined by a space); Bool marks "is not". The remaining
	// children are KindArg nodes (zero, one positional, or a full arg list).
	KindTest
	// KindTernary is "c ? a : b" (and the desugared postfix conditional). Children
	// 0 condition, 1 then, 2 else.
	KindTernary
	// KindCoalesce is "a ?? b"; children 0 and 1. Bool false.
	KindCoalesce
	// KindElvis is "a ?: b"; children 0 and 1.
	KindElvis
	// KindAssign is "target = value". Child 0 is the target (a KindName or a
	// destructuring pattern), child 1 the value.
	KindAssign

	// KindListPattern is a destructuring target reinterpreted from a list literal
	// on the left of "=". Its children are slot targets; an elided slot is a nil
	// child; a tail capture is the trailing KindSpread; an optional slot is a
	// KindOptional wrapping its target.
	KindListPattern
	// KindMapPattern is a destructuring target reinterpreted from a map literal on
	// the left of "=". Its children are KindMapTarget nodes.
	KindMapPattern
	// KindOptional wraps a destructuring slot marked "name?" (child 0).
	KindOptional
	// KindMapTarget is "{name}" or "{key: alias}" in a destructuring pattern. Str
	// is the source key; Bool true when an alias is present, in which case the
	// alias name is in a second field via child 0 (a KindName). When no alias, the
	// bound name equals Str.
	KindMapTarget

	// --- Type annotations ---

	// KindType is a type-annotation node. Str names the atom (any/null/bool/int/
	// float/string/list/map/Object/arrow/union/group). Children carry sub-types:
	//   list<T>      -> child 0 element type
	//   map<K,V>     -> children 0,1
	//   Object<"N">  -> Str "Object", Bool nullable handled at union, name in a
	//                   child KindString
	//   (T,..)=>R    -> last child is the return type, preceding children params
	//   union        -> children are the alternatives; Bool marks trailing "?"
	KindType

	// --- Statements: control flow ---

	// KindIf is "@if". Children are an ordered run of KindClause nodes: the first
	// is the if-clause (with a condition child + body children), each elseif is a
	// KindClause with a condition, and a final else is a KindClause with no
	// condition (its Bool=false). See clause layout in stmt comments.
	KindIf
	// KindClause is one branch of an if/guard. Child 0 is the condition expression
	// when Bool is true (an if/elseif branch); when Bool is false it is the else
	// branch and child 0 onward are body items. When Bool is true, child 0 is the
	// condition and children 1.. are body items.
	KindClause
	// KindFor is "@for". Str-less. Children: target1 (KindTarget), optional target2
	// (KindTarget) when Int>=2, the iterand expression, an optional fused-filter
	// clause (a KindClause with Bool=true carrying the "if cond" condition as child
	// 0), the body (a KindBody), and an optional else (a KindBody). Int holds the
	// target count (1 or 2); Bool marks presence of an else branch. The filter
	// clause, when present, pre-selects the elements the body iterates so every
	// loop.* field reflects the survivors. The ForRecursive bit of Int marks a
	// "@for node in tree recursive" loop, which binds a loop(children) callable that
	// re-enters the same body over a subtree and exposes loop.depth / loop.depth0
	// (design/composition recursive @for). The target count occupies the low bits
	// (ForTargetCount masks it), so a non-recursive loop's Int stays 1 or 2 exactly.
	KindFor
	// KindTarget is a loop/set target name with an optional type. Str is the name;
	// child 0 (optional) is a KindType.
	KindTarget
	// KindBody is an ordered list of items (a block body or a branch body). Its
	// children are the items. Used where a statement needs to group a body as one
	// child slot.
	KindBody

	// KindSet is "@set a, b = e1, e2". Int holds the target count. The first Int
	// children are targets (KindTarget or a destructuring pattern); the remaining
	// children are the value expressions. A single destructuring target with a
	// single value is the [a,b]=e form (Int==1).
	KindSet
	// KindCapture is "@set X = capture { ... }". Str is the bound name; child 0
	// (optional) is a KindType; the body items follow.
	KindCapture
	// KindWith is "@with map [only] { body }". Child 0 is the map expression;
	// Bool marks "only"; the remaining children are body items.
	KindWith
	// KindApply is "@apply | f | g { body }". The leading children are KindFilter-
	// like KindApplyFilter nodes (name + args); the trailing children are body
	// items. Int holds the number of filters.
	KindApply
	// KindApplyFilter is one filter in an apply chain. Str is the name; children
	// are KindArg nodes.
	KindApplyFilter
	// KindDo is "@do expr"; child 0 is the expression.
	KindDo
	// KindFlush is "@flush". No children.
	KindFlush
	// KindDeprecated is "@deprecated msg [since v]". Str is the message; child 0
	// (optional) is the since-version string node (Bool marks its presence).
	KindDeprecated
	// KindGuard is "@guard kind("name") { body } [else { body }]". Str is the
	// callable kind (filter/function/test); a KindString child holds the name; the
	// body items follow; an optional final KindClause(else) carries the else body.
	KindGuard
	// KindTypes is "@types { x: T, ... }". Children are KindTypeDecl nodes.
	KindTypes
	// KindTypeDecl is "name: T" inside a @types block. Str is the name; child 0 is
	// the type.
	KindTypeDecl
	// KindEscape is "@escape strategy { body }". Str is the strategy (or "off");
	// the children are body items.
	KindEscape
	// KindSandbox is "@sandbox { body }". Children are body items.
	KindSandbox
	// KindLine is "@line N"; Int holds N.
	KindLine
	// KindCache is "@cache k=v ... { body }". The leading children are KindCacheArg
	// nodes; the trailing children are body items. Int holds the cache-arg count.
	KindCache
	// KindCacheArg is one "name=expr" of a cache head. Str is the name; child 0 the
	// value.
	KindCacheArg

	// --- Statements: composition ---

	// KindExtends is "@extends expr"; child 0 is the (string-coerced) parent
	// expression or candidate list.
	KindExtends
	// KindBlock is "@block name [sig] { body }" or the shortcut "@block name expr".
	// Str is the block name; Int tags the form (0 brace body, 1 shortcut value).
	// Optional leading children: a KindParams (params) and/or a KindType (return).
	// For the brace form the body items follow; for the shortcut, child is the
	// single value expression. Bool marks "has params"; the parser records the
	// return type as a KindType child when present.
	KindBlock
	// KindParams groups a parameter list (block/macro). Children are KindParam.
	KindParams
	// KindMacro is "@macro name(params) [-> T] { body }". Str is the name. Child 0
	// is a KindParams; child 1 (optional) is the return KindType; the body items
	// follow.
	KindMacro
	// KindImport is "@import src as alias". Str is the alias; child 0 is the source
	// (a KindString, or a KindSpecialName for _self).
	KindImport
	// KindFrom is "@from src import a, b as c". Child 0 is the source; the
	// remaining children are KindFromItem nodes.
	KindFrom
	// KindFromItem is one imported name. Str is the source name; Bool marks an
	// alias, whose value is in child 0 (a KindName) when present.
	KindFromItem
	// KindUse is "@use src [with map]". Child 0 is the source string; child 1
	// (optional) is the alias map literal (Bool marks its presence).
	KindUse
	// KindEmbed is "@embed src [mods] { blocks }". Child 0 is the source; the
	// include-modifier flags ride in Int (see IncFlag*); an optional with-map is a
	// KindMap child flagged by IncWith; the remaining children are KindBlock nodes.
	KindEmbed
	// KindInclude is "@include expr [mods]". Child 0 is the source; Int holds the
	// include-modifier flags; an optional with-map/expr is a child flagged by
	// IncWith.
	KindInclude

	// --- Statements: code generation ---

	// --- Statements: accumulating slots and call-blocks ---

	// KindProvide is "@provide label { body }". Str is the slot label; the children
	// are the body items whose rendered output is APPENDED to the named slot buffer
	// in execution order (additive, order-preserving across call sites), distinct
	// from @block which overrides. It emits nothing at its own position.
	KindProvide
	// KindYield is "@yield label" (the slot(label) function is the expression form).
	// Str is the slot label; it emits the accumulated content of that slot once, in
	// the order the @provide bodies ran. No children.
	KindYield
	// KindCallBlock is "@call [(callerParams)] name(args) { body }". Str is the macro
	// name. Child 0 is a KindParams holding the caller-block parameters (empty when
	// the "(p1, p2)" prefix is absent). The KindArg children that follow carry the
	// macro-call arguments. The final child is a KindBody: the caller block the macro
	// renders via caller(). caller(v1, v2) inside the macro binds the caller
	// parameters positionally and renders the body, so a value round-trips from the
	// macro back into the block.
	KindCallBlock

	// KindLog is "@log expr". Child 0 is the expression to evaluate and write to
	// the host logger. It produces no rendered output and is a coverable unit.
	KindLog
	// KindTabBlock is "@tab(n) { body } @}". Child 0 is the level expression; the
	// remaining children are body items. The whole rendered body is indented by n
	// levels, nesting cumulatively via the output layer's indent stack.
	KindTabBlock
)

// Include-modifier flags packed into the Int field of KindInclude / KindEmbed.
const (
	IncWith          int64 = 1 << iota // a "with <expr>" modifier is present
	IncOnly                            // the "only" modifier is present
	IncIgnoreMissing                   // the "ignore missing" modifier is present
)

// Param-presence flags packed into the Int field of KindParam.
const (
	ParamHasType    int64 = 1 << iota // a ":Type" annotation is present
	ParamHasDefault                   // a "=default" is present
	ParamKwargs                       // a trailing "**name" collecting excess named args
)

// MapEntry and Arg form tags (the Int field of KindMapEntry / KindArg).
const (
	MapEntryKeyed     int64 = 0 // name: value
	MapEntryShorthand int64 = 1 // {a}
	MapEntryComputed  int64 = 2 // (expr): value
	MapEntrySpread    int64 = 3 // ...expr

	ArgPositional int64 = 0
	ArgNamed      int64 = 1
	ArgSpread     int64 = 2
)

// For-loop flags and target-count mask packed into the Int field of KindFor. The
// low two bits hold the target count (1 or 2); ForRecursive marks the recursive
// descent form. A non-recursive loop's Int is just its count, so existing loops
// read unchanged.
const (
	ForTargetCount int64 = 0x3    // mask for the target count in the low bits
	ForRecursive   int64 = 1 << 2 // "@for x in tree recursive" descent form
)

// Slice-bound flags packed into the Int field of KindSlice.
const (
	SliceHasStart int64 = 1 << iota
	SliceHasEnd
)

// String returns a stable ASCII label for the kind, used by tests and dumps.
func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "Kind(?)"
}

var kindNames = [...]string{
	KindInvalid:     "Invalid",
	KindModule:      "Module",
	KindText:        "Text",
	KindPrint:       "Print",
	KindVerbatim:    "Verbatim",
	KindInt:         "Int",
	KindFloat:       "Float",
	KindString:      "String",
	KindBool:        "Bool",
	KindNull:        "Null",
	KindName:        "Name",
	KindSpecialName: "SpecialName",
	KindList:        "List",
	KindMap:         "Map",
	KindMapEntry:    "MapEntry",
	KindArrow:       "Arrow",
	KindParam:       "Param",
	KindAttr:        "Attr",
	KindIndex:       "Index",
	KindSlice:       "Slice",
	KindCall:        "Call",
	KindFilter:      "Filter",
	KindArg:         "Arg",
	KindUnary:       "Unary",
	KindSpread:      "Spread",
	KindBinary:      "Binary",
	KindLogical:     "Logical",
	KindPower:       "Power",
	KindMembership:  "Membership",
	KindTest:        "Test",
	KindTernary:     "Ternary",
	KindCoalesce:    "Coalesce",
	KindElvis:       "Elvis",
	KindAssign:      "Assign",
	KindListPattern: "ListPattern",
	KindMapPattern:  "MapPattern",
	KindOptional:    "Optional",
	KindMapTarget:   "MapTarget",
	KindType:        "Type",
	KindIf:          "If",
	KindClause:      "Clause",
	KindFor:         "For",
	KindTarget:      "Target",
	KindBody:        "Body",
	KindSet:         "Set",
	KindCapture:     "Capture",
	KindWith:        "With",
	KindApply:       "Apply",
	KindApplyFilter: "ApplyFilter",
	KindDo:          "Do",
	KindFlush:       "Flush",
	KindDeprecated:  "Deprecated",
	KindGuard:       "Guard",
	KindTypes:       "Types",
	KindTypeDecl:    "TypeDecl",
	KindEscape:      "Escape",
	KindSandbox:     "Sandbox",
	KindLine:        "Line",
	KindCache:       "Cache",
	KindCacheArg:    "CacheArg",
	KindExtends:     "Extends",
	KindBlock:       "Block",
	KindParams:      "Params",
	KindMacro:       "Macro",
	KindImport:      "Import",
	KindFrom:        "From",
	KindFromItem:    "FromItem",
	KindUse:         "Use",
	KindEmbed:       "Embed",
	KindInclude:     "Include",
	KindLog:         "Log",
	KindTabBlock:    "TabBlock",
	KindProvide:     "Provide",
	KindYield:       "Yield",
	KindCallBlock:   "CallBlock",
}
