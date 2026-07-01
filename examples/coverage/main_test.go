package main

import (
	"os"
	"strings"
	"testing"
)

// TestRender drives the example end to end: it renders both cases, so unit and
// branch coverage are 100% and FailUnder(100) passes. Asserting on substrings
// keeps the test robust to the report table's column padding.
func TestRender(t *testing.T) {
	// Render writes to a *os.File; use a temp file and read it back.
	f, err := os.CreateTemp(t.TempDir(), "cover-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := render(f); err != nil {
		t.Fatalf("render: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{"greet.ql", "5/5 100.0%", "2/2 100.0%", "TOTAL"} {
		if !strings.Contains(s, want) {
			t.Errorf("report missing %q:\n%s", want, s)
		}
	}
}
