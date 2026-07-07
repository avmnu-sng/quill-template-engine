# Expressions

Quill expressions are Go-flavored: infix arithmetic, pipe filters, arrow
functions, and null-safe access. The grammar is Pratt-parsed. An expression
appears anywhere in code -- inside `{{ ... }}`, inside a statement head, inside
`#{ ... }`, and nested in another expression -- and always evaluates to a single
dynamic value ([Types](../types.md)). The runtime rules for equality, ordering,
coercion, and arithmetic are in [Types](../types.md); this page names the
operators and their surface.

## Pipe filters

`|` pipes the left value into a filter as its first argument: `x | upper` is
`upper(x)`; `x | f(a, b)` is `f(x, a, b)`. Filters chain left to right:
`x | trim | upper`. The pipe is an ordinary expression operator, so a filtered
value may appear anywhere an expression may, not only at output sites.

The pipeline is the spine of the collection algebra, composing with arrow
functions and the spaceship comparator into a Unix-style pipeline:

```
{{ users
   | filter((u) => u.active)
   | sort((a, b) => a.rank <=> b.rank)
   | map((u) => u.name | upper)
   | join(", ") }}
```

Because `|` is the filter operator, bitwise OR is the word `b_or` (alias `|||`),
which keeps the pipe unambiguous.

## The precedence ladder

Quill publishes its own precedence numbers; only the relative ordering is
binding. Higher binds tighter (evaluated first).

| Level | Operators | Assoc |
|-------|-----------|-------|
| 17 | postfix: `.` `?.` `[ ]` `?[ ]` `( )` call <code>&#124;</code> filter | left |
| 16 | prefix: `not` (`!`) `-` `+` `...` spread | right |
| 15 | `**` power | right |
| 14 | `*` `/` `//` `%` | left |
| 13 | `+` `-` | left |
| 12 | `~` concat | left |
| 11 | `..` range | left |
| 10 | comparison/membership/test: `==` `!=` `<` `>` `<=` `>=` `<=>` `in` `not in` `matches` `starts with` `ends with` `has some` `has every` `is` `is not` | non-assoc |
| 9 | `b_and` (`&`) | left |
| 8 | `b_xor` (`^`) | left |
| 7 | `b_or` (<code>&#124;&#124;&#124;</code>) | left |
| 6 | `and` (`&&`) | left |
| 5 | `xor` | left |
| 4 | `or` (<code>&#124;&#124;</code>) | left |
| 3 | `??` coalesce, `?:` elvis | right |
| 2 | `? :` ternary, postfix `if`/`unless`/`else` | right |
| 1 | `=>` arrow, `=` assignment / destructuring | right |

**Power and unary minus.** Power (level 15) binds tighter than unary minus (level
16 prefix), but the right operand of the right-associative `**` re-enters at the
prefix level, and unary minus wraps the power by AST shape. One rule
(AST-driven precedence) governs both, so `-1 ** 0` parses as `-(1 ** 0) = -1` and
`(-1) ** 2 = 1`, entirely from the table.

## The operator catalogue

- **Logical** `and` (`&&`), `or` (`||`), `xor`, `not` (`!`): short-circuit
  `and`/`or`, boolean `xor`, prefix `not`, all over the single truthiness rule.
  Word and symbol spellings are interchangeable; the result is a `Bool`.
- **Bitwise** `b_and`, `b_or`, `b_xor`: integer-only; a non-int operand is a type
  error.
- **Equality** `==`, `!=`: typed equality. `1 == "1"` is false. `===`/`!==` are
  accepted aliases; raw reference identity is the `same(a, b)` builtin.
- **Ordering** `<` `>` `<=` `>=` `<=>`: total within the number tower and between
  two strings (byte-lexicographic). Cross-kind ordering is an error -- never
  silent juggling. One comparator backs them all.
- **Membership** `in` / `not in`: for a collection, true iff some element is `==`
  the needle (typed, so `"1" in [1]` is false); for a string haystack, substring
  containment of the rendered needle.
- **Regex** `matches`: Go RE2 dialect. The right operand is a string expression
  whose contents are the RE2 pattern; there is no `/re/` literal token (a bare
  `/` is always division). Inline flags use RE2's `(?flags)` prefix. A backtick
  raw string is the ergonomic form for patterns with backslashes.
- **String predicates** `starts with` / `ends with`.
- **Quantifiers** `has some` / `has every`: a predicate over an iterable using
  arrows, e.g. `xs has some (x => x > 0)`, `xs has every (x => x.valid)`.
- **Range** `..`: `1..5`, `'a'..'e'`; the same engine as `range(...)`.
- **Arithmetic** `+ - * / // % **`: numeric only. `"3" + 4` is a type error, not
  `7`. `/` is true division (float unless exact), `//` is floor division, `%` is
  remainder, `**` is right-associative power. `+` is never array union (that is
  the `merge` filter). Integer overflow is a defined error, not silent promotion
  to float.
- **Concat** `~`: string concatenation, each operand rendered by `ToText`. Kept
  distinct from `+`.
- **Ternary / elvis / coalesce**: three distinct fallthrough predicates.
  `a ? b : c` (truthiness of `a`; no-else `a ? b` yields empty when `a` is
  falsy); `a ?: b` (`a` if truthy else `b`, through a defined-safe path);
  `a ?? b` (`a` if defined and not null else `b`, predicate is definedness). The
  coalescing operators and `default` suppress undefined-misses across the entire
  left-operand access chain, not just its final hop, so `user.nick ?? "anon"`
  yields `"anon"` when `user` itself is absent. The per-hop table is in
  [Types](../types.md).
- **Assignment / destructuring** `=`: expression-form assignment returning the
  assigned value, right-associative. Sequence destructuring `[a, b] = e`, mapping
  destructuring `{name} = e`, rename `{key: alias} = e`, elided slots
  `[, b] = e`. Over/under-supply is an error by default; explicit
  `[a, b, ...rest] = e` captures the tail and `[a, b?] = e` marks a slot
  optional.
- **Arrow** `=>`: `x => expr`, `(x, y) => expr`, `() => expr`. Closes over
  template scope; params shadow context for the call duration. Used by
  `map`/`filter`/`sort`/`reduce`/`find` and the quantifier operators. Params may
  carry type annotations: `(x: int) => x * 2`.
- **Grouping** `( )`: overrides precedence and is the entry point for arrow param
  lists and parenthesized destructuring targets.

## Primary expressions and call arguments

A primary is a bare identifier (context lookup), a literal, `( expr )`, a
sequence or mapping literal, a function call `name(args)`, or a postfix chain
(access, subscript, slice, call, filter, test). Attribute and index access
(`a.b`, `a[k]`, `a?.b`, `a?[k]`, slices) are specified in [Types](../types.md).

Literals cover numbers (`42`, `1_000_000`, `3.14`, `0xFF`, `0b1010`, `0o755`,
`1e9`), bool/null (`true`, `false`, `null`, with `none` an alias for `null`),
sequence literals (`[1, 2, 3]`, spread `[...xs, 4]`), and mapping literals
(`{a: 1, b: 2}`, shorthand `{a}`, computed keys `{(expr): v}`, spread
`{...base, c: 3}`).

**One argument grammar for every callable.** Filter calls, function calls, and
test calls share a single argument grammar: positional `e`, named `name: e`, and
spread `...e`, in that order, with declared defaults filling any parameter not
supplied. The short forms are special cases:

- `name(args)` -- a function call.
- `x | f` and `x | f(args)` -- a filter call. `x | f` is the zero-explicit-
  argument case (the pipe supplies the first argument, defaults fill the rest);
  `x | f(a, key: c, ...rest)` is `f(x, a, key: c, ...rest)`.
- `x is t`, `x is t arg`, and `x is t(args)` -- a test call. `x is t` is the
  zero-argument case; `x is t arg` is the one-positional short form; `x is t(args)`
  admits named and spread arguments.

Because all three resolve to the same argument grammar and the same callable
signature, named arguments are available uniformly across filters, functions,
methods, and tests.

## Next

- [Control Flow](control-flow.md) -- `if`, `for`, `set`, and the region
  statements.
- [Standard Library](../stdlib.md) -- the built-in filters, functions, and
  tests.
- [Types](../types.md) -- the value model and the rules these operators obey.
