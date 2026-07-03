package compile_test

import (
	"bytes"
	"errors"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/compile"
	"github.com/avmnu-sng/quill-template-engine/internal/jsonval"
	"github.com/avmnu-sng/quill-template-engine/parse"
	"github.com/avmnu-sng/quill-template-engine/runtime"
	"github.com/avmnu-sng/quill-template-engine/source"
)

// loweringCases is the construct-by-construct parity table: each case
// compiles, renders through the compiled path in the scratch process, and
// must match the facade's Render byte-for-byte (output or error text).
var loweringCases = []compiledCase{
	// Text, interpolation, and ToText spellings.
	{name: "text-only", template: "hello world\n"},
	{name: "print-scalars", template: "{{ 42 }}|{{ -7 }}|{{ 3.0 }}|{{ 3.5 }}|{{ true }}|{{ false }}|[{{ null }}]|{{ \"a\\tb\" }}\n"},
	{name: "print-name", template: "{{ user }}\n", varsJSON: `{"user":"ada"}`},
	{name: "string-interp", template: "@set n = \"go\"\n{{ \"hi #{n} x#{1 + 2}\" }}\n"},

	// Undefined-name error parity (single var keeps the hint deterministic).
	{name: "undef-strict", template: "{{ missing }}\n"},
	{name: "undef-hint", template: "@set a = 1\n{{ missing }}\n"},
	{name: "undef-lenient", template: "[{{ missing }}]\n", opts: compile.Options{LenientVariables: true}},

	// Operators.
	{name: "arith", template: "{{ 1 + 2 }} {{ 7 - 9 }} {{ 3 * 4 }} {{ 7 / 2 }} {{ 6 / 3 }} {{ 7 // 2 }} {{ -7 // 2 }} {{ 7 % 3 }} {{ 2 ** 10 }} {{ 2 ** -1 }}\n"},
	{name: "arith-div-zero", template: "{{ a / z }}", varsJSON: `{"a":1,"z":0}`},
	{name: "arith-overflow", template: "{{ big + one }}", varsJSON: `{"big":9223372036854775807,"one":1}`},
	{name: "arith-type-error", template: "{{ 1 + s }}", varsJSON: `{"s":"x"}`},
	{name: "compare", template: "{{ 1 < 2 }} {{ 2 <= 2 }} {{ 3 > 4 }} {{ 4 >= 5 }} {{ 1 <=> 2 }} {{ \"a\" < \"b\" }}\n"},
	{name: "compare-error", template: "{{ 1 < s }}", varsJSON: `{"s":"a"}`},
	{name: "equality", template: "{{ 1 == 1.0 }} {{ \"1\" == 1 }} {{ [1,2] == [1,2] }} {{ null == false }} {{ 1 != 2 }}\n"},
	{name: "concat", template: "{{ 1 ~ \"x\" ~ true ~ null ~ 2.5 }}\n"},
	{name: "logical", template: "{{ true and false }} {{ true or false }} {{ true xor true }} {{ not 0 }} {{ 1 and \"a\" }}\n"},
	{name: "logical-shortcircuit", template: "{{ false and missing }} {{ true or missing }}\n"},
	{name: "ternary", template: "{{ 1 > 0 ? \"yes\" : \"no\" }}{{ 0 > 1 ? \"yes\" : \"no\" }}\n"},
	{name: "postfix-if", template: "{{ \"a\" if true }}|{{ \"b\" if false }}|{{ \"c\" unless true }}|{{ \"d\" unless false }}\n"},
	{name: "coalesce", template: "{{ missing ?? \"fb\" }} {{ null ?? 1 }} {{ 0 ?? 1 }} {{ user.name ?? \"anon\" }}\n", varsJSON: `{"user":{}}`},
	{name: "elvis", template: "{{ 0 ?: \"z\" }} {{ \"\" ?: \"e\" }} {{ 5 ?: 9 }} {{ missing ?: \"m\" }}\n"},
	{name: "range-op", template: "{{ (a..b) | join(\",\") }} {{ (a..b) | length }}\n", varsJSON: `{"a":1,"b":4}`},
	{name: "bitwise", template: "{{ 6 b_and 3 }} {{ 6 b_or 3 }} {{ 6 b_xor 3 }}\n"},
	{name: "unary-errors", template: "{{ -s }}", varsJSON: `{"s":"x"}`},

	// Membership and matches.
	{name: "membership", template: "{{ 2 in [1,2,3] }} {{ 9 not in [1,2] }} {{ \"el\" in \"hello\" }} {{ \"k\" in {k: 1} }}\n"},
	{name: "affix", template: "{{ \"hello\" starts with \"he\" }} {{ \"hello\" ends with \"lo\" }} {{ 12 starts with 1 }}\n"},
	{name: "matches", template: "{{ \"abc\" matches \"^a.c$\" }} {{ \"abc\" matches \"z\" }}\n"},
	{name: "matches-error", template: "{{ 5 matches \"a\" }}"},
	{name: "quantify", template: "{{ [1,2,3] has some (x => x > 2) }} {{ [1,2,3] has every (x => x > 0) }} {{ [] has some (x => true) }} {{ [] has every (x => false) }}\n"},

	// Access chains.
	{name: "attr-index", template: "{{ user.name }} {{ user[\"name\"] }} {{ items[1] }} {{ nested.list[0] }}\n", varsJSON: `{"user":{"name":"ada"},"items":[10,20,30],"nested":{"list":[7]}}`},
	{name: "attr-missing", template: "{{ user.zip }}", varsJSON: `{"user":{"name":"ada"}}`},
	{name: "attr-on-scalar", template: "{{ n.member }}", varsJSON: `{"n":5}`},
	{name: "nullsafe", template: "{{ maybe?.name }}|{{ user?.name }}\n", varsJSON: `{"maybe":null,"user":{"name":"x"}}`},
	{name: "nullsafe-index", template: "{{ maybe?[0] }}|{{ items?[1] }}\n", varsJSON: `{"maybe":null,"items":[5,6]}`},
	{name: "slice", template: "{{ \"hello\"[1:3] }} {{ items[1:] }} {{ items[:2] | join(\"-\") }}\n", varsJSON: `{"items":[1,2,3,4]}`},
	{name: "key-canonical", template: "{{ m[\"1\"] }} {{ m[1] }} {{ m[\"01\"] }}\n", varsJSON: `{"m":{"1":"int","01":"str"}}`},

	// Literals.
	{name: "list-map-literals", template: "{{ [1, 2, ...rest, 5] | join(\",\") }}|{{ {a: 1, \"b\": 2, (\"c\" ~ \"x\"): 3, d} | json }}|{{ {...base, z: 9} | json }}\n", varsJSON: `{"rest":[3,4],"d":7,"base":{"p":1}}`},

	// Filters, functions, tests.
	{name: "filters", template: "{{ name | upper }} {{ name | replace({\"a\": \"4\"}) }} {{ items | sort | join(\"-\") }} {{ items | first }} {{ missing | default(\"fb\") }}\n", varsJSON: `{"name":"ada","items":[3,1,2]}`},
	{name: "filter-unknown", template: "{{ 1 | nosuchfilter }}"},
	{name: "filter-named-args", template: "{{ 2.34567 | round(2) }} {{ [3,1] | join(\",\") }}\n"},
	{name: "filter-spread-args", template: "{{ \"a-b-c\" | split(...[\"-\"]) | join(\"+\") }}\n"},
	{name: "functions", template: "{{ range(1, 3) | join(\",\") }} {{ max(1, 9, 4) }} {{ min([5, 2]) }}\n"},
	{name: "function-unknown", template: "{{ nosuchfn(1) }}"},
	{name: "tests", template: "{{ 1 is odd }} {{ 2 is even }} {{ null is null }} {{ \"x\" is string }} {{ [1] is sequence }} {{ {a:1} is mapping }} {{ 5 is not string }} {{ 4 is divisible by(2) }}\n"},
	{name: "test-unknown", template: "{{ 1 is nosuchtest }}"},
	{name: "registry-tests", template: "{{ \"upper\" is filter }} {{ \"nope\" is filter }} {{ \"range\" is function }} {{ \"odd\" is test }} {{ 5 is not filter }}\n"},
	{name: "is-defined", template: "{{ user is defined }} {{ missing is defined }} {{ user.name is defined }} {{ user.zip is defined }} {{ user.zip is not defined }} {{ items[0] is defined }} {{ items[9] is defined }} {{ (1 + 1) is defined }}\n", varsJSON: `{"user":{"name":"a"},"items":[1]}`},
	{name: "dump-context", template: "@set a = 1\n{{ dump(a) }}\n"},

	// Arrows.
	{name: "arrows", template: "{{ nums | map(x => x * x) | join(\",\") }} {{ nums | filter(x => x % 2 == 1) | join(\",\") }} {{ nums | reduce((acc, x) => acc + x, 0) }} {{ nums | find(x => x > 3) }}\n", varsJSON: `{"nums":[1,2,3,4,5]}`},
	{name: "arrow-defaults", template: "@set f = (a, b = 10) => a + b\n{{ [1] | map(f) | first }}\n"},
	{name: "arrow-variadic", template: "@set f = (...xs) => xs | length\n{{ f(1, 2, 3) }}\n"},
	{name: "arrow-live-capture", template: "@set base = 10\n@set f = (n) => n + base\n@set base = 99\n{{ [0] | map(f) | first }}\n"},
	{name: "arrow-loop-capture", template: "@set base = 10\n@set f = null\n@for x in [1] {\n@set f = (n) => n + base\n@}\n@set base = 99\n{{ [0] | map(f) | first }}\n"},
	{name: "arrow-frame-final", template: "@set total = 0\n@set f = null\n@for x in [1, 2, 3] {\n@set f = (n) => n + total\n@set total = total + x\n@}\n{{ [0] | map(f) | first }}\n"},
	{name: "arrow-returns-arrow", template: "@set x = 1\n@set mk = (a) => ((b) => a + b + x)\n@set add = [5] | map(mk) | first\n@set x = 100\n{{ [10] | map(add) | first }}\n"},
	{name: "arrow-called-by-name", template: "@set f = (a, b) => a * b\n{{ f(3, 4) }}\n"},

	// Control flow.
	{name: "if-elseif-else", template: "@if n > 10 {\nbig\n@} elseif n > 0 {\nsmall\n@} else {\nzero\n@}\n", varsJSON: `{"n":5}`},
	{name: "if-else-taken", template: "@if n > 10 {\nbig\n@} else {\nlow\n@}\n", varsJSON: `{"n":1}`},
	{name: "for-basic", template: "@for x in items {\n{{ loop.index }}:{{ x }}\n@}\n", varsJSON: `{"items":["a","b","c"]}`},
	{name: "for-two-target", template: "@for k, v in meta {\n{{ k }}={{ v }}\n@}\n", varsJSON: `{"meta":{"x":1,"y":2}}`},
	{name: "for-else-empty", template: "@for x in items {\n{{ x }}\n@} else {\nnone\n@}\n", varsJSON: `{"items":[]}`},
	{name: "for-else-skipped", template: "@for x in items {\n{{ x }}\n@} else {\nnone\n@}\n", varsJSON: `{"items":[1]}`},
	{name: "for-loop-fields", template: "@for x in items {\n{{ loop.index0 }}/{{ loop.index }}/{{ loop.revindex }}/{{ loop.revindex0 }}/{{ loop.first }}/{{ loop.last }}/{{ loop.length }}/{{ loop.prev ?? \"-\" }}/{{ loop.next ?? \"-\" }}\n@}\n", varsJSON: `{"items":[10,20,30]}`},
	{name: "for-nested-parent", template: "@for a in [1,2] {\n@for b in [7,8] {\np{{ loop.parent.index }}i{{ loop.index }}\n@}\n@}\n"},
	{name: "for-fused-if", template: "@for x in items if x % 2 == 0 {\n{{ loop.index }}:{{ x }}({{ loop.length }})\n@}\n", varsJSON: `{"items":[1,2,3,4,5,6]}`},
	{name: "for-fused-two-target", template: "@for k, v in meta if v > 1 {\n{{ k }}={{ v }}\n@}\n", varsJSON: `{"meta":{"a":1,"b":2,"c":3}}`},
	{name: "for-noniterable", template: "@for x in n {\n{{ x }}\n@}\n", varsJSON: `{"n":5}`},
	{name: "for-noniterable-lenient", template: "@for x in n {\n{{ x }}\n@} else {\nempty\n@}\n", varsJSON: `{"n":5}`, opts: compile.Options{LenientVariables: true}},
	{name: "loop-changed", template: "@for row in rows {\n@if loop.changed(row.group) {\n[{{ row.group }}]\n@}\n- {{ row.name }}\n@}\n", varsJSON: `{"rows":[{"group":"a","name":"1"},{"group":"a","name":"2"},{"group":"b","name":"3"}]}`},
	{name: "loop-changed-in-filter", template: "@for x in items if loop.changed(x) {\n{{ x }}\n@}\n", varsJSON: `{"items":[1,1,2,2,3]}`},
	{name: "loop-changed-outside", template: "{{ loop.changed(1) }}"},
	{name: "loop-snapshot", template: "@for n in [10,20,30] {\n@if not loop.first {\nwas={{ snap.index }}\n@}\n@set snap = loop\n@}\n"},

	// Scope and copy-back semantics.
	{name: "set-basic", template: "@set a = 1\n@set a, b = a + 1, a\n{{ a }},{{ b }}\n"},
	{name: "copyback", template: "@set total = 0\n@for n in [1,2,3] {\n@set total = total + n\n@}\n{{ total }}\n"},
	{name: "body-local-vanishes", template: "@set x = [10,20]\n@for i in [1] {\n@set y = [99]\n@set x[0] = y[0]\n@}\n{{ x[0] }} {{ y is defined }}\n"},
	{name: "copyback-vars-name", template: "@for i in [1] {\n@set seen = i\n@}\n{{ seen }}\n", varsJSON: `{"seen":0}`},
	{name: "loop-frame-persists", template: "@for i in [1,2] {\n{{ x ?? \"-\" }}\n@set x = i\n@}\n"},
	{name: "with", template: "@set a = 1\n@with {b: 2} {\n{{ a }}{{ b }}\n@set a = 9\n{{ a }}\n@}\n{{ a }}\n"},
	{name: "with-only", template: "@set a = 1\n@with {b: 2} only {\n{{ b }}{{ a is defined }}\n@}\n{{ a }}\n"},
	{name: "with-only-undef", template: "@with {b: 2} only {\n{{ a }}\n@}\n", varsJSON: `{"a":1}`},
	{name: "with-expr-map", template: "@with cfg {\n{{ host }}:{{ port }}\n@}\n", varsJSON: `{"cfg":{"host":"h","port":80}}`},
	{name: "with-loop-copyback", template: "@set x = 1\n@with {a: 2} {\n@for i in [1] {\n@set x = 9\n@}\n{{ x }}\n@}\n{{ x }}\n"},
	{name: "inline-assign", template: "{{ (b = 5) + 1 }} {{ b }}\n"},

	// Value semantics (COW).
	{name: "cow-alias", template: "@set a = [1,2]\n@set b = a\n@set a[0] = 99\n{{ a[0] }},{{ b[0] }}\n"},
	{name: "cow-loop-noleak", template: "@set src = [[1,2],[3,4]]\n@for row in src {\n@set row[0] = 99\n@}\n{{ src[0][0] }},{{ src[1][0] }}\n"},
	{name: "cow-nested", template: "@set d = {list: [1,2,3]}\n@set d2 = d\n@set d.list[0] = 99\n{{ d.list[0] }},{{ d2.list[0] }}\n"},
	{name: "cow-cell", template: "@set acc = cell(0)\n@for w in [1,2,3,4] {\n@set acc.value = acc.value + w\n@}\n{{ acc.value }}\n"},
	{name: "cow-member-accumulate", template: "@set m = {}\n@for k in [1,2,3] {\n@set m[k] = k * 10\n@}\n{{ m[1] }},{{ m[2] }},{{ m[3] }}\n"},
	{name: "cow-filter-subvalue", template: "@set a = [[1,2],[3,4]]\n@set f = a | first\n@set f[0] = 99\n{{ a[0][0] }},{{ f[0] }}\n"},
	{name: "member-into-vars", template: "@set user.name = \"eve\"\n{{ user.name }}|{{ user.tag }}\n", varsJSON: `{"user":{"name":"ada","tag":"t"}}`},

	// Destructuring.
	{name: "destructure-seq", template: "@set [a, b] = pair\n@set [head, ...rest] = nums\n@set [x, [y, z]] = nested\n{{ a }}+{{ b }} {{ head }}::{{ rest | json }} {{ x }}/{{ y }}/{{ z }}\n", varsJSON: `{"pair":[1,2],"nums":[3,4,5],"nested":[6,[7,8]]}`},
	{name: "destructure-map", template: "@set {name, port: p} = cfg\n{{ name }}:{{ p }}\n", varsJSON: `{"cfg":{"name":"n","port":80}}`},
	{name: "destructure-optional", template: "@set [a, b?] = [1]\n{{ a }},[{{ b }}]\n"},
	{name: "destructure-elided", template: "@set [, b] = [1, 2]\n{{ b }}\n"},
	{name: "destructure-undersupply", template: "@set [a, b] = [1]\n"},
	{name: "destructure-oversupply", template: "@set [a] = [1, 2]\n"},
	{name: "destructure-not-seq", template: "@set [a] = 5\n"},

	// Capture, do, log, special names.
	{name: "capture", template: "@set block = capture {\nline {{ 1 + 1 }}\n@}\n[{{ block }}]\n"},
	{name: "do-log", template: "@set c = cell(0)\n@do c.set(41)\n@log \"note \" ~ c.value\n{{ c.value + 1 }}\n"},
	{name: "charset", template: "{{ _charset }}\n"},
	{name: "context-special", template: "@set a = 1\n@set b = \"x\"\n{{ _context | json }}\n"},

	// Escaping.
	{name: "autoescape-html", template: "{{ v }}|{{ v | raw }}|<b>lit</b>\n", varsJSON: `{"v":"<a & 'q'>"}`, opts: compile.Options{AutoescapeHTML: true}},
	{name: "escape-region", template: "@escape html {\n{{ v }}\n@}\n@escape off {\n{{ v }}\n@}\n{{ v }}\n", varsJSON: `{"v":"<x>"}`},
	{name: "escape-region-nested", template: "@escape html {\n{{ v }}\n@escape url {\n{{ v }}\n@}\n{{ v }}\n@}\n", varsJSON: `{"v":"a b<c"}`, opts: compile.Options{AutoescapeHTML: true}},
	{name: "capture-under-escape", template: "@escape html {\n@set b = capture {\n{{ v }}\n@}\n{{ b }}\n@}\n", varsJSON: `{"v":"<y>"}`},

	// @tab regions.
	{name: "tab-block", template: "start\n@tab(1) {\nline one\nline two\n\nafter blank\n@}\nend\n"},
	{name: "tab-nested", template: "@tab(1) {\nouter\n@tab(2) {\ninner\n@}\nback\n@}\n"},
	{name: "tab-midline", template: "{{ x -}}\n@tab(1) {\nline one\nline two\n@}\n", varsJSON: `{"x":"head"}`},
	{name: "tab-level-error", template: "@tab(\"x\") {\nbody\n@}\n"},

	// Whitespace control (resolved by the lexer; text nodes are pre-trimmed).
	{name: "whitespace-control", template: "items = [\n@for x in xs {~\n  {{ x }},\n@}~\n]\nhard:a   {{- glue -}}   b\nline:p  {{~ glue ~}}  q\nkeep:{{ glue }}\n", varsJSON: `{"xs":[1,2],"glue":"G"}`},

	// Method calls on host objects.
	{name: "method-call", template: "@set c = cell(5)\n{{ c.get() }}\n"},
	{name: "method-on-scalar", template: "{{ n.frob() }}", varsJSON: `{"n":3}`},

	// Operand aliasing: an inline assignment in a LATER operand must not
	// retroactively change an EARLIER operand's value; the earlier operand is
	// captured before the rebind, exactly as the interpreter evaluates.
	{name: "spill-arith", template: "@set x = 1\n{{ x + (x = 5) }} {{ x }}\n"},
	{name: "spill-compare", template: "@set x = 1\n{{ x < (x = 5) }} {{ x }}\n"},
	{name: "spill-concat", template: "@set x = \"a\"\n{{ x ~ (x = \"b\") }} {{ x }}\n"},
	{name: "spill-xor", template: "@set x = true\n{{ x xor (x = false) }} {{ x }}\n"},
	{name: "spill-power", template: "@set x = 2\n{{ x ** (x = 3) }} {{ x }}\n"},
	{name: "spill-eq", template: "@set x = 1\n{{ x == (x = 5) }} {{ x }}\n"},
	{name: "spill-index-recv", template: "@set x = [1, 2]\n{{ x[(x = [7, 8]) ? 0 : 0] }} {{ x | json }}\n"},
	{name: "spill-memberassign-key", template: "@set x = [1, 2]\n@set x[(x = [9])[0] - 9] = 5\n{{ x | json }}\n"},
	{name: "spill-method-recv", template: "@set c = cell(5)\n{{ c.get((c = 1)) }} {{ c }}\n"},
	{name: "spill-slice-recv", template: "@set x = \"hello\"\n{{ x[(x = \"ab\") ? 1 : 1:3] }} {{ x }}\n"},

	// Non-root frame name ordering: hints, _context, and the needs-context
	// injection must list names in actual first-bind order (a name whose first
	// SOURCE appearance sits in a non-executed arm binds later at runtime),
	// exactly like the interpreter's insertion-ordered Scope frame entries.
	{name: "hint-order-loop", template: "@for i in [1] {\n@if i > 5 {\n@set b = 1\n@}\n@set a = 2\n@set b = 3\n{{ nope }}\n@}\n"},
	{name: "ctx-order-loop", template: "@for i in [1] {\n@if i > 5 {\n@set b = 1\n@}\n@set a = 2\n@set b = 3\n{{ _context | keys | join(\",\") }}\n@}\n"},
	{name: "ctx-order-with", template: "@with {} {\n@if false {\n@set b = 1\n@}\n@set a = 1\n@set b = 2\n{{ _context | keys | join(\",\") }}\n@}\n"},
	{name: "dump-order-loop", template: "@for i in [1] {\n@if i > 5 {\n@set b = 1\n@}\n@set a = 2\n@set b = 3\n{{ dump() }}\n@}\n"},

	// Needs-environment injection: the compiled qEnv handle carries the
	// Options' engine configuration, so the tab FILTER and tab() FUNCTION
	// honor a non-default TabWidth exactly like the @tab region, and seeded
	// randomness is byte-identical to an identically seeded facade.
	{name: "tab-filter-width8", template: "@tab(1) {\nX\n@}\n{{ \"a\\nb\" | tab(1) }}|{{ tab(2) }}|\n", opts: compile.Options{TabWidth: 8}},
	{name: "seeded-random", template: "{{ random(1000000000) }}|{{ (1..20) | shuffle | join(\",\") }}\n", opts: compile.Options{RandomSeed: 42, RandomSeedSet: true}},

	// Edge cases: conditional binds, frame fallbacks, and error spellings.
	{name: "empty-template", template: ""},
	{name: "fused-filter-inline-assign", template: "@for x in [1,2,3] if (last = x) > 1 {\n{{ x }}\n@}\n{{ last is defined }}\n"},
	{name: "if-bind-taken", template: "@if c {\n@set v = 1\n@}\n{{ v ?? \"unset\" }}\n", varsJSON: `{"c":true}`},
	{name: "if-bind-skipped", template: "@if c {\n@set v = 1\n@}\n{{ v ?? \"unset\" }}\n", varsJSON: `{"c":false}`},
	{name: "if-bind-strict-miss", template: "@if c {\n@set v = 1\n@}\n{{ v }}\n", varsJSON: `{"c":false}`},
	{name: "with-member-assign", template: "@with {m: {a: 1}} {\n@set m.a = 2\n{{ m.a }}\n@}\n"},
	{name: "with-maybe-bind-taken", template: "@with {a: 1} {\n@if c {\n@set a = 5\n@}\n{{ a }}\n@}\n", varsJSON: `{"c":true}`},
	{name: "with-maybe-bind-skipped", template: "@with {a: 1} {\n@if c {\n@set a = 5\n@}\n{{ a }}\n@}\n", varsJSON: `{"c":false}`},
	{name: "nested-loop-copyback", template: "@set t = 0\n@for a in [1,2] {\n@for b in [3,4] {\n@set t = t + a * b\n@}\n@}\n{{ t }}\n"},
	{name: "empty-loop-no-copyback", template: "@set t = 0\n@for x in [] {\n@set t = 1\n@}\n{{ t }}\n"},
	{name: "loop-else-binds", template: "@for x in [] {\n{{ x }}\n@} else {\n@set e = 1\n@}\n{{ e }}\n"},
	{name: "vars-then-set", template: "{{ x }}\n@set x = 2\n{{ x }}\n", varsJSON: `{"x":1}`},
	{name: "deep-member-assign", template: "@set d.list[0].x = 99\n{{ d.list[0].x }},{{ d.list[1].x }}\n", varsJSON: `{"d":{"list":[{"x":1},{"x":2}]}}`},
	{name: "concat-array-error", template: "{{ a ~ \"x\" }}", varsJSON: `{"a":[1]}`},
	{name: "print-array-error", template: "{{ a }}", varsJSON: `{"a":[1]}`},
	{name: "attr-on-null", template: "{{ maybe.name }}", varsJSON: `{"maybe":null}`},
	{name: "line-noop", template: "@line 9\nok\n"},
	{name: "verbatim", template: "@verbatim {\n{{ x }} @if raw\n@}\ndone\n"},
}

// TestLoweringParity renders every lowering case through the compiled path
// and asserts byte-equality (output or error text) against the facade.
func TestLoweringParity(t *testing.T) {
	results := map[string]*compile.Result{}
	for _, cs := range loweringCases {
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cs.name, err)
		}
		results[cs.name] = res
	}
	got := runCompiled(t, loweringCases, results)
	for _, cs := range loweringCases {
		r, ok := got[cs.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", cs.name)
			continue
		}
		wantOut, wantErr := renderInterp(t, cs)
		if wantErr != nil {
			if !r.failed {
				t.Errorf("%s: interp errored (%v) but compiled rendered %q", cs.name, wantErr, r.out)
				continue
			}
			if r.errText != wantErr.Error() {
				t.Errorf("%s: error text mismatch\n got  %q\n want %q", cs.name, r.errText, wantErr.Error())
			}
			continue
		}
		if r.failed {
			t.Errorf("%s: compiled errored (%s) but interp rendered %q", cs.name, r.errText, wantOut)
			continue
		}
		if r.out != wantOut {
			t.Errorf("%s: output mismatch\n got  %q\n want %q", cs.name, r.out, wantOut)
		}
	}
}

// renderInterp renders one case through the facade with the options the
// compile Options imply.
func renderInterp(t *testing.T, cs compiledCase) (string, error) {
	t.Helper()
	var opts []quill.Option
	if cs.opts.AutoescapeHTML {
		opts = append(opts, quill.WithAutoescapeHTML(true))
	}
	if cs.opts.LenientVariables {
		opts = append(opts, quill.WithStrictVariables(false))
	}
	if cs.opts.TabWidth > 0 {
		opts = append(opts, quill.WithTabWidth(cs.opts.TabWidth))
	}
	if cs.opts.RandomSeedSet {
		opts = append(opts, quill.WithRandomSeed(cs.opts.RandomSeed))
	}
	vars, err := jsonval.DecodeMap([]byte(orEmptyObject(cs.varsJSON)))
	if err != nil {
		t.Fatalf("%s: vars: %v", cs.name, err)
	}
	if cs.templates != nil {
		env := quill.NewWithArray(cs.templates, opts...)
		return env.Render(cs.entry, vars)
	}
	env := quill.NewWithArray(map[string]string{cs.name + ".ql": cs.template}, opts...)
	return env.Render(cs.name+".ql", vars)
}

func orEmptyObject(s string) string {
	if s == "" {
		return "{}"
	}
	return s
}

// TestDeterminismAndHygiene compiles one template twice and asserts
// byte-identical, gofmt-stable, ASCII-only source free of the forbidden
// tokens, with a populated line map.
func TestDeterminismAndHygiene(t *testing.T) {
	body := "@set total = 0\n@for x in items if x > 1 {\n{{ loop.index }}:{{ x | upper ?? x }}\n@set total = total + 1\n@}\n{{ total }}\n"
	mod, err := parse.Parse(source.New("d.ql", body))
	if err != nil {
		t.Fatal(err)
	}
	r1, err := compile.Module("d.ql", mod, compile.Options{})
	if err != nil {
		t.Fatal(err)
	}
	mod2, err := parse.Parse(source.New("d.ql", body))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := compile.Module("d.ql", mod2, compile.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r1.Source, r2.Source) {
		t.Error("recompilation is not byte-identical")
	}
	formatted, err := format.Source(r1.Source)
	if err != nil {
		t.Fatalf("generated source does not format: %v", err)
	}
	if !bytes.Equal(formatted, r1.Source) {
		t.Error("generated source is not gofmt-stable")
	}
	for i := 0; i < len(r1.Source); i++ {
		if r1.Source[i] >= 0x80 {
			t.Fatalf("non-ASCII byte at offset %d", i)
		}
	}
	for _, token := range hygieneTokens() {
		if strings.Contains(string(r1.Source), token) {
			t.Errorf("generated source contains forbidden token %q", token)
		}
	}
	if len(r1.LineMap) == 0 {
		t.Error("line map is empty")
	}
	for i := 1; i < len(r1.LineMap); i++ {
		if r1.LineMap[i].Generated <= r1.LineMap[i-1].Generated {
			t.Error("line map is not sorted by generated line")
		}
	}
	if r1.FuncName != "Render" {
		t.Errorf("default FuncName = %q", r1.FuncName)
	}
}

// TestLoadGateParity asserts Module runs the facade's load-time gates -- the
// gradual type checker and the literal-regex validation -- rejecting exactly
// the templates the facade rejects at load, with the facade's exact error
// text, even when the offending construct sits in dead code.
func TestLoadGateParity(t *testing.T) {
	cases := []struct {
		name     string
		template string
	}{
		{"annot-set", "@set n: int = \"oops\"\n{{ n }}\n"},
		{"loop-elem-member", "@for i in [1, 2, 3] {\n{{ i }},\n{{ i == 2 ? i.boom : i }};\n@}\n"},
		{"loop-user-root", "@set loop = 1\n@for i in [2] {\n{{ loop.parent }}\n@}\n{{ loop }}\n"},
		{"filter-arg", "{{ 1 | round(\"x\") }}\n"},
		{"regex-literal-dead", "@if false {\n{{ \"a\" matches \"(\" }}\n@}\nok\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod, err := parse.Parse(source.New(tc.name+".ql", tc.template))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, cerr := compile.Module(tc.name+".ql", mod, compile.Options{})
			if cerr == nil {
				t.Fatal("Module compiled a template the facade rejects at load")
			}
			env := quill.NewWithArray(map[string]string{tc.name + ".ql": tc.template})
			_, ferr := env.Render(tc.name+".ql", map[string]runtime.Value{})
			if ferr == nil {
				t.Fatalf("facade unexpectedly rendered; Module error was %v", cerr)
			}
			if cerr.Error() != ferr.Error() {
				t.Fatalf("load error mismatch\n compile %q\n facade  %q", cerr.Error(), ferr.Error())
			}
		})
	}
}

// TestGoKeywordNames asserts a Go-keyword PackageName or FuncName is rejected
// upfront with a clear one-line error instead of crashing go/format with a
// whole-file dump.
func TestGoKeywordNames(t *testing.T) {
	mod, err := parse.Parse(source.New("k.ql", "hi\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, ferr := compile.Module("k.ql", mod, compile.Options{FuncName: "func"})
	if ferr == nil {
		t.Fatal("keyword FuncName compiled")
	}
	if ferr.Error() != `compile: func name "func" is a Go keyword` {
		t.Fatalf("unexpected FuncName error: %v", ferr)
	}
	_, perr := compile.Module("k.ql", mod, compile.Options{PackageName: "type"})
	if perr == nil {
		t.Fatal("keyword PackageName compiled")
	}
	if perr.Error() != `compile: package name "type" is a Go keyword` {
		t.Fatalf("unexpected PackageName error: %v", perr)
	}
}

// hygieneTokens assembles the forbidden strings without embedding them
// verbatim in this file.
func hygieneTokens() []string {
	return []string{
		"interview" + "street",
		"Brah" + "ma",
		"getJava" + "ListDataType",
		"subtract" + "One",
	}
}

// TestGeneratedVet runs go vet over a sample of generated source, including
// the shapes that lower to guarded returns (unknown escape strategy,
// loop.changed outside a loop), so the emitted code is vet-clean.
func TestGeneratedVet(t *testing.T) {
	samples := []compiledCase{
		{name: "vet-a", template: "@set total = 0\n@for x in items if x > 1 {\n{{ loop.index }}:{{ x | upper ?? x }}\n@if loop.changed(x) {\nc\n@}\n@set total = total + 1\n@}\n{{ total }}\n"},
		{name: "vet-b", template: "@escape bogus {\nx\n@}\n{{ loop.changed(1) }}\n@with {a: 1} only {\n{{ a }}\n@}\n@set m = {}\n@set m.k = [1]\n@set m.k[0] = 2\n@tab(1) {\n{{ [1,2] | map(v => v * 2) | join(\",\") }}\n@}\n"},
		// The loop-optimizer shapes: inline field arithmetic and prev/next
		// fallbacks, the on-demand loop materialization inside a needs-context
		// guard across an inline nested chain, a materialized loop beside an
		// inline one, and a with frame on the on-demand parent-probe path.
		{name: "vet-c", template: "@for a in [1,2] {\n@for b in [3] {\n{{ dump() }}{{ loop.parent.prev ?? 0 }}{{ loop.parent.next ?? 0 }}{{ loop.parent.revindex0 }}{{ loop.index0 }}{{ loop[\"last\"] }}\n@}\n@}\n@for y in [1] {\n@set snap = loop\n{{ snap.first }}{{ _context | length }}\n@}\n@with {w: 1} {\n@for z in [2] {\n{{ dump() }}{{ loop.length }}\n@}\n@}\n"},
		// The live-path shapes: the runtime array/pairs split, PairAt target
		// loads (single- and two-target), live prev/next fallbacks, the live
		// length snapshot in inline arithmetic, the on-demand loop-object
		// materialization off the live path (dump), a parent-chain read from
		// a fused (pairs) inner into a live outer, and a mutating sibling
		// loop lowered on the pairs path beside the live ones.
		{name: "vet-d", template: "@for k, v in m {\n{{ loop.prev ?? 0 }}{{ loop.next ?? 0 }}{{ k }}{{ v }}{{ loop.revindex }}\n@}\n@for a in [1,2] {\n{{ dump() }}{{ loop.length }}\n@for b in [3,4] if b > 3 {\n{{ loop.index }}{{ loop.parent.last }}\n@}\n@}\n@set ys = [0]\n@for x in [1] {\n@set ys[0] = x\n{{ loop.changed(x) }}\n@}\n"},
		// The strict dotted-read shapes: the inline KArray fast path over an
		// elided binding local, a spilled literal-map receiver, a chained read,
		// the discarded @do position, and the closure (arrow) return path.
		{name: "vet-e", template: "@set u = {name: \"a\"}\n@set w = {inner: {v: 1}}\n{{ u.name }}{{ w.inner.v }}{{ {a: 1}.a }}\n@do u.name\n{{ [u] | map(r => r.name) | join(\",\") }}\n"},
		// The tab-free emission shapes: direct io.WriteString text and
		// static-Int writes, the Str/Safe print guard, a capture writing into
		// its builder, and the hoisted-injection arms for a needs-context
		// function and an argful filter beside a bare fast-flag pipe.
		{name: "vet-f", template: "@set b = capture {\n{{ 1 }}|{{ s ?? \"x\" }}\n@}\n{{ b }}{{ dump() }}{{ 2.5 }}\n@for i in (1..3) {\n{{ loop.index }}:{{ loop.revindex0 }} {{ b | upper }} {{ [i] | join(\"-\") }}\n@}\n"},
		// The Unit shapes: multi-source qSrc anchors, inlined block bodies,
		// the parent() capture writer, block() captures with their guarded
		// miss error, a loop inside an inlined body, and the guarded
		// parent()-outside-a-block return.
		{name: "vet-u", entry: "page.ql", templates: map[string]string{
			"trait.ql": "@block t {\ntrait\n@}\n",
			"base.ql":  "@use \"trait.ql\" with {t: b}\n@block b {\nbase({{ parent() }})\n@}\n@for x in [1,2] {\n@block row {\nr{{ loop.index }}\n@}\n@}\n{{ block(\"nope\") }}\n",
			"page.ql":  "@extends \"base.ql\"\n@block b {\npage({{ parent() }})\n@}\n@block row {\nR{{ loop.index }} {{ block(\"t\", \"trait.ql\") }}\n@}\n",
		}},
		// The Unit @tab shapes: the parent() capture seeding and copying back
		// the qWriter state, and the composition-error stub body.
		{name: "vet-u-tab", entry: "page.ql", templates: map[string]string{
			"base.ql": "@tab(1) {\n@block b {\nbase\n@}\n@}\n",
			"page.ql": "@extends \"base.ql\"\n@block b {\n{{ parent() }}+\n@}\n",
		}},
		{name: "vet-u-stub", entry: "page.ql", templates: map[string]string{
			"trait.ql": "not a trait\n",
			"page.ql":  "@use \"trait.ql\"\nbody\n",
		}},
	}
	dir := t.TempDir()
	root := repoRoot(t)
	gomod := "module qvet\n\ngo 1.23\n\nrequire github.com/avmnu-sng/quill-template-engine v0.0.0\n\nreplace github.com/avmnu-sng/quill-template-engine => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := os.ReadFile(filepath.Join(root, ".go-version")); err == nil {
		if err := os.WriteFile(filepath.Join(dir, ".go-version"), v, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, cs := range samples {
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cs.name, err)
		}
		sub := filepath.Join(dir, pkgName(cs.name))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "gen.go"), res.Source, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go vet on generated source failed: %v\n%s", err, out)
	}
}

// TestNotCompilable asserts every excluded construct is detected and reported
// as a typed *NotCompilableError naming the construct.
func TestNotCompilable(t *testing.T) {
	cases := []struct {
		name      string
		template  string
		construct string
	}{
		{"extends", "@extends \"base.ql\"\n", "@extends"},
		{"block", "@block header {\nx\n@}\n", "@block"},
		{"macro", "@macro m() {\nx\n@}\n", "@macro"},
		{"import", "@import \"lib.ql\" as lib\n", "@import"},
		{"from", "@from \"lib.ql\" import m\n", "@from"},
		{"use", "@use \"trait.ql\"\n", "@use"},
		{"include", "@include \"part.ql\"\n", "@include"},
		{"embed", "@embed \"part.ql\" {\n@}\n", "@embed"},
		{"provide", "@provide slot {\nx\n@}\n", "@provide"},
		{"yield", "@yield slot\n", "@yield"},
		{"cache", "@cache key=\"k\" {\nx\n@}\n", "@cache"},
		{"sandbox", "@sandbox {\nx\n@}\n", "@sandbox"},
		{"apply", "@apply | upper {\nx\n@}\n", "@apply"},
		{"guard", "@guard filter(\"upper\") {\nx\n@}\n", "@guard"},
		{"flush", "@flush\n", "@flush"},
		{"recursive-for", "@for n in tree recursive {\n{{ n }}\n@}\n", "recursive @for"},
		{"self", "{{ _self.m() }}\n", `special name "_self"`},
		{"parent-fn", "{{ parent() }}\n", `function "parent"`},
		{"block-fn", "{{ block(\"x\") }}\n", `function "block"`},
		{"slot-fn", "{{ slot(\"x\") }}\n", `function "slot"`},
		{"caller-fn", "{{ caller() }}\n", `function "caller"`},
		{"include-fn", "{{ include(\"p.ql\") }}\n", `engine-bound function "include"`},
		{"source-fn", "{{ source(\"p.ql\") }}\n", `engine-bound function "source"`},
		{"tfs-fn", "{{ template_from_string(\"x\") }}\n", `engine-bound function "template_from_string"`},
		{"changed-in-arrow", "@for x in [1] {\n{{ [1] | map(y => loop.changed(y)) | first }}\n@}\n", "loop.changed inside an arrow function"},
		// A destructuring pattern among MULTIPLE set targets is a form the
		// interpreter itself misbinds today (a recorded engine bug), so the
		// backend must reject it typed instead of panicking or reproducing
		// the misbinding.
		{"multi-set-list-pattern", "@set [a], b = [1], 2\n{{ a }}{{ b }}\n", "destructuring pattern in a multi-target set"},
		{"multi-set-pattern-second", "@set b, [a] = 2, [1]\n{{ a }}{{ b }}\n", "destructuring pattern in a multi-target set"},
		// A non-ASCII template identifier cannot become a Go local in the
		// ASCII-only generated file; the rejection names the identifier.
		{"non-ascii-set", "@set caf\xc3\xa9 = 1\n{{ caf\xc3\xa9 }}\n", "non-ASCII template identifier \"caf\\u00e9\""},
		{"non-ascii-loop-target", "@for caf\xc3\xa9 in [1] {\n{{ caf\xc3\xa9 }}\n@}\n", "non-ASCII template identifier \"caf\\u00e9\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod, err := parse.Parse(source.New("t.ql", tc.template))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, cerr := compile.Module("t.ql", mod, compile.Options{})
			if cerr == nil {
				t.Fatalf("expected ErrNotCompilable for %s", tc.construct)
			}
			var nce *compile.NotCompilableError
			if !errors.As(cerr, &nce) {
				t.Fatalf("error is not *NotCompilableError: %v", cerr)
			}
			if !errors.Is(cerr, compile.ErrNotCompilable) {
				t.Fatalf("error does not match ErrNotCompilable sentinel: %v", cerr)
			}
			if nce.Construct != tc.construct {
				t.Fatalf("construct = %q, want %q", nce.Construct, tc.construct)
			}
		})
	}
}
