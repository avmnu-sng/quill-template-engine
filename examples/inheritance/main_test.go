package main

func Example() {
	if err := render(); err != nil {
		panic(err)
	}
	// Output:
	// # Daily Report
	//
	// (no summary)
	//
	// A short report with 3 items.
	// - ship release
	// - triage issues
	// - review PRs
}
