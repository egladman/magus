package godecl

import (
	"go/ast"
	"go/format"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseSrc writes src to a temp .go file and parses it, so each test states the
// declaration it is about inline instead of sharing a fixture nobody can read
// without scrolling.
func parseSrc(t *testing.T, src string) *ast.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))
	f, err := Parse(path)
	require.NoError(t, err)
	return f
}

func TestTag(t *testing.T) {
	raw := "`json:\"size_mb,omitempty\" yaml:\"size_mb\" validate:\"gte=0\"`"
	assert.Equal(t, "size_mb", Tag(raw, "json"), "options are stripped")
	assert.Equal(t, "size_mb,omitempty", TagRaw(raw, "json"), "TagRaw keeps them")
	assert.Equal(t, "gte=0", Tag(raw, "validate"))
	assert.Empty(t, Tag(raw, "absent"))
	assert.Empty(t, Tag("", "json"), "an empty tag is not an error")
}

func TestDocLineStripsTheDeclaredName(t *testing.T) {
	f := parseSrc(t, `package p
// Dir overrides the default cache location.
// A second line is dropped.
var Dir string
`)
	decl := f.Decls[0].(*ast.GenDecl)
	assert.Equal(t, "overrides the default cache location.", DocLine(decl.Doc, "Dir"))
	assert.Empty(t, DocLine(nil, "Dir"), "no doc comment is not an error")
}

func TestSliceOfStructs(t *testing.T) {
	f := parseSrc(t, `package p
type sc struct{ Name, Short string }

var subcommands = []sc{
	{Name: "ls", Short: "list projects"},
	// a comment between entries must not matter
	{Name: "run", Short: "run a target"},
	{Name: "quiet"},
}
var other = []sc{{Name: "ignored"}}
`)
	got := SliceOfStructs(f, "subcommands")
	require.Len(t, got, 3)
	assert.Equal(t, StructLiteral{"Name": "ls", "Short": "list projects"}, got[0])
	assert.Equal(t, "run", got[1]["Name"], "declaration order is preserved")
	assert.Equal(t, StructLiteral{"Name": "quiet"}, got[2], "an omitted field is simply absent")

	assert.Empty(t, SliceOfStructs(f, "nosuchvar"), "a missing declaration yields nothing, not a panic")
}

// TestFlagNamesReadsEveryFunction is the regression this package exists for: the
// scanner it replaces read one named function, so a flag bound in a sibling was
// reported as nonexistent.
func TestFlagNamesReadsEveryFunction(t *testing.T) {
	f := parseSrc(t, `package p

func first() {
	fs.Bool("dry-run", false, "")
	fs.String("base", "", "")
}

func second() {
	fs.Int("max-shards", 0, "")
	var dst string
	fs.StringVar(&dst, "target", "test", "")
	fs.DurationVar(&d, "timeout", 0, "")
}

func notAFlagSet() {
	slog.Bool("attr", true)
	other.String("nope", "", "")
}
`)
	got := FlagNames(f)
	assert.ElementsMatch(t,
		[]string{"dry-run", "base", "max-shards", "target", "timeout"}, got,
		"flags from every function, and only from an fs receiver")
	assert.NotContains(t, got, "attr", "slog.Bool is not a flag binding")
	assert.NotContains(t, got, "nope", "a non-fs receiver is not a flag binding")
}

// TestFlagNamesInScopesToOneFunction is the distinction that made a shared extractor
// worth it: a file can hold several commands, so "what this FILE exposes" and "what
// this COMMAND binds" are different questions with different right answers.
func TestFlagNamesInScopesToOneFunction(t *testing.T) {
	f := parseSrc(t, `package p

func parent() { fs.Bool("dry-run", false, "") }

func subMode() { fs.Int("max-shards", 0, "") }
`)
	assert.Equal(t, []string{"dry-run"}, FlagNamesIn(f, "parent"),
		"a sibling function's flags must not leak into the parent command's set")
	assert.Equal(t, []string{"max-shards"}, FlagNamesIn(f, "subMode"))
	assert.ElementsMatch(t, []string{"dry-run", "max-shards"}, FlagNames(f),
		"the whole-file scope still sees both")
	assert.Nil(t, FlagNamesIn(f, "nosuchfunc"), "an absent function yields nothing, not a panic")
}

// TestDocLineHandlesBlockCommentsAndBareNames covers the two doc shapes the
// sentence-ish reader has to survive. A /* */ comment reaches DocLine with a
// different prefix than //, and a doc whose whole first line IS the declared name
// must yield empty rather than the name back - a flag usage string reading
// "CacheDir" tells the user nothing they did not just type.
func TestDocLineHandlesBlockCommentsAndBareNames(t *testing.T) {
	f := parseSrc(t, `package p

/* CacheDir overrides the cache location. */
var CacheDir string

// Verbose
var Verbose bool

// Quiet suppresses progress.
// Second lines are dropped.
var Quiet bool
`)
	docOf := func(name string) string {
		t.Helper()
		var got string
		ast.Inspect(f, func(n ast.Node) bool {
			gd, ok := n.(*ast.GenDecl)
			if !ok || len(gd.Specs) == 0 {
				return true
			}
			vs, ok := gd.Specs[0].(*ast.ValueSpec)
			if !ok || len(vs.Names) == 0 || vs.Names[0].Name != name {
				return true
			}
			got = DocLine(gd.Doc, name)
			return false
		})
		return got
	}

	assert.Equal(t, "overrides the cache location. */", docOf("CacheDir"),
		"a block comment is read; the trailing delimiter is not stripped today")
	assert.Empty(t, docOf("Verbose"), "a doc that is only the declared name has nothing left to say")
	assert.Equal(t, "suppresses progress.", docOf("Quiet"), "only the first line is taken")
	assert.Empty(t, DocLine(nil, "Anything"), "no doc comment at all")
}

// TestSliceOfStructsIgnoresWhatItCannotRead pins the reader's tolerance. A
// declaration table gets edited, and an entry the reader cannot make sense of must
// be SKIPPED rather than crashing the generator or, worse, emitting a half-read
// entry: a subcommand list missing one name still builds, where a panic mid-generate
// leaves the tree half-written.
func TestSliceOfStructsIgnoresWhatItCannotRead(t *testing.T) {
	f := parseSrc(t, `package p

type cmd struct {
	Name  string
	Short string
	Hits  int
}

var table = []cmd{
	{Name: "run", Short: "run a target"},
	"not-a-composite",
	{Name: "ls", Hits: 3},
	{},
}
`)
	got := SliceOfStructs(f, "table")
	assert.Equal(t, []StructLiteral{
		{"Name": "run", "Short": "run a target"},
		// Hits is an int, so only the string field survives - all a declaration
		// table's generator reads.
		{"Name": "ls"},
	}, got, "a non-composite element and an all-empty entry are both skipped")
}

// TestSliceOfStructsEmptyAndMissing covers the two "nothing to read" answers,
// which must be indistinguishable to a caller: an empty literal and no such
// declaration both mean "this table contributes no entries".
func TestSliceOfStructsEmptyAndMissing(t *testing.T) {
	f := parseSrc(t, `package p

type cmd struct{ Name string }

var table = []cmd{}
`)
	assert.Empty(t, SliceOfStructs(f, "table"), "an empty literal yields no entries")
	assert.Empty(t, SliceOfStructs(f, "absent"), "an undeclared name yields no entries")
}

// TestSliceOfStructsReadsAFunctionLocalTable asserts the whole-file walk is
// INTENDED, not an accident of using ast.Inspect. A table declared inside a
// function is as much a declaration table as a package-level one, and a reader that
// silently skipped it would report an incomplete list - the failure mode this
// package was created to end.
func TestSliceOfStructsReadsAFunctionLocalTable(t *testing.T) {
	f := parseSrc(t, `package p

type cmd struct{ Name string }

func build() []cmd {
	var table = []cmd{{Name: "inner"}}
	return table
}
`)
	assert.Equal(t, []StructLiteral{{"Name": "inner"}}, SliceOfStructs(f, "table"),
		"package scope is not a requirement; the walk covers the whole file on purpose")
}

// TestFlagNamesSkipsANonStringFirstArgument covers the *Var and Var/Func forms,
// where the destination comes first and the name is the first STRING argument. A
// reader taking Args[0] blindly would record the variable's name, or nothing.
func TestFlagNamesSkipsANonStringFirstArgument(t *testing.T) {
	f := parseSrc(t, `package p

import "flag"

func bind(fs *flag.FlagSet) {
	var custom value
	fs.Var(&custom, "custom", "a custom flag")
	var n int
	fs.IntVar(&n, "count", 0, "how many")
	fs.Func("callback", "usage", func(string) error { return nil })
}
`)
	assert.Equal(t, []string{"custom", "count", "callback"}, FlagNames(f),
		"the name is the first string argument, not the first argument")
}

// TestFlagNamesFalsePositiveOnAShadowedReceiver pins the documented limitation the
// package doc admits: the receiver is matched by SPELLING (`fs`), not by type,
// because checking the type would mean resolving imports. A local named fs that is
// not a FlagSet therefore reports flags it does not bind.
//
// This is the right trade for the question FlagNames answers - "what flags does
// this file make reachable", asked of magus's own command files, where fs is a
// FlagSet by convention everywhere. The test exists so the limitation is a choice
// on record rather than a surprise, since the alternative (type resolution) is what
// made the three hand-rolled scanners this package replaced so unreliable.
func TestFlagNamesFalsePositiveOnAShadowedReceiver(t *testing.T) {
	f := parseSrc(t, `package p

func notAFlagSet() {
	fs := somethingElse{}
	fs.String("looks-like-a-flag", "", "")
}
`)
	assert.Equal(t, []string{"looks-like-a-flag"}, FlagNames(f),
		"matched on the receiver's spelling; a shadowed fs is a known false positive")
}

// TestReadersAreStableUnderGofmt is the property the AST approach was chosen FOR,
// and nothing proved it. Reading a declaration from text means a reformat, a
// reorder or a new comment changes the answer; reading it from the AST means it
// cannot. Assert that directly rather than adding more single cases.
func TestReadersAreStableUnderGofmt(t *testing.T) {
	// Deliberately ugly: inconsistent indentation, a comment mid-table, and the
	// trailing-comma/brace layout gofmt will rewrite.
	ugly := `package p

import "flag"

type cmd struct{ Name string; Short string }

var table = []cmd{
{Name:"run",Short:"run a target"},
    // a comment inside the table
        {Name:"ls",Short:"list"},
}

// Verbose turns on more output.
var Verbose bool

func bind(fs *flag.FlagSet){
fs.Bool("verbose",false,"")
    fs.StringVar(nil,"root","","")
}
`
	pretty, err := format.Source([]byte(ugly))
	require.NoError(t, err)
	require.NotEqual(t, ugly, string(pretty), "the fixture must actually be reformatted, or this proves nothing")

	before, after := parseSrc(t, ugly), parseSrc(t, string(pretty))

	assert.Equal(t, SliceOfStructs(before, "table"), SliceOfStructs(after, "table"),
		"a reformatted declaration table reads identically")
	assert.Equal(t, FlagNames(before), FlagNames(after),
		"reformatting does not move a flag binding")
	assert.Equal(t, FlagNamesIn(before, "bind"), FlagNamesIn(after, "bind"),
		"nor does it change which function binds it")
}

// TestDocLineSkipsEmptyCommentLines covers the two remaining doc shapes: a comment
// group that opens with a bare "//" spacer line (the reader must look past it rather
// than return empty), and one that is nothing BUT spacers (which has genuinely
// nothing to report).
func TestDocLineSkipsEmptyCommentLines(t *testing.T) {
	spaced := &ast.CommentGroup{List: []*ast.Comment{
		{Text: "//"},
		{Text: "// Quiet suppresses progress."},
	}}
	assert.Equal(t, "suppresses progress.", DocLine(spaced, "Quiet"),
		"a leading spacer line is skipped, not treated as the doc")

	blank := &ast.CommentGroup{List: []*ast.Comment{{Text: "//"}, {Text: "//"}}}
	assert.Empty(t, DocLine(blank, "Quiet"), "a doc of only spacers reports nothing")
}

// TestSliceOfStructsIgnoresNonSliceAndNonIdentKeys covers the two remaining
// skip paths: a declaration bound to something that is not a composite literal at
// all, and an entry whose key is not a plain field name. Both must be ignored
// rather than crash - the reader runs over hand-edited tables.
func TestSliceOfStructsIgnoresNonSliceAndNonIdentKeys(t *testing.T) {
	f := parseSrc(t, `package p

var table = buildTable()

type cmd struct{ Name string }

var other = []cmd{{Name: "kept"}}
`)
	assert.Empty(t, SliceOfStructs(f, "table"),
		"a call expression is not a declaration table; skip it rather than panic")
	assert.Equal(t, []StructLiteral{{"Name": "kept"}}, SliceOfStructs(f, "other"),
		"and skipping one declaration does not abandon the walk")
}

// TestFlagNamesIgnoresNonBinderCalls covers the receiver/method filter: a call on
// fs that is not a flag binder (fs.Parse, fs.PrintDefaults) must not be read as a
// flag, or every FlagSet would report a phantom flag named after its own arguments.
func TestFlagNamesIgnoresNonBinderCalls(t *testing.T) {
	f := parseSrc(t, `package p

import "flag"

func bind(fs *flag.FlagSet, args []string) {
	fs.Bool("real", false, "")
	fs.Parse(args)
	fs.PrintDefaults()
	fs.Init("name", flag.ContinueOnError)
}
`)
	assert.Equal(t, []string{"real"}, FlagNames(f),
		"only the flag-binding methods name a flag; fs.Init takes a string and is not one")
}

// TestSliceOfStructsDropsAPositionalEntry pins the last skip path, and it is the
// one worth reading twice. The reader maps FIELD NAMES to values, so an entry
// written positionally - `{"run", "run a target"}`, legal Go - carries no names to
// map and is dropped ENTIRELY, not partially.
//
// That is a silent loss: a generator reading the subcommand table would emit a list
// missing that command, with nothing to say so. It is pinned rather than fixed
// because fixing it needs the struct's field order, which means resolving the type -
// and every table in this repo is written with field names. If a positional entry
// ever appears, this test is the note explaining why the generated output was short.
func TestSliceOfStructsDropsAPositionalEntry(t *testing.T) {
	f := parseSrc(t, `package p

type cmd struct {
	Name  string
	Short string
}

var table = []cmd{
	{Name: "run", Short: "run a target"},
	{"ls", "list projects"},
}
`)
	assert.Equal(t, []StructLiteral{{"Name": "run", "Short": "run a target"}},
		SliceOfStructs(f, "table"),
		"a positional entry has no field names to map, so it is dropped whole")
}
