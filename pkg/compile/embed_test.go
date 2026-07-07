package compile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/compile"
)

// embedBattery pins the compiled static-@embed contracts: the embed target is
// linked as an anonymous derived member (its @extends chain, @use traits, and
// merged block table) and the inline @block overrides layer over that table
// most-derived first, exactly as interp execEmbed builds a sub-interp block
// table and prepends the override chain. The flattened body then splices into
// the SAME render, so a @provide inside it appends to the render-level slot
// buffers and a @yield reaches the single post-render resolve pass -- the
// compiled analog of execEmbed's shareSlotsFrom.
//
// The shapes cover the conformance fixture (an override plus a self-contained
// slot block in the embedded template) and the plan's parity corners: an
// override winning over the target's own block; parent() inside an override
// rendering the target's own definition down the prepended chain; block(name)
// resolving through the embed-local table; the with-map binding evaluated in
// the caller scope; only cutting to a fresh scope root; ignore missing on an
// absent target contributing nothing; an embed whose @provide feeds a CALLER
// @yield (the yield-into-parent direction) and one whose @yield is fed by a
// CALLER @provide (the reverse direction); a self-contained embed that both
// @yields and @provides its own label; an embed target that itself @extends a
// base and @use-s a trait (the chain/trait flattening path through linkUnit);
// and escape-strategy inheritance, where an interpolation inside the embedded
// body escapes under the caller's active strategy. Every case runs strict
// through both the Unit and single-template Module lowerings, and the whole
// battery asserts no raw NUL-wrapped yield placeholder survives in any compiled
// output.
var embedBattery = []includeCase{
	{
		name:  "override_and_self_contained_slot",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "page top.\n@embed \"shell.ql\" {\n@block title {\nOverridden Title\n@}\n@}\npage bottom.\n",
			"shell.ql": "@block title {\nDefault Title\n@}\ntags:\n@yield tags\n@provide tags {\nalpha\n@}\n@provide tags {\nbeta\n@}\n",
		},
		want: "page top.\nOverridden Title\ntags:\nalpha\nbeta\npage bottom.\n",
	},
	{
		name:  "override_wins_no_slots",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "@embed \"shell.ql\" {\n@block body {\nfrom embed\n@}\n@}\n",
			"shell.ql": "head\n@block body {\nfrom shell\n@}\nfoot\n",
		},
		want: "head\nfrom embed\nfoot\n",
	},
	{
		name:  "parent_in_override",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "@embed \"shell.ql\" {\n@block body {\n{{ parent() }}and more\n@}\n@}\n",
			"shell.ql": "@block body {\nbase body\n@}\n",
		},
		want: "base body\nand more\n",
	},
	{
		name:  "block_call_in_embed",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "@embed \"shell.ql\" {\n@block a {\nA override\n@}\n@}\n",
			"shell.ql": "@block a {\nA base\n@}\ncall: {{ block(\"a\") }}\n",
		},
		want: "A override\ncall: A override\n\n",
	},
	{
		name:  "with_map",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "@embed \"shell.ql\" with { title: \"Passed\" } {\n@}\n",
			"shell.ql": "title is {{ title }}\n",
		},
		want: "title is Passed\n",
	},
	{
		name:  "only_cuts_scope",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "@set outer = \"caller\"\n@embed \"shell.ql\" with { inner: \"passed\" } only {\n@}\n",
			"shell.ql": "inner={{ inner }} outer={{ outer }}\n",
		},
		vars:    ``,
		want:    "inner=passed outer=\n",
		lenient: true,
	},
	{
		name:  "plain_inherits_caller",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "@set shared = \"seen\"\n@embed \"shell.ql\" {\n@}\n",
			"shell.ql": "shared={{ shared }}\n",
		},
		want: "shared=seen\n",
	},
	{
		name:  "ignore_missing_absent",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql": "before\n@embed \"gone.ql\" ignore missing {\n@}\nafter\n",
		},
		want: "before\nafter\n",
	},
	{
		name:  "provide_feeds_caller_yield",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "@yield sidebar\n@embed \"shell.ql\" {\n@}\n",
			"shell.ql": "@provide sidebar {\nfrom embed\n@}\nshell body\n",
		},
		want: "from embed\nshell body\n",
	},
	{
		name:  "caller_provides_embed_yields",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "@provide msg {\nfrom caller\n@}\n@embed \"shell.ql\" {\n@}\n",
			"shell.ql": "note:\n@yield msg\n",
		},
		want: "note:\nfrom caller\n",
	},
	{
		name:  "target_extends_base",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "@embed \"child.ql\" {\n@block title {\nembed title\n@}\n@}\n",
			"child.ql": "@extends \"base.ql\"\n@block body {\nchild body\n@}\n",
			"base.ql":  "@block title {\nbase title\n@}\n@block body {\nbase body\n@}\ndone\n",
		},
		want: "embed title\nchild body\ndone\n",
	},
	{
		name:  "target_uses_trait",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "@embed \"shell.ql\" {\n@block header {\nembed header\n@}\n@}\n",
			"shell.ql": "@use \"trait.ql\"\ntop\n@block header {\nshell header\n@}\n@block footer {\nshell footer\n@}\nbottom\n",
			"trait.ql": "@block header {\ntrait header\n@}\n",
		},
		want: "top\nembed header\nshell footer\nbottom\n",
	},
	{
		name:  "embed_in_tab_region",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "root\n@tab(1) {\nbefore\n@embed \"shell.ql\" {\n@}\nafter\n@}\ntail\n",
			"shell.ql": "shell line one\nshell line two\n",
		},
		want: "root\n    before\nshell line one\nshell line two\n    after\ntail\n",
	},
	{
		name:  "embed_with_override_in_tab",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "@tab(2) {\nhead\n@embed \"shell.ql\" {\n@block b {\nembed body\n@}\n@}\nfoot\n@}\n",
			"shell.ql": "shell top\n@block b {\ndefault\n@}\nshell end\n",
		},
		want: "        head\nshell top\nembed body\nshell end\n        foot\n",
	},
	{
		name:  "nested_embed",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql": "@embed \"a.ql\" {\n@}\n",
			"a.ql":    "A start\n@embed \"b.ql\" {\n@}\nA end\n",
			"b.ql":    "B body\n",
		},
		want: "A start\nB body\nA end\n",
	},
	{
		name:  "embed_no_trailing_newline_in_tab",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql": "@tab(1) {\n@embed \"s.ql\" {\n@}\ntail\n@}\n",
			"s.ql":    "no newline end",
		},
		want: "no newline end    tail\n",
	},
	{
		name:  "embedded_tab_starts_fresh",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql": "@tab(2) {\n@embed \"n.ql\" {\n@}\n@}\n",
			"n.ql":    "@tab(1) {\ninner tabbed\n@}\nafter inner\n",
		},
		want: "    inner tabbed\nafter inner\n",
	},
	{
		name:  "cow_privatized_isolated",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql": "@set x = [1,2]\n@set x[0] = 9\n@embed \"s.ql\" {\n@}\nparent: {{ x[0] }},{{ x[1] }}\n",
			"s.ql":    "@set x[1] = 77\nembed: {{ x[0] }},{{ x[1] }}\n",
		},
		want: "embed: 9,77\nparent: 9,2\n",
	},
	{
		name:  "cow_only_cuts_scope",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql": "@set x = [1,2]\n@embed \"s.ql\" with { y: x } only {\n@}\nparent: {{ x[0] }},{{ x[1] }}\n",
			"s.ql":    "@set y[0] = 42\nembed: {{ y[0] }},{{ y[1] }}\n",
		},
		want: "embed: 42,2\nparent: 1,2\n",
	},
	{
		name:  "cow_late_alias_clean",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql": "@set a = [1,2]\n@set a[0] = 5\n@embed \"s.ql\" {\n@}\n@set c = a\n@set a[0] = 99\nfinal: {{ a[0] }},{{ c[0] }}\n",
			"s.ql":    "@set a[1] = 8\n",
		},
		want: "final: 99,5\n",
	},
	{
		name:  "escape_inherit",
		entry: "page.ql",
		tmpls: map[string]string{
			"page.ql":  "@embed \"shell.ql\" with { raw: value } {\n@}\n",
			"shell.ql": "{{ raw }}\n",
		},
		vars: `{"value":"a<b&c"}`,
		want: "a&lt;b&amp;c\n",
		auto: true,
	},
}

// TestEmbedBattery renders the embed battery through both compiled lowerings
// (Unit and single-template Module), asserting each output byte-equal to the
// facade's Render AND to the pinned contract, that the dispatch gate served the
// compiled unit under the fixture's configuration, and that no raw yield
// placeholder survives -- the leak class an embed that fed a slot would open if
// the render failed to buffer and resolve.
func TestEmbedBattery(t *testing.T) {
	var cases []compiledCase
	results := map[string]*compile.Result{}
	for _, ec := range embedBattery {
		for _, viaModule := range []bool{false, true} {
			suffix := "-unit"
			if viaModule {
				suffix = "-module"
			}
			cs := compiledCase{
				name:      ec.name + suffix,
				templates: ec.tmpls,
				entry:     ec.entry,
				varsJSON:  ec.vars,
				opts:      compile.Options{AutoescapeHTML: ec.auto, LenientVariables: ec.lenient},
				envCheck:  true,
				viaModule: viaModule,
			}
			res, err := compileCase(t, cs)
			if err != nil {
				t.Fatalf("%s: compile: %v", cs.name, err)
			}
			results[cs.name] = res
			cases = append(cases, cs)
		}
	}
	got := runCompiled(t, cases, results)
	for _, cs := range cases {
		base := embedCaseFor(cs.name)
		r, ok := got[cs.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", cs.name)
			continue
		}
		if r.failed {
			t.Errorf("%s: compiled render errored: %s", cs.name, r.errText)
			continue
		}
		want, err := renderInterp(t, cs)
		if err != nil {
			t.Errorf("%s: interp render errored: %v", cs.name, err)
			continue
		}
		if want != base.want {
			t.Errorf("%s: interpreter drifted from the pinned contract\n got  %q\n want %q", cs.name, want, base.want)
		}
		if r.out != want {
			t.Errorf("%s: compiled output differs from interpreter\n got  %q\n want %q", cs.name, r.out, want)
		}
		if strings.Contains(r.out, "\x00\x01QUILL_SLOT_") {
			t.Errorf("%s: a raw yield placeholder leaked into compiled output: %q", cs.name, r.out)
		}
		if envR, ok := got[cs.name+"@env"]; ok {
			if envR.failed {
				t.Errorf("%s: env-dispatch render errored: %s", cs.name, envR.errText)
			} else if envR.out != want {
				t.Errorf("%s: env-dispatch output differs from interpreter\n got  %q\n want %q", cs.name, envR.out, want)
			}
		}
		if tr, ok := got[cs.name+"@tracer"]; ok && tr.out != "served" {
			t.Errorf("%s: dispatch gate fell back for an embed unit it should serve", cs.name)
		}
		if mx, ok := got[cs.name+"@matrix"]; ok && mx.out != "ok" {
			t.Errorf("%s: fingerprint-matrix leg reported %q", cs.name, mx.out)
		}
	}
}

// TestEmbedErrorPositionParity pins that an error raised inside an inline
// override body cites the embed SITE's template and line, while an error in the
// embedded template's own body cites that template, exactly as the interpreter
// positions through each node's own source. An override authored in page.ql
// that reads a missing strict variable must report page.ql's line; the embedded
// shell.ql's own strict read must report shell.ql's line.
func TestEmbedErrorPositionParity(t *testing.T) {
	cases := []struct {
		name    string
		entry   string
		tmpls   map[string]string
		wantErr string
	}{
		{
			name:  "override_body_cites_site",
			entry: "page.ql",
			tmpls: map[string]string{
				"page.ql":  "@embed \"shell.ql\" {\n@block body {\n{{ missing }}\n@}\n@}\n",
				"shell.ql": "@block body {\ndefault\n@}\n",
			},
			wantErr: "quill undefined error: undefined variable \"missing\" (available: (none)) (page.ql:3)",
		},
		{
			name:  "embedded_body_cites_target",
			entry: "page.ql",
			tmpls: map[string]string{
				"page.ql":  "top\n@embed \"shell.ql\" {\n@}\n",
				"shell.ql": "a\n{{ missing }}\n",
			},
			wantErr: "quill undefined error: undefined variable \"missing\" (available: (none)) (shell.ql:2)",
		},
	}
	var compiled []compiledCase
	results := map[string]*compile.Result{}
	for _, tc := range cases {
		cs := compiledCase{
			name:      tc.name,
			templates: tc.tmpls,
			entry:     tc.entry,
			opts:      compile.Options{},
		}
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", tc.name, err)
		}
		results[cs.name] = res
		compiled = append(compiled, cs)
	}
	got := runCompiled(t, compiled, results)
	for i, cs := range compiled {
		wantErr := cases[i].wantErr
		want, werr := renderInterp(t, cs)
		if werr == nil {
			t.Errorf("%s: interpreter rendered %q, want an error", cs.name, want)
			continue
		}
		if werr.Error() != wantErr {
			t.Errorf("%s: interpreter error drifted from the pinned contract\n got  %q\n want %q", cs.name, werr.Error(), wantErr)
		}
		r := got[cs.name]
		if !r.failed {
			t.Errorf("%s: compiled rendered %q but interp errored %q", cs.name, r.out, werr.Error())
			continue
		}
		if r.errText != werr.Error() {
			t.Errorf("%s: compiled error differs from interpreter\n got  %q\n want %q", cs.name, r.errText, werr.Error())
		}
	}
}

// TestEmbedNotCompilable pins the shapes the @embed lowering refuses, deferring
// each to the interpreter through the typed subset error: a dynamic (non-literal)
// source, a target absent from the compile set, a self-embed cycle, and a target
// whose composition sits outside the flattenable subset (a target declaring a
// macro, which the flattening cannot reproduce). A target that merely uses slots
// or block overrides is NOT here: it flattens through TestEmbedBattery.
func TestEmbedNotCompilable(t *testing.T) {
	cases := []struct {
		name      string
		entry     string
		templates map[string]string
		construct string
	}{
		{"dynamic-source", "page.ql", map[string]string{
			"page.ql": "@embed name {\n@}\n", "shell.ql": "x\n"}, "@embed with a dynamic source"},
		{"target-outside-set", "page.ql", map[string]string{
			"page.ql": "@embed \"gone.ql\" {\n@}\n"}, "@embed of a template outside the flattenable subset"},
		{"self-embed", "page.ql", map[string]string{
			"page.ql": "@embed \"page.ql\" {\n@}\n"}, "recursive @embed (cycle?)"},
		{"target-declares-macro", "page.ql", map[string]string{
			"page.ql":  "@embed \"shell.ql\" {\n@}\n",
			"shell.ql": "@macro m() {\nx\n@}\nbody\n"}, "@embed of a template outside the flattenable subset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mods := parseUnit(t, tc.templates)
			_, cerr := compile.Unit(tc.entry, mods, compile.Options{})
			if cerr == nil {
				t.Fatalf("expected ErrNotCompilable for %s", tc.construct)
			}
			var nce *compile.NotCompilableError
			if !errors.As(cerr, &nce) {
				t.Fatalf("error is not *NotCompilableError: %v", cerr)
			}
			if !errors.Is(cerr, compile.ErrNotCompilable) {
				t.Fatalf("error does not match ErrNotCompilable sentinel: %v", cerr)
			}
			if nce.Construct != tc.construct {
				t.Fatalf("construct = %q, want %q", nce.Construct, tc.construct)
			}
		})
	}
}

// TestEmbedErrorPathNoLeak pins the error-path leak class for a slots-using
// embed: a flattened embed whose body writes a @yield placeholder and then
// errors mid-render must, like the interpreter's buffered render, write NOTHING
// to the caller's writer -- no partial output and no raw yield placeholder. The
// errPath leg drives Environment.RenderTo on both engines and byte-compares
// what each streams on the same error.
func TestEmbedErrorPathNoLeak(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		tmpls map[string]string
		auto  bool
	}{
		{
			name:  "embed_yield_then_error",
			entry: "page.ql",
			tmpls: map[string]string{
				"page.ql":  "@embed \"shell.ql\" {\n@}\n",
				"shell.ql": "@yield s\n@provide s {\nx\n@}\nboom {{ 1 / 0 }}\n",
			},
		},
		{
			name:  "embed_provide_into_caller_then_error",
			entry: "page.ql",
			tmpls: map[string]string{
				"page.ql":  "@yield s\n@embed \"shell.ql\" {\n@}\ntail {{ missing.field }}\n",
				"shell.ql": "@provide s {\nfrom embed\n@}\n",
			},
		},
	}
	var compiled []compiledCase
	results := map[string]*compile.Result{}
	for _, tc := range cases {
		cs := compiledCase{
			name:      tc.name,
			templates: tc.tmpls,
			entry:     tc.entry,
			opts:      compile.Options{AutoescapeHTML: tc.auto},
			errPath:   true,
		}
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", tc.name, err)
		}
		results[cs.name] = res
		compiled = append(compiled, cs)
	}
	got := runCompiled(t, compiled, results)
	for _, tc := range cases {
		r, ok := got[tc.name+"@renderto"]
		if !ok {
			t.Errorf("%s: no RenderTo result from scratch run", tc.name)
			continue
		}
		if r.failed {
			t.Errorf("%s: RenderTo leg errored: %s", tc.name, r.errText)
			continue
		}
		if r.out != "ok" {
			t.Errorf("%s: RenderTo leg reported %q", tc.name, r.out)
		}
	}
}

// embedCaseFor returns the embedBattery entry a suffixed case name derives from.
func embedCaseFor(caseName string) includeCase {
	base := caseName
	for _, suffix := range []string{"-unit", "-module"} {
		if len(base) > len(suffix) && base[len(base)-len(suffix):] == suffix {
			base = base[:len(base)-len(suffix)]
			break
		}
	}
	for _, e := range embedBattery {
		if e.name == base {
			return e
		}
	}
	return includeCase{}
}
