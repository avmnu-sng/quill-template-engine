# Quill -- Types and Semantics

This is the reference for Quill's runtime value layer and its gradual type system: the value
domain, the one truthiness rule, the one equality and one ordering, the single narrow
coercion that renders a value to text, the kind-dispatched attribute and index access rules,
the strict-by-default undefined handling, the escaping and safety subsystem, and the sandbox.
Each rule that diverges from Twig carries a named, justified "Twig divergence" note;
faithfulness to Twig is required at the FEATURE level, not at the level of PHP value
accidents.

Every rule below serves the source-emitting axiom: Quill's primary consumer emits PROGRAM
SOURCE CODE, one exact byte sequence at a time. A silently-absent value becomes a silently
wrong emitted byte; a silently-coerced value becomes a silently wrong emitted token. PHP's
value semantics are a catalogue of silent surprises; Quill removes them.

The runtime backing this layer is reused from the faithful Twig port. The value struct, the
ordered `*Array`, the `Context` save/restore, the `Safe` wrapper, and the host `Object`
interface are taken essentially unchanged. Four modules change -- `compare.go`, `truthy.go`,
`stringify.go`, `attribute.go` -- and one default flips (escaping). The gradual type checker
is a NEW front-end pass with no PHP analogue.

--------------------------------------------------------------------------------

## 1. The gradual type system

### 1.1 Shape

Quill is gradually typed: a template carries as many or as few annotations as its author
chooses. With zero annotations a template is the dynamic language of Sections 2-7, the
strict-by-default runtime is the only line of defense, and the anchor is valid verbatim. With
annotations, the same template gains a static checker that rejects, before any byte is emitted,
a class of error the runtime would otherwise raise mid-render.

The governing slogan: an annotation moves an error earlier in time; it never moves the runtime.
Removing every annotation from a Quill template yields a program that renders identical bytes.
An unannotated value has static type `any` and is checked only dynamically; an annotated value
is checked statically where its type is known and dynamically at the `any`-boundary. This is
classic gradual typing with a SHALLOW boundary cast: the `any`-to-typed cast is an O(1)
top-kind check, so it is fully sound for a scalar target but proves only the container kind for
a structured target (`list`, `map`, `Object`). Accordingly, a SCALAR that crossed the boundary
is trusted and its downstream checks are erased; a STRUCTURED value that crossed the boundary
RETAINS its strict-undefined runtime check on member and element access, because the shallow
cast did not verify its members. Soundness is therefore honest: the checker erases only what the
cast proved, and the strict-by-default runtime is the floor it never removes across an
unverified boundary (`type-system.md` Section 7.5-7.6).

### 1.2 Why a code generator wants gradual types

Two facts about the workload set the type system's priorities. First, a mistake in a generator
template is expensive to find late: a silently-absent value emits an entire missing method; a
silently-coerced value emits a wrong literal into compiled code. The strict-by-default runtime
turns those silent faults into loud runtime errors; the checker's job is to turn the subset a
static reader can see into errors that fire at template-load time, before any output. Second,
the corpus is large and mostly untyped (it descends from Twig, whose `types` block is inert
metadata). A type system demanding annotations everywhere would be unadoptable; gradual typing
lets a large corpus stay untyped while a single hot template opts into checking, incrementally,
with no flag day.

### 1.3 The type lattice

```
any                          // the dynamic top; unannotated values are any
null bool int float string   // scalar base types
list<T>                      // ordered sequence of T
map<K, V>                    // ordered mapping (K is int|string)
T?                           // nullable: sugar for T | null
(A, B) => R                  // arrow/callable types
Object<"Type">               // a host-registered named type
A | B                        // union (for polymorphic host returns, e.g. subtractOne -> int | string)
```

`any` is gradual: a value of type `any` may flow into any typed slot and vice versa, with a
runtime check inserted only where the dynamic value crosses into a typed context.

### 1.4 Annotation sites

```
types {
  users: list<Object<"User">>
  title: string?
}

@macro field(name: string, value: any = null, required: bool = false) -> string { ... @}

@block summary -> string { ... @}
@block body(user: Object<"User">) { ... @}

@set count: int = users | length
@for u: Object<"User"> in users { ... @}
{{ (x: int) => x * 2 }}
```

The `types { ... }` block is the file-scope form of the per-variable annotations, promoted from
an opaque tooling channel to a first-class checker input. Macro params and returns (`-> string`),
block returns and inputs, `set` targets, `for` targets, and arrow params all take optional
annotations. Annotations attach identically under `pragma bare` (drop the `@` lead, close on a
lone `}`); the arrow example `{{ (x: int) => x * 2 }}` is mode-neutral. The annotation grammar
is in `02-grammar.md` Section 5.

### 1.5 What the checker catches

Where types are present, the checker statically rejects -- each promoting a runtime error from
the sections below to check time:

- An undefined read of a declared variable, or a missing field on a typed `Object` / `map`
  (promotes the Section 6 runtime miss).
- A cross-kind comparison or arithmetic on known-incompatible types (`"3" + 4` is a check-time
  error if `"3"` is typed `string`).
- A `for` over a value with a non-iterable static type (promotes the Section 4.2 loop error of
  `01-language-reference.md`).
- Rendering a `list`/`map`-typed value as text (promotes the array-render error of Section 4).
- A filter/function/macro called with wrong arity or argument types, given registered host
  signatures.

Where types are ABSENT (everything is `any`), the checker does nothing and the
strict-by-default runtime is the only line of defense -- so untyped Quill behaves exactly like
the dynamic floor. The two layers agree: the static checker promotes a runtime error to check
time; it never permits something the runtime would reject.

**Twig divergence.** Twig's `types` block is inert tooling metadata, never enforced. Quill's
gradual layer enforces it where present. This EXCEEDS the Twig floor; it never lowers it,
because an annotation-free template renders identical bytes.

--------------------------------------------------------------------------------

## 2. The value domain and truthiness

### 2.1 The value domain

A Quill value is exactly one of eight kinds, the same set as the faithful port's runtime:

| Kind | Go carrier | Meaning |
|------|------------|---------|
| `Null` | (none) | the absence value; renders to the empty string |
| `Bool` | `bool` | `true` or `false` |
| `Int` | `int64` | a signed 64-bit integer |
| `Float` | `float64` | an IEEE-754 double |
| `Str` | `string` | a BYTE string, may be invalid UTF-8 (for lossless byte emission) |
| `*Array` | `*Array` | the ordered, dual-view collection (Section 7) |
| `Object` | host interface | field read, method call, stringify, count, iterate, class name |
| `Safe` | wrapper | already-safe-output carrier (Quill's rename of Twig's `Markup`) |

The numeric model is int64 and float64: `/` is true division (float result unless exact), `//`
is floor division, `%` is remainder, `**` is right-associative power. Int overflow is a defined
error, NOT a silent promotion to float. `Int == Float` bridges numerically -- there is one
number tower.

#### Result kinds, rounding, and the uniform overflow/error rule

The result kind and the error conditions for every arithmetic operator are fixed; nothing is
left to a silent fallback that would emit a wrong numeric literal.

| Op | Result kind | Rules |
|----|-------------|-------|
| `+` `-` `*` | `Int` if both `Int`; `Float` if either is `Float` | int64 overflow is an ERROR (not float promotion) |
| `/` | `Float`, unless both `Int` and the division is exact (then `Int`) | division by zero is an ERROR |
| `//` floor div | `Int` for two `Int`; floored `Float` if either is `Float` | floor of the true quotient; division by zero is an ERROR; int64 overflow is an ERROR |
| `%` remainder | follows the dividend kind | TRUNCATED remainder (Go semantics): sign follows the dividend, so `(-7) % 3 == -1` and `7 % (-3) == 1`; for floats `5.5 % 2.0 == 1.5` (`math.Mod`); modulo by zero is an ERROR |
| `**` power | `Int` for an `Int` base and a NON-NEGATIVE `Int` exponent with no overflow; `Float` for a NEGATIVE integer exponent, any `Float` operand, or an `Int`/`Int` result that would overflow int64 | a NEGATIVE integer exponent always yields `Float` (`2 ** -1 == 0.5`); an int result that overflows is an ERROR, NOT a float promotion |

**The overflow rule is UNIFORM across `+ - * // **`: an int64 result that overflows is a
defined arithmetic error naming the operation and operands -- never a silent promotion to
`Float`.** The earlier formulation that `**`/`//` "fall back to Float on overflow" is
superseded: overflow is an error everywhere, matching the `+ - *` ruling, so a generator never
emits a silently-truncated or silently-widened 64-bit literal. The only kind PROMOTIONS are the
non-overflow rules above (e.g. a negative `**` exponent, a non-exact `/`), which are exact and
documented, not error-recovery.

**Division and modulo by zero (`/`, `//`, `%`) are defined arithmetic errors** naming the
operands, consistent with the overflow stance. They never produce `+Inf`, `-Inf`, or `NaN`.

#### NaN and Inf are never produced; the totality holes are closed at the source

`Float` is IEEE-754 float64, whose `NaN` would break the reflexivity of `==` (`NaN == NaN` is
false), the totality of `Order` (`NaN` is unordered against every value), and therefore the
determinism of `sort`/`min`/`max` and of structural `*Array` equality (a single `NaN` element
would make an array unequal to itself). Quill closes these holes at the source rather than
patching the comparators:

- **Every operation that would PRODUCE a non-finite float is a defined arithmetic error.** This
  includes `0.0 / 0.0`, `x / 0.0`, `0.0 // 0.0`, `x % 0.0`, `0.0 ** -1`, and any `**` whose
  IEEE result is `+Inf`/`-Inf`/`NaN`. The error names the operation and operands, exactly as
  int overflow does. This is the coherent choice for a source generator: a `NaN` or `Inf` token
  is invalid in nearly every target language, so producing one is always a bug.
- **A non-finite float entering from the host** (a host `Object` returning a `NaN`/`Inf`
  float64, or JSON parse of a non-finite literal) is rejected at the value boundary with the
  same arithmetic error, so no `NaN`/`Inf` value ever circulates in the runtime.

Because non-finite floats are unrepresentable in a live `Value`, `==` stays reflexive and
`Order` stays total over the entire `Float` kind without any special NaN case, and `ToText`
(Section 4) never has to render `+Inf`/`NaN` literals. The `Float` row of the `ToText` table is
total over the finite floats it admits.

### 2.2 Truthiness -- one rule

The falsy set is exactly `{ Null, false, 0, 0.0, "", empty *Array }`. Everything else is truthy
-- crucially, `"0"` is TRUTHY (a non-empty string), and any `Object` is truthy. This one rule is
used by `if`, the postfix `if`, the ternary, `?:`, and the boolean operators.

**Twig divergence.** PHP makes `"0"` falsy (a string-as-number accident); Quill never overloads
strings as numbers, so the carve-out has no reason to exist and removing it erases the classic
trap.

--------------------------------------------------------------------------------

## 3. Equality and ordering

### 3.1 Equality

`==` / `!=` is typed equality. Same-kind values compare by value; `Int`==`Float` bridges
numerically; `Safe` normalizes to its wrapped `Str` before comparison (see below); EVERY other
cross-kind pair is `false` (`1 == "1"` false, `null == ""` false, `true == 1` false). It is
total, symmetric, and reflexive with no coercion -- reflexivity holds over the whole `Float`
kind because non-finite floats (`NaN`) are never produced or admitted (Section 2). `*Array`
equality is structural (same length, same keys in the same order, recursively `==`). `Object`
equality is identity unless the host defines an equal hook. `===` is an accepted alias of `==`;
`same(a, b)` is raw reference/kind identity.

**`Safe` is transparent for equality, ordering, membership, and structural compare.** Under the
default (escaping off) a `Safe` value is an inert passthrough indistinguishable from a `Str`
(Section 8.2). To make that claim hold for comparison and not only for rendering, a `Safe` value
is NORMALIZED to its wrapped `Str` BEFORE `==`/`!=`, `Order`, `in`, and structural `*Array`
compare. So `Safe("x") == "x"` is true, `Safe("x") == Safe("x")` is true, and an array
containing a `Safe` element compares equal to the same array containing the equivalent `Str`.
This is a SECOND cross-kind bridge alongside `Int`/`Float`: the cross-kind equality rule is
"every cross-kind pair is false EXCEPT Int/Float (numeric bridge) and Safe-unwraps-to-Str." A
`Safe` against a non-`Str`, non-`Safe` kind compares as its unwrapped `Str` would (so
`Safe("1") == 1` is false, like `"1" == 1`). Normalizing before the structural recursion keeps
array equality consistent regardless of which elements are wrapped, and transitive: two arrays
that render identically compare equal.

**Twig divergence.** PHP loose `==` (`0 == "foo"` false, `"1" == "1.0"` true, `null == false`
true) is the single largest source of silent wrong behavior in PHP; a source generator needs
equality it can reason about by type. Quill's `==` occupies the role of both Twig's `===` (for
safety) and `==` (for ergonomics), keeping only the `Int`/`Float` numeric bridge.

### 3.2 Ordering

`<` `>` `<=` `>=` `<=>`, plus membership `in`/`not in`, `sort`, `min`, and `max`, are backed by
ONE comparator. It is total within the number tower and between two strings (byte-lexicographic),
and defined nowhere across unlike kinds. Totality within the number tower is genuine because
`NaN` is never produced or admitted (Section 2), so `sort`/`min`/`max` are deterministic with no
unordered element; a comparator that could see `NaN` would silently produce a nondeterministic
ordering, the exact failure this rules out at the source. Cross-kind ordering is a check-time
error where types are known, else a runtime error -- never silent juggling. `Safe` orders as its
wrapped `Str` (Section 3.1). Membership `in` uses typed `==`, so `"1" in [1]` is FALSE.

**Twig divergence.** Twig used three comparison algorithms (`LooseEq` for `==`, `Compare` for
`in`/sort, `Identical` for `===`), and routed `in` through a numeric `Compare` that bridged
numeric strings. Quill collapses these to two -- typed `==` and one ordering comparator -- and
`in`/`==`/`same as` share one equality definition. Three subtly different comparison algorithms
in one engine is exactly the hazard this language removes.

--------------------------------------------------------------------------------

## 4. Coercion and the output path

Coercion is EXPLICIT and NARROW. There is exactly ONE implicit coercion: rendering a value to
text at an interpolation site or in `~` concat. Arithmetic and comparison NEVER coerce; `"3" + 4`
is a type error, not `7`. The `ToText` rules:

| Value | Renders to |
|-------|-----------|
| `Null` | `""` |
| `Bool` | `"true"` / `"false"` (NOT PHP `"1"` / `""`) |
| `Int` | decimal, no separators |
| `Float` | shortest round-trippable decimal (`1.0` -> `"1"`, `1.5` -> `"1.5"`) |
| `*Array` | RENDER ERROR (NOT the literal `"Array"`); use `join` / `json` |
| `Object` | its `Stringify` hook, else a render error |
| `Safe` | the wrapped content |

`~` concatenation renders each operand by these rules; `+` is numeric only.

**Twig divergences, all byte-load-bearing for source emission.** `Bool` renders `true`/`false`
(not PHP `true`->"1", `false`->""): an invisible empty string for `false` is a notorious silent
bug, and `true`/`false` are the literal boolean tokens of nearly every target language. `Float`
uses Go shortest round-trippable form, not PHP `precision` formatting, because Quill emits source
not PHP output. An `*Array` render is an explicit error, not PHP's `"Array"` string, which would
inject the literal word `Array` into the emitted program. An `Object` with no `Stringify` hook is
a render error, not an ambient best-effort `__toString` -- the coercion is an explicit, auditable
hook, not PHP magic.

--------------------------------------------------------------------------------

## 5. Attribute and index access

Two distinct operators with two distinct rules, NOT one fused PHP-style resolver:

- **`a.b`** -- dotted access, kind-dispatched and short. If `a` is an `*Array`, read string key
  `"b"`; if `a` is an `Object`, read public field `b`, then accessor `getB`/`isB`/`hasB`
  (precedence `get > is > has`), then a class constant `b`. The kind of `a` selects the lookup
  family; there is no cross-kind cascade, no `__call` magic, no DateTime special-casing. `a.b`
  reads a field/accessor; `a.b()` invokes a method.
- **`a[k]`** -- subscript. `*Array` by key, or an `Object` host index interface. Only int and
  string keys; bool/float/null subscripts are type errors. Slices `a[start:end]`, `a[:end]`,
  `a[start:]`, and negative indices route to the slice operation.
- **`a?.b`, `a?[k]`** -- null-safe. A null receiver short-circuits the whole chain to `null`,
  regardless of strictness.
- **Dynamic access** `attribute(a, name, args?)` for a runtime-computed member name.

**Twig divergence.** PHP's single ANY_CALL cascade (array key -> property -> DateTime keys ->
class constant -> exact method -> lowercased method -> `__call`) is replaced by kind dispatch. The
get/is/has accessor sugar is kept with precedence `get > is > has`, but the PHP alphabetical-sort
and no-overwrite tie-breaking cruft is dropped for a deterministic declared-order rule on the host
object model. Bool/float/null subscripts are errors, not silent int-casts or the empty-string key,
because indexing by a boolean or a fraction is always a generator mistake.

--------------------------------------------------------------------------------

## 6. Scoping and undefined handling -- the headline divergence

Scoping is lexical and block-structured. `block`, `for`, `macro`, `with`, and `capture` each
introduce a scope, reusing the port's context save/restore: loop bodies restore pre-loop bindings
on exit and drop body-local `set`s; macros see only their parameters plus globals; `include`
passes a merged context.

Undefined handling is strict-by-default and gradual:

- Reading an undefined context variable, an absent `*Array` key via `a.b`/`a[k]`, or an absent
  object member is a RUNTIME ERROR naming the symbol and listing the available keys -- NOT a
  silent `Null`. This is the inverse of Twig's `strict_variables=false` default, chosen because a
  silently absent value is a silently wrong emitted byte -- the costliest possible failure for a
  generator.
- Absence is made explicit with three tools: the `is defined` test (true/false, never throws), the
  `?.`/`?[]` null-safe operators (short-circuit to `Null`), and the `default(x, fallback)` filter
  / `a ?? b` coalescing operator (yield the fallback when the left is undefined or null). These
  suppress the strict-undefined miss across the ENTIRE left-operand access chain, not merely its
  final hop (the suppression-depth rule below), reusing the port's `ignoreStrictCheck` plumbing.

#### Suppression depth: the whole-chain rule

`??`, `default`, and `is defined` set the "absence allowed" flag for the COMPLETE evaluation of
their left operand -- every hop in an access chain, including the receiver -- so an absent
variable or an absent intermediate member at ANY depth yields the fallback rather than an error.
`?.`/`?[]` cover the whole chain too, differing only in WHAT they catch. The per-hop table, for
`a`, `a.b`, and `a.b.c` undefined at each hop:

| Expression | `a` absent | `a` present, `.b` absent | `a.b` present, `.c` absent |
|------------|-----------|--------------------------|----------------------------|
| `a ?? fb` | `fb` | -- | -- |
| `a.b ?? fb` | `fb` | `fb` | -- |
| `a.b.c ?? fb` | `fb` | `fb` | `fb` |
| `a.b \| default(fb)` | `fb` | `fb` | -- |
| `a.b.c \| default(fb)` | `fb` | `fb` | `fb` |
| `a.b is defined` | `false` | `false` | -- |
| `a.b.c is defined` | `false` | `false` | `false` |

So `user.nick ?? "anon"` yields `"anon"` when `user` is absent, when `user` is present but
`.nick` is absent, or when `user.nick` is present-and-null -- the common generator case "this
whole path may be absent" works with the bare `??`, with no `?.` required. The chained idiom
`user.nick ?? user.name ?? "anon"` and the guard `user.email is defined` therefore behave as the
headline examples assume: the receiver `user` need not be separately proven defined.

**`??`/`default`/`is defined` versus `?.`** -- both cover the whole left chain; they differ in
the trigger. `?.`/`?[]` short-circuit on a NULL receiver encountered mid-chain (a present-but-
null hop stops the chain and yields `Null`), whereas `??`/`default`/`is defined` catch an ABSENT
variable or member at any hop (and `??`/`default` additionally fall back on a final `Null`
value). They compose: `u?.address.city ?? "n/a"` short-circuits if `u` is null and falls back if
`address` or `city` is absent.
- `is defined` is true for a present key even if its value is `Null`: it tests presence, not value,
  cleanly separating "absent" (an error to read) from "present but null" (renders empty).
- A `lenient` mode flag restores silent-`Null` for migration; it is OFF by default. Under
  `lenient`, a strict miss becomes `Null` and `for` over a non-iterable becomes an empty loop
  (Twig-compatible).

The gradual type checker, where annotations are present, promotes many of these misses to
check-time errors so they never reach run time.

--------------------------------------------------------------------------------

## 7. The `*Array` and the key model

Quill keeps one ordered, dual-view collection: an ordered key slice plus a value map. Iteration is
insertion order; `for k, v in mapping` iterates in insertion order with both targets. `is sequence`
/ `is mapping` split on list-shape (an empty `*Array` is a sequence, not a mapping). The key model:
a canonical decimal-integer key is an integer key; everything else is a string key (`"01"`, `"1.0"`,
`" 1"`, `"+1"` stay strings). `*Array` values are value types, copy-on-write at loop, include, and
rebind boundaries -- a clean Go-native semantic matching the slice/map mental model, not a PHP
accident.

**Twig divergence.** The numeric-string-key canonicalization is KEPT but tightened and stated as an
explicit language rule rather than an emergent PHP behavior. Bool/float/null subscripts are type
errors rather than silent int-casts (Section 5).

--------------------------------------------------------------------------------

## 8. Escaping, safety, and the sandbox

### 8.1 The governing axiom and the default

A Quill template emits one exact byte sequence of program source. If the active strategy were
`html`, a Java generic `List<Map<String,Integer>>` would emit `List&lt;Map&lt;...&gt;&gt;` -- a
syntactically broken file. An ampersand in a C bitwise expression, a `<` in a Rust generic, a `>`
in a shell redirect, a `"` in a string literal: every one is a load-bearing source byte that HTML
escaping would mangle.

Therefore the DEFAULT output strategy is `off` (synonym `raw`): an interpolation renders the
value's `ToText` bytes verbatim with no transformation. Escaping is opt-in: per template or region
via `escape html { ... }`, or per site via `| escape` / `| e("html")`. The six strategies (`html`,
`js`, `css`, `html_attr`, `html_attr_relaxed`, `url`) are all retained as named strategies for
markup-emitting templates, preserving the floor.

**Twig divergence.** Twig defaults to html autoescape; the Twig corpus carried `| raw` on nearly
every interpolation purely to cancel it. Quill makes the correct default the actual default; those
`| raw` annotations become no-ops the corpus no longer needs.

### 8.2 The safeness machinery

- `raw` filter / safeness annotation: a compile-time no-op marking content already-safe; never
  auto-escaped. Inert under the default; switches a single site back to unescaped under an
  `escape`-on region.
- `Safe` value: the already-escaped carrier (the port's `Markup`, renamed), returned unchanged by
  `escape`, produced by captures and macros under escaping, a plain-string passthrough when escaping
  is off.
- Per-strategy filter safeness, pre-escape filters (e.g. `nl2br`), and safeness inference over
  ternary/conditional operands -- reused from the port, active only when escaping is enabled.
- Default-strategy selection: fixed value, off, or a host-supplied resolver including by file
  extension (`body.html.ql` -> `html`). Default is off; the host may register a resolver.
- Compile-time escape injection: escaping is decided and injected at compile time, so the off-path
  has zero render cost and output is deterministic.

**Charset and invalid-UTF-8 behavior of the strategies.** A `Str` is a byte string that may be
invalid UTF-8 (Section 2.1), so the escapers split into two classes:

- `html` and `url` are BYTE-oriented and accept arbitrary bytes losslessly: `html` substitutes
  only the five ASCII characters `& < > " '` and passes every other byte through unchanged;
  `url` percent-encodes byte-by-byte. Neither needs the charset and neither errors on invalid
  UTF-8.
- `js`, `css`, `html_attr`, and `html_attr_relaxed` are CODE-POINT-oriented: they escape by
  Unicode code point (`\uXXXX`, `\XX `, `&#xHHHH;` forms), so they first DECODE the `Str` as the
  configured charset (`_charset`, default UTF-8) injected via `needs_charset`
  (`03-stdlib.md` Section 3.6). If the bytes are not valid in that charset, the escaper raises a
  clear escaping error naming the strategy and the byte offset -- it does NOT silently emit
  replacement characters, because a silent substitution in emitted code is a wrong byte. This is
  the port's retained guard for the code-point strategies, now tied explicitly to the
  charset-injection mechanism.

A markup-emitting template that opts into `escape js` (or `css`/`html_attr`) therefore has fully
defined behavior on an invalid-UTF-8 `Str`: a load-time-clear escaping error, not undefined
output. The escaper machinery is reused wholesale; only the DEFAULT flips from `html` to `off`,
and the code-point strategies' charset decode is stated here rather than left implicit.

### 8.3 The sandbox

A host-supplied security policy restricts permitted tags, filters, functions, per-type methods, and
per-type properties. Method/property allowlisting matches against an explicit host TYPE-GRAPH,
replacing PHP reflection; matching is across registered subtype/interface relations, and
method-name matching is case-sensitive (the Go-native choice). Strict versus lenient policy mode is
supported, with NO grandfathered tags/functions -- allowlisting is uniform.

**Twig divergence.** Twig matches via PHP `instanceof`/`class_parents` reflection and grandfathers
`extends`/`use`/`parent`/`block`/`attribute`; Quill matches against an explicit type graph and drops
the grandfathering for uniform allowlisting.

The activation gate is global, runtime-toggled, or per-include via `sandboxed: true`. Compile-time
collection of used callables (a `..` counts as the `range` function) feeds a single per-render check
that maps violations to source lines. Runtime method and property access enforcement happens at the
access site, with property-vs-method precedence documented (a property error wins). String-coercion
is gated via the `Stringify` hook. Collection ops and arrow callables are gated per element (arrows
must be template-defined). `Safe` values and template instances bypass member-access checks.
Distinct, host-catchable security error types per violation class carry the offending name and type
name. Enabling the sandbox for a nested include and restoring it afterward never disables the sandbox
for an already-sandboxed enclosing render.

The type-graph that powers sandbox method/property matching is the SAME graph the gradual type
checker uses for `Object<"Type">` -- one host type registration serves both security and typing.
