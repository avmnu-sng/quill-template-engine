package cover

import (
	"bytes"
	"strings"
	"testing"
)

// sampleReport builds a report with one covered unit, one uncovered unit, and a
// two-arm branch with only the then arm taken -- enough to exercise every writer
// path (DA hit/miss, BRDA hit and '-' sentinel, covered/uncovered/neutral lines).
func sampleReport() *Report {
	snap := map[regionID]int64{
		{"page.ql", 1, 1, UnitIf}:     2, // covered unit, line 1
		{"page.ql", 1, 1, IfThen}:     2, // then arm taken
		{"page.ql", 1, 1, IfNotTaken}: 0, // not-taken arm never fired
		{"page.ql", 2, 1, UnitText}:   2, // covered unit, line 2
		{"page.ql", 4, 1, UnitText}:   0, // uncovered unit, line 4 (else body)
	}
	src := "@if a {\nyes\n@} else {\nno\n@}\n"
	return buildReport(snap, map[string]string{"page.ql": src})
}

func TestWriteText(t *testing.T) {
	var b bytes.Buffer
	if err := sampleReport().WriteText(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"Template", "Units", "Branches", "Lines", "page.ql", "TOTAL"} {
		if !strings.Contains(out, want) {
			t.Errorf("text report missing %q:\n%s", want, out)
		}
	}
	// Units 2/3, branches 1/2.
	if !strings.Contains(out, "2/3") {
		t.Errorf("expected unit tally 2/3 in:\n%s", out)
	}
	if !strings.Contains(out, "1/2") {
		t.Errorf("expected branch tally 1/2 in:\n%s", out)
	}
}

func TestWriteTextVerbose(t *testing.T) {
	var b bytes.Buffer
	if err := sampleReport().WriteTextVerbose(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	// The uncovered else-body text unit and the never-fired not-taken arm are named.
	if !strings.Contains(out, "never reached") {
		t.Errorf("verbose report should name an uncovered unit:\n%s", out)
	}
	if !strings.Contains(out, "condition never false") {
		t.Errorf("verbose report should name the missing if-not-taken arm:\n%s", out)
	}
}

func TestWriteLCOV(t *testing.T) {
	var b bytes.Buffer
	if err := sampleReport().WriteLCOV(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	// Structure.
	for _, want := range []string{"TN:", "SF:page.ql", "end_of_record"} {
		if !strings.Contains(out, want) {
			t.Errorf("lcov missing %q:\n%s", want, out)
		}
	}
	// DA records: line 1 hit twice, line 4 never (0 hits).
	if !strings.Contains(out, "DA:1,2") {
		t.Errorf("lcov missing DA:1,2:\n%s", out)
	}
	if !strings.Contains(out, "DA:4,0") {
		t.Errorf("lcov missing DA:4,0:\n%s", out)
	}
	// BRDA: the then arm taken 2x, the not-taken arm never (a '-' sentinel).
	if !strings.Contains(out, "BRDA:1,0,0,2") {
		t.Errorf("lcov missing taken branch record:\n%s", out)
	}
	if !strings.Contains(out, "BRDA:1,0,1,-") {
		t.Errorf("lcov missing '-' sentinel for a reachable-but-never-taken arm:\n%s", out)
	}
	// Summary counts: 2 arms found, 1 hit; 3 lines found, 2 hit.
	if !strings.Contains(out, "BRF:2") || !strings.Contains(out, "BRH:1") {
		t.Errorf("lcov branch summary wrong:\n%s", out)
	}
	if !strings.Contains(out, "LF:3") || !strings.Contains(out, "LH:2") {
		t.Errorf("lcov line summary wrong:\n%s", out)
	}
}

func TestWriteHTMLEscapesSource(t *testing.T) {
	// A template whose text contains markup must be escaped so it cannot break the
	// report page.
	snap := map[regionID]int64{
		{"x.ql", 1, 1, UnitText}: 1,
	}
	r := buildReport(snap, map[string]string{"x.ql": "<script>alert(1)</script>\n"})
	var b bytes.Buffer
	if err := r.WriteHTML(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Errorf("source markup was not escaped in the HTML report")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("expected escaped source in the HTML report:\n%s", out)
	}
}

func TestMergeReportsParallel(t *testing.T) {
	// Two per-goroutine collectors each take a different arm; merging unions them.
	build := func(then, notTaken int64) *Report {
		return buildReport(map[regionID]int64{
			{"t.ql", 1, 1, UnitIf}:     1,
			{"t.ql", 1, 1, IfThen}:     then,
			{"t.ql", 1, 1, IfNotTaken}: notTaken,
		}, nil)
	}
	merged := MergeReports(build(3, 0), build(0, 4))
	tc := merged.Templates()[0]
	if tc.Branches != (Counts{Covered: 2, Total: 2}) {
		t.Errorf("merged branches = %+v want {2 2}", tc.Branches)
	}
}
