package source

import "testing"

func TestNewNormalizesNewlines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"lf untouched", "a\nb", "a\nb"},
		{"crlf folded", "a\r\nb", "a\nb"},
		{"lone cr folded", "a\rb", "a\nb"},
		{"mixed", "a\r\nb\rc\nd", "a\nb\nc\nd"},
		{"trailing crlf", "a\r\n", "a\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New("t", tt.in).Code(); got != tt.want {
				t.Fatalf("Code = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLine(t *testing.T) {
	s := New("t", "one\r\ntwo\nthree")
	tests := []struct {
		n    int
		want string
	}{
		{1, "one"},
		{2, "two"},
		{3, "three"},
		{0, ""},
		{4, ""},
	}
	for _, tt := range tests {
		if got := s.Line(tt.n); got != tt.want {
			t.Errorf("Line(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestNilSourceSafe(t *testing.T) {
	var s *Source
	if s.Name() != "" || s.Code() != "" || s.Line(1) != "" {
		t.Fatal("nil Source accessors should be safe and empty")
	}
}
