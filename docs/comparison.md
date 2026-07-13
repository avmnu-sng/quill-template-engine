# Comparison

This is a neutral capability matrix for Quill against four other Go template
engines: the standard library's `text/template` and `html/template`, and the
Twig/Jinja-family engines pongo2 and stick. It is meant to help you choose, not to
rank. Each engine makes different trade-offs, and the right choice depends on
what you are rendering.

## Capability matrix

| Capability | Quill | `text/template` | `html/template` | pongo2 | stick |
|------------|:-----:|:---------------:|:---------------:|:------:|:-----:|
| Escaping off by default | yes | yes | no (HTML auto) | no (HTML auto) | no (HTML auto) |
| Contextual HTML autoescape | opt-in | no | yes | yes | yes |
| Other escape strategies (js/css/url/attr) | yes (6) | no | partial (contextual) | partial | partial |
| Gradual static type checking | yes | no | no | no | no |
| Template inheritance (`extends`/`block`) | yes | no | no | yes | yes |
| Macros with defaults/variadics | yes | (define/template) | (define/template) | yes | yes |
| Traits / horizontal reuse (`use`) | yes | no | no | no | partial |
| Embeds and accumulating slots | yes | no | no | no | partial |
| Pipe filters | yes | yes (pipelines) | yes (pipelines) | yes | yes |
| Arrow functions / higher-order filters | yes | no | no | no | no |
| Native branch-aware coverage | yes | no | no | no | no |
| Compile-to-Go backend | yes | no | no | no | no |
| Byte-exact whitespace control | yes | trim markers | trim markers | trim markers | trim markers |
| Block cleanup on by default | yes | no | no | configurable | configurable |
| Policy sandbox | yes | no | no | no | partial |
| Streaming to `io.Writer` | yes | yes | yes | yes | yes |
| Custom filters/functions/tests | yes | funcs | funcs | yes | yes |
| Standard-library-only runtime | yes | yes | yes | no | no |

"partial" means the engine supports a related but narrower form; the linked
project docs are authoritative for each peer.

## How to read it

- **`text/template` / `html/template`** are the standard library. They are small,
  dependency-free, and ubiquitous. `text/template` escapes nothing;
  `html/template` adds contextual autoescaping for HTML output. Neither has
  template inheritance, static typing, coverage, or a compile backend, and both
  express composition through `define`/`template` rather than `extends`/`block`.
  If you want the smallest possible surface and your needs are simple, they are a
  fine choice.
- **pongo2 / stick** bring Twig/Jinja semantics to Go: inheritance, macros,
  filters, and HTML autoescape by default. They are a good fit when you want
  Django/Twig ergonomics for HTML. They pull external dependencies and default to
  HTML escaping, which you turn off for non-HTML output.
- **Quill** overlaps the Twig/Jinja feature set (inheritance, macros, filters,
  arrows) while adding a gradual type system, native branch-aware coverage, a
  compile-to-Go backend, a policy sandbox, and byte-exact whitespace control, and
  it keeps escaping off by default like `text/template`. It is
  standard-library-only. It is the broadest surface of the five, which is
  overhead you do not need for a two-line HTML snippet and leverage you do want
  for a large, evolving template corpus.

## Performance

Timing against these engines is covered on the [Performance](performance.md) page,
which measures the same three workloads across the offline engines and (behind a
build tag) the two peers. Because the peers run in the same Go runtime, the timing
is fair; because their feature models differ, treat cross-engine timing as a
same-runtime comparison rather than a like-for-like language comparison.

## Choosing

- Rendering **HTML and nothing else**, want the smallest surface -> `html/template`.
- Rendering **plain text / config / source**, want the standard library ->
  `text/template`.
- Want **Twig/Jinja ergonomics for HTML** with Django-style filters -> pongo2 or
  stick.
- Want **inheritance, types, coverage, a compile backend, whitespace control, and
  a sandbox** across mixed output shapes, dependency-free -> Quill.
