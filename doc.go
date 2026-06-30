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
// Implemented this slice (gradual type checker): a front-end pass (package
// check) runs at template Load, between parse and interpret, and rejects
// ill-typed templates with positioned KindTypeCheck errors BEFORE any byte is
// rendered (spec 04 Sections 1-3, design/type-system.md). It consumes the
// annotations the parser already threads through the AST -- the @types block,
// @set/@for targets, @macro/@block params and returns, and arrow params --
// infers types bottom-up where the spec defines it (literals, member/index
// access, operators, the higher-order filters map/filter/sort/reduce/find which
// propagate element types through arrows, and the ??/default coalescing rule),
// and applies the gradual `any` fallback everywhere a value is unannotated. The
// consistency relation (not subtyping) governs flow: `any` is consistent with
// every type in both directions, with a runtime kind-check backstop scheduled at
// the any-to-typed boundary (the shallow cast keeps the strict runtime as the
// floor on structured data it did not fully verify). The checker catches the
// runtime errors a static reader can see -- a string/number arithmetic mismatch,
// rendering a list/map (non-renderable) value, a for over a non-iterable typed
// iterand, a missing member/method on a typed Object or an annotation-declared
// name, a call with wrong arity or argument types against a macro/host
// signature, a bad map key kind, an unknown host type in an annotation, and a
// set/for/param annotation inconsistent with what flows into it -- and narrows
// unions and nullables through `is`-tests and ?. so a narrowed branch type-checks
// (spec 04 Section 8). Object<"Name"> member shapes and host callable signatures
// come from an optional host registry (check.Registry, installed via
// quill.WithTypes); with no registry, Object types are opaque-but-known and host
// calls are dynamic, and the checker still enforces every in-template
// annotation. The CRITICAL INVARIANT holds: annotations never change runtime
// behavior. An unannotated template types entirely as `any`, so the checker is
// silent and the pre-type-checker conformance fixtures render byte-for-byte
// identically; removing every annotation from any template yields the same bytes.
//
// Previously implemented (sandbox): a host-supplied SecurityPolicy
// (sandbox.Policy) restricts the permitted tags, filters, functions, per-type
// methods, and per-type properties; enforcement is two-phase. Phase 1 collects
// the statement keywords, filters, and functions a template uses at compile time
// (the range operator ".." counts as the range function) and validates that set
// against the policy once per render when the sandbox is active, mapping a
// violation to its source line. Phase 2 gates host member access at the access
// site: a method call, a property read, and the string-coercion of a host object
// (via its Stringify hook) each consult the policy, matched against an explicit
// host TYPE-GRAPH (sandbox.TypeGraph) rather than reflection, with case-sensitive
// method names and property-then-method precedence. The string-coercion gate
// fires at EVERY coercion site, not only an interpolation: it also gates an
// operand of "~" concat and every host object reachable as an argument of the
// coercing filters (join, replace, split), which would otherwise stringify it
// inside the extension layer beyond the policy's reach. A higher-order filter
// rejects a non-template (host) callable, on both the inline "| map(f)" form and
// the @apply filter path. Safe values and engine-internal shims bypass the member
// checks. Allowlisting is uniform with NO grandfathering. Strict-versus-lenient
// member-access reporting is supported (sandbox.Policy.Strict): in strict mode an
// access on a host type the policy does not know at all -- no method/property
// entry and absent from the type-graph -- reports a distinct unknown-type error,
// while lenient mode falls through to the ordinary per-member deny; the
// tag/filter/function floor is identical in both modes. The @sandbox region
// forces sandboxing over its body and any templates included within it, restoring
// the prior gate on exit and never disabling an already-sandboxed enclosing
// render; the function-form include's sandboxed flag does the same per include.
// Each violation class raises a distinct, host-catchable *errors.Security
// (errors.As + a SecurityClass) carrying the offending name and, for member
// violations, the host type name (spec 04 Section 8.3).
//
// Previously implemented (composition tail): @use horizontal trait reuse merges a
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
