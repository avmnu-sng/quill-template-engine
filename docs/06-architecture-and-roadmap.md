# Quill -- Architecture and Roadmap

This document maps Quill onto the runtime it reuses, states what changes when gradual typing is
layered on, gives a dependency-ordered milestone roadmap, and records the risks. It is the
implementation-planning reference; the language itself is specified in `00-overview.md` through
`05-twig-parity-and-migration.md`. The runtime Quill builds on is a faithful Twig-to-Go port
(referenced below as
"the port architecture"); the Go type and signature sketches here are design artifacts for review,
not code to ship.

Quill is a NEW front end -- lexer, parser, and gradual type checker -- over a lightly modified
version of the port's runtime. The new work is the front end plus the type checker; the runtime is
reused with four edited modules and one flipped default.

--------------------------------------------------------------------------------

## 1. Package layout and the load-bearing boundary

Quill keeps the port's layered package set with dependencies flowing downward only. The CRITICAL
boundary from the port architecture is preserved: `runtime` imports nothing from AST-evaluation or
parsing, so the correctness budget -- the comparisons, attribute access, truthiness, string
coercion, and escape -- is spent once in `runtime`, fixture-testable in isolation. The interpreter
depends only on `runtime`, and any future compile-to-Go backend would be a pure addition.

```
quill/         facade: Environment, Template wrapper, options, globals, render entry points
  source/      Source value object; CRLF normalization
  lex/         NEW Quill lexer: the two-mode TEXT/CODE machine, sigil predicate,
               @-sigil statement lead (default) and leading-keyword statement test
               (pragma bare), verbatim, trim modifiers, line directive
  ast/         AST node (uniform struct, ordered mixed-key children, Kind discriminator)
  parse/       NEW Quill parser: LL statement parser + Pratt expression loop
  check/       NEW gradual type checker: a front-end pass between parse and interpret
  runtime/     REUSED with edits: Value taxonomy, *Array, Safe, Context, Output, BlockTable;
               Equal/Order/Same (was LooseEq/Compare/Identical); Truthy (IsEmpty/PhpEmpty deleted);
               ToText (four rules changed); GetAttribute (kind-dispatch, strict-by-default);
               Escape (six strategies); the Object protocol
  interp/      REUSED: tree-walking interpreter, for-loop scope, include/block/macro dispatch
  ext/         REUSED: Extension registry, CoreExtension, EscaperExtension, sandbox, loaders
  loader/      REUSED: Loader, FilesystemLoader, ArrayLoader, ChainLoader
  cache/       REUSED: Cache, MemoCache, NullCache (cache key gains the type-check signature)
  errors/      REUSED + extended: Error family, now also carrying TypeCheck diagnostics
```

The `lex`, `parse`, and `check` packages are new; the rest is the port's, with the edits in
`runtime` and the escaping default.

--------------------------------------------------------------------------------

## 2. What is reused, what changes

### 2.1 Reused essentially unchanged

The `*Array` (ordered map, value-copy machinery), the `Context` save/restore, the loaders
(filesystem/in-memory/chain, candidate list, missing tolerance), the compile cache (with the
gradual-type signature added to the cache key), the structured error model with source context (now
also carrying type-check diagnostics), the `Safe` wrapper and the six escapers plus the
safeness-analysis and escaper node visitor, the `replace`/`strtr` Replacer-backed implementation,
the `matches` RE2 backing, the macro scope model, the `Template` contract
(`Display`/`Block`/`HasBlock`/`Macro`/`HasMacro`/`Parent`), the tree-walking interpreter, and the
host registration surfaces (filters, functions, tests, globals, strategies, policies, constant/enum
registries, charset config, strict-variables switch, deterministic RNG).

The host-registration surface carries the port's callable INJECTION FLAGS unchanged:
`needs_charset`, `needs_context`, `needs_environment`, and `needs_is_sandboxed`. A registered
filter/function/test declares which of the active charset, the live context, the environment
handle, and the sandbox-active state it requires, and the runtime prepends those before the
user arguments (`03-stdlib.md` Section 3.6). This is a parity requirement for X6/X7/X8/FL44/F19:
the four case filters and the codepoint escapers need the charset, and `include`/`block`/
`parent`/`dump`/`template_from_string` need context/environment, so the injection mechanism is
load-bearing, not optional.

### 2.2 Changed from the port (four modules and one default)

- **`compare.go`** collapses three entry points (`LooseEq`, `Compare`, `Identical`) to two
  (`Equal`, `Order`) plus a thin `Same`. (`04-types-and-semantics.md` Section 3.)
- **`truthy.go`** keeps one `Truthy` and deletes `IsEmpty`/`PhpEmpty`; `is empty` and `default` are
  re-expressed over truthiness, length, and definedness. (`04-types-and-semantics.md` Section 2.2.)
- **`stringify.go`** changes four `ToText` rules: bool (`true`/`false` not `"1"`/`""`), float (Go
  shortest form), array (render error not `"Array"`), object-without-hook (render error).
  (`04-types-and-semantics.md` Section 4.)
- **`attribute.go`** is rewritten to kind-dispatch instead of the PHP cascade and flips its default
  miss from silent-`Null` to error (gated by the `lenient` flag and the threaded absence flag).
  (`04-types-and-semantics.md` Sections 5-6.)
- **The default escaping strategy** flips from `html` to `off`. (`04-types-and-semantics.md`
  Section 8.)

### 2.3 Added with no PHP analogue

The gradual type checker is a NEW front-end pass between parse and interpret. Where annotations are
present it promotes the strict-by-default runtime errors to check-time errors; it changes nothing at
run time -- annotations only move errors earlier. (`04-types-and-semantics.md` Section 1.)

### 2.4 What gradual typing changes in the front end

The lexer gains nothing for types beyond tokenizing `:` `->` `<` `>` `|` in type position. The
parser gains the type-annotation grammar (`02-grammar.md` Section 5) at the annotation sites and
attaches a `*Type` to the relevant AST nodes. The new `check` pass walks the typed nodes, resolves
`Object<"Type">` against the host type-graph (the same graph the sandbox uses), and emits
`TypeCheck` diagnostics into the existing error family. The interpreter is untouched by typing: a
fully-annotated and a fully-unannotated template render through the identical interpreter path.

The illustrative host and AST shapes (the `Value` struct, the `Filter`/`Function`/`Test`
registration records, the `Type` lattice node, the postfix-`if`-desugars-to-ternary `PrintNode`, and
the sandbox `Policy`) are sketched in the project's design notes.

--------------------------------------------------------------------------------

## 3. Execution model

One shared AST plus a tree-walking interpreter is the primary and only required backend, reused from
the port. A tree-walker pays a `Kind`-switch and a `Value` boxing per node per render; for the batch
generator workload (parse-once-per-process, bounded renders to produce source) this is well within
budget. An ahead-of-time compile-to-Go backend is reserved by the port's `runtime`-imports-nothing-
from-parser discipline but is not part of Quill's scope. The postfix conditional desugars to a
ternary at parse time, so the interpreter needs no new node kind for it. The always-defined loop
metadata requires draining a host iterator to an `*Array` before the loop, a small change in the
interpreter's for-loop setup.

--------------------------------------------------------------------------------

## 4. Dependency-ordered milestone roadmap

Each milestone depends only on those before it. The runtime edits (Section 2.2) are front-loaded
because every later milestone renders through them.

**M0 -- Runtime edits and fixtures.** Edit `compare.go`, `truthy.go`, `stringify.go`,
`attribute.go`; flip the escaping default. Pin each with a table-driven fixture set asserting the
de-PHP-ified rules (typed `==`, one truthiness, the four `ToText` changes, kind-dispatch attribute
access, strict-by-default miss). This is the correctness foundation; no front end renders correctly
until it is frozen.

**M1 -- Lexer.** The two-mode TEXT/CODE machine: the `atSigil` predicate, the @-sigil
statement-lead recognition (default) and the leading-keyword test (pragma bare), bracket
balancing in CODE, the trim modifiers and newline-eating asymmetry, string/
number/identifier scanning, `verbatim` (brace-balanced and fenced), comments, and the line directive.
The acid test is the worked brace-dense Java example rendering byte-for-byte with no escaping.
Depends on: M0 (for the `Str` byte-string contract).

**M2 -- Parser (expressions + core statements).** The Pratt expression loop with the seventeen-level
ladder (including the power/unary fix and the word-operator/identifier reclassification), plus the
core statements `if`/`for`/`set`/`include`/interpolation/comment. The anchor's expression forms and
the `{{ expr if cond }}` desugaring land here. Depends on: M1, the AST node shape.

**M3 -- Interpreter (core render).** Drive `Display` over the M2 AST: `for` with always-defined loop
metadata and the non-iterable error, `set` and `capture`, `if`, interpolation through the edited
`ToText`, and the statement `include`. The strict-by-default undefined path and the `?.`/`??`/
`default` absence tools. Depends on: M0, M2.

**M4 -- Composition.** `extends`/`block` (long and shortcut, nested, `parent()`), `macro` with
defaults/variadics/isolation, `import`/`from`/`use`/`embed`, the function-form `include`, and the
block-table merge and parent-chain walk. Reuses the port's `Template` contract. Depends on: M3.

**M5 -- Standard library.** Wire the full filter/function/test catalogue (`03-stdlib.md`) onto the
reused runtime implementations, including the source-emission helpers (`tab`, `ucfirst`, `indent`,
`raw`) and the host-registration surfaces. Depends on: M3 (filters need the value model and the call
surface).

**M6 -- Escaping and sandbox.** The opt-in escape regions and `escape`/`e`/`raw` filters over the
reused six-strategy escaper, the safeness machinery active only when escaping is on, and the sandbox
re-based on the host type-graph with uniform allowlisting. Depends on: M5 (escape is a filter), M4
(sandbox gates includes).

**M7 -- Gradual type checker.** The `check` pass: parse type annotations, resolve `Object<"Type">`
against the host type-graph, and promote the M0/M3 runtime errors to check-time diagnostics where
annotations are present. The parity guarantee -- annotation-free templates render identical bytes --
is verified by running the untyped corpus through M3 and M7 and diffing. Depends on: M4 (block/macro
signatures), M6 (the shared type-graph).

**M8 -- Migration transpiler.** The source-to-source Twig-to-Quill compiler
(`05-twig-parity-and-migration.md` Section 4): read with the port's Twig parser, map the AST, print
`.ql`, and emit the review manifest. Depends on: M2-M4 (the target language must be parseable and
renderable to validate output).

--------------------------------------------------------------------------------

## 5. Risk register

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| The `{{`-adjacency collision surprises an author emitting double-brace source (Java `HashMap<>() {{ }}`, Go `[][]int{{1}}`) | A literal `{{` at column 0 is mis-lexed as an interpolation open | Low (corpus has zero occurrences) | Three documented escapes (`verbatim`, `{{ "{{" }}`, `\{{`); a suppressible lex diagnostic; `verbatim` is the recommended bulk tool. (`01-language-reference.md` Section 1.2.) |
| A line-leading Quill keyword in emitted output (a literal Ruby `for item in list`) is mis-classified as a statement -- applies ONLY under the opt-in `pragma bare` mode | A statement-shaped output line becomes code | Low (bare mode only) | Under the @-sigil DEFAULT the collision DOES NOT ARISE: an emitted line lacks the `@` lead, so a bare `for`/`if`/`while` line is plainly TEXT, and the default is what source templates use. The row applies only to `pragma bare`, where grammar-shape rejection handles the C/Java/Go `(`-after-keyword shapes for free, the `| ` marker and `verbatim` cover the rest, and a suppressible `line-leading-keyword` diagnostic fires at every such line. (`01-language-reference.md` Section 1.3.) |
| The strict-by-default undefined flip breaks templates that relied on Twig's silent-null | Newly-erroring reads in migrated templates | Medium during migration | The `lenient` mode restores silent-`Null` for an exact-Twig migration; the transpiler transpiles under `lenient` and flags each read for incremental tightening; the gradual checker catches typed misses at check time. (`04-types-and-semantics.md` Section 6.) |
| The four `ToText` changes alter emitted bytes versus a PHP-Twig baseline | Byte-diffs against legacy output | Medium | The changes are deliberate and byte-load-bearing (`false`->`"false"`, array-render error); M0 fixtures pin them; the transpiler's manifest flags `format`/`date`/`number_format`/ambiguous `+` for human review. |
| The compile-to-Go backend is desired later for throughput | Re-sequencing the runtime calls | Low (out of scope; reserved) | The port's `runtime`-imports-nothing-from-parser boundary keeps the backend a pure addition behind the `Template` interface, never a value-model reshape. |
| Gradual-type unsoundness at the `any` boundary lets a typed error slip to run time | A check-time error that should have fired earlier fires at run time instead | Low | The two layers are designed to agree: the checker only ever PROMOTES a runtime error to check time and never permits what the runtime rejects, so a missed promotion degrades to the strict-by-default runtime error, never to silent wrong output. (`04-types-and-semantics.md` Section 1.5.) |
| RE2's lack of PCRE backreferences/lookaround breaks a migrated `matches` pattern | A pattern fails to compile | Low (corpus uses RE2-expressible patterns) | Literal patterns are validated at compile time with a clear error; the transpiler flags PCRE-only features for a human rewrite. |
