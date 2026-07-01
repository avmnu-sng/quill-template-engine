package cover

import "testing"

func TestCountsPercent(t *testing.T) {
	cases := []struct {
		c    Counts
		want float64
	}{
		{Counts{0, 0}, 100.0}, // empty is 100 so an all-text partial never drags
		{Counts{1, 2}, 50.0},  //
		{Counts{3, 4}, 75.0},  //
		{Counts{5, 5}, 100.0}, //
		{Counts{0, 10}, 0.0},  //
	}
	for _, tc := range cases {
		if got := tc.c.Percent(); got != tc.want {
			t.Errorf("Counts%v.Percent() = %.2f want %.2f", tc.c, got, tc.want)
		}
	}
}

func TestBuildReportTallies(t *testing.T) {
	// Two units (one covered, one not) and one two-arm branch (one arm covered) on
	// a single template, spanning two lines.
	snap := map[regionID]int64{
		{"t.ql", 1, 1, UnitText}:  3, // covered unit, line 1
		{"t.ql", 2, 1, UnitPrint}: 0, // uncovered unit, line 2
		{"t.ql", 2, 5, TernThen}:  1, // covered arm, line 2
		{"t.ql", 2, 5, TernElse}:  0, // uncovered arm, line 2
	}
	r := buildReport(snap, nil)
	tcs := r.Templates()
	if len(tcs) != 1 {
		t.Fatalf("want 1 template, got %d", len(tcs))
	}
	tc := tcs[0]
	if tc.Units != (Counts{Covered: 1, Total: 2}) {
		t.Errorf("units = %+v want {1 2}", tc.Units)
	}
	if tc.Branches != (Counts{Covered: 1, Total: 2}) {
		t.Errorf("branches = %+v want {1 2}", tc.Branches)
	}
	// Lines: line 1 has a covered unit; line 2 has an uncovered unit only (the
	// covered branch arm does not make the line a covered unit line).
	if tc.Lines != (Counts{Covered: 1, Total: 2}) {
		t.Errorf("lines = %+v want {1 2}", tc.Lines)
	}
}

func TestMergeReportsUnionsByID(t *testing.T) {
	a := buildReport(map[regionID]int64{
		{"t.ql", 1, 1, UnitPrint}: 2,
		{"t.ql", 2, 1, TernThen}:  1,
		{"t.ql", 2, 1, TernElse}:  0,
	}, map[string]string{"t.ql": "src"})
	b := buildReport(map[regionID]int64{
		{"t.ql", 1, 1, UnitPrint}: 5,
		{"t.ql", 2, 1, TernThen}:  0,
		{"t.ql", 2, 1, TernElse}:  4, // the arm a never took
	}, nil)

	m := MergeReports(a, b)
	tc := m.Templates()[0]
	// Both arms are now covered after merge (each collector took one side).
	if tc.Branches != (Counts{Covered: 2, Total: 2}) {
		t.Errorf("merged branches = %+v want {2 2}", tc.Branches)
	}
	// The unit hit count summed across the two reports (2 + 5).
	var printHits int64
	for _, reg := range tc.Regions {
		if reg.Kind == UnitPrint {
			printHits = reg.Hits
		}
	}
	if printHits != 7 {
		t.Errorf("merged print hits = %d want 7", printHits)
	}
	// Source text survives the merge from report a.
	if m.sources["t.ql"] != "src" {
		t.Errorf("merged source = %q want %q", m.sources["t.ql"], "src")
	}
}

func TestFailUnder(t *testing.T) {
	r := buildReport(map[regionID]int64{
		{"t.ql", 1, 1, UnitPrint}: 1,
		{"t.ql", 2, 1, UnitPrint}: 0,
	}, nil) // 50% unit coverage
	if err := r.FailUnder(40.0); err != nil {
		t.Errorf("FailUnder(40) over 50%% coverage should pass: %v", err)
	}
	if err := r.FailUnder(60.0); err == nil {
		t.Error("FailUnder(60) over 50% coverage should fail")
	}
	// An empty report is 100% and passes any threshold.
	if err := (&Report{}).FailUnder(90.0); err != nil {
		t.Errorf("empty report FailUnder should pass: %v", err)
	}
}
