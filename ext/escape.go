package ext

import "strings"

// EscapeHTML escapes the five HTML text metacharacters, with the single quote as
// the numeric entity &#39; (spec 03 Section 5.5, the html strategy). It is the
// one escape strategy implemented this milestone; the other five (js, css,
// html_attr, html_attr_relaxed, url) are deferred.
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
