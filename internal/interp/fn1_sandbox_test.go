package interp

import (
	"context"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
	"github.com/avmnu-sng/quill-template-engine/pkg/sandbox"
)

// TestFn1SandboxArrowGateOnPipedValue covers B13 on the filter fast call: a
// bare pipe of a smuggled host callable into an Fn1 filter is rejected exactly
// like the general path rejects it, because the fast call runs the arrow gate
// on the piped value, which is the one value that exists there.
func TestFn1SandboxArrowGateOnPipedValue(t *testing.T) {
	pol := sandbox.NewPolicy(sandbox.AllowFilters("upper"))
	eng := sandboxStub(nil, pol)
	err := renderErr(t, eng, "t", "{{ f | upper }}", map[string]runtime.Value{
		"f": runtime.Obj(hostCallable{}),
	})
	wantSecurity(t, err, errors.SecFunction, "(non-template callable)")
}

// TestFn1SandboxStringifyGateShadowedJoin covers B12 against the one shape
// that could try to route around it: a HOST filter that shadows the coercing
// name join and publishes Fn1. The fast call still keys the string-coercion
// gate on the filter name over the piped value, so a collection carrying a
// host object without an allowed Stringify member is denied before the
// filter runs.
func TestFn1SandboxStringifyGateShadowedJoin(t *testing.T) {
	eng := sandboxStub(nil, sandbox.NewPolicy(sandbox.AllowFilters("join")))
	joinLike := func(ctx context.Context, v runtime.Value) (runtime.Value, error) {
		out := ""
		if v.Kind() == runtime.KArray && v.AsArray() != nil {
			for _, p := range v.AsArray().Pairs() {
				s, err := runtime.ToText(p.Val)
				if err != nil {
					return runtime.Null(), err
				}
				out += s
			}
		}
		return runtime.Str(out), nil
	}
	eng.exts.AddFilter(ext.NewFilter1("join", joinLike))

	xs := runtime.NewArray()
	xs.SetStr("u", runtime.Obj(&hostEntity{name: "ada"}))
	err := renderErr(t, eng, "t", "{{ xs | join }}", map[string]runtime.Value{
		"xs": runtime.Arr(xs),
	})
	wantSecurity(t, err, errors.SecMethod, "Stringify")

	// With Stringify allowed the same fast call renders, proving the denial
	// above came from the gate, not from the shadow being unreachable.
	eng2 := sandboxStub(nil, sandbox.NewPolicy(
		sandbox.AllowFilters("join"),
		sandbox.AllowMethods("Entity", "Stringify"),
	))
	eng2.exts.AddFilter(ext.NewFilter1("join", joinLike))
	got, err := renderStubAt(t, eng2, "{{ xs | join }}", map[string]runtime.Value{
		"xs": runtime.Arr(xs),
	})
	if err != nil || got != "ada" {
		t.Fatalf("allowed stringify through shadowed join: got %q err %v", got, err)
	}
}

// TestFn1SandboxAuditedNamesStillPhaseChecked re-runs the Phase-1 allowlist
// over an audited fast-call name: the fast call changes dispatch, never the
// sandbox's per-render callable check, so an unlisted upper is denied before
// the template body runs.
func TestFn1SandboxAuditedNamesStillPhaseChecked(t *testing.T) {
	eng := sandboxStub(nil, sandbox.NewPolicy())
	err := renderErr(t, eng, "t", "{{ s | upper }}", map[string]runtime.Value{
		"s": runtime.Str("x"),
	})
	wantSecurity(t, err, errors.SecFilter, "upper")
}
