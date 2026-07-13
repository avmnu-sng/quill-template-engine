# Standard Library

This is the reference for Quill's built-in standard library: the filters piped
with `|`, the functions called with `name(...)`, and the tests applied with `is`
/ `is not`. Each entry states its Go semantics. The runtime value rules these
callables operate under (truthiness, equality, ordering, coercion, undefined
handling, escaping) are in [Types](types.md). The call surface (pipe/call/test
forms, named and defaulted arguments) is in [Expressions](guide/expressions.md).

## The three callable kinds

| Kind | Surface | First argument |
|------|---------|----------------|
| Filter | <code>x &#124; name</code> or <code>x &#124; name(args)</code> | the piped value `x` |
| Function | `name(args)` | none implicit; all explicit |
| Test | `x is name` / `x is name(arg)` / `x is not name` | the tested value `x` |

A filter is a function whose first parameter is supplied by the pipe: `x | f(a, b)`
is exactly `f(x, a, b)`. There is no separate filter-versus-function namespace: a
name resolves to at most one filter, one function, and one test, and the syntactic
position selects which.

All filter and function names are lower `snake_case` (`format_number`,
`url_encode`, `json`). Two-word tests have a canonical underscore-joined spelling
(`is divisible_by(n)`, `is same_as(y)`); the spaced spelling is accepted as an
alias. Every callable accepts named, defaulted, and spread arguments per the call
surface. Signatures use the gradual type notation of [Types](types.md): `T -> R`
is "piped `T`, returns `R`"; `(T, A) -> R` lists the piped value first.

## Filters

### String filters

| Filter | Signature | Notes |
|--------|-----------|-------|
| `upper` | `string -> string` | Unicode upper-case, charset-aware. |
| `lower` | `string -> string` | Unicode lower-case, charset-aware. |
| `capitalize` | `string -> string` | First rune upper, rest lower-cased. |
| `title` | `string -> string` | First rune of each word upper, rest lower. |
| `trim(side: string = "both", mask: string = WS)` | `string -> string` | Strip from `side` (`"both"`/`"left"`/`"right"`) using `mask`. |
| `nl2br` | `string -> Safe` | Replace `\n` with `<br />\n`; pre-escapes HTML, marks `Safe` for html. |
| `spaceless` | `string -> string` | Collapse inter-tag whitespace. |
| `striptags(allowed: string = "")` | `string -> string` | Remove markup tags, optionally keeping `allowed`. |
| `replace(pairs: map<string,string>)` | `(string, map) -> string` | strtr-style: longest-key-first, non-overlapping, single-pass, byte-level. |
| `split(delim: string, limit: int = 0)` | `(string, ...) -> list<string>` | Split on `delim`; empty `delim` chunks into runes. |
| `slice(start: int, length: int? = null)` | `(string, ...) -> string` | Rune-based substring; also backs `s[a:b]`. On a collection, slices elements. |
| `first` | `any -> any` | First rune of a string / first element of a collection. |
| `last` | `any -> any` | Last rune / last element. |
| `format(...args)` | `(string, ...) -> string` | printf with Go `fmt` verbs. |
| `format_number(decimals: int = 0, point: string = ".", sep: string = ",")` | `(number, ...) -> string` | Fixed-decimal formatting with separators. Alias `number_format`. |
| `convert_encoding(to: string, from: string)` | `(string, ...) -> string` | UTF-8-centric encoding conversion. |
| `ucfirst` | `string -> string` | Upper-case first byte only, rest unchanged. |
| `wrap(width: int, break: string = "\n")` | `(string, ...) -> string` | Word-wrap to `width` runes, breaking only at spaces. |
| `truncate(length: int, omission: string = "...", preserve: bool = false)` | `(string, ...) -> string` | Cap at `length` runes and append `omission` when shortened. |
| `center(width: int, fill: string = " ")` | `(string, ...) -> string` | Pad both sides with `fill` to `width` runes, centered. |
| `wordcount` | `string -> int` | Count maximal runs of non-space runes. |

### Collection filters

| Filter | Signature | Notes |
|--------|-----------|-------|
| `length` | `any -> int` | String runes, collection count, or 1 for a scalar. |
| `join(glue: string = "", final: string? = null)` | `(list, ...) -> string` | Join with `glue`; optional `final` glue for the last pair. |
| `merge(other)` | `(coll, coll) -> coll` | Integer-keyed values appended and reindexed; string-keyed overwritten; order preserved. |
| `keys` | `coll -> list` | Keys in insertion order. |
| `sort(by: (a, b) => int? = null)` | `(coll, ...) -> coll` | Total ordering or a spaceship arrow; key-preserving. |
| `reverse(preserve_keys: bool = true)` | `(any, ...) -> any` | Reverse a collection or a string by runes. |
| `batch(size: int, fill: any? = null)` | `(coll, ...) -> list<list>` | Fixed-size chunks; `fill` pads the last chunk. |
| `columns(n: int, fill: any? = null)` | `(coll, ...) -> list<list>` | Distribute into `n` balanced columns, the transpose of `batch`. |
| `column(name)` | `(list, key) -> list` | Extract one attribute per row. |
| `entries` | `map -> list<list>` | Yield `[key, value]` pairs as an ordered sequence, in insertion order. |
| `sort_map(by: string = "key")` | `map -> map` | Sort a mapping deterministically by `"key"` or `"value"`, key-preserving. |
| `map((value, key?) => expr)` | `(coll, fn) -> coll` | Transform, key-preserving. Accepts `attribute: "path"` to pluck a dotted path. |
| `filter((value, key?) => bool)` | `(coll, fn) -> coll` | Keep where the arrow is truthy, key-preserving. |
| `reduce((acc, value, key?) => expr, initial: any = null)` | `(coll, fn, ...) -> any` | Left fold. |
| `find((value, key?) => bool)` | `(coll, fn) -> any` | First matching value, else `null`. |
| `sum(attribute: string? = null)` | `(coll, ...) -> number` | Add a numeric sequence; with `attribute` sum that dotted path. |
| `unique(attribute: string? = null)` | `coll -> list` | Drop duplicates, first occurrence wins, order preserved. |
| `select(test: string, args...)` | `(coll, ...) -> coll` | Keep elements where the named test passes. |
| `reject(test: string, args...)` | `(coll, ...) -> coll` | The complement of `select`. |
| `selectattr(path: string, test: string? = null, args...)` | `(coll, ...) -> coll` | Pluck `path`; keep by truthiness or a named test. |
| `rejectattr(path: string, test: string? = null, args...)` | `(coll, ...) -> coll` | The complement of `selectattr`. |
| `group_by(by)` | `(coll, path-or-arrow) -> list<map>` | Partition into `{key, items}` mappings, ordered by first appearance. |
| `shuffle(seed: int? = null)` | `coll -> coll` | Permute; `seed` makes it deterministic. |

### Math filters

| Filter | Signature | Notes |
|--------|-----------|-------|
| `abs` | `number -> number` | Absolute value, preserving int/float. |
| `round(precision: int = 0, mode: string = "common")` | `(number, ...) -> float` | `mode` in `"common"`/`"ceil"`/`"floor"`; negative precision rounds to tens/hundreds. |
| `range(...)` | (see [Functions](#functions)) | The `..` operator and the `range` function share one engine. |

### Encoding, serialization, date, and utility filters

| Filter | Signature | Notes |
|--------|-----------|-------|
| `json(pretty: bool = false, indent: string = "  ")` | `any -> string` | Serialize via Go `encoding/json`; `pretty` indents. Alias `json_encode`. |
| `url_encode` | `any -> string` | Percent-encode a string, or build a query string from a mapping. |
| `escape(strategy: string = "html")` | `any -> Safe` | Escape for a named strategy. Alias `e`. Opt-in; see [Escaping & Safety](safety.md). |
| `raw` | `any -> Safe` | Compile-time no-op marking content already-safe; never auto-escaped. |
| `date(layout: string = DEFAULT, tz: string? = null)` | `any -> string` | Format using a Go reference-time layout (`"2006-01-02 15:04:05"`). |
| `date_modify(delta: string)` | `(date, string) -> date` | Apply a relative modification (`"+1 day"`, `"-2 hours"`). |
| `default(fallback: any)` | `(any, any) -> any` | Yield `fallback` when the piped value is undefined or `Null`. |
| `invoke(...args)` | `(callable, ...) -> any` | Call a piped callable with arguments. |

### Two semantics worth calling out

- **`replace` (strtr-style).** Substitution is longest-key-first, non-overlapping,
  single-pass, and byte-level: every position is matched against the longest
  applicable key, the match is emitted as its replacement, and scanning resumes
  after the match. A replacement is never re-scanned. This is not naive
  sequential replacement (which would cascade `a->b` then `b->c`). Backed by
  `strings.Replacer`.
- **`merge`.** Integer-keyed values from `other` are appended and reindexed onto
  the receiver; string-keyed values overwrite by key; insertion order is
  preserved. This is the array-union capability that `+` deliberately does not
  provide (`+` is numeric only).

### The formatting filters and their Go semantics

Three formatting filters read their format string in Go's own notation:

- **`format`** takes Go `fmt` verbs (`%v`, `%q`, `%05.2f`, ...) and formats
  through Go `fmt`.
- **`json`** serializes through Go `encoding/json`: `/` is emitted literally with
  no HTML escaping, keys keep insertion order (deterministic output), and
  non-ASCII passes through unescaped.
- **`date`** formats with a Go reference-time layout (`2006-01-02 15:04:05`), the
  same layout string the runtime parses, so there is one date notation across
  parsing and formatting.

### The `default` filter and emptiness

`default(x, fallback)` yields `fallback` when `x` is undefined or `Null`
(definedness, [Types](types.md)), never raising on undefined.
`0 | default("y")` keeps `0` because it is defined and non-null: `default` keys on
definedness and `Null`, not on emptiness. The separate `is empty` test covers
length-zero collections/strings and `Null`.

### Attribute projection, named-test filtering, and grouping

Four collection filters take an `attribute: "path"` named argument (a dotted
path plucked from each element), so a projection reads a nested value directly
rather than through an arrow:

```
{{ people | map(attribute: "name") | join(", ") }}
{{ orders | sum(attribute: "line.total") }}
{{ people | unique(attribute: "role.title") }}
```

`select`/`reject` take the name of a registered test and keep, respectively drop,
the elements for which it passes. `selectattr`/`rejectattr` first pluck a dotted
path; `selectattr(path)` with no test keeps elements whose projected value is
truthy, and `selectattr(path, test, args...)` applies the named test to the
projected value. The comparison tests `eq`/`ne`/`lt`/`le`/`gt`/`ge` make attribute
comparisons direct:

```
{{ people | selectattr("active") }}
{{ people | selectattr("age", "ge", 18) }}
{{ people | rejectattr("role.title", "eq", "admin") }}
```

`columns(n)` distributes into `n` roughly-equal columns balanced by size (the
transpose of `batch`). `entries` yields a mapping's `[key, value]` pairs as an
ordered sequence. `sort_map(by:)` returns a new mapping reordered by `"key"`
(default) or `"value"`. `group_by(by)` partitions into an ordered sequence of
`{key, items}` mappings, one per distinct key, ordered by first appearance.

## Functions

### The `range` engine

`range(low, high, step: number = 1)` builds an inclusive numeric or
single-character range sequence. The `..` operator (`1..5`, `'a'..'e'`) is the
same engine.

### Aggregate, access, composition, iteration, and registry functions

| Function | Notes |
|----------|-------|
| `max(a, b, ...)` or `max(iterable)` | Maximum by the single total ordering. |
| `min(a, b, ...)` or `min(iterable)` | Minimum. |
| `attribute(var, name, args: list? = null)` | Read member `name` (runtime-computed) of `var`. |
| `parent()` | Render the parent block; legal only inside an overriding block. |
| `block(name, template: string? = null)` | Render a named block of this or another template. |
| `include(template, vars: map = {}, with_context: bool = true, ignore_missing: bool = false, sandboxed: bool = false)` | Function-form include returning rendered output. |
| `source(name, ignore_missing: bool = false)` | Return the raw, unparsed source of a template. |
| `cycle(values, position)` | `values[position % length]`, wrapping. |
| `random(values: any? = null, max: int? = null)` | Random element, random int in `[0, max]`, or a random character; seedable. |
| `constant(name, obj: any? = null, check_defined: bool = false)` | Resolve a host/global or class constant by name. |
| `enum(name)` | First case of a host enumeration. |
| `enum_cases(name)` | All cases of a host enumeration in declaration order. |
| `date(date: any? = null, tz: string? = null)` | Construct a date/time value from a string/timestamp and timezone. |
| `separator(sep: string = ",")` | Return a callable that yields `""` on its first call and `sep` after. |
| `cell(initial: any = null)` | Return a mutable single-slot reference whose `value` member is assignable. |

### Reference values: `separator` and `cell`

`separator(sep)` returns a callable whose first call yields `""` and each later
call yields `sep`, so calling it once per iteration before the element produces
trailing-separator-free joining:

```
@set sep = separator(", ")
@for n in nums {~
{{- sep() }}{{ n -}}
@}
```

renders `1, 2, 3` for `nums = [1, 2, 3]`.

`cell(initial)` returns a mutable reference with one member, `value`. Because a
reference value circulates by pointer, a cell mutated inside a loop body is visible
after the loop, which lets an accumulator survive without weakening the default
no-leak loop scoping:

```
@set acc = cell(0)
@for w in weights {
@set acc.value = acc.value + w
@}
sum: {{ acc.value }}
```

### Debug and dynamic functions (opt-in)

| Function | Notes |
|----------|-------|
| `dump(...vars)` | Debug-dump the given variables, or the whole context; `null` outside debug mode. |
| `template_from_string(source, name: string? = null)` | Compile a string into a template at runtime. Security-sensitive; host-gated. |

### Go-native convenience aliases

`len(x)` aliases `x | length` and `keys(m)` aliases `m | keys`. These add
reachability, not capability; the filter forms remain canonical.

### Runtime-injected parameters

A registered filter, function, or test may declare which engine values the runtime
must inject ahead of the user arguments:

| Flag | Injected value | Used by |
|------|----------------|---------|
| `needs_charset` | the active charset string | the four case filters, the codepoint escapers |
| `needs_context` | the live context as an `*Array` | `include`, `block`, `dump`, `template_from_string` |
| `needs_environment` | the engine/environment handle | `include`, `block`, `parent`, `source`, `template_from_string` |
| `needs_is_sandboxed` | the current sandbox-active boolean | the sandbox-forcing function-form `include` |

The injection flags are part of the registration surface, available to host
callables as well; see [Extensions & Loaders](extensions.md).

## Tests

Applied as `x is name` / `x is not name`. Two-word names are resolved greedily; a
test may take one mandatory or parenthesized argument.

| Test | Argument | Notes |
|------|----------|-------|
| `is defined` | none | True iff the operand resolves; never raises, even under strict mode. |
| `is null` / `is none` | none | True iff the value is `Null`. Aliases. |
| `is even` / `is odd` | none | Integer parity. |
| `is same_as(y)` | one mandatory | Same reference/kind (raw identity). Function form `same(x, y)`. |
| `is divisible_by(n)` | one mandatory | Integer divisibility, `x % n == 0`. |
| `is constant("NAME")` | one mandatory | True iff `x` equals the named host constant. |
| `is empty` | none | Total: `Null` -> true; `Str`/`*Array` -> true iff length 0; numbers/bools/objects -> false. |
| `is iterable` | none | True iff a collection or iterator; a string is not iterable. |
| `is sequence` | none | True iff a list-shaped `*Array`; empty is a sequence. |
| `is mapping` | none | True iff a non-list `*Array` or any `Object`; empty is not a mapping. |
| `is true` | none | True iff the value is `Bool` true. |
| `is string` / `is number` / `is int` / `is float` / `is bool` | none | Scalar-kind predicates. `is number` is `is int` or `is float`. |
| `is callable` | none | True iff the value can be invoked (arrow, host callable, `separator()` result). |
| `is eq(y)` / `is ne(y)` | one mandatory | Value equality and its negation via the one typed equality. |
| `is lt(y)` / `is le(y)` / `is gt(y)` / `is ge(y)` | one mandatory | The four ordering relations via the one ordering. |
| `is filter` / `is function` / `is test` | none | True iff a callable of that kind with the named string subject is registered. |

`is empty` is total over every kind, so `42 is empty`, `true is empty`, and
`someObject is empty` are all defined (false). The registry-existence tests
(`name is filter`, etc.) are the inline value-level form of the `@guard`
statement: `{{ "markdown" is filter }}`.

## Indentation and text-shaping helpers

These filters and functions shape indentation and vertical whitespace. They are
used heavily wherever line layout matters (indented markup, nested config,
program source), and they complement the trim modifiers in
[Whitespace Control](whitespace.md).

### `tab`: the indentation workhorse

`n | tab` produces `n` levels of indentation standalone (`{{ 1 | tab }}` emits one
indent), and `s | tab(n)` indents each non-blank line of `s` by `n` levels. One
level expands to `WithTabWidth` spaces (default 4), so `{{ 1 | tab }}` emits four
spaces by default and a host that sets `WithTabWidth(2)` gets two. A level of zero
or below emits no indentation.

### `space`, `break`, and `tab`: the indentation functions

| Function | Emits |
|----------|-------|
| `space` / `space(n)` | `n` spaces (default 1) |
| `break` / `break(n)` | `n` newlines (default 1) |
| `tab` / `tab(n)` | `n` indent levels (default 1), each `WithTabWidth` spaces |

A count of zero or below emits nothing.

### `@tab(n) { ... }`: the indentation-aware region

`@tab(n) { body @}` indents the entire rendered body by `n` levels. Indentation is
applied by the output layer to each non-blank line as the body renders, so it
covers interpolation, control-flow output, and included partials uniformly. Blank
lines stay blank, and regions nest cumulatively via an indent stack.

### `ucfirst`: byte-first upper-case

`ucfirst` upper-cases the first byte only and leaves the rest unchanged, distinct
from `capitalize` (which lower-cases the remainder).

### `indent`: the explicit multi-line indenter

`s | indent(n, unit: string = "    ")` indents each line of `s` by `n` units, with
the indentation unit configurable. It complements `tab`'s level-based model when
you want explicit control over the indent string.

### `raw` and the default-off escaping

The default output strategy is `off`: an interpolation renders the value's
`ToText` bytes verbatim. `raw` is a compile-time no-op that marks content
already-safe; under the default it is a no-op, and under an `escape`-on region it
switches a single site back to unescaped. See [Escaping & Safety](safety.md).

## Next

- [Extensions & Loaders](extensions.md): register your own filters, functions,
  and tests.
- [Expressions](guide/expressions.md): the call surface these callables use.
- [Types](types.md): the value rules they operate under.
