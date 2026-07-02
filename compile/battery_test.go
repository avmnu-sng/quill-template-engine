package compile_test

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/compile"
)

// batteryCase is one value-semantics contract: the template, the pinned
// expected output, and the case name. The compiled render must match the
// facade byte-for-byte AND the facade must match the pinned contract, so a
// drift on either side fails loudly.
type batteryCase struct {
	name string
	src  string
	want string
}

// semanticsBattery pins the copy-on-write and capture contracts through the
// compiled path: the T1-T7 array-value-semantics cases guarded by
// cow_semantics_test.go and loop_snapshot_test.go, plus the arrow
// live-lexical capture shapes guarded by arrow_capture_test.go (the
// single-template ones; the include and call-block shapes carry
// non-compilable constructs).
var semanticsBattery = []batteryCase{
	{"t1_alias_is_value_copy",
		"@set a = [1,2]\n@set b = a\n@set a[0] = 99\n{{ a[0] }},{{ b[0] }}",
		"99,1"},
	{"t2_loop_var_no_leak",
		"@set src = [[1,2],[3,4]]\n@for row in src {\n@set row[0] = 99\n@}\n{{ src[0][0] }},{{ src[1][0] }}",
		"1,3"},
	{"t3_cell_accumulation_persists",
		"@set acc = cell(0)\n@for w in [1,2,3,4] {\n@set acc.value = acc.value + w\n@}\n{{ acc.value }}",
		"10"},
	{"t4_nested_isolation",
		"@set d = {list: [1,2,3]}\n@set d2 = d\n@set d.list[0] = 99\n{{ d.list[0] }},{{ d2.list[0] }}",
		"99,1"},
	{"t5_member_accumulate",
		"@set m = {}\n@for k in [1,2,3] {\n@set m[k] = k * 10\n@}\n{{ m[1] }},{{ m[2] }},{{ m[3] }}",
		"10,20,30"},
	{"t6_copyback_and_local_vanishes",
		"@set x = [10,20]\n@for i in [1] {\n@set y = [99]\n@set x[0] = y[0]\n@}\n{{ x[0] }} {{ y is defined }}",
		"99 false"},
	{"t6b_filter_subvalue_no_leak",
		"@set a = [[1,2],[3,4]]\n@set f = a | first\n@set f[0] = 99\n{{ a[0][0] }},{{ f[0] }}",
		"1,99"},
	{"t7_loop_frozen_snapshot",
		"@for n in [10,20,30] {\n@if not loop.first {\nwas={{ snap.index }}\n@}\n@set snap = loop\n@}",
		"was=1\nwas=2\n"},
	{"arrow_top_level_live",
		"@set base = 10\n@set f = (n) => n + base\n@set base = 99\n{{ [0] | map(f) | first }}\n",
		"99\n"},
	{"arrow_loop_defined_reads_outer_live",
		"@set base = 10\n@set f = null\n@for x in [1] {\n@set f = (n) => n + base\n@}\n@set base = 99\n{{ [0] | map(f) | first }}\n",
		"99\n"},
	{"arrow_loop_frame_name_reads_final",
		"@set total = 0\n@set f = null\n@for x in [1, 2, 3] {\n@set f = (n) => n + total\n@set total = total + x\n@}\n{{ [0] | map(f) | first }}\n",
		"6\n"},
	{"arrow_returning_arrow_reads_live",
		"@set x = 1\n@set mk = (a) => ((b) => a + b + x)\n@set add = [5] | map(mk) | first\n@set x = 100\n{{ [10] | map(add) | first }}\n",
		"115\n"},
}

// TestValueSemanticsBattery renders the battery through the compiled path in
// one scratch module and asserts each output byte-equal to the facade's
// Render AND to the pinned contract.
func TestValueSemanticsBattery(t *testing.T) {
	var cases []compiledCase
	results := map[string]*compile.Result{}
	for _, bc := range semanticsBattery {
		cs := compiledCase{name: bc.name, template: bc.src}
		res, err := compileCase(t, cs)
		if err != nil {
			t.Fatalf("%s: compile: %v", bc.name, err)
		}
		results[bc.name] = res
		cases = append(cases, cs)
	}
	got := runCompiled(t, cases, results)
	for i, bc := range semanticsBattery {
		r, ok := got[bc.name]
		if !ok {
			t.Errorf("%s: no result from scratch run", bc.name)
			continue
		}
		if r.failed {
			t.Errorf("%s: compiled render errored: %s", bc.name, r.errText)
			continue
		}
		want, err := renderInterp(t, cases[i])
		if err != nil {
			t.Errorf("%s: interp render errored: %v", bc.name, err)
			continue
		}
		if want != bc.want {
			t.Errorf("%s: interpreter drifted from the pinned contract\n got  %q\n want %q", bc.name, want, bc.want)
		}
		if r.out != want {
			t.Errorf("%s: compiled output differs from interpreter\n got  %q\n want %q", bc.name, r.out, want)
		}
	}
}
