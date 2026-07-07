package quillbench

import (
	"fmt"
	"testing"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/interp"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// sink keeps the last rendered string reachable so the compiler cannot elide the
// render call in a b.Loop body whose result would otherwise be dead. Every render
// benchmark assigns its output here after computing the byte count for SetBytes.
var sink string

// ---- Quill templates (the @-sigil statement form) ----

const quillTiny = `Hello {{ name | upper }}!`

const quillLoop = `@for u in users {
{{ loop.index }}. {{ u.name | upper }} <{{ u.email }}>
@}`

const quillBase = `# {{ title }}

@block summary {
(no summary)
@}
@block items {
(no items)
@}
`

const quillPage = `@extends "base.ql"
@block summary {
{{ parent() }}
A short report with {{ items | length }} items.
@}
@block items {
@for it in items {
- {{ it }}
@}
@}
`

// quillRecord builds one map-shaped record as a runtime.Value.
func quillRecord(name, email string, active bool) runtime.Value {
	r := runtime.NewArray()
	r.SetStr("name", runtime.Str(name))
	r.SetStr("email", runtime.Str(email))
	r.SetStr("active", runtime.Bool(active))
	return runtime.Arr(r)
}

// quillUsersN builds n map-shaped records as a runtime list value.
func quillUsersN(n int) runtime.Value {
	vals := make([]runtime.Value, n)
	for i := range vals {
		vals[i] = quillRecord("user", "user@example.com", true)
	}
	return runtime.Arr(runtime.NewList(vals...))
}

func quillUsers() runtime.Value {
	return quillUsersN(loopN)
}

func quillItems() runtime.Value {
	vals := make([]runtime.Value, loopN)
	for i := range vals {
		vals[i] = runtime.Str("item")
	}
	return runtime.Arr(runtime.NewList(vals...))
}

// ==== Workload A: tiny single interpolation ====

// LOAD: fresh Environment (fresh parse cache) + LoadTemplate, so parse + gradual
// type-check happen on every iteration.
func BenchmarkQuill_Tiny_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		env := quill.NewWithArray(map[string]string{"tiny.ql": quillTiny})
		if _, err := env.LoadTemplate("tiny.ql"); err != nil {
			b.Fatal(err)
		}
	}
}

// RENDER: template loaded once, only interp.Render is timed. The output is
// rendered once before the loop to size SetBytes (so ns/op is complemented by
// MB/s) and to keep the result reachable via sink against dead-code elimination.
func BenchmarkQuill_Tiny_Render(b *testing.B) {
	env := quill.NewWithArray(map[string]string{"tiny.ql": quillTiny})
	tmpl, err := env.LoadTemplate("tiny.ql")
	if err != nil {
		b.Fatal(err)
	}
	vars := map[string]runtime.Value{"name": runtime.Str("ada")}
	out, err := interp.Render(env, tmpl, vars)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for b.Loop() {
		if sink, err = interp.Render(env, tmpl, vars); err != nil {
			b.Fatal(err)
		}
	}
}

// ==== Workload B: loop over loopN records, upper filter per row ====

func BenchmarkQuill_Loop_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		env := quill.NewWithArray(map[string]string{"loop.ql": quillLoop})
		if _, err := env.LoadTemplate("loop.ql"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQuill_Loop_Render sweeps the loop row count so per-engine scaling is
// visible. Each sub-benchmark renders once before the loop to size SetBytes and
// pins the result to sink against dead-code elimination.
func BenchmarkQuill_Loop_Render(b *testing.B) {
	env := quill.NewWithArray(map[string]string{"loop.ql": quillLoop})
	tmpl, err := env.LoadTemplate("loop.ql")
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range loopSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			vars := map[string]runtime.Value{"users": quillUsersN(n)}
			out, err := interp.Render(env, tmpl, vars)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			for b.Loop() {
				if sink, err = interp.Render(env, tmpl, vars); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ==== Workload B2: @for whose body @includes a partial reading the caller loop ====

// quillLoopInclude is the G3 static-@include workload: a @for that inlines a
// partial reading the caller's loop, the shape the compiled path must
// materialize the enclosing loop for (an inline loop would splice a null loop
// into the partial). It fixes the loop optimizer's escape analysis for the
// include boundary and records the interpreter cost the compiled path targets.
const quillLoopInclude = `@for u in users {
@include "row.ql"
@}`

const quillLoopIncludeRow = `{{ loop.index }}. {{ u.name | upper }} <{{ u.email }}>
`

func BenchmarkQuill_LoopInclude_Render(b *testing.B) {
	env := quill.NewWithArray(map[string]string{
		"loopinc.ql": quillLoopInclude,
		"row.ql":     quillLoopIncludeRow,
	})
	tmpl, err := env.LoadTemplate("loopinc.ql")
	if err != nil {
		b.Fatal(err)
	}
	vars := map[string]runtime.Value{"users": quillUsers()}
	out, err := interp.Render(env, tmpl, vars)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for b.Loop() {
		if sink, err = interp.Render(env, tmpl, vars); err != nil {
			b.Fatal(err)
		}
	}
}

// ==== Workload C: composition / inheritance (@extends + @block + parent) ====

func BenchmarkQuill_Compose_Load(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		env := quill.NewWithArray(map[string]string{
			"base.ql": quillBase,
			"page.ql": quillPage,
		})
		// Load the child; @extends causes the parent to load through the engine.
		if _, err := env.LoadTemplate("page.ql"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQuill_Compose_Render(b *testing.B) {
	env := quill.NewWithArray(map[string]string{
		"base.ql": quillBase,
		"page.ql": quillPage,
	})
	tmpl, err := env.LoadTemplate("page.ql")
	if err != nil {
		b.Fatal(err)
	}
	vars := map[string]runtime.Value{
		"title": runtime.Str("Daily Report"),
		"items": quillItems(),
	}
	out, err := interp.Render(env, tmpl, vars)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for b.Loop() {
		if sink, err = interp.Render(env, tmpl, vars); err != nil {
			b.Fatal(err)
		}
	}
}
