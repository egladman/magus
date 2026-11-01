package main

import (
	"bytes"
	"context"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/cmdline"
	"github.com/egladman/magus/libs/cmdline/exit"
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
		{"object lists its sorted fields", record, "this record's fields are .count, .target, .workspace "},
		{"a list has no fields to offer", []string{"a"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := renderTemplate(&buf, tc.v, "{{.Workspace}}")

			require.Error(t, err)
			if tc.want == "" {
				assert.NotContains(t, err.Error(), "record's fields")
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

// outputFormatsSuite pins docs/reference/output-formats.md: a command with a
// record renders every format, and a command without one refuses -o.
// A command's formats live in its Go, so the suite reads the package's source.
type outputFormatsSuite struct {
	suite.Suite
	funcs    map[string]*ast.FuncDecl
	parsedAt map[string]string // a FlagSet's name, or its literal prefix, to its func
}

func TestOutputFormatsSuite(t *testing.T) { suite.Run(t, new(outputFormatsSuite)) }

func (s *outputFormatsSuite) SetupSuite() {
	paths, err := filepath.Glob("*.go")
	s.Require().NoError(err)
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		s.Require().NoError(err)
		// The stub redefines selfUpdateCommand for the build that drops it.
		if bytes.HasPrefix(src, []byte("//go:build noselfupdate")) {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		s.Require().NoError(err)
		files = append(files, file)
	}
	s.load(files)
}

func (s *outputFormatsSuite) load(files []*ast.File) {
	s.funcs, s.parsedAt = map[string]*ast.FuncDecl{}, map[string]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				// reach goes by name, so a func outranks a method.
				if fn.Recv == nil || s.funcs[fn.Name.Name] == nil {
					s.funcs[fn.Name.Name] = fn
				}
				s.recordParses(fn)
			}
		}
	}
}

func (s *outputFormatsSuite) loadSource(src string) {
	file, err := parser.ParseFile(token.NewFileSet(), "synthetic.go", src, 0)
	s.Require().NoError(err)
	s.load([]*ast.File{file})
}

func (s *outputFormatsSuite) recordParses(fn *ast.FuncDecl) {
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		switch sel := call.Fun.(type) {
		case *ast.Ident:
			if sel.Name != "commandParse" {
				return true
			}
		case *ast.SelectorExpr:
			if sel.Sel.Name != "NewFlagSet" {
				return true
			}
		default:
			return true
		}
		if name, ok := leadingString(call.Args[0]); ok {
			s.parsedAt[name] = fn.Name.Name
		}
		return true
	})
}

// leadingString is expr's string literal, or the one a concatenation opens.
func leadingString(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BinaryExpr:
		return leadingString(e.X)
	case *ast.BasicLit:
		text, err := strconv.Unquote(e.Value)
		return text, err == nil
	}
	return "", false
}

// handler finds the func serving a leaf. It never guesses from a name.
// A FlagSet named the leaf wins, else its parent's switch names it.
func (s *outputFormatsSuite) handler(leaf string) (string, bool) {
	if fn, ok := servedBy[leaf]; ok {
		return fn, true
	}
	if fn, ok := s.parsedAt[leaf]; ok {
		return fn, true
	}
	parent, last, ok := cutLastWord(leaf)
	if !ok {
		return "", false
	}
	from, ok := s.handler(parent)
	if !ok {
		return "", false
	}
	for name := range s.reach(from, nil) {
		if fn, ok := s.dispatchedTo(s.funcs[name], last); ok {
			return fn, true
		}
	}
	return "", false
}

// servedBy names the funcs whose dispatch the walk cannot read. Each entry
// is a command root, or a leaf whose case label is computed.
var servedBy = map[string]string{
	"affected":         "affected",        // main routes it, no FlagSet named for it
	"describe":         "describeCommand", // root of the describe nouns
	"job":              "jobCommand",      // root, its case labels are hint calls
	"job run":          "jobRunCatalog",   // label is hint.JobRun.Leaf()
	"describe targets": "describeTargets", // its first named func is append
	"ls":               "ls",              // root of the ls nouns
	"ls job":           "lsJobs",          // the noun is plural in the func
	"ls target":        "lsTargets",       // the noun is plural in the func
	"ls targets":       "lsTargets",       // the leaf and its alias share a func
	"query":            "queryCommand",    // root of the query nouns
	"query invocation": "queryInvocation", // queryCommand picks it by flag
	"query output":     "queryOutputRef",  // queryCommand picks it by flag
	"queue":            "runQueue",        // root, the verb is chosen by assignment
	"run":              "runTarget",       // main routes it, no FlagSet named for it
	"server":           "serverCommand",   // root, its case labels are hint calls
	"session":          "sessionCommand",  // root, ls is the default verb
}

var camelBoundary = regexp.MustCompile(`[A-Z][a-z0-9]*|[a-z0-9]+`)

// labelNames reports whether a case label is word: a string literal, or a
// computed label such as hint.ServerStop.Leaf() whose name words contain it.
func labelNames(expr ast.Expr, word string) bool {
	if text, ok := leadingString(expr); ok {
		return text == word
	}
	var tokens []string
	ast.Inspect(expr, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			for _, t := range camelBoundary.FindAllString(id.Name, -1) {
				tokens = append(tokens, strings.ToLower(t))
			}
		}
		return true
	})
	want := strings.Split(word, "-")
	for i := 0; i+len(want) <= len(tokens); i++ {
		if slices.Equal(tokens[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

// dispatchedTo is the first func a case clause for word in fn names.
func (s *outputFormatsSuite) dispatchedTo(fn *ast.FuncDecl, word string) (string, bool) {
	var found string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok || found != "" {
			return found == ""
		}
		matches := slices.ContainsFunc(clause.List, func(e ast.Expr) bool {
			return labelNames(e, word)
		})
		if !matches {
			return true
		}
		for _, stmt := range clause.Body {
			ast.Inspect(stmt, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if ok && found == "" && s.funcs[id.Name] != nil && id.Name != fn.Name.Name {
					found = id.Name
				}
				return found == ""
			})
		}
		return false
	})
	return found, found != ""
}

func cutLastWord(path string) (rest, last string, ok bool) {
	i := strings.LastIndex(path, " ")
	if i < 0 {
		return "", "", false
	}
	return path[:i], path[i+1:], true
}

// reach is every func of this package fn names, directly or through another.
// It stops at stop, whose renders belong to other commands.
func (s *outputFormatsSuite) reach(fn string, stop map[string]bool) map[string]bool {
	seen := map[string]bool{fn: true}
	for queue := []string{fn}; len(queue) > 0; queue = queue[1:] {
		ast.Inspect(s.funcs[queue[0]], func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if ok && s.funcs[id.Name] != nil && !seen[id.Name] && !stop[id.Name] {
				seen[id.Name] = true
				queue = append(queue, id.Name)
			}
			return true
		})
	}
	return seen
}

// othersHandlers is every leaf's handler but the one serving leaf.
func (s *outputFormatsSuite) othersHandlers(leaf string) map[string]bool {
	mine, _ := s.handler(leaf)
	others := map[string]bool{}
	cmdline.Walk(commands.Root, func(other string, c cmdline.Command) {
		if fn, ok := s.handler(other); ok && fn != mine && len(c.Children) == 0 {
			others[fn] = true
		}
	})
	return others
}

func (s *outputFormatsSuite) TestEveryLeafRendersEveryFormatOrRefusesThem() {
	cmdline.Walk(commands.Root, func(leaf string, c cmdline.Command) {
		if len(c.Children) > 0 || commandsWithoutOutput[leaf] {
			return
		}
		fn, ok := s.handler(leaf)
		if !s.True(ok, "no commandParse or FlagSet is named %q: name one, or add it to commandsWithoutOutput", leaf) {
			return
		}
		reached := s.reach(fn, s.othersHandlers(leaf))
		s.True(reached["writeFormatted"], "%s reaches no structured printer from %s: render a record or add it to commandsWithoutOutput", leaf, fn)
		s.True(reached["emitNames"], "%s reaches no -o name printer from %s", leaf, fn)
	})
}

const dispatchSource = `package main

func thing(args []string) error {
	commandParse("thing", args, nil)
	switch args[0] {
	case "ls":
		return lister(args)
	case "rm":
		return remover(args)
	}
	return nil
}

func lister(args []string) error { return writeFormatted(nil, nil, args) }

func remover(args []string) error { return lister(args) }

func thingMv(args []string) error { return writeFormatted(nil, nil, args) }
`

func (s *outputFormatsSuite) TestHandlerIsFoundByDispatchNotByName() {
	var synthetic outputFormatsSuite
	synthetic.SetT(s.T())
	synthetic.loadSource(dispatchSource)
	fn, ok := synthetic.handler("thing rm")
	s.True(ok)
	s.Equal("remover", fn)
	_, ok = synthetic.handler("thing mv")
	s.False(ok, "thingMv is named like the leaf and nothing dispatches to it")
}

func (s *outputFormatsSuite) TestEveryServedByNamesAFunc() {
	for leaf, fn := range servedBy {
		s.NotNil(s.funcs[fn], "servedBy[%q] names %q, which is no func", leaf, fn)
	}
}

func (s *outputFormatsSuite) TestAComputedCaseLabelNamesItsWords() {
	label, err := parser.ParseExpr("hint.ServerStop.Leaf()")
	s.Require().NoError(err)
	s.True(labelNames(label, "stop"))
	s.False(labelNames(label, "top"))
	label, err = parser.ParseExpr("job.NamePrunePreserved")
	s.Require().NoError(err)
	s.True(labelNames(label, "prune-preserved"))
}

func (s *outputFormatsSuite) TestASiblingsPrinterDoesNotReachALeaf() {
	var synthetic outputFormatsSuite
	synthetic.SetT(s.T())
	synthetic.loadSource(dispatchSource)
	s.True(synthetic.reach("remover", nil)["lister"])
	s.False(synthetic.reach("remover", map[string]bool{"lister": true})["lister"])
}

func (s *outputFormatsSuite) TestEveryRefusalNamesAVerbThatExists() {
	inTree := map[string]bool{}
	cmdline.Walk(commands.Root, func(leaf string, _ cmdline.Command) { inTree[leaf] = true })
	for _, verb := range slices.Sorted(maps.Keys(commandsWithoutOutput)) {
		name := strings.Split(verb, " --")[0]
		_, parsed := s.handler(name)
		s.True(inTree[name] || parsed, "commandsWithoutOutput names %q, which nothing parses", verb)
	}
}

func (s *outputFormatsSuite) TestNoFormatSwitchFallsThroughToText() {
	for name, fn := range s.funcs {
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok || !isFormatField(sw.Tag) {
				return true
			}
			var structured, named, fallback bool
			for _, stmt := range sw.Body.List {
				clause := stmt.(*ast.CaseClause)
				fallback = fallback || clause.List == nil
				for _, expr := range clause.List {
					id, ok := expr.(*ast.Ident)
					structured = structured || ok && id.Name == "outputJSON"
					named = named || ok && id.Name == "outputName"
				}
			}
			s.False(structured && !named && !fallback, "%s switches on -o with json and no name arm or default", name)
			return true
		})
	}
}

func isFormatField(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Format"
}

func (s *outputFormatsSuite) TestAFuncThatRendersARecordAlsoHandlesName() {
	// emitRunResult's and emitExplainRecord's callers switch on -o name first.
	exempt := map[string]bool{"emitFormatted": true, "emitRecord": true, "emitRunResult": true, "emitExplainRecord": true}
	for name, fn := range s.funcs {
		var renders, names bool
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				renders = renders || id.Name == "emitFormatted" || id.Name == "writeFormatted"
				names = names || id.Name == "outputName" || id.Name == "emitRecord"
			}
			return true
		})
		s.False(renders && !names && !exempt[name], "%s renders a record and never decides -o name; use emitRecord", name)
	}
}

func (s *outputFormatsSuite) TestNoCommandRefusesAFormatByName() {
	for name, fn := range s.funcs {
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			s.False(ok && strings.Contains(lit.Value, "-o %s is not supported"), "%s refuses a format by name", name)
			return true
		})
	}
}

func (s *outputFormatsSuite) TestOutputBeforeTheVerbIsRefused() {
	err := refuseOutputOnPath("json", []string{"server", "start"})
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "magus server start: -o is not supported")
}

func (s *outputFormatsSuite) TestOutputOnAVerbWithARecordPasses() {
	s.NoError(refuseOutputOnPath("json", []string{"--root", ".", "where", "x"}))
}

func (s *outputFormatsSuite) TestNoOutputPasses() {
	s.NoError(refuseOutputOnPath("", []string{"server", "start"}))
}

func (s *outputFormatsSuite) TestAVerbThatReportsAnOutcomeIsNotRefused() {
	s.NoError(refuseOutputOnPath("json", []string{"server", "stop"}))
	s.NoError(refuseOutputOnPath("json", []string{"config", "set", "key=a,value=b"}))
}

func (s *outputFormatsSuite) TestEveryOutcomeRecordRendersEveryFormat() {
	records := map[string]any{
		"config set":  configSetOutput{Path: "magus.yaml", Key: "cache.mode", Value: "write"},
		"server stop": serverStopOutput{Stopped: true, PID: 7, Address: "unix:///s"},
		"job run":     jobRunOutput{Job: "sync-graph", Outcome: "no-server"},
		"self place":  selfPlaceOutput{Commit: "abc", BuildDate: "d", Path: "/bin/magus"},
	}
	for verb, record := range records {
		for _, format := range []string{"json", "yaml", "jsonl", "name", "template={{.}}"} {
			opts, err := output.Parse(format)
			s.Require().NoError(err, verb)
			got := captureStdout(s.T(), func() {
				s.Require().NoError(emitRecord(opts, record, []string{verb}), "%s -o %s", verb, format)
			})
			s.NotEmpty(got, "%s -o %s", verb, format)
		}
	}
}

func (s *outputFormatsSuite) TestConfigSetReportsTheKeyItWrote() {
	s.T().Cleanup(snapshotGlobals())
	s.T().Chdir(s.T().TempDir())
	global.output = "json"
	got := captureStdout(s.T(), func() {
		s.Require().NoError(runConfigSet(context.Background(), []string{"key=cache.write.enabled,value=true"}))
	})
	s.Contains(got, `"key": "cache.write.enabled"`)
	s.Contains(got, `"path": "magus.yaml"`)
}

func (s *outputFormatsSuite) TestJobRunReportsWhyNothingWasSubmitted() {
	s.T().Cleanup(snapshotGlobals())
	s.T().Chdir(s.T().TempDir())
	globalCfg.Server.Address = filepath.Join(s.T().TempDir(), "absent.sock")
	got := captureStdout(s.T(), func() {
		s.Require().NoError(jobRunCatalog(context.Background(), []string{"-o", "json", "rotate-logs"}))
	})
	s.Contains(got, `"outcome": "no-server"`)
	s.Contains(got, `"job": "rotate-logs"`)
}

func (s *outputFormatsSuite) TestCommandPathStopsAtTheDeepestChild() {
	s.Equal("config cache prune", commandPath([]string{"config", "--x", "cache", "prune", "a"}))
	s.Equal("refs", commandPath([]string{"refs", "--", "config"}))
}

func (s *outputFormatsSuite) TestEmitRecordRendersEveryFormat() {
	record := whereOutput{Workspace: "/w", Count: 1, Matches: []whereMatch{{Path: "a"}}}
	render := func(format string) string {
		opts, err := output.Parse(format)
		s.Require().NoError(err)
		return captureStdout(s.T(), func() {
			s.Require().NoError(emitRecord(opts, record, []string{"a"}))
		})
	}
	s.Contains(render("json"), `"workspace": "/w"`)
	s.Contains(render("yaml"), "workspace: /w")
	s.Contains(render("jsonl"), `"path":"a"`)
	s.Equal("a\n", render("name"))
	s.Equal("yes a", render(`template={{if gt .count 0.0}}yes{{end}}{{range .matches}} {{.path}}{{end}}`))
}

func (s *outputFormatsSuite) TestHelpPrintsGroupsAndExamplesFromTheTree() {
	var buf bytes.Buffer
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	fs.SetOutput(&buf)
	flaggen.BindRefs(fs)
	printOwnDefaults(fs)
	got := buf.String()
	s.Contains(got, "Every site: magus refs <symbol> --occurrences [<path>...]\n  --limit int\n")
	s.Contains(got, "Search text with no index: magus refs --text <pattern> [<path>...]")
	s.Contains(got, "Examples:\n  magus refs Open\n")
	s.NotContains(got, "--file")
}

func (s *outputFormatsSuite) TestHelpPointsAtTheReference() {
	s.Contains(outputFormatHelp, "docs/reference/output-formats.md")
	_, err := os.Stat("../../docs/reference/output-formats.md")
	s.NoError(err)
}
