# Escaping & Safety

Quill's output escaping is off by default, like Go `text/template`, and opt-in per
template, per region, or per site. This page covers the escape strategies, the
safeness machinery that prevents double-escaping, and the policy sandbox for
running untrusted templates. Streaming output is covered at the end.

## Escaping is off by default

The default output strategy is `off` (synonym `raw`): an interpolation renders the
value's `ToText` bytes verbatim, with no transformation. This matches Go
`text/template`, and it is the right default for any text where the raw bytes are
load-bearing -- configuration, program source, plain text, and any format where
characters like `<`, `>`, `&`, and `"` are ordinary content rather than markup
that must be escaped.

Escaping is opt-in, three ways:

- **Per template or region** -- `@escape html { ... @}` sets the active strategy
  for a region; `WithAutoescapeHTML(true)` turns HTML escaping on globally.
- **Per site** -- `| escape` or `| e("html")` escapes a single interpolation.

There is no need to write `| raw` under the default, because nothing is escaped
until you opt in.

## The six escape strategies

When escaping is enabled, the `escape(strategy)` filter (alias `e`) and the
`@escape` region select one of six named strategies:

| Strategy | Escapes |
|----------|---------|
| `html` | `& < > " '` for HTML text (`'` as `&#39;`) |
| `js` | a string for safe embedding in JavaScript |
| `css` | a string for safe embedding in CSS |
| `html_attr` | a string for an HTML attribute value |
| `html_attr_relaxed` | an HTML attribute value, allowing `:@[]` |
| `url` | percent-encode for URLs (RFC 3986; space -> `%20`) |

Strategies compose via a stack, and content produced under an active strategy is
marked `Safe`, so it is never escaped twice.

**Charset and invalid UTF-8.** A `Str` is a byte string that may be invalid UTF-8,
so the escapers split into two classes:

- `html` and `url` are byte-oriented and accept arbitrary bytes losslessly:
  `html` substitutes only the five ASCII characters `& < > " '` and passes every
  other byte through unchanged; `url` percent-encodes byte by byte. Neither needs
  the charset and neither errors on invalid UTF-8.
- `js`, `css`, `html_attr`, and `html_attr_relaxed` are code-point-oriented: they
  escape by Unicode code point, so they first decode the `Str` as the configured
  charset (`_charset`, default UTF-8). If the bytes are not valid in that charset,
  the escaper raises a clear error naming the strategy and the byte offset rather
  than silently emitting replacement characters.

## The safeness machinery

- **`raw` filter / safeness annotation** -- a compile-time no-op marking content
  already-safe; never auto-escaped. It is inert under the default and switches a
  single site back to unescaped under an `escape`-on region.
- **`Safe` value** -- the already-escaped carrier, returned unchanged by `escape`,
  produced by captures and macros under escaping, and a plain-string passthrough
  when escaping is off.
- **Per-strategy filter safeness, pre-escape filters** (e.g. `nl2br`), and
  **safeness inference** over ternary/conditional operands are active only when
  escaping is enabled.
- **Default-strategy selection** -- a fixed value, off, or a host-supplied
  resolver including by file extension (`body.html.quill` -> `html`). The default is
  off; the host may register a resolver.
- **Compile-time escape injection** -- escaping is decided and injected at compile
  time, so the off-path has zero render cost and the output is deterministic.

Under the default (escaping off) a `Safe` value is an inert passthrough
indistinguishable from a `Str`: it is normalized to its wrapped `Str` before
equality, ordering, membership, and structural compare, so `Safe("x") == "x"` is
true. See [Types](types.md) for the equality rules.

## The sandbox

A host-supplied security policy restricts the permitted tags, filters, functions,
per-type methods, and per-type properties, so you can run untrusted templates
under a policy you control.

```go
pol := &sandbox.Policy{
	Filters:   map[string]bool{"upper": true, "lower": true},
	Functions: map[string]bool{},
	Tags:      map[string]bool{"if": true, "for": true},
	Graph:     sandbox.NewTypeGraph(),
}
env := quill.New(ldr,
	quill.WithSandboxPolicy(pol),
	quill.WithSandboxActive(true),
)
```

- **Uniform allowlisting.** Every tag, filter, and function is subject to the same
  allowlist, with none exempt: a host callable is gated exactly like a built-in,
  with no grandfathering.
- **Type-graph matching.** Method and property allowlisting matches against an
  explicit host type-graph, across registered subtype/interface relations, with
  case-sensitive method-name matching. This is the same graph the gradual type
  checker uses for `Object<"Type">`, so one host type registration serves both
  security and typing.
- **Activation.** The sandbox activates globally
  (`WithSandboxActive`), per `@sandbox { ... @}` region, or per sandboxed include
  (`sandboxed: true`). Enabling it for a nested include and restoring it afterward
  never disables the sandbox for an already-sandboxed enclosing render.
- **Enforcement.** Compile-time collection of used callables feeds a single
  per-render check that maps violations to source lines; runtime method and
  property access is enforced at the access site. Arrow callables must be
  template-defined. Each violation raises a distinct, host-catchable
  `*errors.Security` carrying the offending name and type name.

Custom host callables interact with the sandbox exactly like built-ins; see
[Extensions & Loaders](extensions.md).

## Streaming output

By default `Render` returns the whole result as a string. `RenderTo` streams
output to any `io.Writer` without buffering the entire result:

```go
err := env.RenderTo(os.Stdout, "page.quill", vars)
```

`RenderStringTo` is the string-keyed variant. A template that uses deferred slots
(`@yield`/`@provide`) buffers internally and resolves placeholders before
returning, so a mid-render error never leaves an unresolved placeholder in the
caller's writer.

## Next

- [Types](types.md) -- the value model, including how `Safe` behaves under
  equality.
- [Standard Library](stdlib.md) -- the `escape`/`e`, `raw`, and `nl2br` filters.
- [Extensions & Loaders](extensions.md) -- custom callables under the sandbox.
