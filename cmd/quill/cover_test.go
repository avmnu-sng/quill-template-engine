package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// branchTemplate has one @if with an @else, an interpolation, and trailing text,
// so a render that takes only one arm leaves an uncovered unit and an uncovered
// branch arm -- enough to exercise every format and both gate outcomes.
const branchTemplate = "@if admin {\nADMIN\n@} @else {\nUSER\n@}\n{{ name }}\n"

// coverEnv writes the branch template plus a data file into a temp dir and
// returns the dir, so each test drives runCover against real files on disk.
func coverEnv(t *testing.T, data string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "page.ql", branchTemplate)
	writeFile(t, dir, "data.json", data)
	return dir
}

func TestCoverSingleText(t *testing.T) {
	dir := coverEnv(t, `{"admin":true,"name":"ada"}`)
	var out, errOut bytes.Buffer
	err := dispatch([]string{"cover", "-root", dir, "-data", filepath.Join(dir, "data.json"), "page.ql"},
		&out, &errOut, nil)
	if err != nil {
		t.Fatalf("runCover: %v (stderr %q)", err, errOut.String())
	}
	s := out.String()
	if !strings.Contains(s, "page.ql") || !strings.Contains(s, "TOTAL") {
		t.Errorf("text report missing table: %q", s)
	}
	if !strings.Contains(s, "Units") || !strings.Contains(s, "Branches") || !strings.Contains(s, "Lines") {
		t.Errorf("text report missing columns: %q", s)
	}
}

func TestCoverCasesUnionCoversBothArms(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "page.ql", branchTemplate)
	writeFile(t, dir, "cases.json",
		`[{"template":"page.ql","data":{"admin":true,"name":"ada"}},`+
			`{"template":"page.ql","data":{"admin":false,"name":"bob"}}]`)
	var out, errOut bytes.Buffer
	// Both arms taken across the two cases, so unit coverage is 100% and a strict
	// gate passes -- proving the report unions across cases.
	err := dispatch([]string{"cover", "-root", dir, "-cases", filepath.Join(dir, "cases.json"),
		"-fail-under", "100"}, &out, &errOut, nil)
	if err != nil {
		t.Fatalf("runCover cases: %v (stderr %q)", err, errOut.String())
	}
	if !strings.Contains(out.String(), "4/4 100.0%") {
		t.Errorf("expected 100%% unit coverage across cases, got %q", out.String())
	}
}

func TestCoverLCOVFormat(t *testing.T) {
	dir := coverEnv(t, `{"admin":true,"name":"ada"}`)
	var out, errOut bytes.Buffer
	err := dispatch([]string{"cover", "-root", dir, "-data", filepath.Join(dir, "data.json"),
		"-format", "lcov", "page.ql"}, &out, &errOut, nil)
	if err != nil {
		t.Fatalf("runCover lcov: %v", err)
	}
	s := out.String()
	for _, want := range []string{"SF:page.ql", "DA:", "BRDA:", "BRF:", "BRH:", "LF:", "LH:", "end_of_record"} {
		if !strings.Contains(s, want) {
			t.Errorf("lcov output missing %q:\n%s", want, s)
		}
	}
}

func TestCoverHTMLToFile(t *testing.T) {
	dir := coverEnv(t, `{"admin":true,"name":"ada"}`)
	outFile := filepath.Join(dir, "cover.html")
	var out, errOut bytes.Buffer
	err := dispatch([]string{"cover", "-root", dir, "-data", filepath.Join(dir, "data.json"),
		"-format", "html", "-o", outFile, "page.ql"}, &out, &errOut, nil)
	if err != nil {
		t.Fatalf("runCover html: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no stdout when -o is set, got %q", out.String())
	}
	body, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read html file: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "<!doctype html>") || !strings.Contains(s, "Quill Template Coverage") {
		t.Errorf("html file not a coverage page:\n%s", s)
	}
}

func TestCoverThresholdPass(t *testing.T) {
	// Only the admin arm is unreachable content-wise, but the unit denominator is
	// fully covered when admin is true, so a 100 unit gate passes.
	dir := coverEnv(t, `{"admin":true,"name":"ada"}`)
	var out, errOut bytes.Buffer
	err := dispatch([]string{"cover", "-root", dir, "-data", filepath.Join(dir, "data.json"),
		"-fail-under", "100", "page.ql"}, &out, &errOut, nil)
	if err != nil {
		t.Fatalf("expected pass, got %v (stderr %q)", err, errOut.String())
	}
}

func TestCoverThresholdFail(t *testing.T) {
	// admin=false leaves the ADMIN body (a Text unit) unreached, so unit coverage
	// drops below 100 and the gate fails, writing the breakdown to stderr.
	dir := coverEnv(t, `{"admin":false,"name":"ada"}`)
	var out, errOut bytes.Buffer
	err := dispatch([]string{"cover", "-root", dir, "-data", filepath.Join(dir, "data.json"),
		"-fail-under", "100", "page.ql"}, &out, &errOut, nil)
	if err == nil {
		t.Fatal("expected a threshold error, got nil")
	}
	if !strings.Contains(err.Error(), "below -fail-under") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(errOut.String(), "never reached") &&
		!strings.Contains(errOut.String(), "never taken") {
		t.Errorf("expected uncovered breakdown on stderr, got %q", errOut.String())
	}
}

func TestCoverThresholdAlias(t *testing.T) {
	// -threshold is an accepted alias for -fail-under.
	dir := coverEnv(t, `{"admin":false,"name":"ada"}`)
	var out, errOut bytes.Buffer
	err := dispatch([]string{"cover", "-root", dir, "-data", filepath.Join(dir, "data.json"),
		"-threshold", "90", "page.ql"}, &out, &errOut, nil)
	if err == nil {
		t.Fatal("expected -threshold to gate like -fail-under, got nil")
	}
}

func TestCoverStdinCases(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "page.ql", branchTemplate)
	cases := `[{"template":"page.ql","data":{"admin":true,"name":"x"}}]`
	var out, errOut bytes.Buffer
	err := dispatch([]string{"cover", "-root", dir, "-cases", "-"},
		&out, &errOut, strings.NewReader(cases))
	if err != nil {
		t.Fatalf("runCover stdin cases: %v", err)
	}
	if !strings.Contains(out.String(), "page.ql") {
		t.Errorf("stdin cases report: %q", out.String())
	}
}

func TestCoverErrors(t *testing.T) {
	dir := coverEnv(t, `{"admin":true,"name":"ada"}`)
	data := filepath.Join(dir, "data.json")
	cases := []struct {
		name string
		args []string
	}{
		{"no template and no cases", []string{"cover", "-root", dir}},
		{"both cases and name", []string{"cover", "-root", dir, "-cases", data, "page.ql"}},
		{"unknown format", []string{"cover", "-root", dir, "-data", data, "-format", "xml", "page.ql"}},
		{"unknown autoescape", []string{"cover", "-root", dir, "-data", data, "-autoescape", "js", "page.ql"}},
		{"missing template file", []string{"cover", "-root", dir, "-data", data, "nope.ql"}},
		{"bad cases file", []string{"cover", "-root", dir, "-cases", filepath.Join(dir, "absent.json")}},
		{"too many names", []string{"cover", "-root", dir, "-data", data, "a.ql", "b.ql"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if err := dispatch(c.args, &out, &errOut, nil); err == nil {
				t.Errorf("expected an error, got output %q", out.String())
			}
		})
	}
}
