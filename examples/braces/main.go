// Command braces renders a brace-delimited config block: an nginx-style
// "server { ... }" with nested "location { ... }" blocks. Output escaping is
// OFF by default, so the literal { and } of the config pass through verbatim.
// The template uses the @-sigil statement form, so those braces are ordinary
// output and are never confused with control flow. Run it with:
//
//	go run ./examples/braces
package main

import (
	"context"
	"fmt"
	"os"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// The template uses the @-sigil statement form, so the literal { and } of the
// nginx-style config are ordinary output and never confused with control flow.
const tmpl = `server {
	listen {{ port }};
	server_name {{ host }};
@for loc in locations {
	location {{ loc.path }} {
		proxy_pass {{ loc.upstream }};
	}
@}
}
`

func main() {
	if err := render(); err != nil {
		fmt.Fprintln(os.Stderr, "braces:", err)
		os.Exit(1)
	}
}

func render() error {
	env := quill.NewFromMap(map[string]string{"server.quill": tmpl})

	location := func(path, upstream string) runtime.Value {
		l := runtime.NewArray()
		l.SetStr("path", runtime.Str(path))
		l.SetStr("upstream", runtime.Str(upstream))
		return runtime.Arr(l)
	}
	locations := runtime.Arr(runtime.NewList(
		location("/", "http://127.0.0.1:8080"),
		location("/api", "http://127.0.0.1:9090"),
		location("/static", "http://127.0.0.1:7070"),
	))

	out, err := env.Render(context.Background(), "server.quill", map[string]runtime.Value{
		"port":      runtime.Str("80"),
		"host":      runtime.Str("example.com"),
		"locations": locations,
	})
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}
