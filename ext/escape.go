package ext

import (
	"fmt"
	"strings"
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
func Escape(strategy, s string) (string, error) {
	switch strategy {
	case "html":
		return EscapeHTML(s), nil
	case "js":
		return escapeJS(s), nil
	case "css":
		return escapeCSS(s), nil
	case "html_attr":
		return escapeHTMLAttr(s, false), nil
	case "html_attr_relaxed":
		return escapeHTMLAttr(s, true), nil
	case "url":
		return escapeURL(s), nil
	default:
		return "", fmt.Errorf("unknown escape strategy %q", strategy)
	}
}

// escapeJS escapes a string for safe embedding inside a JavaScript single- or
// double-quoted string literal: every non-alphanumeric ASCII rune and every
// non-ASCII rune is emitted as a \xHH or \uHHHH escape, so the result cannot
// terminate the surrounding literal or inject markup (spec 03 Section 5.5, js).
func escapeJS(s string) string {
	var b strings.Builder
	for _, r := range s {
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
	return b.String()
}

// escapeCSS escapes a string for a CSS identifier or quoted value: every
// non-alphanumeric rune becomes a "\HH " hex escape (CSS hex escapes are
// space-terminated), so the result cannot break out of a CSS context (spec 03
// Section 5.5, css).
func escapeCSS(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isAlnum(r) {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, "\\%X ", r)
	}
	return b.String()
}

// escapeHTMLAttr escapes a string for an unquoted-or-quoted HTML attribute
// value: alphanumerics and a small safe set pass through, everything else
// becomes a numeric entity, so the result is safe even in an unquoted attribute.
// The relaxed variant additionally allows the URL/path punctuation : @ [ ] so an
// attribute holding a URL or selector stays readable (spec 03 Section 5.5).
func escapeHTMLAttr(s string, relaxed bool) string {
	var b strings.Builder
	for _, r := range s {
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
	return b.String()
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
