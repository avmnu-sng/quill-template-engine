package ast

import "testing"

// TestDumpPayloadExhaustive drives Dump over one node of every payload-bearing
// kind so the payload/labelWithFlags/flagPayload/incFlags dispatch is exercised
// end to end. Each case pins the exact S-expression string, which is the stable
// contract tests and the parser golden dumps rely on.
func TestDumpPayloadExhaustive(t *testing.T) {
	cases := []struct {
		name string
		node *Node
		want string
	}{
		// --- scalar literals ---
		{"float", &Node{Kind: KindFloat, Float: 1.5}, "(Float 1.5)"},
		{"bool true", &Node{Kind: KindBool, Bool: true}, "(Bool true)"},
		{"bool false", &Node{Kind: KindBool, Bool: false}, "(Bool false)"},
		{"null has no payload", &Node{Kind: KindNull}, "(Null)"},

		// --- labelWithFlags kinds ---
		{"name", &Node{Kind: KindName, Str: "x"}, "(Name x)"},
		{"plain attr", &Node{Kind: KindAttr, Str: "y",
			Children: []*Node{{Kind: KindName, Str: "a"}}}, "(Attr .y (Name a))"},
		{"param plain", &Node{Kind: KindParam, Str: "p"}, "(Param p)"},
		{"param variadic", &Node{Kind: KindParam, Str: "rest", Bool: true},
			"(Param ...rest)"},
		{"binary label", &Node{Kind: KindBinary, Str: "+",
			Children: []*Node{{Kind: KindInt, Int: 1}, {Kind: KindInt, Int: 2}}},
			"(Binary + (Int 1) (Int 2))"},
		{"escape strategy label", &Node{Kind: KindEscape, Str: "html"},
			"(Escape html)"},
		{"block name label", &Node{Kind: KindBlock, Str: "hdr"}, "(Block hdr)"},

		// --- membership / test ---
		{"membership plain", &Node{Kind: KindMembership, Str: "in",
			Children: []*Node{{Kind: KindName, Str: "x"}, {Kind: KindName, Str: "xs"}}},
			"(Membership in (Name x) (Name xs))"},
		{"test negated", &Node{Kind: KindTest, Str: "empty", Bool: true,
			Children: []*Node{{Kind: KindName, Str: "x"}}},
			"(Test not empty (Name x))"},
		{"test plain", &Node{Kind: KindTest, Str: "int",
			Children: []*Node{{Kind: KindName, Str: "x"}}},
			"(Test int (Name x))"},

		// --- flagPayload: Arg forms ---
		{"arg positional", &Node{Kind: KindArg, Int: ArgPositional,
			Children: []*Node{{Kind: KindInt, Int: 1}}}, "(Arg (Int 1))"},
		{"arg named", &Node{Kind: KindArg, Int: ArgNamed, Str: "k",
			Children: []*Node{{Kind: KindInt, Int: 1}}}, "(Arg named:k (Int 1))"},
		{"arg spread", &Node{Kind: KindArg, Int: ArgSpread,
			Children: []*Node{{Kind: KindName, Str: "xs"}}}, "(Arg spread (Name xs))"},

		// --- flagPayload: MapEntry forms ---
		{"map entry keyed", &Node{Kind: KindMapEntry, Int: MapEntryKeyed,
			Children: []*Node{{Kind: KindString, Str: "k"}, {Kind: KindInt, Int: 1}}},
			`(MapEntry keyed (String "k") (Int 1))`},
		{"map entry shorthand", &Node{Kind: KindMapEntry, Int: MapEntryShorthand,
			Children: []*Node{{Kind: KindName, Str: "a"}}},
			"(MapEntry shorthand (Name a))"},
		{"map entry computed", &Node{Kind: KindMapEntry, Int: MapEntryComputed,
			Children: []*Node{{Kind: KindName, Str: "e"}, {Kind: KindInt, Int: 1}}},
			"(MapEntry computed (Name e) (Int 1))"},
		{"map entry spread", &Node{Kind: KindMapEntry, Int: MapEntrySpread,
			Children: []*Node{{Kind: KindName, Str: "m"}}},
			"(MapEntry spread (Name m))"},

		// --- flagPayload: Index / Slice-ish ---
		{"index plain", &Node{Kind: KindIndex,
			Children: []*Node{{Kind: KindName, Str: "a"}, {Kind: KindInt, Int: 0}}},
			"(Index (Name a) (Int 0))"},
		{"index nullsafe", &Node{Kind: KindIndex, Bool: true,
			Children: []*Node{{Kind: KindName, Str: "a"}, {Kind: KindInt, Int: 0}}},
			"(Index nullsafe (Name a) (Int 0))"},

		// --- flagPayload: For / Set ---
		{"for one target no else", &Node{Kind: KindFor, Int: 1, Bool: false},
			"(For targets=1 else=false)"},
		{"for two targets else", &Node{Kind: KindFor, Int: 2, Bool: true},
			"(For targets=2 else=true)"},
		{"set targets", &Node{Kind: KindSet, Int: 2}, "(Set targets=2)"},

		// --- flagPayload: Line / Clause / Guard / Deprecated ---
		{"line", &Node{Kind: KindLine, Int: 7}, "(Line 7)"},
		{"clause if", &Node{Kind: KindClause, Bool: true}, "(Clause if)"},
		{"clause else", &Node{Kind: KindClause, Bool: false}, "(Clause else)"},
		{"guard", &Node{Kind: KindGuard, Str: "filter"}, "(Guard filter)"},
		{"deprecated", &Node{Kind: KindDeprecated, Str: "old"}, `(Deprecated "old")`},

		// --- flagPayload: FromItem / MapTarget with and without alias ---
		{"from item no alias", &Node{Kind: KindFromItem, Str: "a"}, "(FromItem a)"},
		{"from item alias", &Node{Kind: KindFromItem, Str: "a", Bool: true},
			"(FromItem a as)"},
		{"map target no alias", &Node{Kind: KindMapTarget, Str: "k"},
			"(MapTarget k)"},
		{"map target alias", &Node{Kind: KindMapTarget, Str: "k", Bool: true},
			"(MapTarget k as)"},

		// --- flagPayload: Include / Embed modifier bitset (incFlags) ---
		{"include no flags", &Node{Kind: KindInclude, Int: 0,
			Children: []*Node{{Kind: KindString, Str: "p"}}},
			`(Include (String "p"))`},
		{"include with only", &Node{Kind: KindInclude, Int: IncWith | IncOnly,
			Children: []*Node{{Kind: KindString, Str: "p"}}},
			`(Include with,only (String "p"))`},
		{"include all flags", &Node{Kind: KindEmbed,
			Int: IncWith | IncOnly | IncIgnoreMissing},
			"(Embed with,only,ignore-missing)"},

		// --- a kind with no payload branch falls through to "" ---
		{"module no payload", &Node{Kind: KindModule}, "(Module)"},
		{"print no payload", &Node{Kind: KindPrint,
			Children: []*Node{{Kind: KindName, Str: "x"}}}, "(Print (Name x))"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Dump(tc.node); got != tc.want {
				t.Fatalf("Dump = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDumpNilAndNested checks the recursive structure: a nil child renders as
// "_", and nesting composes payloads left to right.
func TestDumpNilAndNested(t *testing.T) {
	if got := Dump(nil); got != "_" {
		t.Fatalf("Dump(nil) = %q, want _", got)
	}
	n := &Node{Kind: KindListPattern, Children: []*Node{
		nil,
		{Kind: KindTarget, Str: "b"},
		{Kind: KindSpread, Children: []*Node{{Kind: KindTarget, Str: "rest"}}},
	}}
	want := "(ListPattern _ (Target b) (Spread (Target rest)))"
	if got := Dump(n); got != want {
		t.Fatalf("Dump = %q, want %q", got, want)
	}
}

// TestNewAndAddFluent checks New wires Kind/Line/Src/children and Add is fluent.
func TestNewAndAddFluent(t *testing.T) {
	a := New(KindName, 3, nil)
	if a.Kind != KindName || a.Line != 3 {
		t.Fatalf("New did not set fields: %+v", a)
	}
	b := New(KindInt, 4, nil)
	got := a.Add(b)
	if got != a {
		t.Fatalf("Add must return the receiver for fluent chaining")
	}
	if a.NumChildren() != 1 || a.Child(0) != b {
		t.Fatalf("Add did not append the child")
	}
	// Negative index is out of range and safe.
	if a.Child(-1) != nil {
		t.Fatalf("negative index must be nil")
	}
}
