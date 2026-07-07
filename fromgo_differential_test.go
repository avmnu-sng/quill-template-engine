package quill

// OLD-vs-NEW differential for the lean FromGo marshaler: every case runs the
// same input through the verbatim pre-plan oracle (fromgo_oracle_test.go) and
// through runtime.FromGo, then requires agreement on runtime.Equal, on a
// kind-and-key-order strict walk, on error presence, and on error text. The
// corpus variant additionally renders the conformance fixtures from both
// marshaled variable sets and byte-diffs the outputs, because green unit
// equality has historically been too weak a bar for value-territory changes.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/loader"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// strictSame is the differential comparator: kind-exact, payload-exact, and
// key-order-exact. runtime.Equal alone bridges Int/Float and normalizes Safe,
// which could mask a divergence where the two marshalers disagree on kind, so
// the byte-identity bar needs this stronger walk on top of Equal.
func strictSame(a, b runtime.Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case runtime.KNull:
		return true
	case runtime.KBool:
		return a.B == b.B
	case runtime.KInt:
		return a.I == b.I
	case runtime.KFloat:
		return a.F == b.F
	case runtime.KStr, runtime.KSafe:
		return a.S == b.S
	case runtime.KArray:
		ap, bp := a.Arr.Pairs(), b.Arr.Pairs()
		if len(ap) != len(bp) {
			return false
		}
		for i := range ap {
			if !strictSame(ap[i].Key, bp[i].Key) || !strictSame(ap[i].Val, bp[i].Val) {
				return false
			}
		}
		return true
	case runtime.KObject:
		// Object passthrough must preserve identity, including a typed-nil
		// pointer carried into the Object interface.
		return a.Obj == b.Obj
	default:
		return false
	}
}

// diffFromGo drives one input through both marshalers and fails on any
// disagreement in value, error presence, or error text.
func diffFromGo(t *testing.T, label string, in any) {
	t.Helper()
	ov, oerr := oracleFromGo(in)
	nv, nerr := runtime.FromGo(in)
	if (oerr == nil) != (nerr == nil) {
		t.Fatalf("%s: error divergence:\n oracle: %v\n new:    %v", label, oerr, nerr)
	}
	if oerr != nil {
		if oerr.Error() != nerr.Error() {
			t.Fatalf("%s: error text divergence:\n oracle: %s\n new:    %s", label, oerr, nerr)
		}
		return
	}
	if !runtime.Equal(ov, nv) {
		t.Fatalf("%s: Equal divergence:\n oracle: %+v\n new:    %+v", label, ov, nv)
	}
	if !strictSame(ov, nv) {
		t.Fatalf("%s: strict divergence:\n oracle: %+v\n new:    %+v", label, ov, nv)
	}
}

// DiffHost is a host object whose POINTER type implements runtime.Object, so
// the matrix can attack the passthrough corners a method-set gate could get
// wrong: a nil *DiffHost member must still pass through as a typed-nil Object
// (the probe matches the pointer type), while a DiffHost VALUE member must
// marshal as a plain struct (the value method set does not implement Object).
type DiffHost struct {
	// Label is the one exported field a struct-marshal of DiffHost emits.
	Label string
}

// GetField satisfies runtime.Object on *DiffHost.
func (h *DiffHost) GetField(string) (runtime.Value, bool) {
	return runtime.Null(), false
}

// CallMethod satisfies runtime.Object on *DiffHost.
func (h *DiffHost) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), nil
}

// The matrix struct shapes. Tags, embedding, pointer chains, and unexported
// members are chosen to hit every branch of the struct planner: quill-vs-json
// precedence, "-" skips, tagged-embedded nesting, nil-pointer-embedded
// fallback, unexported-embedded promotion, and passthrough-typed fields.
type diffBase struct {
	A int `quill:"a"`
	B string
}

type diffTags struct {
	QName     string `quill:"qn"`
	JName     string `json:"jn,omitempty"`
	Both      string `quill:"q2" json:"j2"`
	QSkip     string `quill:"-"`
	JSkip     string `json:"-"`
	QEmptyTag string `quill:","`
	Plain     int
	hidden    string //nolint:unused // exercises the unexported skip
}

type diffValueEmbed struct {
	diffBase
	C int `quill:"c"`
}

type diffPtrEmbed struct {
	*diffBase
	C int `quill:"c"`
}

type diffTaggedEmbed struct {
	diffBase `quill:"base"`
	C        int `quill:"c"`
}

type diffHostEmbed struct {
	*DiffHost
	C int `quill:"c"`
}

type diffQuillFields struct {
	V  runtime.Value
	A  *runtime.Array
	O  runtime.Object
	N  *runtime.Array
	PV *runtime.Value
	H  *DiffHost
	HV DiffHost
	I  any
}

// diffROInner is embedded through an unexported field, so its members reach the
// marshalers with reflect's read-only flag set and the boxing probe disabled;
// a runtime.Value in that position marshals through its own exported fields on
// both sides.
type diffROInner struct {
	X int
	V runtime.Value
}

type diffROOuter struct {
	diffROInner
	W int
}

// diffNode chains by pointer so the plan cache proves out on a recursive type.
type diffNode struct {
	Val  int       `quill:"val"`
	Next *diffNode `quill:"next"`
}

// DiffOuro embeds a pointer to itself: plan construction must terminate on the
// type cycle and the value walk must terminate on the nil chain, with the inner
// flatten's fallback key colliding into the outer field set.
type DiffOuro struct {
	*DiffOuro
	X int `quill:"x"`
}

// TestFromGoDifferentialTypeMatrix byte-checks the two marshalers over the
// hand-picked adversarial corners of the Go type surface.
func TestFromGoDifferentialTypeMatrix(t *testing.T) {
	n := 42
	pn := &n
	s := "deep"
	ps := &s
	var nilIntPtr *int
	var nilHost *DiffHost
	host := &DiffHost{Label: "live"}
	handArr := runtime.NewList(runtime.Int(1), runtime.Str("two"))
	handVal := runtime.Str("kept")
	node := &diffNode{Val: 1, Next: &diffNode{Val: 2, Next: nil}}
	ouro := DiffOuro{DiffOuro: &DiffOuro{X: 7}, X: 9}

	cases := []struct {
		label string
		in    any
	}{
		{"nil", nil},
		{"bool", true},
		{"int", 7},
		{"int8", int8(-8)},
		{"int64-max", int64(math.MaxInt64)},
		{"uint", uint(9)},
		{"uintptr", uintptr(77)},
		{"uint64-overflow", uint64(math.MaxUint64)},
		{"float32", float32(1.5)},
		{"float64", 2.25},
		{"string", "hi"},
		{"json-number", json.Number("42.5")},
		{"named-string", runtime.Kind(3).String()},
		{"ptr-int", pn},
		{"ptr-ptr-string", &ps},
		{"nil-ptr", nilIntPtr},
		{"slice-int", []int{1, 2, 3}},
		{"slice-empty", []int{}},
		{"slice-nil", []string(nil)},
		{"slice-any-mixed", []any{1, "two", true, 3.5, nil, handVal, handArr, []any{4}}},
		{"slice-slice", [][]int{{1}, {2, 3}}},
		{"go-array", [3]int{10, 20, 30}},
		{"slice-values", []runtime.Value{runtime.Int(1), runtime.Str("x")}},
		{"slice-arrays-with-nil", []*runtime.Array{handArr, nil}},
		{"slice-host-ptrs", []*DiffHost{host, nil}},
		{"slice-uint64-overflow", []uint64{1, math.MaxUint64}},
		{"slice-maps", []map[string]int{{"k": 1}, nil}},
		{"map-string-any", map[string]any{"g": 3, "a": 1, "b": handVal}},
		{"map-string-int", map[string]int{"x": 1, "y": 2}},
		{"map-int-sparse", map[int]string{-3: "a", 10: "b", 2: "c"}},
		{"map-int-dense", map[int]string{0: "a", 1: "b", 2: "c"}},
		{"map-int8", map[int8]bool{-1: true, 5: false}},
		{"map-uint", map[uint]string{0: "z", 9: "n"}},
		{"map-uint64-key-overflow", map[uint64]int{math.MaxUint64: 1}},
		{"map-float-key", map[float64]int{1.5: 2}},
		{"map-any-keys", map[any]int{"a": 1, 7: 2}},
		{"map-any-nil-key", map[any]int{nil: 1}},
		{"map-empty", map[string]int{}},
		{"map-nil", map[string]int(nil)},
		{"map-value-elems", map[string]runtime.Value{"v": runtime.Bool(false)}},
		{"map-array-elems", map[string]*runtime.Array{"a": handArr, "n": nil}},
		{"map-host-elems", map[string]*DiffHost{"h": host, "n": nilHost}},
		{"struct-empty", struct{}{}},
		{"struct-tags", diffTags{QName: "q", JName: "j", Both: "b", QSkip: "s1", JSkip: "s2", QEmptyTag: "e", Plain: 3, hidden: "h"}},
		{"struct-embed-value", diffValueEmbed{diffBase: diffBase{A: 1, B: "b"}, C: 2}},
		{"struct-embed-ptr", diffPtrEmbed{diffBase: &diffBase{A: 1, B: "b"}, C: 2}},
		{"struct-embed-ptr-nil", diffPtrEmbed{diffBase: nil, C: 2}},
		{"struct-embed-tagged", diffTaggedEmbed{diffBase: diffBase{A: 1, B: "b"}, C: 2}},
		{"struct-embed-host", diffHostEmbed{DiffHost: host, C: 2}},
		{"struct-embed-host-nil", diffHostEmbed{DiffHost: nil, C: 2}},
		{"struct-quill-fields", diffQuillFields{V: handVal, A: handArr, O: host, N: nil, PV: &handVal, H: host, HV: DiffHost{Label: "v"}, I: handArr}},
		{"struct-quill-fields-nils", diffQuillFields{H: nilHost, I: nil}},
		{"struct-ro-promotion", diffROOuter{diffROInner: diffROInner{X: 5, V: runtime.Str("ro")}, W: 6}},
		{"struct-recursive-chain", node},
		{"struct-self-embed", ouro},
		{"struct-self-embed-nil", DiffOuro{X: 3}},
		{"passthrough-value", handVal},
		{"passthrough-array", handArr},
		{"passthrough-object", host},
		{"unsupported-chan", make(chan int)},
		{"unsupported-func", func() {}},
		{"unsupported-complex64", complex64(1 + 2i)},
		{"unsupported-complex128", complex128(3 + 4i)},
		{"unsupported-nested-slice", []any{1, make(chan int)}},
		{"unsupported-nested-map", map[string]any{"ok": 1, "bad": func() {}}},
		{"unsupported-struct-field", struct{ C chan int }{C: make(chan int)}},
		{"struct-field-uint64-overflow", struct{ U uint64 }{U: math.MaxUint64}},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			diffFromGo(t, c.label, c.in)
		})
	}
}

// The generator's struct alphabet: fixed shapes with randomized content, nil
// pointers, and passthrough-typed members.
type genBase struct {
	A int    `quill:"a"`
	B string `json:"b"`
}

type genRow struct {
	genBase
	P    *genBase       `quill:"p"`
	Name string         `quill:"name"`
	Tags []string       `quill:"tags"`
	Meta map[string]any `quill:"meta"`
	V    runtime.Value  `quill:"v"`
	Skip string         `quill:"-"`
	hid  int            //nolint:unused // exercises the unexported skip
}

type genEmb struct {
	*genBase
	C float64 `quill:"c"`
}

// genValue builds a random Go value from a bounded shape alphabet. The
// generator favors the structures the planner rewrote (structs, slices, maps,
// pointers) over bare scalars.
func genValue(r *rand.Rand, depth int) any {
	if depth <= 0 {
		switch r.Intn(8) {
		case 0:
			return r.Intn(1000)
		case 1:
			return -r.Int63()
		case 2:
			return r.Float64() * 100
		case 3:
			return fmt.Sprintf("s%d", r.Intn(100))
		case 4:
			return r.Intn(2) == 0
		case 5:
			return nil
		case 6:
			return uint16(r.Intn(65536))
		default:
			return json.Number(strconv.Itoa(r.Intn(999)))
		}
	}
	switch r.Intn(10) {
	case 0:
		n := r.Intn(5)
		s := make([]any, n)
		for i := range s {
			s[i] = genValue(r, depth-1)
		}
		return s
	case 1:
		n := r.Intn(5)
		s := make([]int, n)
		for i := range s {
			s[i] = r.Intn(100)
		}
		return s
	case 2:
		n := r.Intn(4)
		m := make(map[string]any, n)
		for i := 0; i < n; i++ {
			m[fmt.Sprintf("k%d", r.Intn(10))] = genValue(r, depth-1)
		}
		return m
	case 3:
		n := r.Intn(4)
		m := make(map[int]any, n)
		for i := 0; i < n; i++ {
			m[r.Intn(20)-5] = genValue(r, depth-1)
		}
		return m
	case 4:
		if r.Intn(3) == 0 {
			var p *genBase
			return p
		}
		return &genBase{A: r.Intn(100), B: fmt.Sprintf("b%d", r.Intn(10))}
	case 5:
		row := genRow{
			genBase: genBase{A: r.Intn(100), B: "base"},
			Name:    fmt.Sprintf("n%d", r.Intn(10)),
			Tags:    []string{"x", fmt.Sprintf("t%d", r.Intn(5))},
			V:       runtime.Int(int64(r.Intn(50))),
			Skip:    "never",
			hid:     1,
		}
		if r.Intn(2) == 0 {
			row.P = &genBase{A: -r.Intn(9), B: "p"}
		}
		if r.Intn(2) == 0 {
			row.Meta = map[string]any{"d": genValue(r, depth-1)}
		}
		return row
	case 6:
		e := genEmb{C: r.Float64()}
		if r.Intn(2) == 0 {
			e.genBase = &genBase{A: r.Intn(100), B: "e"}
		}
		return e
	case 7:
		return runtime.Arr(runtime.NewList(runtime.Int(int64(r.Intn(10))), runtime.Str("hand")))
	case 8:
		return runtime.Str(fmt.Sprintf("v%d", r.Intn(100)))
	default:
		return genValue(r, 0)
	}
}

// TestFromGoDifferentialRandomShapes fuzzes randomized nested shapes through
// both marshalers under a fixed seed, so a planner regression on any shape
// combination reproduces deterministically.
func TestFromGoDifferentialRandomShapes(t *testing.T) {
	r := rand.New(rand.NewSource(0x51))
	for i := 0; i < 500; i++ {
		in := genValue(r, 3)
		diffFromGo(t, fmt.Sprintf("shape-%d", i), in)
	}
}

// TestFromGoDifferentialConformanceVars routes every conformance fixture's
// data.json through both marshalers (as native Go maps decoded by
// encoding/json) and renders the fixture from each variable set: the outputs
// must be byte-identical and any render errors must match textually. This is
// the render-level differential the value-territory history demands on top of
// tree equality.
func TestFromGoDifferentialConformanceVars(t *testing.T) {
	root := filepath.Join("testdata", "conformance")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read conformance dir: %v", err)
	}
	ran := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "data.json"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read data.json in %s: %v", e.Name(), err)
		}
		ran++
		t.Run(e.Name(), func(t *testing.T) {
			var data map[string]any
			if err := json.Unmarshal(raw, &data); err != nil {
				t.Fatalf("decode data.json: %v", err)
			}
			tmpls, main, _, opts, _ := fixtureSetup(t, dir)

			oldVars := make(map[string]runtime.Value, len(data))
			newVars := make(map[string]runtime.Value, len(data))
			for name, v := range data {
				diffFromGo(t, "var "+name, v)
				ov, err := oracleFromGo(v)
				if err != nil {
					t.Fatalf("oracle marshal of %q: %v", name, err)
				}
				nv, err := runtime.FromGo(v)
				if err != nil {
					t.Fatalf("marshal of %q: %v", name, err)
				}
				oldVars[name] = ov
				newVars[name] = nv
			}

			oldEnv := New(loader.NewArrayLoader(tmpls), opts...)
			newEnv := New(loader.NewArrayLoader(tmpls), opts...)
			oldOut, oldErr := oldEnv.Render(main, oldVars)
			newOut, newErr := newEnv.Render(main, newVars)
			if (oldErr == nil) != (newErr == nil) {
				t.Fatalf("render error divergence:\n oracle: %v\n new:    %v", oldErr, newErr)
			}
			if oldErr != nil && oldErr.Error() != newErr.Error() {
				t.Fatalf("render error text divergence:\n oracle: %s\n new:    %s", oldErr, newErr)
			}
			if oldOut != newOut {
				t.Fatalf("render byte divergence:\n--- oracle ---\n%q\n--- new ---\n%q", oldOut, newOut)
			}
		})
	}
	if ran == 0 {
		t.Fatal("no conformance fixtures carry a data.json")
	}
}

// TestFromGoDifferentialConcurrentPlans races many goroutines through FromGo
// over struct types that are cold in the plan cache (they are declared inside
// this function, so the first marshal happens under contention) and checks
// every result against the oracle. Run with -race this proves the plan cache's
// build-and-publish path is data-race free.
func TestFromGoDifferentialConcurrentPlans(t *testing.T) {
	type ccBase struct {
		A int `quill:"a"`
	}
	type ccRow struct {
		ccBase
		P    *ccBase       `quill:"p"`
		Name string        `quill:"name"`
		V    runtime.Value `quill:"v"`
	}
	type ccWide struct {
		R1 ccRow   `quill:"r1"`
		R2 *ccRow  `quill:"r2"`
		L  []ccRow `quill:"l"`
	}
	row := ccRow{ccBase: ccBase{A: 1}, P: &ccBase{A: 2}, Name: "n", V: runtime.Str("v")}
	inputs := []any{
		row,
		ccRow{Name: "nil-embed-ptr-stays-null"},
		ccWide{R1: row, R2: &row, L: []ccRow{row, {Name: "x"}}},
		[]ccRow{row, row, row},
	}
	want := make([]runtime.Value, len(inputs))
	for i, in := range inputs {
		w, err := oracleFromGo(in)
		if err != nil {
			t.Fatalf("oracle marshal %d: %v", i, err)
		}
		want[i] = w
	}

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rep := 0; rep < 100; rep++ {
				for i, in := range inputs {
					got, err := runtime.FromGo(in)
					if err != nil {
						t.Errorf("concurrent marshal %d: %v", i, err)
						return
					}
					if !strictSame(want[i], got) {
						t.Errorf("concurrent marshal %d diverged from oracle", i)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
