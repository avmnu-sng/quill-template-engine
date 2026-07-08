# Migrating to v1.0.0

v1.0.0 is Quill's first stable release. From this version the exported API
follows semantic versioning: no exported symbol in the root package or the
`pkg/` packages changes incompatibly within the v1 series. To get there, v1.0.0
batches every remaining breaking change into a single window -- so the jump from
v0.3.0 carries an unusually large BREAKING set, and after it compatibility is the
rule.

This guide maps each old API to its v1.0.0 replacement. Most hosts only import
the root package plus `runtime` and `loader`, so the changes you are most likely
to hit are the context parameter, the opaque `runtime.Value`, and the `ext`/
`sandbox` renames -- start there.

## At a glance

| Area | v0.3.0 | v1.0.0 |
|------|--------|--------|
| Render / load | `env.Render("t", vars)` | `env.Render(ctx, "t", vars)` -- `context.Context` first |
| In-memory env | `quill.NewWithArray(m)` | `quill.NewFromMap(m)` |
| Read a value | `v.S`, `v.I`, `v.Kind` (fields) | `v.AsStr()`, `v.AsInt()`, `v.Kind()` (accessors) |
| Prepared template | `*interp.Template` with `Block()`/`Macro()` | opaque `*quill.Template`; render with `RenderPrepared` |
| Sandbox policy | `&sandbox.Policy{Filters: ...}` | `sandbox.NewPolicy(sandbox.AllowFilters(...))` |
| Callable set | `ext.NewExtensionSet()` / `*ext.ExtensionSet` | `ext.NewSet()` / `*ext.Set` |
| Bundle interface | `ext.Extension` | `ext.Bundle` |
| Callable body | `func(args []runtime.Value) (...)` | `func(ctx context.Context, args []runtime.Value) (...)` |
| Stream to writer | `env.Display(w, "t", vars)` | `env.RenderTo(ctx, w, "t", vars)` |
| Error position | `err.Line`, `err.Col` (fields) | `err.Line()`, `err.Col()` (methods) |
| Security cause | `sec.Err` (field) | `errors.Unwrap(sec)` / `errors.As` |
| Coverage hooks | `coll.Hit(...)`, `coll.SeedTemplate(...)` | removed (engine-internal) |
| AOT compile | `import ".../pkg/compile"` | the `quill compile` command |
| Not-found check | text match on `"not found"` | `errors.Is(err, loader.ErrNotFound)` |
| Coverage gate exit | `1` (shared with errors) | `2` (distinct); `-threshold` -> `-fail-under` |

## 1. `context.Context` on every render and load

Every render and load entry point now takes a `context.Context` as its first
argument -- `Render`, `RenderTo`, `RenderString`, `RenderStringTo`,
`RenderValues`, `RenderToValues`, `RenderStringValues`, `LoadTemplate`,
`CompileString`, and the new `RenderPrepared`. A long render, or a host callable
doing I/O, can now be cancelled with the context; an uncancelled render produces
byte-identical output to before.

```go
// v0.3.0
out, err := env.Render("greet.quill", vars)

// v1.0.0
out, err := env.Render(context.Background(), "greet.quill", vars)
```

Pass a request's context where you have one (an HTTP handler's `r.Context()`, a
`t.Context()` in tests) and `context.Background()` where you do not.

## 2. `runtime.Value` is opaque

`runtime.Value`'s payload fields are unexported. Read a value through `Kind()`
and the typed accessors instead of reaching into fields:

```go
// v0.3.0
if v.Kind == runtime.KStr {
    s := v.S
}

// v1.0.0
if v.Kind() == runtime.KStr {
    s := v.AsStr()
}
```

The accessors are `AsBool()`, `AsInt()`, `AsFloat()`, `AsStr()`, `AsArray()`, and
`AsObject()`, plus `IsNull()`/`IsScalar()`. Each returns its field's zero value if
called on the wrong kind, so gate on `Kind()` first. Construct values with the
existing constructors (`runtime.Str`, `runtime.Int`, `runtime.Arr`, ...), which
are unchanged. `Value`'s size and copyability are preserved, so it remains valid
inside a compiled `RenderFunc` vars map.

## 3. Opaque prepared templates and `RenderPrepared`

`LoadTemplate` and `CompileString` now return an opaque `*quill.Template` rather
than the internal interpreter template. The handle exposes a curated inspection
surface -- `Name()`, `BlockNames()`, `HasBlock(name)`, `HasMacro(name)` -- and no
longer hands out AST-returning `Block()`/`Macro()` accessors. Render a prepared
handle with `RenderPrepared`, the parse-once/render-many path:

```go
// v1.0.0
tmpl, err := env.LoadTemplate(ctx, "page.quill")
// ... inspect tmpl.BlockNames(), tmpl.HasMacro("row"), ...
out, err := env.RenderPrepared(ctx, tmpl, vars)
```

## 4. `sandbox.Policy` is built, not literal

A `Policy` is now opaque and built with `sandbox.NewPolicy` and its functional
options; the raw allowlist fields are gone.

```go
// v0.3.0
pol := &sandbox.Policy{
    Filters: map[string]bool{"upper": true},
    Tags:    map[string]bool{"if": true, "for": true},
    Graph:   sandbox.NewTypeGraph(),
}

// v1.0.0
pol := sandbox.NewPolicy(
    sandbox.AllowTags("if", "for"),
    sandbox.AllowFilters("upper"),
)
```

The options are `AllowTags`, `AllowFilters`, `AllowFunctions`,
`AllowMethods(typeName, methods...)`, `AllowProperties(typeName, props...)`,
`Strict()`, and `WithTypeGraph(g)`. Anything not allowed is denied. Read a built
policy through its `AllowsTag`/`AllowsFilter`/... accessors. The same opaque-with-
constructor treatment applies to `check.Type`/`ObjectType`/`Signature` and to
`compiled.Manifest`/`Fingerprint` (build the latter with `NewManifest`/
`NewFingerprint`).

## 5. `ext` renames and the callable context

The extension types were renamed to drop the package stutter, and the host
callable signature gained a context:

- `ext.ExtensionSet` -> `ext.Set`; `ext.NewExtensionSet()` -> `ext.NewSet()`.
- `ext.Extension` (the bundle interface) -> `ext.Bundle`.
- `Filter.Fn`, `Function.Fn`, and `Test.Fn` now take `ctx context.Context` first;
  `Filter.Fn1` likewise.

```go
// v0.3.0
set := ext.NewExtensionSet()
set.AddFilter(&ext.Filter{
    Name: "shout",
    Fn: func(args []runtime.Value) (runtime.Value, error) { /* ... */ },
})

// v1.0.0
set := ext.NewSet()
set.AddFilter(&ext.Filter{
    Name: "shout",
    Fn: func(ctx context.Context, args []runtime.Value) (runtime.Value, error) { /* ... */ },
})
```

The `WithExtensions` (takes `*ext.Set` layers) and `WithExtension` (takes
`ext.Bundle` values) option names are unchanged. The typed helpers `NewFilter`/
`NewFunction`/`NewTest` still wrap a plain Go function over Go types and do not
take a context in the wrapped body; only the raw `Fn`/`Fn1` and the `NewFilter1`
callback receive `ctx`.

## 6. Root-package renames and removals

- `quill.NewWithArray(map[string]string)` -> `quill.NewFromMap(...)` (it always
  took a map, never an array).
- `Environment.Display(w, name, vars)` is removed -- use
  `RenderTo(ctx, w, name, vars)`.
- The renderer-internal `Environment` getters are no longer public:
  `StrictVariables`, `AutoescapeHTML`, `Policy`, `SandboxActive`, `Coverage`,
  `TabWidth`, `Logger`, `TemplateExists`, and `RawSource`. Configure the behavior
  through the `With*` options at construction instead of reading it back off the
  `Environment`. (`Extensions()` and `RenderCache()` remain.)

## 7. `errors`: methods for position, hidden cause, new class

- An `*errors.Error`'s position is read through the `Src()`, `Line()`, and
  `Col()` methods instead of struct fields.
- The wrapped `*Error` inside an `*errors.Security` is unexported; reach it with
  `errors.Unwrap`/`errors.As` (or the `Security`'s own `Src()`/`Line()`/`Col()`).
- A dedicated `errors.SecUnknownType` class distinguishes an unregistered host
  type from a denied-but-known member; `errors.SecurityUnknownType(type, member)`
  constructs it.

```go
// v0.3.0
line := err.Line

// v1.0.0
line := err.Line()
```

Continue to classify failures with `errors.As`/`errors.Is` on the exported
`Kind`/`SecurityClass`, which are unchanged.

## 8. `cover`: instrumentation hooks are internal

`cover.Collector`'s record-side hooks -- `Hit`, `SeedTemplate`, `SeedMacro` --
are removed from the public API. They were only ever driven by the interpreter,
never by hosts. Keep using the report side: attach a `Collector` with
`WithCoverage`, then consume `Report()`, its `WriteText`/`WriteLCOV`/`WriteHTML`
writers, `FailUnder`, `Merge`, `Templates`/`Totals`, and `MergeReports`.

## 9. Engine internals moved under `internal/`

The lexer (`lex`), the tree-walking interpreter (`interp`), and the compile-to-Go
backend (`compile`) moved under `internal/` and are no longer importable. They
had no host use case and leaked large, fragile surfaces. If you imported
`.../pkg/compile` to generate code ahead of time, switch to the `quill compile`
command, which emits the same manifest you install with `WithCompiled`:

```
quill compile -root templates -pkg qtpl -o index_gen.go index.quill
```

```go
// v1.0.0 -- install the generated unit
env := quill.New(ldr, quill.WithCompiled(qtpl.Manifest))
```

## 10. `loader.ErrNotFound` replaces text matching

Loader misses now wrap the exported sentinel `loader.ErrNotFound`, and
`loader.IsNotFound` is `errors.Is(err, loader.ErrNotFound)` -- it no longer
matches unrelated errors whose message merely contains "not found". Classify a
miss with the sentinel:

```go
// v1.0.0
if errors.Is(err, loader.ErrNotFound) { /* handle the miss */ }
```

More broadly, error **message strings are not part of the compatibility
contract**: branch on the exported `Kind`, with `errors.As`/`errors.Is`, or
against a sentinel such as `loader.ErrNotFound` -- never by matching message
text.

## 11. CLI: coverage-gate exit code and flag

The `quill cover` CI gate now exits with code **2** when total unit coverage is
below the threshold, distinct from a hard error's exit **1**, so CI can tell a
coverage shortfall from a real failure. The redundant `-threshold` alias is
removed -- use `-fail-under`.

## Checklist

- [ ] Thread a `context.Context` into every `Render*`/`Load*`/`RenderPrepared`
      call (`context.Background()` where you have none).
- [ ] Replace `runtime.Value` field reads with `Kind()` + `AsX()` accessors.
- [ ] `NewWithArray` -> `NewFromMap`; `Display` -> `RenderTo`; drop reads of the
      removed `Environment` getters.
- [ ] `ext.ExtensionSet`/`NewExtensionSet` -> `ext.Set`/`NewSet`; `ext.Extension`
      -> `ext.Bundle`; add `ctx context.Context` to raw `Fn`/`Fn1` bodies.
- [ ] Rebuild `sandbox.Policy` with `sandbox.NewPolicy(...)`.
- [ ] `err.Line`/`err.Col` -> `err.Line()`/`err.Col()`; reach a `Security`'s cause
      via `errors.Unwrap`/`errors.As`.
- [ ] Drop any import of `.../pkg/compile`, `.../pkg/interp`, `.../pkg/lex`; use
      the `quill compile` command for AOT.
- [ ] Replace not-found text matching with `errors.Is(err, loader.ErrNotFound)`.
- [ ] Update CI to `-fail-under` and treat exit code `2` as a coverage shortfall.

See the [Changelog](changelog.md) for the full v1.0.0 entry, and the
[API index](api.md) for where each symbol lives on
[pkg.go.dev](https://pkg.go.dev/github.com/avmnu-sng/quill-template-engine).
