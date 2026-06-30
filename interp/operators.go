package interp

import (
	"math"

	"github.com/avmnusng/quill-template-engine/ast"
	"github.com/avmnusng/quill-template-engine/errors"
	"github.com/avmnusng/quill-template-engine/runtime"
)

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
			return runtime.Int(l.I + r.I), nil
		}
		return finite(n, asF(l)+asF(r))
	case "-":
		if bothInt {
			return runtime.Int(l.I - r.I), nil
		}
		return finite(n, asF(l)-asF(r))
	case "*":
		if bothInt {
			return runtime.Int(l.I * r.I), nil
		}
		return finite(n, asF(l)*asF(r))
	case "/":
		if asF(r) == 0 {
			return runtime.Null(), posErr(n, errors.New(errors.KindArithmetic, "division by zero"))
		}
		// "/" yields an int when both are ints and the division is exact, else a
		// float, keeping integer arithmetic integral where it can (spec 04 Section 2).
		if bothInt && l.I%r.I == 0 {
			return runtime.Int(l.I / r.I), nil
		}
		return finite(n, asF(l)/asF(r))
	case "//":
		if asF(r) == 0 {
			return runtime.Null(), posErr(n, errors.New(errors.KindArithmetic, "floor division by zero"))
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
	res := math.Pow(asF(base), asF(exp))
	// Keep an integer result integral when both operands are ints and the exponent
	// is non-negative, matching the number tower.
	if base.Kind == runtime.KInt && exp.Kind == runtime.KInt && exp.I >= 0 &&
		res == math.Trunc(res) && !math.IsInf(res, 0) {
		return runtime.Int(int64(res)), nil
	}
	return finite(n, res)
}

// evalMembership implements in / not in / matches / starts with / ends with /
// has some / has every (spec 04 Section 4.3). Only the subset real templates
// need is wired; has some / has every are deferred and reported as such.
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
	default:
		return runtime.Null(), posErr(n, errors.New(errors.KindRuntime,
			"the %q operator is not implemented in this milestone", n.Str))
	}
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
