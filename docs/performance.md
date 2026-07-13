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
directory, a **separate nested module** so the engine itself stays
standard-library-only: the peer engines are dependencies of the harness alone,
never of Quill. Three workloads exercise different cost centers, each rendering
the same logical input across engines:

- **Tiny** is a single interpolation with one filter: `Hello {{ name | upper }}!`.
- **Loop** is a `@for` over records, emitting an indexed line per row with an
  `upper` filter and two field reads.
- **Compose** is `@extends` + overriding `@block`s + `parent()` + a loop, the
  template-inheritance path.

A verification test asserts that every engine renders byte-identical output for
the shared data before any timing is taken, so the benchmarks compare engines
doing the same work.

### Engines compared

The default build measures Quill against the Go standard library only; the four
third-party peers are gated behind the `thirdparty` build tag so the default
suite runs offline with zero external dependencies.

| Group | Engines | Build |
|-------|---------|-------|
| Quill | interpreter (default), compiled (`compile` backend) | offline |
| Standard library | `text/template`, `html/template` | offline |
| Third-party peers | [pongo2](https://github.com/flosch/pongo2), [stick](https://github.com/tyler-sommer/stick), [jet](https://github.com/CloudyKit/jet), [quicktemplate](https://github.com/valyala/quicktemplate) | `-tags thirdparty` |

The peers run in the same Go runtime, so the timing is fair, but their feature
model differs from Quill (Twig/Jinja semantics, HTML autoescape defaults,
compile-ahead codegen for quicktemplate), so treat them as a same-runtime peer
comparison rather than a like-for-like language comparison. See
[Comparison](comparison.md) for the capability matrix.

### Methodology

- **`b.Loop`** drives every render benchmark (Go 1.24+), which keeps the timed
  work alive against dead-code elimination and amortizes loop overhead more
  predictably than a manual `b.N` loop.
- **`SetBytes` -> MB/s**: each render benchmark renders once outside the timed
  loop to size `b.SetBytes(len(output))`, so `go test` reports render throughput
  in MB/s alongside ns/op, a size-normalized figure that stays comparable as a
  workload grows.
- **Size-parameterized scaling**: the Loop workload sweeps `n = 1, 10, 100,
  1000` rows as sub-benchmarks (`.../n=100`), so per-engine scaling from a single
  row to a thousand is visible rather than pinned to one size.
- **`b.ReportAllocs`** is on for every benchmark, so allocations/op ships next to
  the timing.
- **Parse once, render many**: templates are parsed outside the timed loop, so
  the render benchmarks measure the render phase, not one-time compilation. The
  `_Load` benchmarks measure the parse/compile phase separately.
- **benchstat**: run the suite repeatedly (`-count`) and summarize the
  distribution (mean +/- variation) with
  [benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat) rather than
  trusting a single noisy run. It is wired up as `task bench:stat` (see
  [Reproducing](#reproducing-the-numbers)).

## Results

The table below is **illustrative, not a guarantee**. It was measured with `go
test -bench` on one machine (Apple M2 Max, Go 1.26, `darwin/arm64`), render phase
only (templates parsed once, outside the timed loop), medians of six runs.
Absolute numbers vary by machine and Go version; reproduce them locally with
`task bench:all` and read the same-run *ratios*, not the nanoseconds:

| Workload | Quill interpreter | Quill compiled | Go `text/template` |
|----------|-------------------|----------------|--------------------|
| Tiny (render) | ~0.31 us | -- | ~0.41 us |
| Loop, 100 rows (render) | ~41.5 us | ~10.2 us | ~88.4 us |
| Compose (render) | ~11.3 us | -- | -- |

Reading the table (directionally, from that run):

- **The interpreter is competitive with or beats `text/template`** on the tiny
  template (~0.31 us vs ~0.41 us) and on the 100-row loop (~41.5 us vs ~88.4 us),
  while carrying a larger feature set (gradual types, whitespace control,
  composition).
- **The compiled loop is several times faster than the interpreter** on the same
  workload (~10.2 us vs ~41.5 us, roughly 4x here), because the generated Go emits
  body literals as constants, inlines `loop.index`, and skips the per-node
  dispatch, `Context`, and copy-on-write the interpreter pays.

The third-party peers (pongo2, stick, jet, quicktemplate) are omitted from this
static table on purpose: their standing against Quill shifts with workload and
version, so run `task bench:all` and compare in your own environment rather than
trusting a frozen ranking here.

The compiled figure is the **real shipped compile backend**: the render function
the `quill compile` command emits, the same unit `WithCompiled` installs,
benchmarked by `BenchmarkCompiledReal_Loop_Render` over the committed generated
source in `bench/compiled_loop_gen.go`. A staleness test regenerates that source
in-memory and fails if it drifts from the backend's current output, and a parity
test pins its bytes to the interpreter's, so the number tracks the actual
generated code rather than a description of it.

The harness also keeps a hand-written proof-of-ceiling
(`BenchmarkCompiled_Loop_Render` in `bench/compiled_poc_test.go`, ~14.4 us),
which renders the loop the way the backend lowers it as an independent bound on
what the compiled path can reach; the shipped backend meets and beats it here
because it buffers its writes. See [Architecture](architecture.md) for how the
compiled path dispatches.

## The compile-to-Go backend

Templates run on the tree-walking interpreter by default. For the hot path, the
compile-to-Go backend (reached through the `quill compile` command) generates
Go source (a render function plus a dispatch manifest) that you install with
`WithCompiled`:

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
quill compile -root templates -pkg qtpl -o index_gen.go index.quill
```

See the [CLI](cli.md) for the `compile` subcommand and [Architecture](architecture.md)
for the backend design.

## Why the interpreter is already fast

The tree-walking interpreter pays a `Kind`-switch and a value boxing per node per
render, which is well within budget for typical render workloads: a parse is
memoized in the cache and reused across renders, the postfix conditional desugars
to a ternary at parse time (no extra node kind), and coverage instrumentation is a
single nil-check when no collector is attached. The value layer spends its
correctness budget once in the `runtime` package (one equality, one ordering,
one truthiness, one `ToText`), so the hot path does no per-site algorithm
selection.

## Reproducing the numbers

The [Taskfile](https://github.com/avmnu-sng/quill-template-engine/blob/main/Taskfile.yml)
wraps every run so you do not have to remember the flags or `cd bench` (each
`bench:*` target runs inside the nested module for you):

| Task | What it runs |
|------|--------------|
| `task bench` | offline suite (Quill + stdlib), zero external deps |
| `task bench:all` | full suite including the four thirdparty peers (`-tags thirdparty`) |
| `task bench:stat` | full suite `-count=10`, summarized with benchstat (mean +/- variation) |
| `task bench:compare` | full suite vs the committed `bench/baseline.txt`, deltas via benchstat |
| `task bench:baseline` | regenerate `bench/baseline.txt` (same run as `bench:stat`) |
| `task bench:profile` | one hot benchmark with CPU/mem profiles into `bench/prof/` |

`task bench` runs the offline Quill-vs-stdlib benchmarks with zero external
dependencies, including `BenchmarkCompiledReal_Loop_Render` (the shipped compile
backend) and `BenchmarkCompiled_Loop_Render` (the proof-of-ceiling).
The generated render function `bench/compiled_loop_gen.go` is committed, so the
real-backend benchmark builds with no manual pre-step; regenerate it after a
compiler change with `go generate ./...` (or `go run genloop.go`) from the
`bench` directory. A plain `go test ./...` in `bench` runs the parity and
staleness guards that keep that generated source honest.

`task bench:all` adds the third-party peers (pongo2, stick, jet, quicktemplate)
via the `thirdparty` build tag; their module dependencies resolve automatically
from `bench/go.mod`. Equivalent by hand:

```
cd bench
go test -tags thirdparty -bench=. -benchmem -run='^$' ./...
```

### Stable numbers with benchstat

A single `-bench` run is noisy. `task bench:stat` runs the suite ten times and
pipes the output through
[benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat), which reports
each benchmark's mean and run-to-run variation so you can tell a real change from
sampling noise. (`task install:tools` installs benchstat; the target falls back
to `go run` if it is not on `PATH`.)

To track deltas over time, `bench/baseline.txt` holds a committed reference run.
`task bench:compare` measures the current tree against it with benchstat, and
`task bench:baseline` regenerates that file. It is committed and refreshed
**intentionally**. After a deliberate performance change, regenerate it on a
quiet machine and commit the update so the comparison stays meaningful. Because
absolute numbers are machine-specific, `bench:compare` is most useful for a
before/after on the *same* machine.

To profile a hot path, `task bench:profile` runs the loop-render benchmark with
`-cpuprofile`/`-memprofile` into `bench/prof/` and prints the `go tool pprof`
command to open them.

## Next

- [Comparison](comparison.md): the neutral capability matrix vs other Go
  engines.
- [Architecture](architecture.md): the interpreter and the compile-to-Go
  backend.
- [CLI](cli.md): the `compile` subcommand.
