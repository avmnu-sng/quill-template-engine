# Quill -- Extensions

Quill's standard library is the floor, not the ceiling. A host application adds
its own filters, functions, and tests -- and its own constants and enumerations
-- through the `ext` package, layers them over the core library when it builds an
`Environment`, and (optionally) teaches the sandbox and the type checker about
them. This document is the full reference for that surface.

The three callable kinds map to the three syntactic positions:

- a **filter** is invoked through the pipe, `x | name(args)`, which is
  `name(x, args)`;
- a **function** is invoked as `name(args)`, every argument explicit;
- a **test** is applied as `x is name` or `x is name(arg)` and yields a boolean.

A name resolves to at most one filter, one function, and one test; the position
selects which family is consulted, so the same name may denote a filter and a
function without collision (the standard library uses this).

--------------------------------------------------------------------------------

## 1. The callable value objects

Every callable is a small struct in package `ext`. The struct form gives you full
control: you receive the already-flattened argument slice and return a
`runtime.Value`.

```go
type Filter struct {
	Name string
	Fn   func(args []runtime.Value) (runtime.Value, error)

	NeedsEnvironment bool
	NeedsContext     bool
	NeedsCharset     bool
}

type Function struct {
	Name string
	Fn   func(args []runtime.Value) (runtime.Value, error)

	NeedsEnvironment bool
	NeedsContext     bool
	NeedsCharset     bool
}

type Test struct {
	Name string
	Fn   func(args []runtime.Value) (bool, error)
}
```

For a filter, the interpreter flattens the piped value and the explicit arguments
(positional, named, and spread) into one `[]runtime.Value` in call order, so
`"ab" | repeat(3)` reaches `Fn` as `[]runtime.Value{Str("ab"), Int(3)}`. A
function receives just its explicit arguments; a test receives the tested value
first, then any argument.

The struct form is the right tool when you need to inspect argument kinds, accept
a variable shape, or build the result value by hand:

```go
set := ext.NewExtensionSet()
set.AddFilter(&ext.Filter{
	Name: "reverse_str",
	Fn: func(args []runtime.Value) (runtime.Value, error) {
		s, _ := runtime.ToText(args[0])
		r := []rune(s)
		for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
			r[i], r[j] = r[j], r[i]
		}
		return runtime.Str(string(r)), nil
	},
})
```

--------------------------------------------------------------------------------

## 2. The typed helpers

Most callables are ordinary Go functions over ordinary Go types. The typed
helpers `NewFilter`, `NewFunction`, and `NewTest` take such a function and wrap it
in the `[]runtime.Value`-based `Fn` the engine calls, so you never touch a
`runtime.Value` in the body:

```go
set := ext.NewExtensionSet()
set.AddFilter(ext.NewFilter("repeat", func(s string, n int64) string {
	return strings.Repeat(s, int(n))
}))
set.AddFunction(ext.NewFunction("clamp", func(x, lo, hi int64) int64 {
	switch {
	case x < lo:
		return lo
	case x > hi:
		return hi
	default:
		return x
	}
}))
set.AddTest(ext.NewTest("even", func(n int64) bool { return n%2 == 0 }))
```

The helpers inspect the function's signature **once, at registration time**, and
resolve one converter per parameter and one for the result. The wrapper the
render loop invokes marshals values through those pre-resolved converters and does
no further signature inspection.

### 2.1 Marshaling rules

Each parameter and result type marshals between a Go type and a `runtime.Value`:

| Go type                       | runtime.Value          |
|-------------------------------|------------------------|
| `string`                      | `Str` (and `Safe`)     |
| `bool`                        | `Bool`                 |
| `int`, `int8` .. `int64`      | `Int`                  |
| `float32`, `float64`          | `Float` (an `Int` widens to float on input) |
| `[]T`                         | `*Array` (element-wise) |
| `runtime.Value`               | passed through unchanged |

A parameter typed `runtime.Value` is the escape hatch inside a typed helper: that
argument arrives unconverted, so a single callable can mix marshaled and raw
parameters.

### 2.2 Variadics, results, and errors

- A **variadic** final parameter (`func(parts ...string) string`) is supported:
  the fixed leading parameters convert as usual and every trailing argument
  converts to the variadic element type.
- The function may return **nothing** (the result is `Null`), a single value, or
  a value followed by an `error`. A non-nil error returned by the body surfaces as
  a positioned render error.
- `NewTest` requires the function to return a leading `bool` (optionally followed
  by an `error`).

Arity and argument-type mismatches at call time produce a clear typed error
naming the callable and the offending argument, for example
`filter repeat: expected 2 argument(s), got 1`. An **unsupported function shape**
is a registration-time programming error, not a template fault, so the helper
**panics** when you register it: a non-func value, an unsupported parameter or
result type, too many results, a second result that is not `error`, or a test
that does not return a bool.

The struct form (Section 1) and the typed helpers interoperate freely -- a single
`ExtensionSet` can hold both -- so reach for the helper by default and drop to the
struct form only where you need the raw slice.

--------------------------------------------------------------------------------

## 3. The ExtensionSet registry

An `ExtensionSet` is the callable registry: name-keyed maps for filters,
functions, and tests, plus the host constant and enumeration tables. Build one
with `NewExtensionSet` and add to it:

```go
set := ext.NewExtensionSet()
set.AddFilter(f)        // register or shadow a filter by name
set.AddFunction(fn)     // ... a function
set.AddTest(t)          // ... a test
set.AddConstant("PI", runtime.Float(3.14159))
set.AddEnum("Color", []runtime.Value{runtime.Str("red"), runtime.Str("green")})
```

Adding a name that already exists **shadows** the earlier entry -- this is exactly
how a host overrides a built-in of the same kind and name. Lookups
(`Filter`/`Function`/`Test`) and existence checks (`HasFilter`/`HasFunction`/
`HasTest`, which back the `@guard` statement) are by exact name. `Clone` returns a
shallow copy with independent maps, so you can layer additions over a base set
without mutating it.

Constants back the `constant()` function and the `is constant` test; enumerations
(ordered case lists) back `enum()` and `enum_cases()` (see
[`03-stdlib.md`](03-stdlib.md) Section 3.2).

--------------------------------------------------------------------------------

## 4. Extension bundles

An `Extension` is a cohesive bundle of callables and host tables a third party
ships as a single unit:

```go
type Extension interface {
	Filters() []*Filter
	Functions() []*Function
	Tests() []*Test
	Constants() map[string]runtime.Value
	Enums() map[string][]runtime.Value
}
```

A bundle implements only the families it provides. `BaseExtension` supplies a
nil-returning implementation of every method, so a bundle embeds it and overrides
just the ones it ships:

```go
type MathExt struct{ ext.BaseExtension }

func (MathExt) Functions() []*ext.Function {
	return []*ext.Function{
		ext.NewFunction("clamp", func(x, lo, hi int64) int64 {
			switch {
			case x < lo:
				return lo
			case x > hi:
				return hi
			default:
				return x
			}
		}),
	}
}

func (MathExt) Filters() []*ext.Filter {
	return []*ext.Filter{ext.NewFilter("times", func(x, n int64) int64 { return x * n })}
}
```

`ExtensionSet.Register(bundle)` folds a bundle in: each filter, function, and test
is added by name (later wins), and every constant and enumeration is merged into
the set's host tables. `Register` returns the receiver so calls chain.

--------------------------------------------------------------------------------

## 5. Composition and shadow order

Two primitives compose registries, both following the uniform **later wins** rule:

- `ExtensionSet.Merge(other)` folds another set's callables and tables into the
  receiver, with `other` shadowing the receiver on every name collision. A nil
  `other` is a no-op.
- `ExtensionSet.Register(bundle)` folds a bundle in with the same rule.

When you build an `Environment`, the registry is layered bottom-up:

1. the **core** standard library is the floor;
2. the engine-bound composition callables (the include/block family);
3. each host layer supplied via `WithExtensions` and `WithExtension`, **in the
   order the options were passed**.

So a later host layer shadows an earlier one, and every host layer shadows core --
a host can override any built-in without editing the engine. `WithExtensions`
takes one or more `*ExtensionSet` layers; `WithExtension` takes one or more
`Extension` bundles; multiple options accumulate, and sets and bundles interleave
in option order.

```go
env := quill.New(ldr,
	quill.WithExtensions(baseSet),   // over core
	quill.WithExtension(MathExt{}),  // over baseSet
	quill.WithExtensions(overrides), // over MathExt
)
```

--------------------------------------------------------------------------------

## 6. Injection flags

A filter or function may need an engine value the template author never passes.
The three flags on `Filter` and `Function` request them:

- `NeedsEnvironment` -- a handle back into the engine, so the callable can load,
  render, or read the source of another template.
- `NeedsContext` -- the live call-site scope, materialized as an `*Array` of the
  variables visible where the callable was called.
- `NeedsCharset` -- the active charset.

When a flag is set, the interpreter **prepends** the requested value(s) ahead of
the piped value and the user arguments, in the fixed order **environment,
context, charset**. A callable that sets `NeedsContext` therefore receives the
context `*Array` as its first argument, then the piped value, then the explicit
arguments. Set only the flags you use; the built-in `include`/`dump`/`source`
family sets them, and they are available to host callables for the same reasons.

The typed helpers do not set these flags; a callable that needs injection uses
the struct form (or sets the flags on the struct the helper returns) and reads
the prepended values off the front of `args`.

--------------------------------------------------------------------------------

## 7. Custom callables and the sandbox

When the sandbox is active, a host callable is gated **exactly like a built-in**:
there is no grandfathering. A filter or function is denied unless the policy
allowlists it by name.

```go
pol := &sandbox.Policy{
	Filters:   map[string]bool{"times": true}, // allow the custom filter
	Functions: map[string]bool{},
	Tags:      map[string]bool{},
	Graph:     sandbox.NewTypeGraph(),
}
env := quill.New(ldr,
	quill.WithExtensions(set),
	quill.WithSandboxPolicy(pol),
	quill.WithSandboxActive(true),
)
```

A custom filter whose name is in `Policy.Filters` passes; one that is not raises a
host-catchable `*errors.Security` naming the offending filter. Any host object a
custom callable exposes is gated by the same per-type method/property rules and
the type-graph as every other host value (see
[`04-types-and-semantics.md`](04-types-and-semantics.md) Section 8.3).

--------------------------------------------------------------------------------

## 8. Custom callables and the type checker

The gradual type checker runs at template load. A host callable with **no
registered signature** types as `any`: an annotated template that calls it loads
and renders without a type error, and the call's result flows on as `any` (the
dynamic floor host calls already use).

To have the checker verify a call against a custom callable, register its
signature in a `check.Registry` and install it with `quill.WithTypes`:

```go
reg := check.NewRegistry()
reg.AddSignature("clamp", &check.Signature{
	Params: []*check.Type{check.Int, check.Int, check.Int},
	Ret:    check.Int,
})
env := quill.New(ldr, quill.WithExtensions(set), quill.WithTypes(reg))
```

With the signature registered, a call to `clamp` with the wrong arity or an
argument of the wrong type is rejected at load time, before any byte is rendered.
Registering a signature never changes runtime behavior -- it only moves an error
earlier in time, so a template renders identical bytes with or without the
registry.

--------------------------------------------------------------------------------

## 9. A complete example

The runnable [`examples/extension`](../examples/extension) registers a custom
filter and function with the typed helpers, layers them over core with
`WithExtensions`, and renders a template that uses both:

```go
func callables() *ext.ExtensionSet {
	set := ext.NewExtensionSet()
	set.AddFilter(ext.NewFilter("repeat", func(s string, n int64) string {
		return strings.Repeat(s, int(n))
	}))
	set.AddFunction(ext.NewFunction("clamp", func(x, lo, hi int64) int64 {
		switch {
		case x < lo:
			return lo
		case x > hi:
			return hi
		default:
			return x
		}
	}))
	return set
}

env := quill.NewWithArray(
	map[string]string{"demo.ql": `{{ "ab" | repeat(3) }} {{ clamp(42, 0, 10) }}`},
	quill.WithExtensions(callables()),
)
out, _ := env.Render("demo.ql", nil) // ababab 10
```

Run it with `go run ./examples/extension`.
