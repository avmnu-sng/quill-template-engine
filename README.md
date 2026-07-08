# Quill

A general-purpose, gradually-typed, fast template engine for Go.

[![Go Reference](https://pkg.go.dev/badge/github.com/avmnu-sng/quill-template-engine.svg)](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine)
[![CI](https://github.com/avmnu-sng/quill-template-engine/actions/workflows/ci.yml/badge.svg)](https://github.com/avmnu-sng/quill-template-engine/actions/workflows/ci.yml)
[![Coverage](https://codecov.io/gh/avmnu-sng/quill-template-engine/branch/main/graph/badge.svg)](https://codecov.io/gh/avmnu-sng/quill-template-engine)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-quill-blue)](https://avmnu-sng.github.io/quill-template-engine/)

Quill is a template engine for Go that pairs Twig-class composition with things
nothing else in the Go template space combines: a gradual type system, a
compile-to-Go backend, native branch-aware coverage, a sandbox, streaming, and
byte-exact whitespace control. Use it for HTML pages, config files, emails,
source, or any other text -- no use case is privileged.

**Full docs:** <https://avmnu-sng.github.io/quill-template-engine/> &middot;
**API reference:** <https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine>

## Contents

- [Why Quill](#why-quill)
- [Install](#install)
- [30-second example](#30-second-example)
- [Passing Go data](#passing-go-data)
- [Escaping](#escaping)
- [Use cases](#use-cases)
- [Coming from Jinja, Twig, or Go text/template](#coming-from-jinja-twig-or-go-texttemplate)
- [Editor support](#editor-support)
- [Documentation](#documentation)
- [Development](#development)
- [License](#license)

## Why Quill

- **Gradual type system.** Annotate as much or as little as you like; the
  checker catches undefined names, bad calls, and type mismatches at load time,
  before a byte is rendered, and untyped values still just work. Removing every
  annotation renders identical bytes -- types only move an error earlier in time.
- **Compile-to-Go backend.** Templates compile to Go for the hot path, installed
  with `WithCompiled` -- a compiled loop renders several times faster than the
  interpreter, while a tiny interpreted template already beats `text/template`.
  See the Performance guide for methodology and numbers.
- **Native branch-aware coverage.** `quill cover` reports unit and branch
  coverage of your templates with text, LCOV, and HTML output and a `FailUnder`
  gate -- the analogue of `go tool cover`, for templates.
- **Twig-class composition.** `extends`, `block`, `use`, `embed`, macros,
  includes, and slots -- real template inheritance and reuse, not string
  concatenation.
- **Whitespace control, byte-exact.** Three trim modes (hard, line, and a
  no-trim close), Jinja-style `trim_blocks`/`lstrip_blocks` cleanup applied by
  default so control statements do not leak blank lines, a `spaceless` filter and
  region, a `trim` filter, and a keep-close-newline pragma.
- **Sandbox and streaming.** Run untrusted templates under a policy sandbox, and
  stream output to any `io.Writer` with `RenderTo` instead of buffering.
- **No escaping by default**, like Go `text/template`. Opt into HTML escaping
  with `WithAutoescapeHTML`.

## Install

Add the library to a module:

```
go get github.com/avmnu-sng/quill-template-engine
```

Install the command-line tool (renders templates and reports coverage):

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
	env := quill.NewWithArray(map[string]string{
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

Loaders compose: `loader.NewChainLoader` tries several loaders in order,
`loader.NewPrefixLoader` routes by name prefix, `loader.NewFSLoader` serves an
`fs.FS` (including an `embed.FS` baked into the binary), and
`loader.NewFuncLoader` sources templates from a callback.

## Passing Go data

`Render` binds a `map[string]runtime.Value` built with the `runtime`
constructors (`runtime.Str`, `runtime.Int`, `runtime.Arr`, and friends). To pass
ordinary Go values instead, use `RenderValues`, which marshals each binding
through `runtime.FromGo`:

```go
type User struct {
	Name  string   `quill:"name"`
	Admin bool     `quill:"admin"`
	Tags  []string `quill:"tags"`
}

out, _ := env.RenderValues("greet.quill", map[string]any{
	"user":  User{Name: "ada", Admin: true, Tags: []string{"x", "y"}},
	"count": 3,
})
```

`FromGo` maps scalars, slices, maps (with a deterministic key order), and
structs (honoring a `quill:"name"` or `json:"name"` tag) to the value model, and
passes any existing `runtime.Value` through unchanged, so hand-built and native
bindings mix freely.

## Escaping

Output escaping is off by default, like Go `text/template`. Turn on HTML escaping
globally with `quill.WithAutoescapeHTML(true)`, or apply one of the six escape
strategies (`html`, `js`, `css`, `html_attr`, `html_attr_relaxed`, `url`)
locally with the `escape(strategy)`/`e` filter or an `@escape strategy { ... @}`
region. Strategies compose via a stack, and content produced under an active
strategy is marked safe so it is never escaped twice.

## Use cases

Quill is general-purpose: no single output shape is privileged. It renders HTML
pages, configuration files, emails, and program source with the same engine --
pick escaping and whitespace settings per job. See the docs for a worked example
of each.

## Coming from Jinja, Twig, or Go text/template

If you already know Jinja, Twig, or Go `text/template`, most of Quill will feel
familiar: interpolation, pipe filters, `if`/`for`, and template inheritance all
map across. The
[Whitespace Control](https://avmnu-sng.github.io/quill-template-engine/whitespace/)
guide includes a side-by-side mapping table so you can translate trim modifiers
and block-cleanup behavior directly.

## Editor support

The engine does not enforce a file extension -- template names are arbitrary
strings -- but the recommended convention for template files on disk is
`.quill`. It avoids a clash with CodeQL, whose `.ql` extension GitHub Linguist
claims by default. A VS Code extension with a TextMate grammar for Quill lives
in [`editors/vscode/`](editors/vscode/); see its README for local install
steps.

## Documentation

The full guide lives at
<https://avmnu-sng.github.io/quill-template-engine/> and covers getting started,
the language, the gradual type system, whitespace control, escaping and the
sandbox, coverage, performance, the standard library, and the architecture. The
canonical API reference is
[pkg.go.dev](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine).

Upgrading from v0.3.0? The [migration
guide](https://avmnu-sng.github.io/quill-template-engine/migration/) maps every
v1.0.0 breaking change from the old API to the new one.

## Development

Quill uses [go-task](https://taskfile.dev) as its build tool. Install it with
`go install github.com/go-task/task/v3/cmd/task@latest`, then run `task --list`
to see the targets. The common ones:

```
task build        # build all packages and the cmd/quill binary
task test         # go test ./...
task test:unit    # tests with the race detector and a coverage profile
task check:all    # gofmt, go vet, and go mod tidy checks
task lint:all     # golangci-lint + actionlint (needs task install:tools)
task ci           # the full pipeline: lint, checks, tests, and security scans
```

A thin `Makefile` forwards `make build`/`make test`/`make check` to the
equivalent `task` targets. The engine itself is standard-library only; the
linters and scanners are dev tooling. See
[CONTRIBUTING.md](.github/CONTRIBUTING.md) for the full workflow.

## License

Apache-2.0. See [LICENSE](LICENSE).
