package cover

import (
	"fmt"
	"io"
	"sort"
)

// WriteLCOV writes a standard LCOV .info stream that Codecov, genhtml, and CI
// coverage gates ingest directly (docs/coverage.md Section 4.2). One SF section
// per template: DA line-hit records derived from unit hit counts, BRDA branch
// records for every arm (with '-' for a reachable-but-never-taken arm), and the
// BRF/BRH and LF/LH summary counts a gate thresholds on.
func (r *Report) WriteLCOV(w io.Writer) error {
	bw := &errWriter{w: w}
	for _, tc := range r.Templates() {
		bw.writeString("TN:\n")
		bw.writeString("SF:" + tc.Name + "\n")

		writeDA(bw, tc)
		lf, lh := writeBRDA(bw, tc)

		bw.writeString(fmt.Sprintf("BRF:%d\n", tc.Branches.Total))
		bw.writeString(fmt.Sprintf("BRH:%d\n", tc.Branches.Covered))
		bw.writeString(fmt.Sprintf("LF:%d\n", lf))
		bw.writeString(fmt.Sprintf("LH:%d\n", lh))
		bw.writeString("end_of_record\n")
	}
	return bw.err
}

// writeDA emits one DA:line,hits record per source line that carries a unit,
// where hits is the summed unit hit count on that line (0 for a line whose units
// never ran). Lines are emitted in ascending order.
func writeDA(bw *errWriter, tc TemplateCoverage) {
	lineHits := map[int]int64{}
	seen := map[int]bool{}
	for _, reg := range tc.Regions {
		if reg.Branch {
			continue
		}
		seen[reg.Line] = true
		lineHits[reg.Line] += reg.Hits
	}
	lines := make([]int, 0, len(seen))
	for l := range seen {
		lines = append(lines, l)
	}
	sort.Ints(lines)
	for _, l := range lines {
		bw.writeString(fmt.Sprintf("DA:%d,%d\n", l, lineHits[l]))
	}
}

// writeBRDA emits BRDA:line,block,branch,taken records. Arms of one branch point
// (same line:col) share a block id; branch numbers the arms within it in a stable
// kind order. taken is the hit count, or '-' for an arm never taken. It returns
// the LF/LH line found/hit counts (derived from the unit lines) so the caller can
// emit them without recomputing.
func writeBRDA(bw *errWriter, tc TemplateCoverage) (lf, lh int) {
	// Group branch arms by their branch point (line, col), preserving first-seen
	// order so block ids are stable and deterministic.
	type point struct{ line, col int }
	order := []point{}
	arms := map[point][]Region{}
	for _, reg := range tc.Regions {
		if !reg.Branch {
			continue
		}
		p := point{reg.Line, reg.Col}
		if _, ok := arms[p]; !ok {
			order = append(order, p)
		}
		arms[p] = append(arms[p], reg)
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].line != order[j].line {
			return order[i].line < order[j].line
		}
		return order[i].col < order[j].col
	})
	for block, p := range order {
		group := arms[p]
		sort.SliceStable(group, func(i, j int) bool { return group[i].Kind < group[j].Kind })
		for branch, reg := range group {
			taken := "-"
			if reg.Covered() {
				taken = fmt.Sprintf("%d", reg.Hits)
			}
			bw.writeString(fmt.Sprintf("BRDA:%d,%d,%d,%s\n", reg.Line, block, branch, taken))
		}
	}

	// LF/LH: line found is the count of lines carrying a unit; line hit is those
	// with a covered unit.
	lf = tc.Lines.Total
	lh = tc.Lines.Covered
	return lf, lh
}
