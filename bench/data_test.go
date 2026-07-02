package quillbench

// Shared benchmark data, expressed once per engine's native value model so that
// every engine renders the SAME logical input. The record count for the loop
// workload is fixed at loopN.

const loopN = 100

// record is the plain-Go shape used by text/template, html/template, and (via a
// map) the third-party peers.
type record struct {
	Name   string
	Email  string
	Active bool
}

// goRecords builds loopN identical-shape records for the stdlib engines.
func goRecords() []record {
	rs := make([]record, loopN)
	for i := range rs {
		rs[i] = record{Name: "user", Email: "user@example.com", Active: true}
	}
	return rs
}
