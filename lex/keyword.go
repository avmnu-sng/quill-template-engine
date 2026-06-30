package lex

// statementKeywords is the closed set of statement keywords (spec 01 Section 5.1,
// spec 02 Section 3). Under the @-default a statement is recognized only when a
// line's first non-whitespace token is '@' immediately followed by one of these
// at a word boundary. The lexer uses this set for the statement-head door out of
// TEXT; it does NOT treat these spellings as keywords anywhere else (positional
// keyword rule, spec 08/R2 -- inside CODE they are ordinary NAMEs).
var statementKeywords = map[string]bool{
	"extends":    true,
	"block":      true,
	"for":        true,
	"if":         true,
	"elseif":     true,
	"else":       true,
	"macro":      true,
	"set":        true,
	"include":    true,
	"import":     true,
	"from":       true,
	"use":        true,
	"embed":      true,
	"with":       true,
	"apply":      true,
	"do":         true,
	"flush":      true,
	"deprecated": true,
	"guard":      true,
	"types":      true,
	"escape":     true,
	"sandbox":    true,
	"verbatim":   true,
	"line":       true,
	"cache":      true,
	"capture":    true,
}

// Note on block-bodied versus line statements: the lexer does NOT pre-classify
// keywords into the two shapes. Head scanning (scanStatement) emits a BLOCK_OPEN
// when it actually sees a depth-zero '{' terminating the head and a STMT_END
// otherwise, so the body shape is decided by the source, not by a per-keyword
// table. This keeps "@set x = capture { ... @}" (a set-tail capture block) working
// without the lexer knowing that capture is special; the parser sorts out which
// heads legitimately carry a body.
