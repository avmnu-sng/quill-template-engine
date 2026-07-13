# Templates

A Quill template is a sequence of literal text (emitted verbatim, byte for byte)
and code (parsed and evaluated). This page covers the lexical surface: the two
statement modes, interpolation, string literals, comments, and the `verbatim`
region. Whitespace control has its own page, [Whitespace Control](../whitespace.md);
the expression language is in [Expressions](expressions.md).

## Two modes, one boundary rule

The lexer is a byte-oriented, two-mode machine. A template starts in text. Under
the default statement mode there are three doors out of text into code, plus one
bulk passthrough region:

| Opener | Closer | Meaning |
|--------|--------|---------|
| `{{` | `}}` | interpolation: render one expression |
| `{#` | `#}` | comment: consumed, emits nothing |
| an `@`-led statement keyword | matching `@}` or end of line | a statement |
| `@verbatim { ... }` / fenced `@verbatim` | balancing `@}` / fence | literal region, never scanned |

Everything else in text is ordinary output. The load-bearing invariant:

> A single `{` or `}` in template text is never a delimiter. A `{` is a delimiter
> only when immediately followed by `{` or `#`, with no intervening character.
> Under the default, a statement begins only at `@`, and a block closes only at
> `@}`, so a bare `}` in text (even a lone `}` at column 0) is always literal
> output.

The byte-level lexer copies text faithfully, including bytes that are not valid
UTF-8; rune decoding happens only inside code. This one rule is what makes the
split decidable with a single byte of lookahead.

## Statement modes

Quill has two statement-lead modes. The default is the `@`-sigil mode; a
keyword-led bare mode is an explicit opt-in.

### Default: `@`-led statements, `@}` close

A statement (`@for`, `@if`, `@block`, `@macro`, `@set`, `@include`, ...) is
recognized only when, after optional leading whitespace, a line's first
non-whitespace character is `@` immediately followed by one of the fixed
statement keywords at a word boundary, and the construct parses as a complete
statement head. A brace block opened by an `@`-led statement closes only at
`@}`: a line whose only non-whitespace content is `@}`, optionally carrying a
trim modifier.

```
@extends "base.quill"

@block body {
  @for u in users {
    {{ u.name }}
  @}
@}
```

Two consequences follow:

- A bare `{` or `}` anywhere in text (including a lone `}` at column 0) is
  unconditionally literal output. Text that emits dense braces (a program-source
  fragment, a JSON body, a nested config block) needs no escaping.
- A line that begins with the word `for`, `if`, or `block` (no `@`) is ordinary
  text; there is no line-leading-keyword ambiguity, because a statement is
  announced only by `@`.

This default keeps brace-dense text correct with zero author effort, at the cost
of one `@` per statement. It is the recommended mode for any template whose text
contains literal braces.

### Opt-in: `pragma bare`

A front-matter `pragma bare` (equivalently `pragma sigil off`) selects the
keyword-led mode: a statement is recognized when a line's first token is one of
the statement keywords at a word boundary and the line parses as a complete
statement head, and a block closes at a lone `}` line. It suits markup and other
templates where literal braces are rare:

```
pragma bare

extends "base.quill"

block body {
  for u in users {
    {{ u.name }}
  }
}
```

Both spellings denote the same template. In bare mode, where a literal line might
begin with a keyword or a literal lone `}` might collide with a block close,
Quill resolves the collision deterministically with grammar-shape rejection, a
leading-pipe text marker (`| `), and the `verbatim` region, never by heuristic.
The [Language Reference](../reference/language.md) specifies the bare-mode rules
in full. The rest of this guide uses the `@`-default spelling.

## Interpolation and output

`{{ expr }}` evaluates `expr` and renders it to output through the `ToText` rules
([Types](../types.md)). `{{ u.name | upper }}` interpolates through a filter;
`|` pipes the left value into a filter as its first argument
([Expressions](expressions.md)).

### The postfix conditional

`{{ expr if cond }}` renders `expr` only when `cond` is truthy; otherwise it
renders nothing (the empty string, with no surrounding whitespace). An optional
`else` tail supplies a fallback:

```
{{ ", admin" if u.isAdmin }}
{{ u.title if u.hasTitle else "(untitled)" }}
```

A symmetric `{{ expr unless cond }}` is accepted. The postfix conditional is
pure sugar: `{{ x if c }}` desugars to the ternary `{{ c ? x : "" }}`, so it
adds no new evaluation rule.

## String literals, interpolation, and comments

- **String literals.** Single-quoted `'...'` (no interpolation; escapes `\\`
  `\'` `\n` `\t` `\xHH`), double-quoted `"..."` (the full escape set plus
  embedded interpolation), and a backtick raw string `` `...` `` (no escape
  processing, useful for regex patterns and paths). Adjacent string literals do
  not implicitly concatenate; use the `~` concat operator.
- **String interpolation.** Inside a double-quoted string, `#{ expr }` embeds an
  expression, compiling to a `~` concatenation chain: `"Hello #{name | upper}!"`.
  `\#{` is a literal `#{`. Single-quoted and backtick strings never interpolate.
- **Block comment.** `{# ... #}` is consumed entirely and emits nothing. Its
  closer eats one following newline.
- **Line comment inside code.** `#` to end of line within an expression or
  statement head emits nothing; a `#` inside a string literal is literal.

## The verbatim region

`@verbatim { ... @}` copies its body byte for byte and does not scan it for any
Quill syntax, not `{{`, not `{#`, not statement keywords:

```
@verbatim {
public static void main(String[] args) {
    Map<String,Integer> m = new HashMap<>() {{
        put("a", 1);
    }};
}
@}
```

It is the escape hatch for a bulk block that would otherwise trip the one text
sequence that flips into code by sigil: a literal `{{` adjacency (rare, since
no mainstream target uses `{{` as a token). Inner `{ }` are tracked by a
raw-brace depth counter that never interprets `{{`. For a body that must contain
an unbalanced brace or the literal close sequence, a fenced form takes an
author-chosen terminator:

```
@verbatim ~~~END
... arbitrary bytes, possibly unbalanced braces ...
~~~END
```

The region ends at the first line equal to the fence token.

## Next

- [Expressions](expressions.md): operators, filters, arrows, and the
  precedence ladder.
- [Control Flow](control-flow.md): `if`, `for`, `set`, and the region
  statements.
- [Composition](composition.md): inheritance, blocks, macros, includes, and
  slots.
