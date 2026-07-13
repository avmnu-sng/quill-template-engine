# CLI

The `quill` command renders a template with JSON data, reports template coverage,
and compiles a template to Go. Install it with:

```
go install github.com/avmnu-sng/quill-template-engine/cmd/quill@latest
```

It has three modes: the default render path (`quill <template>`), the `cover`
subcommand, and the `compile` subcommand.

## Rendering

The default invocation renders a named template resolved by a filesystem loader
rooted at `-root`, so an `@extends` parent, an `@include` target, and an
`@import`/`@from` source all resolve by name under the same root:

```
quill -root templates -data data.json index.quill
quill -root templates -autoescape html page.quill > page.html
cat data.json | quill -root templates -data - index.quill
```

Variables come from a JSON object read from the `-data` file (or stdin when
`-data` is `-`); with no `-data` flag the template renders against an empty
variable set. The rendered output is written to stdout; any load, parse, or render
error is reported to stderr with a non-zero exit status.

Flags:

```
-root string        template root the loader resolves names under (default ".")
-data string        JSON file with the render variables ("-" reads stdin)
-autoescape string  output escaping strategy: "off" (default) or "html"
-strict             strict-undefined handling (default true; -strict=false is lenient mode)
-version            print the version and exit
```

## `quill cover`

`quill cover` renders one or more template+data cases with a coverage collector
attached and writes a report. It is the command-line front door to the same
`cover.Collector` / `Report` API described in [Coverage](coverage.md).

Single template, text report to stdout:

```
quill cover -root templates -data data.json page.quill
```

Choose a format and an output file:

```
quill cover -root templates -data data.json -format lcov -o coverage.info page.quill
quill cover -root templates -data data.json -format html -o cover.html page.quill
```

Aggregate many cases from a JSON case file, so a single report unions coverage
across a whole fixture set:

```
quill cover -root templates -cases cases.json -format html -o cover.html
```

The `cases.json` shape is a list of `{ "template": name, "data": object }`:

```json
[
  { "template": "page.quill",         "data": { "user": { "admin": true } } },
  { "template": "page.quill",         "data": { "user": { "admin": false } } },
  { "template": "partials/nav.quill", "data": { "items": [] } }
]
```

Rendering both admin states above covers both arms of an `@if user.admin`; the
empty `items` list covers the `for-empty` arm of the nav partial.

Flags:

```
-root string        template root the loader resolves names under (default ".")
-data string        JSON data file for a single-template run ("-" reads stdin)
-cases string       JSON file of {template,data} cases; unions coverage across all
-format string      report format: text (default), lcov, or html
-o string           output file for the report (default stdout)
-fail-under N       exit non-zero if total unit coverage percent is below N
-threshold N        alias for -fail-under
-autoescape string  "off" (default) or "html"; matches the render option
-strict             strict-undefined handling (default true)
```

`-fail-under` makes `quill cover` a CI gate: it renders the cases, writes the
report, and exits non-zero when total unit coverage is below the threshold,
printing the uncovered-region breakdown to stderr so the failing CI log shows
exactly what to cover.

## `quill compile`

`quill compile` lowers one template through the compile backend and writes the
generated Go source: a render function plus the exported manifest
`quill.WithCompiled` installs for by-name dispatch. A construct outside the
compilable subset is reported as a not-compilable error naming the construct.

```
quill compile -root templates -pkg qtpl -o index_gen.go index.quill
```

The option flags mirror the Environment knobs the generated unit's fingerprint
captures, so a unit compiled here dispatches on an Environment configured the same
way (see [Performance](performance.md) and [Architecture](architecture.md)):

```
-root string        template root the loader resolves names under (default ".")
-pkg string         package clause of the generated file (default "qtpl")
-func string        name of the generated render function (default "Render")
-autoescape string  "off" (default) or "html"
-strict             strict-undefined handling (default true)
-tabwidth int       spaces one indent level expands to (default 4)
-seed int           fixed seed for the randomness callables (omit for time-seeded)
-o string           output file for the generated source (default stdout)
```

Install the generated manifest when you build the Environment:

```go
env := quill.New(ldr, quill.WithCompiled(qtpl.Manifest))
```

The Environment serves the compiled render only when its fingerprint matches and
every member source byte-equals what the loader serves; otherwise it falls back to
the interpreter, so a compiled unit can change render speed but never rendered
bytes.

## Next

- [Coverage](coverage.md): the coverage model behind `quill cover`.
- [Performance](performance.md): the compile backend behind `quill compile`.
- [API](api.md): the Go API the CLI is built on.
