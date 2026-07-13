# Language Reference

This is the core reference manual for Quill. It covers the lexical structure and
the rule that makes a brace-delimited language safe for emitting brace-dense text;
interpolation and output; the expression language, operators, and precedence;
control flow and scoping; and template composition (inheritance, blocks, macros,
includes). The formal grammar is in the [Grammar](grammar.md); the standard
library is in [Standard Library](../stdlib.md); the runtime value rules, the
gradual type system, escaping, and the sandbox are in [Types](../types.md) and
[Escaping & Safety](../safety.md).

This page is the exhaustive specification. For a gentler introduction, start with
the [Guide](../guide/templates.md).

## 1. Lexical structure and the brace-safety rule

### 1.1 Two modes, one boundary rule

A Quill source is a sequence of TEXT (emitted verbatim, byte for byte) and CODE
(parsed). The lexer is a byte-oriented two-mode state machine. A template starts in
TEXT. Under the default statement-lead mode there are exactly three doors out of
TEXT into CODE, plus one bulk passthrough region:

| Opener | Closer | Meaning | Mode |
|--------|--------|---------|------|
| `{{` | `}}` | interpolation: render one expression | CODE (expression) |
| `{#` | `#}` | comment: consumed, emits nothing | CODE (comment) |
| an `@`-led statement keyword | matching `@}` or end of line | a statement | CODE (statement) |
| `@verbatim { ... }` / fenced `@verbatim` | balancing `@}` / fence | literal region, never scanned | TEXT-literal |

Everything else in TEXT (every bare `{`, `}`, `<`, `>`, `&`, `;`, `(`, `)`) is
ordinary output text. The load-bearing invariant:

> A single `{` or `}` in template text is NEVER a delimiter, in either
> statement-lead mode. A `{` is a delimiter only when immediately followed by `{`
> or `#`, with no intervening character. Under the default, a statement begins ONLY
> at `@`, and a block closes ONLY at `@}`, so a bare `}` in text (even a lone `}`
> at column 0) is always literal output.

A lone `{` followed by a space, a letter, a newline, a digit, or a quote is emitted
as text and the scanner stays in TEXT. A block of code such as
`{ this.head = node; }` passes through TEXT untouched because none of `{`, ` `,
`t`, `;`, `}` opens CODE. The byte-level lexer is byte-oriented so it can copy TEXT
faithfully including bytes that are not valid UTF-8; rune decoding happens only
inside CODE.

The default `@`-sigil statement lead exists because a common workload is
brace-dense text: literal lone-`}` lines are pervasive, many at column 0 (top-level
closes of emitted classes, methods, functions, or nested config blocks). The
`@`-default makes every one of those sites correct with zero escaping, at the cost
of one `@` per statement. The keyword-led BARE mode (statements recognized by a
line-leading keyword, a lone `}` line closing the innermost block) is an explicit
opt-in via a front-matter `pragma bare` (equivalently `pragma sigil off`), for
markup and other templates where brace collisions are rare. Section 1.3 specifies
both modes; unless noted, the rest of this manual writes statements in the
`@`-default spelling.

### 1.2 The literal-`{{` collision

The only sequence that flips TEXT into CODE by sigil is a literal `{{` adjacent
pair. Most text never contains `{{`: no mainstream programming language uses `{{`
as a token. The single real adversary is a Java `new HashMap<>() {{ ... }}`
double-brace block, handled three ways, in order of preference:

1. **Wrap brace-dense bulk output in a `verbatim` region** (Section 1.6). The body
   is copied byte for byte and never scanned. This is the recommended tool
   whenever a template emits a large literal block.
2. **Interpolate a string literal for a one-off:** `{{ "{{" }}` emits two literal
   open braces. A trailing `}}` in TEXT is literal, because the close sigil `}}` is
   recognized only when it closes an OPEN `{{`.
3. **Spot-escape with a backslash:** in TEXT, `\{` emits a literal `{` and
   suppresses sigil detection, so `\{{` emits `{{`. `\}` is symmetric. ONLY `\{`,
   `\}`, and `\\` are escapes; a lone backslash in TEXT is literal, so backslash
   sequences in emitted text are untouched unless they precede a brace.

The asymmetry is deliberate: emitting a literal `{` costs nothing in the
dense-single-brace case, and costs one explicit construct only in the rare
`{{`-adjacency case. A language that disambiguated `{{` by context would need
unbounded lookahead and would still surprise the author; Quill refuses that trade.

### 1.3 Statements are sigil-led brace blocks, and the two modes

Quill has two statement-lead modes. The DEFAULT is the `@`-sigil mode, chosen
because a common workload is brace-dense text; the BARE keyword-led mode is an
explicit opt-in.

**Default mode: `@`-led statements, `@}` close.** A statement (`@for`, `@if`,
`@block`, `@macro`, `@set`, `@include`, ...) is recognized only when, after
optional leading horizontal whitespace, a line's FIRST non-whitespace character is
`@` immediately followed by one of the fixed, closed set of statement keywords
(Section 5.1) at a word boundary, and the construct parses as a complete statement
head. A brace block opened by an `@`-led statement closes ONLY at `@}`: a line
whose only non-whitespace content (after optional leading whitespace) is `@}`,
optionally carrying a `-`/`~`/`+` trim modifier. Consequently:

- A bare `{` or `}` ANYWHERE in TEXT (including a lone `}` at column 0) is
  unconditionally literal output. No literal lone-`}` site needs any escaping.
- A line that begins with the WORD `for`, `if`, `while`, `block` (no `@`) is
  ordinary TEXT; there is no grammar-shape rejection to perform and no
  `line-leading-keyword` diagnostic to emit, because a statement is announced only
  by `@`.

```
@extends "base.quill"

@block body {
  @for u in users {
    {{ u.name }}
  @}
@}
```

Here the only escaping consideration is the rare literal `{{` adjacency (Section
1.2); everything else in TEXT, every `{` and `}`, is literal by construction.

**Opt-in bare mode: `pragma bare`.** A front-matter `pragma bare` (equivalently
`pragma sigil off`) selects the keyword-led mode: a statement is recognized when a
line's FIRST non-whitespace token is one of the statement keywords at a word
boundary and the construct parses as a complete statement head, and a block closes
at a lone `}` line. This suits markup and other templates where literal braces are
rare. In bare mode an output line might legitimately begin with the word `for`
(C's `for (...)`) or `if`, and a literal lone `}` might collide with a block close;
Quill resolves both deterministically (never by heuristic) with grammar-shape
rejection plus the escape tools below:

- **Grammar-shape rejection.** Quill's `for` requires `for <ident> in <expr> {`. An
  output C line `for (int i = 0; i < n; i++) {` is NOT a Quill `for`: the parser
  sees `(` where it requires an identifier, the line does not form a complete
  statement head, and it is TEXT. This handles the common C/Java/Go `for (...)`,
  `if (...)`, `while (...)` shapes for free.

- **The leading-pipe text marker** (per-line total escape). A line whose first
  non-whitespace character is a literal pipe `|` followed by a space is emitted as
  text verbatim, with the `| ` prefix stripped, and is NEVER classified as a
  statement. Interpolation within the line still works:

  ```
  | for item in list
  {{ body }}
  | end
  ```

- **The verbatim region** (bulk escape). For any block of output that begins lines
  with Quill keywords, `verbatim { ... }` (Section 1.6) copies the body literally
  with no scanning.

In bare mode the lexer emits a suppressible `line-leading-keyword` diagnostic at
every line that both begins with a Quill keyword AND forms a Quill-shaped head, so
the collision is never silent. The core lexical invariant (a single `{` or `}` is
never a delimiter) is unchanged in either mode.

#### The block-close rule and the line-leading-`}` collision (bare mode only)

This collision exists ONLY in opt-in bare mode; under the `@`-default a lone `}` is
always literal TEXT and there is nothing to resolve. A brace-bodied statement has a
TEXT body in which literal `{` and `}` are ordinary bytes and are NOT brace-counted
(bracket balancing applies only inside CODE, Section 1.7). In bare mode the closer
of such a body is recognized by exactly this rule:

> A line whose ONLY non-whitespace content is a single `}` (optionally carrying a
> `-`/`~` trim modifier) closes the innermost open Quill block. This is the ONLY
> close form; a `}` with any other non-whitespace on its line is literal TEXT.

So text that closes its own braces ON THE SAME LINE as other content (an indented
close, an interpolation-led close `{{ 1 | tab }}}`, or `} else {` in emitted C)
is never a Quill close, because the line is not a lone `}`. The residual collision
is a TEXT body that emits a brace-balanced block whose own close lands as a bare
`}` at COLUMN 0. There the literal close and the intended Quill close are
byte-identical lone-`}` lines, and the rule above would bind the FIRST such line to
the open block, truncating the body. The `@`-default exists precisely so this case
never arises; under bare mode the author disambiguates explicitly:

1. **The bare line-leading `}` always closes the innermost open Quill block.** The
   parser maintains the open-block stack; a lone `}` line pops one level.
2. **An author emitting a literal lone `}` at column 0 inside a Quill block MUST
   disambiguate it**, by one of: the leading-pipe text marker (`| }`), wrapping the
   brace-dense region in `verbatim`, or emitting the brace through interpolation
   (`{{ "}" }}`). Switching to the `@`-default removes the requirement entirely.
3. **Indentation does not by itself escape a literal `}`**: a line that is
   whitespace then `}` is still a lone-`}` close. Only the leading-pipe marker,
   `verbatim`, or interpolation make a column-aligned or column-0 literal `}`
   non-closing.
4. **A hard error, not the suppressible diagnostic, fires on a structural
   mismatch.** If the open-block stack is non-empty at end of file OR a lone `}`
   appears with an empty stack, the parser raises an unrecoverable
   `unbalanced-block` error naming the open construct and its source line.

Under the `@`-default these escape tools are unnecessary for the lone-`}` case and
remain available only for genuine edge cases.

### 1.4 Whitespace control

Whitespace control is byte-exact. It is documented in full in
[Whitespace Control](../whitespace.md); this section states the rule the rest of
the manual relies on. Two trim modifiers attach to either side of any sigil or
statement brace:

- **`-` (hard trim):** strips ALL adjacent whitespace including newlines. `{{- expr
  -}}`; on a statement, `@for ... {-` ... `-@}`.
- **`~` (line trim):** strips adjacent spaces, tabs, NUL, and vertical tab but NOT
  newlines. `{{~ expr ~}}`.
- **`+` (keep, closing side only):** suppresses the default close-newline-eating of
  a statement's closing `@}` (lone `}` under bare mode) or a comment's `#}`. `@}+`
  closes a block WITHOUT consuming the following newline.

The **statement/interpolation newline asymmetry** is a fixed rule: a statement's
closing `@}` and a comment's `#}` each consume exactly ONE immediately-following
newline; an interpolation's `}}` consumes none. This is what lets

```
@for u in users {
{{ u.name }}
@}
```

emit one line per user with no spurious blank lines. Where the eaten newline would
fuse the last body line with the following line, `@}+` or a per-template
`pragma keep-close-newline` restores byte-exact layout. See
[Whitespace Control](../whitespace.md) for the full treatment and the mapping table
for people coming from Jinja, Twig, or Go `text/template`.

### 1.5 String literals, interpolation, and comments

- **String literals:** single-quoted `'...'` (no interpolation; escapes `\\` `\'`
  `\n` `\t` `\xHH`) and double-quoted `"..."` (full escape set `\n \t \r \\ \"
  \xHH \u{...}` plus embedded interpolation). Adjacent string literals do NOT
  implicitly concatenate; use `~`. A backtick raw string `` `...` `` performs no
  escape processing (useful for regex patterns and paths).
- **String interpolation:** inside a double-quoted string, `#{ expr }` embeds an
  expression, compiling to a `~` concatenation chain. `\#{` is a literal `#{`.
  Single-quoted and backtick strings never interpolate. Example:
  `"Hello #{name | upper}!"`.
- **Block comment:** `{# ... #}`, consumed entirely, emits nothing. An unterminated
  `{#` is a lex error at the opening position. The `#}` closer eats one following
  newline.
- **Line comment inside CODE:** `#` to end of line within an expression or
  statement head emits nothing. A `#` inside a string literal is literal.

### 1.6 The verbatim region

The primary escape hatch for brace-dense bulk output. Its body is copied byte for
byte and is NOT scanned for any Quill syntax (not `{{`, not `{#`, not statement
keywords):

```
@verbatim {
public static void main(String[] args) {
    Map<String,Integer> m = new HashMap<>() {{
        put("a", 1);
    }};
}
@}
```

The body ends at the `@}` that balances the `@verbatim {` opener (a lone `}` under
bare mode); inner `{ }` are tracked by a raw-brace DEPTH COUNTER that never
interprets `{{`. For a body that must contain an unbalanced brace or the literal
close sequence, a FENCED verbatim takes an author-chosen terminator:

```
@verbatim ~~~JAVA
... arbitrary bytes, possibly unbalanced braces ...
~~~JAVA
```

The region ends at the first line equal to the fence token.

### 1.7 Bracket balancing, literals, and word-operator disambiguation

- **Bracket balancing inside CODE.** Inside an interpolation or statement head, the
  lexer balances `()`, `[]`, and `{}` so an inner `}` does not prematurely close
  the interpolation. The close sigil `}}` is recognized only at brace-depth zero
  relative to its opener, so `{{ {a: 1, b: 2} | json }}` works.
- **Numbers:** `42`, `1_000_000` (digit-group `_` separators between digits),
  `3.14`, `1_0.0_5`, `0xFF`, `0b1010`, `0o755`, `1e9`. Int is int64, float is
  float64.
- **Bool / null:** `true`, `false`, `null` (canonical); `none` is an accepted alias
  for `null`. Case-sensitive; `True` and `NULL` are identifiers.
- **Sequence / mapping literals:** `[1, 2, 3]` with trailing comma and spread
  `[...xs, 4]`; `{a: 1, b: 2}` with shorthand `{a}` for `{a: a}`, computed keys
  `{(expr): v}`, trailing comma, and spread `{...base, c: 3}`. Because a mapping
  literal appears only in CODE, its braces never collide with TEXT braces.
- **Word-operator / identifier disambiguation.** Tokenization is maximal-munch:
  longer operators win (`==` before `=`, `//` before `/`, `?.` before `?`, `not in`
  before `in`, `<=>` before `<=`). A word-operator (`and`, `or`, `not`, `in`, `is`,
  `matches`, `xor`, `starts`, `ends`, `has`) is an operator ONLY in operator
  position; in primary position and immediately after `.` or `|` it is a plain
  identifier, so a host field named `in` is reachable as `record.in`.

- **Special primary names.** Three reserved primaries are always defined, exempt
  from the strict-undefined rule, and resolved by the engine rather than by context
  lookup:

  | Name | Value | Use |
  |------|-------|-----|
  | `_self` | the current `Template` | `import _self as me` then `me.helper(...)`; `from _self import helper` |
  | `_context` | the live context as an `*Array` | reflective access, host RNG seeding, `dump(_context)` |
  | `_charset` | the configured charset string (default `"UTF-8"`) | charset-sensitive author logic |

  A bare read of any of the three NEVER raises an undefined-variable error, even
  under strict mode; the engine supplies the value. They are reserved: a context
  variable named `_self`, `_context`, or `_charset` is shadowed. `_self` as an
  `import`/`from` source lets a template call its OWN macros and those it inherits
  via `extends` (Section 5.4).

### 1.8 Fixed delimiters and source positions

Quill FIXES its delimiters. The verbatim fence token and the `pragma bare`
statement-lead mode are the only per-template knobs, and neither changes the
interpolation, comment, or string-interpolation sigil bytes. Source is CR/CRLF-
normalized to LF before line counting; every token and AST node carries 1-based
`{Line, Col}`; diagnostics report `template:line:col`. A `@line N` statement
(Section 4.7) resets the reported line for embedded or generated fragments.

## 2. Interpolation and output

### 2.1 The print form

`{{ expr }}` evaluates `expr` and renders it via the `ToText` rules
([Types](../types.md)). `{{ u.name | upper }}` is a pipe expression rendered to
output.

### 2.2 Pipe filters

`|` pipes the left value into a filter as its first argument: `x | upper` is
`upper(x)`; `x | f(a, b)` is `f(x, a, b)`. Filters chain left to right: `x | trim
| upper`. The pipe is an ordinary expression operator (Section 3), so a filtered
value may appear anywhere an expression may. Because `|` is the filter operator,
bitwise-or is the word `b_or` (Section 3.1).

The pipeline composes with arrow functions and the spaceship comparator into a
Unix-style pipeline:

```
{{ users
   | filter((u) => u.active)
   | sort((a, b) => a.rank <=> b.rank)
   | map((u) => u.name | upper)
   | join(", ") }}
```

### 2.3 The postfix conditional on output

`{{ expr if cond }}` renders `expr` only when `cond` is truthy; otherwise it
renders nothing (the empty string). `{{ ", admin" if u.isAdmin }}` emits `, admin`
exactly when `u.isAdmin` is truthy. An optional `else` tail supplies a fallback:
`{{ u.title if u.hasTitle else "(untitled)" }}`. A symmetric `{{ expr unless cond }}`
is accepted. It is pure sugar: `{{ x if c }}` desugars to the ternary
`{{ c ? x : "" }}`, so it adds no new evaluation rule.

## 3. Expressions, operators, and precedence

Expressions are Go-flavored: infix arithmetic, pipe filters, arrow functions,
null-safe access. The grammar is Pratt-parsed. An expression appears only in CODE
positions and always evaluates to a single dynamic value ([Types](../types.md)).
The full operator catalogue and precedence ladder are in
[Expressions](../guide/expressions.md); this section summarizes them.

### 3.1 The published ladder

Quill publishes its own precedence numbers; only the relative ordering is binding.
Higher binds tighter.

| Level | Operators | Assoc |
|-------|-----------|-------|
| 17 | postfix: `.` `?.` `[ ]` `?[ ]` `( )` call <code>&#124;</code> filter | left |
| 16 | prefix: `not` (`!`) `-` `+` `...` spread | right |
| 15 | `**` power | right |
| 14 | `*` `/` `//` `%` | left |
| 13 | `+` `-` | left |
| 12 | `~` concat | left |
| 11 | `..` range | left |
| 10 | comparison/membership/test | non-assoc |
| 9 | `b_and` (`&`) | left |
| 8 | `b_xor` (`^`) | left |
| 7 | `b_or` (<code>&#124;&#124;&#124;</code>) | left |
| 6 | `and` (`&&`) | left |
| 5 | `xor` | left |
| 4 | `or` (<code>&#124;&#124;</code>) | left |
| 3 | `??` coalesce, `?:` elvis | right |
| 2 | `? :` ternary, postfix `if`/`unless`/`else` | right |
| 1 | `=>` arrow, `=` assignment / destructuring | right |

**Power and unary minus.** Power (15) binds tighter than unary minus (16 prefix),
but the right operand of the right-associative `**` re-enters at the prefix level
and unary minus wraps the power by AST shape, so `-1 ** 0 = -1` and `(-1) ** 2 = 1`
from the table. **Bitwise-or spelling.** Because `|` is the pipe, bitwise OR is
`b_or` (alias `|||`), bitwise XOR is `b_xor` (alias `^`), bitwise AND is `b_and`
(alias `&`).

### 3.2 The operator catalogue

The runtime rules for equality, ordering, coercion, and arithmetic are in
[Types](../types.md); the surface is in [Expressions](../guide/expressions.md).
Briefly: logical `and`/`or`/`xor`/`not` over one truthiness rule; integer-only
bitwise ops; typed `==`/`!=` (`1 == "1"` false); one ordering comparator; membership
`in`/`not in` via typed equality; the RE2 `matches` operator; `starts with`/`ends
with`; the `has some`/`has every` quantifiers; the `..` range; numeric-only
arithmetic (`"3" + 4` is a type error); `~` string concat; the three fallthrough
predicates `?`/`?:`/`??`; destructuring assignment `=`; the arrow `=>`; and
grouping `( )`.

### 3.3 Primary expressions and call arguments

A primary is a bare identifier (context lookup), a literal, `( expr )`, a sequence
or mapping literal, a function call `name(args)`, or the postfix chain (access,
subscript, slice, call, filter, test). Attribute and index access are specified in
[Types](../types.md).

**One argument grammar for every callable.** Filter calls, function calls, and test
calls all share the single `Args` production ([Grammar](grammar.md)): positional
`e`, named `name: e`, and spread `...e`, in that order, with declared defaults
filling any parameter not supplied. `name(args)` is a function call; `x | f` and
`x | f(args)` are filter calls (the pipe supplies the first argument); `x is t`,
`x is t arg`, and `x is t(args)` are test calls. Because all three resolve to the
same `Args` grammar and the same callable signature, named arguments are available
uniformly across filters, functions, methods, and tests.

## 4. Control flow and scoping

All block statements use brace bodies `{ ... }`, with no end-keywords. Under the
`@`-default a body closes at `@}`; under `pragma bare` it closes at a lone `}`. The
examples below use the `@`-default spelling. The narrative treatment is in
[Control Flow](../guide/control-flow.md).

### 4.1 Conditionals

```
@if u.isAdmin {
  granted
@} elseif u.isGuest {
  limited
@} else {
  denied
@}
```

One `if` head, zero-or-more `elseif`, optional `else`. Conditions are arbitrary
expressions taken in the single truthiness rule ([Types](../types.md)).

### 4.2 Loops

```
@for u in users {
  {{ u.name }}
@} else {
  no users
@}
```

- One or two targets: `for v in seq` and `for k, v in mapping` (mappings iterate in
  insertion order with both targets).
- The `else` branch runs exactly when the sequence yielded zero iterations, and
  only after the iterand resolved to a collection. It is reached for an
  iterable-but-empty value; it is NOT reached when the iterand is non-iterable.
- **Non-iterable is a runtime error**, NOT a silent empty loop: a silently
  skipped loop would omit an entire section of output with no signal. The explicit
  "empty is fine" idiom is `for x in (coll ?? []) { ... }`. Where a static type
  proves non-iterability, the error is promoted to check time.

The three cases:

| Iterand `E` | Behavior |
|-------------|----------|
| iterable, one or more elements | body runs per element; `else` NOT run |
| iterable, zero elements (`[]`, `{}`) | body skipped; `else` run |
| `Null` or any non-iterable, written bare (`for x in E`) | RUNTIME ERROR; `else` NOT reached |
| `Null`/absent, coalesced (`for x in (E ?? [])`) | the `??` yields `[]` -> `else` run |

- **Loop metadata.** Inside the body, `loop` exposes `loop.parent`, `loop.index0`,
  `loop.index`, `loop.first`, `loop.last`, `loop.length`, `loop.revindex`,
  `loop.revindex0`, `loop.prev`, `loop.next`, and the method `loop.changed(expr)`.
  ALL loop fields are ALWAYS defined: the `*Array` always knows its length, and a
  host iterator is drained to an `*Array` before the loop.
  - `loop.prev` / `loop.next` are the previous and next element; `loop.prev` is
    `Null` on the first iteration and `loop.next` is `Null` on the last.
  - `loop.changed(expr)` is `true` on the first iteration and whenever `expr`
    differs from its value on the prior iteration, by the single typed-equality
    rule. Each call site is tracked independently. It is the idiom for section
    headers over grouped rows: `@if loop.changed(row.group) { [{{ row.group }}] @}`.
- **Fused loop filtering (`@for ... if cond`).** An optional `if <expr>` clause
  between the iterand and the body brace pre-filters the iterand to the elements
  for which `cond` is truthy:

  ```
  @for u in users if u.active {
    {{ u.name }}{{ ", " if not loop.last }}
  @} else {
    no active users
  @}
  ```

  Every `loop.*` field reflects ONLY the survivors, so a trailing-separator idiom
  keyed on `loop.last` is correct over the filtered subset. The `else` branch runs
  when ZERO elements survive.
- **Recursive descent (`@for ... recursive`).** A `recursive` marker after the
  iterand turns the loop into a tree walk: the body may call `loop(children)` to
  render the same body over a subtree one level deeper, and the descent's rendered
  output is returned as a value the body prints. Two extra fields appear:
  `loop.depth` (1-based) and `loop.depth0` (0-based):

  ```
  @for node in tree recursive {
  @tab(loop.depth0) {
  - {{ node.name }}
  @}
  {{ loop(node.children) }}
  @}
  ```

  `loop(children)` iterates its argument as a fresh level. An argument that is not
  a traversable collection renders nothing. A `recursive` loop body reads outer
  variables but a body `set` of an outer name does not persist after the loop;
  accumulate with a slot (`@provide`) or a `cell`.
- **Scoping.** The plain loop body is a child scope. A variable that existed before
  the loop and is reassigned inside keeps its last in-loop value after the loop; a
  variable introduced only in the body does not leak. The rule is lexical scoping.

### 4.3 Assignment and capture

```
@set name = u.name
@set a, b = e1, e2
@set count: int = users | length          // optional type annotation
```

`@set` binds one or more targets; the same count on both sides or a clear error.
Assignment is an expression returning the assigned value: `{{ b = 1 + 3 }}` both
stores `b` and prints `4`; `@do b = 1 + 3` stores without printing.

A target may be a member place: `@set recv.name = expr` and `@set recv[key] = expr`
assign THROUGH a receiver. On a mapping this stores the key in place; on a reference
value (a `cell`, [Standard Library](../stdlib.md)) it calls the write hook, so
`@set acc.value = acc.value + w` mutates the cell in place. A receiver that does not
support assignment is a runtime error.

Block capture renders a body to a string-like value:

```
@set banner = capture {
  /* header for {{ target }} */
@}
```

Under the default (escaping off) the capture is a plain `Str`; under an
`escape`-on template it is a `Safe` value.

### 4.4 Effect, flush, deprecation, logging

- `@do expr`: evaluate for side effects, no output.
- `@flush`: a documented no-op for a string/byte sink, kept for parity.
- `@deprecated "message" [since "2.0"]` routes a deprecation diagnostic to the
  diagnostics sink, no output.
- `@log expr` evaluates `expr` and writes its text form to the host logger
  (`WithLogger(l)`, default a discarding logger). It produces NO rendered output
  but IS a coverable unit.

### 4.5 Scoped variable region and filter-apply

- `@with { x: 1, y: 2 } { ... }` introduces a scope merging the given vars;
  `@with { x: 1 } only { ... }` replaces the context entirely for the body.
- `@apply | trim | upper { ...body... }` captures the body and pipes it through the
  filter chain.

### 4.6 Feature guard and types

- `@guard filter("markdown") { ... } else { ... }` selects a branch on whether the
  named callable is registered; the dead branch is parsed but NOT validated against
  unknown callables. The value-level counterpart is the inline registry test
  `name is filter` / `name is function` / `name is test`.
- `@types { x: string, n: int }` declares context types, consumed by the gradual
  checker ([Types](../types.md)), not as an inert metadata side-channel.

### 4.7 Region statements

- `@escape html { ... }` / `@escape off { ... }` sets the active escaping strategy
  for a region. Default is `off`; see [Escaping & Safety](../safety.md).
- `@sandbox { ... }` forces sandboxing for templates included within the region.
- `@tab(n) { ... }` indents the entire rendered body by `n` levels, nesting
  cumulatively; blank lines stay blank. One level is `WithTabWidth` spaces (default
  4). See [Standard Library](../stdlib.md).
- `@verbatim { ... }` / fenced verbatim: literal body, not scanned (Section 1.6).
- `@line 42` resets the reported source line for diagnostics.
- `@cache key="header" ttl=3600 tags=["a"] { ... }` caches a rendered body under a
  key with optional ttl/tags.

## 5. Composition: inheritance, blocks, macros, includes

Composition is built on the internal template contract: render a body, resolve
a block by name, look up a macro, and walk the parent chain. (The public opaque
`quill.Template` handle exposes only the inspection surface `Name`/`BlockNames`/
`HasBlock`/`HasMacro`.) The shared data structure is the BLOCK TABLE, an ordered
map from block name to a
`BlockRef{Owner, ID}`; inheritance, embed, and trait reuse all reduce to building
and merging block tables and walking a parent chain. Macros are a separate,
isolated function namespace. The narrative treatment is in
[Composition](../guide/composition.md).

### 5.1 The closed keyword set

The statement keywords, fixed for the lexer's statement recognition (Section 1.3).
Under the `@`-default each is written with a leading `@` and a block closes with
`@}`; under `pragma bare` each is written bare and a block closes with a lone `}`:

```
extends  block  for  if  elseif  else  macro  set  include  import
from  use  embed  with  apply  do  flush  deprecated  guard  types
escape  sandbox  verbatim  line  cache  capture  log  tab
provide  yield  call
```

Under the default, a line begins a statement ONLY when its first non-whitespace
character is `@` immediately followed by one of these keywords; everything else at
line start is text. Under `pragma bare`, a line that begins with one of these
keywords (forming a Quill-shaped head) is a statement.

### 5.2 Inheritance

```
@extends "base.quill"        // single parent; content outside blocks in a child is rejected

@block body {                // define + render-in-place; long form
  ...
@}
@block title "Default Title" // shortcut value form
@block outer {               // nested, independently overridable
  @block inner { ... @}
@}
```

`@extends "<expr>"` takes a string-coerced expression, so a candidate list
`@extends ["a.quill", "b.quill"]` selects the first that exists. Inside an overriding
block, `parent()` renders the parent's version. `block("name")` and `block("name",
"other.quill")` render a named block of this or another template; `block("name") is
defined` tests existence.

### 5.3 Macros

```
@macro greet(name, greeting: string = "Hello", ...rest) {
  {{ greeting }} {{ name | default("guest") }}
@}
```

Declared params with constant defaults, optional type annotations, and a variadic
capture `...rest`. A macro sees ONLY its params, defaults, variadics, and host
globals; the caller's local context is invisible. A macro returns its captured
output (a `Str`, or `Safe` under escaping).

**Tail captures: `...rest` for positional args, `**opts` for named args.** A macro
takes two optional tail parameters in a fixed order: an optional positional
variadic `...rest` that collects excess POSITIONAL arguments into a `list`, then an
optional keyword variadic `**opts` that collects excess NAMED arguments into a
`map<string, any>`. `**opts` must be the last parameter:

```
@macro render_field(name, **opts) {
  <input name="{{ name }}"{{ opts | keys | join(" ") }}>
@}
{{ render_field("email", id: "e1", class: "big") }}   // opts == { id: "e1", class: "big" }
```

Without a `**opts` tail an unmatched named argument is a typo error; with one it is
absorbed. Because `**opts` is an ordinary mapping, it forwards to a nested call by
spread: `inner(...opts)`.

**The macro namespace is in scope inside every macro body.** A macro body sees the
names of all macros visible to the template (its OWN, sibling macros in the same
template, and macros brought in by `import`/`from`), so a macro may call itself or
a sibling directly by name. Recursion and mutual recursion are reachable by bare
name and by the `_self` import path (`import _self as me; me.tree(...)`).

**Call blocks (`@call`).** A `@call name(args) { body }` invokes macro `name` and
binds a `caller()` callable inside the macro body that renders the block:

```
@macro section(title) {
## {{ title }}
{{ caller() }}
@}
@call section("Overview") {
This body is rendered where the macro calls caller().
@}
```

`@call(p1, p2) name(args) { body }` declares caller parameters that `caller(v1, v2)`
binds positionally in the block scope. `caller()` is visible only in the macro the
`@call` directly invokes and is a runtime error outside any `@call`.

### 5.4 Imports and traits

```
@import "forms.quill" as forms                  // namespace; call forms.input(...)
@from "forms.quill" import input, label as lbl  // selective; call input(...), lbl(...)
@use "buttons.quill"                            // import all blocks of a traitable template
@use "buttons.quill" with { submit: ok }        // block aliasing/rename
```

Top-level import is global; in-block import is block-local. A trait has no parent,
no macros, and no free body; trait-then-own precedence means the importing
template's own block definitions win over imported ones.

### 5.5 Embed

```
@embed "card.quill" with { title: t } {
  @block body { {{ content }} @}
@}
```

Inline an anonymous child of the embedded template: include plus block override in
one construct. Supports `with`, `only`, and `ignore missing`.

### 5.6 Includes

Statement form:

```
@include "header.quill"
@include "row.quill" with { user: u }
@include "row.quill" with { user: u } only
@include "maybe.quill" ignore missing
@include ["a.quill", "b.quill"]      // first that exists
```

`with map` adds vars to the current context; `only` renders with just those vars;
`ignore missing` tolerates absence, rendering nothing; a sequence is a candidate
list, first existing wins.

Function form, returning rendered output as an expression value:

```
{{ include("snippet.quill", { x: 1 }, with_context: false, ignore_missing: true, sandboxed: true) }}
```

### 5.7 Accumulating content slots

`@provide` and `@yield` collect rendered content from many sites into a named
buffer and emit it once, the complement of `@block`: where a block OVERRIDES, a
slot ACCUMULATES.

```
imports:
@yield imports
...
@provide imports {
import "os"
@}
@provide imports {
import "fmt"
@}
```

- `@provide <label> { body }` appends the rendered body to the slot named `<label>`
  and emits nothing at its own position. Every contribution appends in execution
  order.
- `@yield <label>` emits the slot's accumulated content once. It is DEFERRED: a
  `@yield` placed before the `@provide` sites that feed it is backfilled with the
  complete accumulation after the render.
- `slot(label)` is the expression form: it returns the slot's content AS OF THE
  CALL as a value, capturing only what accumulated before the call.

Slots span the whole render, INCLUDING sub-renders: a `@provide` inside an
`@include`d or `@embed`ded partial appends to the enclosing render's slot buffer,
and a `@yield` in the shell is backfilled from those contributions.
