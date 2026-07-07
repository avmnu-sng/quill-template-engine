// Command inheritance shows template inheritance: a child @extends a base layout,
// overrides a @block, and pulls the base content in via parent(). Run it with:
//
//	go run ./examples/inheritance
package main

import (
	"fmt"
	"os"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

const base = `# {{ title }}

@block summary {
(no summary)
@}
@block items {
(no items)
@}
`

const page = `@extends "base.quill"
@block summary {
{{ parent() }}
A short report with {{ items | length }} items.
@}
@block items {
@for it in items {
- {{ it }}
@}
@}
`

func main() {
	if err := render(); err != nil {
		fmt.Fprintln(os.Stderr, "inheritance:", err)
		os.Exit(1)
	}
}

func render() error {
	env := quill.NewWithArray(map[string]string{
		"base.quill": base,
		"page.quill": page,
	})
	out, err := env.Render("page.quill", map[string]runtime.Value{
		"title": runtime.Str("Daily Report"),
		"items": runtime.Arr(runtime.NewList(
			runtime.Str("ship release"),
			runtime.Str("triage issues"),
			runtime.Str("review PRs"),
		)),
	})
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}
