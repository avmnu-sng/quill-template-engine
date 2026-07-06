package interp

import (
	"strings"
	"testing"
)

// TestLoopPrevNext covers loop.prev and loop.next: the previous and next
// element's value, Null at the first and last iteration respectively (spec 01
// Section 4.2).
func TestLoopPrevNext(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "prev is null on first",
			body: "@for x in [10,20,30] {\n[{{ loop.prev ?? \"-\" }}]\n@}\n",
			want: "[-]\n[10]\n[20]\n",
		},
		{
			name: "next is null on last",
			body: "@for x in [10,20,30] {\n[{{ loop.next ?? \"-\" }}]\n@}\n",
			want: "[20]\n[30]\n[-]\n",
		},
		{
			name: "single element has null prev and next",
			body: "@for x in [7] {\n{{ loop.prev ?? \"-\" }}|{{ loop.next ?? \"-\" }}\n@}\n",
			want: "-|-\n",
		},
		{
			name: "mapping prev/next are the values",
			body: "@for k, v in {a: 1, b: 2, c: 3} {\n{{ k }}:{{ loop.prev ?? \"-\" }}>{{ loop.next ?? \"-\" }}\n@}\n",
			want: "a:->2\nb:1>3\nc:2>-\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renderStub(t, eng, c.body, nil); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestLoopChanged covers loop.changed(expr): true on the first iteration and
// whenever the watched expression differs from its prior-iteration value (spec 01
// Section 4.2).
func TestLoopChanged(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "changed on first then on each distinct value",
			body: "@for x in [1,1,2,2,3] {\n{{ x }}:{{ loop.changed(x) }}\n@}\n",
			want: "1:true\n1:false\n2:true\n2:false\n3:true\n",
		},
		{
			name: "changed over a derived key",
			body: "@for x in [1,2,3,10,11] {\n{{ x }}:{{ loop.changed(x >= 10) }}\n@}\n",
			want: "1:true\n2:false\n3:false\n10:true\n11:false\n",
		},
		{
			name: "two distinct call sites track independently",
			body: "@for x in [1,1,2] {\n{{ loop.changed(x) }}/{{ loop.changed(x * 0) }}\n@}\n",
			want: "true/true\nfalse/false\ntrue/false\n",
		},
		{
			name: "nested loops keep independent memory",
			body: "@for a in [1,1] {\n@for b in [5,5,6] {\n{{ loop.changed(b) }}\n@}\nA={{ loop.changed(a) }}\n@}\n",
			want: "true\nfalse\ntrue\nA=true\ntrue\nfalse\ntrue\nA=false\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renderStub(t, eng, c.body, nil); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestLoopChangedOutsideLoop rejects loop.changed(...) used where no loop is
// active with a runtime error naming the constraint.
func TestLoopChangedOutsideLoop(t *testing.T) {
	eng := newStub(nil)
	_, err := renderStubResult(t, eng, "{{ loop.changed(1) }}")
	if err == nil {
		t.Fatal("expected an error for loop.changed outside a loop")
	}
	if !strings.Contains(err.Error(), "inside a for loop") {
		t.Errorf("error %q should mention the loop constraint", err.Error())
	}
}

// TestLoopChangedArity rejects a loop.changed call with the wrong argument count.
func TestLoopChangedArity(t *testing.T) {
	eng := newStub(nil)
	_, err := renderStubResult(t, eng, "@for x in [1] {\n{{ loop.changed() }}\n@}\n")
	if err == nil {
		t.Fatal("expected an arity error for loop.changed()")
	}
	if !strings.Contains(err.Error(), "exactly one argument") {
		t.Errorf("error %q should name the arity requirement", err.Error())
	}
}

// TestForIfFilter covers the fused @for..if form: the body runs only over the
// survivors and every loop.* field counts only them (spec 01 Section 4.2).
func TestForIfFilter(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "filter keeps only survivors and renumbers index",
			body: "@for n in [1,2,3,4,5,6] if n % 2 == 0 {\n{{ loop.index }}:{{ n }}\n@}\n",
			want: "1:2\n2:4\n3:6\n",
		},
		{
			name: "length and revindex reflect survivors",
			body: "@for n in [1,2,3,4,5] if n > 2 {\n{{ n }} of {{ loop.length }} rev {{ loop.revindex }}\n@}\n",
			want: "3 of 3 rev 3\n4 of 3 rev 2\n5 of 3 rev 1\n",
		},
		{
			name: "last marks the last survivor for a trailing comma",
			body: "@for n in [1,2,3,4] if n != 3 {~\n{{ n }}{{- \", \" if not loop.last -}}\n@}~",
			want: "1, 2, 4",
		},
		{
			name: "first and prev/next reflect survivors",
			body: "@for n in [1,2,3,4,5] if n % 2 == 1 {\n{{ n }}:first={{ loop.first }}:prev={{ loop.prev ?? \"-\" }}:next={{ loop.next ?? \"-\" }}\n@}\n",
			want: "1:first=true:prev=-:next=3\n3:first=false:prev=1:next=5\n5:first=false:prev=3:next=-\n",
		},
		{
			name: "else runs when zero survive",
			body: "@for n in [1,2,3] if n > 100 {\n{{ n }}\n@} else {\nnone\n@}\n",
			want: "none\n",
		},
		{
			name: "no else and zero survivors emits nothing",
			body: "before\n@for n in [1,2,3] if n > 100 {\n{{ n }}\n@}\nafter\n",
			want: "before\nafter\n",
		},
		{
			name: "condition references both targets of a mapping loop",
			body: "@for k, v in {a: 1, b: 2, c: 3} if v > 1 {\n{{ loop.index }}:{{ k }}={{ v }}\n@}\n",
			want: "1:b=2\n2:c=3\n",
		},
		{
			name: "filter composes with changed over survivors",
			body: "@for n in [1,2,3,4,5,6] if n % 2 == 0 {\n{{ n }}:{{ loop.changed(n > 3) }}\n@}\n",
			want: "2:true\n4:true\n6:false\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renderStub(t, eng, c.body, nil); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestForIfFilterScopeIsolation confirms the filter condition's target bindings
// do not leak into the surrounding scope after the loop.
func TestForIfFilterScopeIsolation(t *testing.T) {
	eng := newStub(nil)
	got := renderStub(t, eng,
		"@set n = 99\n@for n in [1,2,3] if n > 1 {\n{{ n }}\n@}\nafter={{ n }}\n", nil)
	if !strings.Contains(got, "after=99") {
		t.Errorf("loop target leaked past the loop: %q", got)
	}
}

// TestLoopChangedInFilter covers loop.changed(expr) inside a fused @for..if
// filter condition: the call resolves against this loop's own changed-frame,
// tracking each candidate element on this loop's own iteration. At the outermost
// level the frame is active during the filter, so a top-level filter may dedup
// adjacent duplicates; when nested, the filter answers for the inner loop and the
// enclosing loop's changed memory stays intact (spec 01 Section 4.2).
func TestLoopChangedInFilter(t *testing.T) {
	eng := newStub(nil)
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "top-level filter dedups adjacent duplicates",
			body: "@for x in [1,1,2,2,3] if loop.changed(x) {\n{{ x }}\n@}\n",
			want: "1\n2\n3\n",
		},
		{
			name: "filter changed renumbers loop.index over survivors",
			body: "@for x in [1,1,2,3,3] if loop.changed(x) {\n{{ loop.index }}:{{ x }}\n@}\n",
			want: "1:1\n2:2\n3:3\n",
		},
		{
			name: "nested filter answers for the inner loop only",
			body: "@for a in [1,2] {\n@for b in [7,7,8] if loop.changed(b) {\n{{ a }}.{{ b }}\n@}\n@}\n",
			want: "1.7\n1.8\n2.7\n2.8\n",
		},
		{
			name: "nested filter leaves enclosing changed memory intact",
			body: "@for a in [1,1,2] {\n" +
				"a={{ loop.changed(a) }}\n" +
				"@for b in [5,5] if loop.changed(b) {\nb={{ b }}\n@}\n@}\n",
			want: "a=true\nb=5\na=false\nb=5\na=true\nb=5\n",
		},
		{
			name: "filter and body watch the same expression independently",
			body: "@for x in [1,1,2,2] if loop.changed(x) {\n{{ x }}:{{ loop.changed(x) }}\n@}\n",
			want: "1:true\n2:true\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renderStub(t, eng, c.body, nil); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}
