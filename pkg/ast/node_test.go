package ast

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

func TestNodeChildAndAdd(t *testing.T) {
	src := source.New("t", "")
	root := New(KindModule, 1, src)
	if root.NumChildren() != 0 {
		t.Fatalf("new node should have 0 children, got %d", root.NumChildren())
	}
	c := New(KindText, 1, src)
	c.Str = "hi"
	root.Add(c)
	if root.NumChildren() != 1 {
		t.Fatalf("after Add, want 1 child, got %d", root.NumChildren())
	}
	if got := root.Child(0); got != c {
		t.Fatalf("Child(0) mismatch")
	}
	if root.Child(5) != nil {
		t.Fatalf("out-of-range Child should be nil")
	}
	var nilNode *Node
	if nilNode.Child(0) != nil || nilNode.NumChildren() != 0 {
		t.Fatalf("nil receiver should be safe")
	}
}

func TestDumpShapes(t *testing.T) {
	src := source.New("t", "")
	tests := []struct {
		name string
		node *Node
		want string
	}{
		{
			name: "int",
			node: &Node{Kind: KindInt, Int: 42, Src: src},
			want: "(Int 42)",
		},
		{
			name: "string",
			node: &Node{Kind: KindString, Str: "hi", Src: src},
			want: `(String "hi")`,
		},
		{
			name: "nullsafe attr",
			node: &Node{Kind: KindAttr, Str: "x", Bool: true,
				Children: []*Node{{Kind: KindName, Str: "a"}}},
			want: "(Attr ?.x (Name a))",
		},
		{
			name: "elided slot renders underscore",
			node: &Node{Kind: KindListPattern, Children: []*Node{nil,
				{Kind: KindTarget, Str: "b"}}},
			want: "(ListPattern _ (Target b))",
		},
		{
			name: "negated test",
			node: &Node{Kind: KindTest, Str: "empty", Bool: true,
				Children: []*Node{{Kind: KindName, Str: "x"}}},
			want: "(Test not empty (Name x))",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Dump(tc.node); got != tc.want {
				t.Fatalf("Dump = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestKindString(t *testing.T) {
	if KindModule.String() != "Module" {
		t.Fatalf("KindModule label = %q", KindModule.String())
	}
	if Kind(9999).String() != "Kind(?)" {
		t.Fatalf("unknown kind label = %q", Kind(9999).String())
	}
}
