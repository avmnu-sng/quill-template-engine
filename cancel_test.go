package quill

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"
	"time"

	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
	"github.com/avmnu-sng/quill-template-engine/pkg/ext"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// These tests exercise the context threading added to the render boundary: an
// uncancelled render must be byte-identical to a background render (the binding
// invariant), and a cancelled or expired context must abort the render at the
// render-entry or loop-iteration checkpoint with a KindRuntime error that still
// unwraps to the underlying context sentinel. They add NEW assertions only; no
// existing conformance/differential assertion is touched.

// TestRenderCancelledBeforeStart proves an already-cancelled context aborts a
// render at the render-entry checkpoint, before any output, with an error that
// classifies as KindRuntime and unwraps to context.Canceled.
func TestRenderCancelledBeforeStart(t *testing.T) {
	e := NewFromMap(map[string]string{
		"hello.ql": "Hello, {{ name }}!",
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before rendering

	out, err := e.Render(ctx, "hello.ql", map[string]runtime.Value{"name": runtime.Str("world")})
	if err == nil {
		t.Fatalf("expected a cancellation error, got output %q and nil error", out)
	}
	if !stderrors.Is(err, context.Canceled) {
		t.Fatalf("error does not unwrap to context.Canceled: %v", err)
	}
	if errors.KindOf(err) != errors.KindRuntime {
		t.Fatalf("expected KindRuntime, got %v (err %v)", errors.KindOf(err), err)
	}
}

// TestRenderCancelledDuringLoop proves the @for loop-iteration checkpoint honors
// a context cancelled mid-render: a filter cancels the context on the first
// iteration, and the loop must abort at the next iteration boundary rather than
// running to completion.
func TestRenderCancelledDuringLoop(t *testing.T) {
	e := NewFromMap(map[string]string{
		"loop.ql": "@for n in items {\n{{ n | tripwire }}\n@}\n",
	})
	ctx, cancel := context.WithCancel(context.Background())
	// A host filter that cancels the render context the first time it runs, so
	// the SECOND loop iteration hits the cancellation checkpoint.
	e.extensions.AddFilter(&ext.Filter{
		Name: "tripwire",
		Fn: func(_ context.Context, args []runtime.Value) (runtime.Value, error) {
			cancel()
			return args[0], nil
		},
	})

	items := runtime.NewList(runtime.Int(1), runtime.Int(2), runtime.Int(3), runtime.Int(4))
	_, err := e.Render(ctx, "loop.ql", map[string]runtime.Value{"items": runtime.Arr(items)})
	if err == nil {
		t.Fatal("expected a cancellation error from the loop checkpoint, got nil")
	}
	if !stderrors.Is(err, context.Canceled) {
		t.Fatalf("error does not unwrap to context.Canceled: %v", err)
	}
}

// TestRenderDeadlineExceeded proves an expired deadline aborts the render and
// the error unwraps to context.DeadlineExceeded.
func TestRenderDeadlineExceeded(t *testing.T) {
	e := NewFromMap(map[string]string{
		"hello.ql": "Hi {{ name }}",
	})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()

	_, err := e.Render(ctx, "hello.ql", map[string]runtime.Value{"name": runtime.Str("x")})
	if err == nil {
		t.Fatal("expected a deadline-exceeded error, got nil")
	}
	if !stderrors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error does not unwrap to context.DeadlineExceeded: %v", err)
	}
}

// TestRenderUncancelledIsUnaffected is the binding-invariant guard: a live
// (background) context renders exactly what the engine produced before context
// was threaded, so an uncancelled render is byte-identical.
func TestRenderUncancelledIsUnaffected(t *testing.T) {
	e := NewFromMap(map[string]string{
		"loop.ql": "@for n in items {\n{{ n }},\n@}\ndone",
	})
	items := runtime.NewList(runtime.Int(1), runtime.Int(2), runtime.Int(3))
	out, err := e.Render(context.Background(), "loop.ql",
		map[string]runtime.Value{"items": runtime.Arr(items)})
	if err != nil {
		t.Fatalf("unexpected error on a live-context render: %v", err)
	}
	if want := "1,\n2,\n3,\ndone"; out != want {
		t.Fatalf("live-context render drifted: got %q, want %q", out, want)
	}
}

// TestRenderToCancelledBeforeStart proves the streaming entry (RenderTo) also
// honors an already-cancelled context.
func TestRenderToCancelledBeforeStart(t *testing.T) {
	e := NewFromMap(map[string]string{
		"hello.ql": "Hello, {{ name }}!",
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var b strings.Builder
	err := e.RenderTo(ctx, &b, "hello.ql", map[string]runtime.Value{"name": runtime.Str("world")})
	if err == nil {
		t.Fatalf("expected a cancellation error, got output %q and nil error", b.String())
	}
	if !stderrors.Is(err, context.Canceled) {
		t.Fatalf("error does not unwrap to context.Canceled: %v", err)
	}
}
