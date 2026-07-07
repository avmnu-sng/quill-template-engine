# Whitespace Control

Quill gives you byte-exact control over the whitespace around statements and
interpolations, so the output is exactly the bytes you intend -- no spurious blank
lines from control flow, no accidental indentation, and no fighting the engine
when a line break matters. This is a featured differentiator: Quill applies
Jinja-style block cleanup by default, offers three trim modes, and adds a
`spaceless` filter and region, a `trim` filter, and a keep-close-newline pragma
for the cases where the default is not what you want.

If you are coming from Jinja, Twig, or Go `text/template`, jump to the
[mapping table](#coming-from-jinja-twig-or-go-texttemplate) at the end.

## The three trim modes

Two trim modifiers attach to either side of any sigil or statement brace, and a
third attaches to the closing side only:

- **`-` (hard trim)** -- strips all adjacent whitespace, including newlines.
  `{{- expr -}}`; on a statement, `@for ... {-` ... `-@}`.
- **`~` (line trim)** -- strips adjacent spaces, tabs, NUL, and vertical tab but
  **not** newlines. `{{~ expr ~}}`. As a trim modifier `~` sits immediately inside
  a delimiter; as the concat operator it sits between operands, and position
  disambiguates.
- **`+` (keep, closing side only)** -- suppresses the default close-newline-eating
  of a statement's closing `@}` (a lone `}` under bare mode) or a comment's `#}`.
  `@}+` closes a block without consuming the following newline. It is meaningful
  only on a closing delimiter.

A modifier on the opening side trims the preceding text; on the closing side it
trims the following text (or, for `+`, preserves the following newline the close
would otherwise eat).

## Block cleanup by default

By default, Quill applies the equivalent of Jinja's `trim_blocks` and
`lstrip_blocks`: control statements do not leak blank lines into the output. The
rule that does this is the **statement/interpolation newline asymmetry**:

> A statement's closing `@}` (a lone `}` under bare mode) and a comment's `#}`
> each consume exactly one immediately-following newline. An interpolation's `}}`
> consumes none.

This is what lets

```
@for u in users {
{{ u.name }}
@}
```

emit one line per user with no spurious blank lines: the `{` opener's newline and
the closing `@}`'s newline are eaten as statement boundaries, while each
`{{ u.name }}` and the literal newline after it survive. You override the default
per site with the trim modifiers above.

## When you need to keep a newline

The same newline-eating that cleans up list output can be unwanted when the text
around a block already carries meaningful line breaks -- for example when a loop
body's own last line must be followed by a newline before the next block:

```
@for row in (matrix ?? []) {
{{ 2 | tab }}[ {{ row | join(", ") }} ]
@}
{{ "footer" }}
```

Here the `@for`-close `@}` eats the newline before `{{ "footer" }}`, so the last
row and the footer fuse onto one line. The fixes, in order of preference:

1. **Append an explicit newline before the close.** Put `{{ "\n" }}` as the
   body's last line, so the eaten newline is replaced.
2. **Use the no-trim close `@}+`.** The `+` close modifier does not eat the
   trailing newline, so the literal layout is preserved. This is the targeted tool
   when a whole region needs byte-faithful line breaks.
3. **A per-template `pragma keep-close-newline`.** This disables
   close-newline-eating for the entire file, trading the clean list-output default
   for byte-faithful layout. A template that never relies on list-style line
   collapsing can set this pragma to make the byte-faithful case correct by
   default.

The asymmetry that helps per-item output is a deliberate default, not a fixed law:
`@}+` and `pragma keep-close-newline` give byte-exact control wherever the
default's newline-eating would corrupt the intended layout.

## The `spaceless` filter and region

`spaceless` collapses whitespace between tags. As a filter it applies to a piped
string; as a region it wraps a body:

```
{{ markup | spaceless }}

@apply | spaceless {
  <ul>
    <li>one</li>
    <li>two</li>
  </ul>
@}
```

It is the tool for compacting markup where inter-tag whitespace is not
significant.

## The `trim` filter

`trim(side, mask)` strips whitespace from a string:

```
{{ "  hello  " | trim }}            {# "hello" #}
{{ "xxhello" | trim("left", "x") }} {# "hello" #}
```

`side` is `"both"` (default), `"left"`, or `"right"` (aliases `"b"`/`"l"`/`"r"`),
and `mask` is the set of characters to strip (default is the whitespace set). One
filter covers left, right, and both-side trimming.

## Indentation: `tab`, `space`, `break`, and `@tab`

Quill also shapes vertical and leading whitespace directly:

- `n | tab` produces `n` levels of indentation standalone; `s | tab(n)` indents
  each non-blank line of `s` by `n` levels. One level is `WithTabWidth` spaces
  (default 4).
- The functions `space(n)`, `break(n)`, and `tab(n)` emit `n` spaces, `n`
  newlines, and `n` indent levels respectively (each defaults to 1). A count of
  zero or below emits nothing.
- `@tab(n) { body @}` indents the entire rendered body by `n` levels. Indentation
  is applied by the output layer to each non-blank line, so it covers
  interpolation, control-flow output, and included partials uniformly. Blank lines
  stay blank, and regions nest cumulatively via an indent stack.

The indentation helpers are documented in full in the
[Standard Library](stdlib.md).

## Coming from Jinja, Twig, or Go text/template

Most whitespace idioms map directly. This table translates the common ones:

| Goal | Jinja / Twig | Go `text/template` | Quill |
|------|--------------|--------------------|-------|
| Trim whitespace before a tag | `{{- x }}` / `{%- ... %}` | `{{- x }}` | `{{- x }}` / `@for ... {-` |
| Trim whitespace after a tag | `{{ x -}}` / `{% ... -%}` | `{{ x -}}` | `{{ x -}}` / `-@}` |
| Trim spaces/tabs but keep newlines | (no direct form) | (no direct form) | `{{~ x ~}}` (line trim) |
| Strip blank lines from control blocks | `trim_blocks` + `lstrip_blocks` (config) | (manual `-` on every tag) | on by default |
| Keep the newline a block close would eat | (manual) | (manual) | `@}+` or `pragma keep-close-newline` |
| Collapse inter-tag whitespace | `{% spaceless %}` (older) / <code>&#124;spaceless</code> | (none) | <code>&#124; spaceless</code> filter or <code>@apply &#124; spaceless { ... @}</code> |
| Strip leading/trailing whitespace from a value | <code>&#124; trim</code> | (none) | <code>&#124; trim</code> |
| Indent a block of output | <code>&#124; indent(n)</code> | (manual) | <code>&#124; tab(n)</code>, <code>&#124; indent(n)</code>, or `@tab(n) { ... @}` |

The key differences from Go `text/template`: Quill applies block cleanup by
default (so you rarely need to sprinkle `-` on every tag), adds a line-only trim
mode (`~`), and provides a keep-newline escape hatch (`@}+`) for the cases where
the default eats a break you need. The key difference from Jinja/Twig:
`trim_blocks`/`lstrip_blocks` behavior is on by default rather than a config flag,
and the trim modifiers live on the delimiter rather than as `{%-`/`-%}` variants
of every tag.

## Next

- [Standard Library](stdlib.md) -- `spaceless`, `trim`, `tab`, `indent`,
  `space`, `break`.
- [Templates](guide/templates.md) -- the lexical rules the trim modifiers attach
  to.
