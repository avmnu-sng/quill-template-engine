// Package source carries a Quill template's origin: a name and its raw bytes,
// with line endings normalized so that error positions and rendered output do
// not depend on the host platform's newline convention.
//
// A Source is the unit error messages point into. The runtime, lexer, parser,
// and checker all attach a *Source plus a 1-based line number to every
// diagnostic so a failure names the exact template and line that produced it.
package source

import "strings"

// Source is an immutable template origin: a human-facing name and the template
// text after CRLF/CR normalization to LF. It is safe to share by pointer.
type Source struct {
	name string
	code string
}

// New builds a Source from a name and raw template bytes. Both "\r\n" and a
// lone "\r" are normalized to "\n" so line counting and byte-exact output are
// independent of the authoring platform.
func New(name, code string) *Source {
	return &Source{name: name, code: normalizeNewlines(code)}
}

// Name returns the template's name (typically a path or logical identifier).
func (s *Source) Name() string {
	if s == nil {
		return ""
	}
	return s.name
}

// Code returns the normalized template text.
func (s *Source) Code() string {
	if s == nil {
		return ""
	}
	return s.code
}

// Line returns the content of the given 1-based line, or "" when the
// line is out of range or the Source is nil. It is a convenience for building
// error messages that quote the offending line.
func (s *Source) Line(n int) string {
	if s == nil || n < 1 {
		return ""
	}
	lines := strings.Split(s.code, "\n")
	if n > len(lines) {
		return ""
	}
	return lines[n-1]
}

// normalizeNewlines folds "\r\n" and lone "\r" to "\n" in a single pass without
// allocating when the input is already LF-only.
func normalizeNewlines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\r' {
			// Treat a following '\n' as part of the same break.
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			b.WriteByte('\n')
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
