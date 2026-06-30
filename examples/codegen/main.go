// Command codegen shows Quill emitting source code: a Go struct generated from
// a field list. Output escaping is OFF by default, so the angle brackets and
// braces of the generated code pass through verbatim -- the use case Quill is
// built for. Run it with:
//
//	go run ./examples/codegen
package main

import (
	"fmt"
	"os"

	quill "github.com/avmnusng/quill-template-engine"
	"github.com/avmnusng/quill-template-engine/runtime"
)

// The template uses the @-sigil statement form, so the literal { and } of the
// generated Go struct are ordinary output and never confused with control flow.
const tmpl = `type {{ name }} struct {
@for f in fields {
	{{ f.name }} {{ f.type }}
@}
}
`

func main() {
	if err := render(); err != nil {
		fmt.Fprintln(os.Stderr, "codegen:", err)
		os.Exit(1)
	}
}

func render() error {
	env := quill.NewWithArray(map[string]string{"struct.ql": tmpl})

	field := func(name, typ string) runtime.Value {
		f := runtime.NewArray()
		f.SetStr("name", runtime.Str(name))
		f.SetStr("type", runtime.Str(typ))
		return runtime.Arr(f)
	}
	fields := runtime.Arr(runtime.NewList(
		field("ID", "int64"),
		field("Name", "string"),
		field("Tags", "[]string"),
	))

	out, err := env.Render("struct.ql", map[string]runtime.Value{
		"name":   runtime.Str("User"),
		"fields": fields,
	})
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}
