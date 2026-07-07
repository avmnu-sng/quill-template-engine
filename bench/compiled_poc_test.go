package quillbench

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// compiledLoop is a hand-written stand-in for what a compile-to-Go backend would
// emit for quillLoop:
//
//	@for u in users {
//	{{ loop.index }}. {{ u.name | upper }} <{{ u.email }}>
//	@}
//
// It writes directly to an io.Writer with no per-node dispatch, no Context, no
// loopInfo object, and no copy-on-write: loop.index is the inline i+1, the body
// literals are emitted as constants, and the field reads and the upper filter go
// straight through the runtime ops the interpreter already uses. It is the
// proof-of-ceiling for the reserved compile-to-Go backend.
func compiledLoop(w io.Writer, users runtime.Value) error {
	pairs, err := runtime.EnsureTraversable(users, false)
	if err != nil {
		return err
	}
	n := len(pairs)
	for i := 0; i < n; i++ {
		u := pairs[i].Val
		if _, err := io.WriteString(w, strconv.Itoa(i+1)); err != nil {
			return err
		}
		if _, err := io.WriteString(w, ". "); err != nil {
			return err
		}
		name, err := runtime.GetAttribute(u, runtime.Str("name"), runtime.AccessDot, false)
		if err != nil {
			return err
		}
		nameText, err := runtime.ToText(name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, strings.ToUpper(nameText)); err != nil {
			return err
		}
		if _, err := io.WriteString(w, " <"); err != nil {
			return err
		}
		email, err := runtime.GetAttribute(u, runtime.Str("email"), runtime.AccessDot, false)
		if err != nil {
			return err
		}
		emailText, err := runtime.ToText(email)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, emailText); err != nil {
			return err
		}
		if _, err := io.WriteString(w, ">\n"); err != nil {
			return err
		}
	}
	return nil
}

// TestCompiledMatchesInterp asserts the hand-compiled loop is byte-identical to
// the interpreter, so the benchmark below compares equivalent work.
func TestCompiledMatchesInterp(t *testing.T) {
	env := quill.NewFromMap(map[string]string{"loop.ql": quillLoop})
	want, err := env.Render("loop.ql", map[string]runtime.Value{"users": quillUsers()})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := compiledLoop(&b, quillUsers()); err != nil {
		t.Fatal(err)
	}
	if b.String() != want {
		t.Fatalf("compiled output differs from interpreter:\n compiled=%q\n interp  =%q", b.String(), want)
	}
}

// BenchmarkCompiled_Loop_Render times the hand-compiled form across the same row
// counts as BenchmarkQuill_Loop_Render (interpreter) and BenchmarkText_Loop_Render,
// so the proof-of-ceiling scaling lines up column-for-column with the others.
func BenchmarkCompiled_Loop_Render(b *testing.B) {
	for _, n := range loopSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			users := quillUsersN(n)
			var buf bytes.Buffer
			if err := compiledLoop(&buf, users); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(buf.Len()))
			b.ReportAllocs()
			for b.Loop() {
				if err := compiledLoop(io.Discard, users); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
