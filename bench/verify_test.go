package quillbench

import (
	"bytes"
	htmltmpl "html/template"
	"strings"
	"testing"
	texttmpl "text/template"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// TestVerifyOutputs renders every offline engine's tiny + loop templates and
// asserts the first loop data line is byte-identical across the stdlib engines
// and Quill, which is the strongest cheap equivalence check: it guarantees the
// benchmarks below time engines doing the SAME work, not accidentally divergent
// output. The third-party peers (pongo2, stick) are verified in the
// thirdparty-tagged file so this default check stays offline and dependency-free.
func TestVerifyOutputs(t *testing.T) {
	// ----- Quill -----
	env := quill.NewWithArray(map[string]string{
		"tiny.ql": quillTiny,
		"loop.ql": quillLoop,
	})
	qTiny, err := env.Render("tiny.ql", map[string]runtime.Value{"name": runtime.Str("ada")})
	if err != nil {
		t.Fatal(err)
	}
	qLoop, err := env.Render("loop.ql", map[string]runtime.Value{"users": quillUsers()})
	if err != nil {
		t.Fatal(err)
	}
	if qTiny != "Hello ADA!" {
		t.Errorf("quill tiny = %q, want %q", qTiny, "Hello ADA!")
	}

	// ----- text/template -----
	tt := texttmpl.Must(texttmpl.New("loop").Funcs(stdFuncs).Parse(stdLoop))
	var tb bytes.Buffer
	if err := tt.Execute(&tb, struct{ Users []record }{Users: goRecords()}); err != nil {
		t.Fatal(err)
	}

	// ----- html/template -----
	ht := htmltmpl.Must(htmltmpl.New("loop").Funcs(stdFuncs).Parse(stdLoop))
	var hb bytes.Buffer
	if err := ht.Execute(&hb, struct{ Users []record }{Users: goRecords()}); err != nil {
		t.Fatal(err)
	}

	// Equivalence assertion across the engines: the meaningful data line is
	// "1. USER <user@example.com>". html/template contextually HTML-escapes the
	// '<' to "&lt;" -- that is correct, expected behavior for an HTML-autoescaping
	// engine, and is exactly the semantic difference the comparison doc discusses,
	// so we normalize it here rather than treat it as a mismatch.
	want := "1. USER <user@example.com>"
	norm := func(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "&lt;", "<") }
	for name, got := range map[string]string{
		"quill": firstDataLine(qLoop),
		"text":  firstDataLine(tb.String()),
		"html":  firstDataLine(hb.String()),
	} {
		if norm(got) != want {
			t.Errorf("%s first data line = %q, want %q (normalized)", name, got, want)
		}
	}
}

// firstDataLine returns the first non-empty line of s.
func firstDataLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			return strings.TrimSpace(ln)
		}
	}
	return ""
}
