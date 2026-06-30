# Quill -- Standard Library

This is the reference for Quill's built-in standard library: the filters piped with `|`, the
functions called with `name(...)`, and the tests applied with `is` / `is not`. Every Twig
built-in filter, function, and test has an equivalent here; where Quill renames, folds, or
diverges, the divergence is stated with its reason. The runtime value rules these callables
operate under (truthiness, equality, ordering, coercion, undefined handling, escaping) are in
`04-types-and-semantics.md`. The call surface (pipe/call/test forms, named and defaulted
arguments) is in `01-language-reference.md` Sections 2 and 3.

The dynamic implementations are reused from the faithful Twig port's runtime (the `replace`
Replacer, the RE2 backing, the escapers, the ordered `*Array`); what is new is the uniform
calling surface and the gradual-type signatures attached to each entry.

--------------------------------------------------------------------------------

## 1. The three callable kinds and the naming convention

| Kind | Surface | First argument |
|------|---------|----------------|
| Filter | `x \| name` or `x \| name(args)` | the piped value `x` |
| Function | `name(args)` | none implicit; all explicit |
| Test | `x is name` / `x is name(arg)` / `x is not name` | the tested value `x` |

A filter is a function whose first parameter is supplied by the pipe: `x | f(a, b)` is exactly
`f(x, a, b)`. There is no separate filter versus function namespace -- a name resolves to at
most one filter, one function, and one test, and the syntactic position selects which. The
same registered callable MAY be exposed as both a filter and a function; the standard library
does so for `range`/`..` and the include family.

All filter and function names are lower `snake_case` (`format_number`, `url_encode`, `json`,
`date_modify`), matching Twig's own spellings. Twig's two-word tests become single
underscore-joined words at the canonical spelling (`is divisible_by(n)`, `is same_as(y)`);
the Twig two-word spelling (`is divisible by`, `is same as`) is accepted as an alias and
resolved greedily by the parser. Every callable accepts named, defaulted, and spread
arguments per the call surface.

Signatures below are written in the gradual type notation of `04-types-and-semantics.md`
Section 3: `T -> R` is "piped `T`, returns `R`"; `(T, A) -> R` lists the piped value first
then the explicit args; `T?` is nullable; `list<T>`/`map<K,V>` are the collection types.

--------------------------------------------------------------------------------

## 2. Filters

### 2.1 String filters

| Filter | Signature | Notes |
|--------|-----------|-------|
| `upper` | `string -> string` | Unicode upper-case, charset-aware. |
| `lower` | `string -> string` | Unicode lower-case, charset-aware. |
| `capitalize` | `string -> string` | First rune upper, REST lower-cased. |
| `title` | `string -> string` | First rune of each word upper, rest lower. |
| `trim(side: string = "both", mask: string = WS)` | `string -> string` | Strip from `side` (`"both"`/`"left"`/`"right"`, aliases `"b"`/`"l"`/`"r"`) using `mask`. One clean filter folds Twig's `trim`/`ltrim`/`rtrim`. |
| `nl2br` | `string -> Safe` | Replace `\n` with `<br />\n`; pre-escapes HTML, marks `Safe` for html. |
| `spaceless` | `string -> string` | Collapse inter-tag whitespace. Retained though Twig deprecates it. |
| `striptags(allowed: string = "")` | `string -> string` | Remove markup tags, optionally keeping `allowed`. |
| `replace(pairs: map<string,string>)` | `(string, map) -> string` | strtr-style: longest-key-first, non-overlapping, single-pass, byte-level. See Section 2.5. |
| `split(delim: string, limit: int = 0)` | `(string, ...) -> list<string>` | Split on `delim`; positive `limit` puts the remainder in the last element; empty `delim` chunks into runes (or `limit`-length chunks). |
| `slice(start: int, length: int? = null)` | `(string, ...) -> string` | Rune-based substring, negative `start` from the end; also backs `s[a:b]`. On a collection, slices elements. |
| `first` | `any -> any` | First rune of a string / first element of a collection. |
| `last` | `any -> any` | Last rune / last element. |
| `format(...args)` | `(string, ...) -> string` | printf with Go `fmt` verbs (`%s %d %v %q ...`), NOT PHP sprintf. Divergence: Section 2.6. |
| `format_number(decimals: int = 0, point: string = ".", sep: string = ",")` | `(number, ...) -> string` | Fixed-decimal formatting with separators. Alias `number_format`. |
| `convert_encoding(to: string, from: string)` | `(string, ...) -> string` | UTF-8-centric encoding conversion; documented mapping. |
| `ucfirst` | `string -> string` | Upper-case first BYTE only, rest unchanged; host filter, distinct from `capitalize`. See Section 5.2. |

### 2.2 Collection filters

| Filter | Signature | Notes |
|--------|-----------|-------|
| `length` | `any -> int` | String runes, collection count, or 1 for a scalar. |
| `join(glue: string = "", final: string? = null)` | `(list, ...) -> string` | Join with `glue`; optional `final` glue for the last pair ("a, b and c"). Each element rendered by `ToText`. |
| `merge(other)` | `(coll, coll) -> coll` | Integer-keyed values appended and reindexed; string-keyed overwritten; order preserved. See Section 2.5. |
| `keys` | `coll -> list` | Keys in insertion order. |
| `sort(by: (a, b) => int? = null)` | `(coll, ...) -> coll` | Total ordering (`04-types-and-semantics.md` Section 2), or a spaceship arrow; key-preserving. |
| `reverse(preserve_keys: bool = true)` | `(any, ...) -> any` | Reverse a collection (keys preserved by default) or a string by runes. |
| `batch(size: int, fill: any? = null)` | `(coll, ...) -> list<list>` | Fixed-size chunks; `fill` pads the last chunk. |
| `column(name)` | `(list, key) -> list` | Extract one attribute per row. |
| `map((value, key?) => expr)` | `(coll, fn) -> coll` | Transform, key-preserving. |
| `filter((value, key?) => bool)` | `(coll, fn) -> coll` | Keep where the arrow is truthy, key-preserving. |
| `reduce((acc, value, key?) => expr, initial: any = null)` | `(coll, fn, ...) -> any` | Left fold. |
| `find((value, key?) => bool)` | `(coll, fn) -> any` | First matching value, else `null`. |
| `shuffle(seed: int? = null)` | `coll -> coll` | Permute; `seed` makes it deterministic. |

### 2.3 Math filters

| Filter | Signature | Notes |
|--------|-----------|-------|
| `abs` | `number -> number` | Absolute value, preserving int/float. |
| `round(precision: int = 0, mode: string = "common")` | `(number, ...) -> float` | `mode` in `"common"`/`"ceil"`/`"floor"`; negative precision rounds to tens/hundreds. |
| `format_number(...)` | (see Section 2.1) | Same callable; listed once. |
| `range(...)` | (see Section 3.1) | The `..` operator and the `range` function share one engine. |

### 2.4 Encoding, serialization, date, and utility filters

| Filter | Signature | Notes |
|--------|-----------|-------|
| `json(pretty: bool = false, indent: string = "  ")` | `any -> string` | Serialize via Go `encoding/json` output rules; `pretty` switches to indented. Alias `json_encode`. Divergence: Section 2.6. |
| `url_encode` | `any -> string` | Percent-encode a string, or build a query string from a mapping. |
| `escape(strategy: string = "html")` | `any -> Safe` | Escape for a named strategy. Alias `e`. Escaping is OPT-IN; see Section 5.5. |
| `raw` | `any -> Safe` | Compile-time no-op marking content already-safe; never auto-escaped. Load-bearing for source emission; Section 5.4. |
| `date(layout: string = DEFAULT, tz: string? = null)` | `any -> string` | Format using a Go reference-time LAYOUT (`"2006-01-02 15:04:05"`), NOT PHP date codes. Divergence: Section 2.6. |
| `date_modify(delta: string)` | `(date, string) -> date` | Apply a relative modification (`"+1 day"`, `"-2 hours"`). |
| `default(fallback: any)` | `(any, any) -> any` | Yield `fallback` when the piped value is UNDEFINED or `Null`. See Section 2.7. |
| `invoke(...args)` | `(callable, ...) -> any` | Call a piped callable with arguments. |

### 2.5 Two semantics critical to source emission

- **`replace` (strtr-style).** Substitution is longest-key-first, non-overlapping,
  single-pass, byte-level: every position in the input is matched against the longest
  applicable key, the match is emitted as its replacement, and scanning resumes AFTER the
  match -- a replacement is never re-scanned. This is not naive sequential replacement (which
  would cascade `a->b` then `b->c`), and it is critical for source emission: such templates use
  `replace` to rewrite type tokens and must not cascade. Backed by the port's
  `strings.Replacer`.
- **`merge`.** Integer-keyed values from `other` are appended and reindexed onto the receiver;
  string-keyed values overwrite by key. Insertion order is preserved throughout. This is the
  array-union capability that `+` deliberately does NOT provide (`+` is numeric only).

### 2.6 The three documented formatting-dialect divergences

| Filter | Twig (PHP) dialect | Quill (Go) dialect | Reason |
|--------|--------------------|--------------------|--------|
| `format` | PHP `sprintf` specifiers | Go `fmt` verbs (`%v`, `%q`, `%05.2f`) | The runtime IS Go fmt; reimplementing PHP specifiers is pure liability. |
| `json` | PHP `json_encode` byte shape (escapes `/`, `\uXXXX` non-ASCII, sorted keys) | Go `encoding/json` (no HTML escaping, ordered keys, literal `/`) | Source emission must not HTML-escape; ordered keys are deterministic. |
| `date` | PHP `date()` codes (`Y-m-d H:i:s`) | Go reference layout (`2006-01-02 15:04:05`) | The runtime parses Go layouts; PHP codes would be a second date language. |

### 2.7 The `default` filter and emptiness

`default(x, fallback)` yields `fallback` when `x` is UNDEFINED or `Null` (definedness,
`04-types-and-semantics.md` Section 6), never raising on undefined. The anchor's
`name | default("guest")` yields `"guest"` when `name` is undefined or null. `0 | default("y")`
keeps `0` (it is defined and non-null) -- the one useful Twig behavior, without a bespoke
emptiness predicate. The separate `is empty` test (Section 4.1) covers length-zero
collections/strings and `Null`.

--------------------------------------------------------------------------------

## 3. Functions

### 3.1 The `range` engine (shared with `..` and the `range` filter)

`range(low, high, step: number = 1)` builds an inclusive numeric or single-character range
sequence. The `..` operator (`1..5`, `'a'..'e'`) is the same engine. For the sandbox's
compile-time callable collection, a `..` counts as using the `range` function.

### 3.2 Aggregate, access, composition, iteration, and registry functions

| Function | Notes |
|----------|-------|
| `max(a, b, ...)` or `max(iterable)` | Maximum by the single total ordering. |
| `min(a, b, ...)` or `min(iterable)` | Minimum. |
| `attribute(var, name, args: list? = null)` | Read member `name` (runtime-computed) of `var`, optionally calling it with `args`; the dynamic form of `a.b`. |
| `parent()` | Render the parent block; legal only inside an overriding block of an inheriting template. |
| `block(name, template: string? = null)` | Render a named block of this or another template. `block("x") is defined` tests existence. |
| `include(template, vars: map = {}, with_context: bool = true, ignore_missing: bool = false, sandboxed: bool = false)` | Function-form include returning rendered output, distinct from the `include` statement. |
| `source(name, ignore_missing: bool = false)` | Return the raw, unparsed source of a template. |
| `cycle(values, position)` | `values[position % length]`, wrapping. |
| `random(values: any? = null, max: int? = null)` | Random element of a collection, random int in `[0, max]`, or a random character; seedable for tests. |
| `constant(name, obj: any? = null, check_defined: bool = false)` | Resolve a host/global or class constant by name; `check_defined: true` returns whether it exists. |
| `enum(name)` | First case of a host enumeration. |
| `enum_cases(name)` | All cases of a host enumeration in declaration order. |
| `date(date: any? = null, tz: string? = null)` | Construct a date/time value from a string/timestamp and timezone (Go date model). |

### 3.3 Debug and dynamic functions (opt-in)

| Function | Notes |
|----------|-------|
| `dump(...vars)` | Debug-dump the given variables, or the whole context if none; `null` outside debug mode. Go-native dump format (a `%#v`-style structured render), NOT PHP `var_dump`. |
| `template_from_string(source, name: string? = null)` | Compile a string into a template at runtime. Security-sensitive; gated behind host opt-in; never exposed to untrusted authors. |

### 3.4 Go-native convenience aliases (no floor change)

`len(x)` aliases `x | length` (the Go idiom for length), and `keys(m)` aliases `m | keys`
(reads as a function on a map). These add reachability, not capability; the filter forms
remain canonical.

### 3.5 Host functions and the registration mechanism

The host registers functions with positional, named, defaulted, and spread parameters. Two
representative host functions are catalogued because they demonstrate
the registration surface:

| Function | Signature | Notes |
|----------|-----------|-------|
| `getJavaListDataType(name)` | `string -> string` | Map a type name (`boolean`/`int`/`long`/`float`/`double`/`char`) to its Java boxed type, else echo the input. |
| `subtractOne(e)` | `any -> int \| string` | If `e` is all-digits, return `int(e) - 1`; else return the string `"<e> - 1"`. Polymorphic return typed `int | string`. |

The host-function registration mechanism itself is a parity capability (the extension surface,
not just the functions); see `06-architecture-and-roadmap.md`.

### 3.6 Runtime-injected parameters: charset, context, environment, sandbox state

A registered filter, function, or test declares not only its user-visible parameters
(positional, named, defaulted, spread, Section 1) but also which engine values the runtime
must INJECT ahead of the user arguments. The injection flags, reused from the port's callable
option bag, are the parity mechanism for X6/X7/X8/FL44/F19 -- the registration surface, not
just the catalogued callables:

| Flag | Injected value | Used by |
|------|----------------|---------|
| `needs_charset` | the active charset string (`_charset`, default `"UTF-8"`) | `upper`, `lower`, `capitalize`, `title` (the four case filters), the codepoint escapers `js`/`css`/`html_attr` |
| `needs_context` | the live context as an `*Array` | `include`, `block`, `dump`, `template_from_string` |
| `needs_environment` | the engine/environment handle | `include`, `block`, `parent`, `template_from_string`, `source` |
| `needs_is_sandboxed` | the current sandbox-active boolean | the sandbox-forcing function-form `include` |

When a flag is set, the runtime PREPENDS that value to the argument list before the piped
value (for a filter) and before any user arguments, in the fixed order
environment, context, charset, is_sandboxed (only the flagged ones appear). A registration
sketch:

```go
reg.Filter("upper").NeedsCharset().Fn(upperImpl)        // upperImpl(charset, s)
reg.Function("include").NeedsEnvironment().NeedsContext().Fn(includeImpl)
```

This is the same plumbing the reused port already implements; Quill keeps it verbatim. The
charset value supplied to `needs_charset` callables is exactly the `_charset` special name
(`01-language-reference.md` Section 1.7), so the case filters and the codepoint escapers
operate against one configured charset. A host-registered callable with no injection flag set
receives only its declared user parameters.

--------------------------------------------------------------------------------

## 4. Tests

Applied as `x is name` / `x is not name`. Two-word names are resolved greedily; a test may take
one mandatory or parenthesized argument.

| Test | Argument | Notes |
|------|----------|-------|
| `is defined` | none | True iff the operand RESOLVES; never raises even under the strict-by-default runtime. The operand flips to existence-check mode rather than being evaluated. The load-bearing test under strict mode. |
| `is null` / `is none` | none | True iff the value is `Null`. Aliases. |
| `is even` | none | True iff an integer is even. |
| `is odd` | none | True iff an integer is odd. |
| `is same_as(y)` (alias `is same as`) | one mandatory | True iff `x` and `y` are the same reference/kind; subsumes Twig `===`. Function form `same(x, y)`. |
| `is divisible_by(n)` (alias `is divisible by`) | one mandatory | Integer divisibility, `x % n == 0`. |
| `is constant("NAME")` | one mandatory | True iff `x` equals the named host constant. |
| `is empty` | none | TOTAL over all eight kinds: `Null` -> true; `Str`/`*Array` -> true iff length 0; `Int`/`Float`/`Bool`/`Object`/`Safe`(unwrapped) -> false. See Section 4.1. |
| `is iterable` | none | True iff a collection or iterator; a STRING is NOT iterable. |
| `is sequence` | none | True iff a list-shaped `*Array`; empty IS a sequence. |
| `is mapping` | none | True iff a non-list `*Array` or any `Object`; empty is NOT a mapping; any object IS. |
| `is true` | none | True iff the value is `Bool` true (`Safe`-unwrapped first). |

### 4.1 `is empty` -- the one emptiness predicate

`is empty` is TOTAL -- every value of every kind answers it, with no undefined case and no
runtime error:

| Kind | `is empty` |
|------|-----------|
| `Null` | true |
| `Str` | true iff length 0 (`""`) |
| `*Array` | true iff length 0 (`[]`, `{}`) |
| `Int`, `Float` | false -- a number is a value, never empty (`0 is empty` is false) |
| `Bool` | false -- `true`/`false` are values (`false is empty` is false) |
| `Object` | false -- an object has a value |
| `Safe` | the result for its unwrapped content (a `Safe ""` is empty) |

So `is empty` answers only "is this a length-zero string/collection or `Null`?"; `0`, `"0"`,
`0.0`, `false`, and any object are NON-empty. This de-PHP-ifies the test and keeps it total, so
`42 is empty`, `true is empty`, and `someObject is empty` are all defined (false) rather than a
gap. Twig's four coexisting emptiness notions collapse to two in Quill -- truthiness and
definedness -- plus this one explicit, total length check. It backs `default`'s sibling
capability without a bespoke `testEmpty` rule.

### 4.2 Host-test registration

The host registers additional tests through the extension surface
(`06-architecture-and-roadmap.md`); a host test takes the tested value and zero-or-more
arguments and returns a boolean.

--------------------------------------------------------------------------------

## 5. Source-code-emission helpers

These are the host filters source-emitting templates depend on, plus Go-native additions for
the same workload. Indentation (`tab`) and case helpers (`ucfirst`) are heavily used across
real source-generation template sets.

### 5.1 `tab` -- the indentation workhorse

`n | tab` produces `n` levels of indentation standalone (e.g. `{{ 1 | tab }}` emits one
indent), and `s | tab(n)` indents each non-blank line of `s` by `n` levels. The form
`{{ 1 | tab() }}` (with empty parens) is valid Quill; the empty arg list is optional and may be
dropped. Its argument check is expressed in Quill truthiness and length, NOT PHP `empty()`.

### 5.2 `ucfirst` -- byte-first upper-case

`ucfirst` upper-cases the first BYTE only and leaves the rest unchanged, distinct from
`capitalize` (which lower-cases the remainder).

### 5.3 `indent` -- the explicit multi-line indenter

A Go-native addition: `s | indent(n, unit: string = "    ")` indents each line of `s` by `n`
units, with the indentation unit configurable. It complements `tab`'s level-based model when an
author wants explicit control over the indent string.

### 5.4 `raw` and why escaping is off by default

The default output strategy is `off`: an interpolation renders the value's `ToText` bytes
verbatim with no transformation. `raw` is a compile-time no-op safeness annotation that marks
content already-safe; under the default it is a no-op, and under an `escape`-on region it
switches a single site back to unescaped. The Twig corpus carried `| raw` on nearly every
interpolation purely to cancel Twig's html-on default; in Quill those annotations become
unnecessary because the correct default is the actual default. See `04-types-and-semantics.md`
Section 8.

### 5.5 The `escape` filter and the six strategies

`escape(strategy)` (alias `e`) escapes for a named strategy; escaping is opt-in. The six
strategies retained from Twig for markup-emitting templates are:

| Strategy | Escapes |
|----------|---------|
| `html` | `& < > " '` for HTML text (`'` as `&#39;`) |
| `js` | a string for safe embedding in JavaScript |
| `css` | a string for safe embedding in CSS |
| `html_attr` | a string for an HTML attribute value |
| `html_attr_relaxed` | an HTML attribute value, allowing `:@[]` |
| `url` | percent-encode for URLs (RFC 3986; space -> %20) |

The escaper machinery, the per-strategy safeness, the pre-escape filters (e.g. `nl2br`), and
the safeness inference are reused from the port and are active only when escaping is enabled;
only the DEFAULT flips from `html` to `off`. The full escaping and sandbox model is in
`04-types-and-semantics.md` Section 8.
