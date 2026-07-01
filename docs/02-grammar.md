# Quill -- The Consolidated Formal Grammar

This is the complete, single, internally-consistent EBNF grammar of Quill. It is the
authoritative grammar to parse, highlight, or reason about Quill. It conforms to the prose in
`01-language-reference.md`: the expression ladder is the seventeen-level ladder of Section 3.1,
the statement set is the closed keyword set of Section 5.1, the lexical model is the two-mode
TEXT/CODE machine of Section 1, and the type-annotation grammar is the gradual layer of
`04-types-and-semantics.md` Section 3.

The grammar is presented in four layers, mirroring how the front end consumes a file: the
lexical grammar (Section 2), the structural grammar (Section 3), the expression grammar
(Section 4), and the type-annotation grammar (Section 5). Section 6 is the ambiguity-resolution
catalogue.

**Notation.** `=` defines a rule; `.` ends it; `|` alternates; `[ ]` is optional; `{ }` is
zero-or-more; `( )` groups; double-quoted `"..."` is a terminal literal (the literal `|`
operator terminal is written `"\|"` to distinguish it from EBNF alternation). Lexical terminals
produced by the scanner are UPPER_CASE (`NAME`, `STRING`, `INT`, `FLOAT`, `TEXT_RUN`, `NL`). A
production the scanner -- not the parser -- recognizes is marked `(* lexical *)`.

--------------------------------------------------------------------------------

## 1. Two grammars, one design

Quill has two grammars because the input is two languages interleaved: literal TEXT (emitted
verbatim) and Quill CODE (parsed). The scanner runs the lexical grammar to split bytes into
TEXT spans and CODE tokens; the parser runs the structural and expression grammars over the
CODE tokens. The single boundary rule -- a bare `{`/`}` is never a delimiter; only `{{`, `{#`,
an `@`-sigil statement lead, and `verbatim` open CODE -- is what makes the split decidable with
zero heuristic lookahead. Sections 2 and 6 make that rule formal.

The default statement form is the **explicit-close `@`-sigil mode**: a statement head is led by
an `@` immediately before its keyword (`@for`, `@if`, `@elseif`, `@else`, `@block`, `@macro`,
`@set`, `@extends`, `@include`, `@import`, `@from`, `@use`, `@embed`, `@with`, `@apply`, `@do`,
`@flush`, `@deprecated`, `@guard`, `@types`, `@escape`, `@sandbox`, `@verbatim`, `@line`,
`@cache`, `@capture`) and a block is closed by the explicit `@}` token. Under this default a bare
`{` or `}` anywhere in template TEXT -- including a lone `}` line at column 0 closing an emitted
class, method, or function -- is UNCONDITIONALLY literal output: no escaping, no grammar-shape
rejection, no lone-`}` collision, no line-leading-keyword diagnostic. This makes the brace-dense
source-emission case (a source-code generator emits program source code: literal lone-`}` lines
are pervasive, the majority at column 0) correct by default at the cost of one `@`
per statement. Interpolation `{{ }}`, comments `{# #}`, and string interpolation `#{ }` are
unchanged.

The originally-approved **bare keyword-led mode** -- no `@`, with a lone `}` line closing the
innermost block -- remains valid Quill as an explicit per-template opt-in (`pragma bare`, also
spelled `pragma sigil off`). It suits markup and non-source templates where brace collisions are
rare. Under bare mode the lone-`}` escapes of Section 6 (leading-pipe text marker, `verbatim`,
interpolation) apply to any literal `}` line; under the `@`-default they are needed only for
edge cases, never for an ordinary emitted brace.

--------------------------------------------------------------------------------

## 2. The lexical grammar

```
(* A source file is an alternation of TEXT spans and CODE constructs. *)
SourceFile   = { TextSpan | Interp | Comment | Statement | Verbatim } .  (* lexical-driven *)

(* TEXT: any run of bytes that does not open a CODE construct. *)
TextSpan     = TEXT_RUN .                                  (* lexical *)
(* TEXT_RUN is the maximal run of bytes in TEXT mode in which:
     - no '{' is immediately followed by '{' or '#'   (the sigil predicate)
     - under the @-default, the line is not an @-sigil statement head;
       under bare mode (pragma bare), the line is not a leading-keyword head
     - '\{' '\}' '\\' are consumed as the literal '{' '}' '\'
   A lone '{' or '}' is an ordinary TEXT byte. Under the @-default a lone '}'
   line is ALSO ordinary TEXT (only @} closes a block); under bare mode a lone
   '}' line closes the innermost block (R4a). *)

Sigil        = "{{" | "{#" .                               (* lexical, atSigil predicate *)
(* atSigil(src,i) := src[i]=='{' AND i+1<len AND src[i+1] IN {'{','#'} *)
(* StmtLead opens a statement: '@'+keyword under the @-default; a line-leading
   keyword under bare mode. BlockClose is '@}' under the @-default; a lone '}'
   line under bare mode. *)

TrimL        = "-" | "~" .         (* hard trim / line trim, opening side *)
TrimR        = "-" | "~" | "+" .   (* hard trim / line trim / keep-newline, closing side *)

Interp       = "{{" [TrimL] Expr [ PostfixCond ] [TrimR] "}}" .
PostfixCond  = ("if" | "unless") Expr [ "else" Expr ] .
Comment      = "{#" { ANY_BYTE } "#}" .                    (* lexical; eats one trailing NL *)

Verbatim     = "verbatim" "{" RawBody "}"                  (* brace-balanced, never scanned *)
             | "verbatim" FENCE RawLines FENCE .           (* heredoc form *)
RawBody      = (* bytes, inner { } tracked by a raw-brace depth counter *) .
FENCE        = (* an author-chosen token, e.g. ~~~JAVA, on its own line *) .

(* String, number, identifier, and operator tokens, scanned only inside CODE. *)
STRING       = "'" { SBYTE } "'"                           (* single: no interpolation *)
             | "\"" { DBYTE | "#{" Expr "}" } "\""         (* double: interpolation *)
             | "`" { RBYTE } "`" .                         (* backtick: raw, no escapes *)
INT          = DIGITS | "0x" HEXD | "0b" BIND | "0o" OCTD .
FLOAT        = DIGITS "." DIGITS [ EXP ] | DIGITS EXP .
DIGITS       = DIGIT { [ "_" ] DIGIT } .                   (* digit-group separators *)
NAME         = (LETTER | "_") { LETTER | DIGIT | "_" } .
NL           = "\n" .                                      (* after CR/CRLF normalization *)
```

The interpolation closer `}}` is recognized only at brace-depth zero relative to its opener,
so a mapping literal's `}` inside `{{ ... }}` does not close it (Section 6, rule R3). The
comment closer `#}` and a statement's closing `}` each eat one immediately-following newline;
`}}` eats none (Section 6, rule R5).

--------------------------------------------------------------------------------

## 3. The structural grammar

The productions below are written in the `@`-default spelling: a statement head carries a
leading `@` and a block body is closed by the explicit `@}` token (written `BLOCK_CLOSE`). The
opening `{` after a head is a block-open marker, not a literal brace, and it is the only `{`
the parser consumes structurally. Under the bare opt-in (`pragma bare`), the leading `@` is
absent and `BLOCK_CLOSE` is a lone `}` line; the productions are otherwise identical. Throughout
this section a keyword shown as `"@if"` reads as the keyword `if` led by the `@` sigil under the
default and as the bare keyword `if` under `pragma bare`.

```
Template = { TopItem } .
TopItem  = Extends | Block | Macro | Use | Import | From
         | Stmt | TextSpan | Interp | Comment | Verbatim .

(* The explicit block close. BLOCK_CLOSE is "@}" under the @-default and a lone
   "}" line under pragma bare. *)
BLOCK_CLOSE = "@}" .

(* Composition heads. *)
Extends  = "@extends" Expr NL .
Block    = "@block" NAME [ "(" [Params] ")" ] [ "->" Type ]
           ( "{" { Item } BLOCK_CLOSE | Expr NL ) .
Macro    = "@macro" NAME "(" [Params] ")" [ "->" Type ] "{" { Item } BLOCK_CLOSE .
Import   = "@import" ImportSrc "as" NAME NL .
From     = "@from" ImportSrc "import" ImportList NL .
ImportSrc = Expr | "_self" .              (* a path expression, or the current template *)
ImportList = NAME [ "as" NAME ] { "," NAME [ "as" NAME ] } .
Use      = "@use" Expr [ "with" Map ] NL .
Embed    = "@embed" Expr [ "with" Map ] [ "only" ] [ "ignore" "missing" ]
           "{" { Block } BLOCK_CLOSE .

(* Statements (the closed keyword set). *)
Stmt     = If | For | Set | Capture | With | Apply | Do | Flush
         | Deprecated | Guard | Types | Escape | Sandbox | Line | Cache
         | Include | Embed .
(* Capture is a set-tail form, not a free statement and not an Expr (R12). *)

If       = "@if" Expr "{" { Item }
           { "@elseif" Expr "{" { Item } }
           [ "@else" "{" { Item } ] BLOCK_CLOSE .
For      = "@for" Target [ "," Target ] "in" Expr "{" { Item }
           [ "@else" "{" { Item } ] BLOCK_CLOSE .
Target   = NAME [ ":" Type ] .
Set      = "@set" Target { "," Target } "=" Expr { "," Expr } NL .
Capture  = "@set" NAME [ ":" Type ] "=" "capture" "{" { Item } BLOCK_CLOSE .  (* set-tail block-capture form *)
With     = "@with" Map [ "only" ] "{" { Item } BLOCK_CLOSE .
Apply    = "@apply" { "\|" NAME [ "(" Args ")" ] } "{" { Item } BLOCK_CLOSE .
Do       = "@do" Expr NL .
Flush    = "@flush" NL .
Deprecated = "@deprecated" STRING [ "since" STRING ] NL .
Guard    = "@guard" CallableRef "{" { Item } [ "@else" "{" { Item } ] BLOCK_CLOSE .
CallableRef = ( "filter" | "function" | "test" ) "(" STRING ")" .
Types    = "@types" "{" { TypeDecl } BLOCK_CLOSE .
TypeDecl = NAME ":" Type [ "," ] .
Escape   = "@escape" ( NAME | "off" ) "{" { Item } BLOCK_CLOSE .
Sandbox  = "@sandbox" "{" { Item } BLOCK_CLOSE .
Line     = "@line" INT NL .
Cache    = "@cache" { NAME "=" Expr } "{" { Item } BLOCK_CLOSE .
Include  = "@include" Expr [ "with" Expr ] [ "only" ] [ "ignore" "missing" ] NL .

(* The body of a block: text, output, comment, or nested statement. *)
Item     = TextSpan | Interp | Comment | Stmt | Block | Macro | Verbatim .
```

The `@elseif`/`@else` continuation heads and the closing `BLOCK_CLOSE` make `If`, `For`, and
`Guard` close at one explicit `@}`; the intermediate `@elseif`/`@else` heads re-open the body
without closing it. The capture form is `@set X = capture { ... @}`, a dedicated set-tail
production (the `Capture` rule above), NOT a general expression: `capture` is reachable only
immediately after `@set NAME [: Type] =`, and its `{ ... @}` body is parsed at a statement
position so the multi-line body never terminates on an interior `NL`. `capture` is therefore
neither a free `Stmt` nor an `Expr` `Primary`; this removes the contradiction of treating it as
both. The `Block` shortcut value form `@block title "Default"` is the `Expr NL` alternative of
`Block`.

--------------------------------------------------------------------------------

## 4. The expression grammar

The productions encode the ladder of `01-language-reference.md` Section 3.1; a left-associative
operator at binding power `p` recurses on its right operand at `p+1`, a right-associative
operator at `p`. The implementation is a Pratt table; the two agree by construction.

```
Expr        = Assign .
Assign      = Ternary [ "=" Assign ] .                     (* right-assoc; LHS may be a target *)
Ternary     = Coalesce [ "?" Expr [ ":" Expr ] ] .
Coalesce    = Or { ("??" | "?:") Or } .
Or          = Xor { ("or" | "||") Xor } .
Xor         = And { "xor" And } .
And         = BitOr { ("and" | "&&") BitOr } .
BitOr       = BitXor { ("b_or" | "|||") BitXor } .
BitXor      = BitAnd { ("b_xor" | "^") BitAnd } .
BitAnd      = Cmp { ("b_and" | "&") Cmp } .
Cmp         = Range { CmpOp Range | TestApp } .
CmpOp       = "==" | "!=" | "<" | ">" | "<=" | ">=" | "<=>"
            | "in" | "not" "in" | "matches"
            | "starts" "with" | "ends" "with"
            | "has" "some" | "has" "every" .
TestApp     = ("is" | "is" "not") TestName [ TestArgs ] .
TestName    = NAME [ NAME ] .                              (* two-word tests, greedy (R7) *)
TestArgs    = Primary | "(" Args ")" .                     (* one-positional short form, or full Args *)
Range       = Concat { ".." Concat } .
Concat      = Add { "~" Add } .
Add         = Mul { ("+" | "-") Mul } .
Mul         = Power { ("*" | "/" | "//" | "%") Power } .
Power       = Unary [ "**" Power ] .                       (* right-assoc; RHS at prefix (R6) *)
Unary       = ("not" | "!" | "-" | "+" | "...") Unary | Postfix .
Postfix     = Primary { "." Name | "?." Name
                      | "[" Slice "]" | "?[" Expr "]"
                      | "(" Args ")"
                      | "\|" Name [ "(" Args ")" ] } .
Slice       = Expr | [Expr] ":" [Expr] .
Primary     = Literal | SpecialName | Name | "(" Expr ")" | Seq | Map | Arrow .
Name        = NAME .                                       (* word-operators are NAME here (R2) *)
SpecialName = "_self" | "_context" | "_charset" .          (* reserved, engine-resolved (R11) *)
Arrow       = ( NAME | "(" [ParamList] ")" ) "=>" Expr .
ParamList   = Param { "," Param } .
Param       = NAME [ ":" Type ] [ "=" Expr ] | "..." NAME .
Params      = ParamList .
Seq         = "[" [ Expr { "," Expr } [","] ] "]" .
Map         = "{" [ MapEntry { "," MapEntry } [","] ] "}" .
MapEntry    = Name | Name ":" Expr | "(" Expr ")" ":" Expr | "..." Expr .
Args        = [ Arg { "," Arg } ] .
Arg         = Name ":" Expr | "..." Expr | Expr .
Literal     = INT | FLOAT | STRING | "true" | "false" | "null" | "none" .
```

The assignment LHS, when `=` follows, is reinterpreted as a destructuring target:

```
Target_      = Name | Seq_ | Map_ .
Seq_         = "[" [ TgtSlot { "," TgtSlot } [ "," "..." Name ] ] "]" .
TgtSlot      = [ Target_ [ "?" ] ] .                       (* elided slots; optional slot *)
Map_         = "{" MapTgt { "," MapTgt } "}" .
MapTgt       = Name | Name ":" Name .                      (* {name} or {key: alias} *)
```

--------------------------------------------------------------------------------

## 5. The type-annotation grammar

```
Type     = UnionType .
UnionType = AtomType { "|" AtomType } [ "?" ] .            (* A | B, trailing ? is nullable *)
AtomType = "any" | "null" | "bool" | "int" | "float" | "string"
         | "list" "<" Type ">"
         | "map" "<" Type "," Type ">"
         | "Object" "<" STRING ">"                         (* host-registered named type *)
         | "(" [ TypeList ] ")" "=>" Type                  (* arrow/callable type *)
         | "(" Type ")" .
TypeList = Type { "," Type } .
```

Annotation sites: `types { ... }` declarations, `macro f(p: T = d) -> T`, `block b(in: T) -> T`,
`set x: T = e`, `for x: T in e`, and arrow params `(x: T) => e`. The `|` inside a `UnionType`
appears only in a type context (after `:`, `->`, or inside `< >`), where it can never be the
filter pipe (R8).

--------------------------------------------------------------------------------

## 6. Ambiguity-resolution catalogue

Every ambiguity an implementer hits, with the exact rule that resolves it.

**R1 -- TEXT vs CODE at a brace.** A `{` opens CODE only when the next byte is `{` or `#`
(the `atSigil` predicate). Otherwise it is a TEXT byte. Under the `@`-default, statement heads
are led by `@` and blocks close at `@}`, so neither a bare `{` nor a bare `}` -- including a
lone `}` line at column 0 -- is ever a delimiter; both are literal output with no escaping. This
is the whole literal-brace solution; it needs no lookahead beyond one byte.

**R2 -- word-operator vs identifier.** A word-operator spelling (`and`, `or`, `not`, `in`,
`is`, `matches`, `xor`, `starts`, `ends`, `has`) is lexed as a `NAME`. The parser reclassifies
it to an operator only in infix/prefix position. In primary position and immediately after `.`
or `|`, it stays a `NAME`, so `u.in`, `data | matches_count`, and a context variable named
`and` all resolve as identifiers.

**R3 -- interpolation close vs literal `}` inside CODE.** Inside `{{ ... }}` the lexer balances
`()`, `[]`, `{}`; the close `}}` is recognized only at brace-depth zero relative to the opener.
A mapping literal `{a: 1}` inside an interpolation is at depth 1 and does not close it. A bare
`}}` in TEXT with no open `{{` is two literal `}` bytes.

**R4 -- statement head vs literal output line.** Under the `@`-default, a line is a statement
only when its first non-whitespace token is `@` immediately followed by a keyword from the
closed set (Section 5.1 of `01-language-reference.md`) and a word boundary; an unprefixed line
beginning with a keyword spelling (a C `for (int i...)`, a Java `if (x) {`) is UNCONDITIONALLY
TEXT, because no `@` leads it. This is what makes brace-dense source emission correct by default:
emitted code that happens to start a line with `for`, `if`, `do`, or `set` needs no escaping.
Under the bare opt-in (`pragma bare`), a line is a statement when its first non-whitespace token
is the bare keyword followed by a word boundary AND the line parses as a complete statement head;
there a C `for (int i...)` falls back to TEXT only because `(` is not the required `Target`, and
the leading-pipe `| ` marker or a `verbatim` region forces TEXT for a line that would otherwise
parse as a head.

**R5 -- newline-eating asymmetry.** A block close (`@}` under the default, a lone `}` line under
bare mode) and a comment's `#}` consume one immediately-following newline; an interpolation's
`}}` consumes none. This is the default, overridden per-site by the `-`/`~` trim modifiers and
SUPPRESSED by the `+` keep modifier (`@}+`, `#}+`, R14) or by the per-template
`pragma keep-close-newline`. Because under the `@`-default an emitted literal `}` is plain TEXT
and never a close, the only newline-eating site is the block-structure close `@}`; where its
eaten newline would fuse the last emitted body line with the following line, `@}+` or the pragma
restores byte-exact layout (`01-language-reference.md` Section 1.4).

**R4a -- which close pops a block.** Under the `@`-default, the closer of a TEXT-bodied statement
is the explicit `@}` token (optionally with a trim/keep modifier). A line whose only
non-whitespace content is a bare `}` is ORDINARY TEXT and pops nothing; literal lone-`}` lines --
the column-0 closes of emitted classes, methods, and functions in generated source -- emit
with no escaping, no leading-pipe marker, and no diagnostic. Only `@}` closes, so a bare `}` can
never collide with block structure. An `@}` with an empty open-block stack, or an open block
unclosed at end of file, is a hard `unbalanced-block` error -- never silently absorbed
(`01-language-reference.md` Section 1.3).

Under the bare opt-in (`pragma bare`), the closer is instead a line whose only non-whitespace
content is `}` (optionally with a trim/keep modifier). Literal `{`/`}` in a TEXT body are not
brace-counted (only CODE balances braces, R3), so such a lone `}` line ALWAYS pops the innermost
open Quill block. In bare mode a literal lone-`}` line emitted inside a Quill block must be
disambiguated by the leading-pipe marker (`| }`), a `verbatim` region, or interpolation
(`{{ "}" }}`); indentation alone does not exempt it. Bare mode is therefore appropriate only for
markup and non-source templates where literal `}` lines are rare; brace-dense source emission
uses the `@`-default, where these escapes are unnecessary.

**R6 -- power vs unary minus.** `**` is right-associative and binds tighter than unary minus,
but the unary prefix wraps the power node by AST shape: `Unary -> ("-") Unary | Postfix` and
`Power -> Unary [ "**" Power ]` together yield `-1 ** 0 = -(1 ** 0) = -1` and
`(-1) ** 2 = 1` from one consistent rule.

**R7 -- two-word test names.** After `is`/`is not`, the parser greedily consumes up to two
`NAME` tokens to form the test name (`same as`, `divisible by`), then optionally an argument.
Single-token tests (`defined`, `even`) take one `NAME`. A following `(` begins a parenthesized
argument list rather than a third name word.

**R8 -- the pipe `|` vs union `|` vs bitwise-or.** The bare `|` is exclusively the filter pipe
in expression position. Bitwise OR is the word `b_or` (alias `|||`). Type union `|` appears
only in a type context (after `:`, `->`, or inside `< >`). The three uses never overlap because
each is confined to a distinct syntactic position.

**R9 -- arrow param list vs grouping.** `( ... )` is an arrow param list only when immediately
followed by `=>`; otherwise it is a grouped expression. The parser parses the parenthesized
form, then checks for `=>`: if present, it reinterprets the contents as `ParamList`; if not, it
is a grouped `Expr`. A single bare `NAME =>` is also an arrow.

**R10 -- assignment target vs expression.** The LHS of `=` is parsed as an `Expr`, then
reinterpreted as a `Target_` when `=` follows. A sequence literal `[a, b]` on the left of `=`
is a destructuring target; the same `[a, b]` without a following `=` is a sequence value.

**R11 -- special names vs context identifiers.** `_self`, `_context`, and `_charset` are
reserved `SpecialName` primaries, resolved by the engine and exempt from the strict-undefined
rule (`04-types-and-semantics.md` Section 6). They are recognized in primary position and as
an `ImportSrc`; a context variable of the same name is shadowed and unreachable as a bare
identifier. After `.` or `|` they are ordinary `NAME`s (so `obj._self` is a member read).

**R12 -- `capture` is a set-tail, not an expression.** `capture { ... @}` is grammatical ONLY
in the `Capture` production (`@set NAME [: Type] = capture { ... @}`). It is not a free `Stmt`
and not an `Expr` `Primary`, so a line statement's `NL` terminator can never fall inside its
body. A bare `capture { ... @}` outside a `@set` tail is a parse error.

**R13 -- the `matches` operand and the bare `/`.** `matches` takes an ordinary `Expr` whose
value is a string RE2 pattern; there is no regex-literal token. A `/` is always the division
or floor-division operator, never a pattern delimiter, so `a / b` and `a matches "x/y"` never
conflict. Inline flags ride inside the pattern string as RE2 `(?flags)`.

**R14 -- the `+` close modifier.** On a block close or `#}` only, a `+` (`@}+` under the default,
`}+` under bare mode, and `#}+`) suppresses the one-newline-eating of R5, preserving the
following newline. `+` is a close-side trim modifier exclusively; it is not an opening-side
modifier and is not the additive operator in this position (the additive `+` appears only inside
an `Expr`, never adjacent to a closing delimiter).

--------------------------------------------------------------------------------

## 7. The grammar in one block

The productions of Sections 2-5 compose into the single grammar above. The entry point is
`SourceFile`; `Template` is the top-level production the parser drives once the scanner has
classified spans. The two grammars meet at exactly one seam -- the `atSigil` predicate (R1) and
the statement-head test (R4: the `@`-sigil lead under the default, the leading-keyword test under
`pragma bare`) -- and nowhere else, which is why the whole language is decidable byte-by-byte
without heuristic lookahead.
