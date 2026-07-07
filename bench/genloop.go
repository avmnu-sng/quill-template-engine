//go:build ignore

// Command genloop regenerates bench/compiled_loop_gen.go, the committed render
// function the real compile backend emits for the loop benchmark template.
//
// It parses the same loop template the interpreter and text/template loop
// benchmarks render (const quillLoop in quill_bench_test.go), lowers it with
// compile.Module exactly as quill.WithCompiled would, and writes the formatted
// Go source into package quillbench so the benchmark links the actual shipped
// generated render function rather than a hand-written stand-in.
//
// Regenerate after any change to the compile backend or the loop template with:
//
//	cd bench && go generate ./...
//
// or directly:
//
//	cd bench && go run genloop.go
//
// TestCompiledLoopGenIsCurrent fails when the committed file drifts from the
// backend's current output, so a compiler change that is not regenerated here
// is caught by the bench module's own test run.
package main

import (
	"os"

	"github.com/avmnu-sng/quill-template-engine/pkg/compile"
	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// genLoopTemplate is the loop benchmark template. It is kept byte-identical to
// const quillLoop in quill_bench_test.go; TestCompiledLoopGenIsCurrent asserts
// the two agree by regenerating from quillLoop and comparing the result to the
// committed file.
const genLoopTemplate = "@for u in users {\n{{ loop.index }}. {{ u.name | upper }} <{{ u.email }}>\n@}"

// genLoopOutputPath is the committed generated file this command writes.
const genLoopOutputPath = "compiled_loop_gen.go"

// generatedHeader is prepended to the compile backend's output. It marks the
// file as generated and records how to regenerate it, so a reader never
// hand-edits the committed generated file.
const generatedHeader = "// Code emitted by the Quill compile backend and committed for the benchmark harness.\n" +
	"//\n" +
	"// DO NOT EDIT: regenerate with `cd bench && go generate ./...` (or `go run\n" +
	"// genloop.go`) after any change to the compile backend or the loop template.\n" +
	"// TestCompiledLoopGenIsCurrent guards this file against drift.\n" +
	"//\n" +
	"//go:generate go run genloop.go\n\n"

func main() {
	src, err := generate()
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(genLoopOutputPath, src, 0o644); err != nil {
		panic(err)
	}
}

// generate lowers the loop template through the real compile backend and
// returns the committed file's exact bytes: the generated-file header followed
// by the compile.Module output. TestCompiledLoopGenIsCurrent calls this same
// function so the staleness guard and the writer share one code path.
func generate() ([]byte, error) {
	mod, err := parse.Parse(source.New("loop.ql", genLoopTemplate))
	if err != nil {
		return nil, err
	}
	res, err := compile.Module("loop.ql", mod, compile.Options{
		PackageName: "quillbench",
		FuncName:    "RenderLoop",
	})
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(generatedHeader)+len(res.Source))
	out = append(out, generatedHeader...)
	out = append(out, res.Source...)
	return out, nil
}
