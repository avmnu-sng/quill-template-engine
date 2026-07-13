# Control Flow

All block statements use brace bodies `{ ... }` with no end-keywords. Under the
`@`-default a body closes at `@}`; under `pragma bare` it closes at a lone `}`
([Templates](templates.md)). The examples below use the `@`-default spelling.
Scoping is lexical and block-structured; the precise undefined-handling rules are
in [Types](../types.md).

## Conditionals

```
@if u.isAdmin {
  granted
@} elseif u.isGuest {
  limited
@} else {
  denied
@}
```

One `if` head, zero or more `elseif`, an optional `else`. Conditions are
arbitrary expressions taken in the single truthiness rule ([Types](../types.md)).

## Loops

```
@for u in users {
  {{ u.name }}
@} else {
  no users
@}
```

- One or two targets: `for v in seq` and `for k, v in mapping` (mappings iterate
  in insertion order with both targets).
- The `else` branch runs exactly when the sequence yielded zero iterations, and
  only after the iterand resolved to a collection. It is reached for an
  iterable-but-empty value; it is not reached when the iterand is non-iterable,
  because the error preempts the loop.
- **A non-iterable iterand is a runtime error**, not a silent empty loop. A
  silent skip would omit a whole section of output with no signal. The explicit
  "empty is fine" idiom is `for x in (coll ?? []) { ... }`. Where a static type
  proves non-iterability, the error is promoted to check time.

The three cases:

| Iterand `E` | Behavior |
|-------------|----------|
| iterable, one or more elements | body runs per element; `else` not run |
| iterable, zero elements (`[]`, `{}`) | body skipped; `else` run |
| `Null` or any non-iterable, written bare (`for x in E`) | runtime error; `else` not reached |
| `Null`/absent, coalesced (`for x in (E ?? [])`) | the `??` yields `[]` -> `else` run |

### Loop metadata

Inside the body, `loop` exposes `loop.parent`, `loop.index0`, `loop.index`,
`loop.first`, `loop.last`, `loop.length`, `loop.revindex`, `loop.revindex0`,
`loop.prev`, `loop.next`, and the method `loop.changed(expr)`. All loop fields
are always defined: the collection knows its length, and a host iterator is
drained before the loop, trading a small memory cost for an always-present
`loop.last`.

- `loop.prev` / `loop.next` are the previous and next element value; `loop.prev`
  is `Null` on the first iteration and `loop.next` is `Null` on the last.
- `loop.changed(expr)` is `true` on the first iteration and whenever `expr`
  differs from its value on the prior iteration; each call site is tracked
  independently. It is the idiom for section headers over grouped rows:
  `@if loop.changed(row.group) { [{{ row.group }}] @}`.

### Fused loop filtering

An optional `if <expr>` clause between the iterand and the body brace pre-filters
the iterand to the elements for which the condition is truthy, and the body runs
only over those survivors:

```
@for u in users if u.active {
  {{ u.name }}{{ ", " if not loop.last }}
@} else {
  no active users
@}
```

Every `loop.*` field reflects only the survivors, so a trailing-separator idiom
keyed on `loop.last` is correct over the filtered subset. The `else` branch runs
when zero elements survive.

### Recursive descent

A `recursive` marker after the iterand turns the loop into a tree walk: the body
may call `loop(children)` to render the same body over a subtree one level
deeper, and the descent's rendered output is returned as a value the body prints.
Two extra fields appear: `loop.depth` (1-based) and `loop.depth0` (0-based). It
is the idiom for nested structures such as a directory tree, an AST, or a menu:

```
@for node in tree recursive {
@tab(loop.depth0) {
- {{ node.name }}
@}
{{ loop(node.children) }}
@}
```

`loop(children)` iterates its argument as a fresh level. An argument that is not a
traversable collection renders nothing. A `recursive` loop body reads outer
variables but does not persist a body `set` of an outer name after the loop;
accumulate across the walk with a slot (`@provide`) or a `cell`.

### Scoping

The plain loop body is a child scope. A variable that existed before the loop and
is reassigned inside keeps its last in-loop value after the loop; a variable
introduced only in the body does not leak. The rule is lexical scoping.

## Assignment and capture

```
@set name = u.name
@set a, b = e1, e2
@set count: int = users | length          // optional type annotation
```

`@set` binds one or more targets; the same count on both sides or a clear error.
Assignment is an expression returning the assigned value: `{{ b = 1 + 3 }}` both
stores `b` and prints `4`; `@do b = 1 + 3` stores without printing.

A target may be a member place rather than a plain name: `@set recv.name = expr`
and `@set recv[key] = expr` assign through a receiver. On a mapping this stores
the key in place; on a reference value (a `cell`, [Standard Library](../stdlib.md))
it calls the write hook, so `@set acc.value = acc.value + w` mutates the cell in
place. A receiver that does not support assignment is a runtime error.

Block capture renders a body to a string-like value:

```
@set banner = capture {
  /* header for {{ target }} */
@}
```

Under the default (escaping off) the capture is a plain `Str`; under an
`escape`-on template it is a `Safe` value.

## Effect, logging, and flush

- `@do expr`: evaluate for side effects, no output.
- `@log expr`: evaluate and write the text form to the host logger
  (`WithLogger`, default discarding). No rendered output, but it is a coverable
  unit.
- `@flush`: a documented no-op for a string/byte sink, kept for parity.
- `@deprecated "message" [since "2.0"]`: routes a deprecation diagnostic to the
  diagnostics sink, no output.

## Scoped regions

- `@with { x: 1, y: 2 } { ... }` introduces a scope merging the given vars;
  `@with { x: 1 } only { ... }` replaces the context entirely for the body.
- `@apply | trim | upper { ...body... }` captures the body and pipes it through
  the filter chain.
- `@escape html { ... }` / `@escape off { ... }` sets the active escaping strategy
  for a region ([Escaping & Safety](../safety.md)).
- `@sandbox { ... }` forces sandboxing for templates included within the region.
- `@tab(n) { ... }` indents the entire rendered body by `n` levels, nesting
  cumulatively; blank lines stay blank ([Standard Library](../stdlib.md)).
- `@guard filter("markdown") { ... } else { ... }` selects a branch on whether a
  named callable is registered; the dead branch is parsed but not validated
  against unknown callables, so it is portable across host configs.
- `@types { x: string, n: int }` declares context types, consumed by the gradual
  checker ([Types](../types.md)).
- `@line 42` resets the reported source line for diagnostics in embedded or
  generated fragments.
- `@cache key="header" ttl=3600 tags=["a"] { ... }` caches a rendered body under
  a key with optional ttl/tags.

## Next

- [Composition](composition.md): inheritance, blocks, macros, includes, embeds,
  and slots.
- [Whitespace Control](../whitespace.md): the trim modifiers and the block
  cleanup that keep control statements from leaking blank lines.
