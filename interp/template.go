package interp

import (
	"regexp"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/errors"
)

// Template is one parsed-and-prepared template: its module AST plus the indexed
// composition tables the renderer walks (spec 01 Section 5). It is the runtime
// realization of the port's Template contract -- Display (renderTemplate), Block
// / HasBlock (the block table), Macro / HasMacro (the macro table), and Parent
// (the extends head). The tables are built once by Prepare and then shared
// read-only across renders.
type Template struct {
	Name   string
	Module *ast.Node

	// blocks maps a block name to its defining node, in declaration order
	// (nested blocks are flattened: both outer and inner are top-level entries,
	// per design/composition Section 2.4). order preserves first-seen order.
	blocks      map[string]*ast.Node
	blockOrder  []string
	macros      map[string]*ast.Node
	macroOrder  []string
	extendsNode *ast.Node // the @extends node, or nil for a non-inheriting template

	// imports records @import (namespace) and @from (selective) heads at file
	// scope so the renderer can resolve the macro namespace and dotted calls.
	imports []*ast.Node

	// uses records @use (trait) heads at file scope, in source order, so the
	// renderer can pull in a traitable template's blocks with trait-then-own
	// precedence and optional aliasing (spec 01 Section 5.4).
	uses []*ast.Node

	// regexps caches the compiled RE2 for every `matches` node whose pattern is a
	// string literal. The spec (01 Section 3, "Regex matches") requires a literal
	// pattern to be "validated at compile time", so compileLiteralRegexps walks
	// the whole tree during Prepare: a bad literal is an error here regardless of
	// branch reachability, and the cached *regexp.Regexp lets render-time matches
	// reuse one compile instead of recompiling per evaluation (e.g. per loop
	// iteration). A dynamic (non-literal) pattern is absent here and compiled at
	// render time.
	regexps map[*ast.Node]*regexp.Regexp

	// used is the sandbox's compile-time collection of the statement keywords,
	// filters, and functions this template references, gathered once by Prepare
	// (design/escaping-safety Section 6.3, B8). The per-render security check
	// (Phase 1) validates this set against the policy in one pass when the sandbox
	// is active, mapping any violation back to the recorded source node.
	used usedCallables
}

// usedCallables is the statically collected set of tags/filters/functions a
// template references, each mapped to a representative source node so a Phase-1
// violation reports a template:line position. The range operator `..` is
// recorded as the function "range" (B8), so allowing range gates `1..n`.
type usedCallables struct {
	tags      map[string]*ast.Node
	filters   map[string]*ast.Node
	functions map[string]*ast.Node
}

func newUsedCallables() usedCallables {
	return usedCallables{
		tags:      map[string]*ast.Node{},
		filters:   map[string]*ast.Node{},
		functions: map[string]*ast.Node{},
	}
}

// PrepareChecked builds the composition tables from a parsed module and runs the
// compile-time validations the spec requires (currently: literal regex patterns
// in `matches`). It returns an error so a malformed template is rejected before
// any render. Prepare wraps it for callers that have already validated or that
// construct synthetic modules in tests.
func PrepareChecked(name string, mod *ast.Node) (*Template, error) {
	t := Prepare(name, mod)
	if err := t.compileLiteralRegexps(mod); err != nil {
		return nil, err
	}
	return t, nil
}

// Prepare builds the composition tables from a parsed module. It is idempotent
// and cheap; the engine calls it once per template and caches the result.
func Prepare(name string, mod *ast.Node) *Template {
	t := &Template{
		Name:    name,
		Module:  mod,
		blocks:  map[string]*ast.Node{},
		macros:  map[string]*ast.Node{},
		regexps: map[*ast.Node]*regexp.Regexp{},
		used:    newUsedCallables(),
	}
	t.index(mod)
	t.collectUsed(mod, t.used)
	return t
}

// collectUsed walks the whole AST once, recording every statement keyword,
// filter, and function the template references into u, mapped to a source node
// for line reporting (design/escaping-safety Section 6.3, B8). It is the
// compile-time half of the two-phase sandbox enforcement: the names are
// statically known, so the per-render Phase-1 check validates this set against
// the policy in one pass. The range operator `..` is recorded as the function
// "range" so a policy gates `1..n` by allowing range (B8); the `parent`/`block`
// composition builtins are recorded as functions (they are not grandfathered,
// B6). It is also used on a node subtree to scope the @sandbox region's body
// (collectUsed over the region's children).
func (t *Template) collectUsed(n *ast.Node, u usedCallables) {
	if n == nil {
		return
	}
	if tag := tagKeyword(n); tag != "" {
		if _, seen := u.tags[tag]; !seen {
			u.tags[tag] = n
		}
	}
	switch n.Kind {
	case ast.KindFilter:
		if _, seen := u.filters[n.Str]; !seen {
			u.filters[n.Str] = n
		}
	case ast.KindApplyFilter:
		if _, seen := u.filters[n.Str]; !seen {
			u.filters[n.Str] = n
		}
	case ast.KindCall:
		// A bare-name callee is a function (or a macro/composition builtin); macros
		// are template-defined and not policed, so only record a name that is not a
		// macro this template defines. The per-render check skips macro names too.
		if callee := n.Child(0); callee != nil && callee.Kind == ast.KindName {
			if _, seen := u.functions[callee.Str]; !seen {
				u.functions[callee.Str] = n
			}
		}
	case ast.KindMembership:
		// `..` is the range operator; record it as the range function so allowing
		// range gates a literal range expression (B8).
		if n.Str == ".." {
			if _, seen := u.functions["range"]; !seen {
				u.functions["range"] = n
			}
		}
	case ast.KindBinary:
		if n.Str == ".." {
			if _, seen := u.functions["range"]; !seen {
				u.functions["range"] = n
			}
		}
	}
	for _, c := range n.Children {
		t.collectUsed(c, u)
	}
}

// tagKeyword maps a statement node kind to the keyword the policy allowlists by
// name (B1). It returns "" for non-statement nodes and for the module/body/text
// scaffolding that carries no keyword. The @sandbox region itself is not a
// gated tag (it is the activation mechanism, always permitted to appear).
func tagKeyword(n *ast.Node) string {
	switch n.Kind {
	case ast.KindIf:
		return "if"
	case ast.KindFor:
		return "for"
	case ast.KindSet, ast.KindCapture:
		return "set"
	case ast.KindWith:
		return "with"
	case ast.KindApply:
		return "apply"
	case ast.KindDo:
		return "do"
	case ast.KindFlush:
		return "flush"
	case ast.KindDeprecated:
		return "deprecated"
	case ast.KindGuard:
		return "guard"
	case ast.KindTypes:
		return "types"
	case ast.KindEscape:
		return "escape"
	case ast.KindLine:
		return "line"
	case ast.KindCache:
		return "cache"
	case ast.KindExtends:
		return "extends"
	case ast.KindBlock:
		return "block"
	case ast.KindMacro:
		return "macro"
	case ast.KindImport:
		return "import"
	case ast.KindFrom:
		return "from"
	case ast.KindUse:
		return "use"
	case ast.KindEmbed:
		return "embed"
	case ast.KindInclude:
		return "include"
	default:
		return ""
	}
}

// compileLiteralRegexps walks the entire AST (statements and the expression
// subtrees they hang off) and, for every `matches` node whose right operand is a
// plain string literal (KindString -- single-quote, backtick, or escape-only
// double-quote; an interpolated pattern is a KindBinary "~" concat chain and
// stays dynamic), compiles the pattern with the stdlib RE2 engine. A compile
// failure is surfaced as a clear error at the pattern's source position, and the
// compiled regexp is cached on the node so matches() reuses it.
func (t *Template) compileLiteralRegexps(n *ast.Node) error {
	if n == nil {
		return nil
	}
	if n.Kind == ast.KindMembership && n.Str == "matches" {
		pat := n.Child(1)
		if pat != nil && pat.Kind == ast.KindString {
			re, err := regexp.Compile(pat.Str)
			if err != nil {
				return errors.New(errors.KindRuntime,
					"invalid RE2 pattern %q: %v", pat.Str, err).At(pat.Src, pat.Line)
			}
			t.regexps[n] = re
		}
	}
	for _, c := range n.Children {
		if err := t.compileLiteralRegexps(c); err != nil {
			return err
		}
	}
	return nil
}

// index walks the module, recording blocks (recursively, so nested blocks are
// flat), macros, the extends head, and import heads. A later macro of the same
// name wins (design/composition Section 3.4); a later block of the same name is
// a redefinition that also wins, matching the port's table-build.
func (t *Template) index(n *ast.Node) {
	for _, c := range n.Children {
		switch c.Kind {
		case ast.KindBlock:
			if _, seen := t.blocks[c.Str]; !seen {
				t.blockOrder = append(t.blockOrder, c.Str)
			}
			t.blocks[c.Str] = c
			// Recurse so a nested @block is also a flat top-level entry.
			t.index(c)
		case ast.KindMacro:
			if _, seen := t.macros[c.Str]; !seen {
				t.macroOrder = append(t.macroOrder, c.Str)
			}
			t.macros[c.Str] = c
		case ast.KindExtends:
			t.extendsNode = c
		case ast.KindImport, ast.KindFrom:
			t.imports = append(t.imports, c)
		case ast.KindUse:
			t.uses = append(t.uses, c)
		case ast.KindEmbed:
			// An embed defines blocks for its OWN child render, not this template's
			// table; it is handled at render time, not indexed here.
		}
	}
}

// Block returns the node defining the named block in this template, if any.
func (t *Template) Block(name string) (*ast.Node, bool) {
	n, ok := t.blocks[name]
	return n, ok
}

// HasBlock reports whether this template defines the named block.
func (t *Template) HasBlock(name string) bool { _, ok := t.blocks[name]; return ok }

// BlockNames returns the block names in declaration order.
func (t *Template) BlockNames() []string { return t.blockOrder }

// Macro returns the node defining the named macro in this template, if any.
func (t *Template) Macro(name string) (*ast.Node, bool) {
	n, ok := t.macros[name]
	return n, ok
}

// HasMacro reports whether this template defines the named macro.
func (t *Template) HasMacro(name string) bool { _, ok := t.macros[name]; return ok }

// IsChild reports whether this template extends a parent (Parent tri-state, the
// "definitely a child" case), spec 01 Section 5.2.
func (t *Template) IsChild() bool { return t.extendsNode != nil }

// Traitable reports whether this template may be pulled in by @use: it must have
// NO parent (@extends), NO macros, and NO free body content -- only @block
// definitions and @use statements at top level (spec 01 Section 5.4, design/
// composition Section 4). A trait is a pure bundle of blocks.
func (t *Template) Traitable() bool {
	if t.extendsNode != nil || len(t.macros) != 0 {
		return false
	}
	for _, c := range t.Module.Children {
		switch c.Kind {
		case ast.KindBlock, ast.KindUse:
			// Allowed: block definitions and nested trait uses.
		case ast.KindText:
			// A whitespace-only text span (and a leading BOM) is tolerated, matching
			// the content-outside-blocks rule for inheriting templates.
			if strings.TrimLeft(strings.TrimSpace(c.Str), "\ufeff") != "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}
