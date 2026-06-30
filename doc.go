// Package quill is a Go-native, gradually-typed template engine.
//
// Quill compiles and renders templates written in the Quill language. See the
// specification under docs/ for the language reference, grammar, standard
// library, and runtime semantics.
//
// This package is under active development; its API is not yet stable.
//
// Deferred to a later milestone (NOT silently stubbed; each errors clearly or is
// documented at its site rather than mis-rendering):
//
//   - The gradual type checker. @types / per-variable annotations parse but are
//     not enforced at render time (spec 04 Section 1).
//   - The sandbox (@sandbox renders its body transparently; no policy is yet
//     enforced) and @cache (renders its body, no caching), spec 01 Section 4.7.
//   - Map destructuring in @set and @use traits raise explicit errors where
//     reached.
//
// Intentionally omitted from the stdlib (with their reasons):
//
//   - The host-specific corpus functions getJavaListDataType and subtractOne
//     (spec 03 Section 3.5) are not built in: they are application functions, not
//     engine primitives, and are meant to be registered by a host through the
//     extension surface (ext.ExtensionSet.AddFunction), which this slice exercises.
//
// Implemented this slice: the full spec-03 standard-library catalogue (every
// remaining filter, function, and test), arrow functions evaluating through
// map/filter/sort/reduce/find and the "has some"/"has every" quantifiers, and
// all six escape strategies (html, js, css, html_attr, html_attr_relaxed, url).
// The regex "matches" operator (Go RE2 dialect) and the whitespace-control trim
// modifiers (the - / ~ / + flags) were already implemented.
package quill
