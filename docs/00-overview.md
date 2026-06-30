# Quill -- Language Overview

This is the entry point to the Quill specification. It states what Quill is, the identity
and philosophy of the language, the approved anchor example, the headline design decisions
and the reasoning behind each, the parity claim against Twig, what is in and out of scope
for this specification, and how to read the rest of the document set.

The companion documents are:

- `01-language-reference.md` -- the core reference manual: lexical structure and the
  source-emission rule, expressions and precedence, control flow and scoping, and template
  composition.
- `02-grammar.md` -- the consolidated formal EBNF grammar and its ambiguity-resolution
  notes.
- `03-stdlib.md` -- the standard library: the complete filter, function, and test catalogue
  with Go-native names and signatures.
- `04-types-and-semantics.md` -- the gradual type system, the value model, and the
  truthiness, equality, coercion, attribute-access, escaping, safety, and sandbox rules,
  each with its deliberate divergence-from-Twig note.
- `05-twig-parity-and-migration.md` -- the parity matrix, the Go-native delta, and the
  Twig-to-Quill migration story.
- `06-architecture-and-roadmap.md` -- how Quill maps onto the reused runtime, a
  dependency-ordered milestone roadmap, and a risk register.

--------------------------------------------------------------------------------

## 1. What Quill is

Quill is a brace-delimited, gradually-typed, Go-native template language built to emit one
exact byte sequence of program source code. Its dominant consumer is a code generator that
produces Java, Rust, C, Go, and 21 other target language families from templates that are
themselves dense with literal `{ } ( ) < > & ;` characters.

Quill is a FRESH language design, not a port of Twig's syntax. Its front end -- lexer,
parser, and gradual type checker -- is new. Its runtime reuses a faithful Twig-to-Go port's
dynamic core: the `Value` model, the ordered `*Array`, the `Context`, the tree-walking
interpreter, the loaders, the escapers, and the structured error model. Four runtime
modules are edited and one default is flipped; the rest is reused unchanged. The split is
detailed in `06-architecture-and-roadmap.md`.

The name is Quill: a quill writes source faithfully, one stroke at a time. The name carries
none of the HTML-first connotation that "twig" or "template" would mis-signal -- Quill emits
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

Quill adds no ceremony around any of these. There is no `{% %}` envelope, no `endblock`, no
`endfor`, no `endmacro`: a brace body opened by an `@`-led statement closes with a single
`@}` (under the default), or by a leading keyword and a lone `}` (under `pragma bare`). That
decision -- sigil-led brace blocks with no end-keywords -- is what keeps the grammar tiny
while reaching full Twig parity.

--------------------------------------------------------------------------------

## 3. The design philosophy

Quill is governed by one axiom: the primary consumer emits PROGRAM SOURCE CODE, so the
language optimizes for a generator author who must produce one exact byte sequence and must
never be silently surprised. A silently-absent value becomes a silently wrong emitted byte;
a silently-coerced value becomes a silently wrong emitted token. The enemy is the silent
surprise. Twig inherits PHP's value semantics, which are a catalogue of silent surprises;
Quill removes them.

Three principles follow from the axiom:

1. **Fresh and Go-idiomatic at the surface.** The syntax is brace-delimited, sigil-led
   (`@`-led statements by default, keyword-led under `pragma bare`), pipe-driven, and
   Pratt-parsed, designed around the anchor rather than around Twig's `{% %}` envelope.
   Spellings are Go-templating-idiomatic (`snake_case` filters, `=>` arrows, `<=>` spaceship,
   `??`/`?:` fallthrough).

2. **De-PHP-ified semantics.** One typed equality, one ordering, one truthiness rule,
   strict-by-default undefined handling, byte-exact value rendering (`false` renders
   `"false"`, an `*Array` render is an error rather than the literal word `Array`). Every
   PHP accident is replaced by a clean Go-native rule, and every such divergence is named
   and justified in `04-types-and-semantics.md`.

3. **Gradually typed.** Type annotations are optional. With zero annotations a template is
   the dynamic language backed by the strict-by-default runtime, and the anchor is valid
   verbatim. With annotations, a static checker rejects a class of error before any byte is
   emitted. Annotations never change runtime semantics; they only move an error earlier in
   time. This is the one net addition over Twig's surface, alongside the postfix
   conditional.

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

4. **De-PHP-ified value semantics.** One typed `==`, one ordering, one truthiness,
   strict-by-default undefined, `Bool` renders `true`/`false`, an `*Array` render is an
   error. (`04-types-and-semantics.md`.)

5. **Default escaping off, full strategy set retained.** Raw output by default for source
   emission; the six Twig escaping strategies are opt-in filters and regions; the sandbox is
   re-based on a Go host type-graph with uniform allowlisting. (`04-types-and-semantics.md`
   Section 8.)

6. **Gradual types subsume the `types` block.** Optional annotations on `set` targets, macro
   params and returns, block inputs, `for` targets, arrow params, and a `types { }` block.
   The block is promoted from inert tooling metadata to a first-class checker input.
   (`04-types-and-semantics.md` Sections 1-3.)

7. **The port's runtime is reused; only the front end and the type checker are new.** Four
   runtime modules change (`compare`, `truthy`, `stringify`, `attribute`) and the escaping
   default flips. (`06-architecture-and-roadmap.md`.)

--------------------------------------------------------------------------------

## 5. The parity claim: no fewer features than Twig

Quill carries NO FEWER features than Twig 3.27.1. Faithfulness is required at the FEATURE
level -- every author-facing capability Twig offers has an equivalent in Quill -- and
explicitly NOT at the level of PHP value accidents. The full parity matrix maps all 246
catalogued Twig capabilities to a Quill spelling and section in `05-twig-parity-and-migration.md`,
marking each COVERED, EXCEEDED, or FOLDED. There are zero capability-reducing drops: the only
entries that approach a drop are deprecated-alias folds, where one Twig spelling merges into
another with no loss of reachable behavior.

Two features EXCEED the Twig floor and are required new capabilities rather than reductions:

- The postfix conditional on output, `{{ expr if cond }}`, shown in the anchor.
- The gradual type system, which makes the `types` block first-class and adds a static
  checking layer over the dynamic engine.

Both expand the floor; neither lowers it.

--------------------------------------------------------------------------------

## 6. Scope of this specification

IN SCOPE: this document set is the LANGUAGE DESIGN and SPECIFICATION. The deliverable is the
grammar (EBNF), syntax examples, illustrative Go type and signature sketches inside the
specification, the semantics, the standard library catalogue, the parity proof, and the
architecture and roadmap.

OUT OF SCOPE: a working implementation. No lexer, parser, type checker, or interpreter is
built; no Go implementation files are produced; no build is run. The Go type and signature
sketches that appear in these documents are specification aids -- they show how the host
surface and AST shape are expected to look -- not files to compile. Implementation is gated
for human review of this specification first.

--------------------------------------------------------------------------------

## 7. How to read the rest

A first-time reader should read this overview, then `01-language-reference.md` for the core
manual, consulting `02-grammar.md` when an exact production is needed. A reader implementing
or extending the standard library reads `03-stdlib.md`. A reader who needs the precise
runtime rules -- truthiness, equality, coercion, undefined handling, escaping, sandbox, and
the gradual type system -- reads `04-types-and-semantics.md`. A reviewer auditing the
feature claim against Twig, or migrating an existing corpus, reads
`05-twig-parity-and-migration.md`. An engineer planning the build reads
`06-architecture-and-roadmap.md`.

Each document is self-contained and cross-references its siblings by filename and section.
Exhaustive per-area detail (the canonical language definition and the per-area elaborations
covering lexical structure, expressions, control flow, composition, the standard library, the
type system, semantics, escaping and safety, the grammar, the parity-and-delta analysis, and
worked examples) is tracked in the project's design notes, which the specification references
where deeper treatment is warranted.
