# Composition

Quill offers Twig-class composition: single-parent inheritance, overridable
blocks, parameterized macros, imports and traits, embeds, includes, and
accumulating slots. This is real template inheritance and reuse -- not string
concatenation. Composition is built on the `Template` contract; inheritance,
embed, and trait reuse all reduce to building and merging block tables and
walking a parent chain, while macros are a separate, isolated function namespace.

The statement keywords below are written in the `@`-default spelling
([Templates](templates.md)); under `pragma bare` drop the `@` and close on a lone
`}`.

## Inheritance

```
@extends "base.ql"           // single parent; content outside blocks in a child is rejected

@block body {                // define + render in place; long form
  ...
@}
@block title "Default Title" // shortcut value form
@block outer {               // nested, independently overridable
  @block inner { ... @}
@}
```

`@extends "<expr>"` takes a string-coerced expression, so a candidate list
`@extends ["a.ql", "b.ql"]` selects the first that exists. Inside an overriding
block, `parent()` renders the parent's version. `block("name")` and
`block("name", "other.ql")` render a named block of this or another template;
`block("name") is defined` tests existence.

## Macros

```
@macro greet(name, greeting: string = "Hello", ...rest) {
  {{ greeting }} {{ name | default("guest") }}
@}
```

Declared params with constant defaults, optional type annotations, and a variadic
capture `...rest`. A macro sees only its params, defaults, variadics, and host
globals -- the caller's local context is invisible. A macro returns its captured
output (a `Str`, or `Safe` under escaping).

**Tail captures.** A macro may take two optional tail parameters in a fixed
order: a positional variadic `...rest` that collects excess positional arguments
into a list, then a keyword variadic `**opts` that collects excess named
arguments into a `map<string, any>`. `**opts` must be the last parameter:

```
@macro render_field(name, **opts) {
  <input name="{{ name }}"{{ opts | keys | join(" ") }}>
@}
{{ render_field("email", id: "e1", class: "big") }}   // opts == { id: "e1", class: "big" }
```

Without a `**opts` tail an unmatched named argument is a typo error; with one it
is absorbed. Because `**opts` is an ordinary mapping, it forwards to a nested call
by spread: `inner(...opts)`.

**The macro namespace.** A macro body sees the names of all macros visible to the
template -- its own, sibling macros in the same template, and macros brought in by
`import`/`from` -- so a macro may call itself or a sibling directly by name.
Recursion and mutual recursion are reachable both by bare name and by the `_self`
import path (`import _self as me; me.tree(...)`).

**Call blocks.** A `@call name(args) { body }` invokes macro `name` and binds a
`caller()` callable inside the macro body that renders the block. It factors a
wrapping macro (header/footer, table row, section shell) from the content it
wraps:

```
@macro section(title) {
## {{ title }}
{{ caller() }}
@}
@call section("Overview") {
This body is rendered where the macro calls caller().
@}
```

`@call(p1, p2) name(args) { body }` declares caller parameters that `caller(v1,
v2)` inside the macro binds positionally in the block scope.

## Imports and traits

```
@import "forms.ql" as forms                 // namespace; call forms.input(...)
@from "forms.ql" import input, label as lbl  // selective; call input(...), lbl(...)
@use "buttons.ql"                            // import all blocks of a traitable template
@use "buttons.ql" with { submit: ok }        // block aliasing / rename
```

Top-level import is global; in-block import is block-local. A trait has no parent,
no macros, and no free body; trait-then-own precedence means the importing
template's own block definitions win over imported ones.

## Embed

```
@embed "card.ql" with { title: t } {
  @block body { {{ content }} @}
@}
```

Inline an anonymous child of the embedded template: include plus block override
in one construct. Supports `with`, `only`, and `ignore missing`.

## Includes

Statement form:

```
@include "header.ql"
@include "row.ql" with { user: u }
@include "row.ql" with { user: u } only
@include "maybe.ql" ignore missing
@include ["a.ql", "b.ql"]            // first that exists
```

`with map` adds vars to the current context; `only` renders with just those vars;
`ignore missing` tolerates absence, rendering nothing; a sequence is a candidate
list, first existing wins.

Function form, returning rendered output as an expression value:

```
{{ include("snippet.ql", { x: 1 }, with_context: false, ignore_missing: true, sandboxed: true) }}
```

## Accumulating content slots

`@provide` and `@yield` collect rendered content from many sites into a named
buffer and emit it once -- the complement of `@block`: where a block *overrides*,
a slot *accumulates*.

```
imports:
@yield imports
...
@provide imports {
import "os"
@}
@provide imports {
import "fmt"
@}
```

- `@provide <label> { body }` appends the rendered body to the slot named
  `<label>` and emits nothing at its own position. Every contribution appends in
  execution order.
- `@yield <label>` emits the slot's accumulated content once. It is deferred: a
  `@yield` placed before the `@provide` sites that feed it is backfilled with the
  complete accumulation after the render, so a shell can reserve a region at the
  top and have body partials feed it further down.
- `slot(label)` is the expression form: it returns the slot's content as of the
  call as a value, capturing only what accumulated before the call.

Slots span the whole render, including sub-renders: a `@provide` inside an
`@include`d or `@embed`ded partial appends to the enclosing render's slot buffer,
and a `@yield` in the shell is backfilled from those contributions. The canonical
use is collecting import lines or a symbol table from many partials and emitting
the assembled block once in a shell.

## Next

- [Standard Library](../stdlib.md) -- the built-in filters, functions, and
  tests.
- [Language Reference](../reference/language.md) -- the exhaustive treatment of
  every construct on this page.
