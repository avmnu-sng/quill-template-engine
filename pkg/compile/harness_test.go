package compile_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/ast"
	"github.com/avmnu-sng/quill-template-engine/pkg/compile"
	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/source"
)

// compiledCase is one template to compile into the scratch module and render
// in a child process.
type compiledCase struct {
	name     string // unique id; also the fixture package directory
	template string // template body (the main template)
	varsJSON string // data as a JSON object; "" means no vars
	opts     compile.Options
	// templates, when non-nil, makes this a multi-template case: it maps member
	// names to bodies, entry names the entry template, and template above is
	// ignored. It compiles through Unit by default, or through Module with the
	// non-entry members handed as Options.Templates when viaModule is set, so
	// the same include fixtures exercise both single-template and unit lowerings.
	templates map[string]string
	entry     string
	// viaModule compiles a multi-template case through compile.Module (with the
	// siblings as Options.Templates) instead of compile.Unit, so a static
	// @include is exercised on the single-template Module path too.
	viaModule bool
	// envCheck additionally renders the case by name through a quill
	// Environment with the generated manifest installed (frame "<name>@env"),
	// probes the dispatch gate with a tracer manifest (frame "<name>@tracer",
	// payload "served" or "fell-back"), and drives the fingerprint matrix
	// (frame "<name>@matrix", payload "ok"): one mixed serve/fallback
	// Environment per autoescape/strict combination plus a tab-width flip and
	// a random-seed flip, each byte- and error-compared against a
	// manifest-free Environment under the same options, with a tracer proving
	// the gate serves exactly the matching combination. Every probe
	// Environment is configured from the generated manifest's fingerprint, so
	// a case may carry any output-shaping compile option.
	envCheck bool
	// errPath marks a case whose render errors mid-stream: it drives the case
	// through Environment.RenderTo under WithCompiled (frame "<name>@renderto",
	// payload "ok") and asserts the compiled streaming path writes byte-exactly
	// what the interpreter's RenderTo writes on the same error -- for a slots
	// unit, nothing, with no raw yield placeholder reaching the writer.
	errPath bool
	// cacheCheck marks an @cache case whose store persistence across renders is
	// the contract under test: it renders the case TWICE on one long-lived
	// Environment with WithCompiled installed (varsJSON then varsJSON2) and
	// compares each render byte- and error-exactly against an interpreter-only
	// Environment rendered the same two times (frame "<name>@cache", payload
	// "ok"). A single render cannot observe a cross-render hit; a warm store
	// makes the second render replay the first render's body, so a divergent
	// store decision -- a handle-less always-miss or a per-render cache instead
	// of the Environment's shared one -- changes the second render's bytes and
	// fails here.
	cacheCheck bool
	// varsJSON2 is the second render's data for a cacheCheck case, distinct from
	// varsJSON so a warm hit that replays the first body is observable.
	varsJSON2 string
	// sharedRootPeer names a second case whose manifest is installed on the SAME
	// Environment as this one, so a cross-unit @cache key collision is observable:
	// the runner (frame "<name>@sharedroot", payload "ok") renders this case's
	// entry (with varsJSON) and the peer's entry (with sharedPeerVars) on one
	// Environment sharing one RenderCache, byte-comparing each against a
	// two-manifest interpreter Environment. Two units that reach one @cache under
	// one user key through different render roots (two @extends children of one
	// base) must key under their own roots, so the second unit renders fresh
	// rather than replaying the first unit's stored body. An error-position-keyed
	// namespace would collide them under the shared definer and serve wrong bytes.
	sharedRootPeer string
	// sharedPeerVars is the data the sharedRootPeer entry renders with, distinct
	// from varsJSON so a collision that replays this case's body is observable.
	sharedPeerVars string
}

// caseResult is one rendered case's outcome from the scratch process.
type caseResult struct {
	out     string
	errText string
	failed  bool
}

// repoRoot locates the engine repository root by walking up from this package
// (pkg/compile/) until it finds the module's go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found walking up from compile package")
		}
		dir = parent
	}
}

// compileCase parses and compiles one case, failing the test on a parse error
// and returning the compile result or error. A case carrying a templates map
// compiles through Unit, otherwise through Module.
func compileCase(t *testing.T, cs compiledCase) (*compile.Result, error) {
	t.Helper()
	opts := cs.opts
	if opts.PackageName == "" {
		opts.PackageName = pkgName(cs.name)
	}
	if cs.templates != nil {
		mods := map[string]*ast.Node{}
		for name, body := range cs.templates {
			mod, err := parse.Parse(source.New(name, body))
			if err != nil {
				t.Fatalf("%s: parse %s: %v", cs.name, name, err)
			}
			mods[name] = mod
		}
		if cs.viaModule {
			siblings := map[string]*ast.Node{}
			for name, mod := range mods {
				if name != cs.entry {
					siblings[name] = mod
				}
			}
			opts.Templates = siblings
			return compile.Module(cs.entry, mods[cs.entry], opts)
		}
		return compile.Unit(cs.entry, mods, opts)
	}
	mod, err := parse.Parse(source.New(cs.name+".ql", cs.template))
	if err != nil {
		t.Fatalf("%s: parse: %v", cs.name, err)
	}
	return compile.Module(cs.name+".ql", mod, opts)
}

// pkgName derives a Go package name from a case name.
func pkgName(name string) string {
	var b strings.Builder
	b.WriteString("fx")
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			b.WriteByte(ch)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// runCompiled builds ONE scratch Go module containing every case's generated
// package plus a main that renders each case with framed output, runs it
// once, and returns the per-case results. Registry parity with the facade
// comes from building the ExtensionSet through quill.NewFromMap in the
// scratch process.
func runCompiled(t *testing.T, cases []compiledCase, results map[string]*compile.Result) map[string]caseResult {
	t.Helper()
	dir := t.TempDir()
	root := repoRoot(t)

	gomod := fmt.Sprintf("module qscratch\n\ngo 1.23\n\nrequire github.com/avmnu-sng/quill-template-engine v0.0.0\n\nreplace github.com/avmnu-sng/quill-template-engine => %s\n", root)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	// goenv selects the pinned toolchain through .go-version.
	if v, err := os.ReadFile(filepath.Join(root, ".go-version")); err == nil {
		if err := os.WriteFile(filepath.Join(dir, ".go-version"), v, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var mainB bytes.Buffer
	mainB.WriteString("package main\n\nimport (\n")
	mainB.WriteString("\t\"bytes\"\n\t\"encoding/json\"\n\t\"fmt\"\n\t\"io\"\n\t\"os\"\n\t\"strconv\"\n\t\"strings\"\n\n")
	mainB.WriteString("\tquill \"github.com/avmnu-sng/quill-template-engine\"\n")
	mainB.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/pkg/compiled\"\n")
	mainB.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/pkg/cache\"\n")
	mainB.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/pkg/ext\"\n")
	mainB.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/pkg/runtime\"\n")
	var pkgs []string
	for _, cs := range cases {
		res, ok := results[cs.name]
		if !ok || res == nil {
			continue
		}
		pkg := pkgName(cs.name)
		pkgs = append(pkgs, pkg)
		sub := filepath.Join(dir, pkg)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "gen.go"), res.Source, 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&mainB, "\t%s \"qscratch/%s\"\n", pkg, pkg)
	}
	mainB.WriteString(")\n\n")
	mainB.WriteString(mainSupport)
	mainB.WriteString("func main() {\n")
	mainB.WriteString("\texts := quill.NewFromMap(map[string]string{}).Extensions()\n")
	mainB.WriteString("\t_ = exts\n")
	for _, cs := range cases {
		res, ok := results[cs.name]
		if !ok || res == nil {
			continue
		}
		fmt.Fprintf(&mainB, "\trunCase(%q, exts, %q, %s.%s)\n",
			cs.name, cs.varsJSON, pkgName(cs.name), res.FuncName)
		if cs.envCheck {
			fmt.Fprintf(&mainB, "\trunEnvCase(%q, %s.%sManifest, %q)\n",
				cs.name, pkgName(cs.name), res.FuncName, cs.varsJSON)
			fmt.Fprintf(&mainB, "\trunEnvMatrix(%q, %s.%sManifest, %q)\n",
				cs.name, pkgName(cs.name), res.FuncName, cs.varsJSON)
		}
		if cs.errPath {
			fmt.Fprintf(&mainB, "\trunRenderToCase(%q, %s.%sManifest, %q)\n",
				cs.name, pkgName(cs.name), res.FuncName, cs.varsJSON)
		}
		if cs.cacheCheck {
			fmt.Fprintf(&mainB, "\trunCacheCase(%q, %s.%sManifest, %q, %q)\n",
				cs.name, pkgName(cs.name), res.FuncName, cs.varsJSON, cs.varsJSON2)
		}
		if cs.sharedRootPeer != "" {
			peer, ok := results[cs.sharedRootPeer]
			if !ok || peer == nil {
				t.Fatalf("%s: sharedRootPeer %q has no compiled result", cs.name, cs.sharedRootPeer)
			}
			fmt.Fprintf(&mainB, "\trunCacheSharedRootCase(%q, %s.%sManifest, %q, %s.%sManifest, %q)\n",
				cs.name, pkgName(cs.name), res.FuncName, cs.varsJSON,
				pkgName(cs.sharedRootPeer), peer.FuncName, cs.sharedPeerVars)
		}
	}
	mainB.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), mainB.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if len(pkgs) == 0 {
		return map[string]caseResult{}
	}

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run scratch module: %v\nstderr:\n%s", err, stderr.String())
	}
	return parseFramed(t, stdout.String())
}

// parseFramed decodes the scratch process's framed per-case output.
func parseFramed(t *testing.T, out string) map[string]caseResult {
	t.Helper()
	results := map[string]caseResult{}
	for len(out) > 0 {
		if out[0] != '\x01' {
			t.Fatalf("malformed frame near %q", out[:min(len(out), 40)])
		}
		out = out[1:]
		parts := strings.SplitN(out, "\x1f", 4)
		if len(parts) < 4 {
			t.Fatalf("truncated frame header")
		}
		name, flag, lenStr := parts[0], parts[1], parts[2]
		n, err := strconv.Atoi(lenStr)
		if err != nil || n > len(parts[3]) {
			t.Fatalf("bad frame length %q for %q", lenStr, name)
		}
		payload := parts[3][:n]
		out = parts[3][n:]
		r := caseResult{}
		if flag == "1" {
			r.failed = true
			r.errText = payload
		} else {
			r.out = payload
		}
		results[name] = r
	}
	return results
}

// mainSupport is the scratch main's shared code: the order-preserving JSON
// decoder mirroring internal/jsonval (which a module outside the engine
// cannot import) and the framed case runner.
const mainSupport = `func runCase(name string, exts *ext.ExtensionSet, varsJSON string, fn func(io.Writer, *ext.ExtensionSet, map[string]runtime.Value, compiled.RenderCache) error) {
	vars, err := decodeVars(varsJSON)
	if err != nil {
		emit(name, true, "vars decode: "+err.Error())
		return
	}
	var b strings.Builder
	if err := fn(&b, exts, vars, cache.NewRenderCache()); err != nil {
		emit(name, true, err.Error())
		return
	}
	emit(name, false, b.String())
}

func emit(name string, failed bool, payload string) {
	flag := "0"
	if failed {
		flag = "1"
	}
	fmt.Fprintf(os.Stdout, "\x01%s\x1f%s\x1f%d\x1f%s", name, flag, len(payload), payload)
}

func runEnvCase(base string, m *compiled.Manifest, varsJSON string) {
	vars, err := decodeVars(varsJSON)
	if err != nil {
		emit(base+"@env", true, "vars decode: "+err.Error())
		return
	}
	tmpls := manifestTemplates(m)
	env := quill.NewFromMap(tmpls, append(envOpts(m.Fingerprint()), quill.WithCompiled(m))...)
	out, err := env.Render(m.Entry(), vars)
	if err != nil {
		emit(base+"@env", true, err.Error())
	} else {
		emit(base+"@env", false, out)
	}

	// The streaming dispatch must write byte-exactly what Render returns on the
	// success path, including a slots unit routed through RenderTo's scratch
	// buffer: the resolved output reaches w in one write with no placeholder
	// left behind.
	rtvars, err := decodeVars(varsJSON)
	if err != nil {
		emit(base+"@renderto-ok", true, "vars decode: "+err.Error())
	} else {
		rtenv := quill.NewFromMap(tmpls, append(envOpts(m.Fingerprint()), quill.WithCompiled(m))...)
		var rw strings.Builder
		rterr := rtenv.RenderTo(&rw, m.Entry(), rtvars)
		switch {
		case rterr != nil:
			emit(base+"@renderto-ok", true, "RenderTo errored: "+rterr.Error())
		case rw.String() != out:
			emit(base+"@renderto-ok", true, fmt.Sprintf("RenderTo bytes drift: got %q, want %q", rw.String(), out))
		case strings.Contains(rw.String(), "\x00\x01QUILL_SLOT_"):
			emit(base+"@renderto-ok", true, fmt.Sprintf("RenderTo leaked a placeholder: %q", rw.String()))
		default:
			emit(base+"@renderto-ok", false, "ok")
		}
	}

	// The tracer shares the manifest's metadata but renders a marker, so a
	// marker result proves the dispatch gate passes for this configuration;
	// without it the env render above could silently compare the interpreter
	// against itself.
	tenv := quill.NewFromMap(tmpls, append(envOpts(m.Fingerprint()), quill.WithCompiled(tracerManifest(m)))...)
	tvars, err := decodeVars(varsJSON)
	if err != nil {
		emit(base+"@tracer", true, err.Error())
		return
	}
	tout, terr := tenv.Render(m.Entry(), tvars)
	switch {
	case terr != nil:
		emit(base+"@tracer", true, terr.Error())
	case tout == "\x02TRACER\x02":
		emit(base+"@tracer", false, "served")
	default:
		emit(base+"@tracer", false, "fell-back")
	}
}

func manifestTemplates(m *compiled.Manifest) map[string]string {
	tmpls := map[string]string{}
	for n, s := range m.Sources() {
		tmpls[n] = s
	}
	return tmpls
}

// envOpts builds the Environment options a fingerprint describes, so every
// probe Environment is configured from the manifest itself rather than from a
// re-derived subset of the compile options.
func envOpts(fp compiled.Fingerprint) []quill.Option {
	opts := []quill.Option{
		quill.WithAutoescapeHTML(fp.AutoescapeHTML()),
		quill.WithStrictVariables(!fp.LenientVariables()),
		quill.WithTabWidth(fp.TabWidth()),
	}
	if fp.RandomSeedSet() {
		opts = append(opts, quill.WithRandomSeed(fp.RandomSeed()))
	}
	return opts
}

// tracerManifest clones a manifest's dispatch metadata around a marker render:
// a marker result proves the gate served the unit, anything else proves it
// fell back.
func tracerManifest(m *compiled.Manifest) *compiled.Manifest {
	return compiled.NewManifest(compiled.ManifestParams{
		Entry: m.Entry(), Sources: m.Sources(), Fingerprint: m.Fingerprint(), UsesLog: m.UsesLog(),
		Render: func(w io.Writer, _ *ext.ExtensionSet, _ map[string]runtime.Value, _ compiled.RenderCache) error {
			_, err := io.WriteString(w, "\x02TRACER\x02")
			return err
		},
	})
}

func sameErrText(a, b error) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || a.Error() == b.Error()
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// runEnvMatrix drives one case through the fingerprint matrix: the four
// autoescape/strict combinations plus a tab-width flip and a random-seed
// flip. Under every combination the Environment with the manifest installed
// must render byte- and error-identical to a manifest-free Environment with
// the same options (mixed serve/fallback traffic never changes bytes), and a
// tracer manifest must be served exactly when the combination equals the
// unit's fingerprint.
func runEnvMatrix(base string, m *compiled.Manifest, varsJSON string) {
	tmpls := manifestTemplates(m)
	fp := m.Fingerprint()
	var fps []compiled.Fingerprint
	for _, auto := range []bool{false, true} {
		for _, lenient := range []bool{false, true} {
			fps = append(fps, compiled.NewFingerprint(compiled.FingerprintParams{
				AutoescapeHTML: auto, LenientVariables: lenient,
				TabWidth: fp.TabWidth(), RandomSeed: fp.RandomSeed(), RandomSeedSet: fp.RandomSeedSet(),
			}))
		}
	}
	tabFlip := compiled.NewFingerprint(compiled.FingerprintParams{
		AutoescapeHTML: fp.AutoescapeHTML(), LenientVariables: fp.LenientVariables(),
		TabWidth: fp.TabWidth() + 2, RandomSeed: fp.RandomSeed(), RandomSeedSet: fp.RandomSeedSet(),
	})
	seedFlip := compiled.NewFingerprint(compiled.FingerprintParams{
		AutoescapeHTML: fp.AutoescapeHTML(), LenientVariables: fp.LenientVariables(),
		TabWidth: fp.TabWidth(), RandomSeed: fp.RandomSeed() + 1, RandomSeedSet: true,
	})
	fps = append(fps, tabFlip, seedFlip)

	for _, cfp := range fps {
		label := fmt.Sprintf("auto=%v lenient=%v tab=%d seeded=%v",
			cfp.AutoescapeHTML(), cfp.LenientVariables(), cfp.TabWidth(), cfp.RandomSeedSet())

		refVars, err := decodeVars(varsJSON)
		if err != nil {
			emit(base+"@matrix", true, label+": vars decode: "+err.Error())
			return
		}
		ref := quill.NewFromMap(tmpls, envOpts(cfp)...)
		refOut, refErr := ref.Render(m.Entry(), refVars)

		mixVars, err := decodeVars(varsJSON)
		if err != nil {
			emit(base+"@matrix", true, label+": vars decode: "+err.Error())
			return
		}
		mixed := quill.NewFromMap(tmpls, append(envOpts(cfp), quill.WithCompiled(m))...)
		mixOut, mixErr := mixed.Render(m.Entry(), mixVars)
		if mixOut != refOut || !sameErrText(mixErr, refErr) {
			emit(base+"@matrix", true, fmt.Sprintf("%s: mixed render diverged: got %q / %s, want %q / %s",
				label, mixOut, errText(mixErr), refOut, errText(refErr)))
			return
		}

		trcVars, err := decodeVars(varsJSON)
		if err != nil {
			emit(base+"@matrix", true, label+": vars decode: "+err.Error())
			return
		}
		tenv := quill.NewFromMap(tmpls, append(envOpts(cfp), quill.WithCompiled(tracerManifest(m)))...)
		tout, terr := tenv.Render(m.Entry(), trcVars)
		served := terr == nil && tout == "\x02TRACER\x02"
		if served != (cfp == fp) {
			emit(base+"@matrix", true, fmt.Sprintf("%s: gate served=%v, want %v", label, served, cfp == fp))
			return
		}
	}
	emit(base+"@matrix", false, "ok")
}

// runRenderToCase drives a mid-render error case through Environment.RenderTo
// on both engines and asserts the compiled streaming path writes byte-exactly
// what the interpreter's RenderTo writes on the same error. For a slots unit
// the interpreter buffers and writes nothing on error; the compiled unit must
// too, so no raw yield placeholder can reach the caller's writer. This is the
// error-path leak the success-path battery cannot observe.
func runRenderToCase(base string, m *compiled.Manifest, varsJSON string) {
	tmpls := manifestTemplates(m)

	ivars, err := decodeVars(varsJSON)
	if err != nil {
		emit(base+"@renderto", true, "vars decode: "+err.Error())
		return
	}
	ienv := quill.NewFromMap(tmpls, envOpts(m.Fingerprint())...)
	var iw strings.Builder
	ierr := ienv.RenderTo(&iw, m.Entry(), ivars)
	if ierr == nil {
		emit(base+"@renderto", true, "interp RenderTo did not error")
		return
	}

	cvars, err := decodeVars(varsJSON)
	if err != nil {
		emit(base+"@renderto", true, "vars decode: "+err.Error())
		return
	}
	cenv := quill.NewFromMap(tmpls, append(envOpts(m.Fingerprint()), quill.WithCompiled(m))...)
	var cw strings.Builder
	cerr := cenv.RenderTo(&cw, m.Entry(), cvars)
	if !sameErrText(cerr, ierr) {
		emit(base+"@renderto", true, fmt.Sprintf("error text drift: compiled %s, interp %s", errText(cerr), errText(ierr)))
		return
	}
	if cw.String() != iw.String() {
		emit(base+"@renderto", true, fmt.Sprintf("bytes drift: compiled %q, interp %q", cw.String(), iw.String()))
		return
	}
	if strings.Contains(cw.String(), "\x00\x01QUILL_SLOT_") {
		emit(base+"@renderto", true, fmt.Sprintf("raw placeholder leaked: %q", cw.String()))
		return
	}
	emit(base+"@renderto", false, "ok")
}

// runCacheCase drives an @cache case through two renders on ONE long-lived
// Environment with the manifest installed, and compares each render byte- and
// error-exactly against an interpreter-only Environment rendered the same two
// times. The second render exercises the cross-render hit: a warm store makes
// it replay the first render's body regardless of the second render's data, so
// a compiled path that does not share the Environment's store -- an always-miss
// with no handle, or a per-render cache -- diverges from the interpreter on the
// second render and fails here. Both engines render the same two data sets in
// the same order, so the store warms identically on both sides.
func runCacheCase(base string, m *compiled.Manifest, varsJSON, varsJSON2 string) {
	render := func(env *quill.Environment, data string) (string, error) {
		vars, err := decodeVars(data)
		if err != nil {
			return "", err
		}
		return env.Render(m.Entry(), vars)
	}
	tmpls := manifestTemplates(m)
	ienv := quill.NewFromMap(tmpls, envOpts(m.Fingerprint())...)
	cenv := quill.NewFromMap(tmpls, append(envOpts(m.Fingerprint()), quill.WithCompiled(m))...)

	i1, ierr1 := render(ienv, varsJSON)
	c1, cerr1 := render(cenv, varsJSON)
	if c1 != i1 || !sameErrText(cerr1, ierr1) {
		emit(base+"@cache", true, fmt.Sprintf("first render diverged: compiled %q / %s, interp %q / %s",
			c1, errText(cerr1), i1, errText(ierr1)))
		return
	}

	i2, ierr2 := render(ienv, varsJSON2)
	c2, cerr2 := render(cenv, varsJSON2)
	if c2 != i2 || !sameErrText(cerr2, ierr2) {
		emit(base+"@cache", true, fmt.Sprintf("second render diverged (cross-render hit): compiled %q / %s, interp %q / %s",
			c2, errText(cerr2), i2, errText(ierr2)))
		return
	}
	emit(base+"@cache", false, "ok")
}

// runCacheSharedRootCase installs TWO compiled units on ONE Environment so they
// share one RenderCache, then renders each unit's entry and byte-compares against
// a two-manifest interpreter Environment rendered in the same order. When both
// units reach one @cache under one user key through DIFFERENT render roots (two
// @extends children of a shared base), a correct render namespaces each unit's
// store entry under its own root, so the second unit renders its own data fresh.
// A key namespaced by the error-position source collides both units under the
// shared definer, so the second unit replays the first's stored body -- wrong
// bytes the interpreter never produces. Both Environments carry both manifests'
// sources so the interpreter renders the same two entries.
func runCacheSharedRootCase(base string, m *compiled.Manifest, varsJSON string, peer *compiled.Manifest, peerVars string) {
	render := func(env *quill.Environment, entry, data string) (string, error) {
		vars, err := decodeVars(data)
		if err != nil {
			return "", err
		}
		return env.Render(entry, vars)
	}
	tmpls := manifestTemplates(m)
	for n, s := range manifestTemplates(peer) {
		tmpls[n] = s
	}
	ienv := quill.NewFromMap(tmpls, envOpts(m.Fingerprint())...)
	cenv := quill.NewFromMap(tmpls, append(envOpts(m.Fingerprint()), quill.WithCompiled(m, peer))...)

	i1, ierr1 := render(ienv, m.Entry(), varsJSON)
	c1, cerr1 := render(cenv, m.Entry(), varsJSON)
	if c1 != i1 || !sameErrText(cerr1, ierr1) {
		emit(base+"@sharedroot", true, fmt.Sprintf("first unit diverged: compiled %q / %s, interp %q / %s",
			c1, errText(cerr1), i1, errText(ierr1)))
		return
	}

	i2, ierr2 := render(ienv, peer.Entry(), peerVars)
	c2, cerr2 := render(cenv, peer.Entry(), peerVars)
	if c2 != i2 || !sameErrText(cerr2, ierr2) {
		emit(base+"@sharedroot", true, fmt.Sprintf("second unit diverged (cross-unit key collision): compiled %q / %s, interp %q / %s",
			c2, errText(cerr2), i2, errText(ierr2)))
		return
	}
	emit(base+"@sharedroot", false, "ok")
}
` + jsonDecoderSupport

// jsonDecoderSupport reimplements internal/jsonval's order-preserving decode
// for the scratch process: objects keep member order, integer-exact numbers
// become Int, and keys route through the canonical key model.
const jsonDecoderSupport = `
func decodeVars(data string) (map[string]runtime.Value, error) {
	if data == "" {
		return map[string]runtime.Value{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(data)))
	dec.UseNumber()
	first, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := first.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("data root must be a JSON object")
	}
	obj, err := decodeObject(dec)
	if err != nil {
		return nil, err
	}
	out := make(map[string]runtime.Value, obj.Arr.Len())
	for _, p := range obj.Arr.Pairs() {
		name, err := runtime.ToText(p.Key)
		if err != nil {
			return nil, err
		}
		out[name] = p.Val
	}
	return out, nil
}

func decodeValue(dec *json.Decoder) (runtime.Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return runtime.Null(), err
	}
	return decodeFrom(dec, tok)
}

func decodeFrom(dec *json.Decoder, tok json.Token) (runtime.Value, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return decodeObject(dec)
		case '[':
			return decodeArray(dec)
		default:
			return runtime.Null(), fmt.Errorf("unexpected %q", t)
		}
	case nil:
		return runtime.Null(), nil
	case bool:
		return runtime.Bool(t), nil
	case string:
		return runtime.Str(t), nil
	case json.Number:
		if i, err := strconv.ParseInt(t.String(), 10, 64); err == nil {
			return runtime.Int(i), nil
		}
		f, err := t.Float64()
		if err != nil {
			return runtime.Null(), err
		}
		return runtime.Float(f), nil
	default:
		return runtime.Null(), fmt.Errorf("unsupported token %T", tok)
	}
}

func decodeObject(dec *json.Decoder) (runtime.Value, error) {
	arr := runtime.NewArray()
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return runtime.Null(), err
		}
		key, ok := keyTok.(string)
		if !ok {
			return runtime.Null(), fmt.Errorf("object key is not a string")
		}
		val, err := decodeValue(dec)
		if err != nil {
			return runtime.Null(), err
		}
		arr.SetKey(runtime.Str(key), val)
	}
	if _, err := dec.Token(); err != nil {
		return runtime.Null(), err
	}
	return runtime.Arr(arr), nil
}

func decodeArray(dec *json.Decoder) (runtime.Value, error) {
	arr := runtime.NewArray()
	var i int64
	for dec.More() {
		val, err := decodeValue(dec)
		if err != nil {
			return runtime.Null(), err
		}
		arr.SetInt(i, val)
		i++
	}
	if _, err := dec.Token(); err != nil {
		return runtime.Null(), err
	}
	return runtime.Arr(arr), nil
}
`
