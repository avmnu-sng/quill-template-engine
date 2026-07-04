# Grammar

This is the complete, single, internally-consistent EBNF grammar of Quill. It is
the authoritative grammar to parse, highlight, or reason about Quill. It conforms
to the prose in the [Language Reference](language.md): the expression ladder is
the seventeen-level ladder, the statement set is the closed keyword set, the
lexical model is the two-mode TEXT/CODE machine, and the type-annotation grammar
is the gradual layer described in [Types](../types.md).

The grammar is presented in four layers, mirroring how the front end consumes a
file: the lexical grammar, the structural grammar, the expression grammar, and the
type-annotation grammar. The last section is the ambiguity-resolution catalogue.

**Notation.** `=` defines a rule; `.` ends it; `|` alternates; `[ ]` is optional;
`{ }` is zero-or-more; `( )` groups; double-quoted `"..."` is a terminal literal
(the literal `|` operator terminal is written `"\|"` to distinguish it from EBNF
alternation). Lexical terminals produced by the scanner are UPPER_CASE (`NAME`,
`STRING`, `INT`, `FLOAT`, `TEXT_RUN`, `NL`). A production the scanner -- not the
parser -- recognizes is marked `(* lexical *)`.

## Two grammars, one design

Quill has two grammars because the input is two languages interleaved: literal
TEXT (emitted verbatim) and Quill CODE (parsed). The scanner runs the lexical
grammar to split bytes into TEXT spans and CODE tokens; the parser runs the
structural and expression grammars over the CODE tokens. The single boundary rule
-- a bare `{`/`}` is never a delimiter; only `{{`, `{#`, an `@`-sigil statement
lead, and `verbatim` open CODE -- is what makes the split decidable with zero
heuristic lookahead.

The default statement form is the **explicit-close `@`-sigil mode**: a statement
head is led by an `@` immediately before its keyword (`@for`, `@if`, `@block`,
...) and a block is closed by the explicit `@}` token. Under this default a bare
`{` or `}` anywhere in template TEXT -- including a lone `}` line at column 0 -- is
unconditionally literal output: no escaping, no grammar-shape rejection, no lone-`}`
collision, no line-leading-keyword diagnostic. This makes brace-dense TEXT correct
by default at the cost of one `@` per statement. Interpolation `{{ }}`, comments
`{# #}`, and string interpolation `#{ }` are unchanged.

The **bare keyword-led mode** -- no `@`, with a lone `}` line closing the innermost
block -- remains valid Quill as an explicit per-template opt-in (`pragma bare`, also
spelled `pragma sigil off`). It suits markup and other templates where literal
braces are rare. Under bare mode the lone-`}` escapes below (leading-pipe text
marker, `verbatim`, interpolation) apply to any literal `}` line; under the
`@`-default they are needed only for edge cases.

## The lexical grammar

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
FENCE        = (* an author-chosen token, e.g. ~~~END, on its own line *) .

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

The interpolation closer `}}` is recognized only at brace-depth zero relative to
its opener, so a mapping literal's `}` inside `{{ ... }}` does not close it (R3).
The comment closer `#}` and a statement's closing `}` each eat one
immediately-following newline; `}}` eats none (R5).

## The structural grammar

The productions below are written in the `@`-default spelling: a statement head
carries a leading `@` and a block body is closed by the explicit `@}` token
(written `BLOCK_CLOSE`). The opening `{` after a head is a block-open marker, not a
literal brace, and it is the only `{` the parser consumes structurally. Under the
bare opt-in (`pragma bare`), the leading `@` is absent and `BLOCK_CLOSE` is a lone
`}` line; the productions are otherwise identical.

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
         | Log | TabBlock | Provide | Yield | CallBlock
         | Include | Embed .
(* Capture is a set-tail form, not a free statement and not an Expr (R12). *)

If       = "@if" Expr "{" { Item }
           { "@elseif" Expr "{" { Item } }
           [ "@else" "{" { Item } ] BLOCK_CLOSE .
For      = "@for" Target [ "," Target ] "in" Expr [ "recursive" ] [ "if" Expr ]
           "{" { Item } [ "@else" "{" { Item } ] BLOCK_CLOSE .
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
Log      = "@log" Expr NL .
TabBlock = "@tab" "(" Expr ")" "{" { Item } BLOCK_CLOSE .
Provide  = "@provide" NAME "{" { Item } BLOCK_CLOSE .
Yield    = "@yield" NAME NL .
CallBlock = "@call" [ "(" [Params] ")" ] NAME "(" [Args] ")" "{" { Item } BLOCK_CLOSE .
Include  = "@include" Expr [ "with" Expr ] [ "only" ] [ "ignore" "missing" ] NL .

(* The body of a block: text, output, comment, or nested statement. *)
Item     = TextSpan | Interp | Comment | Stmt | Block | Macro | Verbatim .
```

The `@elseif`/`@else` continuation heads and the closing `BLOCK_CLOSE` make `If`,
`For`, and `Guard` close at one explicit `@}`; the intermediate heads re-open the
body without closing it. The capture form is `@set X = capture { ... @}`, a
dedicated set-tail production (not a general expression); `capture` is reachable
only immediately after `@set NAME [: Type] =`. The `Block` shortcut value form
`@block title "Default"` is the `Expr NL` alternative of `Block`.

## The expression grammar

The productions encode the seventeen-level ladder ([Expressions](../guide/expressions.md));
a left-associative operator at binding power `p` recurses on its right operand at
`p+1`, a right-associative operator at `p`. The implementation is a Pratt table;
the two agree by construction.

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
ParamList   = Param { "," Param } .                        (* arrow params are positional-only *)
Param       = NAME [ ":" Type ] [ "=" Expr ] | "..." NAME .
Params      = MacroParamList .                             (* macro/block/call-block heads *)
MacroParamList = [ Param { "," Param } ] [ "," ] [ "..." NAME [ "," ] ] [ "**" NAME ] .
                                                           (* ordered tail: optional "..." NAME then optional "**" NAME, each last (R13) *)
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

## The type-annotation grammar

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

Annotation sites: `types { ... }` declarations, `macro f(p: T = d) -> T`,
`block b(in: T) -> T`, `set x: T = e`, `for x: T in e`, and arrow params
`(x: T) => e`. The `|` inside a `UnionType` appears only in a type context (after
`:`, `->`, or inside `< >`), where it can never be the filter pipe (R8).

## Ambiguity-resolution catalogue

Every ambiguity an implementer hits, with the exact rule that resolves it.

**R1 -- TEXT vs CODE at a brace.** A `{` opens CODE only when the next byte is `{`
or `#` (the `atSigil` predicate). Otherwise it is a TEXT byte. Under the
`@`-default, statement heads are led by `@` and blocks close at `@}`, so neither a
bare `{` nor a bare `}` -- including a lone `}` line at column 0 -- is ever a
delimiter; both are literal output with no escaping. This needs no lookahead beyond
one byte.

**R2 -- word-operator vs identifier.** A word-operator spelling (`and`, `or`,
`not`, `in`, `is`, `matches`, `xor`, `starts`, `ends`, `has`) is lexed as a
`NAME`. The parser reclassifies it to an operator only in infix/prefix position.
In primary position and immediately after `.` or `|`, it stays a `NAME`, so
`u.in`, `data | matches_count`, and a context variable named `and` all resolve as
identifiers.

**R3 -- interpolation close vs literal `}` inside CODE.** Inside `{{ ... }}` the
lexer balances `()`, `[]`, `{}`; the close `}}` is recognized only at brace-depth
zero relative to the opener. A mapping literal `{a: 1}` inside an interpolation is
at depth 1 and does not close it. A bare `}}` in TEXT with no open `{{` is two
literal `}` bytes.

**R4 -- statement head vs literal output line.** Under the `@`-default, a line is a
statement only when its first non-whitespace token is `@` immediately followed by a
keyword from the closed set and a word boundary; an unprefixed line beginning with
a keyword spelling (a C `for (int i...)`, a Java `if (x) {`) is unconditionally
TEXT, because no `@` leads it. Under the bare opt-in (`pragma bare`), a line is a
statement when its first non-whitespace token is the bare keyword followed by a
word boundary AND the line parses as a complete statement head; there a C
`for (int i...)` falls back to TEXT only because `(` is not the required `Target`,
and the leading-pipe `| ` marker or a `verbatim` region forces TEXT for a line that
would otherwise parse as a head.

**R5 -- newline-eating asymmetry.** A block close (`@}` under the default, a lone
`}` line under bare mode) and a comment's `#}` consume one immediately-following
newline; an interpolation's `}}` consumes none. This is the default, overridden
per-site by the `-`/`~` trim modifiers and suppressed by the `+` keep modifier
(`@}+`, `#}+`, R14) or by a per-template `pragma keep-close-newline`.

**R4a -- which close pops a block.** Under the `@`-default, the closer of a
TEXT-bodied statement is the explicit `@}` token (optionally with a trim/keep
modifier). A line whose only non-whitespace content is a bare `}` is ordinary TEXT
and pops nothing. Only `@}` closes, so a bare `}` can never collide with block
structure. An `@}` with an empty open-block stack, or an open block unclosed at end
of file, is a hard `unbalanced-block` error -- never silently absorbed.

Under the bare opt-in (`pragma bare`), the closer is instead a line whose only
non-whitespace content is `}`. Literal `{`/`}` in a TEXT body are not brace-counted
(only CODE balances braces, R3), so such a lone `}` line always pops the innermost
open Quill block. In bare mode a literal lone-`}` line emitted inside a Quill block
must be disambiguated by the leading-pipe marker (`| }`), a `verbatim` region, or
interpolation (`{{ "}" }}`); indentation alone does not exempt it.

**R6 -- power vs unary minus.** `**` is right-associative and binds tighter than
unary minus, but the unary prefix wraps the power node by AST shape:
`Unary -> ("-") Unary | Postfix` and `Power -> Unary [ "**" Power ]` together yield
`-1 ** 0 = -(1 ** 0) = -1` and `(-1) ** 2 = 1` from one consistent rule.

**R7 -- two-word test names.** After `is`/`is not`, the parser greedily consumes up
to two `NAME` tokens to form the test name (`same as`, `divisible by`), then
optionally an argument. Single-token tests take one `NAME`. A following `(` begins
a parenthesized argument list rather than a third name word.

**R8 -- the pipe `|` vs union `|` vs bitwise-or.** The bare `|` is exclusively the
filter pipe in expression position. Bitwise OR is the word `b_or` (alias `|||`).
Type union `|` appears only in a type context. The three uses never overlap because
each is confined to a distinct syntactic position.

**R9 -- arrow param list vs grouping.** `( ... )` is an arrow param list only when
immediately followed by `=>`; otherwise it is a grouped expression. A single bare
`NAME =>` is also an arrow. An arrow `ParamList` is positional-only; a `** NAME`
kwargs tail is a `MacroParamList` feature and is rejected on an arrow. Within a
`MacroParamList` the two tail captures obey a fixed terminal order: an optional
`... NAME` positional variadic, then an optional `** NAME` kwargs, each last.

**R10 -- assignment target vs expression.** The LHS of `=` is parsed as an `Expr`,
then reinterpreted as a `Target_` when `=` follows. A sequence literal `[a, b]` on
the left of `=` is a destructuring target; the same `[a, b]` without a following
`=` is a sequence value.

**R11 -- special names vs context identifiers.** `_self`, `_context`, and
`_charset` are reserved `SpecialName` primaries, resolved by the engine and exempt
from the strict-undefined rule. They are recognized in primary position and as an
`ImportSrc`; a context variable of the same name is shadowed. After `.` or `|` they
are ordinary `NAME`s.

**R12 -- `capture` is a set-tail, not an expression.** `capture { ... @}` is
grammatical only in the `Capture` production. It is not a free `Stmt` and not an
`Expr` `Primary`, so a line statement's `NL` terminator can never fall inside its
body. A bare `capture { ... @}` outside a `@set` tail is a parse error.

**R13 -- the `matches` operand and the bare `/`.** `matches` takes an ordinary
`Expr` whose value is a string RE2 pattern; there is no regex-literal token. A `/`
is always the division or floor-division operator, never a pattern delimiter.
Inline flags ride inside the pattern string as RE2 `(?flags)`.

**R14 -- the `+` close modifier.** On a block close or `#}` only, a `+` (`@}+` under
the default, `}+` under bare mode, and `#}+`) suppresses the one-newline-eating of
R5, preserving the following newline. `+` is a close-side trim modifier
exclusively; the additive `+` appears only inside an `Expr`, never adjacent to a
closing delimiter.

## The grammar in one design

The productions above compose into one grammar. The entry point is `SourceFile`;
`Template` is the top-level production the parser drives once the scanner has
classified spans. The two grammars meet at exactly one seam -- the `atSigil`
predicate (R1) and the statement-head test (R4: the `@`-sigil lead under the
default, the leading-keyword test under `pragma bare`) -- and nowhere else, which
is why the whole language is decidable byte-by-byte without heuristic lookahead.
