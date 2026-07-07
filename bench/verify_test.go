package quillbench

import (
	"bytes"
	htmltmpl "html/template"
	"strings"
	"testing"
	texttmpl "text/template"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// loopWant is the full expected Loop output for loopN rows: every engine that
// should agree must render exactly these bytes (after the documented html escape
// normalization). It is built from the same row shape all engines consume, so the
// benchmarks below provably time byte-identical work rather than accidentally
// divergent output.
func loopWant(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(itoa(i + 1))
		sb.WriteString(". USER <user@example.com>\n")
	}
	return sb.String()
}

// itoa is a tiny base-10 formatter so loopWant needs no strconv import churn.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// normHTML undoes html/template's contextual escaping of the '<' in the email
// angle brackets. That escape is correct, expected behavior for an
// HTML-autoescaping engine and is exactly the semantic difference the comparison
// doc discusses, so it is normalized rather than treated as a mismatch. No other
// escaping fires because the inputs are otherwise escape-free.
func normHTML(s string) string { return strings.ReplaceAll(s, "&lt;", "<") }

// TestVerifyOutputs renders every offline engine's loop template and asserts the
// ENTIRE rendered output is byte-identical across Quill (interpreter), Quill
// (real compile backend), and text/template, with html/template agreeing after
// the documented &lt; normalization. Timing engines that produce identical bytes
// is the whole point: the benchmarks must compare equivalent work.
func TestVerifyOutputs(t *testing.T) {
	want := loopWant(loopN)

	// ----- Quill tiny (sanity on the filter path) -----
	env := quill.NewWithArray(map[string]string{
		"tiny.ql": quillTiny,
		"loop.ql": quillLoop,
	})
	qTiny, err := env.Render("tiny.ql", map[string]runtime.Value{"name": runtime.Str("ada")})
	if err != nil {
		t.Fatal(err)
	}
	if qTiny != "Hello ADA!" {
		t.Errorf("quill tiny = %q, want %q", qTiny, "Hello ADA!")
	}

	// ----- Quill interpreter -----
	qLoop, err := env.Render("loop.ql", map[string]runtime.Value{"users": quillUsers()})
	if err != nil {
		t.Fatal(err)
	}

	// ----- Quill real compile backend (the committed RenderLoop) -----
	var cb strings.Builder
	if err := RenderLoop(&cb, ext.Core(), map[string]runtime.Value{"users": quillUsers()}, nil); err != nil {
		t.Fatal(err)
	}

	// ----- text/template -----
	tt := texttmpl.Must(texttmpl.New("loop").Funcs(stdFuncs).Parse(stdLoop))
	var tb bytes.Buffer
	if err := tt.Execute(&tb, struct{ Users []record }{Users: goRecords()}); err != nil {
		t.Fatal(err)
	}

	// ----- html/template (normalized) -----
	ht := htmltmpl.Must(htmltmpl.New("loop").Funcs(stdFuncs).Parse(stdLoop))
	var hb bytes.Buffer
	if err := ht.Execute(&hb, struct{ Users []record }{Users: goRecords()}); err != nil {
		t.Fatal(err)
	}

	for name, got := range map[string]string{
		"quill-interp":   qLoop,
		"quill-compiled": cb.String(),
		"text":           tb.String(),
		"html":           normHTML(hb.String()),
	} {
		if got != want {
			t.Errorf("%s full loop output mismatch\n got=%q\nwant=%q", name, got, want)
		}
	}
}
