package ext

import (
	"strconv"
	"strings"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// Dump renders a value in a Go-native, %#v-style structured form for the dump()
// debug function (spec 03 Section 3.3). Each value is tagged
// with its Quill kind so a debug dump is unambiguous about types (an Int 0 reads
// differently from a Str "0"). Collections recurse with insertion order
// preserved.
func Dump(v runtime.Value) string {
	var b strings.Builder
	dumpValue(&b, v, "")
	return b.String()
}

func dumpValue(b *strings.Builder, v runtime.Value, indent string) {
	switch v.Kind {
	case runtime.KNull:
		b.WriteString("null")
	case runtime.KBool:
		b.WriteString("bool(")
		b.WriteString(strconv.FormatBool(v.B))
		b.WriteByte(')')
	case runtime.KInt:
		b.WriteString("int(")
		b.WriteString(strconv.FormatInt(v.I, 10))
		b.WriteByte(')')
	case runtime.KFloat:
		b.WriteString("float(")
		b.WriteString(strconv.FormatFloat(v.F, 'g', -1, 64))
		b.WriteByte(')')
	case runtime.KStr:
		b.WriteString("string(")
		b.WriteString(strconv.Quote(v.S))
		b.WriteByte(')')
	case runtime.KSafe:
		b.WriteString("safe(")
		b.WriteString(strconv.Quote(v.S))
		b.WriteByte(')')
	case runtime.KArray:
		dumpArray(b, v.Arr, indent)
	case runtime.KObject:
		b.WriteString("object(")
		if s, err := runtime.ToText(v); err == nil {
			b.WriteString(strconv.Quote(s))
		} else {
			b.WriteString("?")
		}
		b.WriteByte(')')
	default:
		b.WriteString("unknown")
	}
}

func dumpArray(b *strings.Builder, a *runtime.Array, indent string) {
	if a == nil || a.Len() == 0 {
		b.WriteString("array(0) {}")
		return
	}
	inner := indent + "  "
	b.WriteString("array(")
	b.WriteString(strconv.Itoa(a.Len()))
	b.WriteString(") {\n")
	for _, p := range a.Pairs() {
		b.WriteString(inner)
		b.WriteByte('[')
		if p.Key.Kind == runtime.KInt {
			b.WriteString(strconv.FormatInt(p.Key.I, 10))
		} else {
			b.WriteString(strconv.Quote(p.Key.S))
		}
		b.WriteString("] => ")
		dumpValue(b, p.Val, inner)
		b.WriteByte('\n')
	}
	b.WriteString(indent)
	b.WriteByte('}')
}
