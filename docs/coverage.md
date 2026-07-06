# Quill -- Template Coverage

Quill measures which parts of a `.quill` template your tests actually exercised: which
statements and interpolations ran, and which arms of each branch were taken. This is
branch-aware coverage for templates, the analogue of `go tool cover` for Go, aggregated
across many renders and exported as a text summary, LCOV, or a highlighted HTML report.

Coverage answers questions a plain render count cannot: whether the `@else` arm of an `@if`
was ever taken, or whether a `@for` loop was ever entered with a non-empty collection. Quill
records exactly that, per template node and aggregated across every render in a test run.

Coverage is opt-in and zero-overhead when disabled: an Environment with no collector pays
no per-node cost on the render hot path. The instrumentation records reachability only; it
never changes rendered output. A template renders byte-identically with or without coverage
enabled -- this is the binding invariant the conformance suite enforces.

The companion documents are the [Language Reference](reference/language.md), the value
and semantics rules in [Types](types.md), and the [Architecture](architecture.md)
document.

--------------------------------------------------------------------------------

## 1. What coverage measures

Coverage is defined over the AST the parser builds (package `ast`): a uniform `Node` with a
`Kind` discriminator, a 1-based `Line`, a 1-based `Col`, and a `Src` naming the template.
Every coverable thing in a template is a **region** anchored at a node's position.

A region is identified by a stable key:

    template-name : line : col : kind

The `template-name` is the `Source` name (a path or logical id). `line:col` is the node's
1-based start position. `kind` distinguishes the branch role at that position (see below), so
two branch arms that begin at the same `line:col` -- which cannot happen in practice but is
guarded against -- never collide. Line coverage is *derived*: a line is covered when any
region on it is covered.

### 1.1 Coverable units (statement and output coverage)

A **unit** is a region that is "covered" the moment control reaches it and it is about to do
its work. Units answer "did this run at all?". Each is recorded at the node's position when
the interpreter dispatches it:

| Construct                         | Node kind(s)                          | Covered when                          |
|-----------------------------------|---------------------------------------|---------------------------------------|
| Interpolation `{{ expr }}`        | `KindPrint`                           | the print is evaluated and emitted    |
| Literal text span                 | `KindText`, `KindVerbatim`            | the span is emitted                   |
| `@set` / `@set = capture`         | `KindSet`, `KindCapture`              | the assignment executes               |
| `@do`, `@with`, `@apply`          | `KindDo`, `KindWith`, `KindApply`     | the statement executes                |
| `@log <expr>`                     | `KindLog`                             | the expression evaluates and is logged |
| `@escape`, `@sandbox`, `@cache`   | `KindEscape`, `KindSandbox`, `KindCache` | the region body is entered         |
| `@tab(n)` region                  | `KindTabBlock`                        | the indented body is entered          |
| `@guard` selected body            | `KindGuard`                           | the taken body is entered (see 1.2)   |
| `@include` / `@embed`             | `KindInclude`, `KindEmbed`            | the include is resolved and rendered  |
| `@block` render site              | `KindBlock`                           | the block's resolved body is rendered |
| `@macro` body                     | `KindMacro`                           | the macro is invoked at least once    |

Declaration-only heads that emit nothing and take no branch -- `@extends`, `@import`,
`@from`, `@use`, `@types`, `@line`, `@deprecated` -- are **not** counted as units. They have
no runtime reachability to measure, so counting them would only dilute the percentage. `@do`
is counted because it evaluates an expression for effect, and `@log` for the same reason -- it
evaluates and logs its expression even though it emits no rendered output. A comment
`{# ... #}` is NOT a coverable unit at all: the lexer consumes it and produces no node, so
there is nothing for the Collector to seed or hit.

### 1.2 Branch points (branch coverage)

A **branch** region has two or more arms; coverage records which arms were taken across all
renders. An arm is "covered" when control flowed through it at least once. A branch is fully
covered only when *every* arm has been taken. These are the branch points, keyed by the
`kind` tag shown:

**`@if` / `@elseif` / `@else` (`if-then` / `if-else` per clause).**
Each `KindClause` contributes one arm. An `if` or `elseif` clause has a **then** arm (the
condition was truthy and its body ran) and an implicit **else** arm (the condition was
evaluated false). The final `@else` clause is a single arm (its body ran). A chain of N
clauses yields N condition-true arms plus one "fell through all" arm. Concretely, for each
condition-bearing clause we record two branch outcomes at the clause position: `taken` and
`not-taken`; the terminal `@else` records one `taken` outcome at its position.

**`@for` body-executed vs zero-iteration (`for-body` / `for-empty`).**
Two arms at the `KindFor` position: **looped** (the body ran for at least one item across
some render) and **empty** (the collection drained to zero pairs, so the `@else` body ran or
nothing ran). A loop that is sometimes non-empty and sometimes empty across renders covers
both arms. A `@for ... @else` makes the empty arm's body a unit as well.

**Postfix `if` / `unless` (`ternary-then` / `ternary-else`).**
The parser desugars `{{ x if c }}` and `{{ x unless c }}` into a `KindTernary` at parse time
(see the [Language Reference](reference/language.md)), so postfix conditionals are measured exactly like an inline
ternary: two arms, `then` (condition truthy) and `else` (condition falsy). No separate model
is needed -- the desugaring means one implementation covers both surface forms.

**Ternary `c ? a : b`, elvis `a ?: b`, coalesce `a ?? b` (`ternary-*`, `elvis-*`, `coalesce-*`).**
Each is a two-arm branch at its node position. For the ternary the arms are the **then**
(`Child(1)`) and **else** (`Child(2)`) sides. For elvis the arms are **left-kept** (the left
was truthy and returned) and **right-used** (the fallback was taken). For coalesce the arms
are **left-kept** (left was non-null) and **right-used** (left was null, fallback taken).

**Short-circuit logical `and` / `or` (`logical-short` / `logical-full`), optional.**
`and` / `or` short-circuit (see `evalLogical`). The two arms are **short-circuited** (the
right operand was never evaluated) and **evaluated-both** (the right operand ran). `xor`
always evaluates both and is not a branch. This is recorded when logical-branch coverage is
enabled; it is off by default because short-circuit arms are noisy for many
templates.

**`@guard kind("name")` present vs absent (`guard-present` / `guard-absent`).**
Two arms: the guarded body ran (the callable was registered) or the `@else` body ran (it was
not). Each taken body is also a unit.

**Null-safe access `a?.b` / `a?[k]` (`safe-hit` / `safe-null`), optional.**
Two arms: the receiver was non-null and the member was read, or the receiver was null and the
access short-circuited to null. Off by default (same noise rationale as logical).

Every branch point is also a **unit**: reaching the construct counts the unit, and the arm
outcome is recorded on top. So a never-reached `@if` shows as an uncovered unit *and* two
uncovered arms; a reached `@if` whose `@else` never fired shows a covered unit with one
covered and one uncovered arm.

### 1.3 What "covered" means, precisely

- A **unit** at `T:L:C` is covered iff the interpreter dispatched that node at least once
  across all aggregated renders.
- A **branch arm** at `T:L:C#arm` is covered iff control flowed through that specific arm at
  least once.
- A **line** L in template T is covered iff at least one unit on line L is covered. Its hit
  count is the sum of the unit hit counts on that line.
- A **template** percentage is `covered-units / total-units`; a separate **branch**
  percentage is `covered-arms / total-arms`. The report shows both.

Counts are monotonic hit counters (how many times a region fired), not just booleans, so the
LCOV `DA`/`BRDA` records carry real execution counts and the HTML report can show hot lines.

--------------------------------------------------------------------------------

## 2. How it works (instrumentation model)

### 2.1 Node positions carry line and column

`ast.Node` carries `Line`, `Col`, and `Src`. The lexer already tracks a 1-based column on
every token; the parser threads the head token's `Col` into each node it builds, so every
region has an exact `line:col` anchor. This is a parser-only change: positions are metadata,
never consulted during evaluation, so it cannot affect rendered bytes.

### 2.2 A Collector, threaded through the interpreter

Coverage lives in a new package `cover`. Its central type is a `Collector`:

    type Collector struct { /* unexported: per-template region tables, hit counters */ }

    func NewCollector() *Collector
    func (c *Collector) Report() *Report

The interpreter (`package interp`) gains one nullable field, `cov *cover.Collector`. When it
is `nil`, coverage is off and every hook is a single nil-check that the compiler and branch
predictor make free -- this is the zero-overhead-when-disabled guarantee. When it is set, the
interpreter calls into it at each coverable point:

- `execItem` records the **unit** for the dispatched node before doing its work.
- `execIf` records the taken clause's **then** arm and each evaluated-false clause's
  **not-taken** arm.
- `execFor` records **for-body** when it enters the loop and **for-empty** when the pair set
  is empty.
- `evalTernary` / `evalElvis` / `evalCoalesce` / `evalLogical` record their arm as they pick
  a side.
- `execGuard`, `execBlockSite`, `callMacro`, `execInclude` / `execEmbed` record their unit /
  present-absent arm at the point the body or target is chosen.

Two-phase design keeps the model complete even for never-rendered code. Before a render, the
Collector is **seeded** by a static walk of each template's AST (the same walk shape as
`collectUsed` in `interp/template.go`): every coverable node is registered as a region with a
zero hit count. Rendering then only *increments*. This is why a template line that no test
ever reaches still appears in the report as `0` rather than being silently absent -- the
denominator is the whole template, not just what ran. Seeding is idempotent and keyed by
region id, so re-seeding the same template across renders is a no-op.

**Seeding boundary (template granularity).** Seeding is gated on a template being *entered*
by a render, not on it being merely *referenced*. The engine seeds the render root and its
inheritance chain, an `@include`/`@embed` target when its statement actually executes, and a
macro home when one of its macros is invoked. A template that is only referenced but never
entered -- imported for macros that are never called, or an `@include` whose statement never
runs because it sits in a never-taken `@if` arm -- is never seeded, so it is **absent** from
the report rather than shown at `0%`.

The *unit* of seeding depends on **why** a template was entered, because different entries
make different regions reachable:

- **Full entry** -- render root, inheritance target, or an executed `@include`/`@embed`. The
  template's top-level body is rendered, so `Collector.SeedTemplate` seeds the **whole
  module**: an untaken branch or an unreached statement anywhere in it still reports `0`.
- **Macro-home entry** -- the template is reached *only* because one of its macros is invoked
  via `@import`/`@from`. An import never renders the home's top-level markup, so that markup
  is **unreachable** in this context. `Collector.SeedMacro` therefore seeds **only the invoked
  macro's subtree**, and the top-level statements/text are *not* seeded -- they are absent
  rather than reported as an uncovered `0%` gap. Seeding a macro home's whole body would
  charge the denominator for code the import can never reach and distort the percentage for
  the common partial-that-also-exports-macros pattern.

The two seeds are independent and idempotent, so a partial that is *both* imported for a macro
*and* rendered (as a root or executed `@include`) gets its whole body seeded by the full
entry, which supersedes the narrower macro-home seed. Within whatever was seeded, an untaken
branch or unreached statement still reports `0`; only regions unreachable through the actual
entry fall out. This keeps the denominator to code the render pipeline could reach. A caller
that wants an unexercised partial to count as `0%` must seed it explicitly by walking the
reference graph and calling `Collector.SeedTemplate` on each target. The semantics are pinned
by `TestCoverageUnreachedIncludeIsAbsent` and `TestCoverageMacroHomeTopLevelNotSeeded`.

### 2.3 Aggregation across renders

A single `Collector` is shared across every `Render` call made through the Environment it is
attached to. Each render unions its hits into the Collector's per-template tables, keyed by
region id. Rendering template `page.quill` a hundred times with different data accumulates into
one region table for `page.quill`; the report is the union. Includes, parents, traits, and macro
homes are seeded and recorded under their own template names, so a shared partial's coverage
aggregates across every template that includes it. The Collector is safe for sequential
renders; concurrent renders should each use their own Collector and be merged with
`Report.Merge` (see 3.4).

--------------------------------------------------------------------------------

## 3. Go API

### 3.1 Enabling coverage on an Environment

Coverage is a construction option, mirroring the existing `WithAutoescapeHTML` /
`WithStrictVariables` options:

    coll := cover.NewCollector()
    env := quill.New(
        loader.NewFilesystemLoader("templates"),
        quill.WithCoverage(coll),
    )

    _, _ = env.Render("page.quill", vars)      // records into coll
    _, _ = env.Render("page.quill", other)     // unions more hits

    report := coll.Report()

`WithCoverage(nil)` is the same as not passing it: coverage stays off. Without the option the
Environment builds an interpreter whose `cov` field is nil and no region is ever seeded.

### 3.2 The Report and writing formats

`coll.Report()` returns an immutable snapshot:

    type Report struct { /* per-template regions with hit counts */ }

    func (r *Report) Templates() []TemplateCoverage   // sorted by name
    func (r *Report) Totals() Summary                 // units%, branches%, lines%

    func (r *Report) WriteText(w io.Writer) error     // human summary
    func (r *Report) WriteLCOV(w io.Writer) error     // .info for Codecov/CI
    func (r *Report) WriteHTML(w io.Writer) error      // highlighted source

    type TemplateCoverage struct {
        Name     string
        Units    Counts   // covered / total
        Branches Counts
        Lines    Counts
        Regions  []Region // for a per-region breakdown
    }

Each writer takes an `io.Writer`, so a caller streams to a file, a buffer, or stdout. All
three are produced from the same `Report`, so they never disagree.

### 3.3 go-test integration pattern

The intended usage is: build one Environment with a Collector for a test package, render all
your fixtures through it, then assert a threshold and dump a report artifact.

    func TestTemplateCoverage(t *testing.T) {
        coll := cover.NewCollector()
        env := quill.New(loader.NewFilesystemLoader("testdata/templates"),
            quill.WithCoverage(coll))

        for _, tc := range fixtures {           // each renders one template + data
            if _, err := env.Render(tc.Template, tc.Vars); err != nil {
                t.Fatalf("%s: %v", tc.Template, err)
            }
        }

        report := coll.Report()

        // Write an LCOV artifact for CI to ingest.
        f, _ := os.Create("coverage.info")
        defer f.Close()
        _ = report.WriteLCOV(f)

        // Fail below a threshold.
        if got := report.Totals().Units.Percent(); got < 90.0 {
            report.WriteText(os.Stdout)
            t.Fatalf("template unit coverage %.1f%% < 90%%", got)
        }
    }

`Counts.Percent()` returns `100 * covered / total` (and `100` for an empty template, so an
all-text partial never drags the number down). A `Report.FailUnder(threshold float64) error`
convenience wraps the compare-and-report so a test is one line.

### 3.4 Merging (parallel tests)

For `t.Parallel()` fixtures, give each goroutine its own Collector and merge the reports:

    merged := cover.MergeReports(r1, r2, r3)   // unions hit counts by region id

Merging is by region id, so the same template measured in two goroutines combines correctly.

--------------------------------------------------------------------------------

## 4. Report formats

### 4.1 Text summary

A per-template table plus a total line, and (with `-v`) a per-region breakdown grouped by
function-like scope (the module body, each `@block`, each `@macro`). Percentages for units
and branches are shown separately:

    Template                 Units          Branches       Lines
    ------------------------ -------------- -------------- --------------
    page.quill               42/47   89.4%  11/16   68.8%  20/22   90.9%
    partials/nav.quill       8/8    100.0%  2/2    100.0%  5/5    100.0%
    ------------------------ -------------- -------------- --------------
    TOTAL                    50/55   90.9%  13/18   72.2%  25/27   92.6%

The verbose region breakdown lists every uncovered region with its `line:col`, kind, and (for
branches) which arm is missing, so a developer can jump straight to the gap:

    page.quill
      block "body"
        14:3   if-else        else arm never taken
        22:9   for-empty      loop never ran empty
      macro "row"
        31:5   Print          never reached

### 4.2 LCOV export

`WriteLCOV` emits a standard `.info` stream that Codecov, `genhtml`, and CI coverage gates
ingest directly. One `SF` section per template, `DA` line-hit records derived from unit hit
counts, and `BRDA` branch records for every arm:

    TN:
    SF:page.quill
    DA:14,3
    DA:15,3
    DA:22,0
    BRDA:14,0,0,3        # line 14, block 0, branch 0 (then) taken 3x
    BRDA:14,0,1,-        # line 14, block 0, branch 1 (else) never taken
    BRDA:22,1,0,0        # line 22, for block, body arm, 0 hits
    BRDA:22,1,1,5        # line 22, for block, empty arm, 5 hits
    BRF:4
    BRH:3
    LF:22
    LH:20
    end_of_record

`BRDA` fields are `line, block, branch, taken` where `taken` is `-` for an arm the tool
proved reachable but never taken (distinct from a `0` hit count on a measured arm). The
`block` id groups the arms of one branch point (one `@if` chain, one `@for`, one ternary);
`branch` numbers the arms within it. `LF`/`LH` are line found/hit, `BRF`/`BRH` are branch
found/hit -- the numbers a CI gate thresholds on.

### 4.3 HTML report

`WriteHTML` renders a self-contained HTML page (stdlib `html/template`, no external assets)
showing each template's source with per-line highlighting:

- Each source line is shown with its line number and hit count.
- **Covered** lines are green, **uncovered** lines (units that exist but never ran) are red,
  lines with no coverable unit are neutral.
- **Branch markers** sit in the gutter next to a branch point: a filled marker when every arm
  was taken, a half-filled marker when only some arms were (partial branch), an empty marker
  when the branch was never reached. Hovering a marker names the missing arm(s), e.g.
  "else arm never taken".
- A per-template header shows the unit / branch / line percentages, and an index page links
  every template sorted by coverage ascending, so the least-covered template is first.

The HTML is emitted through `html/template`, so template source shown in the report is
auto-escaped and cannot break the report page.

--------------------------------------------------------------------------------

## 5. CLI: `quill cover`

`quill cover` renders one or more template+data cases and writes a coverage report. It is the
command-line front door to the same Collector/Report API.

Single template, text report to stdout:

    quill cover -root templates -data data.json page.quill

Choose a format and an output file:

    quill cover -root templates -data data.json -format lcov -o coverage.info page.quill
    quill cover -root templates -data data.json -format html -o cover.html page.quill

Aggregate many cases from a JSON case file (each case is a template plus its variables), so a
single report unions coverage across a whole fixture set:

    quill cover -root templates -cases cases.json -format html -o cover.html

The `cases.json` shape is a list of `{ "template": name, "data": object }`:

    [
      { "template": "page.quill",        "data": { "user": { "admin": true } } },
      { "template": "page.quill",        "data": { "user": { "admin": false } } },
      { "template": "partials/nav.quill","data": { "items": [] } }
    ]

Rendering both admin states above covers both arms of an `@if user.admin`; the empty `items`
list covers the `for-empty` arm of the nav partial. This is how you drive branch coverage
from the CLI: enumerate the data cases that exercise each arm.

Flags:

    -root string     template root the loader resolves names under (default ".")
    -data string     JSON data file for a single-template run ("-" reads stdin)
    -cases string    JSON file of {template,data} cases; unions coverage across all
    -format string   report format: text (default), lcov, or html
    -o string        output file (default stdout)
    -fail-under N    exit non-zero if unit coverage percent is below N
    -threshold N     alias for -fail-under
    -autoescape      off (default) or html; matches the render option so instrumentation
                     runs under the same strategy the template ships with
    -strict          strict-undefined handling (default true)

A single template is named as a positional argument alongside `-data`; `-cases`
replaces the positional name and data with a JSON list. Supplying both, or
neither, is an error.

`-fail-under` makes `quill cover` a CI gate: it renders the cases, writes the report, and
exits 1 when unit coverage is below the threshold, printing the uncovered-region breakdown to
stderr so the failing CI log shows exactly what to cover.

--------------------------------------------------------------------------------

## 6. Guarantees and non-goals

**Zero overhead when disabled.** No Collector means the interpreter's `cov` field is nil and
every hook is one nil comparison. No region tables are allocated, no positions are consulted.
An Environment built without `WithCoverage` renders exactly as it does today.

**Output invariance.** Instrumentation only reads node positions and increments counters; it
never touches the value pipeline or the output sink. The pre-existing conformance suite passes
byte-identically with coverage on. This is asserted directly: a conformance variant renders
every fixture twice, once with a Collector attached, and diffs the output.

**Reachability, not correctness.** Coverage says an arm ran, not that it produced the right
bytes -- that is what golden fixtures are for. High coverage with weak assertions is still
weak; coverage tells you where assertions are *absent*, not where they are wrong.

**Not a profiler.** Hit counts indicate hot regions but are not timed. Use them to find dead
template branches, not to optimize render latency.
