//go:build thirdparty

// This file is compiled only under the "thirdparty" build tag, so the default
// benchmark run stays offline with zero external dependencies. It adds the
// third-party peer (pongo2, jet) legs for two of the Phase 2a scenarios -- the
// filter pipeline and the if/elseif/else conditional -- so those workloads can
// be compared against the same-runtime Twig/Jinja-family engines, exactly as the
// Phase 1 Loop workload is.
//
// FAIRNESS NOTE ON THE PEER FILTER SCENARIO. The offline filter scenario
// (scenarios_test.go) chains upper|trim|replace|default, a chain pongo2 and jet
// cannot express with a matching filter set (pongo2 has no trim filter; neither
// has Quill's map-form replace or null-only default). So the PEER filter
// scenario here uses the subset every engine reproduces byte-identically --
// upper on the name plus join on the tag list -- with its own templates
// (quillPeerFilter / stdPeerFilter / the pongo2 and jet sources). The name keeps
// its surrounding whitespace precisely because no engine trims it, so all four
// engines emit the same bytes. The conditional scenario, by contrast, reuses the
// offline quillCond/stdCond templates and condRows data unchanged: pongo2 and jet
// both express the four-arm if/elseif/else chain identically.
//
// pongo2 and jet default to HTML-autoescaping, but every interpolated value in
// these two scenarios is escape-free (letters, digits, spaces, commas, the
// bracket/period/colon literals are TEMPLATE text), so no escaping fires and the
// raw output already matches byte for byte -- no normalization is needed. This is
// the same property the Phase 1 Loop fairness relies on.
//
// Fetch the peers and run with:
//
//	cd bench
//	go get github.com/flosch/pongo2/v6@v6.1.0 github.com/CloudyKit/jet/v6@v6.2.0
//	go test -tags thirdparty -bench='Filter|Cond' -benchmem
//
// The default (untagged) build ignores them.

package quillbench

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	texttmpl "text/template"

	quill "github.com/avmnu-sng/quill-template-engine"
	"github.com/avmnu-sng/quill-template-engine/pkg/interp"

	jet "github.com/CloudyKit/jet/v6"
	pongo2 "github.com/flosch/pongo2/v6"
)

// ============================================================================
// Peer filter scenario templates (upper + join, the all-engine-common subset).
// ============================================================================

// quillPeerFilter is the peer-comparable slice of the filter pipeline: upper on
// the name and join on the tag list, the two filters pongo2 and jet also provide.
// The name is NOT trimmed (no engine here trims it), so all four engines emit the
// same bytes.
const quillPeerFilter = `@for u in users {
{{ loop.index }}. {{ u.name | upper }} [{{ u.tags | join(", ") }}]
@}`

// stdPeerFilter mirrors quillPeerFilter through text/template. upper is a FuncMap
// entry (filterFuncs, shared with the offline scenarios) and join is
// strings.Join; the -}} trims frame the loop so the output is byte-identical.
const stdPeerFilter = `{{range $i, $u := .Users -}}
{{add $i 1}}. {{upper $u.Name}} [{{join $u.Tags ", "}}]
{{end -}}`

// pongoPeerFilter mirrors quillPeerFilter in pongo2 (Django/Jinja) syntax. The
// row-terminating newline is INSIDE the loop, before {% endfor %}, so the full
// output matches the Quill @for byte for byte (a multi-line body would leak the
// tag-line newlines).
const pongoPeerFilter = `{% for u in users %}{{ forloop.Counter }}. {{ u.Name|upper }} [{{ u.Tags|join:", " }}]
{% endfor %}`

// jetPeerFilter mirrors quillPeerFilter in jet syntax. jet's range index is a
// separate 0-based variable, so `i + 1` reproduces the 1-based counter; upper is
// a jet builtin used via the pipe form, and join is supplied as the joinTags
// global function (jet has no join builtin over a []string). Trim markers frame
// the loop so the output is byte-identical.
const jetPeerFilter = `{{- range i, u := users -}}
{{ i + 1 }}. {{ u.Name | upper }} [{{ joinTags(u.Tags, ", ") }}]
{{ end -}}`

// jetFilterSet builds a jet Set holding the peer filter and conditional
// templates, with a joinTags global that joins a []string with a separator (the
// join filter jet lacks). Development mode is left off so parsed templates are
// cached -- the fair "loaded once, render many" configuration.
func jetFilterSet() *jet.Set {
	loader := jet.NewInMemLoader()
	loader.Set("filter.jet", jetPeerFilter)
	loader.Set("cond.jet", jetCond)
	set := jet.NewSet(loader)
	set.AddGlobalFunc("joinTags", func(a jet.Arguments) reflect.Value {
		a.RequireNumOfArguments("joinTags", 2, 2)
		list, ok := a.Get(0).Interface().([]string)
		if !ok {
			a.Panicf("joinTags: first argument must be []string")
		}
		return reflect.ValueOf(strings.Join(list, a.Get(1).String()))
	})
	return set
}

// ============================================================================
// Peer conditional scenario templates (reuse quillCond/stdCond/condRows).
// ============================================================================

// pongoCond mirrors quillCond in pongo2 syntax: the four-arm if/elif/else chain
// keyed off the row score, rendering "{counter}:\n{ARM}\n" exactly as the Quill
// and stdlib conditionals do. The arm bodies and the surrounding newlines are
// laid out so the full output matches byte for byte.
const pongoCond = `{% for u in users %}{{ forloop.Counter }}:
{% if u.Score >= 90 %}A
{% elif u.Score >= 70 %}B
{% elif u.Score >= 50 %}C
{% else %}D
{% endif %}{% endfor %}`

// jetCond mirrors quillCond in jet syntax. The 0-based range index plus one is
// the 1-based counter; the four-arm if/else-if/else chain renders the same
// "{counter}:\n{ARM}\n" shape. Trim markers frame the loop.
const jetCond = `{{- range i, u := users -}}
{{ i + 1 }}:
{{ if u.Score >= 90 }}A
{{ else if u.Score >= 70 }}B
{{ else if u.Score >= 50 }}C
{{ else }}D
{{ end }}{{ end -}}`

// ============================================================================
// Fairness: full-output byte-identity across every engine timed below.
// ============================================================================

// TestVerifyScenariosThirdparty asserts the ENTIRE rendered output of the peer
// filter and conditional scenarios is byte-identical across every engine the
// thirdparty benchmarks compare: Quill (interpreter), text/template, pongo2, and
// jet. Timing engines that produce identical bytes is the whole point -- the
// benchmarks must compare equivalent work. No normalization is needed: every
// interpolated value is escape-free, so pongo2's and jet's default HTML
// autoescaping does not fire.
func TestVerifyScenariosThirdparty(t *testing.T) {
	const n = 8 // exercises all four conditional arms and several filter rows

	// ---- Peer filter scenario: upper + join ----
	{
		env := quill.NewFromMap(map[string]string{"filter.ql": quillPeerFilter})
		qOut, err := env.Render("filter.ql", quillFilterVars(n))
		if err != nil {
			t.Fatalf("quill peer filter: %v", err)
		}

		tt := texttmpl.Must(texttmpl.New("filter").Funcs(filterFuncs).Parse(stdPeerFilter))
		var tb strings.Builder
		if err := tt.Execute(&tb, struct{ Users []filterRow }{Users: filterRows(n)}); err != nil {
			t.Fatalf("text peer filter: %v", err)
		}

		pt := pongo2.Must(pongo2.FromString(pongoPeerFilter))
		pOut, err := pt.Execute(pongo2.Context{"users": filterRows(n)})
		if err != nil {
			t.Fatalf("pongo2 peer filter: %v", err)
		}

		jt, err := jetFilterSet().GetTemplate("filter.jet")
		if err != nil {
			t.Fatalf("jet peer filter get: %v", err)
		}
		var jOut writerString
		if err := jt.Execute(&jOut, jet.VarMap{}.Set("users", filterRows(n)), nil); err != nil {
			t.Fatalf("jet peer filter exec: %v", err)
		}

		for name, got := range map[string]string{
			"text":   tb.String(),
			"pongo2": pOut,
			"jet":    jOut.String(),
		} {
			if got != qOut {
				t.Errorf("peer filter %s mismatch\n quill=%q\n %s =%q", name, qOut, name, got)
			}
		}
		// Sanity: upper actually fired (name became AIKO), so a no-op cannot pass.
		if !strings.Contains(qOut, "AIKO") {
			t.Errorf("peer filter did not uppercase the name: %q", qOut)
		}
	}

	// ---- Peer conditional scenario (reuses the offline templates/data) ----
	{
		env := quill.NewFromMap(map[string]string{"cond.ql": quillCond})
		qOut, err := env.Render("cond.ql", quillCondVars(n))
		if err != nil {
			t.Fatalf("quill cond: %v", err)
		}

		tt := texttmpl.Must(texttmpl.New("cond").Funcs(filterFuncs).Parse(stdCond))
		var tb strings.Builder
		if err := tt.Execute(&tb, struct{ Users []condRow }{Users: condRows(n)}); err != nil {
			t.Fatalf("text cond: %v", err)
		}

		pt := pongo2.Must(pongo2.FromString(pongoCond))
		pOut, err := pt.Execute(pongo2.Context{"users": condRows(n)})
		if err != nil {
			t.Fatalf("pongo2 cond: %v", err)
		}

		jt, err := jetFilterSet().GetTemplate("cond.jet")
		if err != nil {
			t.Fatalf("jet cond get: %v", err)
		}
		var jOut writerString
		if err := jt.Execute(&jOut, jet.VarMap{}.Set("users", condRows(n)), nil); err != nil {
			t.Fatalf("jet cond exec: %v", err)
		}

		for name, got := range map[string]string{
			"text":   tb.String(),
			"pongo2": pOut,
			"jet":    jOut.String(),
		} {
			if got != qOut {
				t.Errorf("peer cond %s mismatch\n quill=%q\n %s =%q", name, qOut, name, got)
			}
		}
		// Sanity: every arm appears, so a chain stuck on one branch cannot pass.
		for _, arm := range []string{"\nA\n", "\nB\n", "\nC\n", "\nD\n"} {
			if !strings.Contains(qOut, arm) {
				t.Errorf("peer cond output missing arm %q: %q", arm, qOut)
			}
		}
	}
}

// ============================================================================
// pongo2 benchmarks (filter + conditional).
// ============================================================================

func BenchmarkPongo2_Filter_Render(b *testing.B) {
	t := pongo2.Must(pongo2.FromString(pongoPeerFilter))
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ctx := pongo2.Context{"users": filterRows(n)}
			out, err := t.Execute(ctx)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			for b.Loop() {
				if err := t.ExecuteWriter(ctx, io.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPongo2_Cond_Render(b *testing.B) {
	t := pongo2.Must(pongo2.FromString(pongoCond))
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ctx := pongo2.Context{"users": condRows(n)}
			out, err := t.Execute(ctx)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			for b.Loop() {
				if err := t.ExecuteWriter(ctx, io.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ============================================================================
// jet benchmarks (filter + conditional).
// ============================================================================

func BenchmarkJet_Filter_Render(b *testing.B) {
	t, err := jetFilterSet().GetTemplate("filter.jet")
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			vars := jet.VarMap{}.Set("users", filterRows(n))
			var buf writerString
			if err := t.Execute(&buf, vars, nil); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(buf.String())))
			b.ReportAllocs()
			for b.Loop() {
				if err := t.Execute(io.Discard, vars, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkJet_Cond_Render(b *testing.B) {
	t, err := jetFilterSet().GetTemplate("cond.jet")
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			vars := jet.VarMap{}.Set("users", condRows(n))
			var buf writerString
			if err := t.Execute(&buf, vars, nil); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(buf.String())))
			b.ReportAllocs()
			for b.Loop() {
				if err := t.Execute(io.Discard, vars, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ============================================================================
// Quill peer-filter benchmark: the upper+join subset, so Quill's number sits in
// the SAME column as the pongo2/jet peer-filter numbers (the offline
// BenchmarkQuill_Filter_Render times the richer upper|trim|replace|default chain
// that the peers cannot express, so it is not a like-for-like peer comparison).
// ============================================================================

func BenchmarkQuill_PeerFilter_Render(b *testing.B) {
	env := quill.NewFromMap(map[string]string{"filter.ql": quillPeerFilter})
	tmpl, err := env.LoadTemplate("filter.ql")
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range scenarioSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			vars := quillFilterVars(n)
			out, err := interp.Render(env, tmpl, vars)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			for b.Loop() {
				if sink, err = interp.Render(env, tmpl, vars); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
