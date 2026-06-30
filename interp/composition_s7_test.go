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
