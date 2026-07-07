// Command coverage shows template coverage: it renders one template across two
// data cases through an Environment with a cover.Collector attached, then writes
// a text report and asserts a unit-coverage threshold. Rendering both the admin
// and non-admin cases takes both arms of the @if, so the branch is fully
// covered. Run it with:
//
//	go run ./examples/coverage
//
// See docs/coverage.md for the full model and the LCOV/HTML report formats, and
// `quill cover` for the command-line front door to the same Collector/Report API.
package main

import (
	"fmt"
	"os"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/cover"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// tmpl has an @if/@else branch plus an interpolation, so exercising only one arm
// leaves a visible gap and exercising both fully covers the branch.
const tmpl = `@if admin {
{{ name }} (admin)
@} @else {
{{ name }}
@}
`

func main() {
	if err := render(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "coverage:", err)
		os.Exit(1)
	}
}

// render builds one Environment with a shared Collector, renders the template
// for an admin and a non-admin user (covering both @if arms), then writes the
// text report and enforces a 100% unit-coverage threshold via FailUnder.
func render(out *os.File) error {
	coll := cover.NewCollector()
	env := quill.NewWithArray(
		map[string]string{"greet.quill": tmpl},
		quill.WithCoverage(coll),
	)

	cases := []map[string]runtime.Value{
		{"admin": runtime.Bool(true), "name": runtime.Str("ada")},
		{"admin": runtime.Bool(false), "name": runtime.Str("bob")},
	}
	for _, vars := range cases {
		if _, err := env.Render("greet.quill", vars); err != nil {
			return err
		}
	}

	report := coll.Report()
	if err := report.WriteText(out); err != nil {
		return err
	}
	// FailUnder is the one-line CI/test gate: it returns an error when total unit
	// coverage is below the threshold. Both arms ran, so this passes.
	if err := report.FailUnder(100.0); err != nil {
		return err
	}
	return nil
}
