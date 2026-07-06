package parse

import (
	"strings"
	"testing"
)

// contains asserts the parsed Dump of src contains want.
func dumpContains(t *testing.T, src, want string) {
	t.Helper()
	got := parseDump(t, src)
	if !strings.Contains(got, want) {
		t.Fatalf("dump of %q =\n%s\nwant substring %q", src, got, want)
	}
}

func TestParseProvideYield(t *testing.T) {
	dumpContains(t, "@provide imports {line\n@}", "(Provide imports")
	dumpContains(t, "@yield imports\n", "(Yield imports)")
}

func TestParseCallBlockNoCallerParams(t *testing.T) {
	// A bare @call has an empty caller-parameter Params child, then the macro args.
	dumpContains(t, "@call wrap(\"p\") {body\n@}", "(CallBlock wrap (Params)")
	dumpContains(t, "@call wrap(\"p\") {body\n@}", "(Arg (String \"p\"))")
}

func TestParseCallBlockWithCallerParams(t *testing.T) {
	// @call(a, b) declares two caller parameters ahead of the macro name.
	dumpContains(t, "@call(a, b) row(1) {body\n@}", "(CallBlock row (Params (Param a) (Param b))")
}

func TestParseRecursiveFor(t *testing.T) {
	dumpContains(t, "@for n in tree recursive {\nx\n@}", "recursive")
	// A plain for is not marked recursive.
	got := parseDump(t, "@for n in tree {\nx\n@}")
	if strings.Contains(got, "recursive") {
		t.Fatalf("plain for should not be recursive: %s", got)
	}
}

func TestParseProvideRequiresLabel(t *testing.T) {
	mustErr(t, "@provide {x\n@}", "slot label")
}

func TestParseYieldRequiresLabel(t *testing.T) {
	mustErr(t, "@yield\n", "slot label")
}

func TestParseCallBlockRequiresMacroName(t *testing.T) {
	mustErr(t, "@call {body\n@}", "macro name")
}
