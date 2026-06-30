package ext

import (
	"strings"
	"testing"
)

// TestEscapeCodePointInvalidUTF8 verifies the spec 04 Section 8.2 guard: the
// code-point strategies (js, css, html_attr, html_attr_relaxed) decode the input
// as UTF-8 and, on an invalid byte, return a clear error naming the strategy and
// the byte offset -- they do NOT silently substitute a replacement character,
// because a silent substitution in emitted code is a wrong byte.
func TestEscapeCodePointInvalidUTF8(t *testing.T) {
	// Valid prefix "a" then an invalid lead byte 0xff at offset 1.
	in := "a\xff\xfeb"
	for _, strategy := range []string{"js", "css", "html_attr", "html_attr_relaxed"} {
		t.Run(strategy, func(t *testing.T) {
			out, err := Escape(strategy, in)
			if err == nil {
				t.Fatalf("strategy %q must error on invalid UTF-8, got %q", strategy, out)
			}
			if out != "" {
				t.Errorf("on error the output must be empty, got %q", out)
			}
			if !strings.Contains(err.Error(), strategy) {
				t.Errorf("error should name the strategy %q, got: %v", strategy, err)
			}
			if !strings.Contains(err.Error(), "offset 1") {
				t.Errorf("error should name the byte offset (1), got: %v", err)
			}
			// The forbidden behavior: a silently substituted replacement char.
			if strings.Contains(out, "FFFD") {
				t.Errorf("strategy %q silently emitted a replacement char: %q", strategy, out)
			}
		})
	}
}

// TestEscapeByteOrientedAcceptInvalidUTF8 verifies the complementary spec 04
// Section 8.2 rule: html and url are BYTE-oriented and accept arbitrary bytes
// losslessly -- they never error on invalid UTF-8. html passes the raw bytes
// through (only the five metacharacters change) and url percent-encodes each
// byte.
func TestEscapeByteOrientedAcceptInvalidUTF8(t *testing.T) {
	in := "a\xff\xfeb"
	if out, err := Escape("html", in); err != nil || out != in {
		t.Errorf("html must pass invalid bytes through losslessly: out=%q err=%v", out, err)
	}
	out, err := Escape("url", in)
	if err != nil {
		t.Fatalf("url must not error on invalid bytes: %v", err)
	}
	if out != "a%FF%FEb" {
		t.Errorf("url byte-encode = %q, want %q", out, "a%FF%FEb")
	}
}

// TestEscapeCodePointValidUTF8 confirms the error path does not regress valid
// multi-byte input: a non-ASCII rune still escapes to its code-point form.
func TestEscapeCodePointValidUTF8(t *testing.T) {
	out, err := Escape("js", "\u00e9") // e-acute, U+00E9 (ASCII-source escape)
	if err != nil {
		t.Fatalf("valid UTF-8 must not error: %v", err)
	}
	if out != "\\xE9" {
		t.Errorf("js escape of U+00E9 = %q, want %q", out, "\\xE9")
	}
}
