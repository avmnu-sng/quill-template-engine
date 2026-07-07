// Command filters shows the expression language: pipe filters, a postfix-if, a
// for loop with loop metadata, and the strict typed equality / coalesce rules.
// Run it with:
//
//	go run ./examples/filters
package main

import (
	"fmt"
	"os"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

const tmpl = `roster ({{ users | length }}):
@for u in users {
{{ loop.index }}. {{ u.name | upper }}{{ " *" if u.admin }} <{{ u.email ?? "no-email" }}>
@}
sorted: {{ names | sort | join(", ") }}
`

func main() {
	if err := render(); err != nil {
		fmt.Fprintln(os.Stderr, "filters:", err)
		os.Exit(1)
	}
}

func user(name, email string, admin bool, hasEmail bool) runtime.Value {
	u := runtime.NewArray()
	u.SetStr("name", runtime.Str(name))
	u.SetStr("admin", runtime.Bool(admin))
	if hasEmail {
		u.SetStr("email", runtime.Str(email))
	}
	return runtime.Arr(u)
}

func render() error {
	env := quill.NewWithArray(map[string]string{"roster.quill": tmpl})
	users := runtime.Arr(runtime.NewList(
		user("ada", "ada@example.com", true, true),
		user("bob", "", false, false),
		user("cleo", "cleo@example.com", false, true),
	))
	names := runtime.Arr(runtime.NewList(
		runtime.Str("cleo"), runtime.Str("ada"), runtime.Str("bob"),
	))
	out, err := env.Render("roster.quill", map[string]runtime.Value{
		"users": users,
		"names": names,
	})
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}
