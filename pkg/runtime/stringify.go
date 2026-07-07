package runtime

import (
	"math"
	"strconv"

	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
)

// ToText is Quill's single implicit coercion: rendering a value to text at an
// interpolation site or as an operand of ~ (spec 04 Section 5). Arithmetic and
// comparison NEVER coerce. The spelling table, EXACTLY:
//
//   - Null    -> "" (the empty string)
//   - Bool    -> "true" / "false"
//   - Int     -> decimal, no separators
//   - Float   -> shortest round-trippable decimal (Go 'g'/-1: 1.0 -> "1",
//     1.5 -> "1.5")
//   - Str     -> the bytes verbatim
//   - *Array  -> RENDER ERROR (not the literal "Array"); use join / json
//   - Object  -> its Stringify hook output, else a render error
//   - Safe    -> the wrapped content, unwrapped verbatim
func ToText(v Value) (string, error) {
	switch v.Kind {
	case KNull:
		return "", nil
	case KBool:
		if v.B {
			return "true", nil
		}
		return "false", nil
	case KInt:
		return strconv.FormatInt(v.I, 10), nil
	case KFloat:
		return formatFloat(v.F)
	case KStr:
		return v.S, nil
	case KSafe:
		return v.S, nil
	case KArray:
		return "", errors.New(errors.KindRender,
			"cannot render an array as text; use join or json")
	case KObject:
		if s, ok := v.Obj.(Stringifier); ok {
			out, err := s.Stringify()
			if err != nil {
				return "", err
			}
			return out, nil
		}
		return "", errors.New(errors.KindRender,
			"cannot render object %s as text: no stringify hook", objectClass(v.Obj))
	default:
		return "", errors.New(errors.KindRender, "cannot render value of unknown kind")
	}
}

// formatFloat renders a finite float in Go's shortest round-trippable 'g' form.
// Non-finite floats never reach here because they are rejected at the value
// boundary (spec 04 Section 2.1); the guard is defensive.
func formatFloat(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", errors.New(errors.KindArithmetic,
			"cannot render a non-finite float")
	}
	return strconv.FormatFloat(f, 'g', -1, 64), nil
}

func objectClass(o Object) string {
	if c, ok := o.(ClassNamed); ok {
		return c.ClassName()
	}
	return "object"
}

// RejectNonFinite is the value-boundary guard from spec 04 Section 2.1: a
// NaN/Inf float64 entering from the host (or about to be produced by
// arithmetic) is a defined arithmetic error, so no non-finite float ever
// circulates and Equal/Order stay total over the whole Float kind. Callers wrap
// host float64 values through this before lifting them into a Float Value.
func RejectNonFinite(f float64) error {
	if math.IsNaN(f) {
		return errors.New(errors.KindArithmetic, "NaN is not a representable value")
	}
	if math.IsInf(f, 0) {
		return errors.New(errors.KindArithmetic, "infinity is not a representable value")
	}
	return nil
}
