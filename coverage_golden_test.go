package quill

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/cover"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// updateGolden regenerates the committed coverage report golden files when set
// (`go test -run TestCoverageReportGolden -update ./...`). It is off by default so
// a normal test run only compares against the committed goldens.
var updateGolden = flag.Bool("update", false, "update coverage report golden files")

// goldenReport renders a fixed template set through a coverage-instrumented
// Environment and returns the resulting Report. The set is chosen to exercise
// every branch of every reporter: a covered unit and an uncovered unit; a fully
// covered @if (both arms fired across two renders) and a partially covered @for
// (body arm only); an unreached @else body; and a shared partial recorded under
// its own name. It renders TWICE with different data so the two @if arms and the
// two @for arms are unioned into one Report, the aggregation the reporters
// consume. Output is deterministic: templates and regions sort by name/position,
// so the golden bytes are stable across runs.
func goldenReport(t *testing.T) *cover.Report {
	t.Helper()

	// page.ql lines:
	//   1: HEAD
	//   2: @if admin {
	//   3: {{ name }}
	//   4: @} else {
	//   5: guest
	//   6: @}
	//   7: @for row in rows {
	//   8: [{{ row }}]
	//   9: @}
	//  10: @include "foot.ql"
	page := "HEAD\n" +
		"@if admin {\n" +
		"{{ name }}\n" +
		"@} else {\n" +
		"guest\n" +
		"@}\n" +
		"@for row in rows {\n" +
		"[{{ row }}]\n" +
		"@}\n" +
		"@include \"foot.ql\"\n"
	foot := "FOOT\n"

	coll := cover.NewCollector()
	env := NewFromMap(map[string]string{
		"page.ql": page,
		"foot.ql": foot,
	}, WithCoverage(coll))

	// Render 1: admin true, a non-empty rows list. Takes the @if then arm and the
	// @for body arm; the @else body ("guest") stays unreached.
	if _, err := env.Render(context.Background(), "page.ql", map[string]runtime.Value{
		"admin": runtime.Bool(true),
		"name":  runtime.Str("Ada"),
		"rows":  runtime.Arr(runtime.NewList(runtime.Int(1), runtime.Int(2))),
	}); err != nil {
		t.Fatalf("render 1: %v", err)
	}
	// Render 2: admin false, again a non-empty rows list. Takes the @if not-taken
	// and else arms, so the @if is now fully covered; the @for empty arm still
	// never fires, so the @for stays partially covered.
	if _, err := env.Render(context.Background(), "page.ql", map[string]runtime.Value{
		"admin": runtime.Bool(false),
		"name":  runtime.Str("Bob"),
		"rows":  runtime.Arr(runtime.NewList(runtime.Int(3))),
	}); err != nil {
		t.Fatalf("render 2: %v", err)
	}

	return coll.Report()
}

// compareGolden asserts got matches the committed golden file at
// testdata/coverage/<name>, or rewrites it under -update.
func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "coverage", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create): %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s mismatch\n--- got ----\n%s\n--- want ---\n%s", name, got, want)
	}
}

// TestCoverageReportGolden pins the text, verbose-text, LCOV, and HTML reports of
// a fixed instrumented render against committed golden files. It is the
// acceptance test for the reporters: it proves each format writes to an io.Writer,
// that LCOV carries valid DA and BRDA records, and that the HTML is self-contained
// (a single <!doctype html> document with inline styles and no external asset
// references). Regenerate with `go test -run TestCoverageReportGolden -update`.
func TestCoverageReportGolden(t *testing.T) {
	r := goldenReport(t)

	var text bytes.Buffer
	if err := r.WriteText(&text); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	compareGolden(t, "report.txt", text.Bytes())

	var verbose bytes.Buffer
	if err := r.WriteTextVerbose(&verbose); err != nil {
		t.Fatalf("WriteTextVerbose: %v", err)
	}
	compareGolden(t, "report.verbose.txt", verbose.Bytes())

	var lcov bytes.Buffer
	if err := r.WriteLCOV(&lcov); err != nil {
		t.Fatalf("WriteLCOV: %v", err)
	}
	compareGolden(t, "coverage.info", lcov.Bytes())

	var html bytes.Buffer
	if err := r.WriteHTML(&html); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	compareGolden(t, "report.html", html.Bytes())

	// Guard the self-contained HTML invariant independently of the golden bytes:
	// the report must not reference any external stylesheet, script, or image.
	for _, bad := range []string{"http://", "https://", "src=", "<link", "<script src"} {
		if bytes.Contains(html.Bytes(), []byte(bad)) {
			t.Errorf("HTML report is not self-contained: found %q", bad)
		}
	}
}
