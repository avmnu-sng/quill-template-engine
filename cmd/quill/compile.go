package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/avmnu-sng/quill-template-engine/compile"
	"github.com/avmnu-sng/quill-template-engine/core/parse"
	"github.com/avmnu-sng/quill-template-engine/loader"
)

// runCompile implements the "compile" subcommand: it loads one template by
// name through a filesystem loader rooted at -root, lowers it with the compile
// backend, and writes the generated Go source (render function plus dispatch
// manifest) to -o or stdout. The option flags mirror the Environment knobs the
// generated unit's fingerprint captures, so a unit compiled here dispatches on
// an Environment configured the same way.
func runCompile(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("quill compile", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", ".", "template root directory the loader resolves names under")
	pkg := fs.String("pkg", "qtpl", "package clause of the generated file")
	funcName := fs.String("func", "Render", "name of the generated render function")
	autoescape := fs.String("autoescape", "off", `output escaping strategy: "off" (default, source emission) or "html"`)
	strict := fs.Bool("strict", true, "strict-undefined handling; -strict=false compiles the lenient migration mode")
	tabWidth := fs.Int("tabwidth", 4, "spaces one indent level expands to (the WithTabWidth knob)")
	seed := fs.Int64("seed", 0, "fixed seed for the randomness callables; omit the flag for the time-seeded default")
	outPath := fs.String("o", "", "output file for the generated source; default stdout")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: quill compile [flags] <template-name>\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
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
	// A -seed of zero is a deliberate seed, distinct from the unseeded default,
	// so seed-set is "the flag was passed" rather than "the value is non-zero".
	seedSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "seed" {
			seedSet = true
		}
	})

	rootAbs, err := filepath.Abs(*root)
	if err != nil {
		return fmt.Errorf("resolve -root: %w", err)
	}
	src, err := loader.NewFilesystemLoader(rootAbs).Get(name)
	if err != nil {
		return err
	}
	mod, err := parse.Parse(src)
	if err != nil {
		return err
	}
	res, err := compile.Module(name, mod, compile.Options{
		PackageName:      *pkg,
		FuncName:         *funcName,
		AutoescapeHTML:   autoHTML,
		LenientVariables: !*strict,
		TabWidth:         *tabWidth,
		RandomSeed:       *seed,
		RandomSeedSet:    seedSet,
	})
	if err != nil {
		return err
	}
	if *outPath == "" {
		_, err = out.Write(res.Source)
		return err
	}
	return os.WriteFile(*outPath, res.Source, 0o644)
}
