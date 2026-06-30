package ext

import (
	"strconv"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// encodeJSON serializes a Quill Value to b following Go encoding/json output
// rules but with two deliberate divergences for source emission (spec 03 Section
// 2.6): keys are emitted in the *Array's insertion order (deterministic, not
// sorted), and HTML metacharacters < > & and the slash / are NOT escaped (Go's
// json.Marshal escapes < > & by default; source emission must not). pretty turns
// on indentation by the given unit at the given nesting prefix.
//
// A list-shaped *Array encodes as a JSON array; any other *Array encodes as a
// JSON object with string keys. An Object with a Stringify hook encodes as its
// string; an Object without one is a render error, mirroring ToText.
func encodeJSON(b *strings.Builder, v runtime.Value, pretty bool, indent, prefix string) error {
	switch v.Kind {
	case runtime.KNull:
		b.WriteString("null")
	case runtime.KBool:
		if v.B {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case runtime.KInt:
		b.WriteString(strconv.FormatInt(v.I, 10))
	case runtime.KFloat:
		b.WriteString(strconv.FormatFloat(v.F, 'g', -1, 64))
	case runtime.KStr:
		b.WriteString(encodeJSONString(v.S))
	case runtime.KSafe:
		b.WriteString(encodeJSONString(v.S))
	case runtime.KArray:
		return encodeJSONArray(b, v.Arr, pretty, indent, prefix)
	case runtime.KObject:
		s, err := runtime.ToText(v)
		if err != nil {
			return err
		}
		b.WriteString(encodeJSONString(s))
	default:
		return errors.New(errors.KindRender, "cannot serialize value of unknown kind to json")
	}
	return nil
}

func encodeJSONArray(b *strings.Builder, a *runtime.Array, pretty bool, indent, prefix string) error {
	if a == nil || a.Len() == 0 {
		if a != nil && a.Len() == 0 && !a.IsList() {
			// An empty *Array is list-shaped, so it serializes as []; this branch is
			// unreachable but documents the intent.
		}
		b.WriteString("[]")
		return nil
	}
	inner := prefix + indent
	list := a.IsList()
	if list {
		b.WriteByte('[')
		for i, p := range a.Pairs() {
			if i > 0 {
				b.WriteByte(',')
			}
			writeNewlineIndent(b, pretty, inner)
			if err := encodeJSON(b, p.Val, pretty, indent, inner); err != nil {
				return err
			}
		}
		writeNewlineIndent(b, pretty, prefix)
		b.WriteByte(']')
		return nil
	}
	b.WriteByte('{')
	for i, p := range a.Pairs() {
		if i > 0 {
			b.WriteByte(',')
		}
		writeNewlineIndent(b, pretty, inner)
		key, err := runtime.ToText(p.Key)
		if err != nil {
			return err
		}
		b.WriteString(encodeJSONString(key))
		b.WriteByte(':')
		if pretty {
			b.WriteByte(' ')
		}
		if err := encodeJSON(b, p.Val, pretty, indent, inner); err != nil {
			return err
		}
	}
	writeNewlineIndent(b, pretty, prefix)
	b.WriteByte('}')
	return nil
}

func writeNewlineIndent(b *strings.Builder, pretty bool, prefix string) {
	if pretty {
		b.WriteByte('\n')
		b.WriteString(prefix)
	}
}

// encodeJSONString quotes a string as a JSON string literal WITHOUT Go's default
// HTML escaping of < > &: only the JSON-mandatory escapes (quote, backslash, the
// control characters) are applied, so emitted source keeps its literal angle
// brackets and ampersands (spec 03 Section 2.6).
func encodeJSONString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				b.WriteString(`\u`)
				const hex = "0123456789abcdef"
				b.WriteByte('0')
				b.WriteByte('0')
				b.WriteByte(hex[(r>>4)&0xF])
				b.WriteByte(hex[r&0xF])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
