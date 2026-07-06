package compile_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/compile"
	"github.com/avmnu-sng/quill-template-engine/core/ast"
	"github.com/avmnu-sng/quill-template-engine/core/parse"
	"github.com/avmnu-sng/quill-template-engine/core/source"
	"github.com/avmnu-sng/quill-template-engine/internal/jsonval"
	"github.com/avmnu-sng/quill-template-engine/loader"
)

// oracleConfig mirrors the conformance suite's per-fixture config.json knobs
// (conformance_test.go's conformanceConfig) without importing the root test.
type oracleConfig struct {
	Main          string                       `json:"main"`
	Autoescape    string                       `json:"autoescape"`
	Strict        *bool                        `json:"strict"`
	RandomSeed    *int64                       `json:"random_seed"`
	Constants     map[string]json.RawMessage   `json:"constants"`
	Enums         map[string][]json.RawMessage `json:"enums"`
	Sandbox       json.RawMessage              `json:"sandbox"`
	SandboxActive bool                         `json:"sandbox_active"`
	ErrorContains string                       `json:"error_contains"`
}

// oracleBucket classifies one fixture's oracle outcome.
type oracleBucket struct {
	fixture string
	bucket  string // "compiled-equal", "not-compilable", "not-compilable-config"
	reason  string // the construct or config reason
}

// TestConformanceOracle drives the whole conformance corpus through the
// compile backend: every fixture either compiles and renders byte-identical
// to the interpreter, or is reported not compilable with a named construct,
// or is excluded for a config the compiled path cannot honor. Every fixture
// lands in exactly one bucket; none is silently skipped. Each compiled
// fixture additionally renders by name through an Environment with its
// generated manifest installed (quill.WithCompiled) under the fixture's own
// autoescape/strict combination, with a tracer manifest proving the dispatch
// gate served the compiled unit rather than silently falling back, and then
// through the whole fingerprint matrix (every autoescape/strict combination
// plus a tab-width flip and a random-seed flip): under each combination the
// manifest-installed Environment must render byte- and error-identical to a
// manifest-free one, with the tracer served exactly at the matching
// combination -- the CI leg that runs the corpus as mixed serve/fallback
// traffic once per fingerprint combination.
func TestConformanceOracle(t *testing.T) {
	root := filepath.Join(repoRoot(t), "testdata", "conformance")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read conformance dir: %v", err)
	}

	var buckets []oracleBucket
	var cases []compiledCase
	results := map[string]*compile.Result{}
	interpOut := map[string]string{}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fixture := e.Name()
		dir := filepath.Join(root, fixture)
		cfg := loadOracleConfig(t, dir)

		if reason := configExclusion(cfg); reason != "" {
			buckets = append(buckets, oracleBucket{fixture, "not-compilable-config", reason})
			continue
		}

		main := cfg.Main
		if main == "" {
			main = "template.ql"
		}
		tmpls := loadFixtureTemplates(t, dir)
		body, ok := tmpls[main]
		if !ok {
			t.Fatalf("%s: main template %q missing", fixture, main)
		}

		mod, err := parse.Parse(source.New(main, body))
		if err != nil {
			buckets = append(buckets, oracleBucket{fixture, "not-compilable-config", "parse error: " + err.Error()})
			continue
		}
		// Parse the fixture's sibling templates too, so a static @include in the
		// main module can inline a partial the same way a Module compiled by a
		// host that hands compile the sibling set would.
		siblings := map[string]*ast.Node{}
		for tname, tbody := range tmpls {
			if tname == main {
				continue
			}
			smod, serr := parse.Parse(source.New(tname, tbody))
			if serr != nil {
				continue
			}
			siblings[tname] = smod
		}
		opts := compile.Options{
			PackageName:      pkgName(fixture),
			AutoescapeHTML:   cfg.Autoescape == "html",
			LenientVariables: cfg.Strict != nil && !*cfg.Strict,
			Templates:        siblings,
		}
		res, cerr := compile.Module(main, mod, opts)
		if cerr != nil {
			var nce *compile.NotCompilableError
			if errors.As(cerr, &nce) {
				buckets = append(buckets, oracleBucket{fixture, "not-compilable", nce.Construct})
				continue
			}
			t.Fatalf("%s: unexpected compile error: %v", fixture, cerr)
		}

		// The interpreter is the oracle; a fixture whose reference render
		// errors cannot be byte-compared and is classified out.
		varsJSON := loadFixtureData(t, dir)
		want, rerr := renderFixtureInterp(t, tmpls, main, varsJSON, cfg)
		if rerr != nil {
			buckets = append(buckets, oracleBucket{fixture, "not-compilable-config", "interp render error: " + rerr.Error()})
			continue
		}

		results[fixture] = res
		interpOut[fixture] = want
		cases = append(cases, compiledCase{
			name: fixture, template: body, varsJSON: varsJSON,
			opts: opts, envCheck: true,
		})
	}

	got := runCompiled(t, cases, results)
	for _, cs := range cases {
		r, ok := got[cs.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", cs.name)
			continue
		}
		if r.failed {
			t.Errorf("%s: compiled render errored: %s", cs.name, r.errText)
			continue
		}
		if r.out != interpOut[cs.name] {
			t.Errorf("%s: compiled output differs from interpreter\n got  %q\n want %q", cs.name, r.out, interpOut[cs.name])
			continue
		}
		buckets = append(buckets, oracleBucket{cs.name, "compiled-equal", ""})

		// The WithCompiled leg: the by-name Environment render must match the
		// interpreter byte for byte, and the tracer must confirm the dispatch
		// gate served the compiled unit for this fixture's configuration.
		envR, ok := got[cs.name+"@env"]
		switch {
		case !ok:
			t.Errorf("%s: no env-dispatch result from scratch run", cs.name)
		case envR.failed:
			t.Errorf("%s: env-dispatch render errored: %s", cs.name, envR.errText)
		case envR.out != interpOut[cs.name]:
			t.Errorf("%s: env-dispatch output differs from interpreter\n got  %q\n want %q", cs.name, envR.out, interpOut[cs.name])
		}
		tr, ok := got[cs.name+"@tracer"]
		switch {
		case !ok:
			t.Errorf("%s: no tracer result from scratch run", cs.name)
		case tr.failed:
			t.Errorf("%s: tracer render errored: %s", cs.name, tr.errText)
		case tr.out != "served":
			t.Errorf("%s: dispatch gate fell back for a fixture it should serve", cs.name)
		}
		mx, ok := got[cs.name+"@matrix"]
		switch {
		case !ok:
			t.Errorf("%s: no fingerprint-matrix result from scratch run", cs.name)
		case mx.failed:
			t.Errorf("%s: fingerprint-matrix leg failed: %s", cs.name, mx.errText)
		case mx.out != "ok":
			t.Errorf("%s: fingerprint-matrix leg reported %q", cs.name, mx.out)
		}
	}

	// Report the classification: every fixture in exactly one bucket.
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].fixture < buckets[j].fixture })
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, b := range buckets {
		if seen[b.fixture] {
			t.Errorf("%s: classified twice", b.fixture)
		}
		seen[b.fixture] = true
		counts[b.bucket]++
		if b.reason != "" {
			t.Logf("%-32s %s (%s)", b.fixture, b.bucket, b.reason)
		} else {
			t.Logf("%-32s %s", b.fixture, b.bucket)
		}
	}
	total := 0
	for _, e := range entries {
		if e.IsDir() {
			total++
		}
	}
	if len(seen) != total {
		t.Errorf("classified %d fixtures, corpus has %d", len(seen), total)
	}
	t.Logf("oracle: compiled-equal=%d not-compilable=%d not-compilable-config=%d total=%d",
		counts["compiled-equal"], counts["not-compilable"], counts["not-compilable-config"], total)
	if counts["compiled-equal"] < 15 {
		t.Errorf("only %d fixtures compiled byte-equal; the compilable subset should cover more of the corpus", counts["compiled-equal"])
	}
}

// configExclusion names the config knob that keeps a fixture off the compiled
// path, or "" when the fixture is a candidate.
func configExclusion(cfg oracleConfig) string {
	switch {
	case cfg.ErrorContains != "":
		return "deny fixture (error_contains)"
	case cfg.Sandbox != nil:
		return "sandbox policy"
	case cfg.RandomSeed != nil:
		return "random seed"
	case len(cfg.Constants) > 0:
		return "host constants"
	case len(cfg.Enums) > 0:
		return "host enums"
	default:
		return ""
	}
}

func loadOracleConfig(t *testing.T, dir string) oracleConfig {
	t.Helper()
	var cfg oracleConfig
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if os.IsNotExist(err) {
		return cfg
	}
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("parse config.json: %v", err)
	}
	return cfg
}

func loadFixtureTemplates(t *testing.T, dir string) map[string]string {
	t.Helper()
	tmpls := map[string]string{}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read fixture dir: %v", err)
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".ql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", f.Name(), err)
		}
		tmpls[f.Name()] = string(b)
	}
	return tmpls
}

func loadFixtureData(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "data.json"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read data.json: %v", err)
	}
	return string(b)
}

// renderFixtureInterp renders the fixture through the facade with the same
// options the conformance suite would use for its config.
func renderFixtureInterp(t *testing.T, tmpls map[string]string, main, varsJSON string, cfg oracleConfig) (string, error) {
	t.Helper()
	var opts []quill.Option
	switch cfg.Autoescape {
	case "", "off":
		opts = append(opts, quill.WithAutoescapeHTML(false))
	case "html":
		opts = append(opts, quill.WithAutoescapeHTML(true))
	default:
		t.Fatalf("unknown autoescape %q", cfg.Autoescape)
	}
	if cfg.Strict != nil {
		opts = append(opts, quill.WithStrictVariables(*cfg.Strict))
	}
	vars, err := jsonval.DecodeMap([]byte(orEmptyObject(varsJSON)))
	if err != nil {
		t.Fatalf("decode fixture data: %v", err)
	}
	env := quill.New(loader.NewArrayLoader(tmpls), opts...)
	return env.Render(main, vars)
}
