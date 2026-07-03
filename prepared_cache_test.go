package quill

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/interp"
	"github.com/avmnu-sng/quill-template-engine/loader"
	"github.com/avmnu-sng/quill-template-engine/parse"
	"github.com/avmnu-sng/quill-template-engine/runtime"
	"github.com/avmnu-sng/quill-template-engine/source"
)

// preparedMemoFixtures is a template set exercising every composition family
// the prepared-template memo serves at render time: inheritance with parent()
// (@extends), context-passing includes, namespace and selective macro imports
// (@import/@from), traits with aliasing (@use), and deferred slots resolved
// across included partials (@provide/@yield). Every one of these constructs
// re-loads its target through Environment.LoadTemplate mid-render, so a memo
// bug that leaked mutable state or served a stale Template would surface here
// as a byte diff between repeat renders on one Environment.
func preparedMemoFixtures() map[string]string {
	return map[string]string{
		"base.ql": "=== {{ title }} ===\n@block intro {\ndefault intro\n@}\n---\n" +
			"@block body {\ndefault body\n@}\n===\n",
		"page.ql": "@extends \"base.ql\"\n@block intro {\n{{ parent() }}\nplus child intro\n@}\n" +
			"@block body {\n@for item in items {\n- {{ item }}\n@}\n@}\n",
		"row.ql": "  row: {{ label }} = {{ value }}\n",
		"table.ql": "table:\n@for r in rows {\n@include \"row.ql\" with { label: r.label, value: r.value }\n@}\n" +
			"done\n",
		"forms.ql": "@macro field(name, label = null) {\n{{ label ?? name }}: <{{ name }}>\n@}\n" +
			"@macro list(...items) {\n{{ items | join(\" | \") }}\n@}\n",
		"macros.ql": "@from \"forms.ql\" import field\n@import \"forms.ql\" as forms\n" +
			"{{ field(\"email\") }}\n{{ field(\"pw\", \"Password\") }}\n{{ forms.list(1, 2, 3) }}\n",
		"buttons.ql": "@block submit {\n[submit]\n@}\n@block cancel {\n[cancel]\n@}\n",
		"traits.ql": "@use \"buttons.ql\" with { cancel: dismiss }\nown header\n" +
			"@block submit {\n{{ parent() }}\nown submit wins\n@}\n{{ block(\"dismiss\") }}\n",
		"part-a.ql": "@provide imports {\nimport \"os\"\n@}\nA rendered.\n",
		"part-b.ql": "@provide imports {\nimport \"fmt\"\n@}\nB rendered.\n",
		"shell.ql":  "imports:\n@yield imports\nbody:\n@include \"part-a.ql\"\n@include \"part-b.ql\"\n",
	}
}

// preparedMemoEntries lists the render entry points of preparedMemoFixtures,
// one per composition family.
func preparedMemoEntries() []string {
	return []string{"page.ql", "table.ql", "macros.ql", "traits.ql", "shell.ql"}
}

// preparedMemoVars builds one binding map serving every entry template; strict
// variables only rejects reads of missing names, so unused bindings are inert.
func preparedMemoVars() map[string]runtime.Value {
	items := runtime.NewList(runtime.Str("alpha"), runtime.Str("beta"))
	row := func(label, value string) runtime.Value {
		r := runtime.NewArray()
		r.SetStr("label", runtime.Str(label))
		r.SetStr("value", runtime.Str(value))
		return runtime.Arr(r)
	}
	rows := runtime.NewList(row("a", "1"), row("b", "2"))
	return map[string]runtime.Value{
		"title": runtime.Str("Report"),
		"items": runtime.Arr(items),
		"rows":  runtime.Arr(rows),
	}
}

// TestLoadTemplateReturnsStableTemplatePointer pins the documented identity
// contract: on one Environment, LoadTemplate of an unchanged template returns
// the SAME *interp.Template pointer across calls, because the prepared memo
// serves the Template built at first load instead of re-preparing.
func TestLoadTemplateReturnsStableTemplatePointer(t *testing.T) {
	env := New(loader.NewArrayLoader(preparedMemoFixtures()))
	for _, name := range preparedMemoEntries() {
		first, err := env.LoadTemplate(name)
		if err != nil {
			t.Fatalf("LoadTemplate(%q) first: %v", name, err)
		}
		second, err := env.LoadTemplate(name)
		if err != nil {
			t.Fatalf("LoadTemplate(%q) second: %v", name, err)
		}
		if first != second {
			t.Errorf("LoadTemplate(%q) returned distinct Templates across warm calls", name)
		}
	}
}

// TestLoadTemplateRepreparesWhenParseCacheServesNewModule pins the memo's
// coherence guard: a prepared entry is honored only while the parse cache
// still serves the exact module it was built from. Replacing the cached
// module (the shape any future parse-cache eviction produces) must yield a
// freshly prepared Template over the new module, never the stale memo, and
// the rendered bytes must not change for identical source.
func TestLoadTemplateRepreparesWhenParseCacheServesNewModule(t *testing.T) {
	fixtures := preparedMemoFixtures()
	env := New(loader.NewArrayLoader(fixtures))
	before, err := env.Render("page.ql", preparedMemoVars())
	if err != nil {
		t.Fatalf("render before module swap: %v", err)
	}
	stale, err := env.LoadTemplate("page.ql")
	if err != nil {
		t.Fatalf("LoadTemplate before module swap: %v", err)
	}
	mod, err := parse.Parse(source.New("page.ql", fixtures["page.ql"]))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	env.cache.Put("page.ql", mod)
	fresh, err := env.LoadTemplate("page.ql")
	if err != nil {
		t.Fatalf("LoadTemplate after module swap: %v", err)
	}
	if fresh == stale {
		t.Fatal("LoadTemplate served the stale Template after the parse cache module changed")
	}
	if fresh.Module != mod {
		t.Fatal("re-prepared Template was not built from the parse cache's new module")
	}
	after, err := env.Render("page.ql", preparedMemoVars())
	if err != nil {
		t.Fatalf("render after module swap: %v", err)
	}
	if after != before {
		t.Errorf("render changed across module swap:\nbefore: %q\nafter:  %q", before, after)
	}
}

// TestRepeatRenderStabilityOnOneEnvironment renders every composition-family
// entry 1,000 times on one Environment and requires byte-identical output each
// time. With the prepared memo every render after the first walks the SAME
// Template pointers, so any mutable state on a Template (or on anything it
// reaches) would accumulate across iterations and diverge the bytes.
func TestRepeatRenderStabilityOnOneEnvironment(t *testing.T) {
	env := New(loader.NewArrayLoader(preparedMemoFixtures()))
	vars := preparedMemoVars()
	for _, name := range preparedMemoEntries() {
		first, err := env.Render(name, vars)
		if err != nil {
			t.Fatalf("render %q: %v", name, err)
		}
		for i := 1; i < 1000; i++ {
			out, err := env.Render(name, vars)
			if err != nil {
				t.Fatalf("render %q iteration %d: %v", name, i, err)
			}
			if out != first {
				t.Fatalf("render %q iteration %d diverged:\nfirst: %q\ngot:   %q", name, i, first, out)
			}
		}
	}
}

// TestConcurrentRendersShareOneEnvironment renders all composition-family
// entries from many goroutines against one Environment, so concurrent
// LoadTemplate calls race on the prepared memo (including the benign
// duplicate-prepare window) while renders consume shared Templates. Run under
// the race detector this is the memo's concurrency proof; the byte comparison
// additionally proves no cross-render state leaks through the shared pointers.
// Each goroutine builds its own bindings because a *Array value is a per-render
// COW cell: Scope share-marks it during the render, so bindings -- unlike the
// Environment and its Templates, the shared state under test -- are not a
// cross-goroutine surface.
func TestConcurrentRendersShareOneEnvironment(t *testing.T) {
	env := New(loader.NewArrayLoader(preparedMemoFixtures()))
	entries := preparedMemoEntries()
	want := make(map[string]string, len(entries))
	for _, name := range entries {
		out, err := env.Render(name, preparedMemoVars())
		if err != nil {
			t.Fatalf("seed render %q: %v", name, err)
		}
		want[name] = out
	}
	const goroutines = 8
	const iterations = 200
	errCh := make(chan error, goroutines)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			vars := preparedMemoVars()
			for i := 0; i < iterations; i++ {
				name := entries[(g+i)%len(entries)]
				out, err := env.Render(name, vars)
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d render %q: %w", g, name, err)
					return
				}
				if out != want[name] {
					errCh <- fmt.Errorf("goroutine %d render %q diverged", g, name)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// TestTemplateExistsAndRawSourceUnaffectedByMemo pins that the prepared memo
// changes nothing about the other Engine lookups: TemplateExists still answers
// from the parse cache plus the loader, and RawSource still returns the
// loader's unparsed text, before and after warm loads populate the memo.
func TestTemplateExistsAndRawSourceUnaffectedByMemo(t *testing.T) {
	fixtures := preparedMemoFixtures()
	env := New(loader.NewArrayLoader(fixtures))
	check := func(stage string) {
		if !env.TemplateExists("page.ql") {
			t.Errorf("%s: TemplateExists(page.ql) = false", stage)
		}
		if env.TemplateExists("missing.ql") {
			t.Errorf("%s: TemplateExists(missing.ql) = true", stage)
		}
		src, ok := env.RawSource("page.ql")
		if !ok || src != fixtures["page.ql"] {
			t.Errorf("%s: RawSource(page.ql) = %q, %v; want fixture source, true", stage, src, ok)
		}
		if _, ok := env.RawSource("missing.ql"); ok {
			t.Errorf("%s: RawSource(missing.ql) reported ok", stage)
		}
	}
	check("cold")
	for i := 0; i < 2; i++ {
		if _, err := env.LoadTemplate("page.ql"); err != nil {
			t.Fatalf("warm load %d: %v", i, err)
		}
	}
	check("warm")
}

// TestLoadTemplatePrepareErrorIsNotMemoized pins the memo's error path: a
// template whose prepare fails (an invalid literal RE2 pattern) is never
// stored, so every LoadTemplate re-derives and reports the identical error
// the unmemoized path produced.
func TestLoadTemplatePrepareErrorIsNotMemoized(t *testing.T) {
	env := New(loader.NewArrayLoader(map[string]string{
		"bad.ql": "{{ \"x\" matches \"(\" ? \"y\" : \"n\" }}\n",
	}))
	_, first := env.LoadTemplate("bad.ql")
	if first == nil {
		t.Fatal("LoadTemplate of an invalid literal regex succeeded")
	}
	_, second := env.LoadTemplate("bad.ql")
	if second == nil {
		t.Fatal("second LoadTemplate of an invalid literal regex succeeded")
	}
	if first.Error() != second.Error() {
		t.Errorf("prepare error changed across loads:\nfirst:  %q\nsecond: %q", first, second)
	}
}

// TestTemplateFieldsClassifiedImmutablePostPrepare is the reflection canary
// guarding the sharing contract the prepared memo relies on: every field of
// interp.Template must be immutable once PrepareChecked returns, because one
// Template pointer is now shared across all renders (and goroutines) on an
// Environment. Adding a field to Template makes this test fail until the
// field is classified here -- either immutable-post-Prepare, or explicitly
// exempted with a rationale proving per-render safety (a sanctioned atomic,
// for example), which forces the sharing question to be answered at the
// moment the field is born.
func TestTemplateFieldsClassifiedImmutablePostPrepare(t *testing.T) {
	const immutable = "immutable-post-Prepare"
	classified := map[string]string{
		"Name":          immutable,
		"Module":        immutable,
		"blocks":        immutable,
		"blockOrder":    immutable,
		"macros":        immutable,
		"macroOrder":    immutable,
		"extendsNode":   immutable,
		"imports":       immutable,
		"uses":          immutable,
		"regexps":       immutable,
		"used":          immutable,
		"usesSlots":     immutable,
		"staticRefs":    immutable,
		"hasDynamicRef": immutable,
		// lastOut is the one sanctioned mutable Template field: an atomic
		// output-length hint renderBuffered reads to pre-size its builder and
		// stores after each successful render. It can only influence buffer
		// capacity, never rendered bytes, so cross-render races reduce to
		// benign last-write-wins on a sizing heuristic.
		"lastOut": "exempt-atomic-output-size-hint",
	}
	// Template contains an atomic (a noCopy type), so the reflection subject
	// must be reached through a pointer rather than a by-value literal.
	tt := reflect.TypeOf((*interp.Template)(nil)).Elem()
	seen := map[string]bool{}
	for i := 0; i < tt.NumField(); i++ {
		name := tt.Field(i).Name
		seen[name] = true
		if _, ok := classified[name]; !ok {
			t.Errorf("interp.Template field %q is not classified; declare it %s or exempt it with a rationale", name, immutable)
		}
	}
	for name := range classified {
		if !seen[name] {
			t.Errorf("classification lists %q but interp.Template has no such field; remove the stale entry", name)
		}
	}
}
