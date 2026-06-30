# Quill

Quill is a Go-native, gradually-typed template language and engine. It is built
for generating exact text -- especially program source code -- with the
ergonomics of a modern language and the safety of optional static types.

> Status: early development. The language specification is complete and under
> implementation. The API is not yet stable.

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

## Documentation

The language specification lives in [`docs/`](docs/):

- Overview and design philosophy
- Language reference and formal grammar
- Standard library (filters, functions, tests)
- Types and runtime semantics

## License

Apache-2.0. See [LICENSE](LICENSE).
