// Package ast is Quill's abstract syntax tree: a single uniform Node type whose
// Kind field discriminates every construct, with ordered Children and a 1-based
// source position. The parser (package parse) builds these; the checker and the
// renderer walk them.
//
// The design follows spec 06-architecture-and-roadmap and design/parser-nodes:
// "uniform Node with Kind discriminator, ordered children, Lineno/Source." One
// struct represents a literal, an operator, a statement, and the module root
// alike; the differences live in Kind, the scalar attributes (Str/Int/Float/Op),
// and the shape of Children. This keeps tree walks simple (one switch on Kind)
// at the cost of a few kind-specific accessor conventions, documented per Kind
// in kind.go.
package ast

import "github.com/avmnu-sng/quill-template-engine/source"

// Node is the single AST node type. Every Quill construct -- a module, a text
// span, an interpolation, an expression operator, a statement, a clause -- is a
// Node distinguished by Kind. Children are ordered and their meaning is fixed per
// Kind (see kind.go). Scalar payloads live in the typed fields so a consumer
// never has to re-parse text:
//
//   - Str   holds a name, keyword, decoded string literal, operator spelling,
//     filter/test/block/macro name, or strategy word, depending on Kind.
//   - Int   holds a decoded integer literal (KindInt) or a small enum/flag.
//   - Float holds a decoded float literal (KindFloat).
//   - Bool  holds a boolean literal (KindBool) or a per-kind flag (e.g. the
//     negation of a test, the "only"/"ignore missing" include flags).
//
// Line is 1-based; Src is the template the node came from. Both are filled by the
// parser so any diagnostic raised over a node names template:line exactly
// (spec 01 Section 1.8).
type Node struct {
	Kind     Kind
	Children []*Node

	Str   string
	Int   int64
	Float float64
	Bool  bool

	Line int
	Src  *source.Source
}

// New builds a node of kind k at the given position with the given children.
func New(k Kind, line int, src *source.Source, children ...*Node) *Node {
	return &Node{Kind: k, Line: line, Src: src, Children: children}
}

// Add appends a child and returns the receiver, for fluent construction.
func (n *Node) Add(c *Node) *Node {
	n.Children = append(n.Children, c)
	return n
}

// Child returns the i-th child or nil when out of range. It is the safe accessor
// for kind-specific positional children.
func (n *Node) Child(i int) *Node {
	if n == nil || i < 0 || i >= len(n.Children) {
		return nil
	}
	return n.Children[i]
}

// NumChildren returns the child count.
func (n *Node) NumChildren() int {
	if n == nil {
		return 0
	}
	return len(n.Children)
}
