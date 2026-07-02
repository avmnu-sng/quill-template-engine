package compile

// prelude is the runtime-support tail of every generated file: the output
// writer with the interpreter's @tab indentation semantics, the position
// wrapper reproducing posErr, the arithmetic and operator helpers carrying
// the interpreter's exact error text, and the arrow host object satisfying
// the runtime callable protocol. Each generated file is self-contained, so
// several compiled templates coexist as sibling packages.
const prelude = `// qWriter is the output layer: outside a @tab region it forwards bytes
// unchanged while tracking the line-start cursor; inside one it prefixes the
// active indent to each non-blank line, reproducing the interpreter's write
// and writeIndented exactly.
type qWriter struct {
	w           io.Writer
	indent      string
	atLineStart bool
}

// WriteString writes s through the indentation layer.
func (q *qWriter) WriteString(s string) error {
	if q.indent == "" {
		if s != "" {
			q.atLineStart = strings.HasSuffix(s, "\n")
		}
		_, err := io.WriteString(q.w, s)
		return err
	}
	for len(s) > 0 {
		nl := strings.IndexByte(s, '\n')
		var line string
		var hasNL bool
		if nl < 0 {
			line = s
			s = ""
		} else {
			line = s[:nl]
			s = s[nl+1:]
			hasNL = true
		}
		if q.atLineStart && line != "" {
			if _, err := io.WriteString(q.w, q.indent); err != nil {
				return err
			}
		}
		if line != "" {
			if _, err := io.WriteString(q.w, line); err != nil {
				return err
			}
		}
		if hasNL {
			if _, err := io.WriteString(q.w, "\n"); err != nil {
				return err
			}
			q.atLineStart = true
		} else {
			q.atLineStart = false
		}
	}
	return nil
}

// qemit renders one interpolated value: ToText, then the active escape
// strategy for non-Safe values, then the output layer, like interp emit.
func qemit(q *qWriter, strategy string, v runtime.Value) error {
	text, err := runtime.ToText(v)
	if err != nil {
		return err
	}
	if strategy != "" && v.Kind != runtime.KSafe {
		text, err = ext.Escape(strategy, text)
		if err != nil {
			return err
		}
	}
	return q.WriteString(text)
}

// qpos attaches this template's source and the given line to an error that
// lacks a position, reproducing the interpreter's posErr.
func qpos(err error, line int) error {
	if err == nil {
		return nil
	}
	var qs *qerrors.Security
	if stderrors.As(err, &qs) {
		if qs.Src() == nil && qs.Line() == 0 {
			return qs.At(qSrc, line)
		}
		return qs
	}
	var qe *qerrors.Error
	if stderrors.As(err, &qe) {
		if qe.Src == nil && qe.Line == 0 {
			return qe.At(qSrc, line)
		}
		return qe
	}
	return qerrors.Wrap(qerrors.KindRuntime, err, "%s", err.Error()).At(qSrc, line)
}

// qundef builds the strict-undefined variable error with the available-names
// hint, positioned at the given template line.
func qundef(name string, names []string, line int) error {
	return qerrors.New(qerrors.KindUndefined,
		"undefined variable %q (available: %s)", name, qjoin(names)).At(qSrc, line)
}

// qjoin renders a name list for the undefined-variable hint.
func qjoin(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	out := ""
	for i, s := range names {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// qaddName appends name to names unless already present, reproducing the
// first-seen deduplication of Scope.Names.
func qaddName(names []string, name string) []string {
	for _, n := range names {
		if n == name {
			return names
		}
	}
	return append(names, name)
}

// qwithNames lists a with-map's binding names in insertion order.
func qwithNames(with runtime.Value) []string {
	var out []string
	if with.Kind == runtime.KArray && with.Arr != nil {
		for _, p := range with.Arr.Pairs() {
			s, err := runtime.ToText(p.Key)
			if err == nil {
				out = append(out, s)
			}
		}
	}
	return out
}

// qwithHas reports whether a with-map binds the given name.
func qwithHas(with runtime.Value, name string) bool {
	if with.Kind != runtime.KArray || with.Arr == nil {
		return false
	}
	_, ok := with.Arr.GetStr(name)
	return ok
}

// qEnv is the compiled render's engine handle: a host object carrying the
// compile-time engine configuration that needs-environment callables read
// through their injected environment value (the ext.EngineConfig surface),
// exactly as they read a live engine's configuration.
type qEnv struct {
	tabWidth int
	seed     int64
	seedSet  bool
}

// GetField exposes no fields on the engine handle, returning (null, false).
func (e *qEnv) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }

// CallMethod rejects method calls on the engine handle; it only threads
// configuration into needs-environment callables and is not itself callable.
func (e *qEnv) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), qerrors.New(qerrors.KindRuntime, "engine handle is not directly callable")
}

// TabWidth reports the compiled options' spaces-per-indent-level width.
func (e *qEnv) TabWidth() int { return e.tabWidth }

// RandomSeed reports the compiled options' RNG seed and whether one was set.
func (e *qEnv) RandomSeed() (int64, bool) { return e.seed, e.seedSet }

// qinject prepends the engine values a callable's Needs* flags request, in
// the fixed order environment, context, charset. The environment is this
// render's qEnv handle, so width- and seed-aware callables honor the compiled
// options exactly as they honor a live engine's.
func qinject(env runtime.Value, needsEnv, needsCtx, needsCharset bool, ctx *runtime.Array, args []runtime.Value) []runtime.Value {
	var pre []runtime.Value
	if needsEnv {
		pre = append(pre, env)
	}
	if needsCtx {
		pre = append(pre, runtime.Arr(ctx))
	}
	if needsCharset {
		pre = append(pre, runtime.Str("UTF-8"))
	}
	if len(pre) == 0 {
		return args
	}
	return append(pre, args...)
}

// qkeyOf coerces a computed key to the access layer's Int-or-Str key model.
func qkeyOf(v runtime.Value) runtime.Value {
	if v.Kind == runtime.KInt {
		return v
	}
	s, err := runtime.ToText(v)
	if err != nil {
		return runtime.Str("")
	}
	return runtime.Str(s)
}

// qtoi coerces a number value to int64 (0 for non-numbers), used to translate
// slice bounds into the slice filter's start/length form.
func qtoi(v runtime.Value) int64 {
	switch v.Kind {
	case runtime.KInt:
		return v.I
	case runtime.KFloat:
		return int64(v.F)
	default:
		return 0
	}
}

// qtabLevels coerces a @tab level value to a non-negative level count.
func qtabLevels(v runtime.Value) (int, error) {
	switch v.Kind {
	case runtime.KInt:
		if v.I < 0 {
			return 0, nil
		}
		return int(v.I), nil
	case runtime.KFloat:
		if v.F < 0 {
			return 0, nil
		}
		return int(v.F), nil
	default:
		return 0, qerrors.New(qerrors.KindRuntime, "@tab level must be a number, got %s", v.Kind)
	}
}

// qArrow is the compiled arrow-function value: a Go closure over the
// enclosing locals (the engine's live-lexical capture contract) satisfying
// the runtime callable protocol.
type qArrow struct {
	fn func(args []runtime.Value) (runtime.Value, error)
}

// GetField reports no fields on an arrow value.
func (a *qArrow) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }

// CallMethod rejects method dispatch on an arrow value.
func (a *qArrow) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), qerrors.New(qerrors.KindRuntime,
		"an arrow function is invoked positionally, not by method")
}

// Invoke applies the closure to the evaluated arguments.
func (a *qArrow) Invoke(args []runtime.Value) (runtime.Value, error) { return a.fn(args) }

// qoverflow is the uniform int64-overflow arithmetic error.
func qoverflow(op string, a, b int64) error {
	return qerrors.New(qerrors.KindArithmetic, "%q overflows int64: %d and %d", op, a, b)
}

func qaddInt64(a, b int64) (int64, bool) {
	s := a + b
	if (a > 0 && b > 0 && s < 0) || (a < 0 && b < 0 && s >= 0) {
		return 0, false
	}
	return s, true
}

func qsubInt64(a, b int64) (int64, bool) {
	d := a - b
	if (a >= 0 && b < 0 && d < 0) || (a < 0 && b > 0 && d >= 0) {
		return 0, false
	}
	return d, true
}

func qmulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if (a == math.MinInt64 && b == -1) || (b == math.MinInt64 && a == -1) {
		return 0, false
	}
	p := a * b
	if p/a != b {
		return 0, false
	}
	return p, true
}

func qdivInt64(a, b int64) (int64, bool) {
	if a == math.MinInt64 && b == -1 {
		return 0, false
	}
	return a / b, true
}

func qfloorDivInt64(a, b int64) (int64, bool) {
	q, ok := qdivInt64(a, b)
	if !ok {
		return 0, false
	}
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q, true
}

func qpowInt64(a, e int64) (int64, bool) {
	result := int64(1)
	base := a
	for e > 0 {
		if e&1 == 1 {
			p, ok := qmulInt64(result, base)
			if !ok {
				return 0, false
			}
			result = p
		}
		e >>= 1
		if e == 0 {
			break
		}
		sq, ok := qmulInt64(base, base)
		if !ok {
			return 0, false
		}
		base = sq
	}
	return result, true
}

func qisNum(v runtime.Value) bool {
	return v.Kind == runtime.KInt || v.Kind == runtime.KFloat
}

func qasF(v runtime.Value) float64 {
	if v.Kind == runtime.KInt {
		return float64(v.I)
	}
	return v.F
}

// qfinite lifts a computed float, rejecting non-finite results at the
// arithmetic boundary like the interpreter.
func qfinite(f float64) (runtime.Value, error) {
	if err := runtime.RejectNonFinite(f); err != nil {
		return runtime.Null(), err
	}
	return runtime.Float(f), nil
}

// qarith implements +, -, *, /, //, % with the interpreter's no-coercion
// number rules and exact error text. Errors return unpositioned; the call
// site attaches its template line.
func qarith(op string, l, r runtime.Value) (runtime.Value, error) {
	if !qisNum(l) || !qisNum(r) {
		return runtime.Null(), qerrors.New(qerrors.KindArithmetic,
			"operator %q expects numbers, got %s and %s", op, l.Kind, r.Kind)
	}
	bothInt := l.Kind == runtime.KInt && r.Kind == runtime.KInt
	switch op {
	case "+":
		if bothInt {
			s, ok := qaddInt64(l.I, r.I)
			if !ok {
				return runtime.Null(), qoverflow(op, l.I, r.I)
			}
			return runtime.Int(s), nil
		}
		return qfinite(qasF(l) + qasF(r))
	case "-":
		if bothInt {
			d, ok := qsubInt64(l.I, r.I)
			if !ok {
				return runtime.Null(), qoverflow(op, l.I, r.I)
			}
			return runtime.Int(d), nil
		}
		return qfinite(qasF(l) - qasF(r))
	case "*":
		if bothInt {
			p, ok := qmulInt64(l.I, r.I)
			if !ok {
				return runtime.Null(), qoverflow(op, l.I, r.I)
			}
			return runtime.Int(p), nil
		}
		return qfinite(qasF(l) * qasF(r))
	case "/":
		if qasF(r) == 0 {
			return runtime.Null(), qerrors.New(qerrors.KindArithmetic, "division by zero")
		}
		if bothInt && l.I%r.I == 0 {
			q, ok := qdivInt64(l.I, r.I)
			if !ok {
				return runtime.Null(), qoverflow(op, l.I, r.I)
			}
			return runtime.Int(q), nil
		}
		return qfinite(qasF(l) / qasF(r))
	case "//":
		if qasF(r) == 0 {
			return runtime.Null(), qerrors.New(qerrors.KindArithmetic, "floor division by zero")
		}
		if bothInt {
			q, ok := qfloorDivInt64(l.I, r.I)
			if !ok {
				return runtime.Null(), qoverflow(op, l.I, r.I)
			}
			return runtime.Int(q), nil
		}
		return qfinite(math.Floor(qasF(l) / qasF(r)))
	case "%":
		if bothInt {
			if r.I == 0 {
				return runtime.Null(), qerrors.New(qerrors.KindArithmetic, "modulo by zero")
			}
			return runtime.Int(l.I % r.I), nil
		}
		if qasF(r) == 0 {
			return runtime.Null(), qerrors.New(qerrors.KindArithmetic, "modulo by zero")
		}
		return qfinite(math.Mod(qasF(l), qasF(r)))
	}
	return runtime.Null(), nil
}

// qcompare implements the ordering operators over runtime.Order.
func qcompare(op string, l, r runtime.Value) (runtime.Value, error) {
	c, err := runtime.Order(l, r)
	if err != nil {
		return runtime.Null(), err
	}
	switch op {
	case "<":
		return runtime.Bool(c < 0), nil
	case ">":
		return runtime.Bool(c > 0), nil
	case "<=":
		return runtime.Bool(c <= 0), nil
	case ">=":
		return runtime.Bool(c >= 0), nil
	case "<=>":
		return runtime.Int(int64(c)), nil
	}
	return runtime.Null(), nil
}

// qbitwise implements b_or / b_and / b_xor over integers only.
func qbitwise(op string, l, r runtime.Value) (runtime.Value, error) {
	if l.Kind != runtime.KInt || r.Kind != runtime.KInt {
		return runtime.Null(), qerrors.New(qerrors.KindArithmetic,
			"bitwise operator %q expects integers", op)
	}
	switch op {
	case "b_or":
		return runtime.Int(l.I | r.I), nil
	case "b_and":
		return runtime.Int(l.I & r.I), nil
	case "b_xor":
		return runtime.Int(l.I ^ r.I), nil
	}
	return runtime.Null(), nil
}

// qpow implements ** with the interpreter's int64-exact non-negative integer
// path and float fallback.
func qpow(base, exp runtime.Value) (runtime.Value, error) {
	if !qisNum(base) || !qisNum(exp) {
		return runtime.Null(), qerrors.New(qerrors.KindArithmetic,
			"** expects numbers, got %s and %s", base.Kind, exp.Kind)
	}
	if base.Kind == runtime.KInt && exp.Kind == runtime.KInt && exp.I >= 0 {
		p, ok := qpowInt64(base.I, exp.I)
		if !ok {
			return runtime.Null(), qoverflow("**", base.I, exp.I)
		}
		return runtime.Int(p), nil
	}
	return qfinite(math.Pow(qasF(base), qasF(exp)))
}

// qconcat implements "~": both operands render by ToText and join.
func qconcat(l, r runtime.Value) (runtime.Value, error) {
	ls, err := runtime.ToText(l)
	if err != nil {
		return runtime.Null(), err
	}
	rs, err := runtime.ToText(r)
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Str(ls + rs), nil
}

// qaffix implements starts with / ends with as byte prefix/suffix tests over
// the ToText rendering of both operands.
func qaffix(l, r runtime.Value, prefix bool) (runtime.Value, error) {
	ls, err := runtime.ToText(l)
	if err != nil {
		return runtime.Null(), err
	}
	rs, err := runtime.ToText(r)
	if err != nil {
		return runtime.Null(), err
	}
	if prefix {
		return runtime.Bool(len(ls) >= len(rs) && ls[:len(rs)] == rs), nil
	}
	return runtime.Bool(len(ls) >= len(rs) && ls[len(ls)-len(rs):] == rs), nil
}

// qneg implements unary minus over numbers.
func qneg(v runtime.Value) (runtime.Value, error) {
	switch v.Kind {
	case runtime.KInt:
		return runtime.Int(-v.I), nil
	case runtime.KFloat:
		return runtime.Float(-v.F), nil
	default:
		return runtime.Null(), qerrors.New(qerrors.KindArithmetic,
			"unary - expects a number, got %s", v.Kind)
	}
}

// qplus implements unary plus: a number passes through unchanged.
func qplus(v runtime.Value) (runtime.Value, error) {
	if v.Kind == runtime.KInt || v.Kind == runtime.KFloat {
		return v, nil
	}
	return runtime.Null(), qerrors.New(qerrors.KindArithmetic,
		"unary + expects a number, got %s", v.Kind)
}

// qmatches implements the regex membership operator over the RE2 dialect.
func qmatches(subject, pattern runtime.Value) (runtime.Value, error) {
	if subject.Kind != runtime.KStr && subject.Kind != runtime.KSafe {
		return runtime.Null(), qerrors.New(qerrors.KindRuntime,
			"the %q operator expects a string subject, got %s", "matches", subject.Kind)
	}
	if pattern.Kind != runtime.KStr && pattern.Kind != runtime.KSafe {
		return runtime.Null(), qerrors.New(qerrors.KindRuntime,
			"the %q operator expects a string pattern, got %s", "matches", pattern.Kind)
	}
	re, err := regexp.Compile(pattern.S)
	if err != nil {
		return runtime.Null(), qerrors.New(qerrors.KindRuntime,
			"invalid RE2 pattern %q: %v", pattern.S, err)
	}
	return runtime.Bool(re.MatchString(subject.S)), nil
}

// qquantify implements has some / has every by applying the arrow predicate
// to each element of the collection.
func qquantify(op string, coll, pred runtime.Value, universal, lenient bool) (runtime.Value, error) {
	if !runtime.IsCallable(pred) {
		return runtime.Null(), qerrors.New(qerrors.KindRuntime,
			"the %q operator expects an arrow predicate on the right", op)
	}
	pairs, err := runtime.EnsureTraversable(coll, lenient)
	if err != nil {
		return runtime.Null(), err
	}
	for _, p := range pairs {
		res, err := runtime.Call(pred, []runtime.Value{p.Val, p.Key})
		if err != nil {
			return runtime.Null(), err
		}
		hit := runtime.Truthy(res)
		if universal && !hit {
			return runtime.Bool(false), nil
		}
		if !universal && hit {
			return runtime.Bool(true), nil
		}
	}
	return runtime.Bool(universal), nil
}
`
