package cover

import (
	"fmt"
	"sort"
)

// Counts is a covered/total tally for a category (units, branches, or lines).
type Counts struct {
	Covered int
	Total   int
}

// Percent returns 100*Covered/Total, and 100.0 for an empty category so an
// all-text partial with no branches never drags a percentage down.
func (c Counts) Percent() float64 {
	if c.Total == 0 {
		return 100.0
	}
	return 100.0 * float64(c.Covered) / float64(c.Total)
}

// Region is one recorded region in a template: its position, role kind, and hit
// count. Branch tells a consumer whether it is a branch arm (as opposed to a
// unit) so a per-region breakdown can group arms under their branch point.
type Region struct {
	Line   int
	Col    int
	Kind   RegionKind
	Hits   int64
	Branch bool
}

// Covered reports whether the region fired at least once.
func (r Region) Covered() bool { return r.Hits > 0 }

// TemplateCoverage is the per-template rollup: its name, the three tallies, and
// the flat region list (sorted by line, col, kind) for a per-region breakdown.
type TemplateCoverage struct {
	Name     string
	Units    Counts
	Branches Counts
	Lines    Counts
	Regions  []Region
}

// Summary is a totals rollup across every template in a report.
type Summary struct {
	Units    Counts
	Branches Counts
	Lines    Counts
}

// Report is an immutable snapshot of accumulated coverage. It is produced by
// Collector.Report and never mutated by later renders.
type Report struct {
	// hits is the snapshot of every region's counter, keyed by region id. It is
	// kept so Merge can union two reports by id and the writers can re-derive the
	// per-template rollups.
	hits map[regionID]int64
	// sources is each template's raw text, keyed by name, for the HTML report. It
	// is carried through Merge so a merged report can still render source.
	sources map[string]string

	templates []TemplateCoverage
	totals    Summary
}

// buildReport turns a flat region->hits snapshot into a Report with per-template
// rollups computed. Templates are sorted by name; regions within a template are
// sorted by (line, col, kind). Lines are derived: a line is covered iff any unit
// on it is covered (branch arms do not create their own line entries -- their
// clause line is already a unit line via the enclosing @if/@for unit).
func buildReport(snap map[regionID]int64, sources map[string]string) *Report {
	byTmpl := map[string][]Region{}
	for id, hits := range snap {
		byTmpl[id.tmpl] = append(byTmpl[id.tmpl], Region{
			Line:   id.line,
			Col:    id.col,
			Kind:   id.kind,
			Hits:   hits,
			Branch: id.kind.isBranchArm(),
		})
	}

	names := make([]string, 0, len(byTmpl))
	for name := range byTmpl {
		names = append(names, name)
	}
	sort.Strings(names)

	if sources == nil {
		sources = map[string]string{}
	}
	r := &Report{hits: snap, sources: sources}
	for _, name := range names {
		regions := byTmpl[name]
		sort.Slice(regions, func(i, j int) bool {
			if regions[i].Line != regions[j].Line {
				return regions[i].Line < regions[j].Line
			}
			if regions[i].Col != regions[j].Col {
				return regions[i].Col < regions[j].Col
			}
			return regions[i].Kind < regions[j].Kind
		})
		tc := rollup(name, regions)
		r.templates = append(r.templates, tc)
		r.totals.Units.Covered += tc.Units.Covered
		r.totals.Units.Total += tc.Units.Total
		r.totals.Branches.Covered += tc.Branches.Covered
		r.totals.Branches.Total += tc.Branches.Total
		r.totals.Lines.Covered += tc.Lines.Covered
		r.totals.Lines.Total += tc.Lines.Total
	}
	return r
}

// rollup computes a template's unit/branch/line tallies from its sorted regions.
func rollup(name string, regions []Region) TemplateCoverage {
	tc := TemplateCoverage{Name: name, Regions: regions}
	// A line is covered iff some unit on it is covered; track per line whether a
	// unit exists and whether a unit on it fired.
	lineHasUnit := map[int]bool{}
	lineCovered := map[int]bool{}
	for _, reg := range regions {
		if reg.Branch {
			tc.Branches.Total++
			if reg.Covered() {
				tc.Branches.Covered++
			}
			continue
		}
		tc.Units.Total++
		lineHasUnit[reg.Line] = true
		if reg.Covered() {
			tc.Units.Covered++
			lineCovered[reg.Line] = true
		}
	}
	for line := range lineHasUnit {
		tc.Lines.Total++
		if lineCovered[line] {
			tc.Lines.Covered++
		}
	}
	return tc
}

// Templates returns the per-template coverage, sorted by name.
func (r *Report) Templates() []TemplateCoverage {
	if r == nil {
		return nil
	}
	return r.templates
}

// Totals returns the totals rollup across every template.
func (r *Report) Totals() Summary {
	if r == nil {
		return Summary{}
	}
	return r.totals
}

// Merge returns a new Report whose hit counts are the per-region union (sum) of r
// and other. Merging is by region id, so the same template measured in two
// reports combines correctly. Neither input is mutated.
func (r *Report) Merge(other *Report) *Report {
	return MergeReports(r, other)
}

// MergeReports unions any number of reports by region id, for parallel tests that
// each collect into their own Collector. Source text is carried through so a
// merged report can still render HTML. A nil or empty argument set yields an
// empty report.
func MergeReports(reports ...*Report) *Report {
	merged := map[regionID]int64{}
	sources := map[string]string{}
	for _, rep := range reports {
		for id, h := range mapOf(rep) {
			merged[id] += h
		}
		if rep != nil {
			for name, code := range rep.sources {
				sources[name] = code
			}
		}
	}
	return buildReport(merged, sources)
}

// mapOf returns a report's region->hits map, or an empty map for a nil report.
func mapOf(r *Report) map[regionID]int64 {
	if r == nil || r.hits == nil {
		return map[regionID]int64{}
	}
	return r.hits
}

// FailUnder returns an error when total unit coverage is below threshold percent,
// so a test or CI gate is one line. It returns nil when coverage meets or exceeds
// the threshold (and for an empty report, whose unit percent is 100).
func (r *Report) FailUnder(threshold float64) error {
	got := r.Totals().Units.Percent()
	if got < threshold {
		return fmt.Errorf("template unit coverage %.1f%% is below threshold %.1f%%",
			got, threshold)
	}
	return nil
}
