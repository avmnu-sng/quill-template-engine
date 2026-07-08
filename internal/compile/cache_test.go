package compile_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/internal/compile"
)

// cacheCase is one @cache parity contract: the template, the pinned single-render
// output, an autoescape flag, and the two data sets a cross-render probe renders
// with. want pins the FIRST render's bytes; the second render's bytes are not
// pinned here because they depend on the warm store, and the harness asserts the
// compiled second render matches the interpreter's second render byte for byte.
type cacheCase struct {
	name  string
	src   string
	want  string
	auto  bool
	vars  string
	vars2 string
}

// cacheBattery pins the compiled @cache region against the interpreter's
// execCache. Within one render it covers the miss-then-hit shape (a second
// region under the same key replays the first, so a body-local @set never runs
// twice), body scope isolation (the cached @set does not leak to the caller),
// tags and the no-op ttl, and an escape region around the body. Across two
// renders (the harness cacheCheck leg) it covers the store persistence that a
// single render cannot observe: the second render replays the first render's
// body even when its data differs, because the key hit ignores the fresh data.
var cacheBattery = []cacheCase{
	// The plan's constructed proof: two regions under one key. The first is a
	// miss that runs @set n=n+1 (n becomes 1) and stores; the second is a hit
	// that replays the stored body and does NOT re-run the @set, so both regions
	// read "header build #1" and n stays 0 outside (the body's @set is isolated
	// to the child scope). A handle-less always-miss would run the @set twice and
	// print "header build #2" for the second region.
	{"miss_then_hit_same_key",
		"@set n = 0\n@cache key=\"hdr\" {\n@set n = n + 1\nheader build #{{ n }}\n@}\n@cache key=\"hdr\" {\n@set n = n + 1\nheader build #{{ n }}\n@}\nafter n = {{ n }}\n",
		"header build #1\nheader build #1\nafter n = 0\n", false, "", ""},
	// The cached body reads a top-level variable. The first render stores the
	// body rendered with v="A"; the second render passes v="B" but hits the warm
	// store and replays "A", so the second render's bytes ignore its own data --
	// the decisive cross-render store probe.
	{"cross_render_hit_replays_first_body",
		"@cache key=\"k\" {\nvalue={{ v }}\n@}\n",
		"value=A\n", false, `{"v":"A"}`, `{"v":"B"}`},
	// A computed key coerces to text exactly like execCache's ToText; two
	// distinct keys are two independent store entries, so both regions render
	// fresh on the first render.
	{"computed_key_namespaces_independently",
		"@for i in [1, 2] {\n@cache key=(\"k\" ~ i) {\nbody {{ i }}\n@}\n@}\n",
		"body 1\nbody 2\n", false, "", ""},
	// The body renders in a child scope: a @set inside the cache body does not
	// leak to the caller, matching execCache's captureItems(ctx.Child()).
	{"body_set_does_not_leak",
		"@set x = 1\n@cache key=\"s\" {\n@set x = 99\nin={{ x }}\n@}\nout={{ x }}\n",
		"in=99\nout=1\n", false, "", ""},
	// tags and ttl parse and lower: ttl is a documented no-op, tags ride into
	// Put. The store gate is unaffected, so the second same-key region is a hit.
	{"tags_and_ttl_lower",
		"@cache key=\"t\" ttl=60 tags=[\"nav\", \"hdr\"] {\ncached\n@}\n@cache key=\"t\" {\ncached\n@}\n",
		"cached\ncached\n", false, "", ""},
	// An escape region around the body: the body's interpolation escapes once
	// under the active strategy while it fills the capture, and the spliced
	// output is written raw on both the miss and the hit, never escaped a second
	// time -- byte-exact to execCache's emitString.
	{"escape_region_body_not_double_escaped",
		"@escape html {\n@cache key=\"e\" {\n{{ raw }}\n@}\n@cache key=\"e\" {\n{{ raw }}\n@}\n@}\n",
		"a&lt;b\na&lt;b\n", false, `{"raw":"a<b"}`, `{"raw":"a<b"}`},
	// The COW corner: the cache body reads an outer array and writes a member.
	// The read crosses the child frame, so the array is share-marked; the
	// member-write must copy-on-write into a body-local copy, leaving the caller's
	// array untouched -- exactly execCache's captureItems(ctx.Child()) over the
	// interpreter's Own/SetMember primitives. The caller's post-region read must
	// show the original, and the cached body must show the mutated copy.
	{"body_member_write_is_copy_on_write",
		"@set xs = [1, 2, 3]\n@cache key=\"cow\" {\n@set xs[0] = 99\ninner={{ xs[0] }}\n@}\nouter={{ xs[0] }}\n",
		"inner=99\nouter=1\n", false, "", ""},
}

// cacheCaseVars returns a case's first-render data.
func cacheCaseVars(c cacheCase) string {
	if c.vars != "" {
		return c.vars
	}
	return ""
}

// cacheCaseVars2 returns a case's second-render data, defaulting to the first
// render's data when the case does not vary it.
func cacheCaseVars2(c cacheCase) string {
	if c.vars2 != "" {
		return c.vars2
	}
	return c.vars
}

// TestCacheBattery renders the @cache battery through the compiled path,
// asserting each first render byte-equal to the facade's Render AND to the
// pinned contract, and drives every case through the two-render cross-render
// store probe so the compiled path is shown to share the Environment's warm
// cache with the interpreter.
func TestCacheBattery(t *testing.T) {
	var cases []compiledCase
	results := map[string]*compile.Result{}
	for _, cc := range cacheBattery {
		cs := compiledCase{
			name:       cc.name,
			template:   cc.src,
			varsJSON:   cacheCaseVars(cc),
			varsJSON2:  cacheCaseVars2(cc),
			opts:       compile.Options{AutoescapeHTML: cc.auto},
			envCheck:   true,
			cacheCheck: true,
		}
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cc.name, err)
		}
		results[cc.name] = res
		cases = append(cases, cs)
	}
	got := runCompiled(t, cases, results)
	for i, cc := range cacheBattery {
		r, ok := got[cc.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", cc.name)
			continue
		}
		if r.failed {
			t.Errorf("%s: compiled render errored: %s", cc.name, r.errText)
			continue
		}
		want, err := renderInterp(t, cases[i])
		if err != nil {
			t.Errorf("%s: interp render errored: %v", cc.name, err)
			continue
		}
		if want != cc.want {
			t.Errorf("%s: interpreter drifted from the pinned contract\n got  %q\n want %q", cc.name, want, cc.want)
		}
		if r.out != want {
			t.Errorf("%s: compiled output differs from interpreter\n got  %q\n want %q", cc.name, r.out, want)
		}
		if envR, ok := got[cc.name+"@env"]; ok {
			if envR.failed {
				t.Errorf("%s: env-dispatch render errored: %s", cc.name, envR.errText)
			} else if envR.out != want {
				t.Errorf("%s: env-dispatch output differs from interpreter\n got  %q\n want %q", cc.name, envR.out, want)
			}
		}
		if tr, ok := got[cc.name+"@tracer"]; ok && tr.out != "served" {
			t.Errorf("%s: dispatch gate fell back for an @cache unit it should serve", cc.name)
		}
		if mx, ok := got[cc.name+"@matrix"]; ok && mx.out != "ok" {
			t.Errorf("%s: fingerprint-matrix leg reported %q", cc.name, mx.out)
		}
		if ch, ok := got[cc.name+"@cache"]; ok {
			if ch.failed {
				t.Errorf("%s: cross-render cache leg reported %q", cc.name, ch.errText)
			} else if ch.out != "ok" {
				t.Errorf("%s: cross-render cache leg reported %q", cc.name, ch.out)
			}
		} else {
			t.Errorf("%s: no cross-render cache result", cc.name)
		}
	}
}

// TestCacheSlotGatingRefusesStore pins the slot-touching gate through the
// compiled path across two renders: a region whose body touches the deferred
// slot machinery (a @yield inside, a @provide inside, or an included partial's
// @provide) must render FRESH on every render and never memoize, because a
// stored body would replay a render-unique yield placeholder no later resolve
// pass can substitute or silently lose a render-scoped @provide. The second
// render varies the data; because the region is never stored, its bytes track
// the fresh data on both engines -- the opposite of a stored region, which
// would replay the first render's bytes. Each case is compared byte-exactly
// against the interpreter across both renders.
func TestCacheSlotGatingRefusesStore(t *testing.T) {
	slotGated := []cacheCase{
		// A @yield inside the cache body: its placeholder reaches the finished
		// buffer and is backfilled by the single resolve pass, so the body is
		// never stored. The second render varies the provided content and the
		// region reflects it, proving no replay.
		{"yield_in_body_renders_fresh",
			"@cache key=\"y\" {\nhead:\n@yield syms\n@}\n@provide syms {\n{{ sym }}\n@}\ntail\n",
			"head:\nS1\ntail\n", false, `{"sym":"S1"}`, `{"sym":"S2"}`},
		// A @provide inside the cache body is a render-scoped side effect a replay
		// would lose, so the body is never stored and the fed @yield reflects the
		// current render's data on both renders.
		{"provide_in_body_renders_fresh",
			"@cache key=\"p\" {\nbody\n@provide syms {\n{{ sym }}\n@}\n@}\ngot:\n@yield syms\n",
			"body\ngot:\nP1\n", false, `{"sym":"P1"}`, `{"sym":"P2"}`},
	}

	var cases []compiledCase
	results := map[string]*compile.Result{}
	for _, cc := range slotGated {
		cs := compiledCase{
			name:       cc.name,
			template:   cc.src,
			varsJSON:   cacheCaseVars(cc),
			varsJSON2:  cacheCaseVars2(cc),
			opts:       compile.Options{AutoescapeHTML: cc.auto},
			envCheck:   true,
			cacheCheck: true,
		}
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cc.name, err)
		}
		results[cc.name] = res
		cases = append(cases, cs)
	}
	got := runCompiled(t, cases, results)
	for i, cc := range slotGated {
		r, ok := got[cc.name]
		if !ok || r.failed {
			t.Errorf("%s: compiled render failed or missing", cc.name)
			continue
		}
		want, err := renderInterp(t, cases[i])
		if err != nil {
			t.Errorf("%s: interp render errored: %v", cc.name, err)
			continue
		}
		if want != cc.want {
			t.Errorf("%s: interpreter drifted from the pinned contract\n got  %q\n want %q", cc.name, want, cc.want)
		}
		if r.out != want {
			t.Errorf("%s: compiled output differs from interpreter\n got  %q\n want %q", cc.name, r.out, want)
		}
		if strings.Contains(r.out, "\x00\x01QUILL_SLOT_") {
			t.Errorf("%s: a raw yield placeholder leaked into compiled output: %q", cc.name, r.out)
		}
		if ch, ok := got[cc.name+"@cache"]; ok && ch.out != "ok" {
			t.Errorf("%s: cross-render cache leg reported %q (a slot-touching region must render fresh, not replay)", cc.name, ch.errText+ch.out)
		}
	}
}

// cacheErrorCases pin the compiled @cache error paths against the interpreter's
// execCache: an unknown head argument raised at the argument's own position, a
// missing key raised at the region, and a non-textual key coerced through
// ToText raising the array-as-text error at the region. A body-render error
// inside the miss path also surfaces at the failing statement, proving the
// child-scope render positions its errors like the interpreter's captureItems.
var cacheErrorCases = []compiledCase{
	{name: "cache-unknown-arg", template: "@cache bogus=\"x\" key=\"k\" {\nbody\n@}\n"},
	{name: "cache-missing-key", template: "@cache ttl=60 {\nbody\n@}\n"},
	{name: "cache-array-key", template: "@cache key=[1, 2] {\nbody\n@}\n"},
	{name: "cache-body-error", template: "@cache key=\"k\" {\n{{ missing }}\n@}\n"},
	// A tag whose element is an array fails ToText on the store path. execCache
	// returns that error UNpositioned (evalCacheTags returns it raw and execCache
	// does not posErr it), so the compiled path must not wrap it with the region
	// location either -- the exact error-text corner where a stray qpos would add
	// a source suffix the interpreter's text omits.
	{name: "cache-tag-not-textual", template: "@cache key=\"k\" tags=[[1, 2]] {\nbody\n@}\n"},
}

// TestCacheErrorParity renders each malformed @cache template through the
// compiled path and asserts its error text and position match the interpreter's
// byte for byte. The unknown-argument and missing-key errors are decidable from
// the static head and lower as unconditional raises, so they must still carry
// the interpreter's exact wording and position.
func TestCacheErrorParity(t *testing.T) {
	results := map[string]*compile.Result{}
	for _, cs := range cacheErrorCases {
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cs.name, err)
		}
		results[cs.name] = res
	}
	got := runCompiled(t, cacheErrorCases, results)
	for _, cs := range cacheErrorCases {
		r, ok := got[cs.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", cs.name)
			continue
		}
		_, wantErr := renderInterp(t, cs)
		if wantErr == nil {
			t.Errorf("%s: interp did not error; the case must be malformed", cs.name)
			continue
		}
		if !r.failed {
			t.Errorf("%s: interp errored (%v) but compiled rendered %q", cs.name, wantErr, r.out)
			continue
		}
		if r.errText != wantErr.Error() {
			t.Errorf("%s: error text mismatch\n got  %q\n want %q", cs.name, r.errText, wantErr.Error())
		}
	}
}

// TestCacheIncludeNamespacesUnderPartial pins the key-namespacing corner through
// an inlined @include: a @cache inside an included partial keys under the
// PARTIAL's template name (root.Name + NUL + key), not the entry's, exactly as
// the interpreter's sub-interp keys under its own root. The compiled path inlines
// the partial's statements into the entry render, so the namespace must come from
// the defining member's source, not the entry. The two-render probe confirms the
// store persists under the partial-namespaced key.
func TestCacheIncludeNamespacesUnderPartial(t *testing.T) {
	cs := compiledCase{
		name:  "cache_in_include",
		entry: "main.ql",
		templates: map[string]string{
			"main.ql": "before\n@include \"part.ql\"\nafter\n",
			"part.ql": "@cache key=\"pk\" {\npartial value={{ v }}\n@}\n",
		},
		varsJSON:   `{"v":"A"}`,
		varsJSON2:  `{"v":"B"}`,
		viaModule:  true,
		envCheck:   true,
		cacheCheck: true,
	}
	res, err := compileCase(t, cs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got := runCompiled(t, []compiledCase{cs}, map[string]*compile.Result{cs.name: res})
	r, ok := got[cs.name]
	if !ok || r.failed {
		t.Fatalf("compiled render failed or missing: %+v", r)
	}
	want, err := renderInterp(t, cs)
	if err != nil {
		t.Fatalf("interp render errored: %v", err)
	}
	if r.out != want {
		t.Errorf("compiled output differs from interpreter\n got  %q\n want %q", r.out, want)
	}
	if ch, ok := got[cs.name+"@cache"]; ok {
		if ch.out != "ok" {
			t.Errorf("cross-render cache leg reported %q", ch.errText+ch.out)
		}
	} else {
		t.Error("no cross-render cache result")
	}
}

// TestCacheSlotGatingThroughInclude pins the slot-gating rule through an inlined
// @include nested in the cache body: the @provide runs inside the included
// partial, appending to the SAME render-level slot buffers, so the region's slot
// stamp grows and the body must never be memoized -- the compiled analog of
// execCache's slotStamp catching provides from nested includes. The @yield sits
// outside the region and reflects the current render's data on both renders,
// proving no replay of the first render's provided content. This is the corner a
// stamp that only watched this template's own statements would miss.
func TestCacheSlotGatingThroughInclude(t *testing.T) {
	cs := compiledCase{
		name:  "cache_include_provide",
		entry: "main.ql",
		templates: map[string]string{
			"main.ql": "@cache key=\"deep\" {\n@include \"part.ql\"\n@}\ngot:\n@yield syms\n",
			"part.ql": "partbody\n@provide syms {\n{{ sym }}\n@}\n",
		},
		varsJSON:   `{"sym":"S1"}`,
		varsJSON2:  `{"sym":"S2"}`,
		viaModule:  true,
		envCheck:   true,
		cacheCheck: true,
	}
	res, err := compileCase(t, cs)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got := runCompiled(t, []compiledCase{cs}, map[string]*compile.Result{cs.name: res})
	r, ok := got[cs.name]
	if !ok || r.failed {
		t.Fatalf("compiled render failed or missing: %+v", r)
	}
	want, err := renderInterp(t, cs)
	if err != nil {
		t.Fatalf("interp render errored: %v", err)
	}
	if r.out != want {
		t.Errorf("compiled output differs from interpreter\n got  %q\n want %q", r.out, want)
	}
	if strings.Contains(r.out, "\x00\x01QUILL_SLOT_") {
		t.Errorf("a raw yield placeholder leaked into compiled output: %q", r.out)
	}
	if ch, ok := got[cs.name+"@cache"]; ok {
		if ch.out != "ok" {
			t.Errorf("cross-render cache leg reported %q (an include-provide region must render fresh)", ch.errText+ch.out)
		}
	} else {
		t.Error("no cross-render cache result")
	}
}

// TestCacheKeyNamespacesUnderRenderRoot pins that a @cache reached through an
// inherited @block keys under the RENDER ROOT (the entry template that started
// the render), matching execCache's in.root.Name, not the block-defining base.
// Two children of one base each carry the same @cache block under one user key;
// on a shared Environment their store entries must namespace under their own
// entry names, so the second child renders its own data fresh rather than
// replaying the first child's stored body. Keying by the error-position source
// (the base that DEFINES the block) collides both children under the base name
// and serves the first child's bytes for the second -- a wrong-bytes-served
// outcome the interpreter never produces, since it leaves in.root pinned to the
// entry while inlining a parent block. The two-manifest shared-Environment leg
// drives this end to end through the generated code, and the generated-source
// assertion pins the key line to the entry source variable directly.
func TestCacheKeyNamespacesUnderRenderRoot(t *testing.T) {
	tmpls := map[string]string{
		"base.ql":   "@block b {\n@cache key=\"k\" {\ncached v={{ v }}\n@}\n@}\n",
		"child1.ql": "@extends \"base.ql\"\n",
		"child2.ql": "@extends \"base.ql\"\n",
	}
	child1 := compiledCase{
		name:           "cache_root_child1",
		entry:          "child1.ql",
		templates:      tmpls,
		varsJSON:       `{"v":"AAA"}`,
		sharedRootPeer: "cache_root_child2",
		sharedPeerVars: `{"v":"BBB"}`,
	}
	child2 := compiledCase{
		name:      "cache_root_child2",
		entry:     "child2.ql",
		templates: tmpls,
		varsJSON:  `{"v":"BBB"}`,
	}
	res1, err := compileCase(t, child1)
	if err != nil {
		t.Fatalf("compile child1: %v", err)
	}
	res2, err := compileCase(t, child2)
	if err != nil {
		t.Fatalf("compile child2: %v", err)
	}

	// The generated key must namespace under the entry source (child1.ql), not the
	// block-defining base (base.ql). The base is declared as a later qSrc variable
	// because it is registered second, so a key line built from that variable is
	// the defect. Assert the entry-source variable feeds the key and the base's
	// does not.
	src1 := string(res1.Source)
	entryVar := srcVarFor(t, src1, "child1.ql")
	baseVar := srcVarFor(t, src1, "base.ql")
	keyLine := cacheKeyLine(t, src1)
	if !strings.Contains(keyLine, entryVar+".Name()") {
		t.Errorf("cache key line does not namespace under the entry source %s: %q", entryVar, keyLine)
	}
	if strings.Contains(keyLine, baseVar+".Name()") {
		t.Errorf("cache key line namespaces under the block-defining base %s (should be the render root): %q", baseVar, keyLine)
	}

	cases := []compiledCase{child1, child2}
	results := map[string]*compile.Result{child1.name: res1, child2.name: res2}
	got := runCompiled(t, cases, results)
	if sr, ok := got[child1.name+"@sharedroot"]; ok {
		if sr.out != "ok" {
			t.Errorf("shared-root cache leg reported %q (two @extends children must key under their own entry, not the shared base)", sr.errText+sr.out)
		}
	} else {
		t.Error("no shared-root cache result")
	}
}

// srcVarFor returns the generated qSrc variable a template name is bound to in a
// generated source, e.g. `qSrc` or `qSrc2`. It reads the `qSrcN = source.New(
// "name", ...)` declarations the generated file's var block emits.
func srcVarFor(t *testing.T, gen, name string) string {
	t.Helper()
	needle := "source.New(" + strconv.Quote(name) + ","
	idx := strings.Index(gen, needle)
	if idx < 0 {
		t.Fatalf("generated source has no source.New for %q", name)
	}
	line := gen[strings.LastIndex(gen[:idx], "\n")+1 : idx]
	fields := strings.Fields(line)
	if len(fields) == 0 {
		t.Fatalf("cannot parse source var from line %q", line)
	}
	return fields[0]
}

// cacheKeyLine returns the generated line that builds the namespaced @cache key,
// the `qcfN := <src>.Name() + "\x00" + qckN` assignment stmtCache emits.
func cacheKeyLine(t *testing.T, gen string) string {
	t.Helper()
	for _, line := range strings.Split(gen, "\n") {
		if strings.Contains(line, ".Name() + ") && strings.Contains(line, `"\x00"`) {
			return strings.TrimSpace(line)
		}
	}
	t.Fatal("generated source has no @cache key line")
	return ""
}
