// Package quill is a general-purpose, gradually-typed, fast template engine for
// Go.
//
// Quill compiles and renders templates written in the Quill language: a
// brace-delimited, keyword-led surface with pipe filters, arrow functions, and a
// Pratt-parsed expression language. It pairs Twig-class composition -- template
// inheritance, blocks, macros, includes, embeds, and traits -- with a gradual
// type system, a compile-to-Go backend for the hot path, native branch-aware
// coverage, a policy sandbox, streaming output, and byte-exact whitespace
// control. It renders HTML pages, configuration files, emails, program source,
// or any other text with the same engine; no single use case is privileged. The
// runtime depends on nothing outside the Go standard library.
//
// See the guide at https://avmnu-sng.github.io/quill-template-engine/ for the
// language reference, grammar, standard library, runtime semantics, and the
// extension API.
//
// # Compatibility
//
// From v1.0.0 the exported API follows semantic versioning: no exported symbol in
// the root package or the pkg/ packages changes incompatibly within the v1
// series. Error MESSAGE strings are NOT part of that contract -- classify a
// failure by its exported Kind, with errors.As/errors.Is, or against a sentinel
// such as loader.ErrNotFound, never by matching message text. The engine
// internals (the lexer, parser, interpreter, and compile-to-Go backend) live
// under internal/ and are not importable; the quill command's flags, exit codes,
// and generated-source shape are the compile backend's stable surface.
//
// # The facade
//
// Environment is the engine facade. Build one over a Loader with New (or over an
// in-memory template map with NewFromMap), then Render by name. Output escaping
// is off by default, like Go text/template, and undefined variables are strict by
// default; both, along with the sandbox, the type registry, coverage, streaming,
// the compiled backend, and host extensions, are configured through Option
// values.
//
// # The pipeline
//
// A template loads once and is memoized. Loading parses the source into an AST,
// runs the gradual type checker (package check) between parse and interpret, and
// prepares the module for the tree-walking interpreter. The
// checker consumes the annotations the parser threads through the AST -- the
// @types block, @set/@for targets, @macro/@block params and returns, and arrow
// params -- infers types where the spec defines it, and applies the gradual `any`
// fallback everywhere a value is unannotated. It rejects an ill-typed template
// with a positioned error before any byte is rendered, catching the runtime
// errors a static reader can see: a string/number arithmetic mismatch, rendering
// a list/map value, a for over a non-iterable typed iterand, a missing
// member/method, a call with the wrong arity or argument types, a bad map key
// kind, and a set/for/param annotation inconsistent with what flows into it. It
// narrows unions and nullables through `is` tests and null-safe access. Object
// member shapes and host callable signatures come from an optional host registry
// (check.Registry, installed via WithTypes); with no registry, Object types are
// opaque-but-known and host calls are dynamic, and the checker still enforces
// every in-template annotation.
//
// Annotations never change runtime behavior. An unannotated template types
// entirely as `any`, so the checker is silent and the template renders
// byte-for-byte identically to a build that ignores types; removing every
// annotation from any template yields the same bytes.
//
// # Composition and the standard library
//
// The engine renders the full composition surface -- @extends/@block with
// parent(), @macro with defaults and variadics, @import/@from, @use trait reuse,
// @embed, and the statement- and function-form @include -- and the complete
// standard-library catalogue of filters, functions, and tests, including arrow
// functions through map/filter/sort/reduce/find, the `has some`/`has every`
// quantifiers, the `matches` regex operator, and the whitespace-control trim
// modifiers.
//
// # Performance
//
// Templates run on a tree-walking interpreter by default, and a compiled loop or
// module can be generated with the compile-to-Go backend (the quill compile
// command) and installed with WithCompiled for the hot path. Rendering can either return a
// string (Render) or stream to an io.Writer (RenderTo) without buffering the
// whole output.
//
// # Extensions
//
// A host adds its own filters, functions, and tests through the ext package and
// layers them over the core library with WithExtensions (callable sets) or
// WithExtension (Bundle values). A later host layer shadows an earlier one and
// every host layer shadows core.
//
// # Escaping and the sandbox
//
// Output escaping is off by default. HTML escaping is available globally
// (WithAutoescapeHTML) or as one of six strategies applied by the escape/e filter
// and the @escape region. A host-supplied sandbox.Policy (installed with
// WithSandboxPolicy -- the spec's SecurityPolicy) restricts the permitted tags,
// filters, functions, per-type methods, and per-type properties, enforced at
// compile time for the tag/filter/function floor
// and at each host member-access site for the type-graph. The sandbox activates
// globally (WithSandboxActive), per @sandbox region, or per sandboxed include, and
// each violation raises a host-catchable *errors.Security. Allowlisting is uniform
// with no grandfathering: a host callable is gated exactly like a built-in.
//
// # Coverage
//
// An Environment with a cover.Collector attached (WithCoverage) records which
// units and branch arms each render exercised, unioning across renders and
// exported as text, LCOV, or HTML. Coverage is opt-in and zero-overhead when off,
// and instrumentation never changes rendered bytes.
package quill
