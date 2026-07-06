package ast

import (
	"fmt"
	"strconv"
	"strings"
)

// Dump renders a node and its subtree as a compact, deterministic S-expression
// for tests and debugging. The form is "(Kind payload child child ...)"; a leaf
// with no payload renders as "(Kind)". A nil child (an elided destructuring slot)
// renders as "_". The payload is whichever scalar field is meaningful for the
// kind, so a precedence or structure test can assert against one stable string.
func Dump(n *Node) string {
	var b strings.Builder
	dump(&b, n)
	return b.String()
}

func dump(b *strings.Builder, n *Node) {
	if n == nil {
		b.WriteString("_")
		return
	}
	b.WriteByte('(')
	b.WriteString(n.Kind.String())
	if p := payload(n); p != "" {
		b.WriteByte(' ')
		b.WriteString(p)
	}
	for _, c := range n.Children {
		b.WriteByte(' ')
		dump(b, c)
	}
	b.WriteByte(')')
}

// payload returns the human-facing scalar for a kind, or "" when none applies.
func payload(n *Node) string {
	switch n.Kind {
	case KindInt:
		return strconv.FormatInt(n.Int, 10)
	case KindFloat:
		return strconv.FormatFloat(n.Float, 'g', -1, 64)
	case KindBool:
		return strconv.FormatBool(n.Bool)
	case KindString:
		return strconv.Quote(n.Str)
	case KindName, KindSpecialName, KindAttr, KindBinary, KindLogical,
		KindUnary, KindFilter, KindEscape, KindImport, KindBlock, KindMacro,
		KindCapture, KindTarget, KindParam, KindApplyFilter, KindCacheArg,
		KindTypeDecl, KindType, KindUse, KindProvide, KindYield, KindCallBlock:
		return labelWithFlags(n)
	case KindMembership, KindTest:
		s := n.Str
		if n.Bool {
			s = "not " + s
		}
		return s
	case KindMapEntry, KindArg, KindFor, KindSet, KindInclude, KindEmbed,
		KindIndex, KindSlice, KindClause, KindGuard, KindDeprecated,
		KindFromItem, KindMapTarget, KindLine:
		return flagPayload(n)
	}
	return ""
}

// labelWithFlags formats a kind whose primary payload is Str, appending a "?" for
// the null-safe attribute form, a "..." marker for a variadic param, or a "**"
// marker for a kwargs-tail param.
func labelWithFlags(n *Node) string {
	s := n.Str
	switch n.Kind {
	case KindAttr:
		if n.Bool {
			return "?." + s
		}
		return "." + s
	case KindParam:
		if n.Bool {
			return "..." + s
		}
		if n.Int&ParamKwargs != 0 {
			return "**" + s
		}
		return s
	}
	return s
}

// flagPayload formats kinds whose discriminating payload is the Int field (a form
// tag, a flag bitset, or a count) plus, where relevant, a Str.
func flagPayload(n *Node) string {
	switch n.Kind {
	case KindMembership:
		return n.Str
	case KindArg:
		if n.Int == ArgNamed {
			return "named:" + n.Str
		}
		if n.Int == ArgSpread {
			return "spread"
		}
		return ""
	case KindMapEntry:
		switch n.Int {
		case MapEntryShorthand:
			return "shorthand"
		case MapEntryComputed:
			return "computed"
		case MapEntrySpread:
			return "spread"
		}
		return "keyed"
	case KindIndex:
		if n.Bool {
			return "nullsafe"
		}
		return ""
	case KindFor:
		s := fmt.Sprintf("targets=%d else=%t", n.Int&ForTargetCount, n.Bool)
		if n.Int&ForRecursive != 0 {
			s += " recursive"
		}
		return s
	case KindSet:
		return fmt.Sprintf("targets=%d", n.Int)
	case KindLine:
		return strconv.FormatInt(n.Int, 10)
	case KindInclude, KindEmbed:
		return incFlags(n.Int)
	case KindClause:
		if n.Bool {
			return "if"
		}
		return "else"
	case KindGuard:
		return n.Str
	case KindDeprecated:
		return strconv.Quote(n.Str)
	case KindFromItem:
		if n.Bool {
			return n.Str + " as"
		}
		return n.Str
	case KindMapTarget:
		if n.Bool {
			return n.Str + " as"
		}
		return n.Str
	}
	return ""
}

// incFlags renders the include-modifier bitset compactly.
func incFlags(f int64) string {
	var parts []string
	if f&IncWith != 0 {
		parts = append(parts, "with")
	}
	if f&IncOnly != 0 {
		parts = append(parts, "only")
	}
	if f&IncIgnoreMissing != 0 {
		parts = append(parts, "ignore-missing")
	}
	return strings.Join(parts, ",")
}
