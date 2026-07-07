# API

The canonical Go API reference for Quill is
[pkg.go.dev/github.com/avmnu-sng/quill-template-engine](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine),
generated from the exported doc comments and runnable examples. This page is a
thin index into it; pkg.go.dev is authoritative for signatures, and this guide
site is the narrative layer.

## Where to look

- **The facade** -- [`quill`](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine):
  `Environment`, `New`, `NewFromMap`, `Render`, `RenderValues`, `RenderTo`, and
  the `Option` values (`WithAutoescapeHTML`, `WithStrictVariables`, `WithCoverage`,
  `WithExtensions`, `WithExtension`, `WithSandboxPolicy`, `WithSandboxActive`,
  `WithTypes`, `WithTabWidth`, `WithCompiled`, `WithLogger`).
- **The value model** -- [`runtime`](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine/pkg/runtime):
  `Value`, the constructors (`Str`, `Int`, `Float`, `Bool`, `Arr`, `Null`), the
  ordered `*Array`, `Safe`, the `Object` interface, and `FromGo`.
- **Loaders** -- [`loader`](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine/pkg/loader):
  `Loader`, `NewFilesystemLoader`, `NewArrayLoader`, `NewChainLoader`,
  `NewPrefixLoader`, `NewFSLoader`, `NewFuncLoader`.
- **Extensions** -- [`ext`](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine/pkg/ext):
  `Filter`, `Function`, `Test`, `ExtensionSet`, `Extension`, `BaseExtension`, and
  the typed helpers `NewFilter`, `NewFunction`, `NewTest`, `NewFilter1`.
- **Coverage** -- [`cover`](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine/pkg/cover):
  `Collector`, `Report`, the writers, and `MergeReports`.
- **The sandbox** -- [`sandbox`](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine/pkg/sandbox):
  `Policy`, `NewTypeGraph`.
- **The type checker** -- [`check`](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine/pkg/check):
  `Registry`, `Signature`, `Type`.
- **The compile backend** -- [`compile`](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine/pkg/compile)
  and [`compiled`](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine/pkg/compiled):
  `Module`, `Options`, `Result`, and the `Manifest` contract `WithCompiled`
  installs.
- **Errors** -- [`errors`](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine/pkg/errors):
  the structured error family, including `*errors.Security` for sandbox
  violations.

## Examples

Runnable `ExampleXxx` functions in the package populate the **Examples** tab on
pkg.go.dev and double as tests, so the snippets there compile and pass on every
build. The guide pages here cross-link into the same API for the mechanism behind
each feature:

- [Getting Started](getting-started.md) -- the facade and the render options.
- [Extensions & Loaders](extensions.md) -- the `ext` and `loader` packages in
  depth.
- [Coverage](coverage.md) -- the `cover` API and the go-test integration pattern.
- [Architecture](architecture.md) -- how the packages are layered.

## Stability

Quill is under active development and its API is not yet stable (pre-1.0). Breaking
changes are noted in the [Changelog](changelog.md). Pin a version in your
`go.mod` and review the changelog before upgrading.
