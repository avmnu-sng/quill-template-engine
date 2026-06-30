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
//     enforced), spec 01 Section 4.7.
//   - Optional and elided destructuring slots ([a, b?] and [, b]); the LHS is
//     parsed as an expression and a trailing "?" reads as the ternary, so these
//     two slot forms report a parse error today (a later slice adds a dedicated
//     target grammar). Sequence destructuring otherwise works fully -- flat slots,
//     a "...rest" tail capture, and nested list/map patterns -- and enforces
//     arity (over/under-supply is an error, not silent padding); map/object
//     destructuring works for shorthand and rename forms.
//
// Application-specific functions are intentionally not built in: they are
// registered by the host through the extension surface
// (ext.ExtensionSet.AddFunction), which this milestone exercises, rather than
// shipped as engine primitives.
//
// Implemented this slice (composition tail): @use horizontal trait reuse merges a
// traitable template's blocks below the using template's own (trait-then-own
// precedence), supports block aliasing/rename via "with { trait: alias }", and
// makes parent() reach the trait version before any extends-parent; a use target
// must be a constant string and the trait must have no parent, macros, or free
// body (spec 01 Section 5.4). @cache renders its body once in a child scope,
// memoizes it under the (template-namespaced) key in the engine's pluggable
// in-memory cache, and on a hit re-emits the cached body without re-rendering;
// ttl is a documented no-op for the non-expiring in-memory cache and tags drive
// optional tag-invalidation (spec 01 Section 4.7). @set supports map/object
// destructuring -- shorthand {name} and rename {key: alias} -- reading each slot
// through the same dotted access as a.b so the right-hand side may be a mapping or
// a host object, and full sequence destructuring -- positional slots, a "...rest"
// tail capture binding the remaining elements as a new sequence, and nested
// list/map patterns -- with arity enforced so over/under-supply errors rather than
// silently padding with null or dropping elements (spec 01 Sections 2.1, 3.2). Previously implemented: the @escape
// block region accepts any of the six strategies (html, js, css, html_attr,
// html_attr_relaxed, url) plus off/raw and applies it to its body, sharing the
// same escapers as the escape()/e() filter. Nested regions and the module default
// compose via a strategy stack (save/restore on region entry/exit), so an inner
// region restores the enclosing strategy on exit, and captures/macros/blocks under
// any active strategy yield a Safe value (spec 04 Section 8). @apply joins that
// same safeness model: under an active strategy its filtered body is wrapped Safe
// so the region does not escape it a second time. The code-point strategies (js,
// css, html_attr, html_attr_relaxed) decode their input as UTF-8 and raise a clear
// escaping error naming the strategy and byte offset on an invalid byte, rather
// than silently emitting a replacement character (spec 04 Section 8.2); the
// byte-oriented html and url strategies accept arbitrary bytes losslessly. Also:
// the full spec-03
// standard-library catalogue, arrow functions through
// map/filter/sort/reduce/find and the "has some"/"has every" quantifiers, all six
// escape strategies via the filter, the regex "matches" operator (Go RE2
// dialect), and the whitespace-control trim modifiers (the - / ~ / + flags).
package quill
