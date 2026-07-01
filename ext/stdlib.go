package ext

import (
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// This file completes the spec-03 standard-library catalogue beyond the core
// subset in core.go: the remaining string, collection, math, encoding, and date
// filters; the access/iteration/registry functions; and the remaining tests. It
// is registered by registerStdlib, called from Core after the core subset.
//
// The higher-order collection filters (map, filter, sort with a comparator,
// reduce, find) and the membership quantifiers invoke an arrow predicate through
// the runtime.Callable protocol, so this package needs no dependency on the
// interpreter (spec 03 Section 2.2).

// registerStdlib installs the full catalogue onto s. Some callables close over s
// itself (constant/enum read the host registry on s), so they are registered
// here rather than as bare package functions.
func registerStdlib(s *ExtensionSet) {
	registerStringFilters(s)
	registerCollectionFilters(s)
	registerMathFilters(s)
	registerEncodingFilters(s)
	registerSourceFilters(s)
	registerStdlibFunctions(s)
	registerStdlibTests(s)
}

// --- string filters ---------------------------------------------------------

func registerStringFilters(s *ExtensionSet) {
	s.AddFilter(&Filter{Name: "capitalize", Fn: filterCapitalize})
	s.AddFilter(&Filter{Name: "title", Fn: filterTitle})
	s.AddFilter(&Filter{Name: "ucfirst", Fn: filterUcfirst})
	s.AddFilter(&Filter{Name: "nl2br", Fn: filterNl2br})
	s.AddFilter(&Filter{Name: "spaceless", Fn: filterSpaceless})
	s.AddFilter(&Filter{Name: "striptags", Fn: filterStriptags})
	s.AddFilter(&Filter{Name: "split", Fn: filterSplit})
	s.AddFilter(&Filter{Name: "format", Fn: filterFormat})
	s.AddFilter(&Filter{Name: "convert_encoding", Fn: filterConvertEncoding})
}

// filterCapitalize upper-cases the first rune and lower-cases the rest (spec 03
// Section 2.1). Distinct from ucfirst, which leaves the remainder unchanged.
func filterCapitalize(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	r := []rune(s)
	if len(r) == 0 {
		return runtime.Str(""), nil
	}
	head := string(unicode.ToUpper(r[0]))
	tail := strings.ToLower(string(r[1:]))
	return runtime.Str(head + tail), nil
}

// filterTitle upper-cases the first rune of each word and lower-cases the rest
// (spec 03 Section 2.1). A word boundary is any non-letter/non-digit rune.
func filterTitle(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	var b strings.Builder
	atWordStart := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if atWordStart {
				b.WriteRune(unicode.ToUpper(r))
			} else {
				b.WriteRune(unicode.ToLower(r))
			}
			atWordStart = false
		} else {
			b.WriteRune(r)
			atWordStart = true
		}
	}
	return runtime.Str(b.String()), nil
}

// filterUcfirst upper-cases the FIRST BYTE only, leaving the rest unchanged
// (spec 03 Section 5.2); a source-emission workhorse distinct from capitalize.
func filterUcfirst(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	if s == "" {
		return runtime.Str(""), nil
	}
	c := s[0]
	if c >= 'a' && c <= 'z' {
		c -= 'a' - 'A'
	}
	return runtime.Str(string(c) + s[1:]), nil
}

// filterNl2br replaces newlines with "<br />\n" after HTML-escaping the text,
// returning a Safe value (spec 03 Section 2.1). Pre-escaping makes it safe under
// an escape-on region without a double-escape.
func filterNl2br(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	s, err := runtime.ToText(v)
	if err != nil {
		return runtime.Null(), err
	}
	if v.Kind != runtime.KSafe {
		s = EscapeHTML(s)
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "<br />\n")
	return runtime.Safe(s), nil
}

// filterSpaceless collapses whitespace between tags (">   <" -> "><"), spec 03
// Section 2.1.
func filterSpaceless(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Str(spacelessRe.ReplaceAllString(s, "><")), nil
}

// filterStriptags removes markup tags, optionally keeping those in allowed (a
// string like "<a><b>"), spec 03 Section 2.1.
func filterStriptags(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	allowed := ""
	if len(args) > 1 {
		allowed, err = wantString(args[1])
		if err != nil {
			return runtime.Null(), err
		}
	}
	allow := map[string]bool{}
	for _, m := range tagNameRe.FindAllStringSubmatch(strings.ToLower(allowed), -1) {
		allow[m[1]] = true
	}
	out := tagRe.ReplaceAllStringFunc(s, func(tag string) string {
		m := tagNameRe.FindStringSubmatch(strings.ToLower(tag))
		if m != nil && allow[m[1]] {
			return tag
		}
		return ""
	})
	return runtime.Str(out), nil
}

// filterSplit splits a string on a delimiter (spec 03 Section 2.1). A positive
// limit caps the parts, putting the remainder in the last; an empty delimiter
// chunks into runes, or into limit-length rune groups when limit > 0.
func filterSplit(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	delim := ""
	if len(args) > 1 {
		delim, err = wantString(args[1])
		if err != nil {
			return runtime.Null(), err
		}
	}
	limit := 0
	if len(args) > 2 {
		limit = int(toInt(args[2]))
	}
	var parts []string
	if delim == "" {
		parts = chunkRunes(s, limit)
	} else if limit > 0 {
		parts = strings.SplitN(s, delim, limit)
	} else {
		parts = strings.Split(s, delim)
	}
	out := runtime.NewArray()
	for i, p := range parts {
		out.SetInt(int64(i), runtime.Str(p))
	}
	return runtime.Arr(out), nil
}

// chunkRunes splits s into single runes (size <= 0) or fixed-size rune groups.
func chunkRunes(s string, size int) []string {
	r := []rune(s)
	if size <= 0 {
		size = 1
	}
	var parts []string
	for i := 0; i < len(r); i += size {
		end := i + size
		if end > len(r) {
			end = len(r)
		}
		parts = append(parts, string(r[i:end]))
	}
	return parts
}

// filterFormat is printf with Go fmt verbs (spec 03 Section 2.6). The piped
// value is the format string; the explicit args fill it.
func filterFormat(args []runtime.Value) (runtime.Value, error) {
	format, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	verbArgs := make([]interface{}, 0, len(args)-1)
	for _, a := range args[1:] {
		verbArgs = append(verbArgs, goArg(a))
	}
	return runtime.Str(sprintfGo(format, verbArgs)), nil
}

// filterConvertEncoding is UTF-8-centric: Quill strings are byte strings and the
// runtime is UTF-8 throughout, so a conversion to/from UTF-8 (or an alias of it)
// is the identity, and any other target is an explicit error rather than a
// silent corruption (spec 03 Section 2.1, the documented UTF-8-centric mapping).
func filterConvertEncoding(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	to := "UTF-8"
	if len(args) > 1 {
		to, err = wantString(args[1])
		if err != nil {
			return runtime.Null(), err
		}
	}
	if !isUTF8Name(to) {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"convert_encoding target %q is unsupported; this engine is UTF-8-centric", to)
	}
	return runtime.Str(s), nil
}

func isUTF8Name(name string) bool {
	switch strings.ToUpper(strings.ReplaceAll(name, "-", "")) {
	case "UTF8", "UTF":
		return true
	default:
		return false
	}
}

// --- collection filters -----------------------------------------------------

func registerCollectionFilters(s *ExtensionSet) {
	s.AddFilter(&Filter{Name: "batch", Fn: filterBatch})
	s.AddFilter(&Filter{Name: "column", Fn: filterColumn})
	s.AddFilter(&Filter{Name: "map", Fn: filterMap})
	s.AddFilter(&Filter{Name: "filter", Fn: filterFilter})
	s.AddFilter(&Filter{Name: "reduce", Fn: filterReduce})
	s.AddFilter(&Filter{Name: "find", Fn: filterFind})
	s.AddFilter(&Filter{Name: "shuffle", Fn: filterShuffle})
}

// filterBatch chunks a collection into fixed-size lists, padding the last chunk
// with fill when supplied (spec 03 Section 2.2).
func filterBatch(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	if v.Kind != runtime.KArray || v.Arr == nil {
		return runtime.Null(), errors.New(errors.KindRuntime, "batch expects a collection")
	}
	size := int(toInt(arg(args, 1)))
	if size < 1 {
		return runtime.Null(), errors.New(errors.KindRuntime, "batch size must be >= 1")
	}
	hasFill := len(args) > 2 && !args[2].IsNull()
	fill := arg(args, 2)
	ps := v.Arr.Pairs()
	out := runtime.NewArray()
	chunkIdx := int64(0)
	for i := 0; i < len(ps); i += size {
		chunk := runtime.NewArray()
		ci := int64(0)
		for j := i; j < i+size; j++ {
			if j < len(ps) {
				chunk.SetInt(ci, ps[j].Val)
			} else if hasFill {
				chunk.SetInt(ci, fill)
			} else {
				break
			}
			ci++
		}
		out.SetInt(chunkIdx, runtime.Arr(chunk))
		chunkIdx++
	}
	return runtime.Arr(out), nil
}

// filterColumn extracts one attribute per row of a list, in order (spec 03
// Section 2.2). A row missing the key contributes nothing.
func filterColumn(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	if v.Kind != runtime.KArray || v.Arr == nil {
		return runtime.Null(), errors.New(errors.KindRuntime, "column expects a collection")
	}
	key := arg(args, 1)
	out := runtime.NewArray()
	idx := int64(0)
	for _, p := range v.Arr.Pairs() {
		got, err := runtime.GetAttribute(p.Val, normalizeKey(key), runtime.AccessIndex, true)
		if err != nil {
			return runtime.Null(), err
		}
		if got.IsNull() {
			continue
		}
		out.SetInt(idx, got)
		idx++
	}
	return runtime.Arr(out), nil
}

// filterMap applies an arrow to each (value, key) and returns a key-preserving
// collection of the results (spec 03 Section 2.2).
func filterMap(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	fn := arg(args, 1)
	if v.Kind != runtime.KArray || v.Arr == nil {
		return runtime.Null(), errors.New(errors.KindRuntime, "map expects a collection")
	}
	out := runtime.NewArray()
	for _, p := range v.Arr.Pairs() {
		res, err := runtime.Call(fn, []runtime.Value{p.Val, p.Key})
		if err != nil {
			return runtime.Null(), err
		}
		out.SetKey(p.Key, res)
	}
	return runtime.Arr(out), nil
}

// filterFilter keeps elements where the arrow is truthy, key-preserving (spec 03
// Section 2.2).
func filterFilter(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	fn := arg(args, 1)
	if v.Kind != runtime.KArray || v.Arr == nil {
		return runtime.Null(), errors.New(errors.KindRuntime, "filter expects a collection")
	}
	out := runtime.NewArray()
	for _, p := range v.Arr.Pairs() {
		res, err := runtime.Call(fn, []runtime.Value{p.Val, p.Key})
		if err != nil {
			return runtime.Null(), err
		}
		if runtime.Truthy(res) {
			out.SetKey(p.Key, p.Val)
		}
	}
	return runtime.Arr(out), nil
}

// filterReduce left-folds a collection with an arrow (acc, value, key), starting
// from initial (default Null), spec 03 Section 2.2.
func filterReduce(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	fn := arg(args, 1)
	if v.Kind != runtime.KArray || v.Arr == nil {
		return runtime.Null(), errors.New(errors.KindRuntime, "reduce expects a collection")
	}
	acc := arg(args, 2)
	for _, p := range v.Arr.Pairs() {
		res, err := runtime.Call(fn, []runtime.Value{acc, p.Val, p.Key})
		if err != nil {
			return runtime.Null(), err
		}
		acc = res
	}
	return acc, nil
}

// filterFind returns the first value for which the arrow is truthy, else Null
// (spec 03 Section 2.2).
func filterFind(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	fn := arg(args, 1)
	if v.Kind != runtime.KArray || v.Arr == nil {
		return runtime.Null(), errors.New(errors.KindRuntime, "find expects a collection")
	}
	for _, p := range v.Arr.Pairs() {
		res, err := runtime.Call(fn, []runtime.Value{p.Val, p.Key})
		if err != nil {
			return runtime.Null(), err
		}
		if runtime.Truthy(res) {
			return p.Val, nil
		}
	}
	return runtime.Null(), nil
}

// filterShuffle permutes a collection's values, reindexed as a list. Per its
// own signature shuffle(seed: int? = null), an explicit seed argument makes the
// permutation deterministic; absent one, it draws from a time-seeded source
// unless the engine has installed a host seed (spec 03 Section 2.2, X15).
func filterShuffle(args []runtime.Value) (runtime.Value, error) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	if len(args) > 1 && !args[1].IsNull() {
		rng = rand.New(rand.NewSource(toInt(args[1])))
	}
	return ShuffleWith(rng, args)
}

// ShuffleWith permutes a collection's values against a caller-supplied source.
// When the second argument is an explicit seed it takes precedence over the
// supplied rng, preserving the author-facing shuffle(seed) form; otherwise the
// engine's source (a host seed or time) is used.
func ShuffleWith(rng *rand.Rand, args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	if v.Kind != runtime.KArray || v.Arr == nil {
		return runtime.Null(), errors.New(errors.KindRuntime, "shuffle expects a collection")
	}
	if len(args) > 1 && !args[1].IsNull() {
		rng = rand.New(rand.NewSource(toInt(args[1])))
	}
	ps := v.Arr.Pairs()
	vals := make([]runtime.Value, len(ps))
	for i, p := range ps {
		vals[i] = p.Val
	}
	rng.Shuffle(len(vals), func(i, j int) { vals[i], vals[j] = vals[j], vals[i] })
	out := runtime.NewArray()
	for i, val := range vals {
		out.SetInt(int64(i), val)
	}
	return runtime.Arr(out), nil
}

// filterSortArrow sorts key-preserving by the one total ordering, or by a
// spaceship arrow comparator (a, b) => int when one is supplied (spec 03 Section
// 2.2). It replaces the core filterSort to add comparator support.
func filterSortArrow(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	if v.Kind != runtime.KArray || v.Arr == nil {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"sort expects a collection, got %s", v.Kind)
	}
	ps := v.Arr.Pairs()
	hasCmp := len(args) > 1 && runtime.IsCallable(args[1])
	cmp := arg(args, 1)
	var sortErr error
	sort.SliceStable(ps, func(i, j int) bool {
		if hasCmp {
			res, err := runtime.Call(cmp, []runtime.Value{ps[i].Val, ps[j].Val})
			if err != nil {
				sortErr = err
				return false
			}
			return toInt(res) < 0
		}
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

// --- math filters -----------------------------------------------------------

func registerMathFilters(s *ExtensionSet) {
	s.AddFilter(&Filter{Name: "abs", Fn: filterAbs})
	s.AddFilter(&Filter{Name: "round", Fn: filterRound})
	s.AddFilter(&Filter{Name: "format_number", Fn: filterFormatNumber})
	s.AddFilter(&Filter{Name: "number_format", Fn: filterFormatNumber}) // alias
}

// filterAbs returns the absolute value, preserving int vs float (spec 03 Section
// 2.3).
func filterAbs(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	switch v.Kind {
	case runtime.KInt:
		if v.I < 0 {
			return runtime.Int(-v.I), nil
		}
		return v, nil
	case runtime.KFloat:
		return runtime.Float(math.Abs(v.F)), nil
	default:
		return runtime.Null(), errors.New(errors.KindRuntime, "abs expects a number, got %s", v.Kind)
	}
}

// filterRound rounds to a precision with a mode in common/ceil/floor; negative
// precision rounds to tens/hundreds; result is a Float (spec 03 Section 2.3).
func filterRound(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	if v.Kind != runtime.KInt && v.Kind != runtime.KFloat {
		return runtime.Null(), errors.New(errors.KindRuntime, "round expects a number, got %s", v.Kind)
	}
	f := asFloat(v)
	precision := 0
	if len(args) > 1 {
		precision = int(toInt(args[1]))
	}
	mode := "common"
	if len(args) > 2 {
		m, err := wantString(args[2])
		if err != nil {
			return runtime.Null(), err
		}
		mode = m
	}
	scale := math.Pow(10, float64(precision))
	scaled := f * scale
	var r float64
	switch mode {
	case "common":
		r = math.Round(scaled)
	case "ceil":
		r = math.Ceil(scaled)
	case "floor":
		r = math.Floor(scaled)
	default:
		return runtime.Null(), errors.New(errors.KindRuntime,
			"round mode must be common/ceil/floor, got %q", mode)
	}
	return runtime.Float(r / scale), nil
}

// filterFormatNumber renders a number with a fixed decimal count and thousands
// separators (spec 03 Section 2.1). decimals defaults to 0, point ".", sep ",".
func filterFormatNumber(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	if v.Kind != runtime.KInt && v.Kind != runtime.KFloat {
		return runtime.Null(), errors.New(errors.KindRuntime,
			"format_number expects a number, got %s", v.Kind)
	}
	decimals := 0
	if len(args) > 1 {
		decimals = int(toInt(args[1]))
	}
	point := "."
	if len(args) > 2 {
		p, err := wantString(args[2])
		if err != nil {
			return runtime.Null(), err
		}
		point = p
	}
	sep := ","
	if len(args) > 3 {
		sp, err := wantString(args[3])
		if err != nil {
			return runtime.Null(), err
		}
		sep = sp
	}
	if decimals < 0 {
		decimals = 0
	}
	str := strconv.FormatFloat(asFloat(v), 'f', decimals, 64)
	neg := strings.HasPrefix(str, "-")
	if neg {
		str = str[1:]
	}
	intPart := str
	fracPart := ""
	if dot := strings.IndexByte(str, '.'); dot >= 0 {
		intPart = str[:dot]
		fracPart = str[dot+1:]
	}
	intPart = groupThousands(intPart, sep)
	out := intPart
	if decimals > 0 {
		out += point + fracPart
	}
	if neg {
		out = "-" + out
	}
	return runtime.Str(out), nil
}

// groupThousands inserts sep every three digits from the right.
func groupThousands(digits, sep string) string {
	n := len(digits)
	if n <= 3 || sep == "" {
		return digits
	}
	var b strings.Builder
	first := n % 3
	if first == 0 {
		first = 3
	}
	b.WriteString(digits[:first])
	for i := first; i < n; i += 3 {
		b.WriteString(sep)
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// --- encoding, serialization, date, utility filters -------------------------

func registerEncodingFilters(s *ExtensionSet) {
	s.AddFilter(&Filter{Name: "json", Fn: filterJSON})
	s.AddFilter(&Filter{Name: "json_encode", Fn: filterJSON}) // alias
	s.AddFilter(&Filter{Name: "url_encode", Fn: filterURLEncode})
	s.AddFilter(&Filter{Name: "date", Fn: filterDate})
	s.AddFilter(&Filter{Name: "date_modify", Fn: filterDateModify})
	s.AddFilter(&Filter{Name: "invoke", Fn: filterInvoke})
}

// filterJSON serializes via Go encoding/json output rules (spec 03 Section 2.6):
// no HTML escaping of < > &, ordered keys, literal '/'. pretty switches to
// indented with the given indent (default two spaces).
func filterJSON(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	pretty := len(args) > 1 && runtime.Truthy(args[1])
	indent := "  "
	if len(args) > 2 {
		s, err := wantString(args[2])
		if err != nil {
			return runtime.Null(), err
		}
		indent = s
	}
	var b strings.Builder
	if err := encodeJSON(&b, v, pretty, indent, ""); err != nil {
		return runtime.Null(), err
	}
	return runtime.Str(b.String()), nil
}

// filterURLEncode percent-encodes a string, or builds a query string from a
// mapping (key=value joined by &), spec 03 Section 2.4.
func filterURLEncode(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	if v.Kind == runtime.KArray && v.Arr != nil {
		var parts []string
		for _, p := range v.Arr.Pairs() {
			k, err := runtime.ToText(p.Key)
			if err != nil {
				return runtime.Null(), err
			}
			val, err := runtime.ToText(p.Val)
			if err != nil {
				return runtime.Null(), err
			}
			parts = append(parts, escapeURL(k)+"="+escapeURL(val))
		}
		return runtime.Str(strings.Join(parts, "&")), nil
	}
	s, err := runtime.ToText(v)
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Str(escapeURL(s)), nil
}

// filterInvoke calls a piped callable with the given arguments (spec 03 Section
// 2.4). The callable is an arrow or a host callable Object.
func filterInvoke(args []runtime.Value) (runtime.Value, error) {
	fn := arg(args, 0)
	var rest []runtime.Value
	if len(args) > 1 {
		rest = args[1:]
	}
	return runtime.Call(fn, rest)
}

// --- source-emission filters ------------------------------------------------

func registerSourceFilters(s *ExtensionSet) {
	s.AddFilter(&Filter{Name: "tab", Fn: filterTab})
	s.AddFilter(&Filter{Name: "indent", Fn: filterIndent})
}

// filterTab is the indentation workhorse (spec 03 Section 5.1): n | tab emits n
// levels of indentation standalone; s | tab(n) indents each non-blank line of s
// by n levels. One level is TabWidth spaces (default 4). The argument check is
// expressed in Quill truthiness and length. The engine re-registers a width-aware
// override in front of this so a host's WithTabWidth changes the unit; this core
// form is the standalone default when no engine is present.
func filterTab(args []runtime.Value) (runtime.Value, error) {
	return tabWithWidth(DefaultTabWidth, args)
}

// DefaultTabWidth is the spaces-per-indent-level width the tab filter and the
// tab/space/break functions use when no host width is configured. It matches the
// engine default (WithTabWidth).
const DefaultTabWidth = 4

// TabWith emits tab-filter indentation using an explicit level width, backing the
// engine's width-aware tab override (WithTabWidth). args is the filter's flattened
// argument list (the piped value first).
func TabWith(width int, args []runtime.Value) (runtime.Value, error) {
	return tabWithWidth(width, args)
}

// tabWithWidth is the shared tab-filter body: one level expands to width spaces.
func tabWithWidth(width int, args []runtime.Value) (runtime.Value, error) {
	if width < 0 {
		width = 0
	}
	piped := arg(args, 0)
	unit := strings.Repeat(" ", width)
	// Standalone form: a number piped with no string body -> n levels of indent.
	if piped.Kind == runtime.KInt || piped.Kind == runtime.KFloat {
		n := int(toInt(piped))
		if n < 0 {
			n = 0
		}
		return runtime.Str(strings.Repeat(unit, n)), nil
	}
	s, err := wantString(piped)
	if err != nil {
		return runtime.Null(), err
	}
	levels := 1
	if len(args) > 1 && !args[1].IsNull() {
		levels = int(toInt(args[1]))
	}
	if levels < 0 {
		levels = 0
	}
	return runtime.Str(indentLines(s, strings.Repeat(unit, levels))), nil
}

// SpaceWith emits n spaces (default 1), backing the space() function. args is the
// flattened argument list: an optional count.
func SpaceWith(args []runtime.Value) (runtime.Value, error) {
	return runtime.Str(strings.Repeat(" ", countArg(args, 0, 1))), nil
}

// BreakWith emits n newlines (default 1), backing the break() function. args is
// the flattened argument list: an optional count.
func BreakWith(args []runtime.Value) (runtime.Value, error) {
	return runtime.Str(strings.Repeat("\n", countArg(args, 0, 1))), nil
}

// TabFnWith emits n indent levels (default 1) of width spaces each, backing the
// tab() function's standalone form. args is the flattened argument list: an
// optional level count.
func TabFnWith(width int, args []runtime.Value) (runtime.Value, error) {
	if width < 0 {
		width = 0
	}
	return runtime.Str(strings.Repeat(" ", countArg(args, 0, 1)*width)), nil
}

// countArg reads a non-negative integer count from args[i], defaulting to def
// when the argument is absent or null and clamping a negative value to zero. It
// is the shared arity contract of the space/break/tab indentation functions.
func countArg(args []runtime.Value, i, def int) int {
	if i >= len(args) || args[i].IsNull() {
		return def
	}
	n := int(toInt(args[i]))
	if n < 0 {
		n = 0
	}
	return n
}

// filterIndent is the explicit multi-line indenter (spec 03 Section 5.3):
// s | indent(n, unit="    ") prefixes each non-blank line of s with n units.
func filterIndent(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	n := 1
	if len(args) > 1 && !args[1].IsNull() {
		n = int(toInt(args[1]))
	}
	unit := "    "
	if len(args) > 2 {
		unit, err = wantString(args[2])
		if err != nil {
			return runtime.Null(), err
		}
	}
	if n < 0 {
		n = 0
	}
	return runtime.Str(indentLines(s, strings.Repeat(unit, n))), nil
}

// indentLines prefixes each non-blank line of s with prefix, leaving blank lines
// untouched so trailing/empty lines do not gain stray indentation.
func indentLines(s, prefix string) string {
	if prefix == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}

// --- functions --------------------------------------------------------------

func registerStdlibFunctions(s *ExtensionSet) {
	s.AddFunction(&Function{Name: "attribute", Fn: fnAttribute})
	s.AddFunction(&Function{Name: "cycle", Fn: fnCycle})
	s.AddFunction(&Function{Name: "random", Fn: fnRandom})
	s.AddFunction(&Function{Name: "date", Fn: fnDate})
	s.AddFunction(&Function{Name: "len", Fn: filterLength}) // alias of |length (spec 03 Section 3.4)
	s.AddFunction(&Function{Name: "keys", Fn: filterKeys})  // alias of |keys (spec 03 Section 3.4)

	// Indentation functions (spec 03 Section 5.1): space(n) emits n spaces, break(n)
	// emits n newlines, tab(n) emits n indent levels. space/break are width-free;
	// tab uses the default level width here and the engine re-registers a
	// width-aware override so WithTabWidth changes the level size.
	s.AddFunction(&Function{Name: "space", Fn: SpaceWith})
	s.AddFunction(&Function{Name: "break", Fn: BreakWith})
	s.AddFunction(&Function{Name: "tab", Fn: func(args []runtime.Value) (runtime.Value, error) {
		return TabFnWith(DefaultTabWidth, args)
	}})

	// constant / enum / enum_cases close over s to read the host registry.
	s.AddFunction(&Function{Name: "constant", Fn: func(args []runtime.Value) (runtime.Value, error) {
		return fnConstant(s, args)
	}})
	s.AddFunction(&Function{Name: "enum", Fn: func(args []runtime.Value) (runtime.Value, error) {
		return fnEnum(s, args)
	}})
	s.AddFunction(&Function{Name: "enum_cases", Fn: func(args []runtime.Value) (runtime.Value, error) {
		return fnEnumCases(s, args)
	}})
}

// fnAttribute reads member name of var at runtime, optionally calling it with an
// argument list -- the dynamic form of a.b / a.b(args), spec 03 Section 3.2.
func fnAttribute(args []runtime.Value) (runtime.Value, error) {
	recv := arg(args, 0)
	name := arg(args, 1)
	if len(args) > 2 && !args[2].IsNull() {
		callArgs := args[2]
		if recv.Kind != runtime.KObject {
			return runtime.Null(), errors.New(errors.KindAttribute,
				"attribute() can only call a method on an object, got %s", recv.Kind)
		}
		var ca []runtime.Value
		if callArgs.Kind == runtime.KArray && callArgs.Arr != nil {
			for _, p := range callArgs.Arr.Pairs() {
				ca = append(ca, p.Val)
			}
		}
		nm, err := runtime.ToText(name)
		if err != nil {
			return runtime.Null(), err
		}
		return recv.Obj.CallMethod(nm, ca)
	}
	return runtime.GetAttribute(recv, normalizeKey(name), runtime.AccessDot, true)
}

// fnCycle returns values[position % length], wrapping (spec 03 Section 3.2).
func fnCycle(args []runtime.Value) (runtime.Value, error) {
	values := arg(args, 0)
	if values.Kind != runtime.KArray || values.Arr == nil || values.Arr.Len() == 0 {
		return runtime.Null(), errors.New(errors.KindRuntime, "cycle expects a non-empty collection")
	}
	pos := toInt(arg(args, 1))
	ps := values.Arr.Pairs()
	n := int64(len(ps))
	idx := ((pos % n) + n) % n
	return ps[idx].Val, nil
}

// fnRandom is the host-facing random() with a fresh time-seeded source. The
// engine registers a seed-aware wrapper (RandomWith) when a host seed is set
// (spec 03 Section 3.2, X15); this plain form is the fallback when none is.
func fnRandom(args []runtime.Value) (runtime.Value, error) {
	return RandomWith(rand.New(rand.NewSource(time.Now().UnixNano())), args)
}

// RandomWith implements random(values, max) against a caller-supplied source,
// so the engine can thread a fixed seed for deterministic output. Per the spec
// signature random(values: any? = null, max: int? = null), arg0 selects the
// behavior by type and arg1 is the inclusive upper bound for integer draws:
//
//	random()                -> a non-negative random int
//	random(null, max)       -> int in [0, max]
//	random(n)               -> int in [0, n]
//	random(lo, hi)          -> int in [lo, hi]
//	random(collection)      -> a random element
//	random(string)          -> a random character
//
// max is meaningless for a collection or string and is ignored there.
func RandomWith(rng *rand.Rand, args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	hasMax := len(args) > 1 && !args[1].IsNull()
	switch v.Kind {
	case runtime.KNull:
		if hasMax {
			return randIntInRange(rng, 0, toInt(args[1]))
		}
		return runtime.Int(rng.Int63()), nil
	case runtime.KInt:
		if hasMax {
			return randIntInRange(rng, v.I, toInt(args[1]))
		}
		return randIntInRange(rng, 0, v.I)
	case runtime.KArray:
		if v.Arr == nil || v.Arr.Len() == 0 {
			return runtime.Null(), nil
		}
		ps := v.Arr.Pairs()
		return ps[rng.Intn(len(ps))].Val, nil
	case runtime.KStr, runtime.KSafe:
		r := []rune(v.S)
		if len(r) == 0 {
			return runtime.Str(""), nil
		}
		return runtime.Str(string(r[rng.Intn(len(r))])), nil
	default:
		return runtime.Null(), errors.New(errors.KindRuntime, "random cannot operate on %s", v.Kind)
	}
}

// randIntInRange returns a uniform int in the inclusive [lo, hi] range, tolerating
// a reversed pair by swapping the bounds.
func randIntInRange(rng *rand.Rand, lo, hi int64) (runtime.Value, error) {
	if lo > hi {
		lo, hi = hi, lo
	}
	return runtime.Int(lo + rng.Int63n(hi-lo+1)), nil
}

// fnConstant resolves a named host/global constant; with check_defined true it
// returns whether the constant exists rather than its value (spec 03 Section
// 3.2). The obj argument (a class scope) is accepted but unused in this engine,
// which has a single flat constant namespace.
func fnConstant(s *ExtensionSet, args []runtime.Value) (runtime.Value, error) {
	name, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	checkDefined := len(args) > 2 && runtime.Truthy(args[2])
	v, ok := s.Constant(name)
	if checkDefined {
		return runtime.Bool(ok), nil
	}
	if !ok {
		return runtime.Null(), errors.New(errors.KindRuntime, "undefined constant %q", name)
	}
	return v, nil
}

// fnEnum returns the first case of a named host enumeration (spec 03 Section 3.2).
func fnEnum(s *ExtensionSet, args []runtime.Value) (runtime.Value, error) {
	name, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	cases, ok := s.Enum(name)
	if !ok || len(cases) == 0 {
		return runtime.Null(), errors.New(errors.KindRuntime, "unknown or empty enumeration %q", name)
	}
	return cases[0], nil
}

// fnEnumCases returns all cases of a named host enumeration in declaration order
// (spec 03 Section 3.2).
func fnEnumCases(s *ExtensionSet, args []runtime.Value) (runtime.Value, error) {
	name, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	cases, ok := s.Enum(name)
	if !ok {
		return runtime.Null(), errors.New(errors.KindRuntime, "unknown enumeration %q", name)
	}
	out := runtime.NewArray()
	for i, c := range cases {
		out.SetInt(int64(i), c)
	}
	return runtime.Arr(out), nil
}

// --- tests ------------------------------------------------------------------

func registerStdlibTests(s *ExtensionSet) {
	s.AddTest(&Test{Name: "divisible_by", Fn: testDivisibleBy})
	s.AddTest(&Test{Name: "divisible by", Fn: testDivisibleBy}) // spaced-spelling alias
	s.AddTest(&Test{Name: "sequence", Fn: testSequence})
	s.AddTest(&Test{Name: "mapping", Fn: testMapping})
	s.AddTest(&Test{Name: "true", Fn: testTrue})
	s.AddTest(&Test{Name: "constant", Fn: func(args []runtime.Value) (bool, error) {
		return testConstant(s, args)
	}})
}

// testDivisibleBy reports integer divisibility x % n == 0 (spec 03 Section 4).
func testDivisibleBy(args []runtime.Value) (bool, error) {
	x := arg(args, 0)
	n := arg(args, 1)
	if x.Kind != runtime.KInt || n.Kind != runtime.KInt {
		return false, errors.New(errors.KindRuntime, "divisible_by expects integers")
	}
	if n.I == 0 {
		return false, errors.New(errors.KindArithmetic, "divisible_by zero")
	}
	return x.I%n.I == 0, nil
}

// testSequence reports a list-shaped *Array; an empty array IS a sequence (spec
// 03 Section 4).
func testSequence(args []runtime.Value) (bool, error) { return runtime.IsSequence(arg(args, 0)), nil }

// testMapping reports a non-list *Array or any Object; an empty array is NOT a
// mapping (spec 03 Section 4).
func testMapping(args []runtime.Value) (bool, error) { return runtime.IsMapping(arg(args, 0)), nil }

// testTrue reports whether the value is Bool true (Safe-unwrapped first), spec 03
// Section 4.
func testTrue(args []runtime.Value) (bool, error) {
	v := arg(args, 0)
	if v.Kind == runtime.KSafe {
		// A Safe never carries a bool payload; it is not the Bool true value.
		return false, nil
	}
	return v.Kind == runtime.KBool && v.B, nil
}

// testConstant reports whether x equals the named host constant (spec 03 Section
// 4). The constant name is the test's argument.
func testConstant(s *ExtensionSet, args []runtime.Value) (bool, error) {
	x := arg(args, 0)
	name, err := wantString(arg(args, 1))
	if err != nil {
		return false, err
	}
	v, ok := s.Constant(name)
	if !ok {
		return false, errors.New(errors.KindRuntime, "undefined constant %q", name)
	}
	return runtime.Equal(x, v), nil
}

// --- shared helpers ---------------------------------------------------------

// normalizeKey coerces a value into an Int or Str key for attribute access.
func normalizeKey(v runtime.Value) runtime.Value {
	if v.Kind == runtime.KInt {
		return v
	}
	s, err := runtime.ToText(v)
	if err != nil {
		return runtime.Str("")
	}
	return runtime.Str(s)
}

func asFloat(v runtime.Value) float64 {
	if v.Kind == runtime.KInt {
		return float64(v.I)
	}
	return v.F
}
