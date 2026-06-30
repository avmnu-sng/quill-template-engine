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
// Application-specific functions are intentionally not built in: they are
// registered by the host through the extension surface
// (ext.ExtensionSet.AddFunction), which this milestone exercises, rather than
// shipped as engine primitives.
//
// Implemented this slice: the @escape block region now accepts any of the six
// strategies (html, js, css, html_attr, html_attr_relaxed, url) plus off/raw and
// applies it to its body, sharing the same escapers as the escape()/e() filter.
// Nested regions and the module default compose via a strategy stack
// (save/restore on region entry/exit), so an inner region restores the enclosing
// strategy on exit, and captures/macros/blocks under any active strategy yield a
// Safe value (spec 04 Section 8). Previously implemented: the full spec-03
// standard-library catalogue, arrow functions through
// map/filter/sort/reduce/find and the "has some"/"has every" quantifiers, all six
// escape strategies via the filter, the regex "matches" operator (Go RE2
// dialect), and the whitespace-control trim modifiers (the - / ~ / + flags).
package quill
