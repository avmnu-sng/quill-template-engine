// Package errors is Quill's typed error family. Every failure the engine
// reports -- a parse fault, a type-check diagnostic, an undefined read, an
// arithmetic overflow, a render error -- is one *Error carrying a Kind, the
// *source.Source it occurred in, and a 1-based line number.
//
// The Kind partitions failures into the classes the spec names: syntax,
// type-check, undefined, arithmetic, comparison, attribute access, rendering,
// iteration, key/subscript, security, and a generic runtime bucket. Hosts can
// branch on Kind (or match with errors.As) to react to a class of failure
// without string-matching the message. Messages are ASCII and name the symbol
// or operands at fault, per spec 04 Sections 5, 6, and 8.
package errors

import (
	"fmt"

	"github.com/avmnusng/quill-template-engine/source"
)

// Kind classifies a Quill error. The zero value KindRuntime is the generic
// catch-all; the named kinds correspond to the spec's distinct failure classes.
type Kind uint8

const (
	// KindRuntime is an unclassified runtime failure (the zero value).
	KindRuntime Kind = iota
	// KindSyntax is a lexer/parser fault: malformed template source.
	KindSyntax
	// KindTypeCheck is a gradual-type-checker diagnostic raised before render.
	KindTypeCheck
	// KindUndefined is a strict-by-default miss: an undefined variable, an
	// absent *Array key, or an absent Object member (spec 04 Section 8).
	KindUndefined
	// KindArithmetic is overflow, division/modulo by zero, or a non-finite
	// float crossing the value boundary (spec 04 Section 2).
	KindArithmetic
	// KindComparison is a cross-kind ordering or otherwise undefined compare
	// (spec 04 Section 4.2).
	KindComparison
	// KindAttribute is a dotted/index access fault other than a plain miss:
	// e.g. accessing a member on a kind that has none (spec 04 Section 7).
	KindAttribute
	// KindRender is a ToText failure: an *Array render, or an Object with no
	// Stringify hook (spec 04 Section 5).
	KindRender
	// KindIteration is a for over a non-iterable value (spec 04 Section 8.5).
	KindIteration
	// KindKey is an illegal subscript key kind: bool, float, or null
	// (spec 04 Section 6.2).
	KindKey
	// KindSecurity is a sandbox policy violation (spec 04 Section 8.3).
	KindSecurity
)

// String returns a short, stable, ASCII label for the kind, used in messages.
func (k Kind) String() string {
	switch k {
	case KindRuntime:
		return "runtime"
	case KindSyntax:
		return "syntax"
	case KindTypeCheck:
		return "type"
	case KindUndefined:
		return "undefined"
	case KindArithmetic:
		return "arithmetic"
	case KindComparison:
		return "comparison"
	case KindAttribute:
		return "attribute"
	case KindRender:
		return "render"
	case KindIteration:
		return "iteration"
	case KindKey:
		return "key"
	case KindSecurity:
		return "security"
	default:
		return "runtime"
	}
}

// Error is Quill's structured error. Src and Line may be unset (nil / 0) when a
// failure is raised below the layer that knows the source position; a higher
// layer can fill them in with At before re-returning.
type Error struct {
	Kind Kind
	Msg  string
	Src  *source.Source
	Line int
	// Cause is an optional wrapped error, exposed via Unwrap.
	Cause error
}

// Error renders the message with the kind label and, when known, the source
// name and line, so the text alone locates the fault.
func (e *Error) Error() string {
	loc := ""
	if e.Src != nil {
		if e.Line > 0 {
			loc = fmt.Sprintf(" (%s:%d)", e.Src.Name(), e.Line)
		} else {
			loc = fmt.Sprintf(" (%s)", e.Src.Name())
		}
	} else if e.Line > 0 {
		loc = fmt.Sprintf(" (line %d)", e.Line)
	}
	return fmt.Sprintf("quill %s error: %s%s", e.Kind, e.Msg, loc)
}

// Unwrap exposes the wrapped cause for errors.Is / errors.As chains.
func (e *Error) Unwrap() error { return e.Cause }

// At returns a copy of the error annotated with a source and line, leaving the
// receiver unchanged. It is the canonical way an upper layer attaches position
// to an error raised without one. A nil receiver yields nil.
func (e *Error) At(src *source.Source, line int) *Error {
	if e == nil {
		return nil
	}
	cp := *e
	cp.Src = src
	cp.Line = line
	return &cp
}

// New builds an *Error of the given kind with a formatted, ASCII message and no
// source position. Callers attach position later with At.
func New(kind Kind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// Wrap builds an *Error that wraps an underlying cause.
func Wrap(kind Kind, cause error, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...), Cause: cause}
}

// KindOf reports the Kind of err if it is (or wraps) a *Error, else
// KindRuntime. It lets a host classify any error without a type assertion.
func KindOf(err error) Kind {
	var qe *Error
	if as(err, &qe) {
		return qe.Kind
	}
	return KindRuntime
}

// as is a tiny local indirection over errors.As to avoid importing the stdlib
// errors package under the same name as this one.
func as(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
