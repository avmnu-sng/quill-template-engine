# Quill -- Twig Parity and Migration

This document proves the parity claim -- Quill carries NO FEWER features than Twig 3.27.1 --
and gives the Twig-to-Quill migration story. It has three parts: the parity matrix (every Twig
capability mapped to a Quill spelling, marked COVERED, EXCEEDED, or FOLDED), the Go-native
delta (what Quill adds beyond Twig), and the migration assessment with side-by-side
examples. Faithfulness to Twig is required at the FEATURE level, not at the level of PHP value
accidents; each deliberate Go-native semantic change is justified by the source-emitting axiom.

The exhaustive 246-item parity matrix and the per-element coverage table are tracked in the
project's design notes; this document is the audience-facing summary that cites the governing
sections in the other spec files.

--------------------------------------------------------------------------------

## 1. The parity matrix

Three statuses: **COVERED** (a faithful equivalent exists), **EXCEEDED** (the equivalent is
present and strictly more capable), **FOLDED** (one Twig spelling merged into another with no
loss of reachable behavior). There are ZERO capability-reducing drops.

### 1.1 Tags and statements (T1-T36)

The Quill spellings below are shown in the `@`-default form (statement heads led by `@`, blocks
closed by `@}`), which is the default for source-emitting `.ql` templates. Under the bare opt-in
(`pragma bare`) the same statements drop the `@` and close on a lone `}` line; the bare spelling
of the `if`/`for`/`set` anchor appears in Section 4.5.

| Twig | Quill | Status |
|------|-------|--------|
| `if`/`elseif`/`else` | `@if E { } @elseif E { } @else { @}` | COVERED. Condition taken in the single truthiness rule, no `TrueTest` wrap. |
| `for`/`else` | `@for v in E { } @else { @}`, `@for k, v in E { @}` | EXCEEDED. Non-iterable is a runtime error (opt-in empty via `@for x in (E ?? [])`); ALL `loop` length fields always defined. |
| `set` (inline + capture) | `@set a = E`, `@set a, b = E1, E2`, `@set x = capture { @}` | COVERED. Adds optional per-target type annotation. |
| `include` | `@include "n.ql"` + `with`/`only`/`ignore missing`/candidate list | COVERED. |
| `extends` | `@extends "base.ql"` head directive | COVERED. |
| `block`/`endblock` | `@block name { @}` (long), `@block title "Default"` (shortcut) | COVERED. Nested blocks independently overridable. |
| `macro`/`endmacro` | `@macro f(p, q = "d", ...rest) { @}` | COVERED. `...rest` replaces Twig's `varargs`; adds optional param/return types. The macro namespace (own/sibling/imported macros) is in scope in every macro body, so self and mutual recursion resolve by bare name (T20/V19). |
| `import` | `@import "forms.ql" as forms`; `@import _self as me` | COVERED. `_self` (special name, V4/T21) imports the current template's own and inherited macros under an alias. |
| `from` | `@from "forms.ql" import input, label as lbl` | COVERED. |
| `use` | `@use "buttons.ql"` and `@use ... with { submit: ok }` | COVERED. Trait-then-own precedence. |
| `embed`/`endembed` | `@embed "card.ql" with { } { @block body { @} @}` | COVERED. |
| `apply`/`endapply` | `@apply \| trim \| upper { ...body... @}` | COVERED. Same pipe syntax as expressions. |
| `with`/`endwith` | `@with { x: 1 } { @}`, `@with { x: 1 } only { @}` | COVERED. |
| `do` | `@do E` | COVERED. |
| `flush` | `@flush` | COVERED (parity no-op for a string sink). |
| `deprecated` | `@deprecated "message" [since "2.0"]` | COVERED. |
| `guard`/`else` | `@guard filter("markdown") { } @else { @}` | COVERED. Dead branch parsed but not validated. |
| `types` | `@types { x: string, n: int @}` | EXCEEDED. First-class checker input, actually enforced where present. |
| `autoescape`/`endautoescape` | `@escape html { @}` / `@escape off { @}` | COVERED. Default strategy is `off`/raw, not `html`. |
| `sandbox`/`endsandbox` | `@sandbox { @}`, or per-include `sandboxed: true` | COVERED. |
| `verbatim`/`endverbatim` | `@verbatim { }` and fenced `@verbatim ~~~TAG ... ~~~TAG` | EXCEEDED. Adds the heredoc-style FENCED form. |
| `{% line N %}` | `@line 42` | COVERED. |
| `cache`/`endcache` | `@cache key="h" ttl=3600 tags=["a"] { @}` | COVERED (optional extension). |

### 1.2 Operators (O1-O44)

All 44 operators are COVERED. The notable mappings and their status:

| Twig | Quill | Status |
|------|-------|--------|
| `not`, `...`, unary `-`/`+` | same | COVERED. |
| `?:` Elvis, `??` null-coalesce | `?:` (truthiness), `??` (definedness) | COVERED. |
| `or`/`xor`/`and` | `or`/`||`, `xor`, `and`/`&&` | COVERED. |
| `b-or`/`b-xor`/`b-and` | `b_or` (`|||`) / `b_xor` (`^`) / `b_and` (`&`) | COVERED. Renamed because `|` is the filter pipe; integer-only. |
| `==`/`!=` | `==`/`!=` typed equality | COVERED. Divergence: `1 == "1"` is false. |
| `<=>`, `<`/`>`/`<=`/`>=` | same | COVERED. Divergence: ordering only in the number tower or between two strings. |
| `in`/`not in` | same | COVERED. Divergence: membership uses typed `==`, so `"1" in [1]` is false. |
| `matches` | `matches` (RE2) | COVERED. Divergence: Go RE2; PCRE backreferences/lookaround rejected. |
| `starts with`/`ends with`, `has some`/`has every` | same | COVERED. |
| `===`/`!==` | alias of `==`/`!=`; raw identity is `same(a,b)` / `is same as` | FOLDED. No capability lost. |
| `..` range | `..` | COVERED. |
| `+`/`-` numeric | same | COVERED. Divergence: `"3" + 4` is a type error; `+` is never array union. |
| `~` concat | `~` | COVERED. Distinct from `+`. |
| `*`/`/`/`//`/`%` | same | COVERED. |
| `**` power | `**` right-assoc | COVERED. Divergence: the `-1 ** 0 == -1` leak is removed. |
| ternary, filter `\|`, call `( )`, `.`, `?.`, `[ ]`, `=>`, `=` destructuring | same | COVERED. `.` access uses kind dispatch (divergence); `=` count mismatch is an error (divergence). |

### 1.3 Filters, functions, tests

All 44 filters, 19 functions (including the host-registration mechanism), and 15 tests are
COVERED, with the catalogue and signatures in `03-stdlib.md`. The renames and divergences:

- Filters: `format` uses Go `fmt` verbs, `json` uses Go `encoding/json`, `date` uses Go layouts,
  `default` triggers on undefined-or-null (not a bespoke emptiness rule). Twig's
  `trim`/`ltrim`/`rtrim` fold into one `trim(side, mask)`. All COVERED or FOLDED.
- Functions: `dump` uses a Go-native format; `template_from_string` is gated behind host opt-in.
- Tests: two-word names (`divisible by`, `same as`) are accepted aliases of the canonical
  underscore spellings; `is empty` is length-0-or-`Null` (so `0`/`"0"` are non-empty);
  `is same as` subsumes `===`.

### 1.4 Lexical, escaping, sandbox, engine

- Lexical (LX1-LX20): all COVERED, with the postfix `if` (LX6) EXCEEDED and the verbatim region
  EXCEEDED by the fenced form. Delimiters are fixed (a documented divergence from Twig's
  configurability). The binding source-emission contract (LX15) is discharged in
  `01-language-reference.md` Sections 1.1-1.3, where the `@`-sigil explicit-close mode is the
  default for source emission so a bare `{` or `}` is always literal.
- Escaping (E1-E16): all six strategies COVERED; the default is `off` for source emission (a
  documented, binding divergence). Safeness machinery reused.
- Sandbox (B1-B17): all COVERED; method/property matching uses a host type-graph instead of PHP
  reflection, with uniform allowlisting and no grandfathered tags.
- Engine/loader/extension (X1-X15): all reused from the port and recorded as parity requirements
  -- loaders, candidate-list resolution, missing-template tolerance, compile cache, structured
  error model, host registration of filters/functions/tests/strategies/policies/constants/enums,
  charset config, the strict-variables switch, and the deterministic RNG hook.

--------------------------------------------------------------------------------

## 2. The Go-native delta -- what Quill adds beyond Twig

1. **Gradual static typing** (the headline addition). Optional annotations on variables, macro
   params and returns, block inputs, `set`/`for` targets, and arrows, plus a first-class `types`
   block, checked by a static pass that catches undefined reads, cross-kind operations,
   non-iterable loops, array-render, and arity/type mismatches before any byte is emitted. Untyped
   templates are unchanged. (`04-types-and-semantics.md` Section 1.)

2. **The postfix conditional on output.** `{{ expr if cond }}` (with optional `else` and a
   symmetric `unless`), sugar over the ternary, shown in the anchor. Twig has no postfix `if` on
   prints. (`01-language-reference.md` Section 2.3.)

3. **Source-emission features.** The `@`-sigil explicit-close statement mode as the default for
   `.ql` templates -- with bare `{` and `}` always literal, so the pervasive literal lone-`}`
   lines (the majority at column 0) emit with zero escaping -- the two-character interpolation/comment sigil
   rule, the heredoc-style fenced `verbatim`, the `pragma bare` (alias `pragma sigil off`) opt-in
   keyword-led mode for markup templates, byte-exact whitespace control, and -- most importantly --
   escaping OFF by default. These exist because Quill emits program source, not markup.
   (`01-language-reference.md` Section 1; `04-types-and-semantics.md` Section 8.)

4. **Expression ergonomics.** Arrow functions composing with pipe filters and the spaceship
   comparator into a Unix-style pipeline, null-safe `?.`/`?[]`, the `has some`/`has every`
   quantifiers, and destructuring assignment -- all reachable from the parity primitives but more
   ergonomic than Twig's surface.

5. **Always-defined loop metadata.** Every `loop` field (`last`, `length`, `revindex`,
   `revindex0`) is always present, even for an iterator drained to an `*Array` before looping; Twig
   leaves these undefined for un-countable generators.

6. **Diagnostics and determinism.** Node-carried source positions with no debug-info mapping table,
   deterministic ordered-key JSON, and a seedable RNG -- all serving a batch-generator workflow.

--------------------------------------------------------------------------------

## 3. The deliberate semantic divergences

Each is a PHP accident replaced by a clean Go-native rule, justified by the source-emitting axiom.
None reduces a feature-level capability. The full rules are in `04-types-and-semantics.md`; the
list:

1. One typed equality instead of PHP loose `==` plus a separate `compare` for `in`; `in`/`==`/`same as`
   share it.
2. Clean truthiness -- drop `"0"`-falsy and objects-always-truthy.
3. Clean string coercion -- `true`->`"true"`, `false`->`"false"`, an `*Array` render is an error
   (not `"Array"`).
4. Clean kind-dispatched attribute resolution -- drop the array-key/property/DateTime/const/get-is-has
   cascade.
5. No power/unary-minus precedence leak -- consistent AST-driven precedence.
6. Clean key model -- drop numeric-string-key collision (tightened) and bool/float/null subscript
   coercion (now an error).
7. Sandbox host type-graph instead of PHP reflection; uniform allowlisting, no grandfathered
   tags/functions.
8. Go-native formats for `json`, `format`, and `date`.
9. Go RE2 regex instead of PCRE, with documented feature loss.
10. Default escaping OFF for source emission.
11. Strict-by-default undefined handling (the headline divergence), with a `lenient` mode for
    migration.
12. Non-iterable `for` is a runtime error, not a silent empty loop, with `for x in (coll ?? [])` as
    the explicit opt-in.

--------------------------------------------------------------------------------

## 4. Migration assessment

A typical source-generation template set leans on a small set of statement families
-- `if/elseif/else`, `for`, `set`/capture, and `include` -- plus pipe filters (such as a `tab`
indent helper and `raw`), the
`{{"\n"}}` literal-newline idiom, `#{}` string interpolation, ternary, `matches` in a few, and a
handful of host functions. Such corpora often make no use of
`extends`, `block`, `macro`, `import`, `from`, `use`, `embed`, `apply`, `verbatim`, or `autoescape`.
This makes migration unusually tractable and concentrates the hard cases in a small,
enumerable set. A mechanical transpiler handles roughly 90-95% of template lines with no human
input. Crucially, the brace-dense Java/C/etc. bodies a source generator emits -- including the
pervasive literal
lone-`}` lines, the majority at column 0 closing an emitted class, method, or
function -- need NOTHING under the `@`-default: a bare `}` in template TEXT is literal output, so
those sites transliterate verbatim. The migration is therefore mechanical at exactly the points a
brace-counting scheme would have made hard.

### 4.1 The fully automatable rewrites (the ~90%)

Each is a deterministic, local transformation applied with no judgment:

| Twig | Quill (`@`-default) |
|------|-------|
| `{% if E %} ... {% endif %}` | `@if E { ... @}` |
| `{% elseif E %}` / `{% else %}` | `@elseif E {` / `@else {` |
| `{% for X in Y %} ... {% endfor %}` | `@for X in Y { ... @}` |
| `{% set X = E %}` | `@set X = E` |
| `{% set X %} ... {% endset %}` | `@set X = capture { ... @}` |
| `{% include P %}` / `... with M` | `@include P` / `@include P with M` (map keys de-quoted) |
| `{{ E }}` | `{{ E }}` |
| `{{ E \| raw }}` | `{{ E }}` (the `\| raw` is dropped; default is raw) |
| `{{ N \| tab() }}` | `{{ N \| tab }}` (empty parens dropped, optional) |
| `{{"\n"}}` | `{{ "\n" }}` (whitespace normalized inside the sigil) |
| `#{expr}` in strings | `#{expr}` (unchanged) |
| a literal `}` line in an emitted body | the same `}` line (literal TEXT under the `@`-default; no escaping) |
| `==`, `!=`, `and`, `or`, `not`, `matches`, ternary | unchanged |
| `getJavaListDataType(...)`, `subtractOne(...)`, `ucfirst` | unchanged |

The expression sub-language such templates use is a subset of Quill's expression grammar with
identical surface syntax, so the expression transpiler is mostly an identity function. The only
expression-level rewrites are quote-style and filter-pipe normalization, both optional. The
statement-level rewrites add the leading `@` and the `@}` close; emitted brace-dense body text,
including lone-`}` lines, passes through untouched because it is literal output, not Quill
structure.

### 4.2 The semantic decisions a human reviews (the ~5-10%)

These require knowing INTENT, so the transpiler surfaces them rather than guessing:

- **Non-iterable `for`** (the headline divergence). Twig's `{% for x in Y %}` over null/scalar is a
  silent empty loop; Quill's is a runtime error. The transpiler emits the conservative
  `@for x in (Y ?? []) { ... @}`, which reproduces the empty-on-absent behavior exactly, and flags
  it for a human to decide per site whether to tighten to the strict form.
- **`for`/`else` over a possibly-null value** (a sharper case of the above). Twig fires the
  loop `else` when `Y` is null, because Twig coerces null to `[]` before counting iterations.
  Under Quill a bare `@for x in Y { @else { @}` over null ERRORS before the `else` can fire
  (`01-language-reference.md` Section 4.2). To preserve the else-on-null trigger, a Twig
  `{% for x in Y %}...{% else %}...{% endfor %}` over a possibly-null `Y` MUST transpile to
  `@for x in (Y ?? []) { ... @else { ... @}`: the `?? []` makes null an empty collection, which
  the `else` then catches as zero iterations. The transpiler emits this form and flags it,
  because the `?? []` is load-bearing for the `else` to fire on null exactly as Twig did.
- **Strict-undefined reads.** A read that relied on Twig's silent-null would newly error. The
  transpiler cannot prove which reads are intentional; it can transpile under `lenient` mode (exact
  Twig behavior) and flag a recommended incremental tightening.
- **Inline `{% if %}X{% endif %}` versus postfix `if`.** The transpiler keeps block `if` by default
  and collapses to postfix `{{ X if C }}` only on an opt-in, because collapsing changes the
  whitespace shape. Never collapsing is always correct.
- **`| raw` that would be load-bearing under a future `escape` region.** Dropping `| raw` is safe
  under the off default; the transpiler records every dropped `| raw` in a manifest so a template
  later switched to an `escape` region can restore the ones it needs.

### 4.3 What needs a human (the rare residue)

- PCRE-only `matches` patterns (backreferences, lookaround, possessive quantifiers) -- rejected by
  RE2 and flagged for a rewrite.
- Templates that relied on a removed PHP accident (`"0"`-falsy, loose `==`, `array`->`"Array"`
  render, silent int-overflow-to-float). The transpiler flags uses of `format`, `date`,
  `number_format`, and any `+` whose operands it cannot prove numeric.
- Exotic multi-target `set` overloads (typically unused in source-generation templates).

### 4.4 Transpiler architecture

The transpiler is a source-to-source compiler, NOT a regex rewriter -- the literal-brace
distinction makes regex rewriting unsafe. It reuses the faithful Twig port's correct Twig
lexer/parser to read the source into a Twig AST, maps the AST node-by-node to Quill constructs
(`IfNode` -> `@if { @}`, `ForNode` -> `@for { @}`, `SetNode` -> `@set`/`capture`, `PrintNode` ->
`{{ }}` with `| raw` dropped), pretty-prints `.ql` text in the `@`-default (statement heads led by
`@`, blocks closed by `@}`, `{{ }}` interpolation, normalized whitespace), and collects every
Section 4.2/4.3 decision into a `{# REVIEW: ... #}` comment and a manifest that drives the human
review queue. Literal body text -- including lone-`}` lines -- is copied through as
TEXT, needing no escaping under the `@`-default (the transpiler would auto-escape such a `}` only
when targeting `pragma bare` mode, which it does not emit for source templates). Path and extension
rewriting is a single mechanical decision (rename `.twig` to `.ql`, or transpile contents in place
keeping the name).

### 4.5 Side-by-side examples

**A header (pure passthrough).** A Twig file with no code -- only literal source -- is emitted
byte-for-byte by Quill, proving the baseline: a Quill file with no sigils is a verbatim copy.

```
// Twig and Quill, identical:
import java.io.*;
```

**A loop emitting a class body.** The Twig `{% for %}`/`{% endfor %}` envelope collapses to an
`@`-led block, and the per-interpolation `| raw` epidemic disappears:

```
// Twig:
{% for fn in FUNCTION_DECLARATIONS %}
{{ 1 | tab | raw }}public {{ fn.returnType | raw }} {{ fn.name | raw }}() {
{% endfor %}

// Quill (idiomatic, @-default):
@for fn in (FUNCTION_DECLARATIONS ?? []) {
{{ 1 | tab }}public {{ fn.returnType }} {{ fn.name }}() {
@}
```

The Quill form is shorter, drops the `| raw` noise (escaping is off by default), and makes the
"empty is fine" intent explicit with `?? []`. The literal `()` and the opening `{` after the
interpolations pass through TEXT untouched because none opens a Quill sigil; the loop close is the
explicit `@}`, never a bare `}`. This is the load-bearing case for the `@`-default: an emitted
body whose own lines end in `{` and `}` carries no risk of colliding with block structure.

**A class with a column-0 closing brace.** The commonest brace-dense shape in generated source is an
emitted class or method whose last line is a lone `}` at column 0 (a tail template closing
`main`, for instance). Under the `@`-default that line is plain literal output:

```
// Twig (a tail template, schematically):
    }
}

// Quill (@-default) -- both lines are literal TEXT, no escaping:
    }
}
```

Neither `}` line is a Quill close; only `@}` closes a Quill block. Were the same template written
under `pragma bare`, each lone `}` line would instead need a leading-pipe marker (`| }`), a
`verbatim` region, or interpolation -- which is exactly why bare mode is reserved for markup
templates and the `@`-default carries the source-generation corpus.

**Byte-correct brace-dense layout under the newline-eating rule.** A loop whose emitted body's
last line ends in a literal `}` hits the close-newline-eating asymmetry
(`01-language-reference.md` Section 1.4): the loop-close `@}` eats the newline the emitted code
needs after its own `}`, fusing the last body line with the following line. The three correct
idioms produce identical, byte-exact output:

```
// (a) explicit trailing newline -- the common idiom:
@for fn in (FNS ?? []) {
{{ 1 | tab }}{ return {{ fn.body }}; }{{ "\n" }}
@}

// (b) no-trim close `@}+` -- preserves the trailing newline at the close:
@for fn in (FNS ?? []) {
{{ 1 | tab }}{ return {{ fn.body }}; }
@}+

// (c) per-template pragma at the top -- disables close-newline-eating file-wide:
pragma keep-close-newline
@for fn in (FNS ?? []) {
{{ 1 | tab }}{ return {{ fn.body }}; }
@}
```

All three emit one `    { return ...; }` line per `fn` with the literal newline after each
intact and no fusion. Note the body's own `{` and `}` are literal TEXT under the `@`-default,
needing no escaping; only the loop's `@}` is structural. A source-emitting template that never
relies on list-style line collapsing SHOULD set `pragma keep-close-newline` so brace-dense output
is byte-correct by default; `@}+` is the targeted per-block tool, and `{{ "\n" }}` is the explicit
per-line tool that source-emission templates commonly use (a `}{{"\n"}}` tail line).

--------------------------------------------------------------------------------

## 5. Summary

- All 246 catalogued Twig capabilities map to a Quill spelling; zero are dropped. Two features
  (postfix `if`, gradual typing) and the always-defined loop fields EXCEED the floor.
- The source-emission story holds on real code-emitting templates: under the `@`-default a bare
  `{` or `}` is always literal -- the pervasive lone-`}` lines (the majority at column 0) emit with
  zero escaping -- only the `{{`-adjacency interpolation collision remains, which is rare and has three
  explicit escapes, and escaping off by default removes the `| raw` epidemic seen in markup-oriented ports.
- A typical template set migrates with high automation (~90% mechanical), the manual residue concentrates in
  the strict-by-default and non-iterable-`for` divergences, and the transpiler surfaces every such
  decision rather than guessing.
