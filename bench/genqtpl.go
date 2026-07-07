//go:build ignore

// Command genqtpl regenerates bench/qtpl/bench.qtpl.go, the qtc-compiled render
// functions for the quicktemplate peer benchmark, and guards the result behind
// the "thirdparty" build tag.
//
// quicktemplate's idiomatic fast path is qtc-generated Go. qtc itself emits an
// untagged file, but this harness must keep the DEFAULT (offline) build free of
// every third-party engine, so the generated file cannot be compiled unless the
// "thirdparty" tag is set. This command runs qtc over the qtpl/ directory and
// then prepends a "//go:build thirdparty" constraint (plus a marker line so the
// staleness guard can strip it back off) to the generated file.
//
// Regenerate after any change to qtpl/bench.qtpl with:
//
//	cd bench && go generate ./...
//
// or directly:
//
//	cd bench && go run genqtpl.go
//
// TestQuicktemplateGenIsCurrent fails when the committed file drifts from this
// command's output, so a template change that is not regenerated is caught by
// the bench module's own test run (under the thirdparty tag).
package main

import (
	"bytes"
	"os"
	"os/exec"
)

// qtplDir holds the .qtpl source and the generated *.qtpl.go.
const qtplDir = "qtpl"

// qtplOutputPath is the committed generated file this command tags and writes.
const qtplOutputPath = "qtpl/bench.qtpl.go"

// thirdpartyConstraint is prepended to the qtc output so the generated file only
// compiles under the "thirdparty" build tag, keeping the default build free of
// quicktemplate. TestQuicktemplateGenIsCurrent reproduces this exact prefix.
const thirdpartyConstraint = "//go:build thirdparty\n\n"

func main() {
	src, err := generate()
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(qtplOutputPath, src, 0o644); err != nil {
		panic(err)
	}
}

// generate runs qtc over the qtpl directory and returns the committed file's
// exact bytes: the thirdparty build constraint followed by qtc's output.
// TestQuicktemplateGenIsCurrent calls this same function so the staleness guard
// and the writer share one code path.
func generate() ([]byte, error) {
	cmd := exec.Command("go", "run", "github.com/valyala/quicktemplate/qtc", "-dir="+qtplDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(qtplOutputPath)
	if err != nil {
		return nil, err
	}
	// qtc emits an untagged file; if a previous run already tagged it, strip the
	// old constraint before re-adding so the output is idempotent.
	raw = bytes.TrimPrefix(raw, []byte(thirdpartyConstraint))
	out := make([]byte, 0, len(thirdpartyConstraint)+len(raw))
	out = append(out, thirdpartyConstraint...)
	out = append(out, raw...)
	return out, nil
}
