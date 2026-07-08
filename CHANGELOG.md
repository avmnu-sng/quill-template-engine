# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v1.0.0] - 2026-07-08

First stable release. From v1.0.0 the exported API follows semantic versioning:
no exported symbol in the root package or the `pkg/` packages changes
incompatibly within the v1 series. This release makes the pre-1.0 breaking
changes that shape that frozen surface, so it carries an unusually large
BREAKING set; after it, compatibility is the rule.

### Changed

- **BREAKING: `context.Context` is now the first argument of every render and
  load entry point** (`Render`, `RenderTo`, `RenderString`, `RenderStringTo`,
  `RenderValues`, `RenderToValues`, `RenderStringValues`, `LoadTemplate`,
  `CompileString`, and the new `RenderPrepared`), of the host callable signatures
  (`ext.Filter`/`Function`/`Test`), and of the compiled `RenderFunc` ABI. A long
  render or a host filter doing I/O can now be cancelled; pass
  `context.Background()` when you do not need it. Uncancelled renders are
  byte-identical to before.
- **BREAKING: the engine internals moved under `internal/` and are no longer
  importable** -- the lexer (`lex`), the tree-walking interpreter (`interp`), and
  the compile-to-Go backend (`compile`). Hosts use the root facade and, for AOT
  compilation, the `quill compile` command.
- **BREAKING: `runtime.Value` is now opaque.** Its payload fields are unexported;
  read them with `Kind()`, `AsBool()`, `AsInt()`, `AsFloat()`, `AsStr()`,
  `AsArray()`, `AsObject()`. Construct values with the existing constructors
  (`runtime.Int`, `Str`, ...). The struct's size and copyability are unchanged, so
  it remains valid inside the compiled `RenderFunc` vars map.
- **BREAKING: `LoadTemplate`/`CompileString` return an opaque `*quill.Template`**
  (methods `Name`, `BlockNames`, `HasBlock`, `HasMacro`) rather than the internal
  interpreter template; render a prepared handle with `RenderPrepared`.
- **BREAKING: opaque configuration and result types.** A `sandbox.Policy` is now
  built with `sandbox.NewPolicy(sandbox.AllowTags(...), sandbox.AllowFilters(...),
  sandbox.Strict(), ...)` instead of a struct literal; `check.Type`/`ObjectType`/
  `Signature` and `compiled.Manifest`/`Fingerprint` are opaque and
  constructor-built.
- **BREAKING: `ext` renames** -- `ext.ExtensionSet` -> `ext.Set`,
  `ext.Extension` -> `ext.Bundle`, `NewExtensionSet` -> `NewSet`. The
  `WithExtensions`/`WithExtension` option names are unchanged.
- **BREAKING: root-package renames and removals** -- `NewWithArray` ->
  `NewFromMap`; `Environment.Display` removed (use `RenderTo`); the
  renderer-internal `Environment` getters (`StrictVariables`, `AutoescapeHTML`,
  `Policy`, `SandboxActive`, `Coverage`, `TabWidth`, `Logger`, `TemplateExists`,
  `RawSource`) are no longer part of the public surface.
- **BREAKING: `errors` hardening** -- the wrapped error inside `errors.Security`
  is unexported (reach it via `Unwrap`); an `errors.Error`'s position is read
  through `Src()`/`Line()`/`Col()` methods instead of fields; a dedicated
  `errors.SecUnknownType` class distinguishes an unregistered host type from a
  denied-but-known member.
- **BREAKING: `cover.Collector`'s instrumentation hooks (`Hit`, `SeedTemplate`,
  `SeedMacro`) are removed from the public API.** They were engine-internal --
  driven by the interpreter, never by hosts, which consume coverage through the
  report methods (`Report`, `Summary`, `TemplateCoverage`, `Counts`,
  `MergeReports`). The instrumentation core moved to an internal package.
- Error MESSAGE strings are documented as NOT part of the compatibility contract:
  classify a failure by the exported `Kind`, with `errors.As`/`errors.Is`, or
  against a sentinel such as `loader.ErrNotFound` -- never by matching text.

### Added

- `loader.ErrNotFound`, a sentinel that every loader miss wraps; `loader.IsNotFound`
  is now `errors.Is(err, loader.ErrNotFound)` and no longer matches unrelated
  errors whose text happens to contain "not found".
- Native Go fuzz targets for the lexer (`FuzzLex`) and parser (`FuzzParse`).

### Fixed

- **The lexer honored its single-fault contract only by luck.** For input such as
  `{{$` the interpolation, statement-head, and block-close scanners emitted an
  ERROR token but the scan continued, re-emitting the faulting bytes as text --
  contradicting the documented "a single ERROR token immediately before EOF". The
  scan now stops at the first fault. Found by the new fuzz targets.
- `compile.ErrNotCompilable` was a mutable global any importer could corrupt; it
  is now a non-corruptible sentinel (`errors.Is` classification unchanged).

## [v0.3.0] - 2026-07-07

### Changed

- **BREAKING: the library packages moved under `pkg/`.** All fifteen internal
  and public packages now live under a single `pkg/` directory, leaning the repo
  root from seventeen top-level directories to eight. Update your imports:
  `.../runtime` -> `.../pkg/runtime`, `.../loader` -> `.../pkg/loader`, and
  likewise for `check`, `compile`, `compiled`, `cover`, `errors`, `ext`, and
  `sandbox` (the previously-internal `lex`/`ast`/`parse`/`source`/`interp`/`cache`
  moved from `core/` to `pkg/`). The module root import
  (`github.com/avmnu-sng/quill-template-engine`) and
  `go install .../cmd/quill@latest` are unchanged; most code only imports the
  root package plus `runtime`/`loader`.

### Fixed

- The documentation site rendered filter-pipe examples as `\|` instead of `|`
  inside table code spans (a mkdocs/python-markdown escaping quirk; GitHub was
  unaffected). Those cells now use an HTML entity so the pipe renders correctly.

## [v0.2.0] - 2026-07-06

### Security

- **Parser denial-of-service fixed.** A chain of nested parentheses made the
  arrow-vs-grouping lookahead (`parenIsArrow`) rescan all following tokens per
  `(`, so parsing was O(n^2): a ~220 KB template drove peak memory to ~1 GB and
  ~10 s of CPU, and extreme nesting could crash the process with a goroutine
  stack overflow -- all reachable through the public `Render` API. The lookahead
  is now O(1) via a one-pass bracket-match table, and a parser nesting-depth cap
  turns pathological input into a positioned syntax error instead. The same
  100k-paren input now parses in ~37 ms using tens of MB.

### Changed

- **BREAKING: the internal engine packages moved under `core/`.** `ast`, `cache`,
  `lex`, `parse`, `source`, and `interp` are now imported as `core/ast`,
  `core/cache`, `core/lex`, `core/parse`, `core/source`, and `core/interp`. Update
  the import path if you referenced any of them directly, or if you use the
  `compile`, `check`, or `cover` APIs whose signatures name `*ast.Node` (now
  `*core/ast.Node`). The documented public packages -- `runtime`, `loader`, `ext`,
  `cover`, `sandbox`, `check`, `compile`, `compiled`, `errors`, and `cmd/quill` --
  keep their import paths unchanged, as does `go get`/`go install`.

### Added

- **Error columns.** `errors.Error` now carries a 1-based `Col`, set via the new
  `AtPos(src, line, col)` method (`At` is preserved and fills a zero column).
  Syntax errors render as `name:line:col` when a column is known.
- **Editor support.** A VS Code extension with a TextMate grammar (`source.quill`)
  lives in `editors/vscode/`, and a `.gitattributes` rule maps template files to
  the Twig grammar on GitHub. The recommended template file extension is now
  `.quill`, which avoids the CodeQL `.ql` extension that GitHub Linguist claims.

### Fixed

- **Syntax diagnostics locate and name the fault.** Errors now include a column;
  an unterminated interpolation or block is reported at its opener rather than at
  end-of-input; and a delimiter fault names the literal token (`)`) instead of the
  internal label (`RPAREN`).
- `@tab` level coercion clamps to the platform `int` range, avoiding a wrap on
  32-bit targets.

## [v0.1.0] - 2026-07-04

Initial public release of Quill, a general-purpose, gradually-typed, fast
template engine for Go.

### Added

- **Language and interpreter.** A brace-delimited, keyword-led template language
  with an `@`-sigil statement form and a bare-brace form (`pragma bare`), pipe
  filters, arrow functions, and a Pratt-parsed expression language. Statements
  include `@for` (with `loop` metadata, `@else`, fused `if` filter, and
  `recursive` descent), `@if`/`@elseif`/`@else`, `@set` (with list and map
  destructuring and `capture`), `@with`, `@do`, `@log`, `@tab`, and the region
  and directive statements.
- **Composition.** `@extends`/`@block` with `parent()`, `@macro` with defaults
  and variadics, `@import`/`@from`, `@use` trait reuse, `@embed`, the statement-
  and function-form `@include`, `@call`/`caller()`, and `@provide`/`@yield`
  slots.
- **Gradual type system.** A static checker (package `check`) that runs between
  parse and interpret, consumes in-template annotations (`@types`, `@set`/`@for`
  targets, `@macro`/`@block` params and returns, arrow params), infers types
  where the spec defines it, and applies the gradual `any` fallback elsewhere. It
  rejects ill-typed templates with a positioned error before any byte renders,
  and narrows unions and nullables through `is` tests and null-safe access.
  Annotations never change runtime behavior.
- **Compile-to-Go backend.** A backend (package `compile`) that generates Go for
  the hot path; generated units install through `WithCompiled`, while the default
  path stays on the tree-walking interpreter.
- **Standard library.** A complete built-in catalogue of `snake_case` filters,
  functions, and tests: string and text-shaping filters, collection and
  higher-order filters (arrow-driven `map`/`filter`/`reduce`/`find`, `select`/
  `reject`, `group_by`, the `has some`/`has every` quantifiers), number and
  format helpers, and the scalar-kind, comparison, registry-existence, and type
  tests.
- **Whitespace control.** Three trim modes (hard, line, and a no-trim close),
  Jinja-style `trim_blocks`/`lstrip_blocks` cleanup applied by default, a
  `spaceless` filter and region, a `trim` filter, and a keep-close-newline
  pragma.
- **Escaping and the sandbox.** No output escaping by default;
  `WithAutoescapeHTML` for global HTML escaping, six escape strategies (`html`,
  `js`, `css`, `html_attr`, `html_attr_relaxed`, `url`) via the `escape`/`e`
  filter and `@escape` region, with safeness tracking. A policy sandbox
  (`sandbox.Policy`) restricts permitted tags, filters, functions, methods, and
  properties, activated globally, per `@sandbox` region, or per sandboxed
  include, with each violation raising a `*errors.Security`.
- **Coverage.** Native unit and branch coverage of templates (package `cover`)
  via `WithCoverage`, with text, LCOV, and HTML reports and a `FailUnder` gate.
  Coverage is opt-in and never changes rendered bytes.
- **Streaming.** `RenderTo`/`RenderStringTo` stream output to any `io.Writer`
  without buffering the whole result.
- **Go interop.** `RenderValues`/`RenderStringValues` accept ordinary Go values,
  marshaled through `runtime.FromGo` (scalars, slices, deterministically ordered
  maps, and structs honoring a `quill:"name"` or `json:"name"` tag).
- **Loaders and extensions.** Composable loaders (`Filesystem`, `FS`, `Chain`,
  `Prefix`, `Func`) and host-supplied filters, functions, and tests through the
  `ext` package (`WithExtensions`/`WithExtension`), with a defined shadow order.
- **Command-line tool.** The `quill` command renders a template with JSON data
  (`quill`) and reports coverage (`quill cover`) with text, LCOV, or HTML output
  and a `-fail-under` gate.

[Unreleased]: https://github.com/avmnu-sng/quill-template-engine/compare/v0.3.0...HEAD
[v0.3.0]: https://github.com/avmnu-sng/quill-template-engine/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/avmnu-sng/quill-template-engine/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/avmnu-sng/quill-template-engine/releases/tag/v0.1.0
