# Contributing to Quill

Thanks for your interest in Quill.

## Development

Requires Go (see the `go` directive in `go.mod` for the minimum version).

```
make check   # fmt, vet, test
```

## Guidelines

- Keep the engine dependency-free: standard library only.
- Match the existing code style; run `make fmt` and `make vet` before sending a
  change.
- Add tests for new behavior; the conformance suite under `testdata/` is the
  contract.
- Reference the language specification in `docs/` for intended semantics.

## Commit messages

Use a conventional prefix (`feat:`, `fix:`, `refactor:`, `docs:`, `chore:`), an
imperative subject under 72 characters, and a body explaining what and why.
