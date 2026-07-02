package quillbench

import (
	"testing"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/interp"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// ---- Quill templates (the @-sigil source-emission form) ----

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

func quillUsers() runtime.Value {
	vals := make([]runtime.Value, loopN)
	for i := range vals {
		vals[i] = quillRecord("user", "user@example.com", true)
	}
	return runtime.Arr(runtime.NewList(vals...))
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
	for i := 0; i < b.N; i++ {
		env := quill.NewWithArray(map[string]string{"tiny.ql": quillTiny})
		if _, err := env.LoadTemplate("tiny.ql"); err != nil {
			b.Fatal(err)
		}
	}
}

// RENDER: template loaded once, only interp.Render is timed.
func BenchmarkQuill_Tiny_Render(b *testing.B) {
	env := quill.NewWithArray(map[string]string{"tiny.ql": quillTiny})
	tmpl, err := env.LoadTemplate("tiny.ql")
	if err != nil {
		b.Fatal(err)
	}
	vars := map[string]runtime.Value{"name": runtime.Str("ada")}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := interp.Render(env, tmpl, vars); err != nil {
			b.Fatal(err)
		}
	}
}

// ==== Workload B: loop over loopN records, upper filter per row ====

func BenchmarkQuill_Loop_Load(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		env := quill.NewWithArray(map[string]string{"loop.ql": quillLoop})
		if _, err := env.LoadTemplate("loop.ql"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQuill_Loop_Render(b *testing.B) {
	env := quill.NewWithArray(map[string]string{"loop.ql": quillLoop})
	tmpl, err := env.LoadTemplate("loop.ql")
	if err != nil {
		b.Fatal(err)
	}
	vars := map[string]runtime.Value{"users": quillUsers()}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := interp.Render(env, tmpl, vars); err != nil {
			b.Fatal(err)
		}
	}
}

// ==== Workload C: composition / inheritance (@extends + @block + parent) ====

func BenchmarkQuill_Compose_Load(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
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
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := interp.Render(env, tmpl, vars); err != nil {
			b.Fatal(err)
		}
	}
}
