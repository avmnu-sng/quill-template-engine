//go:build ignore

// Command genloop regenerates bench/compiled_loop_gen.go, the committed render
// function the real compile backend emits for the loop benchmark template.
//
// It writes the loop template (const quillLoop in quill_bench_test.go, mirrored
// here as genLoopTemplate) to a temp file and lowers it by invoking the quill
// CLI's "compile" subcommand, exactly as an integrator running the shipped tool
// would. bench's go.mod replaces the parent engine module, so `go run
// github.com/avmnu-sng/quill-template-engine/cmd/quill` resolves to the local
// source tree. The CLI's generated Go source is written into package quillbench
// so the benchmark links the actual shipped generated render function rather
// than a hand-written stand-in.
//
// Driving the compile backend through the CLI (rather than importing the now
// internal compile package directly) keeps the benchmark harness a plain
// consumer of the engine's public surface.
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
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// generate lowers the loop template through the real compile backend via the
// quill CLI and returns the committed file's exact bytes: the generated-file
// header followed by the CLI's generated source. TestCompiledLoopGenIsCurrent
// calls the equivalent code path so the staleness guard and the writer agree.
func generate() ([]byte, error) {
	out, err := compileLoopViaCLI(genLoopTemplate)
	if err != nil {
		return nil, err
	}
	src := make([]byte, 0, len(generatedHeader)+len(out))
	src = append(src, generatedHeader...)
	src = append(src, out...)
	return src, nil
}

// compileLoopViaCLI writes tmpl to a temp file named loop.ql and runs the quill
// CLI's compile subcommand over it, returning the generated Go source on stdout.
// The template file is named loop.ql so the source name the CLI feeds the
// compile backend matches the "loop.ql" name genloop.go's predecessor passed
// compile.Module directly, keeping error positions and the file header identical.
func compileLoopViaCLI(tmpl string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "quill-genloop-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := os.WriteFile(filepath.Join(dir, "loop.ql"), []byte(tmpl), 0o644); err != nil {
		return nil, fmt.Errorf("write loop template: %w", err)
	}

	cmd := exec.Command(
		"go", "run", "github.com/avmnu-sng/quill-template-engine/cmd/quill",
		"compile", "-root", dir, "-pkg", "quillbench", "-func", "RenderLoop", "loop.ql",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("quill compile: %w\n%s", err, stderr.String())
	}
	return stdout.Bytes(), nil
}
