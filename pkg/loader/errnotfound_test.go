package loader

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
)

// TestErrNotFoundSentinel locks the v1 contract for a loader miss: it is
// matchable with errors.Is(err, ErrNotFound), stays a KindRuntime *errors.Error,
// keeps a name-bearing message, and -- unlike the old strings.Contains heuristic
// -- no longer matches an unrelated error whose text merely contains "not found".
func TestErrNotFoundSentinel(t *testing.T) {
	l := NewArrayLoader(nil)
	_, err := l.Get("missing.ql")
	if err == nil {
		t.Fatal("Get of an absent template should error")
	}
	if !stderrors.Is(err, ErrNotFound) {
		t.Fatalf("miss should match ErrNotFound via errors.Is; got %v", err)
	}
	if !IsNotFound(err) {
		t.Fatal("IsNotFound should report true for a loader miss")
	}
	if k := errors.KindOf(err); k != errors.KindRuntime {
		t.Fatalf("miss should stay KindRuntime; got %v", k)
	}
	if got := err.Error(); !strings.Contains(got, `"missing.ql"`) {
		t.Fatalf("message should name the missing template; got %q", got)
	}
	// Precision guard: the replaced heuristic matched any message containing
	// "not found"; the sentinel must not.
	unrelated := errors.New(errors.KindRuntime, "widget not found in catalog")
	if IsNotFound(unrelated) {
		t.Fatal("IsNotFound must not match an unrelated 'not found' message")
	}
}
