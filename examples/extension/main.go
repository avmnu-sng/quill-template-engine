// Command extension shows how a host adds its own callables. It registers a
// custom filter and a custom function with the typed helpers
// (ext.NewFilter/ext.NewFunction), layers them over the core standard library
// with quill.WithExtensions, and renders a template that pipes through the
// filter and calls the function. Run it with:
//
//	go run ./examples/extension
package main

import (
	"fmt"
	"os"
	"strings"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

const tmpl = `{{ "ab" | repeat(3) }}
@for n in counts {
{{ n }} -> {{ clamp(n, 0, 10) }}
@}`

func main() {
	if err := render(); err != nil {
		fmt.Fprintln(os.Stderr, "extension:", err)
		os.Exit(1)
	}
}

// callables builds the host extension set. NewFilter and NewFunction inspect the
// Go func's signature once, at registration, and marshal runtime.Value arguments
// and results through it -- string<->Str, int64<->Int, and so on -- so the body
// is plain Go.
func callables() *ext.ExtensionSet {
	set := ext.NewExtensionSet()

	// repeat is a filter: the piped string is the first parameter, the argument in
	// repeat(n) is the second. "ab" | repeat(3) is repeat("ab", 3).
	set.AddFilter(ext.NewFilter("repeat", func(s string, n int64) string {
		return strings.Repeat(s, int(n))
	}))

	// clamp is a function: every argument is explicit at the call site.
	set.AddFunction(ext.NewFunction("clamp", func(x, lo, hi int64) int64 {
		switch {
		case x < lo:
			return lo
		case x > hi:
			return hi
		default:
			return x
		}
	}))

	return set
}

func render() error {
	env := quill.NewFromMap(
		map[string]string{"demo.quill": tmpl},
		quill.WithExtensions(callables()),
	)
	counts := runtime.Arr(runtime.NewList(
		runtime.Int(5), runtime.Int(-3), runtime.Int(42),
	))
	out, err := env.Render("demo.quill", map[string]runtime.Value{"counts": counts})
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}
