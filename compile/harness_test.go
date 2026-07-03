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

	"github.com/avmnu-sng/quill-template-engine/compile"
	"github.com/avmnu-sng/quill-template-engine/parse"
	"github.com/avmnu-sng/quill-template-engine/source"
)

// compiledCase is one template to compile into the scratch module and render
// in a child process.
type compiledCase struct {
	name     string // unique id; also the fixture package directory
	template string // template body (the main template)
	varsJSON string // data as a JSON object; "" means no vars
	opts     compile.Options
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
}

// caseResult is one rendered case's outcome from the scratch process.
type caseResult struct {
	out     string
	errText string
	failed  bool
}

// repoRoot locates the engine repository root (the parent of compile/).
func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod: %v", abs, err)
	}
	return abs
}

// compileCase parses and compiles one case, failing the test on a parse error
// and returning the compile result or error.
func compileCase(t *testing.T, cs compiledCase) (*compile.Result, error) {
	t.Helper()
	mod, err := parse.Parse(source.New(cs.name+".ql", cs.template))
	if err != nil {
		t.Fatalf("%s: parse: %v", cs.name, err)
	}
	opts := cs.opts
	if opts.PackageName == "" {
		opts.PackageName = pkgName(cs.name)
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
// comes from building the ExtensionSet through quill.NewWithArray in the
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
	mainB.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/compiled\"\n")
	mainB.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/ext\"\n")
	mainB.WriteString("\t\"github.com/avmnu-sng/quill-template-engine/runtime\"\n")
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
	mainB.WriteString("\texts := quill.NewWithArray(map[string]string{}).Extensions()\n")
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
const mainSupport = `func runCase(name string, exts *ext.ExtensionSet, varsJSON string, fn func(io.Writer, *ext.ExtensionSet, map[string]runtime.Value) error) {
	vars, err := decodeVars(varsJSON)
	if err != nil {
		emit(name, true, "vars decode: "+err.Error())
		return
	}
	var b strings.Builder
	if err := fn(&b, exts, vars); err != nil {
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
	env := quill.NewWithArray(tmpls, append(envOpts(m.Fingerprint), quill.WithCompiled(m))...)
	out, err := env.Render(m.Entry, vars)
	if err != nil {
		emit(base+"@env", true, err.Error())
	} else {
		emit(base+"@env", false, out)
	}

	// The tracer shares the manifest's metadata but renders a marker, so a
	// marker result proves the dispatch gate passes for this configuration;
	// without it the env render above could silently compare the interpreter
	// against itself.
	tenv := quill.NewWithArray(tmpls, append(envOpts(m.Fingerprint), quill.WithCompiled(tracerManifest(m)))...)
	tvars, err := decodeVars(varsJSON)
	if err != nil {
		emit(base+"@tracer", true, err.Error())
		return
	}
	tout, terr := tenv.Render(m.Entry, tvars)
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
	for n, s := range m.Sources {
		tmpls[n] = s
	}
	return tmpls
}

// envOpts builds the Environment options a fingerprint describes, so every
// probe Environment is configured from the manifest itself rather than from a
// re-derived subset of the compile options.
func envOpts(fp compiled.Fingerprint) []quill.Option {
	opts := []quill.Option{
		quill.WithAutoescapeHTML(fp.AutoescapeHTML),
		quill.WithStrictVariables(!fp.LenientVariables),
		quill.WithTabWidth(fp.TabWidth),
	}
	if fp.RandomSeedSet {
		opts = append(opts, quill.WithRandomSeed(fp.RandomSeed))
	}
	return opts
}

// tracerManifest clones a manifest's dispatch metadata around a marker render:
// a marker result proves the gate served the unit, anything else proves it
// fell back.
func tracerManifest(m *compiled.Manifest) *compiled.Manifest {
	return &compiled.Manifest{
		Entry: m.Entry, Sources: m.Sources, Fingerprint: m.Fingerprint, UsesLog: m.UsesLog,
		Render: func(w io.Writer, _ *ext.ExtensionSet, _ map[string]runtime.Value) error {
			_, err := io.WriteString(w, "\x02TRACER\x02")
			return err
		},
	}
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
	fp := m.Fingerprint
	var fps []compiled.Fingerprint
	for _, auto := range []bool{false, true} {
		for _, lenient := range []bool{false, true} {
			cfp := fp
			cfp.AutoescapeHTML = auto
			cfp.LenientVariables = lenient
			fps = append(fps, cfp)
		}
	}
	tabFlip := fp
	tabFlip.TabWidth = fp.TabWidth + 2
	seedFlip := fp
	seedFlip.RandomSeed = fp.RandomSeed + 1
	seedFlip.RandomSeedSet = true
	fps = append(fps, tabFlip, seedFlip)

	for _, cfp := range fps {
		label := fmt.Sprintf("auto=%v lenient=%v tab=%d seeded=%v",
			cfp.AutoescapeHTML, cfp.LenientVariables, cfp.TabWidth, cfp.RandomSeedSet)

		refVars, err := decodeVars(varsJSON)
		if err != nil {
			emit(base+"@matrix", true, label+": vars decode: "+err.Error())
			return
		}
		ref := quill.NewWithArray(tmpls, envOpts(cfp)...)
		refOut, refErr := ref.Render(m.Entry, refVars)

		mixVars, err := decodeVars(varsJSON)
		if err != nil {
			emit(base+"@matrix", true, label+": vars decode: "+err.Error())
			return
		}
		mixed := quill.NewWithArray(tmpls, append(envOpts(cfp), quill.WithCompiled(m))...)
		mixOut, mixErr := mixed.Render(m.Entry, mixVars)
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
		tenv := quill.NewWithArray(tmpls, append(envOpts(cfp), quill.WithCompiled(tracerManifest(m)))...)
		tout, terr := tenv.Render(m.Entry, trcVars)
		served := terr == nil && tout == "\x02TRACER\x02"
		if served != (cfp == fp) {
			emit(base+"@matrix", true, fmt.Sprintf("%s: gate served=%v, want %v", label, served, cfp == fp))
			return
		}
	}
	emit(base+"@matrix", false, "ok")
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
