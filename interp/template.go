package interp

import (
	"github.com/avmnusng/quill-template-engine/ast"
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
}

// Prepare builds the composition tables from a parsed module. It is idempotent
// and cheap; the engine calls it once per template and caches the result.
func Prepare(name string, mod *ast.Node) *Template {
	t := &Template{
		Name:   name,
		Module: mod,
		blocks: map[string]*ast.Node{},
		macros: map[string]*ast.Node{},
	}
	t.index(mod)
	return t
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
