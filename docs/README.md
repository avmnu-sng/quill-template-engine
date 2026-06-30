# Quill Specification

This directory holds the language specification for Quill, a Go-native,
gradually-typed template engine built to emit exact text -- especially program
source code. Each document is self-contained and cross-references its siblings
by filename and section.

Read them in order for a full tour, or jump to the one you need:

| Document | What it covers |
|----------|----------------|
| [`00-overview.md`](00-overview.md) | Entry point: what Quill is, its identity, the design axioms (the `@`-sigil source-emission rule, escaping off by default, gradual typing), and how the rest of the spec fits together. |
| [`01-language-reference.md`](01-language-reference.md) | The core reference manual: lexical structure, the two statement-lead modes, interpolation and comments, whitespace control, statements, expressions, and the precedence ladder. |
| [`02-grammar.md`](02-grammar.md) | The complete, single, internally consistent EBNF grammar, including the lexer state rules and the block-close semantics. |
| [`03-stdlib.md`](03-stdlib.md) | The built-in standard library: filters piped with `|`, functions, tests, the host-registration surface, and the source-code-emission helpers. |
| [`04-types-and-semantics.md`](04-types-and-semantics.md) | The runtime value layer and the gradual type system: typed equality, ordering, truthiness, strict-by-default undefined handling, byte-exact rendering, and the type lattice. |
| [`05-twig-parity-and-migration.md`](05-twig-parity-and-migration.md) | The Twig parity matrix (every Twig capability mapped to a Quill spelling), the Go-native delta, and the Twig-to-Quill migration assessment. |
| [`06-architecture-and-roadmap.md`](06-architecture-and-roadmap.md) | How Quill maps onto the runtime it reuses, what changes when gradual typing is layered on, a dependency-ordered milestone roadmap, and the risks. |

Comparisons to Twig appear throughout; Twig is a public project and is the
closest prior art. Quill diverges from Twig deliberately wherever PHP value
accidents would otherwise leak into output -- those divergences are justified in
[`04-types-and-semantics.md`](04-types-and-semantics.md) and
[`05-twig-parity-and-migration.md`](05-twig-parity-and-migration.md).
