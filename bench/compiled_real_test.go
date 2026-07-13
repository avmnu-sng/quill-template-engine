package quillbench

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
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
// compile backend on the same code path genloop.go uses (the quill CLI's
// compile subcommand) and returns the exact bytes the committed
// compiled_loop_gen.go must contain: the generated-file header followed by the
// CLI's generated source. It is driven by const quillLoop so a divergence
// between the committed generator template and the benchmark template surfaces
// as a staleness-guard failure.
func generateCompiledLoop() ([]byte, error) {
	dir, err := os.MkdirTemp("", "quill-genloop-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := os.WriteFile(filepath.Join(dir, "loop.ql"), []byte(quillLoop), 0o644); err != nil {
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

	out := make([]byte, 0, len(realGeneratedHeader)+stdout.Len())
	out = append(out, realGeneratedHeader...)
	out = append(out, stdout.Bytes()...)
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
	env := quill.NewFromMap(map[string]string{"loop.ql": quillLoop})
	want, err := env.Render(context.Background(), "loop.ql", map[string]runtime.Value{"users": quillUsers()})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := RenderLoop(context.Background(), &b, ext.Core(), map[string]runtime.Value{"users": quillUsers()}, nil); err != nil {
		t.Fatal(err)
	}
	if b.String() != want {
		t.Fatalf("compiled output differs from interpreter:\n compiled=%q\n interp  =%q", b.String(), want)
	}
}

// BenchmarkCompiledReal_Loop_Render times the real shipped compile backend: the
// committed render function emitted by compile.Module (the same unit
// quill.WithCompiled installs) across the same row counts the interpreter and
// text/template loop benchmarks render, so Quill's compiled path scales alongside
// them. The loop template does not use @cache, so a nil RenderCache is passed.
func BenchmarkCompiledReal_Loop_Render(b *testing.B) {
	exts := ext.Core()
	for _, n := range loopSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			vars := map[string]runtime.Value{"users": quillUsersN(n)}
			var buf bytes.Buffer
			if err := RenderLoop(context.Background(), &buf, exts, vars, nil); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(buf.Len()))
			b.ReportAllocs()
			for b.Loop() {
				if err := RenderLoop(context.Background(), io.Discard, exts, vars, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
