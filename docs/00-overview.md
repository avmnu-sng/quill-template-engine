# Quill -- Language Overview

This is the entry point to the Quill specification. It states what Quill is, the identity
and philosophy of the language, the approved anchor example, the headline design decisions
and the reasoning behind each, the breadth of the language surface, and how to read the rest
of the document set.

The companion documents are:

- `01-language-reference.md` -- the core reference manual: lexical structure and the
  source-emission rule, expressions and precedence, control flow and scoping, and template
  composition.
- `02-grammar.md` -- the consolidated formal EBNF grammar and its ambiguity-resolution
  notes.
- `03-stdlib.md` -- the standard library: the complete filter, function, and test catalogue
  with Go-native names and signatures.
- `04-types-and-semantics.md` -- the gradual type system, the value model, and the
  truthiness, equality, coercion, attribute-access, escaping, safety, and sandbox rules.
- `extensions.md` -- the extension API: custom filters, functions, and tests via the typed
  helpers, Extension bundles, composition and shadow order, the injection flags, and how
  custom callables interact with the sandbox and the type checker.
- `06-architecture-and-roadmap.md` -- how the packages are layered, the load-bearing runtime
  boundary, and the execution model.

--------------------------------------------------------------------------------

## 1. What Quill is

Quill is a brace-delimited, gradually-typed, Go-native template language built to emit one
exact byte sequence of program source code. Its dominant consumer is a code generator that
produces Java, Rust, C, Go, and 21 other target language families from templates that are
themselves dense with literal `{ } ( ) < > & ;` characters.

Quill has its own surface and its own front end -- lexer, parser, and gradual type checker.
Its runtime is a dynamic core: the `Value` model, the ordered `*Array`, the `Context`, the
tree-walking interpreter, the loaders, the escapers, and the structured error model. The
package layering is detailed in `06-architecture-and-roadmap.md`.

The name is Quill: a quill writes source faithfully, one stroke at a time. The name carries
none of the HTML-first connotation that "template" would mis-signal -- Quill emits
program source, not markup. The Go package is `quill`. The file extension is `.ql`; a
template that emits a specific target language may use a double extension (`body.java.ql`),
which the escaping-strategy resolver and the diagnostics renderer can key on. The anchor's
`.tmpl` spelling also loads; extensions are resolved by name.

--------------------------------------------------------------------------------

## 2. The anchor example

The following stakeholder-approved snippet is valid, idiomatic Quill. The language was
designed to make it so, and grows coherently outward from exactly this seed. Because Quill's
primary workload is brace-dense PROGRAM SOURCE CODE, the DEFAULT spelling leads each statement
with an `@` sigil and closes each block with `@}`, so a bare `{` or `}` anywhere in template
TEXT is unconditionally literal output. In the `@`-default spelling the anchor is:

```
@extends "base.tmpl"

@block body {
  @for u in users {
    {{ u.name | upper }}{{ ", admin" if u.isAdmin }}
  @}
@}

@macro greet(name) {
  Hello {{ name | default("guest") }}
@}
```

The originally-approved BARE spelling -- no `@`, statements recognized by a line-leading
keyword, a lone `}` line closing the innermost block -- remains valid Quill under the opt-in
`pragma bare` (equivalently `pragma sigil off`), intended for markup and other non-source
templates where brace collisions are rare:

```
pragma bare

extends "base.tmpl"

block body {
  for u in users {
    {{ u.name | upper }}{{ ", admin" if u.isAdmin }}
  }
}

macro greet(name) {
  Hello {{ name | default("guest") }}
}
```

Both spellings denote the same template. Every construct here is core Quill:

- `@extends "base.tmpl"` -- single-parent inheritance, a head-of-file directive
  (`01-language-reference.md` Section 5).
- `@block body { ... @}` -- a named, overridable region with a brace body
  (`01-language-reference.md` Section 5).
- `@for u in users { ... @}` -- a brace-bodied loop over a context sequence
  (`01-language-reference.md` Section 4).
- `{{ u.name | upper }}` -- interpolation with a pipe filter
  (`01-language-reference.md` Sections 2, 3).
- `{{ ", admin" if u.isAdmin }}` -- the postfix conditional on output, the one anchor
  feature that exceeds Twig (`01-language-reference.md` Section 2.6).
- `@macro greet(name) { ... @}` -- a parameterized output function
  (`01-language-reference.md` Section 5).
- `name | default("guest")` -- a filter taking an argument, undefined-safe
  (`03-stdlib.md` Section 2).
- `u.name`, `u.isAdmin`, `users`, `name` -- bare-identifier context lookup and dotted
  attribute access (`04-types-and-semantics.md` Section 5).

Quill adds no ceremony around any of these: a brace body opened by an `@`-led statement closes
with a single `@}` (under the default), or by a leading keyword and a lone `}` (under
`pragma bare`), with no end-keywords. That decision -- sigil-led brace blocks with no
end-keywords -- is what keeps the grammar tiny.

--------------------------------------------------------------------------------

## 3. The design philosophy

Quill is governed by one axiom: the primary consumer emits PROGRAM SOURCE CODE, so the
language optimizes for a generator author who must produce one exact byte sequence and must
never be silently surprised. A silently-absent value becomes a silently wrong emitted byte;
a silently-coerced value becomes a silently wrong emitted token. The enemy is the silent
surprise, and the semantics are chosen to remove it.

Three principles follow from the axiom:

1. **Go-idiomatic at the surface.** The syntax is brace-delimited, sigil-led (`@`-led
   statements by default, keyword-led under `pragma bare`), pipe-driven, and Pratt-parsed,
   designed around the anchor. Spellings are Go-templating-idiomatic (`snake_case` filters,
   `=>` arrows, `<=>` spaceship, `??`/`?:` fallthrough).

2. **Predictable value semantics.** One typed equality, one ordering, one truthiness rule,
   strict-by-default undefined handling, byte-exact value rendering (`false` renders
   `"false"`, an `*Array` render is an error rather than the literal word `Array`). Each rule
   is a clean Go-native rule, named and justified in `04-types-and-semantics.md`.

3. **Gradually typed.** Type annotations are optional. With zero annotations a template is
   the dynamic language backed by the strict-by-default runtime, and the anchor is valid
   verbatim. With annotations, a static checker rejects a class of error before any byte is
   emitted. Annotations never change runtime semantics; they only move an error earlier in
   time.

--------------------------------------------------------------------------------

## 4. Headline decisions and why

Each load-bearing dimension of the language was decided on the merits. The decisions are
stated here so the rest of the document set reads as one coherent language; each is
elaborated in the cited companion document.

1. **Sigil-led brace blocks, no end-keywords.** `@for ... { }`, `@block name { }`,
   `@macro f() { }` -- one closing `@}` per body under the default; one lone `}` per body
   under `pragma bare`. This makes the statement grammar tiny and the anchor idiomatic.
   (`01-language-reference.md` Section 4.)

2. **The `@`-sigil statement lead with `@}` close is the DEFAULT, making brace-dense source
   correct without escaping.** Under the default, a statement begins only at an `@`-led
   keyword (`@for`, `@if`, `@block`, ...) and a block closes only at `@}`; interpolation
   `{{ }}`, comment `{# #}`, and string interpolation `#{ }` are unchanged. A bare `{` or `}`
   ANYWHERE in template TEXT is therefore unconditionally literal output -- no escaping, no
   grammar-shape rejection, no lone-`}` collision, no line-leading-keyword diagnostic. This
   matters because the primary use case (a source-code generator) emits brace-dense source:
   literal lone-`}` lines are pervasive, the majority at column 0 (top-level closes of emitted
   classes and methods). Under the default
   every one of those sites is correct with zero author effort, at the cost of one `@` per
   statement. The BARE keyword-led mode (no `@`, a lone `}` closes) is retained as an
   explicit `pragma bare` / `pragma sigil off` opt-in for markup and non-source templates;
   the escape tools (leading-pipe `| ` text marker, `verbatim` region, interpolation,
   grammar-shape rejection) apply within that opt-in mode and to edge cases.
   (`01-language-reference.md` Section 2.)

3. **One published precedence ladder with the power/unary fix.** Seventeen levels, with one
   consistent AST-driven rule so the PHP `-1 ** 0 == -1` leak cannot occur. The pipe `|` is
   the filter operator, so bitwise-or is the word `b_or` (with `|||`/`^`/`&` aliasing the
   bitwise family) to keep the pipe unambiguous. (`01-language-reference.md` Section 3.)

4. **Predictable value semantics.** One typed `==`, one ordering, one truthiness,
   strict-by-default undefined, `Bool` renders `true`/`false`, an `*Array` render is an
   error. (`04-types-and-semantics.md`.)

5. **Default escaping off, full strategy set available.** Raw output by default for source
   emission; the six escaping strategies (`html`, `js`, `css`, `html_attr`,
   `html_attr_relaxed`, `url`) are opt-in filters and regions; the sandbox is built on a Go
   host type-graph with uniform allowlisting. (`04-types-and-semantics.md` Section 8.)

6. **Gradual types drive the `types` block.** Optional annotations on `set` targets, macro
   params and returns, block inputs, `for` targets, arrow params, and a `types { }` block.
   The block is a first-class checker input. (`04-types-and-semantics.md` Sections 1-3.)

7. **A dynamic runtime under a new front end and type checker.** The lexer, parser, and
   gradual type checker form the front end over the tree-walking runtime.
   (`06-architecture-and-roadmap.md`.)

--------------------------------------------------------------------------------

## 5. The language surface

Quill is a full-featured template language. The author-facing capabilities are:

- **Composition:** single-parent inheritance (`@extends`), overridable blocks with
  `parent()`, parameterized macros with defaults and variadics (`@macro`), macro import
  (`@import`/`@from`), horizontal trait reuse (`@use`), block-overriding include (`@embed`),
  and the statement- and function-form `@include`.
- **Control flow:** `@for`/`@else` with always-defined loop metadata, `@if`/`@elseif`/`@else`,
  `@set` with list/map destructuring and capture, `@with` scoped bindings, and `@do`.
- **Expressions:** a seventeen-level precedence ladder, pipe filters, arrow functions,
  higher-order filters (`map`/`filter`/`sort`/`reduce`/`find`), the `has some`/`has every`
  quantifiers, the `matches` regex operator, the `<=>` spaceship, and the `??`/`?:`/`?.`
  fallback operators.
- **Output:** the postfix conditional `{{ expr if cond }}` (and `unless`), interpolation with
  string interpolation `#{ }`, comments `{# #}`, and whitespace-control trim modifiers.
- **Safety:** six escaping strategies via the `escape`/`e` filter and `@escape` region, and a
  host-configurable sandbox.

Two capabilities are the language's own additions to the brace-templating idiom:

- The postfix conditional on output, `{{ expr if cond }}`, shown in the anchor.
- The gradual type system, which makes the `types` block first-class and adds a static
  checking layer over the dynamic engine.

--------------------------------------------------------------------------------

## 6. Implementation

Quill is implemented as a Go module (`github.com/avmnu-sng/quill-template-engine`): the
lexer, parser, gradual type checker, tree-walking interpreter, the full standard library, the
escaping and sandbox subsystems, the coverage collector, and the `cmd/quill` command. The
runtime depends on nothing outside the Go standard library. The package layering and the
load-bearing runtime boundary are in `06-architecture-and-roadmap.md`; the extension API for
host callables is in `extensions.md`.

--------------------------------------------------------------------------------

## 7. How to read the rest

A first-time reader should read this overview, then `01-language-reference.md` for the core
manual, consulting `02-grammar.md` when an exact production is needed. A reader using or
extending the standard library reads `03-stdlib.md`; a host adding its own callables reads
`extensions.md`. A reader who needs the precise runtime rules -- truthiness, equality,
coercion, undefined handling, escaping, sandbox, and the gradual type system -- reads
`04-types-and-semantics.md`. An engineer working on the engine reads
`06-architecture-and-roadmap.md`.

Each document is self-contained and cross-references its siblings by filename and section.
Exhaustive per-area detail (the canonical language definition and the per-area elaborations
covering lexical structure, expressions, control flow, composition, the standard library, the
type system, semantics, escaping and safety, and the grammar) is tracked in the project's
design notes, which the specification references where deeper treatment is warranted.
