package ext

import (
	"context"
	"testing"

	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// TestShapingWrap covers word-wrap: greedy packing at word boundaries, an
// overlong single word left whole, a custom break joiner, per-line paragraphs,
// and rune-counted width.
func TestShapingWrap(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"basic", callFilter(t, "wrap", runtime.Str("the quick brown fox"), runtime.Int(9)).AsStr(), "the quick\nbrown fox"},
		{"exact fit", callFilter(t, "wrap", runtime.Str("aaa bbb"), runtime.Int(7)).AsStr(), "aaa bbb"},
		{"long word whole", callFilter(t, "wrap", runtime.Str("supercalifragilistic hi"), runtime.Int(5)).AsStr(), "supercalifragilistic\nhi"},
		{"custom break", callFilter(t, "wrap", runtime.Str("a b c d"), runtime.Int(3), runtime.Str("|")).AsStr(), "a b|c d"},
		{"keeps paragraphs", callFilter(t, "wrap", runtime.Str("aa bb\ncc dd"), runtime.Int(5)).AsStr(), "aa bb\ncc dd"},
		{"blank line kept", callFilter(t, "wrap", runtime.Str("a\n\nb"), runtime.Int(4)).AsStr(), "a\n\nb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}

	// Width is counted in runes, not bytes: a three-rune multi-byte word fits a
	// width-3 line, and the next word wraps. U+00E9 is a two-byte rune.
	acc := "\u00e9\u00e9\u00e9"
	got := callFilter(t, "wrap", runtime.Str(acc+" "+acc), runtime.Int(3)).AsStr()
	if want := acc + "\n" + acc; got != want {
		t.Errorf("rune-width wrap = %q, want %q", got, want)
	}
}

// TestShapingWrapWidthError rejects a non-positive width.
func TestShapingWrapWidthError(t *testing.T) {
	s := Core()
	f, _ := s.Filter("wrap")
	if _, err := f.Fn(context.Background(), []runtime.Value{runtime.Str("x"), runtime.Int(0)}); err == nil {
		t.Fatal("expected an error for wrap width 0")
	}
}

// TestShapingTruncate covers the length cap plus omission marker, the
// word-boundary preserve form, marker-fits budgeting, and a within-length
// passthrough. It stays distinct from slice: the marker is appended and the
// result never overruns the requested length.
func TestShapingTruncate(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"cap with default omission", callFilter(t, "truncate", runtime.Str("hello world foo"), runtime.Int(11)).AsStr(), "hello wo..."},
		{"within length", callFilter(t, "truncate", runtime.Str("short"), runtime.Int(10)).AsStr(), "short"},
		{"exact length", callFilter(t, "truncate", runtime.Str("abcde"), runtime.Int(5)).AsStr(), "abcde"},
		{"custom omission", callFilter(t, "truncate", runtime.Str("abcdefgh"), runtime.Int(5), runtime.Str(">")).AsStr(), "abcd>"},
		{"preserve word", callFilter(t, "truncate", runtime.Str("hello world foo"), runtime.Int(11), runtime.Str("..."), runtime.Bool(true)).AsStr(), "hello..."},
		{"marker larger than budget", callFilter(t, "truncate", runtime.Str("abcdef"), runtime.Int(2)).AsStr(), ".."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

// truncLen renders a truncate and returns the rune length of the result, used to
// assert the result never exceeds the requested length.
func truncLen(t *testing.T, s string, n int) int {
	t.Helper()
	out := callFilter(t, "truncate", runtime.Str(s), runtime.Int(int64(n)))
	return len([]rune(out.AsStr()))
}

// TestShapingTruncateLength asserts the rune length never exceeds the cap.
func TestShapingTruncateLength(t *testing.T) {
	for _, n := range []int{1, 2, 3, 5, 8, 12} {
		got := truncLen(t, "the quick brown fox jumps", n)
		if got > n {
			t.Errorf("truncate(%d) produced %d runes, exceeds cap", n, got)
		}
	}
}

// TestShapingCenter covers centered padding with the odd-extra-on-right rule, a
// multi-rune fill, a within-width passthrough, and rune-counted width.
func TestShapingCenter(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"even padding", callFilter(t, "center", runtime.Str("hi"), runtime.Int(6)).AsStr(), "  hi  "},
		{"odd extra on right", callFilter(t, "center", runtime.Str("hi"), runtime.Int(5)).AsStr(), " hi  "},
		{"within width", callFilter(t, "center", runtime.Str("hello"), runtime.Int(3)).AsStr(), "hello"},
		{"custom fill", callFilter(t, "center", runtime.Str("x"), runtime.Int(5), runtime.Str("*")).AsStr(), "**x**"},
		{"multi-rune fill clipped", callFilter(t, "center", runtime.Str("x"), runtime.Int(5), runtime.Str("ab")).AsStr(), "abxab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

// TestShapingWordcount covers word counting over runs of non-space, tolerant of
// leading/trailing/repeated whitespace and empty input.
func TestShapingWordcount(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int64
	}{
		{"three words", "the quick brown", 3},
		{"padded", "  a  b   c ", 3},
		{"empty", "", 0},
		{"all spaces", "   \t\n ", 0},
		{"one word", "solo", 1},
		{"newlines separate", "a\nb\tc", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := callFilter(t, "wordcount", runtime.Str(c.in))
			if got.Kind() != runtime.KInt || got.AsInt() != c.want {
				t.Errorf("wordcount(%q) = %v, want %d", c.in, got, c.want)
			}
		})
	}
}
