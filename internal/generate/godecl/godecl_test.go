package godecl

import (
	"go/ast"
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
