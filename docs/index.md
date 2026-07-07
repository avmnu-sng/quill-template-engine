# Quill

A general-purpose, gradually-typed, fast template engine for Go.

Quill is a template engine for Go that pairs Twig-class composition with things
nothing else in the Go template space combines: a gradual type system, a
compile-to-Go backend, native branch-aware coverage, a sandbox, streaming, and
byte-exact whitespace control. Use it for HTML pages, config files, emails,
program source, or any other text -- no use case is privileged.

The canonical API reference is
[pkg.go.dev](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine).

## Why Quill

- **Gradual type system.** Annotate as much or as little as you like; the
  checker catches undefined names, bad calls, and type mismatches at load time,
  before a byte is rendered, and untyped values still just work. Removing every
  annotation renders identical bytes -- types only move an error earlier in
  time. See [Types](types.md).
- **Compile-to-Go backend.** Templates compile to Go for the hot path, installed
  with `WithCompiled`, while the default path stays on the tree-walking
  interpreter. A compiled loop renders several times faster than the
  interpreter, and a tiny interpreted template already beats `text/template`.
  See [Performance](performance.md) for the methodology and numbers.
- **Native branch-aware coverage.** `quill cover` reports unit and branch
  coverage of your templates with text, LCOV, and HTML output and a `FailUnder`
  gate -- the analogue of `go tool cover`, for templates. See
  [Coverage](coverage.md).
- **Twig-class composition.** `extends`, `block`, `use`, `embed`, macros,
  includes, and slots -- real template inheritance and reuse, not string
  concatenation. See [Composition](guide/composition.md).
- **Whitespace control, byte-exact.** Three trim modes (hard, line, and a
  no-trim close), Jinja-style `trim_blocks`/`lstrip_blocks` cleanup applied by
  default so control statements do not leak blank lines, a `spaceless` filter
  and region, a `trim` filter, and a keep-close-newline pragma. See
  [Whitespace Control](whitespace.md).
- **Sandbox and streaming.** Run untrusted templates under a policy sandbox, and
  stream output to any `io.Writer` with `RenderTo` instead of buffering. See
  [Escaping & Safety](safety.md).
- **No escaping by default**, like Go `text/template`. Opt into HTML escaping
  with `WithAutoescapeHTML`, or apply one of six escape strategies per site.

## Install

Add the library to a module:

```
go get github.com/avmnu-sng/quill-template-engine
```

Install the command-line tool (renders templates, reports coverage, and
compiles templates to Go):

```
go install github.com/avmnu-sng/quill-template-engine/cmd/quill@latest
```

Quill requires Go 1.26 or newer and depends on nothing outside the Go standard
library.

## 30-second example

Build an `Environment` over a loader and render by name.

```go
package main

import (
	"fmt"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

func main() {
	env := quill.NewFromMap(map[string]string{
		"greet.quill": `Hello {{ name | upper }}{{ "!" if loud }}`,
	})
	out, err := env.Render("greet.quill", map[string]runtime.Value{
		"name": runtime.Str("ada"),
		"loud": runtime.Bool(true),
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(out) // Hello ADA!
}
```

To load templates from disk (so `@extends`, `@include`, and `@import` resolve by
name under a root), use a filesystem loader:

```go
env := quill.New(loader.NewFilesystemLoader("templates"))
```

See [Getting Started](getting-started.md) for loaders, passing Go data, and the
render options.

## Escaping

Output escaping is off by default, like Go `text/template`. Turn on HTML
escaping globally with `quill.WithAutoescapeHTML(true)`, or apply one of the six
escape strategies (`html`, `js`, `css`, `html_attr`, `html_attr_relaxed`, `url`)
locally with the `escape(strategy)`/`e` filter or an `@escape strategy { ... @}`
region. The full model is in [Escaping & Safety](safety.md).

## Use cases

Quill is general-purpose: no single output shape is privileged. It renders HTML
pages, configuration files, emails, and program source with the same engine --
pick escaping and whitespace settings per job.

## Coming from Jinja, Twig, or Go text/template

If you already know Jinja, Twig, or Go `text/template`, most of Quill will feel
familiar: interpolation, pipe filters, `if`/`for`, and template inheritance all
map across. The [Whitespace Control](whitespace.md) guide includes a side-by-side
mapping table so you can translate trim modifiers and block-cleanup behavior
directly.

## Documentation

- [Getting Started](getting-started.md) -- install, first render, loaders,
  passing Go data.
- Guide: [Templates](guide/templates.md), [Expressions](guide/expressions.md),
  [Control Flow](guide/control-flow.md), [Composition](guide/composition.md).
- [Types](types.md) -- the gradual type system.
- [Whitespace Control](whitespace.md), [Escaping & Safety](safety.md),
  [Coverage](coverage.md), [Performance](performance.md),
  [Comparison](comparison.md).
- [Standard Library](stdlib.md), [Extensions & Loaders](extensions.md),
  [CLI](cli.md), [API](api.md).
- Reference: [Language Reference](reference/language.md),
  [Grammar](reference/grammar.md).
- [Architecture](architecture.md), [Contributing](contributing.md),
  [Changelog](changelog.md).

The canonical API reference is
[pkg.go.dev](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine),
generated from the exported doc comments and runnable examples.

## License

Apache-2.0. See the [LICENSE](https://github.com/avmnu-sng/quill-template-engine/blob/main/LICENSE).
