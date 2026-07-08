package sandbox

import "testing"

// TestTypeGraphAncestors checks the type-graph walk: a type's own name comes
// first, then transitive supertypes/interfaces, with a cyclic-declaration guard.
func TestTypeGraphAncestors(t *testing.T) {
	g := NewTypeGraph()
	g.Declare("Admin", "User")
	g.Declare("User", "Entity", "Stringer")

	got := g.ancestors("Admin")
	want := map[string]bool{"Admin": true, "User": true, "Entity": true, "Stringer": true}
	if len(got) != len(want) {
		t.Fatalf("ancestors(Admin) = %v, want the 4 declared ancestors", got)
	}
	if got[0] != "Admin" {
		t.Errorf("ancestors must list the type itself first, got %q", got[0])
	}
	for _, a := range got {
		if !want[a] {
			t.Errorf("unexpected ancestor %q", a)
		}
	}

	// A type with no declared parents matches only its own name.
	if solo := g.ancestors("Loner"); len(solo) != 1 || solo[0] != "Loner" {
		t.Errorf("undeclared type should match only itself, got %v", solo)
	}
}

// TestTypeGraphCycleGuard ensures a cyclic declaration cannot loop the walk.
func TestTypeGraphCycleGuard(t *testing.T) {
	g := NewTypeGraph()
	g.Declare("A", "B")
	g.Declare("B", "A")
	got := g.ancestors("A")
	if len(got) != 2 {
		t.Fatalf("cyclic graph should yield {A,B}, got %v", got)
	}
}

// TestPolicyAllowsFlatLists checks the tag/filter/function allowlists, including
// that a nil policy and a zero-value policy deny everything (uniform allowlist,
// no grandfathering, B6).
func TestPolicyAllowsFlatLists(t *testing.T) {
	var nilPol *Policy
	if nilPol.AllowsTag("for") || nilPol.AllowsFilter("upper") || nilPol.AllowsFunction("range") {
		t.Fatal("a nil policy must deny everything")
	}
	empty := NewPolicy()
	if empty.AllowsTag("for") {
		t.Fatal("an empty policy must deny every tag (no grandfathering)")
	}

	p := NewPolicy(
		AllowTags("for", "if"),
		AllowFilters("upper"),
		AllowFunctions("range"),
	)
	if !p.AllowsTag("for") || !p.AllowsTag("if") {
		t.Error("allowed tags rejected")
	}
	if p.AllowsTag("include") {
		t.Error("unlisted tag allowed")
	}
	if !p.AllowsFilter("upper") || p.AllowsFilter("raw") {
		t.Error("filter allowlist wrong")
	}
	if !p.AllowsFunction("range") || p.AllowsFunction("dump") {
		t.Error("function allowlist wrong")
	}
}

// TestPolicyMethodsAndPropertiesViaGraph checks that a per-type allowlist entry
// on a base type or interface covers a registered subtype (B4/B5), and that
// matching is case-sensitive (the Go-native choice).
func TestPolicyMethodsAndPropertiesViaGraph(t *testing.T) {
	g := NewTypeGraph()
	g.Declare("Admin", "User")
	g.Declare("User", "Entity")

	p := NewPolicy(
		AllowMethods("Entity", "Name"),
		AllowProperties("User", "ID"),
		WithTypeGraph(g),
	)

	// Name() allowed on Entity covers Admin (Admin -> User -> Entity).
	if !p.AllowsMethod("Admin", "Name") {
		t.Error("base-type method entry should cover subtype")
	}
	// A method not listed anywhere is denied.
	if p.AllowsMethod("Admin", "Permissions") {
		t.Error("unlisted method allowed")
	}
	// Case-sensitive: "name" is not "Name".
	if p.AllowsMethod("Admin", "name") {
		t.Error("method matching must be case-sensitive")
	}
	// Property allowed on User covers Admin but not the Entity base.
	if !p.AllowsProperty("Admin", "ID") {
		t.Error("subtype should inherit a supertype's property entry")
	}
	if p.AllowsProperty("Entity", "ID") {
		t.Error("a base type must not inherit a subtype's property entry")
	}
}

// TestPolicyKnows covers the strict-mode unknown-type discriminator: a type is
// known when it (or a declared ancestor) has a method/property allowlist entry or
// an edge in the type-graph; otherwise the policy does not know it.
func TestPolicyKnows(t *testing.T) {
	g := NewTypeGraph()
	g.Declare("Admin", "User")

	p := NewPolicy(
		AllowMethods("Entity", "Name"),
		WithTypeGraph(g),
	)
	// A type with a method allowlist entry is known.
	if !p.Knows("Entity") {
		t.Error("type with a method entry should be known")
	}
	// A type present only as a type-graph edge is known.
	if !p.Knows("Admin") {
		t.Error("type with a type-graph edge should be known")
	}
	// A type the policy never mentions is unknown.
	if p.Knows("Stranger") {
		t.Error("unmentioned type must be unknown")
	}
	// A nil policy knows nothing.
	var nilp *Policy
	if nilp.Knows("Entity") {
		t.Error("nil policy must know nothing")
	}
}

// TestPolicyStrict covers the Strict option/accessor: a policy built without the
// option is lenient, one built with it is strict, and a nil policy is lenient.
func TestPolicyStrict(t *testing.T) {
	if NewPolicy().Strict() {
		t.Error("a policy without the Strict option must be lenient")
	}
	if !NewPolicy(Strict()).Strict() {
		t.Error("a policy built with Strict() must report strict")
	}
	var nilp *Policy
	if nilp.Strict() {
		t.Error("a nil policy must be lenient")
	}
}
