package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"
)

func TestResolveOutput(t *testing.T) {
	cases := []struct {
		input     string
		wantFmt   Format
		wantTmpl  string
		wantError bool
	}{
		{"", outputText, "", false},
		{"json", outputJSON, "", false},
		{"yaml", outputYAML, "", false},
		{"jsonl", outputJSONL, "", false},
		{"name", outputName, "", false},
		{"wide", outputWide, "", false},
		{"template={{.Path}}", outputTemplate, "{{.Path}}", false},
		{
			`template={{range .Projects}}{{.Path}}={{.Spell}}{{"\n"}}{{end}}`, outputTemplate,
			`{{range .Projects}}{{.Path}}={{.Spell}}{{"\n"}}{{end}}`, false,
		},
		{"template=", "", "", true},
		{"unknown", "", "", true},
	}
	for _, tc := range cases {
		spec, err := ResolveOutput(tc.input)
		if (err != nil) != tc.wantError {
			t.Errorf("ResolveOutput(%q) error = %v, wantError = %v", tc.input, err, tc.wantError)
			continue
		}
		if tc.wantError {
			continue
		}
		if spec.Format != tc.wantFmt {
			t.Errorf("ResolveOutput(%q).Format = %q, want %q", tc.input, spec.Format, tc.wantFmt)
		}
		if spec.Template != tc.wantTmpl {
			t.Errorf("ResolveOutput(%q).Template = %q, want %q", tc.input, spec.Template, tc.wantTmpl)
		}
	}
}

func TestWriteTemplate(t *testing.T) {
	type project struct {
		Path  string
		Spell string
		Deps  []string
	}
	v := struct {
		Workspace string
		Count     int
		Projects  []project
	}{
		Workspace: "/tmp/ws",
		Count:     2,
		Projects: []project{
			{Path: "api", Spell: "go", Deps: []string{"internal/db"}},
			{Path: "web", Spell: "typescript"},
		},
	}

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			"simple field",
			"{{.Workspace}} ({{.Count}})",
			"/tmp/ws (2)",
		},
		{
			"range with newline",
			`{{range .Projects}}{{.Path}}={{.Spell}}{{"\n"}}{{end}}`,
			"api=go\nweb=typescript\n",
		},
		{
			"join helper",
			`{{range .Projects}}{{.Path}}: {{join .Deps ","}}{{"\n"}}{{end}}`,
			"api: internal/db\nweb: \n",
		},
		{
			"upper helper",
			`{{upper .Workspace}}`,
			"/TMP/WS",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeTemplate(&buf, v, tc.body); err != nil {
				t.Fatalf("writeTemplate: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWriteTemplateBadSyntax(t *testing.T) {
	var buf bytes.Buffer
	err := writeTemplate(&buf, struct{}{}, "{{.MissingClose")
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse template") {
		t.Errorf("error = %v, want one wrapping 'parse template'", err)
	}
}

func TestWriteTemplateSprigHelpers(t *testing.T) {
	type item struct {
		Name string
		Tags []string
	}
	v := struct {
		Items []item
		Count int
	}{
		Items: []item{
			{Name: "foo-bar", Tags: []string{"a", "b"}},
			{Name: "baz", Tags: nil},
		},
		Count: 2,
	}

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			"quote helper",
			`{{range .Items}}{{quote .Name}} {{end}}`,
			`"foo-bar" "baz" `,
		},
		{
			"toJson helper",
			`{{index .Items 0 | toJson}}`,
			`{"Name":"foo-bar","Tags":["a","b"]}`,
		},
		{
			"default helper",
			`{{$zero := ""}}{{default "fallback" $zero}}`,
			"fallback",
		},
		{
			"camelcase helper",
			`{{camelcase "foo-bar"}}`,
			"FooBar",
		},
		{
			"kebabcase helper",
			`{{kebabcase "FooBar"}}`,
			"foo-bar",
		},
		{
			"base path helper",
			`{{base "a/b/c"}}`,
			"c",
		},
		{
			"ternary helper",
			`{{ternary "yes" "no" (eq .Count 2)}}`,
			"yes",
		},
		{
			"sortAlpha helper",
			`{{$s := list "c" "a" "b" | sortAlpha}}{{join $s ","}}`,
			"a,b,c",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeTemplate(&buf, v, tc.body); err != nil {
				t.Fatalf("writeTemplate: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWriteTemplateEnvBlocked(t *testing.T) {
	var buf bytes.Buffer
	err := writeTemplate(&buf, struct{}{}, `{{env "PATH"}}`)
	if err == nil {
		t.Fatal("expected error for blocked 'env' function, got nil")
	}
	if !strings.Contains(err.Error(), "parse template") {
		t.Errorf("expected parse template error, got: %v", err)
	}
}

func TestWriteJSONL(t *testing.T) {
	type proj struct {
		Path string `json:"path"`
		Pack string `json:"pack"`
	}
	// Struct with exactly one slice field — should emit one line per element.
	v := struct {
		Workspace string `json:"workspace"`
		Count     int    `json:"count"`
		Projects  []proj `json:"projects"`
	}{
		Workspace: "/tmp/ws",
		Count:     2,
		Projects: []proj{
			{Path: "api", Pack: "go"},
			{Path: "web", Pack: "typescript"},
		},
	}

	var buf bytes.Buffer
	if err := writeJSONL(&buf, v); err != nil {
		t.Fatalf("writeJSONL: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), buf.String())
	}
	var p0, p1 proj
	if err := json.Unmarshal([]byte(lines[0]), &p0); err != nil {
		t.Fatalf("unmarshal line 0: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &p1); err != nil {
		t.Fatalf("unmarshal line 1: %v", err)
	}
	if p0.Path != "api" || p0.Pack != "go" {
		t.Errorf("line 0: got %+v, want {api go}", p0)
	}
	if p1.Path != "web" || p1.Pack != "typescript" {
		t.Errorf("line 1: got %+v, want {web typescript}", p1)
	}
}

func TestOutputDstNoTee(t *testing.T) {
	// With no --tee set, outputDst returns os.Stdout.
	prev := global.tee
	global.tee = ""
	defer func() { global.tee = prev }()

	w, cleanup, err := outputDst()
	if err != nil {
		t.Fatalf("outputDst: %v", err)
	}
	defer cleanup()
	if w != os.Stdout {
		t.Errorf("expected os.Stdout, got %T", w)
	}
}

func TestOutputDstTeeWritesBoth(t *testing.T) {
	// With --tee set, outputDst returns a MultiWriter that writes to both
	// stdout and the named file.
	f, err := os.CreateTemp(t.TempDir(), "tee-*.json")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	teeTarget := f.Name()
	f.Close()

	prev := global.tee
	global.tee = teeTarget
	defer func() { global.tee = prev }()

	w, cleanup, err := outputDst()
	if err != nil {
		t.Fatalf("outputDst: %v", err)
	}
	defer cleanup()

	v := struct{ X int }{X: 42}
	if err := writeJSON(w, v); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	cleanup()

	got, err := os.ReadFile(teeTarget)
	if err != nil {
		t.Fatalf("read tee file: %v", err)
	}
	if !strings.Contains(string(got), `"X": 42`) {
		t.Errorf("tee file missing expected content: %q", got)
	}
}

func TestOutputDstTeeAppends(t *testing.T) {
	// Repeated calls to outputDst with the same tee path append (not overwrite).
	dir := t.TempDir()
	teeTarget := dir + "/out.json"

	for i := range 2 {
		prev := global.tee
		global.tee = teeTarget
		w, cleanup, err := outputDst()
		global.tee = prev
		if err != nil {
			t.Fatalf("outputDst call %d: %v", i, err)
		}
		v := struct{ N int }{N: i}
		if err := writeJSON(w, v); err != nil {
			t.Fatalf("writeJSON call %d: %v", i, err)
		}
		cleanup()
	}

	got, err := os.ReadFile(teeTarget)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Both JSON objects should be present.
	if !strings.Contains(string(got), `"N": 0`) || !strings.Contains(string(got), `"N": 1`) {
		t.Errorf("tee file missing both writes: %q", got)
	}
}

func TestOutputDstTeeBadPath(t *testing.T) {
	prev := global.tee
	global.tee = "/nonexistent-dir-that-should-not-exist/out.json"
	defer func() { global.tee = prev }()

	_, _, err := outputDst()
	if err == nil {
		t.Fatal("expected error for bad --tee path, got nil")
	}
	if !strings.Contains(err.Error(), "--tee") {
		t.Errorf("error should mention --tee, got: %v", err)
	}
}

func TestWriteJSONLMultiSliceFallback(t *testing.T) {
	// Struct with multiple slice fields — should emit whole object as one line.
	v := struct {
		Roots []string `json:"roots"`
		Nodes []string `json:"nodes"`
	}{
		Roots: []string{"a"},
		Nodes: []string{"b", "c"},
	}

	var buf bytes.Buffer
	if err := writeJSONL(&buf, v); err != nil {
		t.Fatalf("writeJSONL: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (fallback), got %d: %q", len(lines), buf.String())
	}
	var got struct {
		Roots []string `json:"roots"`
		Nodes []string `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Roots) != 1 || len(got.Nodes) != 2 {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestFlagWasSet(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("write", false, "")
	fs.String("output", "", "")

	// Before parsing: nothing is set.
	if flagWasSet(fs, "write") {
		t.Error("flagWasSet(write) = true before Parse, want false")
	}

	// Parse with --write explicitly set.
	if err := fs.Parse([]string{"--write=false"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if !flagWasSet(fs, "write") {
		t.Error("flagWasSet(write) = false after explicit --write=false, want true")
	}
	if flagWasSet(fs, "output") {
		t.Error("flagWasSet(output) = true for unset flag, want false")
	}
}

func TestFlagWasSetDefault(t *testing.T) {
	// A flag registered with a non-zero default but never passed on the
	// command line must NOT be considered "set".
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("write", true, "")
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if flagWasSet(fs, "write") {
		t.Error("flagWasSet(write) = true for default-only flag, want false")
	}
}

func TestWriteFormattedJSON(t *testing.T) {
	var buf bytes.Buffer
	v := struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}{Name: "hello", N: 42}

	spec := outputSpec{Format: outputJSON}
	if err := writeFormatted(&buf, spec, v); err != nil {
		t.Fatalf("writeFormatted json: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v (output was %q)", err, buf.String())
	}
	if decoded["name"] != "hello" {
		t.Errorf("name = %v, want hello", decoded["name"])
	}
}

func TestWriteFormattedUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	spec := outputSpec{Format: "unsupported-format"}
	err := writeFormatted(&buf, spec, struct{}{})
	if err == nil {
		t.Fatal("expected error for unsupported format, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Errorf("error = %q, want to contain 'unsupported format'", err.Error())
	}
}
