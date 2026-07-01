package ext

import (
	"strings"
	"unicode"

	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// This file holds the text-shaping filters (spec 03 Section 2.1): wrap
// (word-wrap honoring word boundaries), truncate (a length cap with an omission
// marker, optionally on a word boundary), center (pad a string to a width), and
// wordcount (count the whitespace-separated words). They are charset-aware: width
// and length are measured in runes, not bytes, so a multi-byte string wraps and
// truncates by visible characters.

// registerShapingFilters installs the text-shaping filters onto s. It is called
// from registerStdlib alongside the other filter families.
func registerShapingFilters(s *ExtensionSet) {
	s.AddFilter(&Filter{Name: "wrap", Fn: filterWrap})
	s.AddFilter(&Filter{Name: "truncate", Fn: filterTruncate})
	s.AddFilter(&Filter{Name: "center", Fn: filterCenter})
	s.AddFilter(&Filter{Name: "wordcount", Fn: filterWordcount})
}

// filterWrap word-wraps a string to width columns, breaking only at spaces so a
// word is never split mid-token (spec 03 Section 2.1): s | wrap(width, break:"\n").
// The break argument (default "\n") joins the produced lines. Existing newlines
// in the input start fresh paragraphs, each wrapped independently, so a
// pre-formatted block keeps its own line breaks. A single word longer than the
// width is emitted whole on its own line rather than being cut. Width is counted
// in runes, so multi-byte text wraps by visible characters.
func filterWrap(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	width := int(toInt(arg(args, 1)))
	if width < 1 {
		return runtime.Null(), errors.New(errors.KindRuntime, "wrap width must be >= 1")
	}
	brk := "\n"
	if len(args) > 2 && !args[2].IsNull() {
		brk, err = wantString(args[2])
		if err != nil {
			return runtime.Null(), err
		}
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		out = append(out, wrapParagraph(para, width)...)
	}
	return runtime.Str(strings.Join(out, brk)), nil
}

// wrapParagraph greedily packs the words of one paragraph into lines no wider
// than width runes, breaking only at spaces. An all-blank paragraph yields a
// single empty line so a blank line between paragraphs is preserved.
func wrapParagraph(para string, width int) []string {
	words := strings.Fields(para)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	var line strings.Builder
	lineLen := 0
	for _, w := range words {
		wl := len([]rune(w))
		if lineLen == 0 {
			line.WriteString(w)
			lineLen = wl
			continue
		}
		if lineLen+1+wl > width {
			lines = append(lines, line.String())
			line.Reset()
			line.WriteString(w)
			lineLen = wl
			continue
		}
		line.WriteByte(' ')
		line.WriteString(w)
		lineLen += 1 + wl
	}
	lines = append(lines, line.String())
	return lines
}

// filterTruncate caps a string at length runes, appending an omission marker when
// it was shortened (spec 03 Section 2.1): s | truncate(length, omission:"...",
// preserve:false). The returned string is at most length runes INCLUDING the
// omission marker, so it never overruns the requested width. When preserve is
// true the cut is pulled back to the last word boundary within the budget so a
// word is not split. A string already within the length is returned unchanged.
// Distinct from slice, which is a pure window with no marker. Length is counted
// in runes.
func filterTruncate(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	length := int(toInt(arg(args, 1)))
	if length < 0 {
		length = 0
	}
	omission := "..."
	if len(args) > 2 && !args[2].IsNull() {
		omission, err = wantString(args[2])
		if err != nil {
			return runtime.Null(), err
		}
	}
	preserve := len(args) > 3 && runtime.Truthy(args[3])

	r := []rune(s)
	if len(r) <= length {
		return runtime.Str(s), nil
	}
	om := []rune(omission)
	// The marker must fit inside the budget; when the budget is smaller than the
	// marker, the marker itself is truncated to the budget so the result never
	// exceeds length runes.
	if len(om) >= length {
		return runtime.Str(string(om[:length])), nil
	}
	cut := length - len(om)
	head := r[:cut]
	if preserve {
		if i := lastSpace(head); i >= 0 {
			head = head[:i]
		}
	}
	return runtime.Str(strings.TrimRight(string(head), " ") + omission), nil
}

// lastSpace returns the index of the last space rune in r, or -1 when there is
// none. It is the word-boundary pull-back point for truncate(preserve:true).
func lastSpace(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == ' ' {
			return i
		}
	}
	return -1
}

// filterCenter pads a string on both sides with a fill character so it is width
// runes wide, centered (spec 03 Section 2.1): s | center(width, fill:" "). An odd
// amount of padding puts the extra unit on the right, matching the common
// str.center convention. A string already at or over the width is returned
// unchanged. Width and the padding are counted in runes.
func filterCenter(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	width := int(toInt(arg(args, 1)))
	fill := " "
	if len(args) > 2 && !args[2].IsNull() {
		fill, err = wantString(args[2])
		if err != nil {
			return runtime.Null(), err
		}
	}
	if fill == "" {
		fill = " "
	}
	sw := len([]rune(s))
	if width <= sw {
		return runtime.Str(s), nil
	}
	total := width - sw
	left := total / 2
	right := total - left
	return runtime.Str(padRunes(fill, left) + s + padRunes(fill, right)), nil
}

// padRunes builds a padding string exactly n runes wide by repeating fill and
// trimming to n runes, so a multi-rune fill never overshoots the requested pad.
func padRunes(fill string, n int) string {
	if n <= 0 {
		return ""
	}
	fr := []rune(fill)
	out := make([]rune, 0, n)
	for len(out) < n {
		out = append(out, fr...)
	}
	return string(out[:n])
}

// filterWordcount counts the words in a string: maximal runs of non-space runes
// separated by whitespace (spec 03 Section 2.1). Leading, trailing, and repeated
// whitespace do not inflate the count; an empty or all-whitespace string counts
// zero words.
func filterWordcount(args []runtime.Value) (runtime.Value, error) {
	s, err := wantString(arg(args, 0))
	if err != nil {
		return runtime.Null(), err
	}
	count := 0
	inWord := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			inWord = false
			continue
		}
		if !inWord {
			count++
			inWord = true
		}
	}
	return runtime.Int(int64(count)), nil
}
