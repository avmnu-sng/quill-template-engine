package ext

import (
	"fmt"
	"regexp"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// spacelessRe matches whitespace runs that sit entirely between a closing ">"
// and an opening "<", used by the spaceless filter to collapse inter-tag space.
var spacelessRe = regexp.MustCompile(`>\s+<`)

// tagRe matches a single HTML/XML tag (opening, closing, or self-closing),
// used by striptags to find candidate tags.
var tagRe = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)

// tagNameRe extracts the lower-cased tag name from a tag (or from one entry of
// an allowed-tags string like "<a><b>").
var tagNameRe = regexp.MustCompile(`</?\s*([a-zA-Z0-9]+)`)

// goArg lowers a Quill Value to the Go value the fmt verbs expect. Numbers and
// bools pass as their Go kinds so %d/%f/%t work; everything else renders through
// ToText so %s/%q/%v see the canonical Quill text spelling rather than the Go
// struct dump (spec 03 Section 2.6, the Go-fmt-dialect format filter).
func goArg(v runtime.Value) interface{} {
	switch v.Kind {
	case runtime.KInt:
		return v.I
	case runtime.KFloat:
		return v.F
	case runtime.KBool:
		return v.B
	default:
		s, err := runtime.ToText(v)
		if err != nil {
			return fmt.Sprintf("<%s>", v.Kind)
		}
		return s
	}
}

// sprintfGo applies Go fmt verbs to args. It is a thin wrapper so the format
// filter's Go fmt semantics have a single, named home.
func sprintfGo(format string, args []interface{}) string {
	return fmt.Sprintf(format, args...)
}
