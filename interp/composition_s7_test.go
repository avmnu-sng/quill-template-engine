package interp

import (
	"strings"
	"testing"

	"github.com/avmnusng/quill-template-engine/runtime"
)

func arr(pairs ...runtime.Pair) runtime.Value {
	a := runtime.NewArray()
	for _, p := range pairs {
		a.SetKey(p.Key, p.Val)
	}
	return runtime.Arr(a)
}

// --- map / object destructuring in @set (spec 01 Sections 2.1, 3.2) ---

func TestSetMapDestructureShorthand(t *testing.T) {
	eng := newStub(nil)
	user := arr(
		runtime.Pair{Key: runtime.Str("name"), Val: runtime.Str("Ada")},
		runtime.Pair{Key: runtime.Str("role"), Val: runtime.Str("admin")},
	)
	got := renderStub(t, eng, "@set {name, role} = user\n{{ name }}/{{ role }}",
		map[string]runtime.Value{"user": user})
	if got != "Ada/admin" {
		t.Fatalf("shorthand destructure: %q", got)
	}
}

func TestSetMapDestructureRename(t *testing.T) {
	eng := newStub(nil)
	user := arr(runtime.Pair{Key: runtime.Str("role"), Val: runtime.Str("admin")})
	got := renderStub(t, eng, "@set {role: title} = user\n{{ title }}",
		map[string]runtime.Value{"user": user})
	if got != "admin" {
		t.Fatalf("rename destructure: %q", got)
	}
}

func TestSetMapDestructureFromHostObject(t *testing.T) {
	eng := newStub(nil)
	u := runtime.Obj(&hostUser{name: "ada"})
	got := renderStub(t, eng, "@set {name} = u\n{{ name }}",
		map[string]runtime.Value{"u": u})
	if got != "ada" {
		t.Fatalf("object destructure: %q", got)
	}
}

func TestSetMapDestructureMissingStrict(t *testing.T) {
	eng := newStub(nil)
	user := arr(runtime.Pair{Key: runtime.Str("name"), Val: runtime.Str("Ada")})
	_, err := renderStubErr(t, eng, "@set {missing} = user\n{{ missing }}",
		map[string]runtime.Value{"user": user})
	if err == nil {
		t.Fatal("a missing key under strict variables must error")
	}
}

func TestSetMapDestructureMissingLenient(t *testing.T) {
	eng := newStub(nil)
	eng.strict = false
	user := arr(runtime.Pair{Key: runtime.Str("name"), Val: runtime.Str("Ada")})
	got := renderStub(t, eng, "@set {missing} = user\n[{{ missing }}]",
		map[string]runtime.Value{"user": user})
	if got != "[]" {
		t.Fatalf("lenient missing key should bind null: %q", got)
	}
}

// --- sequence destructuring in @set (spec 01 Section 3.2) ---

// list builds a sequence-shaped runtime value from values, for fixture data.
func list(vals ...runtime.Value) runtime.Value {
	a := runtime.NewArray()
	for i, v := range vals {
		a.SetInt(int64(i), v)
	}
	return runtime.Arr(a)
}

func TestSetListDestructureExact(t *testing.T) {
	eng := newStub(nil)
	got := renderStub(t, eng, "@set [a, b] = xs\n{{ a }}/{{ b }}",
		map[string]runtime.Value{"xs": list(runtime.Int(1), runtime.Int(2))})
	if got != "1/2" {
		t.Fatalf("exact-supply destructure: %q", got)
	}
}

// Over/under-supply must error by default rather than pad or drop (the de-PHP-ified
// generator-correctness default, spec 01 Section 3.2).
func TestSetListDestructureArityErrors(t *testing.T) {
	cases := []struct {
		name string
		xs   runtime.Value
	}{
		{"under-supply", list(runtime.Int(1))},
		{"over-supply", list(runtime.Int(1), runtime.Int(2), runtime.Int(3))},
		{"empty", list()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newStub(nil)
			_, err := renderStubErr(t, eng, "@set [a, b] = xs\n{{ a }}/{{ b }}",
				map[string]runtime.Value{"xs": tc.xs})
			if err == nil || !strings.Contains(err.Error(), "sequence destructuring expects") {
				t.Fatalf("arity mismatch must error, got %v", err)
			}
		})
	}
}

// A trailing "...rest" captures the remaining elements as a new sequence; the tail
// may be empty and over-supply past the fixed slots is then legal.
func TestSetListDestructureTail(t *testing.T) {
	cases := []struct {
		name string
		xs   runtime.Value
		want string
	}{
		{"multi-tail", list(runtime.Int(1), runtime.Int(2), runtime.Int(3)), "1|[2,3]"},
		{"single-tail", list(runtime.Int(1), runtime.Int(2)), "1|[2]"},
		{"empty-tail", list(runtime.Int(1)), "1|[]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newStub(nil)
			got := renderStub(t, eng, "@set [a, ...rest] = xs\n{{ a }}|{{ rest|json }}",
				map[string]runtime.Value{"xs": tc.xs})
			if got != tc.want {
				t.Fatalf("tail capture: got %q want %q", got, tc.want)
			}
		})
	}
}

// A tail with too few elements to fill the fixed slots is still an error.
func TestSetListDestructureTailUnderSupply(t *testing.T) {
	eng := newStub(nil)
	_, err := renderStubErr(t, eng, "@set [a, b, ...rest] = xs\n{{ a }}",
		map[string]runtime.Value{"xs": list(runtime.Int(1))})
	if err == nil || !strings.Contains(err.Error(), "at least 2 element") {
		t.Fatalf("tail under-supply must error, got %v", err)
	}
}

// A nested list pattern recurses into the corresponding element.
func TestSetListDestructureNested(t *testing.T) {
	eng := newStub(nil)
	got := renderStub(t, eng, "@set [a, [b, c]] = xs\n{{ a }}/{{ b }}/{{ c }}",
		map[string]runtime.Value{"xs": list(
			runtime.Int(1), list(runtime.Int(9), runtime.Int(8)))})
	if got != "1/9/8" {
		t.Fatalf("nested list destructure: %q", got)
	}
}

// A nested map pattern recurses into the corresponding element.
func TestSetListDestructureNestedMap(t *testing.T) {
	eng := newStub(nil)
	inner := arr(runtime.Pair{Key: runtime.Str("k"), Val: runtime.Str("v")})
	got := renderStub(t, eng, "@set [a, {k}] = xs\n{{ a }}/{{ k }}",
		map[string]runtime.Value{"xs": list(runtime.Int(1), inner)})
	if got != "1/v" {
		t.Fatalf("nested map destructure: %q", got)
	}
}

// An optional slot "b?" binds the element when present and null when the source
// is short (spec 01 Section 2.1). A required slot before it still enforces arity.
func TestSetListDestructureOptional(t *testing.T) {
	cases := []struct {
		name string
		xs   runtime.Value
		want string
	}{
		{"full", list(runtime.Int(1), runtime.Int(2)), "1/2"},
		{"short", list(runtime.Int(1)), "1/"}, // b is null -> renders empty
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newStub(nil)
			got := renderStub(t, eng, "@set [a, b?] = xs\n{{ a }}/{{ b }}",
				map[string]runtime.Value{"xs": tc.xs})
			if got != tc.want {
				t.Fatalf("optional slot: got %q want %q", got, tc.want)
			}
		})
	}
}

// A required slot with no element is still an error even when a later slot is
// optional: the source must cover every required position.
func TestSetListDestructureOptionalRequiredUnderSupply(t *testing.T) {
	eng := newStub(nil)
	_, err := renderStubErr(t, eng, "@set [a, b?] = xs\n{{ a }}",
		map[string]runtime.Value{"xs": list()})
	if err == nil || !strings.Contains(err.Error(), "at least 1 element") {
		t.Fatalf("required under-supply must error, got %v", err)
	}
}

// An elided slot "[, b]" / "[a, , c]" consumes a source position without binding,
// while the surrounding slots bind their elements (spec 01 Section 2.1).
func TestSetListDestructureElided(t *testing.T) {
	cases := []struct {
		name, src, want string
		xs              runtime.Value
	}{
		{"leading", "@set [, b] = xs\n{{ b }}", "20",
			list(runtime.Int(10), runtime.Int(20))},
		{"interior", "@set [a, , c] = xs\n{{ a }}/{{ c }}", "1/3",
			list(runtime.Int(1), runtime.Int(2), runtime.Int(3))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newStub(nil)
			got := renderStub(t, eng, tc.src, map[string]runtime.Value{"xs": tc.xs})
			if got != tc.want {
				t.Fatalf("elided slot: got %q want %q", got, tc.want)
			}
		})
	}
}

// An elided slot counts toward the required arity, so it still rejects a short
// source: "[a, , c]" needs three elements.
func TestSetListDestructureElidedArity(t *testing.T) {
	eng := newStub(nil)
	_, err := renderStubErr(t, eng, "@set [a, , c] = xs\n{{ a }}",
		map[string]runtime.Value{"xs": list(runtime.Int(1), runtime.Int(2))})
	if err == nil || !strings.Contains(err.Error(), "sequence destructuring expects") {
		t.Fatalf("elided arity mismatch must error, got %v", err)
	}
}

// Optional and elided slots compose with a "...rest" tail and nested patterns.
func TestSetListDestructureOptionalElidedTailNested(t *testing.T) {
	eng := newStub(nil)
	// [a, , [b, c]?, ...rest]: skip element 1, optionally destructure element 2 as a
	// pair, capture the rest. Full source exercises the present-optional path.
	got := renderStub(t, eng,
		"@set [a, , [b, c]?, ...rest] = xs\n{{ a }}|{{ b }}/{{ c }}|{{ rest|json }}",
		map[string]runtime.Value{"xs": list(
			runtime.Int(1),
			runtime.Int(99), // elided
			list(runtime.Int(7), runtime.Int(8)),
			runtime.Int(5), runtime.Int(6))})
	if got != "1|7/8|[5,6]" {
		t.Fatalf("composed destructure: %q", got)
	}
	// Short source: the optional nested pattern is absent, so b and c are null and the
	// tail is empty.
	got = renderStub(t, eng,
		"@set [a, , [b, c]?, ...rest] = xs\n{{ a }}|{{ b }}/{{ c }}|{{ rest|json }}",
		map[string]runtime.Value{"xs": list(runtime.Int(1), runtime.Int(99))})
	if got != "1|/|[]" {
		t.Fatalf("composed destructure (short): %q", got)
	}
}

// --- @use traits (spec 01 Section 5.4) ---

func traitEngine() *stubEngine {
	return newStub(map[string]string{
		"trait.ql": "@block submit {\n[trait submit]\n@}\n@block cancel {\n[trait cancel]\n@}\n",
		// a non-traitable template: it has free body content.
		"notrait.ql": "free text\n@block x {\ny\n@}\n",
		// a traitable template that itself uses another trait (nested).
		"outer.ql": "@use \"trait.ql\"\n@block extra {\n[extra]\n@}\n",
	})
}

func TestUseTraitPullsBlocks(t *testing.T) {
	eng := traitEngine()
	got := renderStub(t, eng, "@use \"trait.ql\"\n{{ block(\"submit\") }}{{ block(\"cancel\") }}", nil)
	if got != "[trait submit]\n[trait cancel]\n" {
		t.Fatalf("trait blocks not pulled: %q", got)
	}
}

func TestUseTraitOwnWinsAndParentReachesTrait(t *testing.T) {
	eng := traitEngine()
	// own submit overrides the trait's, and parent() reaches the trait version.
	got := renderStub(t, eng, "@use \"trait.ql\"\n@block submit {\n{{ parent() }}own wins\n@}", nil)
	if got != "[trait submit]\nown wins\n" {
		t.Fatalf("own-wins/parent precedence: %q", got)
	}
}

func TestUseTraitAlias(t *testing.T) {
	eng := traitEngine()
	got := renderStub(t, eng, "@use \"trait.ql\" with { cancel: dismiss }\n{{ block(\"dismiss\") }}", nil)
	if got != "[trait cancel]\n" {
		t.Fatalf("trait alias: %q", got)
	}
}

func TestUseTraitNested(t *testing.T) {
	eng := traitEngine()
	// outer.ql uses trait.ql; using outer.ql must transitively expose trait blocks.
	got := renderStub(t, eng, "@use \"outer.ql\"\n{{ block(\"submit\") }}{{ block(\"extra\") }}", nil)
	if got != "[trait submit]\n[extra]\n" {
		t.Fatalf("nested trait: %q", got)
	}
}

func TestUseNonTraitableErrors(t *testing.T) {
	eng := traitEngine()
	_, err := renderStubErr(t, eng, "@use \"notrait.ql\"\nx", nil)
	if err == nil || !strings.Contains(err.Error(), "cannot be used as a trait") {
		t.Fatalf("non-traitable use must error, got %v", err)
	}
}

func TestUseDynamicTargetErrors(t *testing.T) {
	eng := traitEngine()
	_, err := renderStubErr(t, eng, "@set t = \"trait.ql\"\n@use t\nx", nil)
	if err == nil || !strings.Contains(err.Error(), "constant string") {
		t.Fatalf("dynamic use target must error, got %v", err)
	}
}

func TestUseAliasUnknownBlockErrors(t *testing.T) {
	eng := traitEngine()
	_, err := renderStubErr(t, eng, "@use \"trait.ql\" with { nope: alias }\nx", nil)
	if err == nil || !strings.Contains(err.Error(), "not defined in trait") {
		t.Fatalf("alias of unknown trait block must error, got %v", err)
	}
}

// --- @cache region (spec 01 Section 4.7) ---

func TestCacheHitDoesNotReRender(t *testing.T) {
	eng := newStub(nil)
	// Two cache regions sharing a key: the second is a hit and reuses the first's
	// rendered body, so the side-effecting counter stays at 1 across both, and the
	// child-scope set does not leak (n outside stays 0).
	body := "@set n = 0\n" +
		"@cache key=\"k\" {\n@set n = n + 1\nbuild {{ n }}\n@}\n" +
		"@cache key=\"k\" {\n@set n = n + 1\nbuild {{ n }}\n@}\n" +
		"n={{ n }}"
	got := renderStub(t, eng, body, nil)
	if got != "build 1\nbuild 1\nn=0" {
		t.Fatalf("cache hit/scope: %q", got)
	}
}

func TestCacheDistinctKeys(t *testing.T) {
	eng := newStub(nil)
	body := "@set n = 0\n" +
		"@cache key=\"a\" {\n@set n = n + 1\nA{{ n }}\n@}\n" +
		"@cache key=\"b\" {\n@set n = n + 1\nB{{ n }}\n@}"
	got := renderStub(t, eng, body, nil)
	// distinct keys each render once in their own child scope.
	if got != "A1\nB1\n" {
		t.Fatalf("distinct cache keys: %q", got)
	}
}

func TestCacheTagInvalidation(t *testing.T) {
	eng := newStub(nil)
	rc := eng.RenderCache()
	rc.Put("test\x00k", "stale", []string{"nav"})
	rc.InvalidateTag("nav")
	if _, ok := rc.Get("test\x00k"); ok {
		t.Fatal("tag invalidation should drop the entry")
	}
}
