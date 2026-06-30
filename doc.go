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
//   - The full stdlib catalogue. This milestone ships the subset in ext.Core;
//     unregistered filters/functions/tests raise an "unknown ..." error.
//   - Escape strategies beyond html: js, css, html_attr, html_attr_relaxed, url
//     raise an explicit "not implemented" error when requested (spec 03 5.5).
//   - The sandbox (@sandbox renders its body transparently; no policy is yet
//     enforced) and @cache (renders its body, no caching), spec 01 Section 4.7.
//   - The membership operators "has some", "has every", map destructuring in
//     @set, and @use traits raise explicit errors where reached. The regex
//     "matches" operator (Go RE2 dialect) and the whitespace-control trim
//     modifiers (the - / ~ / + flags) ARE implemented.
package quill
