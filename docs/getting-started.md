# Getting Started

Quill is a general-purpose template engine for Go. This page takes you from
install to a first render, then covers loaders, passing Go data, and the render
options you will reach for most. It assumes no prior Quill knowledge; the
[Guide](guide/templates.md) covers the language itself.

## Install

Add the library to a module:

```
go get github.com/avmnu-sng/quill-template-engine
```

Optionally install the command-line tool, which renders a template with JSON
data, reports coverage, and compiles a template to Go:

```
go install github.com/avmnu-sng/quill-template-engine/cmd/quill@latest
```

Quill requires Go 1.26 or newer and depends on nothing outside the Go standard
library.

## Your first render

An `Environment` is the engine facade: you build one over a loader, then render
templates by name. The quickest loader is an in-memory map, via `NewFromMap`:

```go
package main

import (
	"context"
	"fmt"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

func main() {
	env := quill.NewFromMap(map[string]string{
		"greet.quill": `Hello {{ name | upper }}{{ "!" if loud }}`,
	})
	out, err := env.Render(context.Background(), "greet.quill", map[string]runtime.Value{
		"name": runtime.Str("ada"),
		"loud": runtime.Bool(true),
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(out) // Hello ADA!
}
```

`{{ name | upper }}` interpolates `name` through the `upper` filter, and
`{{ "!" if loud }}` is a postfix conditional that emits `!` only when `loud` is
truthy. Both are covered in the [Guide](guide/templates.md).

Every render and load method takes a `context.Context` as its first argument, so
a render honors cancellation and deadlines: pass a request's context and a caller
that goes away or a deadline that elapses aborts the render with the context's
error. Use `context.Background()` when you have no context to thread.

A template loads once and is memoized; subsequent renders of the same name reuse
the parsed and type-checked module.

## Loaders

A loader resolves a template name to its source. Loading templates from disk lets
`@extends`, `@include`, `@import`, and `@from` resolve other templates by name
under a root:

```go
import "github.com/avmnu-sng/quill-template-engine/pkg/loader"

env := quill.New(loader.NewFilesystemLoader("templates"))
```

Loaders compose, so you can assemble exactly the resolution strategy you need:

- `loader.NewChainLoader(a, b, ...)` tries several loaders in order and serves
  the first hit -- the base-plus-override pattern.
- `loader.NewPrefixLoader(routes)` routes a name by its leading prefix to a
  sub-loader.
- `loader.NewFSLoader(fsys, root...)` serves an `fs.FS`, including an `embed.FS`
  baked into the binary so a program ships its templates with no filesystem at
  runtime.
- `loader.NewFuncLoader(fn)` sources templates from a callback -- a database, a
  config object, or any lookup the host already owns.

The composable loaders are documented in full in
[Extensions & Loaders](extensions.md).

## Passing Go data

`Render` binds a `map[string]runtime.Value` built with the `runtime`
constructors (`runtime.Str`, `runtime.Int`, `runtime.Bool`, `runtime.Arr`, and
friends). To pass ordinary Go values instead, use `RenderValues`, which marshals
each binding through `runtime.FromGo`:

```go
type User struct {
	Name  string   `quill:"name"`
	Admin bool     `quill:"admin"`
	Tags  []string `quill:"tags"`
}

out, _ := env.RenderValues(context.Background(), "greet.quill", map[string]any{
	"user":  User{Name: "ada", Admin: true, Tags: []string{"x", "y"}},
	"count": 3,
})
```

`FromGo` maps scalars, slices, maps (with a deterministic key order), and structs
(honoring a `quill:"name"` or `json:"name"` tag) to the value model, and passes
any existing `runtime.Value` through unchanged, so hand-built and native bindings
mix freely.

## Streaming output

`Render` returns the result as a string. To stream to any `io.Writer` without
buffering the whole output, use `RenderTo`:

```go
err := env.RenderTo(context.Background(), os.Stdout, "greet.quill", vars)
```

`RenderStringTo` is the string-keyed variant. Streaming is covered alongside the
sandbox in [Escaping & Safety](safety.md).

## Render options

The `Environment` is configured with `Option` values passed to `New` /
`NewFromMap`. The ones you meet first:

- `quill.WithAutoescapeHTML(true)` -- turn on HTML escaping globally. Off by
  default; see [Escaping & Safety](safety.md).
- `quill.WithStrictVariables(false)` -- switch from the strict-undefined default
  to lenient mode, where a missing read becomes `Null`. Strict by default; see
  [Types](types.md).
- `quill.WithCoverage(collector)` -- attach a coverage collector; see
  [Coverage](coverage.md).
- `quill.WithExtensions(set)` / `quill.WithExtension(bundle)` -- register custom
  filters, functions, and tests; see [Extensions & Loaders](extensions.md).
- `quill.WithTabWidth(n)` -- the spaces one indent level expands to (default 4).

## Editor support

The engine does not enforce a file extension -- a template name is any string --
but the recommended convention for template files on disk is `.quill`. It sidesteps
a clash with CodeQL, whose `.ql` extension GitHub Linguist claims by default. A VS
Code extension with a Quill TextMate grammar lives under `editors/vscode/` in the
repository; its README covers local installation.

## Where to go next

- The [Guide](guide/templates.md) walks through the language: templates,
  expressions, control flow, and composition.
- [Types](types.md) introduces the gradual type system.
- [Whitespace Control](whitespace.md) covers byte-exact output and a mapping
  table for people coming from Jinja, Twig, or Go `text/template`.
- The [Language Reference](reference/language.md) and
  [Grammar](reference/grammar.md) are the exhaustive specification.
