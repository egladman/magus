package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestResolveOutput(t *testing.T) {
	// assertResolve checks a successful resolution's format and template.
	assertResolve := func(input string, wantFmt Format, wantTmpl string) {
		t.Run("ok/"+input, func(t *testing.T) {
			opts, err := ResolveOutput(input)
			require.NoError(t, err)
			assert.Equal(t, wantFmt, opts.Format)
			assert.Equal(t, wantTmpl, opts.Template)
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
		_, err := ResolveOutput("unknown")
		assert.Error(t, err)
	})
}

// -o template renders against the JSON-normalized value, so template field names are
// the json-tag keys (lowercase here), exactly what -o json emits - NOT the PascalCase
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
			require.NoError(t, writeTemplate(&buf, v, body))
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
	// join over a list field (now []any) - templateJoin handles it where strings.Join could not.
	assertTmpl("join helper",
		`{{range .projects}}{{.path}}: {{join .deps ","}}{{"\n"}}{{end}}`,
		"api: internal/db\nweb: \n")
	assertTmpl("upper helper",
		`{{upper .workspace}}`,
		"/TMP/WS")
}

func TestWriteTemplateBadSyntax(t *testing.T) {
	var buf bytes.Buffer
	err := writeTemplate(&buf, struct{}{}, "{{.MissingClose")
	require.Error(t, err, "expected parse error")
	assert.Contains(t, err.Error(), "parse template")
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
			require.NoError(t, writeTemplate(&buf, v, body))
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
	require.NoError(t, writeTemplateFields(&buf, types.ProjectsOutput{
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
	require.NoError(t, writeTemplateFields(&buf, outer{}))
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
	err := writeTemplateFields(&buf, "just a string")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no fields")
}

func TestWriteTemplateEnvBlocked(t *testing.T) {
	var buf bytes.Buffer
	err := writeTemplate(&buf, struct{}{}, `{{env "PATH"}}`)
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
	require.NoError(t, writeJSONL(&buf, v))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	var p0, p1 proj
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &p0))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &p1))
	assert.Equal(t, proj{Path: "api", Pack: "go"}, p0)
	assert.Equal(t, proj{Path: "web", Pack: "typescript"}, p1)
}

func TestOutputDstNoTee(t *testing.T) {
	// With no --tee set, outputDst returns os.Stdout.
	prev := global.tee
	global.tee = ""
	defer func() { global.tee = prev }()

	w, cleanup, err := outputDst()
	require.NoError(t, err)
	defer cleanup()
	assert.Equal(t, os.Stdout, w)
}

func TestOutputDstTeeWritesBoth(t *testing.T) {
	// With --tee set, outputDst returns a MultiWriter that writes to both
	// stdout and the named file.
	f, err := os.CreateTemp(t.TempDir(), "tee-*.json")
	require.NoError(t, err)
	teeTarget := f.Name()
	f.Close()

	prev := global.tee
	global.tee = teeTarget
	defer func() { global.tee = prev }()

	w, cleanup, err := outputDst()
	require.NoError(t, err)
	defer cleanup()

	v := struct{ X int }{X: 42}
	require.NoError(t, writeJSON(w, v))
	cleanup()

	got, err := os.ReadFile(teeTarget)
	require.NoError(t, err)
	assert.Contains(t, string(got), `"X": 42`)
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
		require.NoError(t, err, "outputDst call %d", i)
		v := struct{ N int }{N: i}
		require.NoError(t, writeJSON(w, v), "writeJSON call %d", i)
		cleanup()
	}

	got, err := os.ReadFile(teeTarget)
	require.NoError(t, err)
	// Both JSON objects should be present.
	assert.Contains(t, string(got), `"N": 0`)
	assert.Contains(t, string(got), `"N": 1`)
}

func TestOutputDstTeeBadPath(t *testing.T) {
	prev := global.tee
	global.tee = "/nonexistent-dir-that-should-not-exist/out.json"
	defer func() { global.tee = prev }()

	_, _, err := outputDst()
	require.Error(t, err, "expected error for bad --tee path")
	assert.Contains(t, err.Error(), "--tee", "error should mention --tee")
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
	require.NoError(t, writeJSONL(&buf, v))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 1, "expected 1 line (fallback)")
	var got struct {
		Roots []string `json:"roots"`
		Nodes []string `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &got))
	assert.Len(t, got.Roots, 1)
	assert.Len(t, got.Nodes, 2)
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

	opts := OutputOptions{Format: outputJSON}
	require.NoError(t, writeFormatted(&buf, opts, v))

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded), "output was %q", buf.String())
	assert.Equal(t, "hello", decoded["name"])
}

func TestWriteFormattedUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	opts := OutputOptions{Format: "unsupported-format"}
	err := writeFormatted(&buf, opts, struct{}{})
	require.Error(t, err, "expected error for unsupported format")
	assert.Contains(t, err.Error(), "unsupported format")
}

func TestResolveOutput_GraphFormatExtras(t *testing.T) {
	t.Parallel()
	for _, fmt := range []Format{outputDot, outputMermaid, outputTree} {
		opts, err := ResolveOutput(string(fmt), outputDot, outputMermaid, outputTree)
		assert.NoError(t, err, "ResolveOutput(%q, extras)", fmt)
		assert.Equal(t, fmt, opts.Format)
	}
}

func TestResolveOutput_RejectsGraphFormatsWithoutExtras(t *testing.T) {
	t.Parallel()
	for _, fmt := range []Format{outputDot, outputMermaid, outputTree} {
		_, err := ResolveOutput(string(fmt)) // no extra formats
		assert.Error(t, err, "ResolveOutput(%q) should fail for non-graph commands", fmt)
	}
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
