# Quill

Quill is a Go-native, gradually-typed template language and engine. It is built
for generating exact text -- especially program source code -- with the
ergonomics of a modern language and the safety of optional static types.

> Status: the full language is implemented -- the lexer, parser, tree-walking
> renderer, the complete standard library, composition (inheritance, blocks,
> macros, includes, embeds, traits), the escaping/safety subsystem and sandbox,
> and the gradual type checker. The language specification is complete. The API
> is not yet stable.

## Why Quill

- **Go-native syntax.** Brace-delimited, keyword-led, pipe filters, arrow
  functions, a Pratt-parsed expression language. No PHP heritage.
- **Gradually typed.** Type annotations are optional and enforced by a static
  checker that runs at template load, before a single byte is emitted. With no
  annotations Quill is a fully dynamic template language; with them the checker
  rejects whole classes of error -- a string/number mismatch, rendering a
  collection, looping over a non-iterable, a missing member or method, a call
  with the wrong arity or argument types -- and narrows unions through `is`-tests
  and null-safe access. Annotations never change runtime behavior: they only move
  an error earlier in time, so removing every annotation renders identical bytes.
- **Built to emit source code.** Output escaping is off by default (you are
  usually emitting code, not HTML), and the lexer keeps literal braces in
  generated code unambiguous from template control flow.
- **Predictable semantics.** One typed equality, one ordering, one truthiness
  rule, strict-by-default undefined handling, byte-exact rendering. No silent
  coercions.
- **Full composition.** Template inheritance, blocks, macros, includes, embeds,
  and traits.

## A taste

Source-emitting templates use the explicit `@` statement form, so literal `{`
and `}` in generated code are always literal output:

```
@extends "base.ql"

@block body {
  @for u in users {
    {{ u.name | upper }}{{ ", admin" if u.isAdmin }}
  @}
@}

@macro greet(name) {
  Hello {{ name | default("guest") }}
@}
```

A bare-brace form (no `@`, blocks closed by `}`) is available for markup-style
templates where literal braces are rare.

## Using the library

Build an `Environment` over a loader and render by name. Output escaping is off
by default and undefined variables are strict by default; both are options.

```go
package main

import (
	"fmt"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

func main() {
	env := quill.NewWithArray(map[string]string{
		"greet.ql": `Hello {{ name | upper }}{{ "!" if loud }}`,
	})
	out, err := env.Render("greet.ql", map[string]runtime.Value{
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

Options: `quill.WithAutoescapeHTML(true)` switches the output strategy to HTML
escaping; `quill.WithStrictVariables(false)` enables the lenient migration mode
(an undefined read becomes empty instead of an error); `quill.WithTypes(reg)`
installs a host static-typing registry (`check.Registry`) so the checker can
verify annotations that reference host `Object<"Name">` types and host callable
signatures. The checker always enforces the annotations a template carries; the
registry only adds the host-type and host-signature knowledge it cannot infer.

## Command line

The `quill` command renders a template from a directory with data from a JSON
file:

```
go install github.com/avmnu-sng/quill-template-engine/cmd/quill@latest

quill -root templates -data data.json index.ql
quill -root templates -autoescape html page.ql > page.html
echo '{"name":"ada"}' | quill -root templates -data - greet.ql
```

Flags: `-root` (template directory, default `.`), `-data` (JSON object of
variables; `-` reads stdin), `-autoescape` (`off` or `html`), `-strict`
(strict-undefined handling, default on).

## Template coverage

Quill measures which parts of a `.ql` template your renders actually exercised:
which statements and interpolations ran (**units**), and which arms of each
branch were taken (**branches**) -- `@if`/`@elseif`/`@else`, `@for` looped vs
empty, ternary/elvis/coalesce, postfix `if`/`unless`, and `@guard`. It is the
analogue of `go tool cover` for templates, aggregated across many renders and
exported as a text summary, LCOV `.info`, or a highlighted HTML report. Coverage
is opt-in and zero-overhead when off: an Environment with no collector pays no
per-node cost, and a template renders byte-identically with or without it.

Enable it with a `cover.Collector` and the `WithCoverage` option, render your
fixtures through the Environment, then take a `Report`:

```go
coll := cover.NewCollector()
env := quill.New(loader.NewFilesystemLoader("templates"),
    quill.WithCoverage(coll))

_, _ = env.Render("page.ql", adminVars)    // covers the @if admin then-arm
_, _ = env.Render("page.ql", guestVars)    // covers the else-arm

report := coll.Report()
_ = report.WriteText(os.Stdout)             // or WriteLCOV / WriteHTML
if err := report.FailUnder(90.0); err != nil {
    log.Fatal(err)                          // one-line CI/test gate on unit %
}
```

In a test, build one Environment with a Collector, render every fixture through
it, write an LCOV artifact for CI, and assert a threshold with
`report.FailUnder(...)` (or compare `report.Totals().Units.Percent()`). For
`t.Parallel()` fixtures give each goroutine its own Collector and combine them
with `cover.MergeReports(...)`.

The `quill cover` subcommand is the command-line front door to the same API. It
renders a single template or a JSON list of `{template, data}` cases, unions
coverage across them, and writes the report:

```
quill cover -root templates -data data.json page.ql
quill cover -root templates -data data.json -format lcov -o coverage.info page.ql
quill cover -root templates -cases cases.json -format html -o cover.html
```

`-format` is `text` (default), `lcov`, or `html`; `-o` is the output file
(default stdout). `-fail-under N` (alias `-threshold N`) makes it a CI gate: it
exits non-zero when total unit coverage is below `N`, printing the
uncovered-region breakdown to stderr. The full model, the LCOV/HTML formats, and
the seeding boundary are documented in [`docs/coverage.md`](docs/coverage.md).

## Examples

Runnable examples live in [`examples/`](examples/):

- [`codegen`](examples/codegen) -- emit a Go struct (literal braces pass through
  because escaping is off and the `@` form keeps `{`/`}` literal).
- [`inheritance`](examples/inheritance) -- `@extends`, `@block`, and `parent()`.
- [`filters`](examples/filters) -- pipe filters, postfix-if, and loop metadata.
- [`coverage`](examples/coverage) -- collect template coverage across two data
  cases and assert a threshold with `FailUnder`.

```
go run ./examples/codegen
```

## Conformance

The engine's behavior is pinned by a fixture suite under
[`testdata/conformance`](testdata/conformance): each case is a self-contained
`template.ql` + `data.json` + `expected.out` triple, and a table test renders
each and diffs the bytes. The fixtures cover interpolation, pipe filters,
postfix-if, the `@for`/`@if`/`@set` statements, `@extends`/`@block`/`parent()`,
`@macro`/`@import`, `@include`, whitespace control, escaping off vs html, and the
de-PHP-ified semantics (typed equality, truthiness, byte-exact `ToText`).

## Documentation

The language specification lives in [`docs/`](docs/):

- Overview and design philosophy
- Language reference and formal grammar
- Standard library (filters, functions, tests)
- Types and runtime semantics

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
task ci           # the full pipeline lint + checks + tests + security
```

A thin `Makefile` forwards `make build`/`make test`/`make check` to the
equivalent `task` targets. The engine itself is standard-library only; the
linters and scanners under `task install:tools` are dev tooling.

## License

Apache-2.0. See [LICENSE](LICENSE).
