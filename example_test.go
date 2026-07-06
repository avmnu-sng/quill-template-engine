package quill_test

import (
	"fmt"
	"os"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/ext"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// Render a template by name from an in-memory template map. Output escaping is
// off by default.
func Example() {
	env := quill.NewWithArray(map[string]string{
		"greet.quill": `Hello {{ name | upper }}{{ "!" if loud }}`,
	})
	out, err := env.Render("greet.quill", map[string]runtime.Value{
		"name": runtime.Str("ada"),
		"loud": runtime.Bool(true),
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(out)
	// Output: Hello ADA!
}

// Pass ordinary Go values -- structs, slices, scalars -- with RenderValues,
// which marshals each binding through runtime.FromGo.
func ExampleEnvironment_RenderValues() {
	type User struct {
		Name  string   `quill:"name"`
		Admin bool     `quill:"admin"`
		Tags  []string `quill:"tags"`
	}

	env := quill.NewWithArray(map[string]string{
		"user.quill": `{{ user.name }} (admin: {{ user.admin }}) tags: {{ user.tags | join(", ") }}`,
	})
	out, err := env.RenderValues("user.quill", map[string]any{
		"user": User{Name: "ada", Admin: true, Tags: []string{"x", "y"}},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(out)
	// Output: ada (admin: true) tags: x, y
}

// Stream output to any io.Writer with RenderTo instead of buffering the whole
// result.
func ExampleEnvironment_RenderTo() {
	env := quill.NewWithArray(map[string]string{
		"list.quill": "@for n in nums {\nitem {{ n }}\n@}",
	})
	err := env.RenderTo(os.Stdout, "list.quill", map[string]runtime.Value{
		"nums": runtime.Arr(runtime.NewList(
			runtime.Int(1), runtime.Int(2), runtime.Int(3),
		)),
	})
	if err != nil {
		panic(err)
	}
	// Output:
	// item 1
	// item 2
	// item 3
}

// Turn on HTML escaping globally with WithAutoescapeHTML.
func ExampleWithAutoescapeHTML() {
	env := quill.NewWithArray(
		map[string]string{"page.quill": `<p>{{ body }}</p>`},
		quill.WithAutoescapeHTML(true),
	)
	out, err := env.Render("page.quill", map[string]runtime.Value{
		"body": runtime.Str("<b>hi</b>"),
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(out)
	// Output: <p>&lt;b&gt;hi&lt;/b&gt;</p>
}

// Register a host filter and function through the ext package and render with
// them.
func ExampleWithExtensions() {
	set := ext.NewExtensionSet()
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

	env := quill.NewWithArray(
		map[string]string{"demo.quill": `{{ clamp(42, 0, 10) }}`},
		quill.WithExtensions(set),
	)
	out, err := env.Render("demo.quill", map[string]runtime.Value(nil))
	if err != nil {
		panic(err)
	}
	fmt.Println(out)
	// Output: 10
}
