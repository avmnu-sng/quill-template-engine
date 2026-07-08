package quill

import (
	"context"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/loader"
)

// TestInnermostIncludeProvideFeedsShellYield pins the slot-owner routing
// through a nested include chain: only the innermost partial contributes a
// @provide, two interp levels below the shell that @yields the slot, so its
// content must travel through the shared owner state -- including a slots map
// the owner creates only when that innermost @provide runs -- and land in the
// shell's deferred placeholder.
func TestInnermostIncludeProvideFeedsShellYield(t *testing.T) {
	tmpls := map[string]string{
		"shell.ql": "shell-top:\n@yield syms\n@include \"mid.ql\"\nshell-bottom\n",
		"mid.ql":   "mid-body\n@include \"leaf.ql\"\n",
		"leaf.ql":  "leaf-body\n@provide syms {\nDEEP\n@}\n",
	}
	env := New(loader.NewArrayLoader(tmpls))
	first, err := env.Render(context.Background(), "shell.ql", nil)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	if !strings.Contains(first, "DEEP") {
		t.Fatalf("innermost provide never reached the shell yield: %q", first)
	}
	if strings.Contains(first, "QUILL_SLOT_") {
		t.Fatalf("render leaked an unresolved slot placeholder: %q", first)
	}
	second, err := env.Render(context.Background(), "shell.ql", nil)
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if first != second {
		t.Fatalf("nested include slots diverged across renders\nfirst:  %q\nsecond: %q", first, second)
	}
}

// TestEmbedOverrideOntoBlocklessTemplate pins the embed override layering when
// the embedded template defines no blocks at all: the override is the
// sub-render's first block definition, so it must create the lazily-built
// block table rather than write into a nil map, and -- with no matching
// @block site in the embedded body -- it renders nothing.
func TestEmbedOverrideOntoBlocklessTemplate(t *testing.T) {
	tmpls := map[string]string{
		"page.ql":  "top\n@embed \"plain.ql\" {\n@block extra {\nOVERRIDE\n@}\n@}\nbottom\n",
		"plain.ql": "plain-body\n",
	}
	env := New(loader.NewArrayLoader(tmpls))
	out, err := env.Render(context.Background(), "page.ql", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "plain-body") {
		t.Fatalf("embedded body missing from output: %q", out)
	}
	if strings.Contains(out, "OVERRIDE") {
		t.Fatalf("override with no matching block site leaked into output: %q", out)
	}
}
