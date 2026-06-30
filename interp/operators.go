package interp

import (
	"math"
	"regexp"

	"github.com/avmnusng/quill-template-engine/ast"
	"github.com/avmnusng/quill-template-engine/errors"
	"github.com/avmnusng/quill-template-engine/runtime"
)

// overflowErr is the uniform int64-overflow arithmetic error, naming the
// operation and both operands (spec 04 Section 2.1: overflow is a defined error
// across + - * // **, never a silent wrap or float promotion).
func overflowErr(n *ast.Node, op string, a, b int64) error {
	return posErr(n, errors.New(errors.KindArithmetic,
		"%q overflows int64: %d and %d", op, a, b))
}

// addInt64/subInt64/mulInt64/divInt64/floorDivInt64 are checked signed-int64
// operations: each returns (result, false) when the true result is not
// representable in int64. They are the single source of the overflow rule so
// every arithmetic path reports it identically.
func addInt64(a, b int64) (int64, bool) {
	s := a + b
	// Overflow iff both operands share a sign and the sum's sign differs.
	if (a > 0 && b > 0 && s < 0) || (a < 0 && b < 0 && s >= 0) {
		return 0, false
	}
	return s, true
}

func subInt64(a, b int64) (int64, bool) {
	d := a - b
	// Overflow iff the operands' signs differ and the result's sign differs from a.
	if (a >= 0 && b < 0 && d < 0) || (a < 0 && b > 0 && d >= 0) {
		return 0, false
	}
	return d, true
}

func mulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	p := a * b
	// p/a recovers b exactly unless the product overflowed. The MinInt64 * -1
	// case (which p/a would not catch cleanly) is covered by the explicit guard.
	if (a == math.MinInt64 && b == -1) || (b == math.MinInt64 && a == -1) {
		return 0, false
	}
	if p/a != b {
		return 0, false
	}
	return p, true
}

func divInt64(a, b int64) (int64, bool) {
	if a == math.MinInt64 && b == -1 {
		return 0, false // the one quotient that overflows int64
	}
	return a / b, true
}

// floorDivInt64 floors toward negative infinity (Go's / truncates toward zero),
// so the quotient is decremented when the division is inexact and the operands
// have opposite signs. Shares the MinInt64/-1 overflow guard with divInt64.
func floorDivInt64(a, b int64) (int64, bool) {
	q, ok := divInt64(a, b)
	if !ok {
		return 0, false
	}
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q, true
}

// powInt64 computes a**e for a non-negative exponent e by repeated checked
// multiplication, returning (result, false) on int64 overflow. e is assumed
// >= 0 (the caller routes negative exponents to the float path).
func powInt64(a, e int64) (int64, bool) {
	result := int64(1)
	base := a
	for e > 0 {
		if e&1 == 1 {
			p, ok := mulInt64(result, base)
			if !ok {
				return 0, false
			}
			result = p
		}
		e >>= 1
		if e == 0 {
			break
		}
		sq, ok := mulInt64(base, base)
		if !ok {
			return 0, false
		}
		base = sq
	}
	return result, true
}

// evalBinary handles the KindBinary operators: arithmetic, concat, range,
// comparison, bitwise. Comparison routes to runtime.Equal/Order so there is one
// equality and one ordering (spec 04 Sections 3, 4); arithmetic never coerces.
func (in *interp) evalBinary(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	op := n.Str
	// "~" renders each operand by ToText (the only coercion besides print).
	if op == "~" {
		return in.evalConcat(n, ctx)
	}
	left, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	right, err := in.eval(n.Child(1), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	switch op {
	case "==":
		return runtime.Bool(runtime.Equal(left, right)), nil
	case "!=":
		return runtime.Bool(!runtime.Equal(left, right)), nil
	case "<", ">", "<=", ">=", "<=>":
		return in.compare(n, op, left, right)
	case "+", "-", "*", "/", "//", "%":
		return in.arith(n, op, left, right)
	case "..":
		return callRange(in, left, right)
	case "b_or", "b_and", "b_xor":
		return in.bitwise(n, op, left, right)
	default:
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"unknown binary operator %q", op))
	}
}

func (in *interp) compare(n *ast.Node, op string, l, r runtime.Value) (runtime.Value, error) {
	c, err := runtime.Order(l, r)
	if err != nil {
		return runtime.Null(), posErr(n, err)
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

// arith implements +, -, *, /, //, % with NO coercion: both operands must be
// numbers (spec 04 Section 4). Int op Int stays Int (except / which yields a
// float when not exact, following the number tower); a Float operand promotes.
// Division and modulo by zero are arithmetic errors.
func (in *interp) arith(n *ast.Node, op string, l, r runtime.Value) (runtime.Value, error) {
	if !isNum(l) || !isNum(r) {
		return runtime.Null(), posErr(n, errors.New(errors.KindArithmetic,
			"operator %q expects numbers, got %s and %s", op, l.Kind, r.Kind))
	}
	bothInt := l.Kind == runtime.KInt && r.Kind == runtime.KInt
	switch op {
	case "+":
		if bothInt {
			s, ok := addInt64(l.I, r.I)
			if !ok {
				return runtime.Null(), overflowErr(n, op, l.I, r.I)
			}
			return runtime.Int(s), nil
		}
		return finite(n, asF(l)+asF(r))
	case "-":
		if bothInt {
			d, ok := subInt64(l.I, r.I)
			if !ok {
				return runtime.Null(), overflowErr(n, op, l.I, r.I)
			}
			return runtime.Int(d), nil
		}
		return finite(n, asF(l)-asF(r))
	case "*":
		if bothInt {
			p, ok := mulInt64(l.I, r.I)
			if !ok {
				return runtime.Null(), overflowErr(n, op, l.I, r.I)
			}
			return runtime.Int(p), nil
		}
		return finite(n, asF(l)*asF(r))
	case "/":
		if asF(r) == 0 {
			return runtime.Null(), posErr(n, errors.New(errors.KindArithmetic, "division by zero"))
		}
		// "/" yields an int when both are ints and the division is exact, else a
		// float, keeping integer arithmetic integral where it can (spec 04 Section 2).
		if bothInt && l.I%r.I == 0 {
			q, ok := divInt64(l.I, r.I)
			if !ok {
				return runtime.Null(), overflowErr(n, op, l.I, r.I)
			}
			return runtime.Int(q), nil
		}
		return finite(n, asF(l)/asF(r))
	case "//":
		if asF(r) == 0 {
			return runtime.Null(), posErr(n, errors.New(errors.KindArithmetic, "floor division by zero"))
		}
		// Two ints floor-divide in int64 so the result keeps Int kind (spec 04
		// Section 2.1 // row): a Float here would silently break any downstream
		// Int-only context (bitwise ops, exact-kind equality). Only MinInt64/-1
		// overflows int64 division.
		if bothInt {
			q, ok := floorDivInt64(l.I, r.I)
			if !ok {
				return runtime.Null(), overflowErr(n, op, l.I, r.I)
			}
			return runtime.Int(q), nil
		}
		return finite(n, math.Floor(asF(l)/asF(r)))
	case "%":
		if bothInt {
			if r.I == 0 {
				return runtime.Null(), posErr(n, errors.New(errors.KindArithmetic, "modulo by zero"))
			}
			return runtime.Int(l.I % r.I), nil
		}
		if asF(r) == 0 {
			return runtime.Null(), posErr(n, errors.New(errors.KindArithmetic, "modulo by zero"))
		}
		return finite(n, math.Mod(asF(l), asF(r)))
	}
	return runtime.Null(), nil
}

func (in *interp) bitwise(n *ast.Node, op string, l, r runtime.Value) (runtime.Value, error) {
	if l.Kind != runtime.KInt || r.Kind != runtime.KInt {
		return runtime.Null(), posErr(n, errors.New(errors.KindArithmetic,
			"bitwise operator %q expects integers", op))
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

// evalConcat implements "~": render both operands by ToText and join (spec 04
// Section 4). It is the only binary operator that coerces.
func (in *interp) evalConcat(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	l, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	r, err := in.eval(n.Child(1), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	ls, err := runtime.ToText(l)
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	rs, err := runtime.ToText(r)
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	return runtime.Str(ls + rs), nil
}

// evalPower implements ** (right-associative; the AST already encodes
// associativity and the unary-minus interaction, spec 02 R6).
func (in *interp) evalPower(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	base, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	exp, err := in.eval(n.Child(1), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	if !isNum(base) || !isNum(exp) {
		return runtime.Null(), posErr(n, errors.New(errors.KindArithmetic,
			"** expects numbers, got %s and %s", base.Kind, exp.Kind))
	}
	// An Int base with a NON-NEGATIVE Int exponent yields an Int; the power is
	// computed in int64 (NOT via math.Pow, which loses precision above 2^53 and
	// saturates on int64 conversion) and an overflow is an ERROR, never a float
	// promotion or a silently-truncated literal (spec 04 Section 2.1 ** row).
	if base.Kind == runtime.KInt && exp.Kind == runtime.KInt && exp.I >= 0 {
		p, ok := powInt64(base.I, exp.I)
		if !ok {
			return runtime.Null(), overflowErr(n, "**", base.I, exp.I)
		}
		return runtime.Int(p), nil
	}
	// A negative integer exponent or any Float operand yields a Float (spec 04:
	// 2 ** -1 == 0.5). A non-finite result is rejected at the finite() boundary.
	return finite(n, math.Pow(asF(base), asF(exp)))
}

// evalMembership implements in / not in / matches / starts with / ends with /
// has some / has every (spec 04 Section 4.3). The regex matches operator uses
// the stdlib RE2 engine; the quantifiers apply an arrow predicate to each
// element of the left collection.
func (in *interp) evalMembership(n *ast.Node, ctx *runtime.Context) (runtime.Value, error) {
	left, err := in.eval(n.Child(0), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	right, err := in.eval(n.Child(1), ctx, false)
	if err != nil {
		return runtime.Null(), err
	}
	switch n.Str {
	case "in":
		ok, err := runtime.In(left, right)
		if err != nil {
			return runtime.Null(), posErr(n, err)
		}
		if n.Bool { // "not in"
			ok = !ok
		}
		return runtime.Bool(ok), nil
	case "starts with":
		return in.affix(n, left, right, true)
	case "ends with":
		return in.affix(n, left, right, false)
	case "matches":
		return in.matches(n, left, right)
	case "has some":
		return in.quantify(n, left, right, false)
	case "has every":
		return in.quantify(n, left, right, true)
	default:
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"the %q operator is not implemented in this milestone", n.Str))
	}
}

// quantify implements the `has some` / `has every` quantifiers: it applies the
// right-hand arrow predicate to each element of the left collection. "has some"
// is the existential (true iff the predicate holds for at least one element);
// "has every" is the universal (true iff it holds for all). An empty collection
// makes "has every" vacuously true and "has some" false, matching the standard
// quantifier identities (spec 04 Section 4.3). The predicate receives the value
// and, if it declares a second parameter, the key.
func (in *interp) quantify(n *ast.Node, coll, pred runtime.Value, universal bool) (runtime.Value, error) {
	if !runtime.IsCallable(pred) {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"the %q operator expects an arrow predicate on the right", n.Str))
	}
	pairs, err := runtime.EnsureTraversable(coll, in.eng.StrictVariables() == false)
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	for _, p := range pairs {
		res, err := runtime.Call(pred, []runtime.Value{p.Val, p.Key})
		if err != nil {
			return runtime.Null(), posErr(n, err)
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

// affix implements starts with / ends with as byte-prefix/suffix tests over the
// ToText rendering of both operands.
func (in *interp) affix(n *ast.Node, l, r runtime.Value, prefix bool) (runtime.Value, error) {
	ls, err := runtime.ToText(l)
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	rs, err := runtime.ToText(r)
	if err != nil {
		return runtime.Null(), posErr(n, err)
	}
	if prefix {
		return runtime.Bool(len(ls) >= len(rs) && ls[:len(rs)] == rs), nil
	}
	return runtime.Bool(len(ls) >= len(rs) && ls[len(ls)-len(rs):] == rs), nil
}

// matches implements the regex membership operator using the Go RE2 dialect
// (spec 01 Section, "Regex matches"). The right operand must be a string whose
// contents are the RE2 pattern; the left operand is matched as its ToText
// rendering. A subject that is not a string is a type error (a regex over a
// number/array is a programming mistake, not a silent coercion); an invalid or
// PCRE-only pattern is a clear runtime error rather than a panic.
func (in *interp) matches(n *ast.Node, subject, pattern runtime.Value) (runtime.Value, error) {
	if subject.Kind != runtime.KStr && subject.Kind != runtime.KSafe {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"the %q operator expects a string subject, got %s", "matches", subject.Kind))
	}
	if pattern.Kind != runtime.KStr && pattern.Kind != runtime.KSafe {
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"the %q operator expects a string pattern, got %s", "matches", pattern.Kind))
	}
	// A literal pattern was already validated and compiled during Prepare (spec
	// 01 Section 3, "validated at compile time"); reuse that *regexp.Regexp so a
	// `matches` in a loop does not recompile each iteration. A dynamic pattern
	// (absent from the cache) is compiled here.
	re := in.regexps[n]
	if re == nil {
		var err error
		re, err = regexp.Compile(pattern.S)
		if err != nil {
			return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
				"invalid RE2 pattern %q: %v", pattern.S, err))
		}
	}
	return runtime.Bool(re.MatchString(subject.S)), nil
}

func isNum(v runtime.Value) bool { return v.Kind == runtime.KInt || v.Kind == runtime.KFloat }

// toInt coerces a number value to int64 (0 for non-numbers), used to translate
// slice bounds into the slice filter's (start, length) form.
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

func asF(v runtime.Value) float64 {
	if v.Kind == runtime.KInt {
		return float64(v.I)
	}
	return v.F
}

// finite lifts a computed float into a Float value, rejecting non-finite results
// at the arithmetic boundary so no NaN/Inf circulates (spec 04 Section 2.1).
func finite(n *ast.Node, f float64) (runtime.Value, error) {
	if err := runtime.RejectNonFinite(f); err != nil {
		return runtime.Null(), posErr(n, err)
	}
	return runtime.Float(f), nil
}
