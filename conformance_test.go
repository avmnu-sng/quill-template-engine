package quill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avmnusng/quill-template-engine/ext"
	"github.com/avmnusng/quill-template-engine/internal/jsonval"
	"github.com/avmnusng/quill-template-engine/loader"
	"github.com/avmnusng/quill-template-engine/runtime"
	"github.com/avmnusng/quill-template-engine/sandbox"
)

// buildPolicy turns a fixture's sandbox config into a *sandbox.Policy: the flat
// allowlists become string sets, the method/property maps become two-level
// sets, and the type-graph edges are declared so per-type matching walks them.
func buildPolicy(c *sandboxConfig) *sandbox.Policy {
	set := func(xs []string) map[string]bool {
		m := map[string]bool{}
		for _, x := range xs {
			m[x] = true
		}
		return m
	}
	two := func(in map[string][]string) map[string]map[string]bool {
		out := map[string]map[string]bool{}
		for typ, members := range in {
			out[typ] = set(members)
		}
		return out
	}
	g := sandbox.NewTypeGraph()
	for typ, supers := range c.Graph {
		g.Declare(typ, supers...)
	}
	return &sandbox.Policy{
		Tags:       set(c.Tags),
		Filters:    set(c.Filters),
		Functions:  set(c.Functions),
		Methods:    two(c.Methods),
		Properties: two(c.Properties),
		Graph:      g,
	}
}

// conformanceConfig is the optional per-fixture knob file (config.json). All
// fields have render-time defaults so most fixtures need no config at all.
type conformanceConfig struct {
	// Main is the template name to render; defaults to "template.ql".
	Main string `json:"main"`
	// Autoescape selects the output strategy: "off" (default) or "html".
	Autoescape string `json:"autoescape"`
	// Strict sets strict-undefined handling. The pointer distinguishes an
	// absent field (default: strict on) from an explicit false.
	Strict *bool `json:"strict"`
	// RandomSeed, when present, fixes the RNG seed (WithRandomSeed) so a fixture
	// exercising random()/shuffle gets deterministic, golden-comparable output.
	RandomSeed *int64 `json:"random_seed"`
	// Constants registers host constants (name -> JSON value) so a fixture can
	// exercise constant()/`is constant` (spec 03 Sections 3.2, 4).
	Constants map[string]json.RawMessage `json:"constants"`
	// Enums registers host enumerations (name -> ordered JSON case list) so a
	// fixture can exercise enum()/enum_cases (spec 03 Section 3.2).
	Enums map[string][]json.RawMessage `json:"enums"`
	// Sandbox, when present, installs a sandbox security policy and (when
	// SandboxActive is true) turns the sandbox on globally so a fixture exercises
	// the allow/deny enforcement against rendered output (spec 04 Section 8.3).
	Sandbox       *sandboxConfig `json:"sandbox"`
	SandboxActive bool           `json:"sandbox_active"`
}

// sandboxConfig is the JSON shape of a fixture's sandbox policy: the five
// allowlists plus an optional type-graph (typeName -> supertypes/interfaces).
type sandboxConfig struct {
	Tags       []string            `json:"tags"`
	Filters    []string            `json:"filters"`
	Functions  []string            `json:"functions"`
	Methods    map[string][]string `json:"methods"`
	Properties map[string][]string `json:"properties"`
	Graph      map[string][]string `json:"graph"`
}

// TestConformance walks testdata/conformance, where each subdirectory is one
// self-contained fixture: every *.ql file in it is loaded by name (so an
// @extends parent, an @include target, and an @import source resolve), the data
// comes from data.json (an object, optional), the knobs from config.json
// (optional), and the rendered bytes are diffed against expected.out exactly.
//
// This is Quill's own proof-of-behavior suite: it covers interpolation, pipe
// filters, postfix-if, @for/@if/@set, @extends/@block/parent, @macro/@import,
// @include, whitespace control, escaping off vs html, and the de-PHP-ified
// semantics edge cases (typed equality, truthiness, ToText spellings).
func TestConformance(t *testing.T) {
	root := filepath.Join("testdata", "conformance")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read conformance dir: %v", err)
	}
	var ran int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			runFixture(t, dir)
		})
		ran++
	}
	if ran == 0 {
		t.Fatal("no conformance fixtures found")
	}
}

func runFixture(t *testing.T, dir string) {
	t.Helper()

	cfg := loadConfig(t, dir)
	main := cfg.Main
	if main == "" {
		main = "template.ql"
	}

	// Load every .ql file in the fixture into an in-memory loader by base name,
	// so cross-template references (extends/include/import) resolve.
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
	if _, ok := tmpls[main]; !ok {
		t.Fatalf("main template %q not present in fixture", main)
	}

	vars := loadData(t, dir)

	opts := []Option{}
	switch cfg.Autoescape {
	case "", "off":
		opts = append(opts, WithAutoescapeHTML(false))
	case "html":
		opts = append(opts, WithAutoescapeHTML(true))
	default:
		t.Fatalf("unknown autoescape %q", cfg.Autoescape)
	}
	if cfg.Strict != nil {
		opts = append(opts, WithStrictVariables(*cfg.Strict))
	}
	if cfg.RandomSeed != nil {
		opts = append(opts, WithRandomSeed(*cfg.RandomSeed))
	}
	if cfg.Sandbox != nil {
		opts = append(opts, WithSandboxPolicy(buildPolicy(cfg.Sandbox)))
		if cfg.SandboxActive {
			opts = append(opts, WithSandboxActive(true))
		}
	}
	if len(cfg.Constants) > 0 || len(cfg.Enums) > 0 {
		set := ext.Core()
		for name, raw := range cfg.Constants {
			v, err := jsonval.Decode(raw)
			if err != nil {
				t.Fatalf("decode constant %q: %v", name, err)
			}
			set.AddConstant(name, v)
		}
		for name, rawCases := range cfg.Enums {
			cases := make([]runtime.Value, len(rawCases))
			for i, raw := range rawCases {
				v, err := jsonval.Decode(raw)
				if err != nil {
					t.Fatalf("decode enum %q case %d: %v", name, i, err)
				}
				cases[i] = v
			}
			set.AddEnum(name, cases)
		}
		opts = append(opts, WithExtensions(set))
	}

	env := New(loader.NewArrayLoader(tmpls), opts...)
	got, err := env.Render(main, vars)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}

	want, err := os.ReadFile(filepath.Join(dir, "expected.out"))
	if err != nil {
		t.Fatalf("read expected.out: %v", err)
	}
	if got != string(want) {
		t.Errorf("output mismatch\n--- got ----\n%q\n--- want ---\n%q", got, string(want))
	}
}

func loadConfig(t *testing.T, dir string) conformanceConfig {
	t.Helper()
	var cfg conformanceConfig
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

func loadData(t *testing.T, dir string) map[string]runtime.Value {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "data.json"))
	if os.IsNotExist(err) {
		return map[string]runtime.Value{}
	}
	if err != nil {
		t.Fatalf("read data.json: %v", err)
	}
	vars, err := jsonval.DecodeMap(b)
	if err != nil {
		t.Fatalf("decode data.json: %v", err)
	}
	return vars
}
