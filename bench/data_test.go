package quillbench

// Shared benchmark data, expressed once per engine's native value model so that
// every engine renders the SAME logical input. The loop workload is exercised at
// several sizes (loopSizes) to reveal per-engine scaling; the single-size default
// used by the non-scaling workloads (Tiny/Compose) is loopN.

// loopN is the default row count for the non-size-parameterized loop workloads.
const loopN = 100

// loopSizes are the row counts the size-parameterized Loop render benchmarks and
// the fairness checks sweep over, so the sub-benchmarks reveal how each engine
// scales from a single row to a thousand.
var loopSizes = []int{1, 10, 100, 1000}

// record is the plain-Go shape used by text/template, html/template, and (via a
// map) the third-party peers.
type record struct {
	Name   string
	Email  string
	Active bool
}

// records builds n identical-shape records for the stdlib engines.
func records(n int) []record {
	rs := make([]record, n)
	for i := range rs {
		rs[i] = record{Name: "user", Email: "user@example.com", Active: true}
	}
	return rs
}

// goRecords builds loopN identical-shape records for the stdlib engines.
func goRecords() []record {
	return records(loopN)
}
