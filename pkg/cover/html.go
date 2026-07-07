package cover

import (
	"html/template"
	"io"
	"sort"
	"strings"
)

// WriteHTML writes a self-contained HTML report (stdlib html/template, no
// external assets) of the coverage snapshot (docs/coverage.md Section 4.3). An
// index lists every template sorted by unit coverage ascending (least covered
// first); each template shows its source with per-line hit counts, covered lines
// green, uncovered-unit lines red, and gutter branch markers (filled = all arms
// taken, half = partial, empty = never reached) whose title names any missing
// arm. Source flows through html/template, so it is auto-escaped and cannot break
// the page.
func (r *Report) WriteHTML(w io.Writer) error {
	page := htmlPage{Totals: r.Totals()}
	for _, tc := range r.Templates() {
		page.Templates = append(page.Templates, r.htmlTemplate(tc))
	}
	// Index order: least-covered first (ascending unit percent), then by name.
	sort.SliceStable(page.Templates, func(i, j int) bool {
		pi, pj := page.Templates[i].UnitPct, page.Templates[j].UnitPct
		if pi != pj {
			return pi < pj
		}
		return page.Templates[i].Name < page.Templates[j].Name
	})
	return htmlTmpl.Execute(w, page)
}

// htmlPage is the top-level view model.
type htmlPage struct {
	Totals    Summary
	Templates []htmlTemplateView
}

// htmlTemplateView is one template's rendered view: header percentages and its
// annotated source lines.
type htmlTemplateView struct {
	Name      string
	UnitPct   float64
	BranchPct float64
	LinePct   float64
	Units     Counts
	Branches  Counts
	Lines     Counts
	Rows      []htmlLine
}

// htmlLine is one source line: number, text, a coverage class, its hit count (for
// unit lines), and a branch marker describing the arms of any branch point on it.
type htmlLine struct {
	Num    int
	Text   string
	Class  string // "covered", "uncovered", or "neutral"
	HasHit bool
	Hits   int64
	Marker htmlMarker
}

// htmlMarker is the gutter branch marker for a line: its fill state and a title
// naming missing arms (empty when the line has no branch point).
type htmlMarker struct {
	Has   bool
	Fill  string // "full", "half", "empty"
	Title string
}

// htmlTemplate builds one template's view from its rollup and stored source.
func (r *Report) htmlTemplate(tc TemplateCoverage) htmlTemplateView {
	v := htmlTemplateView{
		Name:      tc.Name,
		UnitPct:   tc.Units.Percent(),
		BranchPct: tc.Branches.Percent(),
		LinePct:   tc.Lines.Percent(),
		Units:     tc.Units,
		Branches:  tc.Branches,
		Lines:     tc.Lines,
	}

	// Per-line unit hit rollup and covered flag.
	unitHits := map[int]int64{}
	unitOnLine := map[int]bool{}
	unitCovered := map[int]bool{}
	// Per-line branch arms for the gutter marker.
	branchArms := map[int][]Region{}
	for _, reg := range tc.Regions {
		if reg.Branch {
			branchArms[reg.Line] = append(branchArms[reg.Line], reg)
			continue
		}
		unitOnLine[reg.Line] = true
		unitHits[reg.Line] += reg.Hits
		if reg.Covered() {
			unitCovered[reg.Line] = true
		}
	}

	lines := splitLines(r.sources[tc.Name])
	for i, text := range lines {
		num := i + 1
		row := htmlLine{Num: num, Text: text, Class: "neutral"}
		if unitOnLine[num] {
			row.HasHit = true
			row.Hits = unitHits[num]
			if unitCovered[num] {
				row.Class = "covered"
			} else {
				row.Class = "uncovered"
			}
		}
		if arms, ok := branchArms[num]; ok {
			row.Marker = markerFor(arms)
		}
		v.Rows = append(v.Rows, row)
	}
	return v
}

// markerFor summarizes a line's branch arms into a gutter marker: full when every
// arm was taken, empty when none were, half otherwise, with a title listing the
// missing arms.
func markerFor(arms []Region) htmlMarker {
	sort.SliceStable(arms, func(i, j int) bool { return arms[i].Kind < arms[j].Kind })
	taken, total := 0, len(arms)
	var missing []string
	for _, a := range arms {
		if a.Covered() {
			taken++
		} else {
			missing = append(missing, armNote(a.Kind))
		}
	}
	m := htmlMarker{Has: true}
	switch taken {
	case total:
		m.Fill = "full"
		m.Title = "all arms taken"
	case 0:
		m.Fill = "empty"
		m.Title = "branch never reached: " + strings.Join(missing, "; ")
	default:
		m.Fill = "half"
		m.Title = "partial: " + strings.Join(missing, "; ")
	}
	return m
}

// splitLines splits source into lines without a trailing empty element for a
// final newline, so line numbers match the source's 1-based line count.
func splitLines(src string) []string {
	if src == "" {
		return nil
	}
	lines := strings.Split(src, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// htmlTmpl is the self-contained report template. Source text is rendered through
// html/template's contextual escaping, so it cannot inject markup.
var htmlTmpl = template.Must(template.New("cover").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>Quill Template Coverage</title>
<style>
body{font:14px/1.5 -apple-system,Segoe UI,Roboto,sans-serif;margin:2rem;color:#1a1a1a}
h1{font-size:1.4rem} h2{font-size:1.1rem;margin-top:2rem}
table.index{border-collapse:collapse;margin:1rem 0}
table.index th,table.index td{border:1px solid #ddd;padding:.3rem .6rem;text-align:left}
.src{border-collapse:collapse;font:12px/1.4 SFMono-Regular,Consolas,monospace;width:100%}
.src td{padding:0 .5rem;white-space:pre;vertical-align:top}
.num{color:#999;text-align:right;user-select:none}
.hits{color:#666;text-align:right;user-select:none}
.covered{background:#e6ffed} .uncovered{background:#ffeef0} .neutral{background:#fff}
.mark{width:1rem;text-align:center;user-select:none}
.full{color:#22863a} .half{color:#b08800} .empty{color:#cb2431}
</style></head><body>
<h1>Quill Template Coverage</h1>
<p>Total units {{.Totals.Units.Covered}}/{{.Totals.Units.Total}} ({{printf "%.1f" .Totals.Units.Percent}}%),
branches {{.Totals.Branches.Covered}}/{{.Totals.Branches.Total}} ({{printf "%.1f" .Totals.Branches.Percent}}%),
lines {{.Totals.Lines.Covered}}/{{.Totals.Lines.Total}} ({{printf "%.1f" .Totals.Lines.Percent}}%).</p>
<table class="index"><thead><tr><th>Template</th><th>Units</th><th>Branches</th><th>Lines</th></tr></thead><tbody>
{{range .Templates}}<tr>
<td><a href="#{{.Name}}">{{.Name}}</a></td>
<td>{{.Units.Covered}}/{{.Units.Total}} ({{printf "%.1f" .UnitPct}}%)</td>
<td>{{.Branches.Covered}}/{{.Branches.Total}} ({{printf "%.1f" .BranchPct}}%)</td>
<td>{{.Lines.Covered}}/{{.Lines.Total}} ({{printf "%.1f" .LinePct}}%)</td>
</tr>{{end}}
</tbody></table>
{{range .Templates}}
<h2 id="{{.Name}}">{{.Name}}</h2>
<table class="src"><tbody>
{{range .Rows}}<tr class="{{.Class}}">
<td class="num">{{.Num}}</td>
<td class="mark {{.Marker.Fill}}"{{if .Marker.Has}} title="{{.Marker.Title}}"{{end}}>{{if .Marker.Has}}{{if eq .Marker.Fill "full"}}&#9679;{{else if eq .Marker.Fill "half"}}&#9680;{{else}}&#9675;{{end}}{{end}}</td>
<td class="hits">{{if .HasHit}}{{.Hits}}{{end}}</td>
<td>{{.Text}}</td>
</tr>{{end}}
</tbody></table>
{{end}}
</body></html>
`))
