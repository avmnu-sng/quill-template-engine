# Quill -- Language Reference

This is the core reference manual for Quill. It covers the lexical structure and the rule
that makes a brace-delimited language safe for emitting brace-dense source; interpolation
and output; the expression language, operators, and precedence; control flow and scoping;
and template composition (inheritance, blocks, macros, includes). The formal grammar is in
`02-grammar.md`; the standard library is in `03-stdlib.md`; the runtime value rules, the
gradual type system, escaping, and the sandbox are in `04-types-and-semantics.md`.

--------------------------------------------------------------------------------

## 1. Lexical structure and the source-emission rule

### 1.1 Two modes, one boundary rule

A Quill source is a sequence of TEXT (emitted verbatim, byte-for-byte) and CODE (parsed).
The lexer is a byte-oriented two-mode state machine. A template starts in TEXT. Under the
DEFAULT statement-lead mode there are exactly three doors out of TEXT into CODE, plus one
bulk passthrough region:

| Opener | Closer | Meaning | Mode |
|--------|--------|---------|------|
| `{{` | `}}` | interpolation: render one expression | CODE (expression) |
| `{#` | `#}` | comment: consumed, emits nothing | CODE (comment) |
| an `@`-led statement keyword | matching `@}` or end of line | a statement | CODE (statement) |
| `@verbatim { ... }` / fenced `@verbatim` | balancing `@}` / fence | literal region, never scanned | TEXT-literal |

Everything else in TEXT -- every bare `{`, `}`, `<`, `>`, `&`, `;`, `(`, `)` -- is ordinary
output text. The load-bearing invariant:

> A single `{` or `}` in template text is NEVER a delimiter, in either statement-lead mode.
> A `{` is a delimiter only when immediately followed by `{` or `#`, with no intervening
> character. Under the default, a statement begins ONLY at `@`, and a block closes ONLY at
> `@}`, so a bare `}` in text -- even a lone `}` at column 0 -- is always literal output.

A lone `{` followed by a space, a letter, a newline, a digit, or a quote is emitted as text
and the scanner stays in TEXT. A Java method body `{ this.head = node; }` passes through
TEXT untouched because none of `{`, ` `, `t`, `;`, `}` opens CODE. The byte-level lexer is
byte-oriented so it can copy TEXT faithfully including bytes that are not valid UTF-8; rune
decoding happens only inside CODE.

The default `@`-sigil statement lead exists because the primary use case emits brace-dense
PROGRAM SOURCE CODE: literal lone-`}` lines are pervasive, the majority at column 0 (top-level
closes of emitted classes, methods, and
functions). The `@`-default makes every one of those sites correct with zero escaping, at the
cost of one `@` per statement. The keyword-led BARE mode -- statements recognized by a
line-leading keyword, a lone `}` line closing the innermost block -- is an explicit opt-in
via a front-matter `pragma bare` (equivalently `pragma sigil off`), for markup and other
non-source templates where brace collisions are rare. Section 1.3 specifies both modes;
unless noted, the rest of this manual writes statements in the `@`-default spelling.

### 1.2 The literal-`{{` collision

The only sequence that flips TEXT into CODE by sigil is a literal `{{` adjacent pair.
Emitted source essentially never contains `{{`: no mainstream target language uses `{{` as a
token. The single real adversary is a Java `new HashMap<>() {{ ... }}` double-brace block,
handled three ways, in order of preference:

1. **Wrap brace-dense bulk output in a `verbatim` region** (Section 1.6). The body is copied
   byte-for-byte and never scanned. This is the recommended tool whenever a template emits a
   large literal block.
2. **Interpolate a string literal for a one-off:** `{{ "{{" }}` emits two literal open
   braces. A trailing `}}` in TEXT is literal, because the close sigil `}}` is recognized
   only when it closes an OPEN `{{`.
3. **Spot-escape with a backslash:** in TEXT, `\{` emits a literal `{` and suppresses sigil
   detection, so `\{{` emits `{{`. `\}` is symmetric. ONLY `\{`, `\}`, and `\\` are escapes;
   a lone backslash in TEXT is literal, so Rust lifetimes and C escapes in emitted source are
   untouched unless they precede a brace.

The asymmetry is deliberate: emitting a literal `{` costs nothing in the dense-single-brace
case that dominates emitted source, and costs one explicit construct only in the vanishingly
rare `{{`-adjacency case. A language that disambiguated `{{` by context would need unbounded
lookahead and would still surprise the author; Quill refuses that trade.

### 1.3 Statements are sigil-led brace blocks, and the two modes

Quill has two statement-lead modes. The DEFAULT is the `@`-sigil mode, chosen because the
primary use case emits brace-dense source; the BARE keyword-led mode is an explicit opt-in.

**Default mode: `@`-led statements, `@}` close.** A statement (`@for`, `@if`, `@block`,
`@macro`, `@set`, `@include`, ...) is recognized only when, after optional leading horizontal
whitespace, a line's FIRST non-whitespace character is `@` immediately followed by one of the
fixed, closed set of statement keywords (Section 5.1) at a word boundary, and the construct
parses as a complete statement head. A brace block opened by an `@`-led statement closes
ONLY at `@}` -- a line whose only non-whitespace content (after optional leading whitespace)
is `@}`, optionally carrying a `-`/`~`/`+` trim modifier. Consequently:

- A bare `{` or `}` ANYWHERE in TEXT -- including a lone `}` at column 0 -- is unconditionally
  literal output. No literal lone-`}` site in source-emission templates needs any escaping.
- A line that begins with the WORD `for`, `if`, `while`, `block` (no `@`) is ordinary TEXT;
  there is no grammar-shape rejection to perform and no `line-leading-keyword` diagnostic to
  emit, because a statement is announced only by `@`.

```
@extends "base.tmpl"

@block body {
  @for u in users {
    {{ u.name }}
  @}
@}
```

Here the only escaping consideration that remains is the vanishingly rare literal `{{`
adjacency (Section 1.2); everything else in TEXT, every `{` and `}`, is literal by
construction.

**Opt-in bare mode: `pragma bare`.** A front-matter `pragma bare` (equivalently
`pragma sigil off`) selects the keyword-led mode: a statement is recognized when a line's
FIRST non-whitespace token is one of the statement keywords at a word boundary and the
construct parses as a complete statement head, and a block closes at a lone `}` line. This
is intended for markup and other non-source templates where literal braces are rare. The
stakeholder-approved anchor is valid Quill in this mode. In bare mode an output line might
legitimately begin with the word `for` (C's `for (...)`) or `if`, and a literal lone `}`
might collide with a block close; Quill resolves both deterministically -- never by heuristic
-- with grammar-shape rejection plus the escape tools below:

- **Grammar-shape rejection.** Quill's `for` requires `for <ident> in <expr> {`. An emitted
  C line `for (int i = 0; i < n; i++) {` is NOT a Quill `for`: the parser sees `(` where it
  requires an identifier, the line does not form a complete statement head, and it is TEXT.
  This handles the common C/Java/Go `for (...)`, `if (...)`, `while (...)` shapes for free,
  because those languages put a `(` immediately after the keyword and Quill never does.

- **The leading-pipe text marker** (per-line total escape). A line whose first non-whitespace
  character is a literal pipe `|` followed by a space is emitted as text verbatim, with the
  `| ` prefix stripped, and is NEVER classified as a statement. Interpolation within the line
  still works. This is the marker for a line that begins with a bare Quill keyword followed
  by statement-shaped content (a literal Ruby `for item in list`, a Python `if cond:`):

  ```
  | for item in list
  {{ body }}
  | end
  ```

- **The verbatim region** (bulk escape). For any block of output that begins lines with Quill
  keywords, `verbatim { ... }` (Section 1.6) copies the body literally with no scanning.

In bare mode the lexer emits a suppressible `line-leading-keyword` diagnostic at every line
that both begins with a Quill keyword AND forms a Quill-shaped head, so the collision is never
silent. The core lexical invariant -- a single `{` or `}` is never a delimiter -- is unchanged
in either mode.

#### The block-close rule and the line-leading-`}` collision (bare mode only)

This collision exists ONLY in opt-in bare mode; under the `@`-default a lone `}` is always
literal TEXT and there is nothing to resolve. A brace-bodied statement (`for`, `if`, `block`,
...) has a TEXT body in which literal `{` and `}` are ordinary bytes and are NOT brace-counted
(bracket balancing applies only inside CODE, Section 1.7). In bare mode the closer of such a
body is recognized by exactly this rule:

> A line whose ONLY non-whitespace content is a single `}` (optionally carrying a `-`/`~`
> trim modifier) closes the innermost open Quill block. This is the ONLY close form; a `}`
> with any other non-whitespace on its line is literal TEXT.

So emitted source that closes its own braces ON THE SAME LINE as other content -- the common
idiom `{{ 1 | tab }}}` (an indented or interpolation-led close) or `} else {` in emitted C --
is never a Quill close, because the line is not a lone `}`. The collision is the residual
case: a TEXT body that emits a brace-balanced block whose own close lands as a bare `}` at
COLUMN 0 (a Python dedent to a column-0 `}`, an un-indented generated `}`, a C label, a
heredoc terminator). In source-emission templates such literal lone-`}` lines are pervasive,
the majority at column 0, written bare rather than interpolation-led. There the
literal close and the intended Quill close are byte-identical lone-`}` lines, and the rule
above would bind the FIRST such line to the open block, truncating the body. The `@`-default
exists precisely so this case never arises for source emission; under bare mode the author
disambiguates explicitly:

1. **The bare line-leading `}` always closes the innermost open Quill block.** The parser
   maintains the open-block stack; a lone `}` line pops one level. This is fixed and
   unambiguous -- there is no heuristic that tries to tell a literal close from a Quill close.
2. **An author emitting a literal lone `}` at column 0 inside a Quill block MUST disambiguate
   it**, by one of: prefixing the leading-pipe text marker (`| }`), wrapping the brace-dense
   region in `verbatim`, or emitting the brace through interpolation (`{{ "}" }}`). The
   leading-pipe and `verbatim` forms are the recommended tools. (Indentation does NOT help --
   see below.) Switching the template to the `@`-default removes the requirement entirely.
3. **Indentation does not by itself escape a literal `}`** -- a line that is whitespace then
   `}` is still a lone-`}` close. Only the leading-pipe marker, `verbatim`, or interpolation
   make a column-aligned or column-0 literal `}` non-closing. A template may also indent
   literal closes via `{{ N | tab }}}` (interpolation-led, not a lone `}`); that is an
   instance of the interpolation escape, not of indentation.
4. **A hard error, not the suppressible diagnostic, fires on a structural mismatch.** If the
   open-block stack is non-empty at end of file (a Quill block never closed) OR a lone `}`
   appears with an empty stack (a close with no open block), the parser raises an
   unrecoverable `unbalanced-block` error naming the open construct and its source line. This
   is distinct from the suppressible `line-leading-keyword` diagnostic: a brace imbalance is
   never silently absorbed.

The decision procedure is therefore total in bare mode: every lone-`}` line closes the
innermost block; literal lone-`}` output is escaped explicitly; and a mismatch is a hard
error. Under the `@`-default these escape tools are unnecessary for the lone-`}` case and
remain available only for genuine edge cases.

### 1.4 Whitespace control -- byte-exact

Generated-source fidelity demands byte-exact whitespace control. Two trim modifiers attach to
either side of any sigil or statement brace:

- **`-` (hard trim):** strips ALL adjacent whitespace including newlines. `{{- expr -}}`; on a
  statement, `@for ... {-` ... `-@}`.
- **`~` (line trim):** strips adjacent spaces, tabs, NUL, and vertical tab but NOT newlines.
  `{{~ expr ~}}`. As a trim modifier `~` sits immediately inside a delimiter; as the concat
  operator (Section 3) it sits between operands. Position disambiguates.
- **`+` (keep, closing side only):** suppresses the default close-newline-eating of a
  statement's closing `@}` (lone `}` under bare mode) or a comment's `#}`. `@}+` closes a
  block WITHOUT consuming the following newline, the inverse of the newline-eating asymmetry
  below. It is meaningful only on a closing `@}`/`}`/`#}`; on any other delimiter side it is
  not a trim modifier.

A modifier on the opening side trims preceding text; on the closing side trims following text
(or, for `+`, preserves the following newline the close would otherwise eat).

The **statement/interpolation newline asymmetry** is a fixed rule that controls emitted line
layout: a statement's closing `@}` (lone `}` under bare mode) and a comment's `#}` each
consume exactly ONE immediately-following newline; an interpolation's `}}` consumes none. This
is what lets

```
@for u in users {
{{ u.name }}
@}
```

emit one line per user with no spurious blank lines: the `{` opener's newline and the closing
`@}`'s newline are eaten as statement boundaries, while each `{{ u.name }}` and the literal
newline after it survive. Authors override per-site with the trim modifiers.

**The close-newline interaction with brace-dense source.** The same newline-eating that cleans
up list output can FIGHT brace-dense source output, the primary workload. When a Quill block
wraps emitted code whose own last line is a literal `}`, the loop's closing `@}` line eats the
newline the EMITTED code needs after its own literal `}`, fusing two source lines:

```
@for row in (matrix ?? []) {
{{ 2 | tab }}{ {{ row | join(", ") }} }
@}
{{ "footer" }}
```

The `@for`-close `@}` eats the newline before `{{ "footer" }}`, so the last matrix row and the
footer fuse onto one line. (This concerns only the close NEWLINE; the literal `}` emitted by
the body is unconditionally literal under the `@`-default and needs no escaping.) This is the exact failure that source-emission templates work around with the
trailing `{{"\n"}}` idiom (a `}{{"\n"}}` tail line). The
canonical fixes, in order of preference:

1. **Append an explicit newline before the close:** put `{{ "\n" }}` as the body's last line,
   or end the preceding line with it, so the eaten newline is replaced.
2. **Use the no-trim close `@}+`** (the `+` close modifier is the inverse of `-`/`~`): a `@}+`
   does NOT eat the trailing newline, so the literal layout is preserved. This is the
   targeted tool when an entire region is brace-dense.
3. **A per-template `pragma keep-close-newline`** disables close-newline-eating for the whole
   file, trading the clean list-output default for byte-faithful source layout. A
   source-emitting template that never relies on list-style line collapsing SHOULD set this
   pragma; it makes the brace-dense case correct by default and is recommended for source emission.

The asymmetry that helps `{{ u.name }}` per-user output is a deliberate default, not a fixed
law: `@}+` and `pragma keep-close-newline` give byte-exact control wherever the default's
newline-eating would corrupt emitted brace layout.

### 1.5 String literals, interpolation, and comments

- **String literals:** single-quoted `'...'` (no interpolation; escapes `\\` `\'` `\n` `\t`
  `\xHH`) and double-quoted `"..."` (full escape set `\n \t \r \\ \" \xHH \u{...}` plus
  embedded interpolation). Adjacent string literals do NOT implicitly concatenate; use `~`. A
  backtick raw string `` `...` `` performs no escape processing (useful for regex patterns
  and paths).
- **String interpolation:** inside a double-quoted string, `#{ expr }` embeds an expression,
  compiling to a `~` concatenation chain. `\#{` is a literal `#{`. Single-quoted and backtick
  strings never interpolate. Example: `"Hello #{name | upper}!"`.
- **Block comment:** `{# ... #}`, consumed entirely, emits nothing. An unterminated `{#` is a
  lex error at the opening position. The `#}` closer eats one following newline.
- **Line comment inside CODE:** `#` to end of line within an expression or statement head
  emits nothing. A `#` inside a string literal is literal.

### 1.6 The verbatim region

The primary escape hatch for brace-dense bulk output. Its body is copied byte-for-byte and is
NOT scanned for any Quill syntax -- not `{{`, not `{#`, not statement keywords:

```
@verbatim {
public static void main(String[] args) {
    Map<String,Integer> m = new HashMap<>() {{
        put("a", 1);
    }};
}
@}
```

The body ends at the `@}` that balances the `@verbatim {` opener (a lone `}` under bare mode);
inner `{ }` are tracked by a raw-brace DEPTH COUNTER that never interprets `{{`. For a body
that must contain an unbalanced brace or the literal close sequence, a FENCED verbatim takes an
author-chosen terminator (the heredoc model):

```
@verbatim ~~~JAVA
... arbitrary bytes, possibly unbalanced braces ...
~~~JAVA
```

The region ends at the first line equal to the fence token.

### 1.7 Bracket balancing, literals, and word-operator disambiguation

- **Bracket balancing inside CODE.** Inside an interpolation or statement head, the lexer
  balances `()`, `[]`, and `{}` so an inner `}` does not prematurely close the interpolation.
  The close sigil `}}` is recognized only at brace-depth zero relative to its opener, so
  `{{ {a: 1, b: 2} | json }}` works.
- **Numbers:** `42`, `1_000_000` (digit-group `_` separators between digits), `3.14`,
  `1_0.0_5`, `0xFF`, `0b1010`, `0o755`, `1e9`. Int is int64, float is float64.
- **Bool / null:** `true`, `false`, `null` (canonical); `none` is an accepted alias for
  `null`. Case-sensitive; `True` and `NULL` are identifiers.
- **Sequence / mapping literals:** `[1, 2, 3]` with trailing comma and spread `[...xs, 4]`;
  `{a: 1, b: 2}` with shorthand `{a}` for `{a: a}`, computed keys `{(expr): v}`, trailing
  comma, and spread `{...base, c: 3}`. Because a mapping literal appears only in CODE, its
  braces never collide with TEXT braces.
- **Word-operator / identifier disambiguation.** Tokenization is maximal-munch: longer
  operators win (`==` before `=`, `//` before `/`, `?.` before `?`, `not in` before `in`,
  `<=>` before `<=`). A word-operator (`and`, `or`, `not`, `in`, `is`, `matches`, `xor`,
  `starts`, `ends`, `has`) is an operator ONLY in operator position; in primary position and
  immediately after `.` or `|` it is a plain identifier, so a host field named `in` is
  reachable as `record.in`.

- **Special primary names.** Three reserved primaries are always defined, exempt from the
  strict-undefined rule (Section 6 of `04-types-and-semantics.md`), and resolved by the
  engine rather than by context lookup:

  | Name | Value | Use |
  |------|-------|-----|
  | `_self` | the current `Template` (its name renders via `ToText`; it is also a valid `import`/`from` source) | `import _self as me` then `me.helper(...)`; `from _self import helper` |
  | `_context` | the live context as an `*Array` (string keys to current bindings) | reflective access, host RNG seeding, `dump(_context)` |
  | `_charset` | the configured charset string (default `"UTF-8"`) | charset-sensitive author logic |

  A bare read of any of the three NEVER raises an undefined-variable error, even under strict
  mode; the engine supplies the value. They are reserved: a context variable named `_self`,
  `_context`, or `_charset` is shadowed by the special name and unreachable as a plain
  identifier. The grammar production is `SpecialName` (`02-grammar.md` Section 4). `_self` as
  an `import`/`from` source lets a template call its OWN macros and those it inherits via
  `extends` (Section 5.4); this is the only way a template reaches its own macro namespace
  through a name.

### 1.8 Fixed delimiters and source positions

Quill FIXES its delimiters. The
verbatim fence token and the `pragma bare` statement-lead mode (which opts out of the
`@`-sigil default, Section 1.3) are the only per-template knobs, and neither changes the
interpolation, comment, or string-interpolation sigil bytes. Source is CR/CRLF-normalized to LF before line
counting; every token and AST node carries 1-based `{Line, Col}`; diagnostics report
`template:line:col`. A `line N` statement (Section 4.7) resets the reported line for
generated or embedded fragments.

--------------------------------------------------------------------------------

## 2. Interpolation and output

### 2.1 The print form

`{{ expr }}` evaluates `expr` and renders it via the `ToText` rules
(`04-types-and-semantics.md` Section 4). The anchor's `{{ u.name | upper }}` is a pipe
expression rendered to output.

### 2.2 Pipe filters

`|` pipes the left value into a filter as its first argument: `x | upper` is `upper(x)`;
`x | f(a, b)` is `f(x, a, b)`. Filters chain left to right: `x | trim | upper`. The pipe is
an ordinary expression operator (Section 3 places it in the ladder), so a filtered value may
appear anywhere an expression may, not only at output sites. Because `|` is the filter
operator, bitwise-or is the word `b_or` (Section 3.1).

The pipeline is the ergonomic spine of the collection algebra, composing with arrow functions
and the spaceship comparator into a Unix-style pipeline:

```
{{ users
   | filter((u) => u.active)
   | sort((a, b) => a.rank <=> b.rank)
   | map((u) => u.name | upper)
   | join(", ") }}
```

### 2.3 The postfix conditional on output

`{{ expr if cond }}` renders `expr` only when `cond` is truthy; otherwise it renders nothing
(the empty string, with no surrounding whitespace produced). The anchor's
`{{ ", admin" if u.isAdmin }}` emits the literal `, admin` exactly when `u.isAdmin` is
truthy. An optional `else` tail supplies a fallback: `{{ u.title if u.hasTitle else
"(untitled)" }}`. A symmetric `{{ expr unless cond }}` is accepted.

The postfix conditional is pure sugar: `{{ x if c }}` desugars to `{{ x if c else "" }}`,
which desugars to the ternary `{{ c ? x : "" }}`. One AST node, zero new evaluation rules.
The grammar reserves the postfix `if [...] [else ...]` tail specifically in interpolation
context, parsed after the main expression at the lowest interpolation precedence, so it never
collides with a ternary inside the expression.

--------------------------------------------------------------------------------

## 3. Expressions, operators, and precedence

Expressions are Go-flavored: infix arithmetic, pipe filters, arrow functions, null-safe
access. The grammar is Pratt-parsed. An expression appears only in CODE positions -- inside
`{{ ... }}`, inside a statement head, inside `#{ ... }`, and nested in another expression. It
always evaluates to a single dynamic `Value` (`04-types-and-semantics.md` Section 1).

### 3.1 The published ladder

Quill publishes its own precedence numbers; only the relative ordering is binding. Higher
binds tighter (evaluated first).

| Level | Operators | Assoc |
|-------|-----------|-------|
| 17 | postfix: `.` `?.` `[ ]` `?[ ]` `( )` call `\|` filter | left |
| 16 | prefix: `not` (`!`) `-` `+` `...` spread | right |
| 15 | `**` power | right |
| 14 | `*` `/` `//` `%` | left |
| 13 | `+` `-` | left |
| 12 | `~` concat | left |
| 11 | `..` range | left |
| 10 | comparison/membership/test: `==` `!=` `<` `>` `<=` `>=` `<=>` `in` `not in` `matches` `starts with` `ends with` `has some` `has every` `is` `is not` | non-assoc |
| 9 | `b_and` (`&`) | left |
| 8 | `b_xor` (`^`) | left |
| 7 | `b_or` (`\|\|\|`) | left |
| 6 | `and` (`&&`) | left |
| 5 | `xor` | left |
| 4 | `or` (`\|\|`) | left |
| 3 | `??` coalesce, `?:` elvis | right |
| 2 | `? :` ternary, postfix `if`/`unless`/`else` | right |
| 1 | `=>` arrow, `=` assignment / destructuring | right |

**Power and unary minus.** Power (level 15) binds tighter than unary minus (level 16 prefix),
but the right operand of the right-associative `**` re-enters at the prefix level, and unary
minus wraps the power by AST shape. One rule (AST-driven precedence) governs both, so
`-1 ** 0` parses as `-(1 ** 0) = -1` and `(-1) ** 2 = 1`, entirely from the published
precedence table.

**Bitwise-or spelling.** Because `|` is the filter pipe, bitwise OR is the word `b_or` (alias
`|||`), bitwise XOR is `b_xor` (alias `^`), and bitwise AND is `b_and` (alias `&`). `b_or` is
canonical. The word spelling keeps the pipe unambiguous while providing the full bitwise
capability.

### 3.2 The operator catalogue

The runtime rules for equality, ordering, coercion, and arithmetic are specified in
`04-types-and-semantics.md` Sections 2-4; this section names the operators and their surface.

- **Logical** `and` (`&&`), `or` (`||`), `xor`, `not` (`!`): short-circuit `and`/`or`,
  boolean `xor`, prefix `not`, all over the single truthiness rule. Word and symbol spellings
  are interchangeable. Result is a `Bool`.
- **Bitwise** `b_and`, `b_or`, `b_xor`: integer-only; a non-int operand is a type error.
- **Equality** `==`, `!=`: typed equality. `1 == "1"` is false. `===`/`!==` are accepted
  aliases of `==`/`!=` (which is already strict); raw reference/kind identity is the
  `same(a, b)` builtin.
- **Ordering** `<` `>` `<=` `>=` `<=>`: total within the number tower and between two strings
  (byte-lexicographic). Cross-kind ordering is a check-time error where types are known, else
  a runtime error -- never silent juggling. One comparator backs them all.
- **Membership** `in` / `not in`: for an `*Array`, true iff some element is `==` the needle
  (typed equality, so `"1" in [1]` is FALSE); for a string haystack, substring containment of
  the rendered needle.
- **Regex** `matches`: Go RE2 dialect. The right operand is a STRING expression whose
  contents are the RE2 pattern; there is no `/re/` regex-literal token (a bare `/` is always
  the division operator, Section 3.1). Inline flags use RE2's own `(?flags)` prefix inside the
  pattern string, e.g. `s matches "(?i)^err"`. A backtick raw string is the ergonomic form for
  patterns with backslashes: `s matches \`^\d+$\``. A literal-string pattern is validated at
  compile time. PCRE-only features (backreferences, lookaround, possessive quantifiers) are
  unavailable and rejected with a clear error.
- **String predicates** `starts with` / `ends with`.
- **Quantifiers** `has some` / `has every`: a predicate over an iterable using arrows, e.g.
  `xs has some (x => x > 0)`, `xs has every (x => x.valid)`.
- **Range** `..`: `1..5`, `'a'..'e'`; same engine as `range(...)`.
- **Arithmetic** `+ - * / // % **`: numeric only. `"3" + 4` is a type error, not `7`. `/` is
  true division (float result unless exact), `//` is floor division, `%` is remainder, `**` is
  right-associative power. `+` is NEVER array union (that is the `merge` filter). Int overflow
  is a defined error, not silent promotion to float.
- **Concat** `~`: string concatenation, each operand rendered by `ToText`. Kept distinct from
  `+` -- doubly important for source assembly, where concatenation dominates.
- **Ternary / elvis / coalesce**: three distinct fallthrough predicates. `a ? b : c`
  (truthiness of `a`; no-else `a ? b` yields empty when `a` is falsy); `a ?: b` (`a` if truthy
  else `b`, through a defined-safe path); `a ?? b` (`a` if defined and not null else `b`,
  predicate is definedness not truthiness). The coalescing operators and `default` suppress
  undefined-misses across the ENTIRE left-operand access chain, not just its final hop, so
  `user.nick ?? "anon"` yields `"anon"` when `user` itself is absent -- the full rule and the
  per-hop table are in `04-types-and-semantics.md` Section 6.
- **Assignment / destructuring** `=`: expression-form assignment returning the assigned value,
  right-associative (`c = d = 'x'`). Sequence destructuring `[a, b] = e`, mapping/object
  destructuring `{name} = e`, rename `{key: alias} = e`, elided slots `[, b] = e`. Over/under-
  supply is an error by default (a generator should not silently pad with null); explicit
  `[a, b, ...rest] = e` captures the tail and `[a, b?] = e` marks a slot optional (padded with
  null).
- **Arrow** `=>`: `x => expr`, `(x, y) => expr`, `() => expr`. Closes over template scope;
  params shadow context for the call duration. Used by `map`/`filter`/`sort`/`reduce`/`find`
  and the quantifier operators. Params may carry type annotations: `(x: int) => x * 2`. Under
  sandbox, an arrow must be template-defined.
- **Grouping** `( )`: overrides precedence, fixes associativity, and is the entry point for
  arrow param lists and parenthesized destructuring targets.

### 3.3 Primary expressions and call arguments

A primary is a bare identifier (context lookup), a literal, `( expr )`, a sequence or mapping
literal, a function call `name(args)`, or the postfix chain (access, subscript, slice, call,
filter, test). Attribute and index access (`a.b`, `a[k]`, `a?.b`, `a?[k]`, slices) are
specified in `04-types-and-semantics.md` Section 5.

**One argument grammar for every callable.** Filter calls, function calls, and test calls all
share the single `Args` production (`02-grammar.md` Section 4): positional `e`, named
`name: e`, and spread `...e`, in that left-to-right order, with defaults declared on the
callable filling any parameter not supplied. The three short forms are special cases of it:

- `name(args)` -- a function call; `args` is the full grammar.
- `x | f` and `x | f(args)` -- a filter call. `x | f` is the zero-explicit-argument case
  (the pipe supplies the first argument and declared defaults fill the rest); `x | f(args)`
  threads `args` after the piped value, so `x | f(a, key: c, ...rest)` is
  `f(x, a, key: c, ...rest)`. Named, defaulted, and spread arguments are all legal in the
  parenthesized filter form.
- `x is t` and `x is t arg` and `x is t(args)` -- a test call. `x is t` is the
  zero-argument case; `x is t arg` is the one-positional-argument short form (`arg` is a bare
  `Primary`); `x is t(args)` is the full form and admits named and spread arguments, so
  `n is divisible_by(n: 3)` is legal. Declared defaults fill the rest in every form.

Because all three resolve to the same `Args` grammar and the same callable signature, named
arguments are available uniformly across filters, functions, methods, and tests; the bare and
one-arg short forms are only sugar for the zero/one-positional cases.

--------------------------------------------------------------------------------

## 4. Control flow and scoping

All block statements use brace bodies `{ ... }` -- no end-keywords. Under the `@`-default a
body closes at `@}`; under `pragma bare` it closes at a lone `}` (Section 1.3). The examples
below use the `@`-default spelling.

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

One `if` head, zero-or-more `elseif`, optional `else`. Conditions are arbitrary expressions
taken in the single truthiness rule (`04-types-and-semantics.md` Section 2). There is no
`TrueTest` wrapper.

### 4.2 Loops

```
@for u in users {
  {{ u.name }}
@} else {
  no users
@}
```

- One or two targets: `for v in seq` and `for k, v in mapping` (mappings iterate in insertion
  order with both targets).
- The `else` branch runs exactly when the sequence yielded zero iterations -- and only after
  the iterand was successfully resolved to a collection. The `else` is reached for an
  iterable-but-empty value; it is NOT reached when the iterand is non-iterable, because the
  error preempts the loop before any iteration count exists.
- **Non-iterable is a runtime error**, NOT a silent empty loop -- a silently skipped loop
  emits an entire missing code section, catastrophic for a generator. The explicit "empty is
  fine" idiom is `for x in (coll ?? []) { ... }`. Where a static type proves non-iterability,
  the error is promoted to check time.
- **The three cases, made precise** (this resolves the `else`-vs-error interaction):

  | Iterand `E` | Behavior |
  |-------------|----------|
  | iterable, one or more elements | body runs per element; `else` NOT run |
  | iterable, zero elements (`[]`, `{}`) | body skipped; `else` run |
  | `Null` or any non-iterable, written bare (`for x in E`) | RUNTIME ERROR; `else` NOT reached |
  | `Null`/absent, coalesced (`for x in (E ?? [])`) | the `??` yields `[]`, an empty collection -> `else` run |

  Coalescing with `?? []` collapses "absent" and "empty" into one observable case (empty), so
  a `for x in (E ?? []) { } else { }` runs the `else` when `E` is absent, null, or an empty
  collection alike. An author who needs to distinguish "absent" from "present but empty" tests
  `E is defined` before the loop rather than relying on the `else`.
- **Loop metadata.** Inside the body, `loop` exposes `loop.parent`, `loop.index0`,
  `loop.index`, `loop.first`, `loop.last`, `loop.length`, `loop.revindex`, `loop.revindex0`,
  `loop.prev`, `loop.next`, and the method `loop.changed(expr)`.
  ALL loop fields are ALWAYS defined: the `*Array` always knows its length, and a host iterator
  is drained to an `*Array` before the loop, trading a small memory cost for an always-present
  `loop.last`.
  - `loop.prev` and `loop.next` are the value of the previous and next element. Because Quill
    materializes the sequence before iterating, both are available: `loop.prev` is `Null` on the
    first iteration and `loop.next` is `Null` on the last. For a `for k, v in mapping` loop they
    are the previous and next VALUE (the `v` side).
  - `loop.changed(expr)` is `true` on the first iteration and whenever `expr` differs -- by the
    single typed-equality rule (`04-types-and-semantics.md` Section 4) -- from the value it had on
    the prior iteration; otherwise `false`. Each call site is tracked independently, so a body may
    watch several expressions, and a nested loop keeps its own memory. It is the idiom for
    section headers over grouped rows: `@if loop.changed(row.group) { [{{ row.group }}] @}`.
    `expr` may reference the loop variable(s). It resolves against THIS loop's own memory,
    including when it appears in a fused `if` filter clause: there it tracks each candidate
    element on this loop's own iteration, so a top-level `@for x in xs if loop.changed(x)` keeps
    one element per run of adjacent duplicates. A filter call site and a body call site are
    tracked independently, and a nested loop's filter never disturbs an enclosing loop's memory.
- **Fused loop filtering (`@for ... if cond`).** An optional `if <expr>` clause between the
  iterand and the body brace pre-filters the iterand to the elements for which `cond` is truthy,
  and the body runs only over those SURVIVORS:

  ```
  @for u in users if u.active {
    {{ u.name }}{{ ", " if not loop.last }}
  @} else {
    no active users
  @}
  ```

  Every `loop.*` field -- `index`/`index0`, `first`/`last`, `length`, `revindex`/`revindex0`,
  and `prev`/`next` -- reflects ONLY the survivors, so a trailing-separator idiom keyed on
  `loop.last` is correct over the filtered subset (the last survivor is the one with no trailing
  comma). The `else` branch runs when ZERO elements survive the filter. `cond` may reference the
  loop variable(s); for a `for k, v in mapping if cond` loop it may reference both the key and the
  value. The filter condition runs in the loop scope, and its bindings do not leak past the loop.
- **Scoping.** The loop body is a child scope. A variable that existed before the loop and is
  reassigned inside keeps its last in-loop value after the loop; a variable introduced only in
  the body (via `set`) does not leak. The rule is lexical scoping.

### 4.3 Assignment and capture

```
@set name = u.name
@set a, b = e1, e2
@set count: int = users | length          // optional type annotation
```

`@set` binds one or more targets; same count on both sides or a clear error. Optional type
annotation per target. Assignment is an expression returning the assigned value:
`{{ b = 1 + 3 }}` both stores `b` and prints `4`; `@do b = 1 + 3` stores without printing.

A target may also be a member place rather than a plain name: `@set recv.name = expr` and
`@set recv[key] = expr` assign THROUGH a receiver. On a mapping this stores the key in place;
on a host reference value (a `cell`, `03-stdlib.md` Section 3.2a) it calls the write hook, so
`@set acc.value = acc.value + w` mutates the cell in place. Because a reference value
circulates by pointer, such a mutation is visible after a loop body while the loop's own name
rebindings still do not leak. A receiver that does not support assignment is a runtime error.

Block capture:

```
@set banner = capture {
  /* generated header for {{ target }} */
@}
```

The `capture { ... }` block renders its body to a string-like value. Under the default
(escaping off) the capture is a plain `Str`; under an `escape`-on template it is a `Safe`
value.

### 4.4 Effect, flush, deprecation, logging

- `@do expr` -- evaluate for side effects, no output.
- `@flush` -- a documented no-op for a string/byte sink, kept for parity.
- `@deprecated "message" [since "2.0"]` -- routes a deprecation diagnostic to the diagnostics
  sink, no output.
- `@log expr` -- evaluates `expr` and writes its text form to the host logger
  (`WithLogger(l)`, default a discarding logger). It produces NO rendered output but IS a
  coverable unit: the coverage Collector records the `@log` node as executed. A comment
  `{# ... #}`, by contrast, is consumed by the lexer, emits nothing, and is never counted by
  coverage.

### 4.5 Scoped variable region and filter-apply

- `@with { x: 1, y: 2 } { ... }` introduces a scope merging the given vars; `@with { x: 1 }
  only { ... }` replaces the context entirely for the body, then pops.
- `@apply | trim | upper { ...body... }` captures the body and pipes it through the filter
  chain. The pipe syntax composes here exactly as in expressions.

### 4.6 Feature guard and types

- `@guard filter("markdown") { ... } else { ... }` selects a branch on whether the named
  callable (filter/function/test) is registered; the dead branch is parsed but NOT validated
  against unknown callables -- portable across host configs.
- `@types { x: string, n: int }` declares context types. In Quill this is the file-scope form
  of the per-variable annotations and is consumed by the gradual checker directly
  (`04-types-and-semantics.md` Section 1), not as an inert metadata side-channel.

### 4.7 Region statements

- `@escape html { ... }` / `@escape off { ... }` sets the active escaping strategy for a
  region. Default is `off` (source emission); see `04-types-and-semantics.md` Section 8.
- `@sandbox { ... }` forces sandboxing for templates included within the region; equivalently
  the per-include `sandboxed: true` flag.
- `@tab(n) { ... }` indents the entire rendered body by `n` levels, nesting cumulatively via an
  output-layer indent stack; blank lines stay blank. One level is `WithTabWidth` spaces
  (default 4). See `03-stdlib.md` Section 5.1b.
- `@verbatim { ... }` / fenced verbatim -- literal body, not scanned (Section 1.6).
- `@line 42` resets the reported source line for diagnostics in embedded or generated fragments.
- `@cache key="header" ttl=3600 tags=["a"] { ... }` caches a rendered body under a key with
  optional ttl/tags -- an optional, pluggable extension.

--------------------------------------------------------------------------------

## 5. Composition: inheritance, blocks, macros, includes

Composition is built on the `Template` contract (`Display`/`Block`/`HasBlock`/`Macro`/
`HasMacro`/`Parent`). The shared data structure is the BLOCK TABLE, an ordered map from block name to a
`BlockRef{Owner, ID}`; inheritance, embed, and trait reuse all reduce to building and merging
block tables and walking a parent chain. Macros are a separate, isolated function namespace.

### 5.1 The closed keyword set

The statement keywords, fixed for the lexer's statement recognition (Section 1.3). Under the
`@`-default each is written with a leading `@` (`@for`, `@if`, `@block`, ...) and a block
closes with `@}`; under `pragma bare` each is written bare and a block closes with a lone `}`:

```
extends  block  for  if  elseif  else  macro  set  include  import
from  use  embed  with  apply  do  flush  deprecated  guard  types
escape  sandbox  verbatim  line  cache  capture
```

Under the default, a line begins a statement ONLY when its first non-whitespace character is
`@` immediately followed by one of these keywords; everything else at line start is text,
including a line that begins with the bare WORD of any keyword. Under `pragma bare`, a line
that begins with one of these keywords (forming a Quill-shaped head) is a statement, and
everything else at line start is text.

### 5.2 Inheritance

```
@extends "base.ql"           // single parent; forbidden in block/macro;
                             // content outside blocks in a child is rejected

@block body {                // define + render-in-place; long form
  ...
@}
@block title "Default Title" // shortcut value form
@block outer {               // nested, independently overridable
  @block inner { ... @}
@}
```

`@extends "<expr>"` takes a string-coerced expression, so a candidate list
`@extends ["a.ql", "b.ql"]` selects the first that exists. Inside an overriding block,
`parent()` renders the parent's version. `block("name")` and `block("name", "other.ql")`
render a named block of this or another template; `block("name") is defined` tests existence.

### 5.3 Macros

```
@macro greet(name, greeting: string = "Hello", ...rest) {
  {{ greeting }} {{ name | default("guest") }}
@}
```

Declared params with constant defaults, optional type annotations, and a variadic capture
`...rest`. A macro sees ONLY its params, defaults, variadics, and host globals -- the caller's
local context is invisible. A macro returns its captured output (a `Str`, or `Safe` under
escaping).

**The macro namespace is in scope inside every macro body.** A macro body sees, in addition
to its params/defaults/variadics/globals, the names of all macros visible to the template --
its OWN macros, sibling macros declared in the same template, and macros brought in by
`import`/`from`. So a macro may call itself or a sibling directly by name:

```
@macro tree(node) {
  {{ node.label }}
  @for child in (node.children ?? []) {
    {{ tree(child) }}            // direct recursive self-call
  @}
@}

@macro page(title) {
  {{ header(title) }}            // sibling macro, called by bare name
@}
```

Recursion and mutual recursion are therefore reachable two ways: by bare name (the macro
namespace above) and by the `_self` import path (`import _self as me; me.tree(...)`, Section
1.7 and 5.4). The macro namespace is the only caller-context-independent set of names visible
to a macro; it does not breach isolation, because a macro name resolves to a callable, not to
the caller's locals.

### 5.4 Imports and traits

```
@import "forms.ql" as forms          // namespace; call forms.input(...)
@from "forms.ql" import input, label as lbl   // selective; call input(...), lbl(...)
@use "buttons.ql"                    // import all blocks of a traitable template
@use "buttons.ql" with { submit: ok }   // block aliasing/rename
```

Top-level import is global; in-block import is block-local. A trait has no parent, no macros,
and no free body; trait-then-own precedence means the importing template's own block
definitions win over imported ones.

### 5.5 Embed

```
@embed "card.ql" with { title: t } {
  @block body { {{ content }} @}
@}
```

Inline an anonymous child of the embedded template: include plus block override in one
construct. Supports `with`, `only`, and `ignore missing`.

### 5.6 Includes

Statement form:

```
@include "header.ql"
@include "row.ql" with { user: u }
@include "row.ql" with { user: u } only
@include "maybe.ql" ignore missing
@include ["a.ql", "b.ql"]            // first that exists
```

`with map` adds vars to the current context; `only` renders with just those vars;
`ignore missing` tolerates absence, rendering nothing; a sequence is a candidate list, first
existing wins.

Function form, returning rendered output as an expression value, distinct from the statement:

```
{{ include("snippet.ql", { x: 1 }, with_context: false, ignore_missing: true, sandboxed: true) }}
```
