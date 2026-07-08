package interp

import (
	"context"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/parse"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// renderStubErr renders an ad-hoc template and returns the render error (or nil),
// for the negative cases where the template parses but fails at render time.
func renderStubErr(t *testing.T, eng *stubEngine, body string, vars map[string]runtime.Value) (string, error) {
	t.Helper()
	mod, err := parse.ParseString("test", body)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return Render(context.Background(), eng, Prepare("test", mod), vars)
}

// TestEscapeRegionStrategies checks that an @escape region applies each of the
// six named strategies to its body, that off and its synonym raw disable
// escaping, and that the bytes match the escape()/e() filter exactly (spec 04
// Section 8). The
// payload reuses the conformance corpus string. A single interpolation on its own
// line emits the escaped value plus the line's trailing newline.
func TestEscapeRegionStrategies(t *testing.T) {
	eng := newStub(nil)
	payload := `a&b <x> 'q' "d" /p u:1@h[0]`
	vars := map[string]runtime.Value{"p": runtime.Str(payload)}

	cases := []struct {
		strategy string
		want     string
	}{
		{"off", payload},
		{"raw", payload},
		{"html", `a&amp;b &lt;x&gt; &#39;q&#39; &quot;d&quot; /p u:1@h[0]`},
		{"js", `a\x26b\x20\x3Cx\x3E\x20\x27q\x27\x20\x22d\x22\x20\x2Fp\x20u\x3A1\x40h\x5B0\x5D`},
		{"css", `a\26 b\20 \3C x\3E \20 \27 q\27 \20 \22 d\22 \20 \2F p\20 u\3A 1\40 h\5B 0\5D `},
		{"html_attr", `a&#x26;b&#x20;&#x3C;x&#x3E;&#x20;&#x27;q&#x27;&#x20;&#x22;d&#x22;&#x20;&#x2F;p&#x20;u&#x3A;1&#x40;h&#x5B;0&#x5D;`},
		{"html_attr_relaxed", `a&#x26;b&#x20;&#x3C;x&#x3E;&#x20;&#x27;q&#x27;&#x20;&#x22;d&#x22;&#x20;&#x2F;p&#x20;u:1@h[0]`},
		{"url", `a%26b%20%3Cx%3E%20%27q%27%20%22d%22%20%2Fp%20u%3A1%40h%5B0%5D`},
	}
	for _, c := range cases {
		t.Run(c.strategy, func(t *testing.T) {
			body := "@escape " + c.strategy + " {\n{{ p }}\n@}"
			got := renderStub(t, eng, body, vars)
			want := c.want + "\n"
			if got != want {
				t.Errorf("region %q\n got %q\nwant %q", c.strategy, got, want)
			}
		})
	}
}

// TestEscapeRegionStack checks that nested @escape regions compose via a stack:
// an inner strategy applies inside its body and the OUTER strategy is restored
// when the inner body ends (spec 04 Section 8). The module default (here off) is
// restored after the outermost region.
func TestEscapeRegionStack(t *testing.T) {
	eng := newStub(nil)
	vars := map[string]runtime.Value{"p": runtime.Str("<x>")}
	body := strings.Join([]string{
		"{{ p }}",        // module default: off
		"@escape html {", // push html
		"{{ p }}",        // html
		"@escape js {",   // push js
		"{{ p }}",        // js
		"@}",             // pop -> html
		"{{ p }}",        // html again
		"@}",             // pop -> off
		"{{ p }}",        // off again
	}, "\n")
	got := renderStub(t, eng, body, vars)
	want := strings.Join([]string{
		"<x>",
		"&lt;x&gt;",
		`\x3Cx\x3E`,
		"&lt;x&gt;",
		"<x>",
	}, "\n")
	if got != want {
		t.Errorf("stack composition\n got %q\nwant %q", got, want)
	}
}

// TestEscapeRegionDefaultHTML checks that with the module default set to html, an
// @escape off region disables escaping for its body and the html default is
// restored after it, while a nested @escape html re-enables escaping within the
// off region (the stack composes with the module default at its base).
func TestEscapeRegionDefaultHTML(t *testing.T) {
	eng := newStub(nil)
	eng.autoht = true
	vars := map[string]runtime.Value{"p": runtime.Str("<x>")}
	body := strings.Join([]string{
		"{{ p }}",        // module default html
		"@escape off {",  // push off
		"{{ p }}",        // off
		"@escape html {", // push html within off
		"{{ p }}",        // html
		"@}",             // pop -> off
		"{{ p }}",        // off
		"@}",             // pop -> html (module default)
		"{{ p }}",        // html
	}, "\n")
	got := renderStub(t, eng, body, vars)
	want := strings.Join([]string{
		"&lt;x&gt;",
		"<x>",
		"&lt;x&gt;",
		"<x>",
		"&lt;x&gt;",
	}, "\n")
	if got != want {
		t.Errorf("default-html region\n got %q\nwant %q", got, want)
	}
}

// TestEscapeRegionRawSite checks that the raw filter cancels escaping at a single
// site inside an on-region without disturbing the region strategy for the rest of
// the body (spec 04 Section 8.2).
func TestEscapeRegionRawSite(t *testing.T) {
	eng := newStub(nil)
	vars := map[string]runtime.Value{"p": runtime.Str("<x>")}
	body := "@escape html {\n{{ p }}|{{ p | raw }}|{{ p }}\n@}"
	got := renderStub(t, eng, body, vars)
	want := "&lt;x&gt;|<x>|&lt;x&gt;\n"
	if got != want {
		t.Errorf("raw site\n got %q\nwant %q", got, want)
	}
}

// TestEscapeRegionCaptureIsSafe checks that a capture taken under an active
// strategy yields a Safe value that emits verbatim (not double-escaped) when
// later interpolated under that same strategy (spec 04 Section 8.2).
func TestEscapeRegionCaptureIsSafe(t *testing.T) {
	eng := newStub(nil)
	vars := map[string]runtime.Value{"p": runtime.Str("<x>")}
	body := strings.Join([]string{
		"@escape html {",
		"@set cap = capture {",
		"{{ p }}",
		"@}",
		"{{ cap }}", // Safe: already-escaped once, must not double-escape
		"@}",
	}, "\n")
	got := renderStub(t, eng, body, vars)
	// The capture renders "<x>" under html to "&lt;x&gt;\n" and binds it as Safe;
	// re-emitting that Safe value under html emits it verbatim (no re-escaping to
	// "&amp;lt;x&amp;gt;").
	want := "&lt;x&gt;\n" + "\n"
	if got != want {
		t.Errorf("capture-as-safe\n got %q\nwant %q", got, want)
	}
}

// TestEscapeRegionApplyNotDoubleEscaped checks that @apply, like capture, treats
// its filtered body as already-safe under an active strategy: the body is escaped
// once during the capture render, so the final emit must NOT escape it a second
// time (spec 04 Section 8.2). The byte-for-byte oracle is capture of the SAME
// input under the same strategy -- apply through a metacharacter-preserving
// filter must match it exactly. A regression (the old plain-Str path) re-escapes,
// turning e.g. "&lt;" into "&amp;lt;".
func TestEscapeRegionApplyNotDoubleEscaped(t *testing.T) {
	eng := newStub(nil)
	// trim is a no-op here (no surrounding whitespace) so the apply output equals
	// the once-escaped capture byte-for-byte; it still forces the filter-chain path.
	vars := map[string]runtime.Value{"p": runtime.Str("<x>&'\"/")}
	for _, strategy := range []string{"html", "js", "css", "html_attr", "html_attr_relaxed", "url"} {
		t.Run(strategy, func(t *testing.T) {
			capBody := strings.Join([]string{
				"@escape " + strategy + " {",
				"@set cap = capture {",
				"{{ p }}",
				"@}",
				"{{ cap }}",
				"@}",
			}, "\n")
			// trim both sides so the comparison isolates the ESCAPED CONTENT and is
			// not confused by the differing newline shapes of the two block forms
			// (the apply filter itself trims its body's surrounding whitespace).
			want := strings.TrimSpace(renderStub(t, eng, capBody, vars))

			applyBody := strings.Join([]string{
				"@escape " + strategy + " {",
				"@apply | trim {",
				"{{ p }}",
				"@}",
				"@}",
			}, "\n")
			got := strings.TrimSpace(renderStub(t, eng, applyBody, vars))
			if got != want {
				t.Errorf("apply under %q double-escapes\n got %q\nwant %q (capture oracle)", strategy, got, want)
			}
		})
	}
}

// TestEscapeRegionCodePointInvalidUTF8 checks the spec 04 Section 8.2 guard end
// to end: emitting an invalid-UTF-8 Str under a CODE-POINT strategy (js, css,
// html_attr, html_attr_relaxed) is a clear render error naming the strategy and
// byte offset, surfaced through emit -- NOT a silently substituted replacement
// char. The BYTE-oriented strategies (html, url) accept the same bytes
// losslessly. The invalid byte is injected as a host var because JSON data
// cannot carry one (encoding/json normalizes it to U+FFFD).
func TestEscapeRegionCodePointInvalidUTF8(t *testing.T) {
	eng := newStub(nil)
	vars := map[string]runtime.Value{"p": runtime.Str("a\xffb")}

	for _, strategy := range []string{"js", "css", "html_attr", "html_attr_relaxed"} {
		t.Run(strategy+"-errors", func(t *testing.T) {
			body := "@escape " + strategy + " {\n{{ p }}\n@}"
			_, err := renderStubErr(t, eng, body, vars)
			if err == nil {
				t.Fatalf("strategy %q must error on invalid UTF-8", strategy)
			}
			if !strings.Contains(err.Error(), strategy) {
				t.Errorf("error should name the strategy %q, got: %v", strategy, err)
			}
			if !strings.Contains(err.Error(), "offset 1") {
				t.Errorf("error should name the byte offset (1), got: %v", err)
			}
		})
	}

	for _, strategy := range []string{"html", "url"} {
		t.Run(strategy+"-lossless", func(t *testing.T) {
			body := "@escape " + strategy + " {\n{{ p }}\n@}"
			if _, err := renderStubErr(t, eng, body, vars); err != nil {
				t.Errorf("byte-oriented strategy %q must accept invalid bytes, got: %v", strategy, err)
			}
		})
	}
}

// TestEscapeRegionUnknownStrategy checks that an unrecognized strategy word is a
// clear render error naming the valid set, not a silent passthrough.
func TestEscapeRegionUnknownStrategy(t *testing.T) {
	eng := newStub(nil)
	_, err := renderStubErr(t, eng, "@escape bogus {\n{{ p }}\n@}", map[string]runtime.Value{"p": runtime.Str("x")})
	if err == nil {
		t.Fatal("expected an error for an unknown escape strategy")
	}
	if !strings.Contains(err.Error(), "unknown escape strategy") {
		t.Errorf("error should name the unknown strategy, got: %v", err)
	}
}
