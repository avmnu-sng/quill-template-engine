// Command quill renders a Quill template from disk with JSON data, measures
// template coverage, and compiles a template to a Go render function.
//
// Usage:
//
//	quill -root templates -data data.json index.ql
//	quill -root templates -autoescape html page.ql > page.html
//	cat data.json | quill -root templates -data - index.ql
//	quill cover -root templates -data data.json page.ql
//	quill cover -root templates -cases cases.json -format html -o cover.html
//	quill compile -root templates -pkg qtpl -o index_gen.go index.ql
//
// The named template is resolved by a filesystem loader rooted at -root, so an
// @extends parent, an @include target, and an @import/@from source all resolve
// by name under the same root. Variables come from a JSON object read from the
// -data file (or stdin when -data is "-"); with no -data flag the template
// renders against an empty variable set. The rendered output is written to
// stdout; any load, parse, or render error is reported to stderr with a non-zero
// exit status.
//
// The "cover" subcommand renders one template (or a JSON list of template+data
// cases) with a coverage Collector attached and writes a text, LCOV, or HTML
// report; -fail-under makes it a CI gate (see docs/coverage.md Section 5).
//
// The "compile" subcommand lowers one template through the compile backend and
// writes the generated Go source: a render function plus the exported manifest
// quill.WithCompiled installs for by-name dispatch. A construct outside the
// compilable subset is reported as a not-compilable error naming the construct.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/internal/jsonval"
	"github.com/avmnu-sng/quill-template-engine/pkg/loader"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

func main() {
	if err := dispatch(os.Args[1:], os.Stdout, os.Stderr, os.Stdin); err != nil {
		fmt.Fprintf(os.Stderr, "quill: %v\n", err)
		os.Exit(1)
	}
}

// dispatch routes to a subcommand ("cover" or "compile"); anything else is the
// default render path, so the plain "quill <template>" invocation and all its
// flags keep working unchanged.
func dispatch(args []string, out, errOut io.Writer, stdin io.Reader) error {
	if len(args) > 0 && args[0] == "cover" {
		return runCover(args[1:], out, errOut, stdin)
	}
	if len(args) > 0 && args[0] == "compile" {
		return runCompile(args[1:], out)
	}
	return run(args, out, stdin)
}

// run is the testable entry point: it parses args, loads the template, and
// writes the render to out. It returns an error instead of calling os.Exit so a
// test can drive it directly.
func run(args []string, out io.Writer, stdin io.Reader) error {
	fs := flag.NewFlagSet("quill", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", ".", "template root directory the loader resolves names under")
	dataPath := fs.String("data", "", `JSON file with the render variables (an object); "-" reads stdin`)
	autoescape := fs.String("autoescape", "off", `output escaping strategy: "off" (default, source emission) or "html"`)
	strict := fs.Bool("strict", true, "strict-undefined handling; -strict=false enables lenient migration mode")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: quill [flags] <template-name>\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Fprintf(out, "quill %s\n", quill.Version)
		return nil
	}

	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("expected exactly one template name, got %d", fs.NArg())
	}
	name := fs.Arg(0)

	autoHTML, err := parseAutoescape(*autoescape)
	if err != nil {
		return err
	}

	vars, err := loadVars(*dataPath, stdin)
	if err != nil {
		return err
	}

	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		return fmt.Errorf("resolve -root: %w", err)
	}
	env := quill.New(
		loader.NewFilesystemLoader(rootAbs),
		quill.WithAutoescapeHTML(autoHTML),
		quill.WithStrictVariables(*strict),
	)

	rendered, err := env.Render(context.Background(), name, vars)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, rendered)
	return err
}

// loadVars reads the JSON data file into the variable map. An empty path means
// no data (an empty map); "-" reads stdin.
func loadVars(path string, stdin io.Reader) (map[string]runtime.Value, error) {
	if path == "" {
		return map[string]runtime.Value{}, nil
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}
	vars, err := jsonval.DecodeMap(data)
	if err != nil {
		return nil, fmt.Errorf("parse data %q: %w", path, err)
	}
	return vars, nil
}
