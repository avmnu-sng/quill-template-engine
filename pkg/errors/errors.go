// Package errors is Quill's typed error family. Every failure the engine
// reports (a parse fault, a type-check diagnostic, an undefined read, an
// arithmetic overflow, a render error) is one *Error carrying a Kind, the
// *source.Source it occurred in, and a 1-based line number.
//
// The Kind partitions failures into the classes the spec names: syntax,
// type-check, undefined, arithmetic, comparison, attribute access, rendering,
// iteration, key/subscript, security, and a generic runtime bucket. Hosts can
// branch on Kind (or match with errors.As) to react to a class of failure
// without string-matching the message. Messages are ASCII and name the symbol
// or operands at fault, per spec 04 Sections 5, 6, and 8. Error message text is
// NOT part of the compatibility contract; branch on Kind, errors.As, or a
// sentinel (e.g. loader.ErrNotFound) instead.
package errors

import (
	"fmt"

	"github.com/avmnu-sng/quill-template-engine/pkg/source"
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

// Error is Quill's structured error. Kind is the failure class hosts branch on
// (read-only: mutating it after construction is undefined). The fault position
// is read through the Src, Line, and Col methods and may be unset when a failure
// is raised below the layer that knows it; a higher layer fills it in with At or
// AtPos before re-returning. Col is the 1-based column; it is 0 when unknown, and
// the rendered message omits it in that case.
//
// A constructed *Error is immutable and safe for concurrent use by multiple
// goroutines (At and AtPos return annotated copies rather than mutating the
// receiver) so long as callers treat the exported Kind, Msg, and Cause fields
// as read-only after construction.
type Error struct {
	Kind Kind
	// Msg is the human-readable, ASCII fault message. Like Kind it is read-only:
	// mutating it after construction is undefined, and its exact text is not part
	// of the compatibility contract (see the package doc).
	Msg string
	// Cause is an optional wrapped error, exposed via Unwrap.
	Cause error

	// src/line/col hold the fault position. They are unexported so position
	// access is method-based and uniform with *Security (spec 01 Section 1.8),
	// and are set only through At/AtPos, which return copies.
	src  *source.Source
	line int
	col  int
}

// Error renders the message with the kind label and, when known, the source
// name and line, so the text alone locates the fault.
func (e *Error) Error() string {
	loc := ""
	if e.src != nil {
		switch {
		case e.line > 0 && e.col > 0:
			loc = fmt.Sprintf(" (%s:%d:%d)", e.src.Name(), e.line, e.col)
		case e.line > 0:
			loc = fmt.Sprintf(" (%s:%d)", e.src.Name(), e.line)
		default:
			loc = fmt.Sprintf(" (%s)", e.src.Name())
		}
	} else if e.line > 0 {
		loc = fmt.Sprintf(" (line %d)", e.line)
	}
	return fmt.Sprintf("quill %s error: %s%s", e.Kind, e.Msg, loc)
}

// Src returns the source the fault occurred in, or nil when unknown (or the
// receiver is nil).
func (e *Error) Src() *source.Source {
	if e == nil {
		return nil
	}
	return e.src
}

// Line returns the 1-based line of the fault, or 0 when unknown.
func (e *Error) Line() int {
	if e == nil {
		return 0
	}
	return e.line
}

// Col returns the 1-based column of the fault, or 0 when unknown.
func (e *Error) Col() int {
	if e == nil {
		return 0
	}
	return e.col
}

// Unwrap exposes the wrapped cause for errors.Is / errors.As chains.
func (e *Error) Unwrap() error { return e.Cause }

// At returns a copy of the error annotated with a source and line (no column),
// leaving the receiver unchanged. It is the canonical way an upper layer attaches
// position to an error raised without one. A nil receiver yields nil. It is
// preserved as public API; it delegates to AtPos with a zero column so the
// rendered message keeps the plain "name:line" form.
func (e *Error) At(src *source.Source, line int) *Error {
	return e.AtPos(src, line, 0)
}

// AtPos returns a copy of the error annotated with a source, 1-based line, and
// 1-based column, leaving the receiver unchanged. A zero col is treated as
// unknown and omitted from the rendered message (see Error). A nil receiver
// yields nil.
func (e *Error) AtPos(src *source.Source, line, col int) *Error {
	if e == nil {
		return nil
	}
	cp := *e
	cp.src = src
	cp.line = line
	cp.col = col
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

// SecurityClass partitions a sandbox violation into the categories the spec
// names (spec 04 Section 8.3, design/escaping-safety Section 6.9). It is carried
// on a *Security error so a host can branch on the exact violation class without
// string-matching the message.
type SecurityClass uint8

const (
	// SecTag is a disallowed statement keyword (e.g. an unlisted @for).
	SecTag SecurityClass = iota
	// SecFilter is a disallowed filter.
	SecFilter
	// SecFunction is a disallowed function (the range operator `..` counts as
	// the range function).
	SecFunction
	// SecMethod is a disallowed host-object method call or string coercion.
	SecMethod
	// SecProperty is a disallowed host-object property read or column access.
	SecProperty
	// SecUnknownType is a member access on a host type the strict-mode policy
	// does not know at all: it has no method or property allowlist entry and is
	// absent from the type-graph. It is distinct from SecMethod/SecProperty so a
	// host can tell an unregistered or mistyped type from a denied-but-known
	// member (spec 04 Section 8.3, B6).
	SecUnknownType
)

// String returns a stable ASCII label for the security violation class.
func (c SecurityClass) String() string {
	switch c {
	case SecTag:
		return "tag"
	case SecFilter:
		return "filter"
	case SecFunction:
		return "function"
	case SecMethod:
		return "method"
	case SecProperty:
		return "property"
	case SecUnknownType:
		return "unknown-type"
	default:
		return "unknown"
	}
}

// Security is the typed sandbox-violation error. It wraps an *Error (always
// KindSecurity) so it stays in the engine's error family (KindOf and
// errors.As(&Error{}) reach it via Unwrap) while adding the offending Name,
// the host Type name for member violations (empty for tag/filter/function), and
// the violation Class so a host can catch with errors.As(&Security{}) and switch
// on Class (spec 04 Section 8.3, design/escaping-safety Section 6.9). The wrapped
// *Error is unexported; reach it (and its position) through Unwrap or the Src,
// Line, and Col methods.
//
// A constructed *Security is immutable and safe for concurrent use by multiple
// goroutines (At returns an annotated copy rather than mutating the receiver)
// so long as callers treat the exported Class, Name, and Type fields (and the
// wrapped *Error) as read-only after construction.
type Security struct {
	err   *Error
	Class SecurityClass
	Name  string // the offending tag/filter/function/method/property name
	Type  string // the host type name for member violations; "" otherwise
}

// Error renders the wrapped *Error's message, making *Security an error.
func (s *Security) Error() string { return s.err.Error() }

// Unwrap exposes the wrapped *Error so errors.As(&errors.Error{}) and KindOf
// still reach a *Security.
func (s *Security) Unwrap() error { return s.err }

// Src returns the source the violation occurred in, if known.
func (s *Security) Src() *source.Source { return s.err.Src() }

// Line returns the 1-based line of the violation, if known.
func (s *Security) Line() int { return s.err.Line() }

// Col returns the 1-based column of the violation, if known.
func (s *Security) Col() int { return s.err.Col() }

// security builds a *Security of the given class with an ASCII message and no
// source position; callers attach position later with At.
func security(class SecurityClass, typeName, name, msg string) *Security {
	return &Security{
		err:   &Error{Kind: KindSecurity, Msg: msg},
		Class: class,
		Name:  name,
		Type:  typeName,
	}
}

// SecurityTag reports a disallowed statement keyword (B1).
func SecurityTag(tag string) *Security {
	return security(SecTag, "", tag, fmt.Sprintf("statement %q is not allowed by the sandbox policy", tag))
}

// SecurityFilter reports a disallowed filter (B2).
func SecurityFilter(name string) *Security {
	return security(SecFilter, "", name, fmt.Sprintf("filter %q is not allowed by the sandbox policy", name))
}

// SecurityFunction reports a disallowed function; `..` reports as range (B3).
func SecurityFunction(name string) *Security {
	return security(SecFunction, "", name, fmt.Sprintf("function %q is not allowed by the sandbox policy", name))
}

// SecurityMethod reports a disallowed method call or string coercion, naming
// the host type and the method in its real (case-sensitive) spelling (B4, B12).
func SecurityMethod(typeName, method string) *Security {
	return security(SecMethod, typeName, method,
		fmt.Sprintf("method %q on type %q is not allowed by the sandbox policy", method, typeName))
}

// SecurityProperty reports a disallowed property read or column access (B5,
// B13).
func SecurityProperty(typeName, prop string) *Security {
	return security(SecProperty, typeName, prop,
		fmt.Sprintf("property %q on type %q is not allowed by the sandbox policy", prop, typeName))
}

// SecurityUnknownType reports a member access on a host type the strict-mode
// policy does not know at all: it has no method or property allowlist entry
// and is absent from the type-graph (spec 04 Section 8.3 strict-vs-lenient mode,
// B6). Lenient mode does not raise this; it falls through to the per-member deny.
// The violation carries the dedicated SecUnknownType class so a host can
// distinguish a typo or unregistered type from a denied-but-known member, and
// the message names the unknown type.
func SecurityUnknownType(typeName, member string) *Security {
	return security(SecUnknownType, typeName, member,
		fmt.Sprintf("type %q is unknown to the sandbox policy (strict mode); access to %q is denied", typeName, member))
}

// At returns a copy of the security error annotated with a source and line,
// leaving the receiver unchanged (the *Error.At analogue that preserves the
// Security wrapper and its Class/Name/Type). A nil receiver yields nil.
func (s *Security) At(src *source.Source, line int) *Security {
	if s == nil {
		return nil
	}
	cp := *s
	cp.err = s.err.At(src, line)
	return &cp
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
