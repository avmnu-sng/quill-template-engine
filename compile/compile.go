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
	"sort"
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
	// Templates carries the sibling modules a static @include may inline, keyed
	// by template name (the same map Unit consumes as its whole template set).
	// A Module compilation reaches an included partial's statements only through
	// this map; a name absent from it makes a plain @include of that literal a
	// typed subset rejection, and an ignore-missing @include of it a
	// gate-guarded render-nothing. The entry itself may appear here and is
	// ignored. Unit fills this from its own templates argument, so a Unit needs
	// nothing set here.
	Templates map[string]*ast.Node
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
// alongside an exported <FuncName>Manifest value (package compiled) describing
// the unit to the Environment's by-name dispatch: quill.WithCompiled installs
// the manifest and serves renders of the entry template through the generated
// function whenever the Environment's configuration matches the manifest's
// fingerprint and the loader still serves the compiled source bytes.
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
	normalized, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	opts = normalized

	// The facade's load-time gates, in the facade's order (LoadTemplate runs
	// check.Check, then PrepareChecked validates literal regexes), so Module
	// rejects exactly what the facade rejects at load, with the same error.
	if err := check.Check(mod, opts.Types); err != nil {
		return nil, err
	}
	if err := checkLiteralRegexps(mod); err != nil {
		return nil, err
	}

	c := newCompiler(moduleSource(name, mod), opts)
	if err := c.compileModule(mod); err != nil {
		return nil, err
	}
	return c.finish()
}

// normalizeOptions applies the Options defaults and validates the generated
// Go identifiers, shared by the Module and Unit entry points.
func normalizeOptions(opts Options) (Options, error) {
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
		return opts, fmt.Errorf("compile: package name %q is not a Go identifier", opts.PackageName)
	}
	if !isGoIdent(opts.FuncName) {
		return opts, fmt.Errorf("compile: func name %q is not a Go identifier", opts.FuncName)
	}
	// A Go keyword passes the identifier shape check but crashes go/format
	// with a whole-file dump; reject it upfront with a clear error.
	if goKeywords[opts.PackageName] {
		return opts, fmt.Errorf("compile: package name %q is a Go keyword", opts.PackageName)
	}
	if goKeywords[opts.FuncName] {
		return opts, fmt.Errorf("compile: func name %q is a Go keyword", opts.FuncName)
	}
	return opts, nil
}

// moduleSource returns the parse source of one member module, synthesizing an
// empty-code source when the module carries none so error positions still
// name the template.
func moduleSource(name string, mod *ast.Node) *source.Source {
	if mod.Src != nil {
		return mod.Src
	}
	return source.New(name, "")
}

// finish assembles, formats, and hygiene-checks the generated file.
func (c *compiler) finish() (*Result, error) {
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
		FuncName: c.opts.FuncName,
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

// notCompilable builds the typed subset error for the construct at node n,
// naming the member template whose statements are being lowered.
func (c *compiler) notCompilable(construct string, n *ast.Node) error {
	name := c.src.Name()
	ref := c.srcRef()
	for _, s := range c.srcs {
		if c.srcVars[s] == ref {
			name = s.Name()
			break
		}
	}
	e := &NotCompilableError{Construct: construct, Template: name}
	if n != nil {
		e.Line = n.Line
	}
	return e
}

// sortedAbsentIncludes returns the ignore-missing include targets recorded as
// compile-time-absent, sorted so the generated manifest stays byte-identical
// across recompilations of the same template set.
func (c *compiler) sortedAbsentIncludes() []string {
	if len(c.absentIncludes) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.absentIncludes))
	for name := range c.absentIncludes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// assemble stitches the fixed prologue, the collected callable resolutions,
// the root declarations, the lowered statements, the dispatch manifest, and
// the helper prelude into one unformatted Go file.
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
	if c.usesStrconv || c.usesSlots {
		b.WriteString("\t\"strconv\"\n")
	}
	b.WriteString("\t\"strings\"\n")
	if c.usesSlots {
		b.WriteString("\t\"sync/atomic\"\n")
	}
	b.WriteString("\n")
	b.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/compiled\"\n")
	b.WriteString("\tqerrors \"github.com/avmnu-sng/quill-template-engine/errors\"\n")
	b.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/ext\"\n")
	b.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/runtime\"\n")
	b.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/source\"\n")
	b.WriteString(")\n\n")
	b.WriteString("// qSrc anchors every runtime error this render function raises to the\n")
	b.WriteString("// template it was compiled from, so error text matches the interpreter's.\n")
	if len(c.srcs) == 1 {
		fmt.Fprintf(&b, "var qSrc = source.New(%s, %s)\n\n",
			strconv.QuoteToASCII(c.src.Name()), strconv.QuoteToASCII(c.src.Code()))
	} else {
		// A unit carries one source anchor per member template, so an error
		// raised by an inlined block body cites the defining template.
		b.WriteString("var (\n")
		for _, s := range c.srcs {
			fmt.Fprintf(&b, "\t%s = source.New(%s, %s)\n",
				c.srcVars[s], strconv.QuoteToASCII(s.Name()), strconv.QuoteToASCII(s.Code()))
		}
		b.WriteString(")\n\n")
	}
	b.WriteString("// qEnvVal is the engine handle injected into needs-environment callables,\n")
	b.WriteString("// carrying this compilation's engine configuration exactly as the\n")
	b.WriteString("// interpreter's handle carries its Environment's.\n")
	fmt.Fprintf(&b, "var qEnvVal = runtime.Obj(&qEnv{tabWidth: %d, seed: %d, seedSet: %v})\n\n",
		c.opts.TabWidth, c.opts.RandomSeed, c.opts.RandomSeedSet)
	fmt.Fprintf(&b, "// %s renders template %s to w, resolving callables through exts and\n",
		c.opts.FuncName, strconv.QuoteToASCII(c.src.Name()))
	b.WriteString("// reading top-level variables from vars. Output and error behavior are\n")
	b.WriteString("// byte-identical to the interpreter's for the compiled construct set.\n")
	if c.usesSlots {
		c.assembleSlotHeader(&b)
	} else {
		fmt.Fprintf(&b, "func %s(w io.Writer, exts *ext.ExtensionSet, vars map[string]runtime.Value) error {\n", c.opts.FuncName)
		if !c.tabFree {
			b.WriteString("\tqw := &qWriter{w: w, atLineStart: true}\n")
			b.WriteString("\t_ = qw\n")
		}
	}
	b.WriteString("\tqNames := make([]string, 0, len(vars))\n")
	b.WriteString("\tfor qn := range vars {\n\t\tqNames = append(qNames, qn)\n\t}\n")
	b.WriteString("\t_ = qNames\n")
	for _, cr := range c.callables {
		fmt.Fprintf(&b, "\t%s, %s := exts.%s(%s)\n", cr.val, cr.ok, cr.method, strconv.QuoteToASCII(cr.name))
		fmt.Fprintf(&b, "\t_, _ = %s, %s\n", cr.val, cr.ok)
		if cr.fast != "" {
			// The arity-known dispatch decision, hoisted with the lookup: the
			// filter's Fn1 runs only when it exists and no Needs* flag asks for
			// engine-value injection, so a host re-registration without Fn1 or
			// with injection flags turns every site back to the general path.
			fmt.Fprintf(&b, "\t%s := %s && %s.Fn1 != nil && !%s.NeedsEnvironment && !%s.NeedsContext && !%s.NeedsCharset\n",
				cr.fast, cr.ok, cr.val, cr.val, cr.val, cr.val)
			fmt.Fprintf(&b, "\t_ = %s\n", cr.fast)
		}
		if cr.inject != "" {
			// The injection decision, hoisted with the lookup: any Needs* flag
			// on the resolved callable routes its call sites through the
			// engine-value injection path, so an injection-free callable skips
			// the whole test-and-inject residue on one bool per invocation.
			fmt.Fprintf(&b, "\t%s := %s && (%s.NeedsEnvironment || %s.NeedsContext || %s.NeedsCharset)\n",
				cr.inject, cr.ok, cr.val, cr.val, cr.val)
			fmt.Fprintf(&b, "\t_ = %s\n", cr.inject)
		}
	}
	b.Write(c.rootDecls.Bytes())
	b.Write(c.body.Bytes())
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n\n")
	if c.usesSlots {
		b.WriteString(slotTokenSupport)
	}
	fmt.Fprintf(&b, "// %sManifest describes this compiled unit to the Environment's dispatch\n", c.opts.FuncName)
	b.WriteString("// (quill.WithCompiled): the entry template, its embedded source text, the\n")
	b.WriteString("// fingerprint of the compile options its bytes depend on, and the render\n")
	b.WriteString("// entry point.\n")
	fmt.Fprintf(&b, "var %sManifest = &compiled.Manifest{\n", c.opts.FuncName)
	b.WriteString("\tEntry:   qSrc.Name(),\n")
	if len(c.srcs) == 1 {
		b.WriteString("\tSources: map[string]string{qSrc.Name(): qSrc.Code()},\n")
	} else {
		b.WriteString("\tSources: map[string]string{\n")
		for _, s := range c.srcs {
			v := c.srcVars[s]
			fmt.Fprintf(&b, "\t\t%s.Name(): %s.Code(),\n", v, v)
		}
		b.WriteString("\t},\n")
	}
	fmt.Fprintf(&b, "\tFingerprint: compiled.Fingerprint{AutoescapeHTML: %v, LenientVariables: %v, TabWidth: %d, RandomSeed: %d, RandomSeedSet: %v},\n",
		c.opts.AutoescapeHTML, c.opts.LenientVariables, c.opts.TabWidth, c.opts.RandomSeed, c.opts.RandomSeedSet)
	fmt.Fprintf(&b, "\tUsesLog: %v,\n", c.usesLog)
	fmt.Fprintf(&b, "\tUsesSlots: %v,\n", c.usesSlots)
	if names := c.sortedAbsentIncludes(); len(names) > 0 {
		b.WriteString("\tAbsentIncludes: []string{\n")
		for _, name := range names {
			fmt.Fprintf(&b, "\t\t%s,\n", strconv.QuoteToASCII(name))
		}
		b.WriteString("\t},\n")
	}
	fmt.Fprintf(&b, "\tRender:  %s,\n", c.opts.FuncName)
	b.WriteString("}\n\n")
	b.WriteString(prelude)
	return b.Bytes()
}

// assembleSlotHeader writes the render function signature and prologue for a
// unit that uses deferred slots. Output is buffered into an internal builder
// (qout) rather than streamed to w so the yield placeholders can be resolved
// over the finished buffer, mirroring interp renderBuffered plus resolveSlots.
// The per-render slot state -- the accumulating buffers, the render-order label
// list, and the unique placeholder token -- lives as function locals so
// concurrent renders of one compiled unit never share it. A deferred resolve
// pass runs on the success return; on an error return it writes the partial,
// unresolved buffer, matching renderBuffered's error shape (the partial buffer
// the interpreter returns alongside the error).
func (c *compiler) assembleSlotHeader(b *bytes.Buffer) {
	fmt.Fprintf(b, "func %s(w io.Writer, exts *ext.ExtensionSet, vars map[string]runtime.Value) (qErr error) {\n", c.opts.FuncName)
	b.WriteString("\tvar qout strings.Builder\n")
	if !c.tabFree {
		b.WriteString("\tqw := &qWriter{w: &qout, atLineStart: true}\n")
		b.WriteString("\t_ = qw\n")
	}
	b.WriteString("\tvar qslots map[string]*strings.Builder\n")
	b.WriteString("\tvar qyielded []string\n")
	b.WriteString("\tqtok := qnewYieldToken()\n")
	b.WriteString("\tdefer func() {\n")
	b.WriteString("\t\tif qErr != nil {\n")
	b.WriteString("\t\t\t_, _ = io.WriteString(w, qout.String())\n")
	b.WriteString("\t\t\treturn\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tqres := qout.String()\n")
	b.WriteString("\t\tfor _, qlabel := range qyielded {\n")
	b.WriteString("\t\t\tqph := qtok + qlabel + qtok\n")
	b.WriteString("\t\t\tvar qc string\n")
	b.WriteString("\t\t\tif qb, qok := qslots[qlabel]; qok {\n")
	b.WriteString("\t\t\t\tqc = qb.String()\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tqres = strings.ReplaceAll(qres, qph, qc)\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tif _, werr := io.WriteString(w, qres); werr != nil {\n")
	b.WriteString("\t\t\tqErr = werr\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}()\n")
}
