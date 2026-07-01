package loader

import (
	"embed"
	"testing"
)

//go:embed testdata/embed
var embedFS embed.FS

// TestChainLoaderFallback checks that the chain serves the first hit, that an
// earlier loader shadows a later one for the same name, and that a name absent
// from every loader is reported not-found.
func TestChainLoaderFallback(t *testing.T) {
	base := NewArrayLoader(map[string]string{
		"shared.ql": "base-shared",
		"only.ql":   "base-only",
	})
	override := NewArrayLoader(map[string]string{
		"shared.ql": "override-shared",
	})
	chain := NewChainLoader(override, base)

	cases := []struct {
		name string
		want string
	}{
		{"shared.ql", "override-shared"}, // earlier loader wins
		{"only.ql", "base-only"},         // falls through to base
	}
	for _, tc := range cases {
		src, err := chain.Get(tc.name)
		if err != nil {
			t.Fatalf("Get(%q): %v", tc.name, err)
		}
		if src.Code() != tc.want {
			t.Errorf("Get(%q) = %q, want %q", tc.name, src.Code(), tc.want)
		}
		if !chain.Exists(tc.name) {
			t.Errorf("Exists(%q) = false, want true", tc.name)
		}
	}

	if chain.Exists("missing.ql") {
		t.Error("Exists(missing.ql) = true, want false")
	}
	if _, err := chain.Get("missing.ql"); !IsNotFound(err) {
		t.Errorf("Get(missing.ql) err = %v, want not-found", err)
	}
}

// TestChainLoaderEmpty checks an empty chain reports every name not-found.
func TestChainLoaderEmpty(t *testing.T) {
	chain := NewChainLoader()
	if chain.Exists("x.ql") {
		t.Error("empty chain should have nothing")
	}
	if _, err := chain.Get("x.ql"); !IsNotFound(err) {
		t.Errorf("err = %v, want not-found", err)
	}
}

// TestChainLoaderCopiesSlice checks that mutating the caller's slice after
// construction does not change the chain.
func TestChainLoaderCopiesSlice(t *testing.T) {
	loaders := []Loader{NewArrayLoader(map[string]string{"a.ql": "A"})}
	chain := NewChainLoader(loaders...)
	loaders[0] = NewArrayLoader(map[string]string{"a.ql": "MUTATED"})
	src, err := chain.Get("a.ql")
	if err != nil {
		t.Fatal(err)
	}
	if src.Code() != "A" {
		t.Errorf("code = %q, want A", src.Code())
	}
}

// TestPrefixLoaderRouting checks that a prefixed name reaches the registered
// sub-loader with the prefix stripped, that the returned source keeps the
// fully-qualified name, and that longest-prefix wins.
func TestPrefixLoaderRouting(t *testing.T) {
	lang := NewArrayLoader(map[string]string{"header": "LANG-HEADER"})
	langDe := NewArrayLoader(map[string]string{"header": "LANG-DE-HEADER"})
	mail := NewArrayLoader(map[string]string{"welcome": "MAIL-WELCOME"})
	pl := NewPrefixLoader(map[string]Loader{
		"lang":    lang,
		"lang/de": langDe, // longer prefix must win over "lang"
		"mail":    mail,
	})

	cases := []struct {
		name string
		want string
	}{
		{"lang/header", "LANG-HEADER"},
		{"lang/de/header", "LANG-DE-HEADER"},
		{"mail/welcome", "MAIL-WELCOME"},
	}
	for _, tc := range cases {
		if !pl.Exists(tc.name) {
			t.Errorf("Exists(%q) = false, want true", tc.name)
		}
		src, err := pl.Get(tc.name)
		if err != nil {
			t.Fatalf("Get(%q): %v", tc.name, err)
		}
		if src.Code() != tc.want {
			t.Errorf("Get(%q) = %q, want %q", tc.name, src.Code(), tc.want)
		}
		// The source keeps the fully-qualified name for diagnostics.
		if src.Name() != tc.name {
			t.Errorf("Get(%q).Name() = %q, want %q", tc.name, src.Name(), tc.name)
		}
	}

	if pl.Exists("other/x") {
		t.Error("unrouted prefix should not exist")
	}
	if _, err := pl.Get("other/x"); !IsNotFound(err) {
		t.Errorf("Get(other/x) err = %v, want not-found", err)
	}
	// A routed prefix whose delegate lacks the stripped name is not-found.
	if _, err := pl.Get("mail/missing"); !IsNotFound(err) {
		t.Errorf("Get(mail/missing) err = %v, want not-found", err)
	}
}

// TestPrefixLoaderCustomDelimiter checks a non-slash delimiter, including a
// route key already ending in the delimiter.
func TestPrefixLoaderCustomDelimiter(t *testing.T) {
	sub := NewArrayLoader(map[string]string{"page": "PAGE"})
	pl := NewPrefixLoaderDelim("::", map[string]Loader{"admin::": sub})
	src, err := pl.Get("admin::page")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if src.Code() != "PAGE" {
		t.Errorf("code = %q, want PAGE", src.Code())
	}
}

// TestFSLoaderEmbed checks loading from an embed.FS, both at the root of the
// embedded tree and scoped by an explicit root argument.
func TestFSLoaderEmbed(t *testing.T) {
	// Root at the embedded directory: names include the sub-path.
	l := NewFSLoader(embedFS)
	src, err := l.Get("testdata/embed/header.ql")
	if err != nil {
		t.Fatalf("Get header: %v", err)
	}
	if src.Code() != "HEADER" {
		t.Errorf("code = %q, want HEADER", src.Code())
	}
	if !l.Exists("testdata/embed/footer.ql") {
		t.Error("footer should exist")
	}
	if l.Exists("testdata/embed/missing.ql") {
		t.Error("missing should not exist")
	}
	if _, err := l.Get("testdata/embed/missing.ql"); !IsNotFound(err) {
		t.Errorf("err = %v, want not-found", err)
	}

	// Scoped root: names are relative to the root sub-tree.
	scoped := NewFSLoader(embedFS, "testdata/embed")
	src, err = scoped.Get("header.ql")
	if err != nil {
		t.Fatalf("scoped Get header: %v", err)
	}
	if src.Code() != "HEADER" {
		t.Errorf("scoped code = %q, want HEADER", src.Code())
	}
	src, err = scoped.Get("lang/de.ql")
	if err != nil {
		t.Fatalf("scoped Get lang/de: %v", err)
	}
	if src.Code() != "DE-HEADER" {
		t.Errorf("scoped code = %q, want DE-HEADER", src.Code())
	}
	if !scoped.Exists("footer.ql") {
		t.Error("scoped footer should exist")
	}
}

// TestFSLoaderEscape checks that a "../" name is confined to the root and that
// a directory name is not reported as an existing template.
func TestFSLoaderEscape(t *testing.T) {
	scoped := NewFSLoader(embedFS, "testdata/embed")
	// "../" segments collapse against the root, so the name is confined to the
	// embedded sub-tree and resolves as if the leading "../" were absent.
	src, err := scoped.Get("../header.ql")
	if err != nil {
		t.Fatalf("Get(../header.ql): %v", err)
	}
	if src.Code() != "HEADER" {
		t.Errorf("code = %q, want HEADER (confined to root)", src.Code())
	}
	// A directory is not a template.
	if scoped.Exists("lang") {
		t.Error("a directory should not be reported as an existing template")
	}
}

// TestFuncLoaderCallback checks the callback loader, including its not-found
// path and a nil callback.
func TestFuncLoaderCallback(t *testing.T) {
	store := map[string]string{"greeting.ql": "hi {{ name }}"}
	l := NewFuncLoader(func(name string) (string, bool) {
		src, ok := store[name]
		return src, ok
	})

	if !l.Exists("greeting.ql") {
		t.Error("greeting.ql should exist")
	}
	src, err := l.Get("greeting.ql")
	if err != nil {
		t.Fatal(err)
	}
	if src.Code() != "hi {{ name }}" {
		t.Errorf("code = %q", src.Code())
	}
	if src.Name() != "greeting.ql" {
		t.Errorf("name = %q", src.Name())
	}

	if l.Exists("nope.ql") {
		t.Error("nope.ql should not exist")
	}
	if _, err := l.Get("nope.ql"); !IsNotFound(err) {
		t.Errorf("err = %v, want not-found", err)
	}

	nilLoader := NewFuncLoader(nil)
	if nilLoader.Exists("anything") {
		t.Error("nil callback should report nothing exists")
	}
	if _, err := nilLoader.Get("anything"); !IsNotFound(err) {
		t.Errorf("nil callback err = %v, want not-found", err)
	}
}

// TestComposableSatisfyLoader is a compile-time assertion that every new loader
// satisfies the Loader interface.
func TestComposableSatisfyLoader(t *testing.T) {
	var _ Loader = (*ChainLoader)(nil)
	var _ Loader = (*PrefixLoader)(nil)
	var _ Loader = (*FSLoader)(nil)
	var _ Loader = (*FuncLoader)(nil)
}

// TestLoadersCompose checks the four loaders working together: a prefix router
// over an fs.FS and a callback, layered under a chain with an override.
func TestLoadersCompose(t *testing.T) {
	router := NewPrefixLoader(map[string]Loader{
		"embed": NewFSLoader(embedFS, "testdata/embed"),
		"func":  NewFuncLoader(func(name string) (string, bool) { return "FN:" + name, true }),
	})
	override := NewArrayLoader(map[string]string{"embed/header.ql": "OVERRIDDEN"})
	chain := NewChainLoader(override, router)

	// Override shadows the fs.FS entry.
	if src, err := chain.Get("embed/header.ql"); err != nil || src.Code() != "OVERRIDDEN" {
		t.Fatalf("embed/header.ql = %v / %q, want OVERRIDDEN", err, src.Code())
	}
	// Non-overridden name resolves through the router to the fs.FS.
	if src, err := chain.Get("embed/footer.ql"); err != nil || src.Code() != "FOOTER" {
		t.Fatalf("embed/footer.ql = %v / %q, want FOOTER", err, src.Code())
	}
	// The callback route answers any name.
	if src, err := chain.Get("func/whatever"); err != nil || src.Code() != "FN:whatever" {
		t.Fatalf("func/whatever = %v / %q, want FN:whatever", err, src.Code())
	}
}
