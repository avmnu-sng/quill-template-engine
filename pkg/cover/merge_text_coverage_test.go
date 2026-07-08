package cover

import (
	"bytes"
	"strings"
	"testing"
)

// TestReportMergeMethodUnionsCounts drives the Merge METHOD (the (*Report).Merge
// wrapper over MergeReports, distinct from the package-level MergeReports the
// existing tests exercise). It asserts the two documented guarantees: hit counts
// add per region id, and a region present in only one operand survives the union.
func TestReportMergeMethodUnionsCounts(t *testing.T) {
	// a covers the then arm and a unit on line 1; b covers the else arm and adds a
	// wholly new unit on line 3 that a never mentions.
	a := buildReport(map[regionID]int64{
		{"t.ql", 1, 1, UnitPrint}: 2,
		{"t.ql", 2, 1, IfThen}:    1,
		{"t.ql", 2, 1, IfElse}:    0,
	}, map[string]string{"t.ql": "src-a"})
	b := buildReport(map[regionID]int64{
		{"t.ql", 1, 1, UnitPrint}: 5,
		{"t.ql", 2, 1, IfThen}:    0,
		{"t.ql", 2, 1, IfElse}:    3, // arm a never took
		{"t.ql", 3, 1, UnitText}:  4, // region present only in b
	}, nil)

	m := a.Merge(b)
	tcs := m.Templates()
	if len(tcs) != 1 {
		t.Fatalf("merged report should have 1 template, got %d", len(tcs))
	}
	tc := tcs[0]

	// Region-level assertions: gather the merged hits by (line, col, kind).
	hits := map[regionID]int64{}
	for _, reg := range tc.Regions {
		hits[regionID{tmpl: "t.ql", line: reg.Line, col: reg.Col, kind: reg.Kind}] = reg.Hits
	}
	// Counts add for a region hit in both operands (2 + 5).
	if got := hits[regionID{tmpl: "t.ql", line: 1, col: 1, kind: UnitPrint}]; got != 7 {
		t.Errorf("merged UnitPrint hits = %d want 7", got)
	}
	// Each branch arm was taken by exactly one operand; both add to non-zero.
	if got := hits[regionID{tmpl: "t.ql", line: 2, col: 1, kind: IfThen}]; got != 1 {
		t.Errorf("merged IfThen hits = %d want 1", got)
	}
	if got := hits[regionID{tmpl: "t.ql", line: 2, col: 1, kind: IfElse}]; got != 3 {
		t.Errorf("merged IfElse hits = %d want 3", got)
	}
	// A region present only in b is present after the union with its own count.
	if got := hits[regionID{tmpl: "t.ql", line: 3, col: 1, kind: UnitText}]; got != 4 {
		t.Errorf("b-only UnitText hits = %d want 4", got)
	}

	// Rollup-level assertions: both arms are now covered (each operand took one).
	if tc.Branches != (Counts{Covered: 2, Total: 2}) {
		t.Errorf("merged branches = %+v want {2 2}", tc.Branches)
	}
	// Two units total (Print on line 1, Text on line 3), both covered.
	if tc.Units != (Counts{Covered: 2, Total: 2}) {
		t.Errorf("merged units = %+v want {2 2}", tc.Units)
	}
	// Source text carries through the merge from operand a.
	if m.sources["t.ql"] != "src-a" {
		t.Errorf("merged source = %q want %q", m.sources["t.ql"], "src-a")
	}
}

// TestReportMergeMethodNilOperand asserts the Merge method tolerates a nil other
// operand: the result equals the receiver's own counts, nothing is dropped.
func TestReportMergeMethodNilOperand(t *testing.T) {
	a := buildReport(map[regionID]int64{
		{"t.ql", 1, 1, UnitPrint}: 3,
		{"t.ql", 1, 1, UnitText}:  0,
	}, nil)
	m := a.Merge(nil)
	tc := m.Templates()[0]
	if tc.Units != (Counts{Covered: 1, Total: 2}) {
		t.Errorf("Merge(nil) units = %+v want {1 2}", tc.Units)
	}
	// Merge(nil) preserves the receiver's region set exactly -- no region is
	// dropped and no phantom region is invented by unioning against nil.
	if len(tc.Regions) != 2 {
		t.Errorf("Merge(nil) region count = %d want 2", len(tc.Regions))
	}
	var printHits int64
	for _, reg := range tc.Regions {
		if reg.Kind == UnitPrint {
			printHits = reg.Hits
		}
	}
	if printHits != 3 {
		t.Errorf("Merge(nil) UnitPrint hits = %d want 3", printHits)
	}
}

// TestArmNoteEveryArm pins the exact note armNote returns for every branch-arm
// kind, including the default fallback for a non-arm kind. These strings are the
// verbose-breakdown wording a developer reads, so a golden-exact table guards
// against silent wording drift and covers each switch case.
func TestArmNoteEveryArm(t *testing.T) {
	cases := []struct {
		kind RegionKind
		want string
	}{
		{IfThen, "then arm never taken"},
		{IfNotTaken, "condition never false"},
		{IfElse, "else arm never taken"},
		{ForBody, "loop never ran non-empty"},
		{ForEmpty, "loop never ran empty"},
		{TernThen, "then arm never taken"},
		{TernElse, "else arm never taken"},
		{ElvisLeft, "left never kept"},
		{ElvisRight, "fallback never used"},
		{CoalLeft, "left never non-null"},
		{CoalRight, "fallback never used"},
		{GuardYes, "callable never present"},
		{GuardNo, "callable never absent"},
		// A non-arm kind falls through to the default branch.
		{UnitText, "arm never taken"},
	}
	for _, tc := range cases {
		if got := armNote(tc.kind); got != tc.want {
			t.Errorf("armNote(%s) = %q want %q", tc.kind, got, tc.want)
		}
	}
}

// TestClip pins clip's truncation for each branch: a short name is returned
// verbatim, a name exactly at the width is unchanged, an over-long name is cut to
// width-1 with a trailing '~', and the width<=1 guard returns a hard prefix.
func TestClip(t *testing.T) {
	cases := []struct {
		s     string
		width int
		want  string
	}{
		{"short", 24, "short"}, // len < width: verbatim
		{"exactly-twenty-four-char", 24, "exactly-twenty-four-char"},         // len == width: verbatim
		{"this-name-is-far-too-long-to-fit", 24, "this-name-is-far-too-lo~"}, // clipped: 23 chars + '~'
		{"abc", 1, "a"}, // width<=1: hard prefix, no '~'
		{"ab", 0, ""},   // width 0: empty prefix
	}
	for _, tc := range cases {
		got := clip(tc.s, tc.width)
		if got != tc.want {
			t.Errorf("clip(%q, %d) = %q want %q", tc.s, tc.width, got, tc.want)
		}
		// The clipped result never exceeds the requested width.
		if len(got) > tc.width {
			t.Errorf("clip(%q, %d) len %d exceeds width", tc.s, tc.width, len(got))
		}
	}
}

// TestWriteTextClipsLongTemplateName drives clip through the real WriteText path:
// a template whose name overflows the 24-char column must appear truncated with a
// trailing '~', and the full name must NOT appear verbatim.
func TestWriteTextClipsLongTemplateName(t *testing.T) {
	const longName = "deeply/nested/partials/header.ql" // 32 chars, over the 24 column
	snap := map[regionID]int64{
		{longName, 1, 1, UnitText}: 1,
	}
	r := buildReport(snap, nil)
	var b bytes.Buffer
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	clipped := clip(longName, 24)
	if !strings.HasSuffix(clipped, "~") {
		t.Fatalf("test setup: %q should have been clipped", longName)
	}
	if !strings.Contains(out, clipped) {
		t.Errorf("text report missing clipped name %q:\n%s", clipped, out)
	}
	if strings.Contains(out, longName) {
		t.Errorf("text report should not contain the full over-long name %q:\n%s", longName, out)
	}
}

// TestWriteTextVerboseGoldenBreakdown asserts the exact per-region breakdown lines
// the verbose writer emits: an uncovered unit reads "never reached", and each
// uncovered branch arm reads its armNote wording, with the Kind label and
// line:col columns formatted precisely.
func TestWriteTextVerboseGoldenBreakdown(t *testing.T) {
	// One uncovered unit, plus an @for whose body ran but whose empty arm never did,
	// plus a coalesce whose right fallback never fired.
	snap := map[regionID]int64{
		{"p.ql", 3, 1, UnitText}:  0, // uncovered unit -> "never reached"
		{"p.ql", 5, 1, UnitFor}:   2, // covered enclosing unit
		{"p.ql", 5, 1, ForBody}:   2, // taken arm (covered)
		{"p.ql", 5, 1, ForEmpty}:  0, // uncovered arm -> "loop never ran empty"
		{"p.ql", 7, 4, CoalRight}: 0, // uncovered arm -> "fallback never used"
	}
	r := buildReport(snap, nil)
	var b bytes.Buffer
	if err := r.WriteTextVerbose(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	// The template header appears before its gaps.
	if !strings.Contains(out, "\np.ql\n") {
		t.Errorf("verbose report missing template header:\n%s", out)
	}
	// Golden-exact breakdown lines (format "  %d:%-3d %-14s %s").
	wantLines := []string{
		"  3:1   Text           never reached",
		"  5:1   for-empty      loop never ran empty",
		"  7:4   coalesce-right fallback never used",
	}
	for _, want := range wantLines {
		if !strings.Contains(out, want) {
			t.Errorf("verbose breakdown missing exact line %q in:\n%s", want, out)
		}
	}
	// The covered arm and the covered enclosing unit must NOT be listed as gaps.
	if strings.Contains(out, "for-body") {
		t.Errorf("verbose breakdown listed a covered arm (for-body):\n%s", out)
	}
	// The covered UnitFor on line 5 ("For" label) is a gap only if it were
	// uncovered; a "5:1" line whose kind column reads "For" would mean a covered
	// unit was over-listed. Guard against that specific over-listing.
	if strings.Contains(out, "5:1   For ") {
		t.Errorf("verbose breakdown listed a covered unit (For) as a gap:\n%s", out)
	}
	// Exactly three regions are uncovered (the text unit, the for-empty arm, and
	// the coalesce-right arm); no covered region may leak into the breakdown. The
	// gap lines are the ones indented with two leading spaces after the header.
	gapSection := out[strings.Index(out, "\np.ql\n")+len("\np.ql\n"):]
	gapLines := 0
	for _, ln := range strings.Split(gapSection, "\n") {
		if strings.HasPrefix(ln, "  ") && strings.TrimSpace(ln) != "" {
			gapLines++
		}
	}
	if gapLines != 3 {
		t.Errorf("verbose breakdown listed %d gap lines, want exactly 3:\n%s", gapLines, out)
	}
}
