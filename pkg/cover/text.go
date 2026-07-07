package cover

import (
	"fmt"
	"io"
	"sort"
)

// WriteText writes a human-readable per-template table plus a TOTAL row, with
// separate Units / Branches / Lines columns (covered/total and percent). It is
// the default report format for the CLI and a test artifact.
func (r *Report) WriteText(w io.Writer) error {
	bw := &errWriter{w: w}
	const nameW = 24
	header := fmt.Sprintf("%-*s %-14s %-14s %-14s\n", nameW, "Template", "Units", "Branches", "Lines")
	rule := fmt.Sprintf("%s %s %s %s\n", dashes(nameW), dashes(14), dashes(14), dashes(14))
	bw.writeString(header)
	bw.writeString(rule)
	for _, tc := range r.Templates() {
		bw.writeString(fmt.Sprintf("%-*s %-14s %-14s %-14s\n",
			nameW, clip(tc.Name, nameW),
			cell(tc.Units), cell(tc.Branches), cell(tc.Lines)))
	}
	bw.writeString(rule)
	tot := r.Totals()
	bw.writeString(fmt.Sprintf("%-*s %-14s %-14s %-14s\n",
		nameW, "TOTAL", cell(tot.Units), cell(tot.Branches), cell(tot.Lines)))
	return bw.err
}

// WriteTextVerbose writes the text summary followed by a per-region breakdown of
// every UNCOVERED region, grouped per template, so a developer can jump straight
// to a gap. Uncovered units list their kind; uncovered branch arms name the
// missing arm. This backs the CLI's -v and the -fail-under stderr breakdown.
func (r *Report) WriteTextVerbose(w io.Writer) error {
	if err := r.WriteText(w); err != nil {
		return err
	}
	bw := &errWriter{w: w}
	for _, tc := range r.Templates() {
		var gaps []Region
		for _, reg := range tc.Regions {
			if !reg.Covered() {
				gaps = append(gaps, reg)
			}
		}
		if len(gaps) == 0 {
			continue
		}
		bw.writeString("\n" + tc.Name + "\n")
		sort.SliceStable(gaps, func(i, j int) bool {
			if gaps[i].Line != gaps[j].Line {
				return gaps[i].Line < gaps[j].Line
			}
			return gaps[i].Col < gaps[j].Col
		})
		for _, g := range gaps {
			note := "never reached"
			if g.Branch {
				note = armNote(g.Kind)
			}
			bw.writeString(fmt.Sprintf("  %d:%-3d %-14s %s\n", g.Line, g.Col, g.Kind, note))
		}
	}
	return bw.err
}

// armNote describes what a missing branch arm means, for the verbose breakdown.
func armNote(k RegionKind) string {
	switch k {
	case IfThen:
		return "then arm never taken"
	case IfNotTaken:
		return "condition never false"
	case IfElse:
		return "else arm never taken"
	case ForBody:
		return "loop never ran non-empty"
	case ForEmpty:
		return "loop never ran empty"
	case TernThen:
		return "then arm never taken"
	case TernElse:
		return "else arm never taken"
	case ElvisLeft:
		return "left never kept"
	case ElvisRight:
		return "fallback never used"
	case CoalLeft:
		return "left never non-null"
	case CoalRight:
		return "fallback never used"
	case GuardYes:
		return "callable never present"
	case GuardNo:
		return "callable never absent"
	default:
		return "arm never taken"
	}
}

// cell formats a "covered/total  pp.p%" coverage cell.
func cell(c Counts) string {
	return fmt.Sprintf("%d/%d %.1f%%", c.Covered, c.Total, c.Percent())
}

// clip truncates a name to width, keeping it left-aligned in the column.
func clip(s string, width int) string {
	if len(s) <= width {
		return s
	}
	if width <= 1 {
		return s[:width]
	}
	return s[:width-1] + "~"
}

// dashes returns a run of n dash characters for a table rule.
func dashes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '-'
	}
	return string(b)
}

// errWriter accumulates the first write error so a sequence of writes needs a
// single error check at the end.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) writeString(s string) {
	if e.err != nil {
		return
	}
	_, e.err = io.WriteString(e.w, s)
}
