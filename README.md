# Quill

Quill is a Go-native, gradually-typed template language and engine. It is built
for generating exact text -- especially program source code -- with the
ergonomics of a modern language and the safety of optional static types.

> Status: early development. The language specification is complete and a
> working tree-walking engine implements its core. The API is not yet stable.

## Why Quill

- **Go-native syntax.** Brace-delimited, keyword-led, pipe filters, arrow
  functions, a Pratt-parsed expression language. No PHP heritage.
- **Gradually typed.** Type annotations are optional. With none, Quill is a
  dynamic template language. With them, a static checker rejects whole classes
  of error before a single byte is emitted. Annotations never change runtime
  behavior -- they only move an error earlier.
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

	quill "github.com/avmnusng/quill-template-engine"
	"github.com/avmnusng/quill-template-engine/runtime"
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
(an undefined read becomes empty instead of an error).

## Command line

The `quill` command renders a template from a directory with data from a JSON
file:

```
go install github.com/avmnusng/quill-template-engine/cmd/quill@latest

quill -root templates -data data.json index.ql
quill -root templates -autoescape html page.ql > page.html
echo '{"name":"ada"}' | quill -root templates -data - greet.ql
```

Flags: `-root` (template directory, default `.`), `-data` (JSON object of
variables; `-` reads stdin), `-autoescape` (`off` or `html`), `-strict`
(strict-undefined handling, default on).

## Examples

Runnable examples live in [`examples/`](examples/):

- [`codegen`](examples/codegen) -- emit a Go struct (literal braces pass through
  because escaping is off and the `@` form keeps `{`/`}` literal).
- [`inheritance`](examples/inheritance) -- `@extends`, `@block`, and `parent()`.
- [`filters`](examples/filters) -- pipe filters, postfix-if, and loop metadata.

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

## License

Apache-2.0. See [LICENSE](LICENSE).
