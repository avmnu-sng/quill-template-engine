package ext

import (
	"strings"

	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// Core builds an ExtensionSet holding the full spec-03 standard-library
// catalogue. The core subset (the most-used string/collection/math filters, the
// aggregate functions, and the basic tests) is registered here directly; the
// remaining catalogue (the higher-order collection filters, the encoding/date/
// source filters, the access/registry functions, and the remaining tests) is
// installed by registerStdlib below. The include/source/dump/
// template_from_string callables that need the engine handle are registered by
// the engine facade (they need the environment and loader), so they are NOT
// installed here.
func Core() *ExtensionSet {
	s := NewExtensionSet()
	registerCoreFilters(s)
	registerCoreFunctions(s)
	registerCoreTests(s)
	// registerStdlib installs the remaining spec-03 catalogue (the higher-order
	// collection filters, the math/encoding/date/source filters, the access and
	// registry functions, and the remaining tests) on top of the core subset.
	registerStdlib(s)
	return s
}

func registerCoreFilters(s *ExtensionSet) {
	addFilterFast1(s, NewFilter1("upper", filterUpper1))
	addFilterFast1(s, NewFilter1("lower", filterLower1))
	s.AddFilter(&Filter{Name: "default", Fn: filterDefault})
	addFilterFast1(s, NewFilter1("length", filterLength1))
	s.AddFilter(&Filter{Name: "join", Fn: filterJoin})
	addFilterFast1(s, &Filter{Name: "trim", Fn: filterTrim, Fn1: filterTrim1})
	s.AddFilter(&Filter{Name: "replace", Fn: filterReplace})
	addFilterFast1(s, NewFilter1("raw", filterRaw1))
	s.AddFilter(&Filter{Name: "escape", Fn: filterEscape})
	s.AddFilter(&Filter{Name: "e", Fn: filterEscape}) // alias
	addFilterFast1(s, NewFilter1("first", filterFirst1))
	addFilterFast1(s, NewFilter1("last", filterLast1))
	addFilterFast1(s, NewFilter1("keys", filterKeys1))
	addFilterFast1(s, &Filter{Name: "reverse", Fn: filterReverse, Fn1: filterReverse1})
	s.AddFilter(&Filter{Name: "sort", Fn: filterSortArrow})
	s.AddFilter(&Filter{Name: "merge", Fn: filterMerge})
	s.AddFilter(&Filter{Name: "slice", Fn: filterSlice})
}

// sandboxChokeFilters names the filters the interpreter's sandbox choke points
// treat specially by name: the string-coercion gate (B12) scans join/replace/
// split arguments, and the arrow-gating rule (B13) governs the callable
// arguments of the higher-order collection filters. The Fn1 fast call skips
// the argument-slice build those gates scan, so the audited Fn1 set must stay
// disjoint from these names; addFilterFast1 enforces the disjointness at
// registration time, before any template can render.
var sandboxChokeFilters = map[string]bool{
	"join":    true,
	"replace": true,
	"split":   true,
	"map":     true,
	"filter":  true,
	"sort":    true,
	"reduce":  true,
	"find":    true,
}

// addFilterFast1 registers a stdlib filter that carries an Fn1 fast call,
// panicking when the name collides with a sandbox choke-point filter. Core()
// runs on every engine construction, so a bad audited registration fails the
// process immediately rather than silently bypassing a sandbox gate.
func addFilterFast1(s *ExtensionSet, f *Filter) {
	if f.Fn1 != nil && sandboxChokeFilters[f.Name] {
		panic("ext: core filter " + f.Name + " is a sandbox choke point and must not carry an Fn1 fast call")
	}
	s.AddFilter(f)
}

func registerCoreFunctions(s *ExtensionSet) {
	s.AddFunction(&Function{Name: "range", Fn: fnRange})
	s.AddFunction(&Function{Name: "max", Fn: fnMax})
	s.AddFunction(&Function{Name: "min", Fn: fnMin})
}

func registerCoreTests(s *ExtensionSet) {
	// `is defined` is handled specially by the interpreter (it flips its operand
	// to existence-check mode and never evaluates it), so there is no Fn here; the
	// interpreter intercepts the name before any value reaches a Test.
	s.AddTest(&Test{Name: "null", Fn: testNull})
	s.AddTest(&Test{Name: "none", Fn: testNull}) // alias
	s.AddTest(&Test{Name: "empty", Fn: testEmpty})
	s.AddTest(&Test{Name: "even", Fn: testEven})
	s.AddTest(&Test{Name: "odd", Fn: testOdd})
	s.AddTest(&Test{Name: "iterable", Fn: testIterable})
	s.AddTest(&Test{Name: "same as", Fn: testSameAs})
	s.AddTest(&Test{Name: "same_as", Fn: testSameAs}) // canonical spelling
}

// --- argument helpers ---

// wantString coerces a value the same way ToText does, used by filters that
// declare a `string` parameter.
func wantString(v runtime.Value) (string, error) { return runtime.ToText(v) }

// arg returns the i-th element of args or the zero Null when absent.
func arg(args []runtime.Value, i int) runtime.Value {
	if i < len(args) {
		return args[i]
	}
	return runtime.Null()
}

// --- string filters ---

// filterUpper1 is the unary upper implementation; the pipe form and the fast
// call share it, so the two dispatch routes are one function.
func filterUpper1(v runtime.Value) (runtime.Value, error) {
	s, err := wantString(v)
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Str(strings.ToUpper(s)), nil
}

// filterLower1 is the unary lower implementation shared by both dispatch routes.
func filterLower1(v runtime.Value) (runtime.Value, error) {
	s, err := wantString(v)
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Str(strings.ToLower(s)), nil
}

// defaultTrimMask is trim's default character set: ASCII+Unicode whitespace
// (spec 03 Section 2.1), shared by the general and unary trim paths.
const defaultTrimMask = " \t\n\r\x00\x0B"

// filterTrim1 is trim's zero-extra-argument behavior (both sides, the default
// whitespace mask); filterTrim delegates the argument-less call here so the
// fast call and the general path cannot drift.
func filterTrim1(v runtime.Value) (runtime.Value, error) {
	s, err := wantString(v)
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Str(strings.Trim(s, defaultTrimMask)), nil
}

// filterTrim covers left, right, and both-side trimming: side in both/left/right
// (aliases b/l/r), mask defaults to ASCII+Unicode whitespace (spec 03 Section 2.1).
func filterTrim(args []runtime.Value) (runtime.Value, error) {
	if len(args) <= 1 {
		return filterTrim1(arg(args, 0))
	}
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	side, err := wantString(args[1])
	if err != nil {
		return runtime.Null(), err
	}
	mask := defaultTrimMask
	if len(args) > 2 {
		mask, err = wantString(args[2])
		if err != nil {
			return runtime.Null(), err
		}
	}
	switch side {
	case "both", "b", "":
		return runtime.Str(strings.Trim(s, mask)), nil
	case "left", "l":
		return runtime.Str(strings.TrimLeft(s, mask)), nil
	case "right", "r":
		return runtime.Str(strings.TrimRight(s, mask)), nil
	default:
		return runtime.Null(), errors.New(errors.KindRuntime,
			"trim side must be both/left/right, got %q", side)
	}
}

// filterReplace is strtr-style: longest-key-first, non-overlapping, single-pass,
// byte-level, via strings.Replacer so a replacement is never re-scanned (spec 03
// Section 2.5). The argument is a map<string,string>.
func filterReplace(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	pairsVal := arg(args, 1)
	if pairsVal.Kind != runtime.KArray || pairsVal.Arr == nil {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"replace expects a map of from->to pairs")
	}
	var oldnew []string
	for _, p := range pairsVal.Arr.Pairs() {
		from, err := runtime.ToText(p.Key)
		if err != nil {
			return runtime.Null(), err
		}
		to, err := runtime.ToText(p.Val)
		if err != nil {
			return runtime.Null(), err
		}
		oldnew = append(oldnew, from, to)
	}
	return runtime.Str(strings.NewReplacer(oldnew...).Replace(s)), nil
}

// --- safeness filters ---

// filterRaw1 marks content already-safe. Under the default (escaping off) it is
// a passthrough; the interpreter's escape pass leaves a Safe value untouched.
// Its effect is load-bearing only under an escape-on region (spec 03 Section
// 5.4). It is the unary implementation both dispatch routes share.
func filterRaw1(v runtime.Value) (runtime.Value, error) {
	s, err := runtime.ToText(v)
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Safe(s), nil
}

// filterEscape escapes for a named strategy (default html) and returns a Safe
// value. The six strategies are html, js, css, html_attr, html_attr_relaxed,
// and url (spec 03 Section 5.5). A value that is already Safe is returned
// unchanged.
func filterEscape(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	if v.Kind == runtime.KSafe {
		return v, nil
	}
	strategy := "html"
	if len(args) > 1 {
		s, err := wantString(args[1])
		if err != nil {
			return runtime.Null(), err
		}
		strategy = s
	}
	text, err := runtime.ToText(v)
	if err != nil {
		return runtime.Null(), err
	}
	out, err := Escape(strategy, text)
	if err != nil {
		return runtime.Null(), errors.New(errors.KindRuntime, "%s", err.Error())
	}
	return runtime.Safe(out), nil
}

// --- collection filters ---

// filterDefault yields the fallback when the piped value is Null (which the
// interpreter also produces for an undefined read under default's suppression);
// a defined non-null value, including 0 and "", is kept (spec 03 Section 2.7).
func filterDefault(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	if v.IsNull() {
		return arg(args, 1), nil
	}
	return v, nil
}

// filterLength1 returns string runes, collection count, or 1 for a scalar
// (spec 03 Section 2.2); the unary implementation both dispatch routes share.
func filterLength1(v runtime.Value) (runtime.Value, error) {
	switch v.Kind {
	case runtime.KStr, runtime.KSafe:
		return runtime.Int(int64(len([]rune(v.S)))), nil
	case runtime.KArray:
		if v.Arr == nil {
			return runtime.Int(0), nil
		}
		return runtime.Int(int64(v.Arr.Len())), nil
	case runtime.KObject:
		if c, ok := v.Obj.(runtime.Counter); ok {
			return runtime.Int(int64(c.Count())), nil
		}
		return runtime.Int(1), nil
	case runtime.KNull:
		return runtime.Int(0), nil
	default:
		return runtime.Int(1), nil
	}
}

// filterJoin joins a collection with glue, each element rendered by ToText, with
// an optional distinct glue before the last element (spec 03 Section 2.2).
func filterJoin(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	glue := ""
	if len(args) > 1 {
		g, err := wantString(args[1])
		if err != nil {
			return runtime.Null(), err
		}
		glue = g
	}
	hasFinal := false
	final := ""
	if len(args) > 2 && !args[2].IsNull() {
		f, err := wantString(args[2])
		if err != nil {
			return runtime.Null(), err
		}
		final = f
		hasFinal = true
	}
	var parts []string
	if v.Kind == runtime.KArray && v.Arr != nil {
		for _, p := range v.Arr.Pairs() {
			t, err := runtime.ToText(p.Val)
			if err != nil {
				return runtime.Null(), err
			}
			parts = append(parts, t)
		}
	} else if !v.IsNull() {
		t, err := runtime.ToText(v)
		if err != nil {
			return runtime.Null(), err
		}
		parts = append(parts, t)
	}
	if hasFinal && len(parts) >= 2 {
		head := strings.Join(parts[:len(parts)-1], glue)
		return runtime.Str(head + final + parts[len(parts)-1]), nil
	}
	return runtime.Str(strings.Join(parts, glue)), nil
}

// filterLength adapts the unary length implementation to the n-ary callable
// shape, backing the len() function alias (spec 03 Section 3.4).
func filterLength(args []runtime.Value) (runtime.Value, error) {
	return filterLength1(arg(args, 0))
}

// filterKeys1 returns the keys of a collection as a list, in insertion order
// (spec 03 Section 2.2); the unary implementation both dispatch routes share.
func filterKeys1(v runtime.Value) (runtime.Value, error) {
	if v.Kind != runtime.KArray || v.Arr == nil {
		return runtime.Arr(runtime.NewArray()), nil
	}
	return runtime.Arr(runtime.NewList(v.Arr.Keys()...)), nil
}

// filterKeys adapts the unary keys implementation to the n-ary callable shape,
// backing the keys() function alias (spec 03 Section 3.4).
func filterKeys(args []runtime.Value) (runtime.Value, error) {
	return filterKeys1(arg(args, 0))
}

// filterFirst1 returns the first element of a collection or the first rune of
// a string (spec 03 Section 2.1); the unary implementation both routes share.
func filterFirst1(v runtime.Value) (runtime.Value, error) {
	switch v.Kind {
	case runtime.KArray:
		if v.Arr == nil || v.Arr.Len() == 0 {
			return runtime.Null(), nil
		}
		return v.Arr.Pairs()[0].Val, nil
	case runtime.KStr, runtime.KSafe:
		r := []rune(v.S)
		if len(r) == 0 {
			return runtime.Str(""), nil
		}
		return runtime.Str(string(r[0])), nil
	default:
		return runtime.Null(), nil
	}
}

// filterLast1 returns the last element / last rune (spec 03 Section 2.1); the
// unary implementation both dispatch routes share.
func filterLast1(v runtime.Value) (runtime.Value, error) {
	switch v.Kind {
	case runtime.KArray:
		if v.Arr == nil || v.Arr.Len() == 0 {
			return runtime.Null(), nil
		}
		ps := v.Arr.Pairs()
		return ps[len(ps)-1].Val, nil
	case runtime.KStr, runtime.KSafe:
		r := []rune(v.S)
		if len(r) == 0 {
			return runtime.Str(""), nil
		}
		return runtime.Str(string(r[len(r)-1])), nil
	default:
		return runtime.Null(), nil
	}
}

// filterReverse reverses a collection (keys preserved by default) or a string
// by runes (spec 03 Section 2.2). It parses the optional preserve-keys flag and
// delegates to the shared core.
func filterReverse(args []runtime.Value) (runtime.Value, error) {
	preserveKeys := true
	if len(args) > 1 {
		preserveKeys = runtime.Truthy(args[1])
	}
	return reverseCore(arg(args, 0), preserveKeys)
}

// filterReverse1 is reverse's zero-extra-argument behavior (keys preserved),
// routed through the same core as the general path so the two cannot drift.
func filterReverse1(v runtime.Value) (runtime.Value, error) {
	return reverseCore(v, true)
}

// reverseCore is the single reverse implementation behind filterReverse and
// filterReverse1.
func reverseCore(v runtime.Value, preserveKeys bool) (runtime.Value, error) {
	switch v.Kind {
	case runtime.KStr, runtime.KSafe:
		r := []rune(v.S)
		for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
			r[i], r[j] = r[j], r[i]
		}
		return runtime.Str(string(r)), nil
	case runtime.KArray:
		if v.Arr == nil {
			return runtime.Arr(runtime.NewArray()), nil
		}
		ps := v.Arr.Pairs()
		out := runtime.NewArray()
		for i := len(ps) - 1; i >= 0; i-- {
			if preserveKeys {
				out.SetKey(ps[i].Key, ps[i].Val)
			} else {
				out.SetInt(int64(len(ps)-1-i), ps[i].Val)
			}
		}
		return runtime.Arr(out), nil
	default:
		return runtime.Null(), errors.New(errors.KindRuntime,
			"reverse expects a string or collection, got %s", v.Kind)
	}
}

// filterMerge appends integer-keyed values (reindexed) and overwrites
// string-keyed values, preserving order (spec 03 Section 2.5).
func filterMerge(args []runtime.Value) (runtime.Value, error) {
	a := arg(args, 0)
	b := arg(args, 1)
	if a.Kind != runtime.KArray || b.Kind != runtime.KArray {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"merge expects two collections")
	}
	out := runtime.NewArray()
	nextInt := int64(0)
	add := func(src *runtime.Array) {
		if src == nil {
			return
		}
		for _, p := range src.Pairs() {
			if p.Key.Kind == runtime.KInt {
				out.SetInt(nextInt, p.Val)
				nextInt++
			} else {
				out.SetKey(p.Key, p.Val)
			}
		}
	}
	add(a.Arr)
	add(b.Arr)
	return runtime.Arr(out), nil
}

// filterSlice is the rune/element slice (spec 03 Section 2.1), also backing
// a[start:end]. start may be negative (from the end); length is optional.
func filterSlice(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	start := int(toInt(arg(args, 1)))
	hasLen := len(args) > 2 && !args[2].IsNull()
	length := 0
	if hasLen {
		length = int(toInt(args[2]))
	}
	switch v.Kind {
	case runtime.KStr, runtime.KSafe:
		r := []rune(v.S)
		lo, hi := sliceBounds(len(r), start, length, hasLen)
		return runtime.Str(string(r[lo:hi])), nil
	case runtime.KArray:
		if v.Arr == nil {
			return runtime.Arr(runtime.NewArray()), nil
		}
		ps := v.Arr.Pairs()
		lo, hi := sliceBounds(len(ps), start, length, hasLen)
		out := runtime.NewArray()
		for i := lo; i < hi; i++ {
			out.SetInt(int64(i-lo), ps[i].Val)
		}
		return runtime.Arr(out), nil
	default:
		return runtime.Null(), errors.New(errors.KindRuntime,
			"slice expects a string or collection, got %s", v.Kind)
	}
}

// sliceBounds clamps a [start, start+length) window over n elements, with a
// negative start counting from the end and an absent length running to the end.
func sliceBounds(n, start, length int, hasLen bool) (lo, hi int) {
	if start < 0 {
		start += n
	}
	if start < 0 {
		start = 0
	}
	if start > n {
		start = n
	}
	if !hasLen {
		return start, n
	}
	end := start + length
	if length < 0 {
		end = n + length
	}
	if end < start {
		end = start
	}
	if end > n {
		end = n
	}
	return start, end
}

func toInt(v runtime.Value) int64 {
	switch v.Kind {
	case runtime.KInt:
		return v.I
	case runtime.KFloat:
		return int64(v.F)
	default:
		return 0
	}
}

// --- functions ---

// fnRange builds an inclusive numeric or single-character range (spec 03 Section
// 3.1), the engine shared with the `..` operator.
func fnRange(args []runtime.Value) (runtime.Value, error) {
	if len(args) < 2 {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"range expects at least low and high arguments")
	}
	low, high := args[0], args[1]
	step := int64(1)
	if len(args) > 2 {
		step = toInt(args[2])
	}
	if step == 0 {
		return runtime.Null(), errors.New(errors.KindRuntime, "range step must be non-zero")
	}
	// Single-character range, e.g. 'a'..'e'.
	if low.Kind == runtime.KStr && high.Kind == runtime.KStr &&
		len([]rune(low.S)) == 1 && len([]rune(high.S)) == 1 {
		lo := []rune(low.S)[0]
		hi := []rune(high.S)[0]
		out := runtime.NewArray()
		idx := int64(0)
		if lo <= hi {
			for c := lo; c <= hi; c += rune(step) {
				out.SetInt(idx, runtime.Str(string(c)))
				idx++
			}
		} else {
			for c := lo; c >= hi; c -= rune(step) {
				out.SetInt(idx, runtime.Str(string(c)))
				idx++
			}
		}
		return runtime.Arr(out), nil
	}
	lo := toInt(low)
	hi := toInt(high)
	out := runtime.NewArray()
	idx := int64(0)
	if lo <= hi {
		for n := lo; n <= hi; n += step {
			out.SetInt(idx, runtime.Int(n))
			idx++
		}
	} else {
		for n := lo; n >= hi; n -= step {
			out.SetInt(idx, runtime.Int(n))
			idx++
		}
	}
	return runtime.Arr(out), nil
}

// fnMax / fnMin reduce by the single total ordering, accepting either a list of
// scalars or one iterable argument (spec 03 Section 3.2).
func fnMax(args []runtime.Value) (runtime.Value, error) { return reduceOrder(args, 1) }
func fnMin(args []runtime.Value) (runtime.Value, error) { return reduceOrder(args, -1) }

// reduceOrder folds args under Order, keeping the value whose comparison against
// the accumulator equals want (1 for max, -1 for min).
func reduceOrder(args []runtime.Value, want int) (runtime.Value, error) {
	var items []runtime.Value
	if len(args) == 1 && args[0].Kind == runtime.KArray && args[0].Arr != nil {
		for _, p := range args[0].Arr.Pairs() {
			items = append(items, p.Val)
		}
	} else {
		items = args
	}
	if len(items) == 0 {
		return runtime.Null(), errors.New(errors.KindRuntime, "max/min of an empty set")
	}
	best := items[0]
	for _, v := range items[1:] {
		c, err := runtime.Order(v, best)
		if err != nil {
			return runtime.Null(), err
		}
		if c == want {
			best = v
		}
	}
	return best, nil
}

// --- tests ---

func testNull(args []runtime.Value) (bool, error) { return arg(args, 0).IsNull(), nil }

func testEmpty(args []runtime.Value) (bool, error) { return runtime.Empty(arg(args, 0)), nil }

func testEven(args []runtime.Value) (bool, error) {
	v := arg(args, 0)
	if v.Kind != runtime.KInt {
		return false, errors.New(errors.KindRuntime, "the even test expects an integer")
	}
	return v.I%2 == 0, nil
}

func testOdd(args []runtime.Value) (bool, error) {
	v := arg(args, 0)
	if v.Kind != runtime.KInt {
		return false, errors.New(errors.KindRuntime, "the odd test expects an integer")
	}
	return v.I%2 != 0, nil
}

// testIterable is true for a collection or an iterable object; a STRING is NOT
// iterable (spec 03 Section 4).
func testIterable(args []runtime.Value) (bool, error) {
	v := arg(args, 0)
	switch v.Kind {
	case runtime.KArray:
		return true, nil
	case runtime.KObject:
		_, ok := v.Obj.(runtime.Iterable)
		return ok, nil
	default:
		return false, nil
	}
}

func testSameAs(args []runtime.Value) (bool, error) {
	return runtime.Same(arg(args, 0), arg(args, 1)), nil
}
