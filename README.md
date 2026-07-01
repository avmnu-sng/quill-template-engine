# Quill

Quill is a Go-native, gradually-typed template language and engine. It is built
for generating exact text -- especially program source code -- with the
ergonomics of a modern language and the safety of optional static types.

- **Go-native syntax.** Brace-delimited, keyword-led, pipe filters, arrow
  functions, a Pratt-parsed expression language.
- **Gradually typed.** Type annotations are optional and enforced by a static
  checker that runs at template load, before a single byte is emitted. Annotations
  never change runtime behavior: they only move an error earlier in time, so
  removing every annotation renders identical bytes.
- **Built to emit source code.** Output escaping is off by default (you are
  usually emitting code, not markup), and literal braces in generated code stay
  unambiguous from template control flow.
- **Predictable semantics.** One typed equality, one ordering, one truthiness
  rule, strict-by-default undefined handling, byte-exact rendering. No silent
  coercions.
- **Standard-library-only runtime.** The engine depends on nothing outside the Go
  standard library.

> Status: the language is complete -- the lexer, parser, tree-walking renderer,
> the full standard library, composition (inheritance, blocks, macros, includes,
> embeds, traits), the escaping/safety subsystem and sandbox, and the gradual type
> checker. The API is not yet stable.

## Install

```
go get github.com/avmnu-sng/quill-template-engine
go install github.com/avmnu-sng/quill-template-engine/cmd/quill@latest
```

## Quick start

Build an `Environment` over a loader and render by name. Output escaping is off by
default and undefined variables are strict by default; both are options.

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

Loaders compose. `loader.NewChainLoader` tries several loaders in order and
serves the first hit, so a host layers project overrides over shipped defaults;
`loader.NewPrefixLoader` routes a name by its leading prefix to a sub-loader;
`loader.NewFSLoader` serves templates from an `fs.FS`, including an `embed.FS`
baked into the binary; and `loader.NewFuncLoader` sources templates from a
callback the host already owns. See [docs/extensions.md](docs/extensions.md#10-composable-loaders)
for the full reference.

Options: `quill.WithAutoescapeHTML(true)` switches the output strategy to HTML
escaping; `quill.WithStrictVariables(false)` enables the lenient mode (an
undefined read becomes empty instead of an error); `quill.WithTypes(reg)` installs
a host static-typing registry; `quill.WithSandboxPolicy`/`WithSandboxActive` gate
a render; `quill.WithCoverage(coll)` measures template coverage;
`quill.WithTabWidth(n)` sets the spaces-per-indent-level width (default 4) for
the `tab` filter, the `tab`/`space`/`break` functions, and the `@tab` region;
`quill.WithLogger(l)` sets the destination `@log` writes to (default discards);
`quill.WithExtensions`/`quill.WithExtension` add host callables.

## Passing data

`Render` binds a `map[string]runtime.Value`, so you build values with the
`runtime` constructors (`runtime.Str`, `runtime.Int`, `runtime.Arr`, ...). To
pass ordinary Go values instead, use `RenderValues` (and `RenderStringValues`
for an ad-hoc body), which marshal each binding through `runtime.FromGo`:

```go
type User struct {
	Name  string   `quill:"name"`
	Admin bool     `quill:"admin"`
	Tags  []string `quill:"tags"`
}

out, _ := env.RenderValues("greet.ql", map[string]any{
	"user":  User{Name: "ada", Admin: true, Tags: []string{"x", "y"}},
	"count": 3,
})
```

`runtime.FromGo(any) (runtime.Value, error)` maps native Go values to the value
model:

- scalars (`string`, `bool`, every signed and unsigned integer within the int64
  range, `float32`/`float64`, `nil`) become the matching scalar Value;
- a slice or array becomes a list-shaped array in element order;
- a map becomes an array with a deterministic key order: a string-keyed map
  sorts by its string keys, and an integer-keyed map sorts numerically by key
  value, so a dense `0..n-1` int map is list-shaped and iterates in ascending
  order;
- a struct becomes a mapping of its exported fields in declaration order,
  honoring a `quill:"name"` tag (and, absent that, a `json:"name"` tag); a field
  tagged `-` is omitted and an embedded struct is flattened in place;
- a pointer or interface is followed to its element, and an existing
  `runtime.Value`, `*runtime.Array`, or `Object` passes through unchanged at any
  depth -- including as a concretely-typed struct field -- so hand-built and
  native bindings mix freely.

An unsupported kind (a channel, a bare function, a complex number) returns a
clear typed error naming the offending Go kind, and the render emits nothing.

## The language at a glance

Source-emitting templates use the explicit `@` statement form: each statement
leads with `@`, each block closes with `@}`, so a bare `{` or `}` anywhere in
template text is unconditionally literal output.

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
templates where literal braces are rare, under `pragma bare`.

### Statements

- `@for x in seq { ... @}` -- iterate, with always-defined `loop` metadata
  (`loop.index`, `loop.first`, `loop.last`, `loop.length`, `loop.prev`,
  `loop.next`, and the `loop.changed(expr)` method); an optional
  `@else { ... @}` arm runs when the sequence is empty. A fused filter
  `@for x in seq if cond { ... @}` iterates only the elements for which `cond`
  is truthy, and every `loop.*` field counts only those survivors. A
  `@for node in tree recursive { ... loop(node.children) ... @}` descends a
  tree, binding a `loop(children)` callable and `loop.depth` / `loop.depth0`; a
  fused `if` filter on the recursive form prunes a node and its subtree at every
  level, and its body is a pure emitter (a body `set` of an outer name does not
  write back).
- `@if cond { ... @} @elseif other { ... @} @else { ... @}` -- conditionals.
- `@set x = expr` -- bind a variable; `@set x: int = expr` annotates it; the
  target may destructure a list (`@set [a, b, ...rest] = xs`) or a map
  (`@set {name, id: uid} = user`); `@set x = capture { ... @}` captures a block.
- `@with { a: 1, b: 2 } { ... @}` -- push a scoped set of bindings over a body.
- `@do expr` -- evaluate an expression for its effect, emitting nothing.
- `@log expr` -- evaluate `expr` and write its text to the host logger; no
  rendered output, but a coverable unit (a comment `{# ... #}` is neither).
- `@tab(n) { ... @}` -- indent the entire rendered body by `n` levels
  (cumulative when nested); one level is `WithTabWidth` spaces (default 4).
- `@guard`, `@apply`, `@escape`, `@sandbox`, `@cache`, `@verbatim`, `@types` --
  the region and directive statements (see the language reference).

### Expressions

A full Pratt-parsed expression language: arithmetic and comparison, one typed
`==`, the `<=>` spaceship, `and`/`or`/`not`, the `~` string concat, the `..`
range, the `matches` regex operator, and member/index access.

- **Pipe filters:** `x | upper | join(", ")` -- `x | name(args)` is
  `name(x, args)`.
- **Arrow functions:** `users | filter(u => u.active) | map(u => u.name)`.
- **Postfix conditional:** `{{ ", admin" if u.isAdmin }}` prints only when the
  condition holds; `{{ x unless done }}` is its negation.
- **Fallback operators:** `a ?? b` (coalesce on null/undefined), `a ?: b` (elvis
  on falsy), `a.b?.c` (null-safe access), `x | default("guest")`.

### Composition

- `@extends "base.ql"` -- single-parent inheritance.
- `@block name { ... @}` -- an overridable region; `parent()` re-emits the
  parent's block body inside an override.
- `@macro f(a, b = 1, ...rest, **opts) { ... @}` -- a parameterized output
  function; `...rest` collects excess positional args into a list and `**opts`
  collects excess named args into a mapping.
- `@import "macros.ql" as m` / `@from "macros.ql" import greet` -- bring macros
  into scope.
- `@use "traits.ql"` -- horizontal trait reuse, merging a traitable template's
  blocks below the using template's own (`with { block: alias }` renames).
- `@embed "widget.ql" { @block slot { ... @} @}` -- include a template while
  overriding its blocks.
- `@include "part.ql"` -- render another template inline; the function form
  `include("part.ql", with = {...}, only = true, sandboxed = true)` controls the
  child's context and sandboxing.
- `@call name(args) { ... @}` -- invoke a macro with a `caller()` callback that
  renders the block; `@call(p) name(args) { ... @}` passes a value from the macro
  back into the block via `caller(v)`.
- `@provide label { ... @}` / `@yield label` -- accumulate rendered content from
  many sites into a named slot and emit it once (`slot(label)` is the value form),
  the complement of the overriding `@block`. Contributions from `@include`d and
  `@embed`ded partials feed the enclosing render's slots, so a shell `@yield`
  collects what its body partials `@provide`.

The full reference is [`docs/01-language-reference.md`](docs/01-language-reference.md);
the formal grammar is [`docs/02-grammar.md`](docs/02-grammar.md).

## Standard library

Quill ships a complete built-in library of filters, functions, and tests, all
`snake_case`-named:

- **String:** `upper`, `lower`, `title`, `capitalize`, `trim`, `replace`,
  `split`, `join`, `slice`, `length`, `url_encode`, `nl2br`, the text-shaping
  filters `wrap`/`truncate`/`center`/`wordcount`, the source-emission helpers
  `indent`/`tab`/`ucfirst`, and the escapers `escape`/`e`/`raw`.
- **Collection:** `first`, `last`, `keys`, `reverse`, `sort`, `merge`, `batch`,
  `columns` (the transpose of `batch`), `column`, `entries`, `sort_map`, the
  higher-order `map`/`filter`/`reduce`/`find` (arrow-driven), the
  attribute-projecting `map`/`sum`/`unique` (`attribute: "path"`), the named-test
  and truthiness `select`/`reject`/`selectattr`/`rejectattr`, `group_by`, plus the
  `has some` / `has every` quantifiers.
- **Number/format:** `abs`, `round`, `number_format`, `format`, `date`.
- **Functions:** `range`, `min`, `max`, `random`, `cycle`, `constant`, `enum`,
  `enum_cases`, `include`, `source`, `template_from_string`, `dump`, the
  indentation emitters `space`/`break`/`tab`, and the reference values
  `separator` (trailing-separator-free joining) and `cell` (a mutable accumulator
  that survives a loop body).
- **Tests:** `is defined`, `is empty`, `is even`/`odd`, `is iterable`,
  `is constant`, `is divisible by`, the comparison tests
  `is eq`/`ne`/`lt`/`le`/`gt`/`ge`, the scalar-kind tests
  `is string`/`number`/`int`/`float`/`bool`/`callable`, the registry-existence
  tests `is filter`/`function`/`test` (the inline form of `@guard`), and the
  type tests.

The full catalogue with signatures is
[`docs/03-stdlib.md`](docs/03-stdlib.md).

## Gradual types

Annotations are optional. With none, Quill is a fully dynamic template language
backed by a strict runtime. With them, a static checker runs at load and rejects
whole classes of error before a byte is emitted -- a string/number mismatch,
rendering a collection, looping over a non-iterable, a missing member or method, a
call with the wrong arity or argument types -- and narrows unions through `is`
tests and null-safe access.

```
@types { users: list<Object<"User">> }

@for u in users {
  {{ u.name | upper }}   {# checked against the User shape #}
@}
```

`Object<"Name">` member shapes and host callable signatures come from an optional
host registry (`check.Registry`, installed via `quill.WithTypes`). Annotations
never change runtime behavior: removing every one renders identical bytes. See
[`docs/04-types-and-semantics.md`](docs/04-types-and-semantics.md).

## Escaping and the sandbox

Output escaping is **off by default** -- the source-emission default. Turn on HTML
escaping globally with `quill.WithAutoescapeHTML(true)`, or apply any of the six
strategies (`html`, `js`, `css`, `html_attr`, `html_attr_relaxed`, `url`) locally
with the `escape(strategy)`/`e` filter or an `@escape strategy { ... @}` region.
Strategies compose via a stack, and captures/macros/blocks under an active
strategy yield a `Safe` value that is not escaped twice.

The **sandbox** restricts what a template may do against a host-supplied
`sandbox.Policy`: the permitted tags, filters, functions, per-type methods, and
per-type properties. Allowlisting is uniform with no grandfathering -- a custom
callable is gated exactly like a built-in. Activate it globally
(`quill.WithSandboxActive`), per `@sandbox` region, or per sandboxed include. Each
violation raises a host-catchable `*errors.Security`. See
[`docs/04-types-and-semantics.md`](docs/04-types-and-semantics.md) Section 8.

## Template coverage

Quill measures which parts of a `.ql` template your renders actually exercised:
which statements and interpolations ran (**units**), and which arms of each branch
were taken (**branches**) -- `@if`/`@elseif`/`@else`, `@for` looped vs empty,
ternary/elvis/coalesce, postfix `if`/`unless`, and `@guard`. It is the analogue of
`go tool cover` for templates, aggregated across many renders and exported as a
text summary, LCOV `.info`, or a highlighted HTML report. Coverage is opt-in and
zero-overhead when off: a template renders byte-identically with or without it.

```go
coll := cover.NewCollector()
env := quill.New(loader.NewFilesystemLoader("templates"),
	quill.WithCoverage(coll))

_, _ = env.Render("page.ql", adminVars) // covers the @if admin then-arm
_, _ = env.Render("page.ql", guestVars) // covers the else-arm

report := coll.Report()
_ = report.WriteText(os.Stdout) // or WriteLCOV / WriteHTML
if err := report.FailUnder(90.0); err != nil {
	log.Fatal(err) // one-line CI/test gate on unit %
}
```

For `t.Parallel()` fixtures give each goroutine its own Collector and combine them
with `cover.MergeReports(...)`. The full model, the LCOV/HTML formats, and the
seeding boundary are documented in [`docs/coverage.md`](docs/coverage.md).

## Extensibility

A host adds its own filters, functions, and tests -- and its own constants and
enumerations -- through the `ext` package. The typed helpers
`ext.NewFilter`/`ext.NewFunction`/`ext.NewTest` wrap a plain Go function: the
signature is inspected once at registration, and `runtime.Value` arguments and
results marshal through it, so the body never touches a `runtime.Value`.

```go
package main

import (
	"fmt"
	"strings"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/ext"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

func main() {
	set := ext.NewExtensionSet()
	set.AddFilter(ext.NewFilter("repeat", func(s string, n int64) string {
		return strings.Repeat(s, int(n))
	}))
	set.AddFunction(ext.NewFunction("clamp", func(x, lo, hi int64) int64 {
		switch {
		case x < lo:
			return lo
		case x > hi:
			return hi
		default:
			return x
		}
	}))

	env := quill.NewWithArray(
		map[string]string{"demo.ql": `{{ "ab" | repeat(3) }} {{ clamp(42, 0, 10) }}`},
		quill.WithExtensions(set),
	)
	out, _ := env.Render("demo.ql", map[string]runtime.Value(nil))
	fmt.Println(out) // ababab 10
}
```

Ship a cohesive group of callables as an `ext.Extension` bundle (embed
`ext.BaseExtension` and override the families you provide), register it with
`quill.WithExtension`, and compose several layers -- a later host layer shadows an
earlier one, and every host layer shadows core. Custom callables interact with the
sandbox (allowlist by name) and the type checker (register a `check.Signature`)
exactly like built-ins. The full API -- the value structs, the typed helpers, the
`Extension` interface, `Merge`/`Register` and shadow order, the
`NeedsEnvironment`/`NeedsContext`/`NeedsCharset` injection flags, and the sandbox
and type-checker interaction -- is in [`docs/extensions.md`](docs/extensions.md).

## Command line

The `quill` command renders a template from a directory with data from a JSON
file:

```
quill -root templates -data data.json index.ql
quill -root templates -autoescape html page.ql > page.html
echo '{"name":"ada"}' | quill -root templates -data - greet.ql
```

Flags: `-root` (template directory, default `.`), `-data` (JSON object of
variables; `-` reads stdin), `-autoescape` (`off` or `html`), `-strict`
(strict-undefined handling, default on).

The `quill cover` subcommand is the command-line front door to the coverage API.
It renders a single template or a JSON list of `{template, data}` cases, unions
coverage across them, and writes the report:

```
quill cover -root templates -data data.json page.ql
quill cover -root templates -data data.json -format lcov -o coverage.info page.ql
quill cover -root templates -cases cases.json -format html -o cover.html
```

`-format` is `text` (default), `lcov`, or `html`; `-o` is the output file
(default stdout). `-fail-under N` (alias `-threshold N`) makes it a CI gate: it
exits non-zero when total unit coverage is below `N`.

## Examples

Runnable examples live in [`examples/`](examples/):

- [`codegen`](examples/codegen) -- emit a Go struct (literal braces pass through
  because escaping is off and the `@` form keeps `{`/`}` literal).
- [`inheritance`](examples/inheritance) -- `@extends`, `@block`, and `parent()`.
- [`filters`](examples/filters) -- pipe filters, postfix-if, and loop metadata.
- [`extension`](examples/extension) -- register a custom filter and function via
  the typed helpers and render with them.
- [`coverage`](examples/coverage) -- collect template coverage across two data
  cases and assert a threshold with `FailUnder`.

```
go run ./examples/codegen
```

## Conformance

The engine's behavior is pinned by a fixture suite under
[`testdata/conformance`](testdata/conformance): each case is a self-contained
`template.ql` + `data.json` + `expected.out` triple, and a table test renders each
and diffs the bytes. The fixtures cover interpolation, pipe filters, postfix-if,
the statements, `@extends`/`@block`/`parent()`, `@macro`/`@import`, `@include`,
`@use` traits, destructuring, whitespace control, escaping off vs html, the
escape strategies and regions, the sandbox, the type-checker denials, and the
value semantics (typed equality, truthiness, byte-exact `ToText`).

## Documentation

The specification lives in [`docs/`](docs/):

- [Overview and design philosophy](docs/00-overview.md)
- [Language reference](docs/01-language-reference.md) and
  [formal grammar](docs/02-grammar.md)
- [Standard library](docs/03-stdlib.md) (filters, functions, tests)
- [Types and runtime semantics](docs/04-types-and-semantics.md)
- [Extensions](docs/extensions.md) (custom callables and bundles)
- [Coverage](docs/coverage.md)
- [Architecture](docs/06-architecture-and-roadmap.md)

## Development

Quill uses [go-task](https://taskfile.dev) as its build tool. Install it with
`go install github.com/go-task/task/v3/cmd/task@latest`, then run `task --list` to
see the targets. The common ones:

```
task build        # build all packages and the cmd/quill binary
task test         # go test ./...
task test:unit    # tests with the race detector and a coverage profile
task check:all    # gofmt, go vet, and go mod tidy checks
task lint:all     # golangci-lint + actionlint (needs task install:tools)
task ci           # the full pipeline: lint + checks + tests + security
```

A thin `Makefile` forwards `make build`/`make test`/`make check` to the
equivalent `task` targets. The engine itself is standard-library only; the linters
and scanners under `task install:tools` are dev tooling.

## License

Apache-2.0. See [LICENSE](LICENSE).
