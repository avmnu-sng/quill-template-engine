# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/avmnu-sng/quill-template-engine/compare/v0.1.0...HEAD
[v0.1.0]: https://github.com/avmnu-sng/quill-template-engine/releases/tag/v0.1.0
