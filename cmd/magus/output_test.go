package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/cmdline/output"
	"github.com/egladman/magus/types"
)

func renderTemplate(w io.Writer, v any, body string) error {
	return writeFormatted(w, output.Options{Format: outputTemplate, Template: body}, v)
}

func renderJSONL(w io.Writer, v any) error {
	return writeFormatted(w, output.Options{Format: outputJSONL}, v)
}

func TestParseOutput(t *testing.T) {
	// assertResolve checks a successful resolution's format and template.
	assertResolve := func(input string, wantFmt output.Format, wantTmpl string) {
		t.Run("ok/"+input, func(t *testing.T) {
			opts, err := output.Parse(input)
			require.NoError(t, err)
			assert.Equal(t, output.Options{Format: wantFmt, Template: wantTmpl}, opts)
		})
	}

	assertResolve("", outputText, "")
	assertResolve("json", outputJSON, "")
	assertResolve("yaml", outputYAML, "")
	assertResolve("jsonl", outputJSONL, "")
	assertResolve("name", outputName, "")
	assertResolve("template={{.path}}", outputTemplate, "{{.path}}")
	assertResolve(
		`template={{range .projects}}{{.path}}={{.spell}}{{"\n"}}{{end}}`, outputTemplate,
		`{{range .projects}}{{.path}}={{.spell}}{{"\n"}}{{end}}`,
	)
	// Bare "-o template" (and an empty body) resolve to the template format with no
	// body: that lists the output's fields rather than erroring.
	assertResolve("template", outputTemplate, "")
	assertResolve("template=", outputTemplate, "")

	t.Run("err/unknown", func(t *testing.T) {
		_, err := output.Parse("unknown")
		assert.Error(t, err)
	})
}

// -o template renders against the JSON-normalized value, so template field names are
// the json-tag keys (lowercase here), exactly what -o json emits, NOT the PascalCase
// Go fields. The fixtures carry json tags to exercise that contract.
func TestWriteTemplate(t *testing.T) {
	type project struct {
		Path  string   `json:"path"`
		Spell string   `json:"spell"`
		Deps  []string `json:"deps"`
	}
	v := struct {
		Workspace string    `json:"workspace"`
		Count     int       `json:"count"`
		Projects  []project `json:"projects"`
	}{
		Workspace: "/tmp/ws",
		Count:     2,
		Projects: []project{
			{Path: "api", Spell: "go", Deps: []string{"internal/db"}},
			{Path: "web", Spell: "typescript"},
		},
	}

	assertTmpl := func(name, body, want string) {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, renderTemplate(&buf, v, body))
			assert.Equal(t, want, buf.String())
		})
	}

	// count is an int in Go but arrives as float64 after json-normalization;
	// text/template prints it without a fractional part.
	assertTmpl("simple field",
		"{{.workspace}} ({{.count}})",
		"/tmp/ws (2)")
	assertTmpl("range with newline",
		`{{range .projects}}{{.path}}={{.spell}}{{"\n"}}{{end}}`,
		"api=go\nweb=typescript\n")
	// join over a list field (now []any): templateJoin handles it where strings.Join could not.
	assertTmpl("join helper",
		`{{range .projects}}{{.path}}: {{join .deps ","}}{{"\n"}}{{end}}`,
		"api: internal/db\nweb: \n")
	assertTmpl("upper helper",
		`{{upper .workspace}}`,
		"/TMP/WS")
}

func TestWriteTemplateBadSyntax(t *testing.T) {
	var buf bytes.Buffer
	err := renderTemplate(&buf, struct{}{}, "{{.MissingClose")
	require.Error(t, err, "expected parse error")
	assert.Contains(t, err.Error(), "parse template")
}

func TestWriteTemplateMissingFieldListsRecordFields(t *testing.T) {
	record := struct {
		Workspace string `json:"workspace"`
		Count     int    `json:"count"`
		Target    string `json:"target"`
	}{}

	cases := []struct {
		name string
		v    any
		want string
	}{
		{"object lists its sorted fields", record, "this record's top-level fields are count, target, workspace: "},
		{"a list has no fields to offer", []string{"a"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := renderTemplate(&buf, tc.v, "{{.Workspace}}")

			require.Error(t, err)
			if tc.want == "" {
				assert.NotContains(t, err.Error(), "top-level fields")
			} else {
				assert.Contains(t, err.Error(), tc.want)
			}
			assert.Contains(t, err.Error(), "bare -o template")
		})
	}
}

func TestWriteTemplateSprigHelpers(t *testing.T) {
	type item struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	v := struct {
		Items []item `json:"items"`
		Count int    `json:"count"`
	}{
		Items: []item{
			{Name: "foo-bar", Tags: []string{"a", "b"}},
			{Name: "baz", Tags: nil},
		},
		Count: 2,
	}

	assertTmpl := func(name, body, want string) {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, renderTemplate(&buf, v, body))
			assert.Equal(t, want, buf.String())
		})
	}

	assertTmpl("quote helper",
		`{{range .items}}{{quote .name}} {{end}}`,
		`"foo-bar" "baz" `)
	// toJson renders the json-normalized element, so its keys are the json tags (lowercase).
	assertTmpl("toJson helper",
		`{{index .items 0 | toJson}}`,
		`{"name":"foo-bar","tags":["a","b"]}`)
	assertTmpl("default helper",
		`{{$zero := ""}}{{default "fallback" $zero}}`,
		"fallback")
	assertTmpl("camelcase helper",
		`{{camelcase "foo-bar"}}`,
		"FooBar")
	assertTmpl("kebabcase helper",
		`{{kebabcase "FooBar"}}`,
		"foo-bar")
	assertTmpl("base path helper",
		`{{base "a/b/c"}}`,
		"c")
	// count arrives as float64; compare numerics by coercing with sprig's int
	// (the real-world pattern under the json-shape model).
	assertTmpl("ternary helper",
		`{{ternary "yes" "no" (eq (int .count) 2)}}`,
		"yes")
	assertTmpl("sortAlpha helper",
		`{{$s := list "c" "a" "b" | sortAlpha}}{{join $s ","}}`,
		"a,b,c")
}

// Bare "-o template" lists a value's fields (json keys) by reflecting its type, and
// drills into referenced struct types.
func TestWriteTemplateFields(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.Fields(&buf, types.ProjectsOutput{
		Projects: []types.ProjectEntry{{Path: "api"}},
	}))
	out := buf.String()
	// Root fields (json keys, not Go names) and the drilled-in referenced type.
	assert.Contains(t, out, "projects")
	assert.Contains(t, out, "[]ProjectEntry")
	assert.Contains(t, out, "ProjectEntry:")
	assert.Contains(t, out, "path")
	assert.NotContains(t, out, "Projects ", "should list json keys, not Go field names")
}

// Reflection makes the listing universal: any struct works (not a curated set), so a
// type with no registration still lists its json-key fields, skips json:"-", and
// recurses into referenced structs.
func TestWriteTemplateFields_arbitraryStruct(t *testing.T) {
	type inner struct {
		Slug string `json:"slug"`
	}
	type outer struct {
		Name    string   `json:"name"`
		Tags    []string `json:"tags"`
		Skipped int      `json:"-"`
		Inners  []inner  `json:"inners"`
	}
	var buf bytes.Buffer
	require.NoError(t, output.Fields(&buf, outer{}))
	out := buf.String()
	assert.Contains(t, out, "name")
	assert.Contains(t, out, "tags")
	assert.Contains(t, out, "[]string")
	assert.Contains(t, out, "inners")
	assert.Contains(t, out, "inner:", "recurses into the referenced struct")
	assert.Contains(t, out, "slug")
	assert.NotContains(t, out, "Skipped", `json:"-" fields are omitted`)
}

// A non-struct value has no fields to list.
func TestWriteTemplateFields_nonStruct(t *testing.T) {
	var buf bytes.Buffer
	err := output.Fields(&buf, "just a string")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no fields")
}

func TestWriteTemplateEnvBlocked(t *testing.T) {
	var buf bytes.Buffer
	err := renderTemplate(&buf, struct{}{}, `{{env "PATH"}}`)
	require.Error(t, err, "expected error for blocked 'env' function")
	assert.Contains(t, err.Error(), "parse template")
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
	require.NoError(t, renderJSONL(&buf, v))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	var p0, p1 proj
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &p0))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &p1))
	assert.Equal(t, proj{Path: "api", Pack: "go"}, p0)
	assert.Equal(t, proj{Path: "web", Pack: "typescript"}, p1)
}

func TestOutputDestinationNoTee(t *testing.T) {
	// With no --tee set, outputDestination returns the writer it was handed.
	prev := global.tee
	global.tee = ""
	defer func() { global.tee = prev }()

	w, cleanup, err := outputDestination(os.Stdout)
	require.NoError(t, err)
	defer cleanup()
	assert.Equal(t, os.Stdout, w)
}

func TestOutputDestinationTeeWritesBoth(t *testing.T) {
	// With --tee set, outputDestination returns a MultiWriter that writes to both
	// stdout and the named file.
	f, err := os.CreateTemp(t.TempDir(), "tee-*.json")
	require.NoError(t, err)
	teeTarget := f.Name()
	f.Close()

	prev := global.tee
	global.tee = teeTarget
	defer func() { global.tee = prev }()

	w, cleanup, err := outputDestination(os.Stdout)
	require.NoError(t, err)
	defer cleanup()

	v := struct {
		X int `json:"X"`
	}{X: 42}
	require.NoError(t, writeFormatted(w, output.Options{Format: outputJSON}, v))
	cleanup()

	got, err := os.ReadFile(teeTarget)
	require.NoError(t, err)
	assert.Contains(t, string(got), `"X": 42`)
}

func TestOutputDestinationTeeAppends(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	// Repeated calls to outputDestination with the same tee path append (not overwrite).
	dir := t.TempDir()
	teeTarget := dir + "/out.json"

	for i := range 2 {
		prev := global.tee
		global.tee = teeTarget
		w, cleanup, err := outputDestination(os.Stdout)
		global.tee = prev
		require.NoError(t, err, "outputDestination call %d", i)
		v := struct {
			N int `json:"N"`
		}{N: i}
		require.NoError(t, writeFormatted(w, output.Options{Format: outputJSON}, v), "write call %d", i)
		cleanup()
	}

	got, err := os.ReadFile(teeTarget)
	require.NoError(t, err)
	// Both JSON objects should be present.
	assert.Contains(t, string(got), `"N": 0`)
	assert.Contains(t, string(got), `"N": 1`)
}

func TestOutputDestinationTeeBadPath(t *testing.T) {
	prev := global.tee
	global.tee = "/nonexistent-dir-that-should-not-exist/out.json"
	defer func() { global.tee = prev }()

	_, _, err := outputDestination(os.Stdout)
	require.Error(t, err, "expected error for bad --tee path")
	assert.Contains(t, err.Error(), "--tee", "error should mention --tee")
}

// Several collections and no declared primary is an ERROR naming the candidates. It used
// to emit the whole object as one line, which turned -o jsonl into -o json at exit 0 the
// day a type grew a second list.
func TestWriteJSONLAmbiguousIsAnError(t *testing.T) {
	v := struct {
		Roots []string `json:"roots"`
		Nodes []string `json:"nodes"`
	}{
		Roots: []string{"a"},
		Nodes: []string{"b", "c"},
	}

	var buf bytes.Buffer
	err := renderJSONL(&buf, v)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "roots", "the error names the candidate fields")
	assert.Contains(t, err.Error(), "nodes")
	assert.Contains(t, err.Error(), `jsonl:"primary"`)
	assert.Empty(t, buf.String(), "nothing is emitted when the stream is ambiguous")
}

// A declared primary settles the ambiguity: that field streams, the rest are dropped.
func TestWriteJSONLDeclaredPrimary(t *testing.T) {
	v := struct {
		Roots []string `json:"roots"`
		Nodes []string `json:"nodes" jsonl:"primary"`
	}{
		Roots: []string{"a"},
		Nodes: []string{"b", "c"},
	}

	var buf bytes.Buffer
	require.NoError(t, renderJSONL(&buf, v))
	assert.Equal(t, "\"b\"\n\"c\"\n", buf.String())
}

// A value that IS the collection streams its elements rather than emitting the whole
// array as one line.
func TestWriteJSONLTopLevelSlice(t *testing.T) {
	type proj struct {
		Path string `json:"path"`
	}
	var buf bytes.Buffer
	require.NoError(t, renderJSONL(&buf, []proj{{Path: "api"}, {Path: "web"}}))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	var p0 proj
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &p0))
	assert.Equal(t, proj{Path: "api"}, p0)
}

// A record with no collection at all is one line: there is nothing to stream, and that
// is not an ambiguity.
func TestWriteJSONLSingleRecord(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, renderJSONL(&buf, struct {
		Name string `json:"name"`
	}{Name: "api"}))
	assert.Equal(t, 1, strings.Count(buf.String(), "\n"))
}

// Bare -o template on a command whose whole output is a LIST lists the element's fields;
// refusing there left nothing to read the template field names off.
func TestWriteTemplateFieldsSliceElement(t *testing.T) {
	type entry struct {
		Path string `json:"path"`
	}
	var buf bytes.Buffer
	require.NoError(t, output.Fields(&buf, []entry{{Path: "api"}}))
	out := buf.String()
	assert.Contains(t, out, "entry:")
	assert.Contains(t, out, "path")
}

func TestFlagWasSet(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("write", false, "")
	fs.String("output", "", "")

	// Before parsing: nothing is set.
	assert.False(t, flagWasSet(fs, "write"), "flagWasSet(write) should be false before Parse")

	// Parse with --write explicitly set.
	require.NoError(t, fs.Parse([]string{"--write=false"}))

	assert.True(t, flagWasSet(fs, "write"), "flagWasSet(write) should be true after explicit --write=false")
	assert.False(t, flagWasSet(fs, "output"), "flagWasSet(output) should be false for unset flag")
}

func TestFlagWasSetDefault(t *testing.T) {
	// A flag registered with a non-zero default but never passed on the
	// command line must NOT be considered "set".
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("write", true, "")
	require.NoError(t, fs.Parse(nil))
	assert.False(t, flagWasSet(fs, "write"), "flagWasSet(write) should be false for default-only flag")
}

func TestWriteFormattedJSON(t *testing.T) {
	var buf bytes.Buffer
	v := struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}{Name: "hello", N: 42}

	opts := output.Options{Format: outputJSON}
	require.NoError(t, writeFormatted(&buf, opts, v))

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded), "output was %q", buf.String())
	assert.Equal(t, "hello", decoded["name"])
}

func TestWriteFormattedUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	opts := output.Options{Format: "unsupported-format"}
	err := writeFormatted(&buf, opts, struct{}{})
	require.Error(t, err, "expected error for unsupported format")
	assert.Contains(t, err.Error(), "does not render")
}

func TestResolveOutput_GraphFormatExtras(t *testing.T) {
	t.Parallel()
	for _, fmt := range []output.Format{outputDot, outputTree} {
		opts, err := output.Parse(string(fmt), outputDot, outputTree)
		require.NoError(t, err, "output.Parse(%q, extras)", fmt)
		assert.Equal(t, fmt, opts.Format)
	}
}

func TestResolveOutput_RejectsGraphFormatsWithoutExtras(t *testing.T) {
	t.Parallel()
	for _, fmt := range []output.Format{outputDot, outputTree} {
		_, err := output.Parse(string(fmt)) // no extra formats
		assert.Error(t, err, "output.Parse(%q) should fail for non-graph commands", fmt)
	}
}

func TestResolveOutput_MermaidIsUnknown(t *testing.T) {
	t.Parallel()
	_, err := output.Parse("mermaid", outputDot, outputTree, outputMarkdown, outputGraphML)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown output format "mermaid"`)
}

func TestParseTarget(t *testing.T) {
	t.Parallel()
	assertTarget := func(input, wantPack, wantName string) {
		t.Run(input, func(t *testing.T) {
			pack, target := parseTarget(input)
			assert.Equal(t, wantPack, pack)
			assert.Equal(t, wantName, target)
		})
	}

	assertTarget("build", "", "build")
	assertTarget("lint", "", "lint")
	assertTarget("typescript::lint", "typescript", "lint")
	assertTarget("go::test", "go", "test")
	assertTarget("rust::build", "rust", "build")
	assertTarget("::lint", "", "lint") // empty spell — treated as no filter
}

func TestOutputDestinationSilentTee(t *testing.T) {
	const record = "{\n  \"N\": 7\n}\n"
	cases := []struct {
		name       string
		silent     bool
		wantStdout string
	}{
		{"silent leaves stdout empty", true, ""},
		{"not silent mirrors to stdout", false, record},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(snapshotGlobals())
			teeTarget := t.TempDir() + "/out.json"
			global.tee = teeTarget
			global.silent = tc.silent

			stdout := captureStdout(t, func() {
				require.NoError(t, emitFormatted(output.Options{Format: outputJSON}, struct {
					N int `json:"N"`
				}{N: 7}))
			})

			assert.Equal(t, tc.wantStdout, stdout)
			got, err := os.ReadFile(teeTarget)
			require.NoError(t, err)
			assert.Equal(t, record, string(got), "the tee file always holds the record")
		})
	}
}
