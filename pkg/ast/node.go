// Package ast is Quill's abstract syntax tree: a single uniform Node type whose
// Kind field discriminates every construct, with ordered Children and a 1-based
// source position. The parser (package parse) builds these; the checker and the
// renderer walk them.
//
// The design follows spec 06-architecture-and-roadmap and design/parser-nodes:
// "uniform Node with Kind discriminator, ordered children, Lineno/Source." One
// struct represents a literal, an operator, a statement, and the module root
// alike; the differences live in Kind, the scalar attributes (Str/Int/Float/Bool),
// and the shape of Children. This keeps tree walks simple (one switch on Kind)
// at the cost of a few kind-specific accessor conventions, documented per Kind
// in kind.go.
//
// A Node is immutable after parse. Once the parser returns a module, its tree is
// treated as read-only for the rest of the process: the same *Node is shared by
// pointer across cache keys and read concurrently by the interpreter, the
// coverage collector (package cover), and the checker (package check) without
// synchronization. Nothing downstream of the parser mutates a Node or its
// Children; a walk that needs to transform the tree must build new nodes.
package ast

import (
	"math"

	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// Node is the single AST node type. Every Quill construct (a module, a text
// span, an interpolation, an expression operator, a statement, a clause) is a
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
// Line and Col are 1-based; Src is the template the node came from. All are
// filled by the parser so any diagnostic raised over a node names template:line
// exactly (spec 01 Section 1.8), and so coverage can anchor a region at an exact
// line:col position (see package cover). Col is metadata only: it is never
// consulted during evaluation, so filling it cannot change rendered output.
type Node struct {
	Kind     Kind
	Children []*Node

	Str   string
	Int   int64
	Float float64
	Bool  bool

	Line int
	Col  int
	Src  *source.Source
}

// New builds a node of kind k at the given line position with the given
// children. Col is left zero; the parser sets it via node() when a column is
// known. A zero Col means "column unknown" and never breaks a region key.
func New(k Kind, line int, src *source.Source, children ...*Node) *Node {
	return &Node{Kind: k, Line: line, Src: src, Children: children}
}

// NewAt builds a node of kind k at an exact 1-based line:col in src. It is the
// position-complete constructor the parser prefers so coverage regions carry a
// column; New remains for synthetic nodes and tests that only have a line.
func NewAt(k Kind, line, col int, src *source.Source, children ...*Node) *Node {
	return &Node{Kind: k, Line: line, Col: col, Src: src, Children: children}
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

// IntCount reads the Int payload as a non-negative int count. Kinds that carry a
// child count (a @set's targets, an @apply's filters, a @cache's tag args, and
// so on) store it as int64(len(...)), so it always fits int; the bounds guard
// makes that invariant explicit: it is dead on a 64-bit platform and only
// clamps the physically unrepresentable case where int is 32-bit.
func (n *Node) IntCount() int {
	if n.Int < 0 || n.Int > math.MaxInt {
		return 0
	}
	return int(n.Int)
}
