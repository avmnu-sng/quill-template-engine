package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/cover"
	"github.com/avmnu-sng/quill-template-engine/internal/jsonval"
	"github.com/avmnu-sng/quill-template-engine/loader"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// coverCase is one template+data case in a -cases JSON file: a template name to
// render and the variable object to render it with. It mirrors the shape
// documented in docs/coverage.md Section 5.
type coverCase struct {
	Template string          `json:"template"`
	Data     json.RawMessage `json:"data"`
}

// runCover implements the "quill cover" subcommand: it renders one template
// (-data + positional name) or a JSON list of template+data cases (-cases)
// through an Environment with a coverage Collector attached, then writes the
// report to -o (or stdout) in the -format text|lcov|html. -fail-under makes it a
// CI gate: when total unit coverage is below the threshold it writes the
// uncovered-region breakdown to errOut and returns a non-zero error. It returns
// an error instead of calling os.Exit so a test can drive it directly.
func runCover(args []string, out, errOut io.Writer, stdin io.Reader) error {
	fs := flag.NewFlagSet("quill cover", flag.ContinueOnError)
	fs.SetOutput(errOut)
	root := fs.String("root", ".", "template root the loader resolves names under")
	dataPath := fs.String("data", "", `JSON data file for a single-template run ("-" reads stdin)`)
	casesPath := fs.String("cases", "", "JSON file of {template,data} cases; unions coverage across all")
	format := fs.String("format", "text", "report format: text (default), lcov, or html")
	outPath := fs.String("o", "", "output file for the report (default stdout)")
	failUnder := fs.Float64("fail-under", 0, "exit non-zero if total unit coverage percent is below N")
	// -threshold is an accepted alias for -fail-under.
	threshold := fs.Float64("threshold", 0, "alias for -fail-under")
	autoescape := fs.String("autoescape", "off", `output escaping strategy: "off" (default) or "html"`)
	strict := fs.Bool("strict", true, "strict-undefined handling; -strict=false enables lenient mode")
	fs.Usage = func() {
		fmt.Fprintf(errOut, "Usage: quill cover [flags] [<template-name>]\n\n"+
			"Renders one template (-data + name) or a JSON list of cases (-cases),\n"+
			"collects template coverage, and writes a text/lcov/html report.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	autoHTML, err := parseAutoescape(*autoescape)
	if err != nil {
		return err
	}

	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		return fmt.Errorf("resolve -root: %w", err)
	}

	cases, err := collectCases(fs.Args(), *casesPath, *dataPath, stdin)
	if err != nil {
		return err
	}

	coll := cover.NewCollector()
	env := quill.New(
		loader.NewFilesystemLoader(rootAbs),
		quill.WithAutoescapeHTML(autoHTML),
		quill.WithStrictVariables(*strict),
		quill.WithCoverage(coll),
	)
	for _, c := range cases {
		if _, err := env.Render(c.Template, c.Vars); err != nil {
			return fmt.Errorf("render %q: %w", c.Template, err)
		}
	}

	report := coll.Report()

	if err := writeReport(report, *format, *outPath, out); err != nil {
		return err
	}

	// -fail-under (or its -threshold alias) gates on total unit coverage. When
	// below the bar, print the uncovered-region breakdown to stderr so a failing
	// CI log shows exactly what to cover, then return a non-zero error.
	gate := *failUnder
	if *threshold > gate {
		gate = *threshold
	}
	if gate > 0 {
		if got := report.Totals().Units.Percent(); got < gate {
			_ = report.WriteTextVerbose(errOut)
			return fmt.Errorf("template unit coverage %.1f%% is below -fail-under %.1f%%", got, gate)
		}
	}
	return nil
}

// renderCase is one resolved render: a template name and its decoded variables.
type renderCase struct {
	Template string
	Vars     map[string]runtime.Value
}

// collectCases resolves the cover input into an ordered list of renders. Exactly
// one of -cases or a positional template name is used: -cases reads a JSON list
// of {template,data} objects (each unioned into one report), while a positional
// name renders once with -data (an empty object when -data is absent). Supplying
// both, or neither, is an error.
func collectCases(posArgs []string, casesPath, dataPath string, stdin io.Reader) ([]renderCase, error) {
	switch {
	case casesPath != "" && len(posArgs) > 0:
		return nil, fmt.Errorf("give either -cases or a template name, not both")
	case casesPath != "":
		return loadCases(casesPath, stdin)
	case len(posArgs) == 1:
		vars, err := loadVars(dataPath, stdin)
		if err != nil {
			return nil, err
		}
		return []renderCase{{Template: posArgs[0], Vars: vars}}, nil
	case len(posArgs) == 0:
		return nil, fmt.Errorf("expected a template name or -cases")
	default:
		return nil, fmt.Errorf("expected exactly one template name, got %d", len(posArgs))
	}
}

// loadCases reads a -cases JSON file (or stdin when path is "-") into ordered
// render cases. Each case's data object is decoded through the same jsonval
// bridge the render path uses, so keys and number kinds map identically.
func loadCases(path string, stdin io.Reader) ([]renderCase, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read -cases: %w", err)
	}
	var raw []coverCase
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse -cases %q: %w", path, err)
	}
	cases := make([]renderCase, 0, len(raw))
	for i, rc := range raw {
		if rc.Template == "" {
			return nil, fmt.Errorf("case %d: missing \"template\"", i)
		}
		vars := map[string]runtime.Value{}
		if len(rc.Data) > 0 && string(rc.Data) != "null" {
			vars, err = jsonval.DecodeMap(rc.Data)
			if err != nil {
				return nil, fmt.Errorf("case %d (%s): %w", i, rc.Template, err)
			}
		}
		cases = append(cases, renderCase{Template: rc.Template, Vars: vars})
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("-cases file has no cases")
	}
	return cases, nil
}

// parseAutoescape maps the -autoescape flag to the HTML-escaping boolean, so the
// coverage run instruments under the same output strategy the template ships
// with.
func parseAutoescape(mode string) (bool, error) {
	switch mode {
	case "off":
		return false, nil
	case "html":
		return true, nil
	default:
		return false, fmt.Errorf("unknown -autoescape %q (want \"off\" or \"html\")", mode)
	}
}

// writeReport writes the report in the chosen format to outPath (or out when
// outPath is empty). An unknown format is an error.
func writeReport(report *cover.Report, format, outPath string, out io.Writer) error {
	write := func(w io.Writer) error {
		switch format {
		case "text":
			return report.WriteText(w)
		case "lcov":
			return report.WriteLCOV(w)
		case "html":
			return report.WriteHTML(w)
		default:
			return fmt.Errorf("unknown -format %q (want text, lcov, or html)", format)
		}
	}
	if outPath == "" {
		return write(out)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create -o %q: %w", outPath, err)
	}
	if err := write(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
