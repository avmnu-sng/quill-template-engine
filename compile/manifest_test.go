package compile_test

import (
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/compile"
	"github.com/avmnu-sng/quill-template-engine/parse"
	"github.com/avmnu-sng/quill-template-engine/source"
)

// compileSource is a tiny helper that parses and compiles one body with the
// given options, failing the test on any error.
func compileSource(t *testing.T, name, body string, opts compile.Options) string {
	t.Helper()
	mod, err := parse.Parse(source.New(name, body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := compile.Module(name, mod, opts)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return string(res.Source)
}

// TestGeneratedManifestCarriesOptionsFingerprint pins the emitted manifest:
// the exported <FuncName>Manifest value must carry the normalized compile
// options field for field, because the Environment's dispatch gate compares
// them against its own configuration byte for byte.
func TestGeneratedManifestCarriesOptionsFingerprint(t *testing.T) {
	src := compileSource(t, "t.ql", "hello {{ x }}", compile.Options{
		PackageName:      "fx",
		FuncName:         "RenderT",
		AutoescapeHTML:   true,
		LenientVariables: true,
		TabWidth:         2,
		RandomSeed:       9,
		RandomSeedSet:    true,
	})
	for _, want := range []string{
		"var RenderTManifest = &compiled.Manifest{",
		"Entry:       qSrc.Name(),",
		"Sources:     map[string]string{qSrc.Name(): qSrc.Code()},",
		"Fingerprint: compiled.Fingerprint{AutoescapeHTML: true, LenientVariables: true, TabWidth: 2, RandomSeed: 9, RandomSeedSet: true},",
		"UsesLog:     false,",
		"Render:      RenderT,",
		"\"github.com/avmnu-sng/quill-template-engine/compiled\"",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated source missing %q", want)
		}
	}
}

// TestGeneratedManifestNormalizesDefaults pins that the fingerprint records
// the NORMALIZED options (tab width defaulting to 4), matching what the
// generated engine handle actually uses, not the raw zero values passed in.
func TestGeneratedManifestNormalizesDefaults(t *testing.T) {
	src := compileSource(t, "t.ql", "hi", compile.Options{PackageName: "fx"})
	want := "Fingerprint: compiled.Fingerprint{AutoescapeHTML: false, LenientVariables: false, TabWidth: 4, RandomSeed: 0, RandomSeedSet: false},"
	if !strings.Contains(src, want) {
		t.Errorf("generated source missing %q", want)
	}
	if !strings.Contains(src, "var RenderManifest = &compiled.Manifest{") {
		t.Error("default FuncName must yield RenderManifest")
	}
}

// TestGeneratedManifestMarksLogUnits pins UsesLog: a unit lowering any @log
// statement must say so, because the dispatch gate falls back for such units
// whenever a non-discarding logger is configured.
func TestGeneratedManifestMarksLogUnits(t *testing.T) {
	src := compileSource(t, "t.ql", "x\n@log \"note\"\n", compile.Options{PackageName: "fx"})
	if !strings.Contains(src, "UsesLog:     true,") {
		t.Error("unit with @log must emit UsesLog: true")
	}
}
