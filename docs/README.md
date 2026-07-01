# Quill Documentation

This directory documents Quill, a Go-native, gradually-typed template engine built
to emit exact text -- especially program source code. Each document is
self-contained and cross-references its siblings by filename and section.

Read them in order for a full tour, or jump to the one you need:

| Document | What it covers |
|----------|----------------|
| [`00-overview.md`](00-overview.md) | Entry point: what Quill is, its identity, the design axioms (the `@`-sigil source-emission rule, escaping off by default, gradual typing), the language surface, and how the rest fits together. |
| [`01-language-reference.md`](01-language-reference.md) | The core reference manual: lexical structure, the two statement-lead modes, interpolation and comments, whitespace control, statements, expressions, and the precedence ladder. |
| [`02-grammar.md`](02-grammar.md) | The complete, single, internally consistent EBNF grammar, including the lexer state rules and the block-close semantics. |
| [`03-stdlib.md`](03-stdlib.md) | The built-in standard library: filters piped with `|`, functions, tests, the host-registration surface, and the source-code-emission helpers. |
| [`04-types-and-semantics.md`](04-types-and-semantics.md) | The runtime value layer and the gradual type system: typed equality, ordering, truthiness, strict-by-default undefined handling, byte-exact rendering, and the type lattice. |
| [`extensions.md`](extensions.md) | The extension API: custom filters, functions, and tests via the typed helpers, Extension bundles, composition and shadow order, the injection flags, and the sandbox and type-checker interaction. |
| [`coverage.md`](coverage.md) | Template coverage: the unit/branch model, the text/LCOV/HTML reports, the seeding boundary, and the `FailUnder` CI gate. |
| [`06-architecture-and-roadmap.md`](06-architecture-and-roadmap.md) | How the packages are layered, the load-bearing runtime boundary, and the execution model. |
