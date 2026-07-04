# Performance

Quill is fast on two fronts: the tree-walking interpreter is competitive with the
Go standard library on small templates, and the compile-to-Go backend takes the
hot path several times faster still. This page presents the benchmark
methodology, the numbers, and how to reproduce them.

!!! note "Absolute numbers are machine-dependent"
    The nanosecond figures below were measured on one machine (Apple M2 Max, Go
    1.26, `darwin/arm64`). Absolute latencies vary with CPU, Go version, and
    build flags; the **ratios** between engines measured in the same run are the
    portable signal. Reproduce them on your own hardware with the commands under
    [Reproducing](#reproducing-the-numbers).

## The benchmark harness

The benchmarks live in the [`bench/`](https://github.com/avmnu-sng/quill-template-engine/tree/main/bench)
directory, a separate nested module so the engine itself stays
standard-library-only. Three workloads exercise different cost centers, each
rendering the same logical input across engines:

- **Tiny** -- a single interpolation with one filter: `Hello {{ name | upper }}!`.
- **Loop** -- a `@for` over 100 records, emitting an indexed line per row with an
  `upper` filter and two field reads.
- **Compose** -- `@extends` + overriding `@block`s + `parent()` + a loop, the
  template-inheritance path.

A verification test asserts that every engine renders byte-identical output for
the shared data before any timing is taken, so the benchmarks compare engines
doing the same work.

## Results

Measured with `go test -bench` on Apple M2 Max, Go 1.26 (`darwin/arm64`), render
phase only (templates parsed once, outside the timed loop):

| Workload | Quill interpreter | Quill compiled | Go `text/template` |
|----------|-------------------|----------------|--------------------|
| Tiny (render) | ~0.29 us | -- | ~0.39 us |
| Loop, 100 rows (render) | ~40.5 us | ~14.0 us | ~85.3 us |
| Compose (render) | ~10.9 us | -- | -- |

Reading the table:

- **The interpreter beats `text/template` on the tiny template** (~0.29 us vs
  ~0.39 us) and on the 100-row loop (~40.5 us vs ~85.3 us), while carrying a
  larger feature set (gradual types, whitespace control, composition).
- **The compiled loop is roughly 2.9x faster than the interpreter** on the same
  workload (~14.0 us vs ~40.5 us), because the generated Go emits body literals as
  constants, inlines `loop.index`, and skips the per-node dispatch, `Context`, and
  copy-on-write the interpreter pays.

The compiled figure is measured against a hand-written stand-in that renders the
loop workload the way the compile backend lowers it (a proof-of-ceiling in
`bench/compiled_poc_test.go`, asserted byte-identical to the interpreter). The
shipped backend (package `compile`, installed with `WithCompiled`) targets that
ceiling; see [Architecture](architecture.md) for how the compiled path
dispatches.

## The compile-to-Go backend

Templates run on the tree-walking interpreter by default. For the hot path, the
compile backend (package `compile`) generates Go source -- a render function plus
a dispatch manifest -- that you install with `WithCompiled`:

```go
env := quill.New(ldr, quill.WithCompiled(qtpl.Manifest))
```

The Environment serves a by-name render through the generated function only when
its fingerprint (escape strategy, undefined-handling mode, tab width, seed)
matches the Environment's configuration and every member template's source byte-
equals what the loader currently serves; anything unprovable falls back to the
interpreter. So installing a compiled unit can change render speed but never
rendered bytes. Generate a unit with the CLI:

```
quill compile -root templates -pkg qtpl -o index_gen.go index.ql
```

See the [CLI](cli.md) for the `compile` subcommand and [Architecture](architecture.md)
for the backend design.

## Why the interpreter is already fast

The tree-walking interpreter pays a `Kind`-switch and a value boxing per node per
render, which is well within budget for typical render workloads: a parse is
memoized in the cache and reused across renders, the postfix conditional desugars
to a ternary at parse time (no extra node kind), and coverage instrumentation is a
single nil-check when no collector is attached. The value layer spends its
correctness budget once in the `runtime` package -- one equality, one ordering,
one truthiness, one `ToText` -- so the hot path does no per-site algorithm
selection.

## Reproducing the numbers

From the repository root:

```
cd bench
go test -run '^$' -bench 'Render' -benchmem -count=3
```

That runs the offline Quill-vs-stdlib benchmarks with zero external dependencies.
To include the Twig/Jinja-family peers (pongo2, stick) in the comparison, fetch
them into the bench module and pass the build tag:

```
cd bench
go get github.com/flosch/pongo2/v6@v6.1.0 github.com/tyler-sommer/stick@v1.0.10
go test -tags thirdparty -bench=. -benchmem
```

The peers run in the same Go runtime, so the timing is fair, but their feature
model differs from Quill (Twig/Jinja semantics, HTML autoescape defaults), so
treat those as a same-runtime peer comparison rather than a like-for-like
language comparison. See [Comparison](comparison.md) for the capability matrix.

## Next

- [Comparison](comparison.md) -- the neutral capability matrix vs other Go
  engines.
- [Architecture](architecture.md) -- the interpreter and the compile-to-Go
  backend.
- [CLI](cli.md) -- the `compile` subcommand.
