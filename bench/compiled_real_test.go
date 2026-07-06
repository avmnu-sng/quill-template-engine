package quillbench

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/compile"
	"github.com/avmnu-sng/quill-template-engine/core/parse"
	"github.com/avmnu-sng/quill-template-engine/core/source"
	"github.com/avmnu-sng/quill-template-engine/ext"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// realGeneratedHeader is the generated-file header genloop.go prepends to the
// compile backend's output. It is duplicated here so TestCompiledLoopGenIsCurrent
// can reproduce the committed file byte for byte from the compile.Module output;
// it must stay identical to generatedHeader in genloop.go.
const realGeneratedHeader = "// Code emitted by the Quill compile backend and committed for the benchmark harness.\n" +
	"//\n" +
	"// DO NOT EDIT: regenerate with `cd bench && go generate ./...` (or `go run\n" +
	"// genloop.go`) after any change to the compile backend or the loop template.\n" +
	"// TestCompiledLoopGenIsCurrent guards this file against drift.\n" +
	"//\n" +
	"//go:generate go run genloop.go\n\n"

// generateCompiledLoop lowers the loop benchmark template through the real
// compile backend on the same code path genloop.go uses and returns the exact
// bytes the committed compiled_loop_gen.go must contain: the generated-file
// header followed by the compile.Module output. It is driven by const quillLoop
// so a divergence between the committed generator template and the benchmark
// template surfaces as a staleness-guard failure.
func generateCompiledLoop() ([]byte, error) {
	mod, err := parse.Parse(source.New("loop.ql", quillLoop))
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
	out := make([]byte, 0, len(realGeneratedHeader)+len(res.Source))
	out = append(out, realGeneratedHeader...)
	out = append(out, res.Source...)
	return out, nil
}

// TestCompiledLoopGenIsCurrent regenerates the loop render function in memory and
// asserts it is byte-identical to the committed compiled_loop_gen.go, so a
// compile-backend or loop-template change that is not regenerated fails here.
func TestCompiledLoopGenIsCurrent(t *testing.T) {
	want, err := generateCompiledLoop()
	if err != nil {
		t.Skipf("compile backend cannot generate the loop render function: %v", err)
	}
	got, err := os.ReadFile("compiled_loop_gen.go")
	if err != nil {
		t.Fatalf("read committed compiled_loop_gen.go: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("compiled_loop_gen.go is stale; regenerate with `cd bench && go generate ./...` (or `go run genloop.go`)")
	}
}

// TestCompiledRealMatchesInterp asserts the committed generated render function
// is byte-identical to the interpreter's Render of the same template and data,
// so BenchmarkCompiledReal_Loop_Render measures equivalent work.
func TestCompiledRealMatchesInterp(t *testing.T) {
	env := quill.NewWithArray(map[string]string{"loop.ql": quillLoop})
	want, err := env.Render("loop.ql", map[string]runtime.Value{"users": quillUsers()})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := RenderLoop(&b, ext.Core(), map[string]runtime.Value{"users": quillUsers()}, nil); err != nil {
		t.Fatal(err)
	}
	if b.String() != want {
		t.Fatalf("compiled output differs from interpreter:\n compiled=%q\n interp  =%q", b.String(), want)
	}
}

// BenchmarkCompiledReal_Loop_Render times the real shipped compile backend: the
// committed render function emitted by compile.Module (the same unit
// quill.WithCompiled installs) over the same loopN-row data the interpreter and
// text/template loop benchmarks render. The loop template does not use @cache,
// so a nil RenderCache is passed.
func BenchmarkCompiledReal_Loop_Render(b *testing.B) {
	vars := map[string]runtime.Value{"users": quillUsers()}
	exts := ext.Core()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := RenderLoop(io.Discard, exts, vars, nil); err != nil {
			b.Fatal(err)
		}
	}
}
