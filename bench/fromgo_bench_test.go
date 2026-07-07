package quillbench

import (
	"testing"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// hostRecord is the plain-Go row shape a Values-API host hands to RenderValues
// without touching the runtime.Value constructors. The tags map the exported
// fields onto the same lowercase keys the hand-built quillRecord uses, so the
// FromGo workloads render byte-identical output to the Loop workload.
type hostRecord struct {
	Name   string `quill:"name"`
	Email  string `quill:"email"`
	Active bool   `quill:"active"`
}

// hostRecords builds loopN identical native rows, mirroring goRecords for the
// stdlib engines and quillUsers for the hand-built Quill path.
func hostRecords() []hostRecord {
	rs := make([]hostRecord, loopN)
	for i := range rs {
		rs[i] = hostRecord{Name: "user", Email: "user@example.com", Active: true}
	}
	return rs
}

// ==== Workload E: FromGo marshal of loopN native rows ====

// The marshal alone, isolated from any render: this is the per-call tax every
// naive Values-API host pays before a single output byte exists.
func BenchmarkQuill_FromGo_Marshal(b *testing.B) {
	rows := hostRecords()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := runtime.FromGo(rows); err != nil {
			b.Fatal(err)
		}
	}
}

// ==== Workload F: RenderValues host loop (marshal + render per call) ====

// The end-to-end Values-API pattern: the template is loaded once (warming the
// prepared-template cache), then every iteration marshals the native rows
// through FromGo and renders the Loop workload from the result.
func BenchmarkQuill_RenderValues_Loop(b *testing.B) {
	env := quill.NewWithArray(map[string]string{"loop.ql": quillLoop})
	if _, err := env.LoadTemplate("loop.ql"); err != nil {
		b.Fatal(err)
	}
	vars := map[string]any{"users": hostRecords()}
	out, err := env.RenderValues("loop.ql", vars)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for b.Loop() {
		if sink, err = env.RenderValues("loop.ql", vars); err != nil {
			b.Fatal(err)
		}
	}
}
