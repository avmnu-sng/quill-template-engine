package compile_test

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/compile"
)

// fn1Cases is the differential battery for the arity-known filter fast call:
// each case renders through the compiled path and must match the facade
// byte-for-byte (output or error text). It walks both arms of the generated
// dispatch -- the bare pipe takes the hoisted Fn1 branch, while explicit
// arguments, spreads, injection-flagged filters, and filters without Fn1
// stay on the general slice-building arm -- plus the observable-timing edges
// (unknown filter mid-stream, error text and position off both arms).
var fn1Cases = []compiledCase{
	// The fast arm over every audited filter, piped bare inside a loop.
	{name: "fn1-upper-loop", template: "@for u in users {\n{{ loop.index }}. {{ u.name | upper }}\n@}\n", varsJSON: `{"users":[{"name":"ada"},{"name":"grace"}]}`},
	{name: "fn1-lower", template: "{{ s | lower }}\n", varsJSON: `{"s":"MiXeD"}`},
	{name: "fn1-trim", template: "[{{ s | trim }}]\n", varsJSON: `{"s":"  padded\t "}`},
	{name: "fn1-capitalize", template: "{{ s | capitalize }}\n", varsJSON: `{"s":"hello WORLD"}`},
	{name: "fn1-title", template: "{{ s | title }}\n", varsJSON: `{"s":"war and peace"}`},
	{name: "fn1-length", template: "{{ xs | length }}/{{ s | length }}\n", varsJSON: `{"xs":[1,2,3],"s":"abcd"}`},
	{name: "fn1-first-last", template: "{{ xs | first }}..{{ xs | last }}\n", varsJSON: `{"xs":[7,8,9]}`},
	{name: "fn1-reverse", template: "{{ xs | reverse | join(\",\") }}\n", varsJSON: `{"xs":[1,2,3]}`},
	{name: "fn1-keys", template: "{{ m | keys | join(\",\") }}\n", varsJSON: `{"m":{"b":1,"a":2}}`},
	{name: "fn1-raw", template: "{{ s | raw }}\n", varsJSON: `{"s":"<b>x</b>"}`, opts: compile.Options{AutoescapeHTML: true}},

	// The general arm: explicit arguments on audited names, and the same
	// name with an empty spread (identical argument vector, slice path).
	{name: "fn1-trim-args", template: "[{{ s | trim(\"left\") }}]\n", varsJSON: `{"s":"  x  "}`},
	{name: "fn1-reverse-arg", template: "{{ m | reverse(false) | join(\",\") }}\n", varsJSON: `{"m":{"a":1,"b":2}}`},
	{name: "fn1-empty-spread", template: "{{ s | upper(...empty) }}\n", varsJSON: `{"s":"ab","empty":[]}`},

	// Filters without Fn1 keep their exact path: an argful core filter, a
	// needs-environment filter (tab honors the engine width), and default's
	// undefined suppression.
	{name: "fn1-join-general", template: "{{ xs | join(\"-\") }}\n", varsJSON: `{"xs":[1,2]}`},
	{name: "fn1-default-suppression", template: "{{ missing | default(\"d\") }}\n"},

	// Fast-arm error parity: ToText on a collection errors with the same
	// text and position through Fn1 as through Fn.
	{name: "fn1-error-text", template: "before\n{{ xs | upper }}\n", varsJSON: `{"xs":[1,2]}`},
	// The per-iteration unknown-filter timing stays observable: the loop
	// body streams one iteration's prefix before the miss aborts.
	{name: "fn1-unknown-mid-stream", template: "@for x in [1,2] {\n{{ x }}: {{ x | nosuchfilter }}\n@}\n"},

	// A chained pipe mixes both arms in one expression: upper is fast,
	// join is general.
	{name: "fn1-chain-mixed", template: "{{ xs | reverse | join(\"+\") | upper }}\n", varsJSON: `{"xs":["a","b"]}`},
}

// TestFn1CompiledParity renders the fast-call battery through the compiled
// path and asserts byte-equality (output or error text) against the facade,
// so both consumers of the ext.Filter.Fn1 surface are held to one oracle.
func TestFn1CompiledParity(t *testing.T) {
	results := map[string]*compile.Result{}
	for _, cs := range fn1Cases {
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", cs.name, err)
		}
		results[cs.name] = res
	}
	got := runCompiled(t, fn1Cases, results)
	for _, cs := range fn1Cases {
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
