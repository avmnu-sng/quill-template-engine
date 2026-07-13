# Architecture

This document describes how Quill's packages are layered, the load-bearing runtime
boundary, the value layer, the gradual type checker, and the two execution
backends: the tree-walking interpreter and the shipped compile-to-Go backend.
The language itself is specified in the [Language Reference](reference/language.md)
and [Grammar](reference/grammar.md); the extension API is in
[Extensions & Loaders](extensions.md).

Quill is a front end (lexer, parser, and gradual type checker) over a
tree-walking runtime, with a compile-to-Go backend behind the same `Template`
contract. The Go module is `github.com/avmnu-sng/quill-template-engine`, and the
runtime depends on nothing outside the Go standard library.

## Package layout and the load-bearing boundary

Quill is a layered package set with dependencies flowing downward only. The
critical boundary is that `runtime` imports nothing from AST evaluation or
parsing, so the correctness budget (the comparisons, attribute access,
truthiness, string coercion, and escape) is spent once in `runtime` and is
fixture-testable in isolation. The interpreter depends only on `runtime`, and the
compile-to-Go backend is a pure addition behind the same `Template` contract.

```
quill/           facade: Environment, options, the render entry points, engine callables
  pkg/           the frozen public API, semver-stable from v1.0.0:
    source/      Source value object; CRLF normalization
    ast/         AST node (uniform struct, ordered mixed-key children, Kind discriminator)
    parse/       the parser: LL statement parser + Pratt expression loop
    check/       the gradual type checker: a front-end pass between parse and interpret
    runtime/     the value layer: Value taxonomy, ordered *Array, Safe, Context, Output;
                 Equal/Order/Same, Truthy, ToText, GetAttribute (kind-dispatch,
                 strict-by-default), the six-strategy Escape, and the Object protocol
    compiled/    the manifest contract between generated render functions and the
                 Environment's compiled dispatch (a leaf: stdlib + ext + runtime only)
    ext/         the callable registry: Filter/Function/Test, the Set registry, Bundle,
                 the typed NewFilter/NewFunction/NewTest helpers, and the core standard library
    loader/      Loader, FilesystemLoader, ArrayLoader, ChainLoader, PrefixLoader, FSLoader, FuncLoader
    cache/       the parse cache and the rendered-body cache backing @cache
    errors/      the structured error family, carrying source context and TypeCheck diagnostics
    sandbox/     the sandbox Policy (the spec's SecurityPolicy) and host TYPE-GRAPH
    cover/       the coverage Collector, Report, and the text/LCOV/HTML writers
  internal/      engine internals, not importable, exempt from the API freeze:
    lex/         the two-mode TEXT/CODE lexer: sigil predicate, @-sigil statement lead
                 (default) and leading-keyword statement test (pragma bare), verbatim,
                 trim modifiers, line directive
    interp/      the tree-walking interpreter: for-loop scope, include/block/macro dispatch,
                 composition, escaping regions, sandbox gating, coverage hooks
    compile/     the compile-to-Go backend: lowers a template (or a multi-template unit)
                 to Go source (a render function plus a dispatch manifest)
    covercore/   the coverage instrumentation core (region map, Hit/Seed) the interpreter drives
    jsonval/     converts decoded JSON into runtime values for the CLI data path
  cmd/quill/     the command: render, the cover subcommand, and the compile subcommand
```

The `runtime`-imports-nothing-from-parser discipline is what keeps the value model
independently testable and lets both backends consume the same value semantics.

## The value layer

The `runtime` package holds the load-bearing value semantics:

- The `*Array` (ordered map with value-copy machinery), the `Context`
  save/restore, the `Safe` wrapper, and the `Output` sink.
- One typed equality (`Equal`), one ordering (`Order`), and a thin identity
  (`Same`).
- One `Truthy`, with `is empty` and `default` expressed over truthiness, length,
  and definedness.
- `ToText`, the byte-exact string coercion: `Bool` renders `true`/`false`, `Float`
  renders in Go shortest form, and an `*Array` render or an object without a
  stringify hook is an error rather than a placeholder word.
- `GetAttribute`, kind-dispatched and strict-by-default: a miss is an error unless
  the lenient flag is set.
- The six escape strategies and the safeness analysis, active when escaping is on.
- `FromGo`, the host-facing marshaler: a native Go value (scalar, slice/array,
  map, struct honoring a `quill:"name"` or `json:"name"` tag, pointer, or an
  existing `runtime.Value`) becomes a `Value`, with a deterministic key order for
  maps and a clear typed error on an unsupported kind. `RenderValues` and
  `RenderStringValues` marshal a `map[string]any` through it before rendering.

The host-registration surface carries callable injection flags (`NeedsCharset`,
`NeedsContext`, `NeedsEnvironment`), so a registered filter/function/test declares
which of the active charset, the live context, and the environment handle it
requires, and the interpreter prepends those before the user arguments.

The value semantics are documented for template authors in [Types](types.md).

## The gradual type checker

The `check` package is a front-end pass between parse and interpret. It parses the
type-annotation grammar at the annotation sites and attaches a type to the relevant
AST nodes, walks the typed nodes, resolves `Object<"Type">` against the host
registry (`check.Registry`, the typing counterpart of the sandbox type-graph), and
emits `TypeCheck` diagnostics into the error family. Where annotations are present
it promotes the strict-by-default runtime errors to check-time errors; it changes
nothing at run time. A fully-annotated and a fully-unannotated template render
through the identical interpreter path. See [Types](types.md).

## Execution model

Quill has two backends behind one `Template` contract.

### The tree-walking interpreter (default)

One shared AST plus a tree-walking interpreter is the default backend. A
tree-walker pays a `Kind`-switch and a value boxing per node per render; for a
parse-once, render-many workload this is well within budget, and a parse is
memoized in the cache and reused across renders. The postfix conditional desugars
to a ternary at parse time, so the interpreter needs no new node kind for it. The
always-defined loop metadata drains a host iterator to an `*Array` before the loop
so `loop.index`/`loop.first`/`loop.last`/`loop.length` are always available.
Coverage instrumentation is a set of interpreter hooks that read node positions and
increment counters; when no collector is attached each hook is a single nil-check,
so coverage is zero-overhead when off and never changes rendered bytes.

### The compile-to-Go backend (shipped)

The internal compile backend (`internal/compile`, driven by the `quill compile`
command) generates Go source for the hot path: a render function plus a dispatch
manifest that a host installs with `WithCompiled`. The generated code emits
body literals as constants, inlines loop metadata, and skips
the per-node dispatch, `Context`, and copy-on-write the interpreter pays, so it
renders several times faster on loop-heavy workloads (see
[Performance](performance.md)).

The `compiled` package is a leaf (it imports only the standard library, `ext`, and
`runtime`) that defines the manifest contract between the generated code and the
Environment. A `Manifest` carries the entry template name, every member
template's source text, a `Fingerprint` of the compile options that shape rendered
bytes (escape strategy, undefined-handling mode, tab width, seed), and the render
entry point. The Environment serves a by-name render through the compiled function
only when the fingerprint matches its own configuration field for field and every
member source byte-equals the text its loader currently serves; anything
unprovable falls back to the interpreter. So installing a manifest can change
render speed but never rendered bytes. A verify mode (`WithCompiledVerify`) runs
both backends and reports any divergence to a host callback, always serving the
interpreter's result, so a deployment can measure trust in a unit before switching
it to direct compiled dispatch.

A construct outside the compilable subset makes the backend report a
not-compilable error naming the construct, at compile time; such a template stays
on the interpreter. The CLI's `compile` subcommand ([CLI](cli.md)) drives the
backend from the command line.

## Next

- [Performance](performance.md): benchmark methodology and the compiled-vs-
  interpreter numbers.
- [Types](types.md): the value model and the gradual type system.
- [API](api.md): the exported Go surface, indexed to pkg.go.dev.
