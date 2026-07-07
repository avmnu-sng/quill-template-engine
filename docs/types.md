# Types

Quill's gradual type system is its lead differentiator: a static type checker that
runs between parse and interpret, catches a class of error before any byte is
rendered, and stays entirely optional. This page covers the type system, the
value model, and the runtime rules -- truthiness, equality, ordering, coercion,
attribute access, and undefined handling -- that the checker promotes to
load-time errors where types are present.

The governing invariant: **an annotation moves an error earlier in time; it never
moves the runtime.** Removing every annotation from a Quill template yields a
program that renders identical bytes.

## The gradual type system

### Shape

A template carries as many or as few annotations as its author chooses. With zero
annotations a template is a dynamic language backed by the strict-by-default
runtime. With annotations, the same template gains a static checker that rejects,
before any byte is emitted, a class of error the runtime would otherwise raise
mid-render.

An unannotated value has static type `any` and is checked only dynamically; an
annotated value is checked statically where its type is known and dynamically at
the `any`-boundary. This is classic gradual typing with a shallow boundary cast:
the `any`-to-typed cast is an O(1) top-kind check, fully sound for a scalar target
but proving only the container kind for a structured target (`list`, `map`,
`Object`). Accordingly, a scalar that crossed the boundary is trusted and its
downstream checks are erased; a structured value retains its strict-undefined
runtime check on member and element access, because the shallow cast did not
verify its members. The checker erases only what the cast proved, and the
strict-by-default runtime is the floor it never removes across an unverified
boundary.

### Why gradual typing fits templates

Two facts about real template corpora set the priorities. First, a mistake in a
template is expensive to find late: a silently-absent value or a
silently-coerced one surfaces far from its cause. The strict-by-default runtime
turns those silent faults into loud runtime errors; the checker's job is to turn
the subset a static reader can see into errors that fire at load time, before any
output. Second, a real corpus is large and mostly untyped. A type system
demanding annotations everywhere would be unadoptable; gradual typing lets a large
corpus stay untyped while a single hot template opts into checking, incrementally,
with no flag day.

### The type lattice

```
any                          // the dynamic top; unannotated values are any
null bool int float string   // scalar base types
list<T>                      // ordered sequence of T
map<K, V>                    // ordered mapping (K is int|string)
T?                           // nullable: sugar for T | null
(A, B) => R                  // arrow/callable types
Object<"Type">               // a host-registered named type
A | B                        // union (for polymorphic host returns, e.g. int | string)
```

`any` is gradual: a value of type `any` may flow into any typed slot and vice
versa, with a runtime check inserted only where the dynamic value crosses into a
typed context.

### Annotation sites

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

The `types { ... }` block is the file-scope form of the per-variable annotations,
promoted from an opaque tooling channel to a first-class checker input. Macro
params and returns, block returns and inputs, `set` targets, `for` targets, and
arrow params all take optional annotations. The annotation grammar is in the
[Grammar](reference/grammar.md).

### What the checker catches

Where types are present, the checker statically rejects (each promoting a runtime
error to check time):

- An undefined read of a declared variable, or a missing field on a typed
  `Object` / `map`.
- A cross-kind comparison or arithmetic on known-incompatible types (`"3" + 4` is
  a check-time error if `"3"` is typed `string`).
- A `for` over a value with a non-iterable static type.
- Rendering a `list`/`map`-typed value as text.
- A filter/function/macro called with the wrong arity or argument types, given
  registered host signatures.

Where types are absent (everything is `any`), the checker does nothing and the
strict-by-default runtime is the only line of defense -- so untyped Quill behaves
exactly like the dynamic floor. The two layers agree: the static checker promotes
a runtime error to check time; it never permits something the runtime would
reject.

## The value domain and truthiness

A Quill value is exactly one of eight kinds:

| Kind | Go carrier | Meaning |
|------|------------|---------|
| `Null` | (none) | the absence value; renders to the empty string |
| `Bool` | `bool` | `true` or `false` |
| `Int` | `int64` | a signed 64-bit integer |
| `Float` | `float64` | an IEEE-754 double |
| `Str` | `string` | a byte string, may be invalid UTF-8 (for lossless byte output) |
| `*Array` | `*Array` | the ordered, dual-view collection |
| `Object` | host interface | field read, method call, stringify, count, iterate, class name |
| `Safe` | wrapper | already-safe-output carrier |

The numeric model is int64 and float64: `/` is true division (float unless
exact), `//` is floor division, `%` is remainder, `**` is right-associative power.
Integer overflow, division/modulo by zero, and any operation that would produce a
non-finite float (`NaN`, `Inf`) are all **defined arithmetic errors** naming the
operation and operands -- never a silent promotion or a `NaN`/`Inf` token. Because
non-finite floats are unrepresentable in a live value, `==` stays reflexive and
ordering stays total over the entire `Float` kind with no special `NaN` case.

**Truthiness -- one rule.** The falsy set is exactly `{ Null, false, 0, 0.0, "",
empty *Array }`. Everything else is truthy -- crucially `"0"` is truthy (a
non-empty string), and any `Object` is truthy. This one rule is used by `if`, the
postfix `if`, the ternary, `?:`, and the boolean operators.

## Equality and ordering

`==` / `!=` is typed equality. Same-kind values compare by value; `Int`==`Float`
bridges numerically; `Safe` normalizes to its wrapped `Str` before comparison;
every other cross-kind pair is `false` (`1 == "1"` false, `null == ""` false,
`true == 1` false). It is total, symmetric, and reflexive with no coercion.
`*Array` equality is structural (same length, same keys in the same order,
recursively `==`). `Object` equality is identity unless the host defines an equal
hook. `===` is an alias of `==`; `same(a, b)` is raw reference identity.

`<` `>` `<=` `>=` `<=>`, plus membership `in`/`not in`, `sort`, `min`, and `max`,
are backed by one comparator. It is total within the number tower and between two
strings (byte-lexicographic), and defined nowhere across unlike kinds. Cross-kind
ordering is a check-time error where types are known, else a runtime error --
never silent juggling. Membership `in` uses typed `==`, so `"1" in [1]` is false.

## Coercion and the output path

Coercion is explicit and narrow. There is exactly one implicit coercion:
rendering a value to text at an interpolation site or in `~` concat. Arithmetic
and comparison never coerce; `"3" + 4` is a type error, not `7`. The `ToText`
rules:

| Value | Renders to |
|-------|-----------|
| `Null` | `""` |
| `Bool` | `"true"` / `"false"` |
| `Int` | decimal, no separators |
| `Float` | shortest round-trippable decimal (`1.0` -> `"1"`, `1.5` -> `"1.5"`) |
| `*Array` | render error (not the literal `"Array"`); use `join` / `json` |
| `Object` | its `Stringify` hook, else a render error |
| `Safe` | the wrapped content |

`Bool` renders the literal tokens `true`/`false`, so `false` never becomes an
invisible empty string. `Float` uses Go's shortest round-trippable form. An
`*Array` render is an explicit error rather than a placeholder word, so a stray
collection can never inject a literal `Array` into the output. `~` concatenation
renders each operand by these rules; `+` is numeric only.

## Attribute and index access

Two distinct operators, each kind-dispatched:

- **`a.b`** -- dotted access. If `a` is an `*Array`, read string key `"b"`; if `a`
  is an `Object`, read public field `b`, then accessor `getB`/`isB`/`hasB`
  (precedence `get > is > has`), then a class constant `b`. `a.b()` invokes a
  method.
- **`a[k]`** -- subscript. `*Array` by key, or an `Object` index interface. Only
  int and string keys; bool/float/null subscripts are type errors. Slices
  `a[start:end]`, `a[:end]`, `a[start:]`, and negative indices route to the slice
  operation.
- **`a?.b`, `a?[k]`** -- null-safe. A null receiver short-circuits the whole chain
  to `null`, regardless of strictness.
- **Dynamic access** `attribute(a, name, args?)` for a runtime-computed member
  name.

The kind of the receiver selects one lookup family, with no cross-kind cascade to
reason about.

## Scoping and undefined handling

Scoping is lexical and block-structured: `block`, `for`, `macro`, `with`, and
`capture` each introduce a scope. Undefined handling is strict-by-default and
gradual:

- Reading an undefined context variable, an absent `*Array` key via `a.b`/`a[k]`,
  or an absent object member is a **runtime error** naming the symbol and listing
  the available keys -- not a silent `Null`. A silently absent value is the
  costliest failure to debug, so the default is loud. Lenient mode
  (`WithStrictVariables(false)`) restores the silent `Null`.
- Absence is made explicit with three tools: the `is defined` test (true/false,
  never throws), the `?.`/`?[]` null-safe operators (short-circuit to `Null`), and
  the `default(x, fallback)` filter / `a ?? b` coalescing operator (yield the
  fallback when the left is undefined or null).

### Suppression depth: the whole-chain rule

`??`, `default`, and `is defined` set the "absence allowed" flag for the complete
evaluation of their left operand -- every hop in an access chain, including the
receiver -- so an absent variable or an absent intermediate member at any depth
yields the fallback rather than an error:

| Expression | `a` absent | `a` present, `.b` absent | `a.b` present, `.c` absent |
|------------|-----------|--------------------------|----------------------------|
| `a ?? fb` | `fb` | -- | -- |
| `a.b ?? fb` | `fb` | `fb` | -- |
| `a.b.c ?? fb` | `fb` | `fb` | `fb` |
| <code>a.b &#124; default(fb)</code> | `fb` | `fb` | -- |
| <code>a.b.c &#124; default(fb)</code> | `fb` | `fb` | `fb` |
| `a.b is defined` | `false` | `false` | -- |
| `a.b.c is defined` | `false` | `false` | `false` |

So `user.nick ?? "anon"` yields `"anon"` when `user` is absent, when `user` is
present but `.nick` is absent, or when `user.nick` is present-and-null -- the
common "this whole path may be absent" case works with the bare `??`, no `?.`
required.

`?.`/`?[]` cover the whole chain too, differing in what they catch: they
short-circuit on a *null* receiver mid-chain, whereas `??`/`default`/`is defined`
catch an *absent* variable or member. They compose:
`u?.address.city ?? "n/a"`. `is defined` tests presence, not value, so it is true
for a present key even if its value is `Null`.

Where annotations are present, the gradual type checker promotes many of these
misses to check-time errors so they never reach run time.

## The `*Array` and the key model

Quill keeps one ordered, dual-view collection: an ordered key slice plus a value
map. Iteration is insertion order; `for k, v in mapping` iterates in insertion
order with both targets. `is sequence` / `is mapping` split on list-shape (an
empty `*Array` is a sequence). The key model: a canonical decimal-integer key is
an integer key; everything else is a string key (`"01"`, `"1.0"`, `" 1"`, `"+1"`
stay strings). `*Array` values are value types, copy-on-write at loop, include,
and rebind boundaries -- a value semantic matching the slice/map mental model.

## Next

- [Escaping & Safety](safety.md) -- the escape strategies, the safeness
  machinery, and the sandbox.
- [Standard Library](stdlib.md) -- the filters, functions, and tests that operate
  under these rules.
- [Types & Semantics reference](reference/language.md) points back into the full
  language manual.
