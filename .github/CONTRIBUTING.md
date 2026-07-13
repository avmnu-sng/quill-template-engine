# Contributing to Quill

Thanks for your interest in Quill. This guide covers the build, the checks a
change must pass, and the conventions the codebase follows.

## Development environment

The library's minimum is Go 1.23 (the `go` line in `go.mod`, kept conservative
for broad compatibility). Development uses a newer Go: the test and benchmark
suites rely on `b.Loop` (Go 1.24+), so pin a recent toolchain locally (for
example with `goenv`). The engine itself is standard-library only; the linters
and scanners below are dev tooling.

Quill uses [go-task](https://taskfile.dev) as its build tool:

```
go install github.com/go-task/task/v3/cmd/task@latest
task --list
```

Install the dev tools (golangci-lint, govulncheck, actionlint, go-licenses)
once with `task install:tools`.

## Build, test, and lint

The common targets:

```
task build        # build all packages and the cmd/quill binary
task test         # go test ./...
task test:unit    # tests with the race detector and a coverage profile
task check:all    # gofmt, go vet, and go mod tidy checks
task lint:all     # golangci-lint + actionlint
task ci           # the full pipeline: lint, checks, tests, and security scans
```

Run `task ci` before opening a pull request; it is the same pipeline CI runs. A
thin `Makefile` forwards `make build`/`make test`/`make check` to the equivalent
`task` targets.

The gates a change must keep green:

- `go build ./...` and `go vet ./...` succeed.
- `gofmt -l .` prints nothing (the tree is formatted).
- `golangci-lint run` reports zero issues.
- `go test ./...` passes, including any `ExampleXxx` functions.
- `govulncheck ./...` is clean.

## Conventions

- **Dependency-free runtime.** The engine depends on nothing outside the Go
  standard library. Do not add a runtime dependency.
- **Tests for new behavior.** Add tests for anything you add or change. The
  conformance suite under `testdata/conformance` is the behavioral contract:
  each case is a `template.ql` + `data.json` + `expected.out` triple, and a
  table test renders each and diffs the bytes. Rendered output is byte-exact, so
  golden fixtures must match to the byte.
- **Coverage.** Keep meaningful coverage on new code; `task test:unit` produces a
  profile you can inspect with `task test:cover`.
- **Doc comments on exported API.** Every exported symbol carries a doc comment
  that is a full sentence ending in a period; `golangci-lint` (revive
  `exported` + `godot`) enforces this. Package-level prose lives in `doc.go`, and
  runnable `ExampleXxx` functions double as tests and populate the Examples tab
  on pkg.go.dev.
- **Documentation surfaces.** User-facing docs live in `docs/` (the guide site)
  and in the `README.md`; the canonical API reference is pkg.go.dev, generated
  from the doc comments. Keep behavior and docs in sync in the same change.

## Commit messages and pull requests

- Use a conventional prefix (`feat:`, `fix:`, `refactor:`, `docs:`, `chore:`,
  `ci:`), an imperative subject under 72 characters, and a body explaining what
  changed and why.
- Keep a pull request focused on one logical change. Note any user-visible change
  in `CHANGELOG.md` under the unreleased section.

## Reporting security issues

Do not open a public issue for a security vulnerability. See
[SECURITY.md](https://github.com/avmnu-sng/quill-template-engine/blob/main/.github/SECURITY.md)
for the private reporting process.
