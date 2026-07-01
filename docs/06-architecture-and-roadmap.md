# Quill -- Architecture

This document describes how Quill's packages are layered, the load-bearing runtime
boundary, and the execution model. The language itself is specified in
`00-overview.md` through `04-types-and-semantics.md`; the extension API is in
`extensions.md`.

Quill is a front end -- lexer, parser, and gradual type checker -- over a
tree-walking runtime. The Go module is `github.com/avmnu-sng/quill-template-engine`,
and the runtime depends on nothing outside the Go standard library.

--------------------------------------------------------------------------------

## 1. Package layout and the load-bearing boundary

Quill is a layered package set with dependencies flowing downward only. The
CRITICAL boundary is that `runtime` imports nothing from AST evaluation or
parsing, so the correctness budget -- the comparisons, attribute access,
truthiness, string coercion, and escape -- is spent once in `runtime` and is
fixture-testable in isolation. The interpreter depends only on `runtime`, and a
future compile-to-Go backend would be a pure addition behind the same `Template`
contract.

```
quill/         facade: Environment, options, the render entry points, engine callables
  source/      Source value object; CRLF normalization
  lex/         the two-mode TEXT/CODE lexer: sigil predicate, @-sigil statement lead
               (default) and leading-keyword statement test (pragma bare), verbatim,
               trim modifiers, line directive
  ast/         AST node (uniform struct, ordered mixed-key children, Kind discriminator)
  parse/       the parser: LL statement parser + Pratt expression loop
  check/       the gradual type checker: a front-end pass between parse and interpret
  runtime/     the value layer: Value taxonomy, ordered *Array, Safe, Context, Output;
               Equal/Order/Same, Truthy, ToText, GetAttribute (kind-dispatch,
               strict-by-default), the six-strategy Escape, and the Object protocol
  interp/      the tree-walking interpreter: for-loop scope, include/block/macro dispatch,
               composition, escaping regions, sandbox gating, coverage hooks
  ext/         the callable registry: Filter/Function/Test, ExtensionSet, Extension bundles,
               the typed NewFilter/NewFunction/NewTest helpers, and the core standard library
  loader/      Loader, FilesystemLoader, ArrayLoader, ChainLoader
  cache/       the parse cache and the rendered-body cache backing @cache
  errors/      the structured error family, carrying source context and TypeCheck diagnostics
  sandbox/     the SecurityPolicy and host TYPE-GRAPH
  cover/       the coverage Collector, Report, and the text/LCOV/HTML writers
  cmd/quill/   the command: render and the cover subcommand
```

The `runtime`-imports-nothing-from-parser discipline is what keeps the value model
independently testable and the interpreter the only consumer of the AST.

--------------------------------------------------------------------------------

## 2. The value layer

The `runtime` package holds the load-bearing value semantics:

- The `*Array` (ordered map with value-copy machinery), the `Context` save/restore,
  the `Safe` wrapper, and the `Output` sink.
- One typed equality (`Equal`), one ordering (`Order`), and a thin identity
  (`Same`). (`04-types-and-semantics.md` Section 3.)
- One `Truthy`, with `is empty` and `default` expressed over truthiness, length,
  and definedness. (`04-types-and-semantics.md` Section 2.2.)
- `ToText`, the byte-exact string coercion: `Bool` renders `true`/`false`, `Float`
  renders in Go shortest form, and an `*Array` render or an object without a
  stringify hook is an error rather than a placeholder word.
  (`04-types-and-semantics.md` Section 4.)
- `GetAttribute`, kind-dispatched and strict-by-default: a miss is an error unless
  the lenient flag is set. (`04-types-and-semantics.md` Sections 5-6.)
- The six escape strategies and the safeness analysis, active when escaping is on.
- `FromGo`, the host-facing marshaler: a native Go value (scalar, slice/array,
  map, struct honoring a `quill:"name"` or `json:"name"` tag, pointer, or an
  existing `runtime.Value` passed through) becomes a `Value`, with a
  deterministic sorted key order for maps and a clear typed error on an
  unsupported kind (channel, bare function, complex). The facade's `RenderValues`
  and `RenderStringValues` marshal a `map[string]any` through it before
  rendering. (`04-types-and-semantics.md` Section 6.)

The host-registration surface carries callable INJECTION FLAGS -- `NeedsCharset`,
`NeedsContext`, `NeedsEnvironment` -- so a registered filter/function/test declares
which of the active charset, the live context, and the environment handle it
requires, and the interpreter prepends those before the user arguments
(`03-stdlib.md` Section 3.6, `extensions.md` Section 6). The case filters and the
codepoint escapers need the charset; `include`/`block`/`parent`/`dump`/
`template_from_string` need context or environment, so the injection mechanism is
load-bearing.

--------------------------------------------------------------------------------

## 3. The gradual type checker

The `check` package is a front-end pass between parse and interpret. It parses the
type-annotation grammar (`02-grammar.md` Section 5) at the annotation sites and
attaches a `*Type` to the relevant AST nodes, walks the typed nodes, resolves
`Object<"Type">` against the host registry (`check.Registry`, the typing
counterpart of the sandbox type-graph), and emits `TypeCheck` diagnostics into the
error family. Where annotations are present it promotes the strict-by-default
runtime errors to check-time errors; it changes nothing at run time. A
fully-annotated and a fully-unannotated template render through the identical
interpreter path. (`04-types-and-semantics.md` Section 1.)

--------------------------------------------------------------------------------

## 4. Execution model

One shared AST plus a tree-walking interpreter is the primary backend. A
tree-walker pays a `Kind`-switch and a `Value` boxing per node per render; for the
batch generator workload (parse-once-per-process, bounded renders to produce
source) this is well within budget. A parse is memoized in the cache and reused
across renders. An ahead-of-time compile-to-Go backend is reserved by the
`runtime`-imports-nothing-from-parser discipline behind the `Template` interface,
but is not part of the current engine.

The postfix conditional desugars to a ternary at parse time, so the interpreter
needs no new node kind for it. The always-defined loop metadata drains a host
iterator to an `*Array` before the loop so `loop.index`/`loop.first`/`loop.last`/
`loop.length` are always available.

Coverage instrumentation is a set of interpreter hooks that read node positions
and increment counters through the attached `cover.Collector`. When no Collector
is attached each hook is a single nil-check, so coverage is zero-overhead when off
and never changes rendered bytes.
