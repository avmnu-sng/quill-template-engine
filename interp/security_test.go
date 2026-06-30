package interp

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/parse"
	"github.com/avmnu-sng/quill-template-engine/runtime"
	"github.com/avmnu-sng/quill-template-engine/sandbox"
)

// hostEntity is a host Object with a registered type name, a field, a method,
// and a Stringify hook, for the sandbox member-access and string-coercion tests.
type hostEntity struct{ name string }

func (h *hostEntity) GetField(name string) (runtime.Value, bool) {
	switch name {
	case "name":
		return runtime.Str(h.name), true
	case "secret":
		return runtime.Str("xyzzy"), true
	default:
		return runtime.Null(), false
	}
}

func (h *hostEntity) CallMethod(name string, args []runtime.Value) (runtime.Value, error) {
	switch name {
	case "greet":
		return runtime.Str("hi " + h.name), nil
	case "danger":
		return runtime.Str("boom"), nil
	default:
		return runtime.Null(), errNotFound(name)
	}
}

func (h *hostEntity) ClassName() string          { return "Entity" }
func (h *hostEntity) Stringify() (string, error) { return h.name, nil }

// hostCallable is a non-arrow callable smuggled in as context data, to exercise
// the arrow-gating rule (B13): a higher-order filter under the sandbox must
// reject it.
type hostCallable struct{}

func (hostCallable) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }
func (hostCallable) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), nil
}
func (hostCallable) Invoke(args []runtime.Value) (runtime.Value, error) {
	return runtime.Bool(true), nil
}

// sandboxStub builds a stub engine with the sandbox forced on globally and the
// given policy, so a render exercises Phase-1 and the runtime gates.
func sandboxStub(tmpls map[string]string, p *sandbox.Policy) *stubEngine {
	s := newStub(tmpls)
	s.policy = p
	s.sandboxOn = true
	return s
}

func renderErr(t *testing.T, eng *stubEngine, name, body string, vars map[string]runtime.Value) error {
	t.Helper()
	mod, err := parse.ParseString(name, body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	_, rerr := Render(eng, Prepare(name, mod), vars)
	return rerr
}

// wantSecurity asserts err is a *errors.Security of the given class naming the
// offending element.
func wantSecurity(t *testing.T, err error, class errors.SecurityClass, name string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a security error naming %q, got nil", name)
	}
	var sec *errors.Security
	if !stderrors.As(err, &sec) {
		t.Fatalf("error is not *errors.Security: %v", err)
	}
	if sec.Class != class {
		t.Errorf("class = %v, want %v (err: %v)", sec.Class, class, err)
	}
	if name != "" && sec.Name != name {
		t.Errorf("name = %q, want %q", sec.Name, name)
	}
	if errors.KindOf(err) != errors.KindSecurity {
		t.Errorf("KindOf = %v, want security", errors.KindOf(err))
	}
}

// TestSandboxTagAllowDeny covers the Phase-1 tag allowlist (B1): an allowed tag
// renders, a disallowed one raises SecurityTag naming the keyword.
func TestSandboxTagAllowDeny(t *testing.T) {
	// for is allowed -> renders.
	pol := &sandbox.Policy{Tags: map[string]bool{"for": true}}
	eng := sandboxStub(nil, pol)
	got, err := renderStubAt(t, eng, "@for x in xs {\n{{ x }}\n@}\n",
		map[string]runtime.Value{"xs": runtime.Arr(runtime.NewList(runtime.Int(1), runtime.Int(2)))})
	if err != nil {
		t.Fatalf("allowed @for should render, got %v", err)
	}
	if !strings.Contains(got, "1") || !strings.Contains(got, "2") {
		t.Errorf("unexpected output %q", got)
	}

	// if is NOT allowed -> denied.
	err = renderErr(t, eng, "t", "@if true {\nX\n@}\n", nil)
	wantSecurity(t, err, errors.SecTag, "if")
}

// TestSandboxFilterAllowDeny covers the filter allowlist (B2).
func TestSandboxFilterAllowDeny(t *testing.T) {
	pol := &sandbox.Policy{Filters: map[string]bool{"upper": true}}
	eng := sandboxStub(nil, pol)
	if got, err := renderStubAt(t, eng, "{{ s | upper }}",
		map[string]runtime.Value{"s": runtime.Str("hi")}); err != nil || got != "HI" {
		t.Fatalf("allowed filter: got %q err %v", got, err)
	}
	err := renderErr(t, eng, "t", "{{ s | lower }}", map[string]runtime.Value{"s": runtime.Str("HI")})
	wantSecurity(t, err, errors.SecFilter, "lower")
}

// TestSandboxFunctionAllowDeny covers the function allowlist (B3), including the
// `..` range operator counting as the range function (B8).
func TestSandboxFunctionAllowDeny(t *testing.T) {
	pol := &sandbox.Policy{
		Tags:      map[string]bool{"for": true},
		Functions: map[string]bool{"range": true, "max": true},
	}
	eng := sandboxStub(nil, pol)
	// range allowed -> `1..3` works.
	if _, err := renderStubAt(t, eng, "@for x in 1..3 {\n{{ x }}\n@}\n", nil); err != nil {
		t.Fatalf("allowed range via ..: %v", err)
	}
	// max allowed -> max() works.
	if got, err := renderStubAt(t, eng, "{{ max(1, 2) }}", nil); err != nil || got != "2" {
		t.Fatalf("allowed function: got %q err %v", got, err)
	}
	// min not allowed -> denied.
	err := renderErr(t, eng, "t", "{{ min(1, 2) }}", nil)
	wantSecurity(t, err, errors.SecFunction, "min")

	// .. with range NOT allowed -> denied as the range function.
	eng2 := sandboxStub(nil, &sandbox.Policy{Tags: map[string]bool{"for": true}})
	err = renderErr(t, eng2, "t", "@for x in 1..3 {\n{{ x }}\n@}\n", nil)
	wantSecurity(t, err, errors.SecFunction, "range")
}

// TestSandboxMethodAllowDeny covers runtime method gating via the type-graph
// (B4/B10), with case-sensitive matching.
func TestSandboxMethodAllowDeny(t *testing.T) {
	g := sandbox.NewTypeGraph()
	pol := &sandbox.Policy{
		Methods: map[string]map[string]bool{"Entity": {"greet": true}},
		Graph:   g,
	}
	eng := sandboxStub(nil, pol)
	u := runtime.Obj(&hostEntity{name: "ada"})

	if got, err := renderStubAt(t, eng, "{{ u.greet() }}", map[string]runtime.Value{"u": u}); err != nil || got != "hi ada" {
		t.Fatalf("allowed method: got %q err %v", got, err)
	}
	err := renderErr(t, eng, "t", "{{ u.danger() }}", map[string]runtime.Value{"u": u})
	wantSecurity(t, err, errors.SecMethod, "danger")
	var sec *errors.Security
	stderrors.As(err, &sec)
	if sec.Type != "Entity" {
		t.Errorf("method error type = %q, want Entity", sec.Type)
	}
}

// TestSandboxPropertyAllowDeny covers runtime property gating (B5/B11).
func TestSandboxPropertyAllowDeny(t *testing.T) {
	pol := &sandbox.Policy{
		Properties: map[string]map[string]bool{"Entity": {"name": true}},
	}
	eng := sandboxStub(nil, pol)
	u := runtime.Obj(&hostEntity{name: "ada"})

	if got, err := renderStubAt(t, eng, "{{ u.name }}", map[string]runtime.Value{"u": u}); err != nil || got != "ada" {
		t.Fatalf("allowed property: got %q err %v", got, err)
	}
	err := renderErr(t, eng, "t", "{{ u.secret }}", map[string]runtime.Value{"u": u})
	wantSecurity(t, err, errors.SecProperty, "secret")
}

// TestSandboxStringifyGate covers the string-coercion gate (B12): coercing a
// host object to text requires its Stringify member be allowed.
func TestSandboxStringifyGate(t *testing.T) {
	// Stringify allowed -> interpolation works.
	pol := &sandbox.Policy{Methods: map[string]map[string]bool{"Entity": {"Stringify": true}}}
	eng := sandboxStub(nil, pol)
	u := runtime.Obj(&hostEntity{name: "ada"})
	if got, err := renderStubAt(t, eng, "{{ u }}", map[string]runtime.Value{"u": u}); err != nil || got != "ada" {
		t.Fatalf("allowed stringify: got %q err %v", got, err)
	}
	// Stringify NOT allowed -> denied at the coercion site.
	eng2 := sandboxStub(nil, &sandbox.Policy{})
	err := renderErr(t, eng2, "t", "{{ u }}", map[string]runtime.Value{"u": u})
	wantSecurity(t, err, errors.SecMethod, "Stringify")
}

// TestSandboxArrowGating covers B13: a higher-order filter under the sandbox
// rejects a non-template (host) callable, while a template-defined arrow works.
func TestSandboxArrowGating(t *testing.T) {
	pol := &sandbox.Policy{Filters: map[string]bool{"map": true, "join": true}}
	eng := sandboxStub(nil, pol)
	xs := runtime.Arr(runtime.NewList(runtime.Int(1), runtime.Int(2)))

	// A template-defined arrow is allowed and runs.
	if got, err := renderStubAt(t, eng, "{{ xs | map(x => x) | join(\",\") }}",
		map[string]runtime.Value{"xs": xs}); err != nil || got != "1,2" {
		t.Fatalf("template arrow should be allowed: got %q err %v", got, err)
	}

	// A smuggled host callable is rejected as a non-template callable.
	err := renderErr(t, eng, "t", "{{ xs | map(f) }}", map[string]runtime.Value{
		"xs": xs,
		"f":  runtime.Obj(hostCallable{}),
	})
	wantSecurity(t, err, errors.SecFunction, "(non-template callable)")
}

// TestSandboxRegionForcesSandbox covers the @sandbox region (B7): outside the
// region the unsandboxed (no policy) engine renders freely; inside, the body is
// checked against the policy. The region activates the sandbox locally even
// when the engine's global gate is off.
func TestSandboxRegionForcesSandbox(t *testing.T) {
	// Engine is NOT globally sandboxed, but has a policy allowing only `if`.
	eng := newStub(nil)
	eng.policy = &sandbox.Policy{Tags: map[string]bool{"if": true}}

	// Outside any region, an unlisted filter renders fine (sandbox off).
	if got, err := renderStubAt(t, eng, "{{ s | upper }}",
		map[string]runtime.Value{"s": runtime.Str("hi")}); err != nil || got != "HI" {
		t.Fatalf("outside region should be unsandboxed: got %q err %v", got, err)
	}

	// Inside @sandbox, the disallowed `upper` filter is denied.
	body := "@sandbox {\n{{ s | upper }}\n@}\n"
	err := renderErr(t, eng, "t", body, map[string]runtime.Value{"s": runtime.Str("hi")})
	wantSecurity(t, err, errors.SecFilter, "upper")

	// Inside @sandbox, an allowed construct renders.
	ok := "@sandbox {\n@if true {\nX\n@}\n@}\n"
	if _, err := renderStubAt(t, eng, ok, nil); err != nil {
		t.Fatalf("allowed @if inside region: %v", err)
	}
}

// TestSandboxedInclude covers the per-include sandboxed flag and B16: a render
// already sandboxed keeps an included template sandboxed.
func TestSandboxedInclude(t *testing.T) {
	tmpls := map[string]string{
		"child.ql": "{{ s | upper }}",
	}
	// Globally sandboxed, policy allows include but NOT upper. The child include
	// stays sandboxed (B16), so its `upper` is denied.
	pol := &sandbox.Policy{
		Tags:      map[string]bool{"include": true},
		Functions: map[string]bool{"include": true},
	}
	eng := sandboxStub(tmpls, pol)
	err := renderErr(t, eng, "t", "@include \"child.ql\"\n",
		map[string]runtime.Value{"s": runtime.Str("hi")})
	wantSecurity(t, err, errors.SecFilter, "upper")
}

// TestSandboxStringifyGateConcat covers the string-coercion gate at the `~`
// concat site (B12, spec 04 Section 8.3): coercing a host object to text in a
// concat consults the policy just like an interpolation does. Without the gate a
// sandboxed template with an empty policy could read the object's Stringify
// output via `{{ "" ~ u }}` -- a sandbox escape.
func TestSandboxStringifyGateConcat(t *testing.T) {
	u := runtime.Obj(&hostEntity{name: "ada"})

	// Empty policy -> the concat coercion is denied.
	eng := sandboxStub(nil, &sandbox.Policy{})
	err := renderErr(t, eng, "t", `{{ "" ~ u }}`, map[string]runtime.Value{"u": u})
	wantSecurity(t, err, errors.SecMethod, "Stringify")

	// Stringify allowed -> the concat renders.
	pol := &sandbox.Policy{Methods: map[string]map[string]bool{"Entity": {"Stringify": true}}}
	eng2 := sandboxStub(nil, pol)
	if got, err := renderStubAt(t, eng2, `{{ "x" ~ u }}`, map[string]runtime.Value{"u": u}); err != nil || got != "xada" {
		t.Fatalf("allowed stringify in concat: got %q err %v", got, err)
	}
}

// TestSandboxStringifyGateJoin covers the string-coercion gate at the join
// argument site (B12): join coerces each element of its piped collection to text
// inside ext, beyond the policy's reach, so the interp gates the elements at the
// filter choke point. Without the gate `{{ xs | join(",") }}` over a host object
// element leaks its Stringify output under an empty policy.
func TestSandboxStringifyGateJoin(t *testing.T) {
	u := runtime.Obj(&hostEntity{name: "ada"})
	xs := runtime.Arr(runtime.NewList(u))

	// join allowed but Stringify not -> the element coercion is denied.
	eng := sandboxStub(nil, &sandbox.Policy{Filters: map[string]bool{"join": true}})
	err := renderErr(t, eng, "t", `{{ xs | join(",") }}`, map[string]runtime.Value{"xs": xs})
	wantSecurity(t, err, errors.SecMethod, "Stringify")

	// Both allowed -> renders.
	pol := &sandbox.Policy{
		Filters: map[string]bool{"join": true},
		Methods: map[string]map[string]bool{"Entity": {"Stringify": true}},
	}
	eng2 := sandboxStub(nil, pol)
	if got, err := renderStubAt(t, eng2, `{{ xs | join(",") }}`, map[string]runtime.Value{"xs": xs}); err != nil || got != "ada" {
		t.Fatalf("allowed stringify in join: got %q err %v", got, err)
	}

	// A scalar (non-object) collection is unaffected by the gate.
	scalars := runtime.Arr(runtime.NewList(runtime.Int(1), runtime.Int(2)))
	eng3 := sandboxStub(nil, &sandbox.Policy{Filters: map[string]bool{"join": true}})
	if got, err := renderStubAt(t, eng3, `{{ xs | join(",") }}`, map[string]runtime.Value{"xs": scalars}); err != nil || got != "1,2" {
		t.Fatalf("scalar join under empty member policy: got %q err %v", got, err)
	}
}

// TestSandboxStringifyGateReplace covers the gate at a replace argument site
// (B12): the from->to pairs map is coerced inside ext, so a host object used as a
// pair key or value is gated here.
func TestSandboxStringifyGateReplace(t *testing.T) {
	u := runtime.Obj(&hostEntity{name: "ada"})
	pairs := runtime.NewArray()
	pairs.SetStr("x", u) // value is a host object -> coerced by replace.

	eng := sandboxStub(nil, &sandbox.Policy{Filters: map[string]bool{"replace": true}})
	err := renderErr(t, eng, "t", `{{ "x" | replace(m) }}`, map[string]runtime.Value{"m": runtime.Arr(pairs)})
	wantSecurity(t, err, errors.SecMethod, "Stringify")
}

// TestSandboxApplyArrowGating covers B13 on the @apply filter path: a smuggled
// host callable passed to a higher-order filter inside @apply is rejected just as
// the inline `| map(f)` form rejects it. Without the gate the two
// filter-application paths enforce the rule inconsistently.
func TestSandboxApplyArrowGating(t *testing.T) {
	pol := &sandbox.Policy{
		Tags:    map[string]bool{"apply": true},
		Filters: map[string]bool{"map": true},
	}
	eng := sandboxStub(nil, pol)
	err := renderErr(t, eng, "t", "@apply | map(f) {\nx\n@}\n", map[string]runtime.Value{
		"f": runtime.Obj(hostCallable{}),
	})
	wantSecurity(t, err, errors.SecFunction, "(non-template callable)")
}

// TestSandboxStrictUnknownType covers the strict-vs-lenient reporting difference
// (spec 04 Section 8.3): in strict mode a member access on a type the policy does
// not know at all reports a distinct unknown-type error, while lenient mode falls
// through to the ordinary per-member deny.
func TestSandboxStrictUnknownType(t *testing.T) {
	u := runtime.Obj(&hostEntity{name: "ada"}) // ClassName "Entity", unknown to an empty policy.

	// Lenient (default): the per-member property deny is reported.
	lenient := sandboxStub(nil, &sandbox.Policy{})
	err := renderErr(t, lenient, "t", "{{ u.secret }}", map[string]runtime.Value{"u": u})
	wantSecurity(t, err, errors.SecProperty, "secret")
	if got := err.Error(); !strings.Contains(got, "is not allowed by the sandbox policy") {
		t.Errorf("lenient message = %q, want per-member deny", got)
	}

	// Strict: the same access reports the unknown-type variant naming the type.
	strict := sandboxStub(nil, &sandbox.Policy{Strict: true})
	err = renderErr(t, strict, "t", "{{ u.secret }}", map[string]runtime.Value{"u": u})
	wantSecurity(t, err, errors.SecProperty, "secret")
	if got := err.Error(); !strings.Contains(got, "unknown to the sandbox policy") {
		t.Errorf("strict message = %q, want unknown-type variant", got)
	}

	// Strict but the type IS known (has a property entry) -> ordinary per-member
	// deny for the unlisted member, not the unknown-type variant.
	known := sandboxStub(nil, &sandbox.Policy{
		Strict:     true,
		Properties: map[string]map[string]bool{"Entity": {"name": true}},
	})
	err = renderErr(t, known, "t", "{{ u.secret }}", map[string]runtime.Value{"u": u})
	wantSecurity(t, err, errors.SecProperty, "secret")
	if got := err.Error(); strings.Contains(got, "unknown to the sandbox policy") {
		t.Errorf("strict+known message = %q, want per-member deny", got)
	}
}

// renderStubAt renders an ad-hoc template and returns output and error (the
// error-returning sibling of renderStub).
func renderStubAt(t *testing.T, eng *stubEngine, body string, vars map[string]runtime.Value) (string, error) {
	t.Helper()
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return Render(eng, Prepare("test", mod), vars)
}
