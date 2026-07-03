// Package compile lowers a parsed Quill module to a single deterministic Go
// source file whose render function writes the template's output to an
// io.Writer. The generated function evaluates expressions through the same
// runtime operations (package runtime) and callable registry (package ext) the
// tree-walking interpreter uses, so the compiled output is byte-identical to
// the interpreter's for every construct in the compilable subset, including
// runtime error text and template:line positions.
//
// The compiled variable scope is Go locals: every template name binds a
// runtime.Value local, shadowing uses compile-time generations, and loop
// copy-back assigns the inner generation back to the enclosing frame exactly
// where the interpreter's execFor copy-back would. Value semantics mirror the
// interpreter's copy-on-write contract: binds mark arrays shared where
// Scope.Set would, and member assignment unrolls the interpreter's ownPath
// (privatize-and-rebind) per site.
//
// Constructs outside the compilable subset are detected and reported as a
// typed *NotCompilableError naming the construct; Module never emits silently
// wrong code for them.
package compile

import (
	"bytes"
	"fmt"
	"go/format"
	"regexp"
	"strconv"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/ast"
	"github.com/avmnu-sng/quill-template-engine/check"
	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/source"
)

// Options configures one Module compilation.
type Options struct {
	// PackageName is the package clause of the generated file. Empty selects
	// "qtpl". It must be a valid Go identifier and not a Go keyword.
	PackageName string
	// FuncName is the name of the generated render function. Empty selects
	// "Render". It must be a valid Go identifier and not a Go keyword.
	FuncName string
	// AutoescapeHTML selects the module-level output strategy: html when true,
	// off when false, matching the engine's WithAutoescapeHTML option. An
	// @escape region overrides it for its body at compile time.
	AutoescapeHTML bool
	// LenientVariables selects the engine's lenient migration mode
	// (WithStrictVariables(false)): an undefined read yields null and a for
	// over a non-iterable yields an empty loop. The zero value is the engine
	// default, strict variables.
	LenientVariables bool
	// TabWidth is the number of spaces one @tab indent level expands to,
	// matching the engine's WithTabWidth option. Zero selects the engine
	// default of 4. The generated render function carries this width on the
	// engine handle it injects into needs-environment callables, so the tab
	// filter and tab() function honor it exactly like the facade's.
	TabWidth int
	// RandomSeed fixes the seed of the engine's randomness callables (the
	// random() function and the shuffle filter) for deterministic output,
	// matching the engine's WithRandomSeed option. It applies only when
	// RandomSeedSet is true; the zero value leaves the callables on the engine
	// default, a time-seeded source per call. A compiled render seeded like
	// its facade counterpart produces byte-identical random output; an
	// UNSEEDED template whose output depends on random()/shuffle draws from
	// two independent time-seeded sources, so its output compares to the
	// facade's distributionally, never byte-wise.
	RandomSeed int64
	// RandomSeedSet reports whether RandomSeed is meaningful, distinguishing a
	// deliberate seed of zero from the unseeded engine default.
	RandomSeedSet bool
	// Types is the host static-typing registry the gradual type checker
	// consults, matching the engine's WithTypes option. Module runs the same
	// load-time checker the facade runs, so a nil registry behaves exactly
	// like a facade built without WithTypes: Object types are opaque and host
	// callables dynamic, while in-template annotations are still enforced.
	Types *check.Registry
}

// LineMapEntry maps one generated source line to the template line whose
// lowering begins there. The generated file carries the same information as
// "//q:l N" marker comments; Result.LineMap is the parsed table.
type LineMapEntry struct {
	// Generated is the 1-based line number of the marker in Result.Source.
	Generated int
	// Source is the 1-based template line the following statements lower.
	Source int
}

// Result is the output of a successful Module call.
type Result struct {
	// Source is the gofmt-formatted generated Go file.
	Source []byte
	// FuncName is the name of the generated render function.
	FuncName string
	// LineMap maps generated lines to template lines, sorted by Generated.
	LineMap []LineMapEntry
}

// ErrNotCompilable is the sentinel every *NotCompilableError matches through
// errors.Is, so callers can classify a compilation failure without inspecting
// the concrete type.
var ErrNotCompilable = &NotCompilableError{Construct: "construct outside the compilable subset"}

// NotCompilableError reports a template construct outside the compilable
// subset. It names the construct and the template line it appears on.
type NotCompilableError struct {
	// Construct names the unsupported construct, e.g. "@macro" or "function \"include\"".
	Construct string
	// Template is the template name the construct appears in.
	Template string
	// Line is the 1-based template line of the construct, or 0 when unknown.
	Line int
}

// Error renders the construct name with its template:line position.
func (e *NotCompilableError) Error() string {
	if e.Template != "" && e.Line > 0 {
		return fmt.Sprintf("not compilable: %s (%s:%d)", e.Construct, e.Template, e.Line)
	}
	return fmt.Sprintf("not compilable: %s", e.Construct)
}

// Is matches the ErrNotCompilable sentinel so errors.Is classification works
// on any construct instance.
func (e *NotCompilableError) Is(target error) bool {
	_, ok := target.(*NotCompilableError)
	return ok
}

// Module compiles a parsed template module to one Go source file containing a
// render function with the signature
//
//	func <FuncName>(w io.Writer, exts *ext.ExtensionSet, vars map[string]runtime.Value) error
//
// The name parameter is the template name errors and the file header cite; when
// the module's parse source is available it takes precedence so error positions
// match the interpreter's exactly.
//
// Module runs the facade's load-time gates before lowering: the gradual type
// checker (check.Check with Options.Types) and the literal-regex validation of
// `matches` patterns, so a template the facade rejects at load fails here with
// the same error. A construct outside the compilable subset returns a
// *NotCompilableError; any other error is an internal failure.
//
// Byte parity with the facade holds for every compiled construct EXCEPT
// unseeded randomness: without a RandomSeed on both sides, random() and the
// shuffle filter draw from independent time-seeded sources, so such output
// compares distributionally only. Seeding Options and the facade identically
// restores byte parity.
func Module(name string, mod *ast.Node, opts Options) (*Result, error) {
	if mod == nil || mod.Kind != ast.KindModule {
		return nil, fmt.Errorf("compile: Module expects a %s node", ast.KindModule)
	}
	if opts.PackageName == "" {
		opts.PackageName = "qtpl"
	}
	if opts.FuncName == "" {
		opts.FuncName = "Render"
	}
	if opts.TabWidth == 0 {
		opts.TabWidth = 4
	}
	if opts.TabWidth < 0 {
		opts.TabWidth = 0
	}
	if !isGoIdent(opts.PackageName) {
		return nil, fmt.Errorf("compile: package name %q is not a Go identifier", opts.PackageName)
	}
	if !isGoIdent(opts.FuncName) {
		return nil, fmt.Errorf("compile: func name %q is not a Go identifier", opts.FuncName)
	}
	// A Go keyword passes the identifier shape check but crashes go/format
	// with a whole-file dump; reject it upfront with a clear error.
	if goKeywords[opts.PackageName] {
		return nil, fmt.Errorf("compile: package name %q is a Go keyword", opts.PackageName)
	}
	if goKeywords[opts.FuncName] {
		return nil, fmt.Errorf("compile: func name %q is a Go keyword", opts.FuncName)
	}

	// The facade's load-time gates, in the facade's order (LoadTemplate runs
	// check.Check, then PrepareChecked validates literal regexes), so Module
	// rejects exactly what the facade rejects at load, with the same error.
	if err := check.Check(mod, opts.Types); err != nil {
		return nil, err
	}
	if err := checkLiteralRegexps(mod); err != nil {
		return nil, err
	}

	src := mod.Src
	if src == nil {
		src = source.New(name, "")
	}
	c := newCompiler(src, opts)
	if err := c.compileModule(mod); err != nil {
		return nil, err
	}

	raw := c.assemble()
	formatted, err := format.Source(raw)
	if err != nil {
		return nil, fmt.Errorf("compile: generated source does not format: %w\n%s", err, raw)
	}
	for i := 0; i < len(formatted); i++ {
		if formatted[i] >= 0x80 {
			return nil, fmt.Errorf("compile: generated source contains a non-ASCII byte at offset %d", i)
		}
	}
	return &Result{
		Source:   formatted,
		FuncName: opts.FuncName,
		LineMap:  parseLineMap(formatted),
	}, nil
}

// parseLineMap extracts the //q:l marker table from a formatted generated file.
func parseLineMap(src []byte) []LineMapEntry {
	var out []LineMapEntry
	for i, line := range strings.Split(string(src), "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "//q:l ") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(t, "//q:l ")))
		if err != nil {
			continue
		}
		out = append(out, LineMapEntry{Generated: i + 1, Source: n})
	}
	return out
}

// checkLiteralRegexps mirrors the interpreter's compile-time validation of
// literal `matches` patterns (interp's PrepareChecked): every KindMembership
// "matches" node whose right operand is a plain string literal must compile
// under the stdlib RE2 engine regardless of branch reachability, and a failure
// carries the same error text and position the facade load produces.
func checkLiteralRegexps(n *ast.Node) error {
	if n == nil {
		return nil
	}
	if n.Kind == ast.KindMembership && n.Str == "matches" {
		pat := n.Child(1)
		if pat != nil && pat.Kind == ast.KindString {
			if _, err := regexp.Compile(pat.Str); err != nil {
				return errors.New(errors.KindRuntime,
					"invalid RE2 pattern %q: %v", pat.Str, err).At(pat.Src, pat.Line)
			}
		}
	}
	for _, c := range n.Children {
		if err := checkLiteralRegexps(c); err != nil {
			return err
		}
	}
	return nil
}

// goKeywords lists the Go keywords: each passes isGoIdent's shape check but is
// not a legal package or function name, so Module rejects it explicitly.
var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

// isGoIdent reports whether s is a plain ASCII Go identifier.
func isGoIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		alpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
		digit := c >= '0' && c <= '9'
		if i == 0 && !alpha {
			return false
		}
		if !alpha && !digit {
			return false
		}
	}
	return true
}

// notCompilable builds the typed subset error for the construct at node n.
func (c *compiler) notCompilable(construct string, n *ast.Node) error {
	e := &NotCompilableError{Construct: construct, Template: c.src.Name()}
	if n != nil {
		e.Line = n.Line
	}
	return e
}

// assemble stitches the fixed prologue, the collected callable resolutions,
// the root declarations, the lowered statements, and the helper prelude into
// one unformatted Go file.
func (c *compiler) assemble() []byte {
	var b bytes.Buffer
	b.WriteString("// Code generated by the Quill compile backend. DO NOT EDIT.\n")
	fmt.Fprintf(&b, "//\n// Template: %s\n", strconv.QuoteToASCII(c.src.Name()))
	b.WriteString("//\n// Lines marked \"//q:l N\" map the statements that follow to template line N.\n")
	fmt.Fprintf(&b, "package %s\n\n", c.opts.PackageName)
	b.WriteString("import (\n")
	b.WriteString("\tstderrors \"errors\"\n")
	b.WriteString("\t\"io\"\n")
	b.WriteString("\t\"math\"\n")
	b.WriteString("\t\"regexp\"\n")
	b.WriteString("\t\"strings\"\n\n")
	b.WriteString("\tqerrors \"github.com/avmnu-sng/quill-template-engine/errors\"\n")
	b.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/ext\"\n")
	b.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/runtime\"\n")
	b.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/source\"\n")
	b.WriteString(")\n\n")
	b.WriteString("// qSrc anchors every runtime error this render function raises to the\n")
	b.WriteString("// template it was compiled from, so error text matches the interpreter's.\n")
	fmt.Fprintf(&b, "var qSrc = source.New(%s, %s)\n\n",
		strconv.QuoteToASCII(c.src.Name()), strconv.QuoteToASCII(c.src.Code()))
	b.WriteString("// qEnvVal is the engine handle injected into needs-environment callables,\n")
	b.WriteString("// carrying this compilation's engine configuration exactly as the\n")
	b.WriteString("// interpreter's handle carries its Environment's.\n")
	fmt.Fprintf(&b, "var qEnvVal = runtime.Obj(&qEnv{tabWidth: %d, seed: %d, seedSet: %v})\n\n",
		c.opts.TabWidth, c.opts.RandomSeed, c.opts.RandomSeedSet)
	fmt.Fprintf(&b, "// %s renders template %s to w, resolving callables through exts and\n",
		c.opts.FuncName, strconv.QuoteToASCII(c.src.Name()))
	b.WriteString("// reading top-level variables from vars. Output and error behavior are\n")
	b.WriteString("// byte-identical to the interpreter's for the compiled construct set.\n")
	fmt.Fprintf(&b, "func %s(w io.Writer, exts *ext.ExtensionSet, vars map[string]runtime.Value) error {\n", c.opts.FuncName)
	b.WriteString("\tqw := &qWriter{w: w, atLineStart: true}\n")
	b.WriteString("\t_ = qw\n")
	b.WriteString("\tqNames := make([]string, 0, len(vars))\n")
	b.WriteString("\tfor qn := range vars {\n\t\tqNames = append(qNames, qn)\n\t}\n")
	b.WriteString("\t_ = qNames\n")
	for _, cr := range c.callables {
		fmt.Fprintf(&b, "\t%s, %s := exts.%s(%s)\n", cr.val, cr.ok, cr.method, strconv.QuoteToASCII(cr.name))
		fmt.Fprintf(&b, "\t_, _ = %s, %s\n", cr.val, cr.ok)
		if cr.fast == "" {
			continue
		}
		// The arity-known dispatch decision, hoisted with the lookup: the
		// filter's Fn1 runs only when it exists and no Needs* flag asks for
		// engine-value injection, so a host re-registration without Fn1 or
		// with injection flags turns every site back to the general path.
		fmt.Fprintf(&b, "\t%s := %s && %s.Fn1 != nil && !%s.NeedsEnvironment && !%s.NeedsContext && !%s.NeedsCharset\n",
			cr.fast, cr.ok, cr.val, cr.val, cr.val, cr.val)
		fmt.Fprintf(&b, "\t_ = %s\n", cr.fast)
	}
	b.Write(c.rootDecls.Bytes())
	b.Write(c.body.Bytes())
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n\n")
	b.WriteString(prelude)
	return b.Bytes()
}
