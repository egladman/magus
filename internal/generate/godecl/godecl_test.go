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

// parseSource writes src to a temp .go file and parses it, so each test states the
// declaration it is about inline instead of sharing a fixture nobody can read
// without scrolling.
func parseSource(t *testing.T, src string) *ast.File {
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

// TestFlagNamesReadsEveryFunction is the regression this package exists for: the
// scanner it replaces read one named function, so a flag bound in a sibling was
// reported as nonexistent.
func TestFlagNamesReadsEveryFunction(t *testing.T) {
	f := parseSource(t, `package p

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

// TestFlagNamesSkipsANonStringFirstArgument covers the *Var and Var/Func forms,
// where the destination comes first and the name is the first STRING argument. A
// reader taking Args[0] blindly would record the variable's name, or nothing.
func TestFlagNamesSkipsANonStringFirstArgument(t *testing.T) {
	f := parseSource(t, `package p

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
// This is the right trade for the question FlagNames answers: "what flags does
// this file make reachable", asked of magus's own command files, where fs is a
// FlagSet by convention everywhere. The test exists so the limitation is a choice
// on record rather than a surprise, since the alternative (type resolution) is what
// made the three hand-rolled scanners this package replaced so unreliable.
func TestFlagNamesFalsePositiveOnAShadowedReceiver(t *testing.T) {
	f := parseSource(t, `package p

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

	before, after := parseSource(t, ugly), parseSource(t, string(pretty))

	assert.Equal(t, FlagNames(before), FlagNames(after),
		"reformatting does not move a flag binding")
}

// TestFlagNamesIgnoresNonBinderCalls covers the receiver/method filter: a call on
// fs that is not a flag binder (fs.Parse, fs.PrintDefaults) must not be read as a
// flag, or every FlagSet would report a phantom flag named after its own arguments.
func TestFlagNamesIgnoresNonBinderCalls(t *testing.T) {
	f := parseSource(t, `package p

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
