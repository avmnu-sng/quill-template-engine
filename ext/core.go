package ext

import (
	"sort"
	"strings"

	"github.com/avmnusng/quill-template-engine/errors"
	"github.com/avmnusng/quill-template-engine/runtime"
)

// Core builds an ExtensionSet holding the stdlib SUBSET this milestone ships
// (spec 03). It is the floor real templates need, not the full catalogue; the
// deferred entries are listed in the package and slice notes. The include/block
// family is registered by the engine facade (it needs the environment handle and
// loader), so it is NOT installed here.
//
// Filters: upper, lower, default, length, join, trim, replace, raw, escape,
// first, last, keys, reverse, sort, merge, slice.
// Functions: range, max, min. (include is registered by the engine.)
// Tests: defined, null/none, empty, even, odd, iterable, same as.
func Core() *ExtensionSet {
	s := NewExtensionSet()
	registerCoreFilters(s)
	registerCoreFunctions(s)
	registerCoreTests(s)
	return s
}

func registerCoreFilters(s *ExtensionSet) {
	s.AddFilter(&Filter{Name: "upper", Fn: filterUpper})
	s.AddFilter(&Filter{Name: "lower", Fn: filterLower})
	s.AddFilter(&Filter{Name: "default", Fn: filterDefault})
	s.AddFilter(&Filter{Name: "length", Fn: filterLength})
	s.AddFilter(&Filter{Name: "join", Fn: filterJoin})
	s.AddFilter(&Filter{Name: "trim", Fn: filterTrim})
	s.AddFilter(&Filter{Name: "replace", Fn: filterReplace})
	s.AddFilter(&Filter{Name: "raw", Fn: filterRaw})
	s.AddFilter(&Filter{Name: "escape", Fn: filterEscape})
	s.AddFilter(&Filter{Name: "e", Fn: filterEscape}) // alias
	s.AddFilter(&Filter{Name: "first", Fn: filterFirst})
	s.AddFilter(&Filter{Name: "last", Fn: filterLast})
	s.AddFilter(&Filter{Name: "keys", Fn: filterKeys})
	s.AddFilter(&Filter{Name: "reverse", Fn: filterReverse})
	s.AddFilter(&Filter{Name: "sort", Fn: filterSort})
	s.AddFilter(&Filter{Name: "merge", Fn: filterMerge})
	s.AddFilter(&Filter{Name: "slice", Fn: filterSlice})
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

func filterUpper(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Str(strings.ToUpper(s)), nil
}

func filterLower(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Str(strings.ToLower(s)), nil
}

// filterTrim folds Twig's trim/ltrim/rtrim: side in both/left/right (aliases
// b/l/r), mask defaults to ASCII+Unicode whitespace (spec 03 Section 2.1).
func filterTrim(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	side := "both"
	if len(args) > 1 {
		side, err = wantString(args[1])
		if err != nil {
			return runtime.Null(), err
		}
	}
	mask := " \t\n\r\x00\x0B"
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

// filterRaw marks content already-safe. Under the default (escaping off) it is a
// passthrough; the interpreter's escape pass leaves a Safe value untouched. Its
// effect is load-bearing only under an escape-on region (spec 03 Section 5.4).
func filterRaw(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	s, err := runtime.ToText(v)
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Safe(s), nil
}

// filterEscape escapes for a named strategy (default html) and returns a Safe
// value. Only the html strategy is implemented this slice; the remaining five
// strategies are deferred (spec 03 Section 5.5). A value that is already Safe is
// returned unchanged.
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
	switch strategy {
	case "html":
		return runtime.Safe(EscapeHTML(text)), nil
	default:
		return runtime.Null(), errors.New(errors.KindRuntime,
			"escape strategy %q is not implemented; only \"html\" is available", strategy)
	}
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

// filterLength returns string runes, collection count, or 1 for a scalar (spec
// 03 Section 2.2).
func filterLength(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
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

// filterKeys returns the keys of a collection as a list, in insertion order
// (spec 03 Section 2.2).
func filterKeys(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	if v.Kind != runtime.KArray || v.Arr == nil {
		return runtime.Arr(runtime.NewArray()), nil
	}
	return runtime.Arr(runtime.NewList(v.Arr.Keys()...)), nil
}

// filterFirst returns the first element of a collection or the first rune of a
// string (spec 03 Section 2.1).
func filterFirst(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
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

// filterLast returns the last element / last rune (spec 03 Section 2.1).
func filterLast(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
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

// filterReverse reverses a collection (keys preserved by default) or a string by
// runes (spec 03 Section 2.2).
func filterReverse(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	preserveKeys := true
	if len(args) > 1 {
		preserveKeys = runtime.Truthy(args[1])
	}
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

// filterSort sorts a collection by the one total ordering, key-preserving (spec
// 03 Section 2.2). The optional comparator arrow is deferred this slice.
func filterSort(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	if v.Kind != runtime.KArray || v.Arr == nil {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"sort expects a collection, got %s", v.Kind)
	}
	ps := v.Arr.Pairs()
	var sortErr error
	sort.SliceStable(ps, func(i, j int) bool {
		c, err := runtime.Order(ps[i].Val, ps[j].Val)
		if err != nil {
			sortErr = err
			return false
		}
		return c < 0
	})
	if sortErr != nil {
		return runtime.Null(), sortErr
	}
	out := runtime.NewArray()
	for _, p := range ps {
		out.SetKey(p.Key, p.Val)
	}
	return runtime.Arr(out), nil
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
