package ext

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// EscapeHTML escapes the five HTML text metacharacters, with the single quote as
// the numeric entity &#39; (spec 03 Section 5.5, the html strategy).
//
// The order of the replacer pairs is irrelevant because strings.NewReplacer is
// single-pass and non-overlapping: an emitted "&amp;" is never re-scanned, so
// "&" -> "&amp;" does not cascade into the "&" of "&lt;".
func EscapeHTML(s string) string {
	return htmlEscaper.Replace(s)
}

var htmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&#39;",
)

// Escape applies the named escaping strategy (spec 03 Section 5.5). The six
// strategies retained from Twig for markup-emitting templates are html, js, css,
// html_attr, html_attr_relaxed, and url. An unknown strategy is a caller error.
//
// The strategies split into two charset classes (spec 04 Section 8.2). html and
// url are BYTE-oriented and accept arbitrary bytes losslessly. js, css,
// html_attr, and html_attr_relaxed are CODE-POINT-oriented: they decode the
// input as UTF-8 and, on an invalid byte, return a clear error naming the
// strategy and the byte offset -- they do NOT silently emit a replacement
// character, because a silent substitution in emitted code is a wrong byte.
func Escape(strategy, s string) (string, error) {
	switch strategy {
	case "html":
		return EscapeHTML(s), nil
	case "js":
		return escapeJS(s)
	case "css":
		return escapeCSS(s)
	case "html_attr":
		return escapeHTMLAttr(s, false)
	case "html_attr_relaxed":
		return escapeHTMLAttr(s, true)
	case "url":
		return escapeURL(s), nil
	default:
		return "", fmt.Errorf("unknown escape strategy %q", strategy)
	}
}

// invalidUTF8Error reports an invalid byte at the given offset for a code-point
// strategy, the spec 04 Section 8.2 guard against silently emitting a
// replacement character into generated code.
func invalidUTF8Error(strategy string, offset int) error {
	return fmt.Errorf("escape %s: invalid UTF-8 byte at offset %d", strategy, offset)
}

// escapeJS escapes a string for safe embedding inside a JavaScript single- or
// double-quoted string literal: every non-alphanumeric ASCII rune and every
// non-ASCII rune is emitted as a \xHH or \uHHHH escape, so the result cannot
// terminate the surrounding literal or inject markup (spec 03 Section 5.5, js).
// Invalid UTF-8 is an error, not a silent replacement char (spec 04 Section 8.2).
func escapeJS(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return "", invalidUTF8Error("js", i)
		}
		i += size
		if isAlnum(r) {
			b.WriteRune(r)
			continue
		}
		switch r {
		case ',', '.', '_':
			b.WriteRune(r)
		default:
			if r < 0x100 {
				fmt.Fprintf(&b, "\\x%02X", r)
			} else {
				fmt.Fprintf(&b, "\\u%04X", r)
			}
		}
	}
	return b.String(), nil
}

// escapeCSS escapes a string for a CSS identifier or quoted value: every
// non-alphanumeric rune becomes a "\HH " hex escape (CSS hex escapes are
// space-terminated), so the result cannot break out of a CSS context (spec 03
// Section 5.5, css). Invalid UTF-8 is an error, not a silent replacement char
// (spec 04 Section 8.2).
func escapeCSS(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return "", invalidUTF8Error("css", i)
		}
		i += size
		if isAlnum(r) {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, "\\%X ", r)
	}
	return b.String(), nil
}

// escapeHTMLAttr escapes a string for an unquoted-or-quoted HTML attribute
// value: alphanumerics and a small safe set pass through, everything else
// becomes a numeric entity, so the result is safe even in an unquoted attribute.
// The relaxed variant additionally allows the URL/path punctuation : @ [ ] so an
// attribute holding a URL or selector stays readable (spec 03 Section 5.5).
// Invalid UTF-8 is an error, not a silent replacement char (spec 04 Section 8.2).
func escapeHTMLAttr(s string, relaxed bool) (string, error) {
	strategy := "html_attr"
	if relaxed {
		strategy = "html_attr_relaxed"
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return "", invalidUTF8Error(strategy, i)
		}
		i += size
		if isAlnum(r) || r == ',' || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		if relaxed && (r == ':' || r == '@' || r == '[' || r == ']') {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, "&#x%X;", r)
	}
	return b.String(), nil
}

// escapeURL percent-encodes a string per RFC 3986 unreserved rules, with a space
// becoming %20 (NOT '+'); the unreserved set A-Za-z0-9-._~ passes through (spec
// 03 Section 5.5, url).
func escapeURL(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isURLUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func isURLUnreserved(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
		c == '-' || c == '.' || c == '_' || c == '~'
}
