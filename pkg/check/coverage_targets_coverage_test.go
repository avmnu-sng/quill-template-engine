package check

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
)

// TestValidMapKeyUnion drives validMapKey through its previously-uncovered KUnion
// branch: a map<K, V> annotation whose key is a union. A union of only legal key
// kinds (int | string) is accepted; a union containing an illegal kind (bool) is
// rejected with a KindTypeCheck error whose message names the offending union and
// is positioned. This mirrors the runtime bool/float/null-subscript error, hoisted
// to load time (design/type-system.md Section 7.2).
func TestValidMapKeyUnion(t *testing.T) {
	t.Run("union of legal key kinds is accepted", func(t *testing.T) {
		// int | string are the two legal *Array key kinds; a union of them is legal,
		// so validMapKey's KUnion loop returns true and the template checks clean.
		src := "@types {\n  m: map<int | string, int>\n@}\n{{ m | length }}"
		if err := checkSrc(t, src, nil); err != nil {
			t.Fatalf("expected a union of int|string keys to be well-typed, got: %v", err)
		}
	})

	t.Run("multi-member union of legal kinds is accepted and the key is usable", func(t *testing.T) {
		// unionOf dedupes int|string|int down to the real two-member union int|string,
		// so validMapKey's KUnion loop iterates more than one arm and returns true.
		// (A `int | int` annotation would collapse to a plain int and never reach the
		// KUnion branch, so it is deliberately NOT used here.) Beyond "no error", assert
		// the union key is actually a usable *Array key: subscripting m with both an int
		// and a string literal type-checks, which only holds if the key kind is legal.
		src := "@types {\n  m: map<int | string | int, int>\n@}\n{{ m[1] }}{{ m[\"k\"] }}"
		if err := checkSrc(t, src, nil); err != nil {
			t.Fatalf("expected a multi-member union of legal keys to be well-typed and subscriptable, got: %v", err)
		}
	})

	t.Run("union with an illegal key kind is rejected naming the bad kind", func(t *testing.T) {
		// bool is not a legal key kind, so validMapKey's KUnion loop returns false and
		// validateType raises the map-key error. The message must name the bad key
		// kind (bool) and be a positioned KindTypeCheck diagnostic.
		src := "@types {\n  m: map<int | bool, int>\n@}\n{{ m | length }}"
		err := checkSrc(t, src, nil)
		if err == nil {
			t.Fatalf("expected a bad-map-key error, got none")
		}
		if errors.KindOf(err) != errors.KindTypeCheck {
			t.Fatalf("expected KindTypeCheck, got %v: %v", errors.KindOf(err), err)
		}
		if !strings.Contains(err.Error(), "map key type must be int or string") {
			t.Fatalf("error %q does not explain the map-key rule", err.Error())
		}
		// The rejected union is rendered in the diagnostic, naming the bad bool kind.
		if !strings.Contains(err.Error(), "bool") {
			t.Fatalf("error %q does not name the offending bool key kind", err.Error())
		}
		if !strings.Contains(err.Error(), "t.ql:") {
			t.Fatalf("error %q is not positioned", err.Error())
		}
	})

	t.Run("nested union key inside a list is still rejected", func(t *testing.T) {
		// validateType recurses into a list<map<...>>; the nested union key is checked
		// by the same KUnion branch of validMapKey. float is illegal.
		src := "@types {\n  m: list<map<string | float, int>>\n@}\n{{ m | length }}"
		err := checkSrc(t, src, nil)
		if err == nil {
			t.Fatalf("expected a nested bad-map-key error, got none")
		}
		if errors.KindOf(err) != errors.KindTypeCheck {
			t.Fatalf("expected KindTypeCheck, got %v: %v", errors.KindOf(err), err)
		}
		if !strings.Contains(err.Error(), "map key type must be int or string") {
			t.Fatalf("error %q does not explain the map-key rule", err.Error())
		}
		if !strings.Contains(err.Error(), "float") {
			t.Fatalf("error %q does not name the offending float key kind", err.Error())
		}
	})
}

// TestNumericUnion drives numeric() through its KUnion branch by forcing an
// arithmetic context over a union-typed operand. A union of only numbers
// (int | float) satisfies numeric() and the arithmetic yields a number; a union
// that also carries a non-number (int | string) fails numeric() and promotes the
// runtime arithmetic type error to load time (check/expr2.go arithType).
func TestNumericUnion(t *testing.T) {
	t.Run("arithmetic over a numeric union is accepted", func(t *testing.T) {
		// numeric(int | float) walks the KUnion branch, finds every member numeric,
		// and returns true, so `x + 1` type-checks. The result is float (mixed tower),
		// which is renderable, so the whole template is clean.
		src := "@types {\n  x: int | float\n@}\n{{ x + 1 }}"
		if err := checkSrc(t, src, nil); err != nil {
			t.Fatalf("expected arithmetic over int|float to be well-typed, got: %v", err)
		}
	})

	t.Run("nested numeric union is accepted", func(t *testing.T) {
		// A union whose members are themselves numeric (int | float, flattened) still
		// returns true from numeric()'s KUnion loop.
		src := "@types {\n  x: (int | float) | int\n@}\n{{ x + 1 }}"
		if err := checkSrc(t, src, nil); err != nil {
			t.Fatalf("expected arithmetic over a nested numeric union to be well-typed, got: %v", err)
		}
	})

	t.Run("arithmetic over a union with a non-number is rejected", func(t *testing.T) {
		// numeric(int | string) returns false on the string member, so arithType raises
		// the promoted arithmetic type error naming the whole union operand type.
		src := "@types {\n  x: int | string\n@}\n{{ x + 1 }}"
		err := checkSrc(t, src, nil)
		if err == nil {
			t.Fatalf("expected an arithmetic type error, got none")
		}
		if errors.KindOf(err) != errors.KindTypeCheck {
			t.Fatalf("expected KindTypeCheck, got %v: %v", errors.KindOf(err), err)
		}
		if !strings.Contains(err.Error(), "operator + requires a number") {
			t.Fatalf("error %q does not report the arithmetic requirement", err.Error())
		}
		// The offending operand type (the union) is named in the diagnostic.
		if !strings.Contains(err.Error(), "string") {
			t.Fatalf("error %q does not name the non-numeric union member", err.Error())
		}
		if !strings.Contains(err.Error(), "t.ql:") {
			t.Fatalf("error %q is not positioned", err.Error())
		}
	})

	t.Run("ordering a numeric union against a number is accepted", func(t *testing.T) {
		// checkOrder also consults numeric(); a numeric union is order-comparable
		// against an int literal, exercising numeric()'s union branch a second way.
		src := "@types {\n  x: int | float\n@}\n{{ \"y\" if x < 1 }}"
		if err := checkSrc(t, src, nil); err != nil {
			t.Fatalf("expected ordering a numeric union to be well-typed, got: %v", err)
		}
	})
}

// TestBindPatternDestructure drives bindPattern, the destructuring path of
// checkSet taken when a single pattern is bound from several values (targets and
// values differ in count). Each slot's declared type is read into scope by
// bindPatternNames, so a slot annotation governs how the bound name checks at its
// USE site: a well-typed use passes, an inconsistent use promotes the runtime
// error to load time. This is the observable effect of bindPattern binding the
// pattern's slots with their annotated types rather than plain any.
func TestBindPatternDestructure(t *testing.T) {
	t.Run("slot bound as int is concretely int at use", func(t *testing.T) {
		// `[a: int, b] = 1, 2` is one ListPattern target against two values, so checkSet
		// calls bindPattern. Slot a is bound int, so `a + 1` type-checks (int + int).
		src := "@set [a: int, b] = 1, 2\n{{ a + 1 }}"
		if err := checkSrc(t, src, nil); err != nil {
			t.Fatalf("expected an int-annotated slot to check cleanly, got: %v", err)
		}
		// Clean arithmetic alone is WEAK: an undefined name would resolve to the any-floor
		// and `any + 1` is also clean, so "no error" does not prove a is concretely int.
		// Order a against a string: this errors "cannot order int against string" ONLY if
		// a is bound as the concrete int annotation (an any-floor binding orders silently).
		// This is the discriminating proof that bindPattern read the slot's int type.
		orderSrc := "@set [a: int, b] = 1, 2\n{{ \"z\" if a < \"s\" }}"
		err := checkSrc(t, orderSrc, nil)
		if err == nil {
			t.Fatalf("expected ordering a concrete int slot against a string to error; slot was not bound as int")
		}
		if errors.KindOf(err) != errors.KindTypeCheck {
			t.Fatalf("expected KindTypeCheck, got %v: %v", errors.KindOf(err), err)
		}
		if !strings.Contains(err.Error(), "cannot order int against string") {
			t.Fatalf("error %q does not show the slot was bound as the concrete int type", err.Error())
		}
	})

	t.Run("unannotated slot orders silently as the any-floor (negative control)", func(t *testing.T) {
		// The discriminator's control: with NO annotation, slot a binds any, so the same
		// `a < "s"` ordering is silent. This is what makes the int-slot order error above
		// meaningful rather than a property of the destructure itself.
		src := "@set [a, b] = 1, 2\n{{ \"z\" if a < \"s\" }}"
		if err := checkSrc(t, src, nil); err != nil {
			t.Fatalf("expected an unannotated slot to order silently under the any-floor, got: %v", err)
		}
	})

	t.Run("string-annotated slot rejects a numeric use", func(t *testing.T) {
		// bindPattern binds slot a as string; using it in arithmetic then promotes the
		// runtime arithmetic error to load time, proving the slot's declared type was
		// checked at the use site.
		src := "@set [a: string, b] = 1, 2\n{{ a + 1 }}"
		err := checkSrc(t, src, nil)
		if err == nil {
			t.Fatalf("expected a type error from a string slot used in arithmetic, got none")
		}
		if errors.KindOf(err) != errors.KindTypeCheck {
			t.Fatalf("expected KindTypeCheck, got %v: %v", errors.KindOf(err), err)
		}
		if !strings.Contains(err.Error(), "operator + requires a number, found string") {
			t.Fatalf("error %q does not reflect the string-typed slot", err.Error())
		}
		if !strings.Contains(err.Error(), "t.ql:") {
			t.Fatalf("error %q is not positioned", err.Error())
		}
	})

	t.Run("list-annotated slot is a non-renderable at use", func(t *testing.T) {
		// A slot bound as list<int> is not renderable, so printing it is a check error,
		// again showing bindPattern carried the slot annotation into scope.
		src := "@set [a: list<int>, b] = 1, 2\n{{ a }}"
		err := checkSrc(t, src, nil)
		if err == nil {
			t.Fatalf("expected a render error from a list-typed slot, got none")
		}
		if errors.KindOf(err) != errors.KindTypeCheck {
			t.Fatalf("expected KindTypeCheck, got %v: %v", errors.KindOf(err), err)
		}
		if !strings.Contains(err.Error(), "cannot render a value of type list<int>") {
			t.Fatalf("error %q does not reflect the list-typed slot", err.Error())
		}
	})

	t.Run("unannotated slots bind as any and never error", func(t *testing.T) {
		// With no slot annotations, bindPattern binds every name as any, so the same
		// destructure with a numeric use is silent (the dynamic floor).
		src := "@set [a, b] = 1, 2\n{{ a + 1 }}"
		if err := checkSrc(t, src, nil); err != nil {
			t.Fatalf("expected unannotated destructured slots to be silent, got: %v", err)
		}
	})

	t.Run("map-pattern slots bind as any and render cleanly", func(t *testing.T) {
		// `{x, y}` is a MapPattern with two MapTarget slots (no `x: alias` rename), so
		// bindPattern -> bindPatternNames binds x and y as any via the KindMapTarget arm.
		// Both are renderable, so the template is silent. NOTE: an *undefined* name would
		// also resolve to any and render silently, so "renders" alone does not prove the
		// slots bound; the strong signal is in the sibling subtest below, where a slot's
		// value is fed into arithmetic that only the any-floor binding can survive.
		src := "@set {x, y} = 1, 2\n{{ x }}{{ y }}"
		if err := checkSrc(t, src, nil); err != nil {
			t.Fatalf("expected map destructure across two values to be silent, got: %v", err)
		}
	})
}
