package commands

import (
	"flag"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/log/quiet"
)

// bindGlobals is the prescan's flag set: the generated config flags and the display value
// flag -o, which the real binder adds beside bool flags that do not matter here.
func bindGlobals(fs *flag.FlagSet) {
	flaggen.BindFlags(fs, &config.Config{})
	fs.String("o", "", "")
}

// repeatedFlag is a value-taking flag that may repeat, the shape of a command's own --skip.
type repeatedFlag []string

func (r *repeatedFlag) String() string     { return strings.Join(*r, ",") }
func (r *repeatedFlag) Set(v string) error { *r = append(*r, v); return nil }

type AffectedTargetSplitSuite struct{ suite.Suite }

func TestAffectedTargetSplitSuite(t *testing.T) { suite.Run(t, new(AffectedTargetSplitSuite)) }

func (s *AffectedTargetSplitSuite) TestBaseKeepsItsValueBesideATarget() {
	var (
		af     *flaggen.AffectedFlags
		format string
	)
	bind := func(fs *flag.FlagSet) {
		af = flaggen.BindAffected(fs)
		fs.StringVar(&format, "o", "", "")
	}

	target, rest, ok := SplitTargetFromArgs([]string{"ci", "--base", "origin/main", "-o", "jsonl"}, bind)
	s.Require().True(ok)
	s.Equal("ci", target)

	fs := flag.NewFlagSet("affected", flag.ContinueOnError)
	bind(fs)
	s.Require().NoError(fs.Parse(rest))
	s.Equal("origin/main", af.Base)
	s.Equal("jsonl", format)
}

func TestReorderFlagsFirst(t *testing.T) {
	// A flag set mirroring the shapes commandParse sees: a value flag, a bool flag.
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.String("output", "", "value flag")
		fs.Bool("explain", false, "bool flag")
		var v quiet.Verbosity
		fs.Var(&v, "v", "counted bool flag")
		return fs
	}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"bool flag after positional", []string{"lint:rw", "--explain"}, []string{"--explain", "lint:rw"}},
		{"value flag after positional", []string{"lint:rw", "-output", "name"}, []string{"-output", "name", "lint:rw"}},
		{"flags already first unchanged", []string{"--explain", "lint:rw"}, []string{"--explain", "lint:rw"}},
		{"equals form self-contained", []string{"api", "--output=json"}, []string{"--output=json", "api"}},
		{"counted bool -v does not eat positional", []string{"build", "-v"}, []string{"-v", "build"}},
		{"double-dash halts reorder, tail preserved", []string{"test", "--explain", "--", "-run", "X"}, []string{"--explain", "test", "--", "-run", "X"}},
		{"bare dash is a positional", []string{"-", "--explain"}, []string{"--explain", "-"}},
		{"multiple positionals keep order", []string{"a", "--explain", "b"}, []string{"--explain", "a", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ReorderFlagsFirst(newFS(), c.in))
		})
	}
}

// TestPartitionFlags locks the flag/positional split that both ReorderFlagsFirst and
// SplitTargetFromArgs depend on. A regression here silently breaks flag placement, so
// every rule (bool flag, value flag, -flag=value, "--" passthrough, unknown flag, and a
// value flag with no trailing value) is pinned.
func TestPartitionFlags(t *testing.T) {
	// A self-contained FlagSet so the cases don't drift with the real global flags:
	// "b" is boolean (no value), "val" takes the next token.
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.Bool("b", false, "")
		fs.String("val", "", "")
		return fs
	}

	cases := []struct {
		name    string
		args    []string
		flags   []string
		posargs []string
	}{
		{"positional only", []string{"x"}, []string{}, []string{"x"}},
		{"bool flag before positional", []string{"-b", "x"}, []string{"-b"}, []string{"x"}},
		{"bool flag after positional", []string{"x", "-b"}, []string{"-b"}, []string{"x"}},
		{"value flag keeps its value (before)", []string{"-val", "v", "x"}, []string{"-val", "v"}, []string{"x"}},
		{"value flag keeps its value (after)", []string{"x", "-val", "v"}, []string{"-val", "v"}, []string{"x"}},
		{"value is not mistaken for a positional", []string{"-val", "x", "y"}, []string{"-val", "x"}, []string{"y"}},
		{"flag=value is self-contained", []string{"-val=v", "x"}, []string{"-val=v"}, []string{"x"}},
		{"double dash halts, tail preserved", []string{"-b", "x", "--", "-y", "z"}, []string{"-b"}, []string{"x", "--", "-y", "z"}},
		{"unknown flag does not consume next token", []string{"-nope", "x"}, []string{"-nope"}, []string{"x"}},
		{"value flag at end with no value", []string{"x", "-val"}, []string{"-val"}, []string{"x"}},
		{"double-dash long forms too", []string{"--b", "--val", "v", "x"}, []string{"--b", "--val", "v"}, []string{"x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotFlags, gotPos := PartitionFlags(newFS(), c.args)
			assert.Equal(t, c.flags, gotFlags, "flags")
			assert.Equal(t, c.posargs, gotPos, "positionals")
			// ReorderFlagsFirst must remain flags-then-positionals over the same split.
			assert.Equal(t, append(append([]string{}, c.flags...), c.posargs...),
				ReorderFlagsFirst(newFS(), c.args), "ReorderFlagsFirst")
		})
	}
}

// TestSplitTargetFromArgs is the regression guard for the fix: the target must be found
// regardless of where recognized global/display flags sit (the bug was a flag BEFORE the
// target being mistaken for it, e.g. `magus run --dry-run build`). Uses the real global
// flag set (--dry-run bool, -o value) so it tracks the actual bindings.
func TestSplitTargetFromArgs(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		target string
		rest   []string
		ok     bool
	}{
		{"target only", []string{"build"}, "build", []string{}, true},
		{"bool flag BEFORE target (the fix)", []string{"--dry-run", "build"}, "build", []string{"--dry-run"}, true},
		{"bool flag after target", []string{"build", "--dry-run"}, "build", []string{"--dry-run"}, true},
		{"flag before target, project after", []string{"--dry-run", "build", "web"}, "build", []string{"--dry-run", "web"}, true},
		{"value flag before target keeps value", []string{"-o", "json", "build"}, "build", []string{"-o", "json"}, true},
		{"value flag between target and project", []string{"build", "-o", "json", "web"}, "build", []string{"-o", "json", "web"}, true},
		{"multiple flags around target", []string{"--dry-run", "build", "-o", "json"}, "build", []string{"--dry-run", "-o", "json"}, true},
		{"no args", []string{}, "", nil, false},
		{"flag but no target", []string{"--dry-run"}, "", nil, false},
		{"only a passthrough marker", []string{"--", "x"}, "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target, rest, ok := SplitTargetFromArgs(c.args, bindGlobals)
			assert.Equal(t, c.ok, ok, "ok")
			assert.Equal(t, c.target, target, "target")
			assert.Equal(t, c.rest, rest, "rest")
		})
	}
}

// TestSplitTargetFromArgsLocalFlags proves why the prescan takes the command's own
// flags. Without them a value-taking local flag reads as a bool here, so its value is
// hoisted out as a positional, which one flag survives by luck of ordering, and a
// REPEATED one does not: the first --skip ends up holding the second as its value.
func TestSplitTargetFromArgsLocalFlags(t *testing.T) {
	args := []string{"generate", "--skip", "docs", "--skip", "console"}

	target, rest, ok := SplitTargetFromArgs(args, func(fs *flag.FlagSet) {
		bindGlobals(fs)
		fs.Var(&repeatedFlag{}, "skip", "")
	})
	require.True(t, ok)
	assert.Equal(t, "generate", target)
	assert.Equal(t, []string{"--skip", "docs", "--skip", "console"}, rest)

	// With no binder the first --skip swallows the second. A known cost.
	_, rest, ok = SplitTargetFromArgs(args, bindGlobals)
	require.True(t, ok)
	assert.Equal(t, []string{"--skip", "--skip", "docs", "console"}, rest)
}

func TestFlagValueOfAndIsFlagNamedAgreeOnTheSameFlag(t *testing.T) {
	// The two halves must not disagree about what counts as the flag: `--explain` is the
	// bare form and only IsFlagNamed sees it, `--explain=web` carries a value and only
	// ParseFlagValue sees it. A scanner that accepted both from one helper would read the
	// next positional as the value.
	require.True(t, IsFlagNamed("--explain", "explain"))
	require.Empty(t, ParseFlagValue("--explain", "explain"))

	require.False(t, IsFlagNamed("--explain=web", "explain"))
	require.Equal(t, "web", ParseFlagValue("--explain=web", "explain"))
}

func TestFlagIsBool(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.Bool("force", false, "")
	fs.String("dir", "", "")

	assert.True(t, FlagIsBool(fs.Lookup("force")))
	assert.False(t, FlagIsBool(fs.Lookup("dir")))
}

// TestIsFlagNamedAndFlagValueOf pins all four spellings Go's flag package accepts. A
// hand-rolled scanner that handles three of them fails invisibly on the fourth, which is
// exactly how `magus affected -explain .` came to read "." as a target name.
func TestIsFlagNamedAndFlagValueOf(t *testing.T) {
	assert.True(t, IsFlagNamed("-explain", "explain"))
	assert.True(t, IsFlagNamed("--explain", "explain"))
	assert.False(t, IsFlagNamed("-explain=web", "explain"))
	assert.False(t, IsFlagNamed("--explains", "explain"))

	assert.Equal(t, "web", ParseFlagValue("-explain=web", "explain"))
	assert.Equal(t, "web", ParseFlagValue("--explain=web", "explain"))
	assert.Equal(t, "", ParseFlagValue("--explain", "explain"))
	assert.Equal(t, "", ParseFlagValue("--base=main", "explain"))
}

// TestSplitOnDashDash pins the forwarding boundary: everything after "--" is the spell's
// verbatim argv, and an absent separator must not manufacture an empty tail.
func TestSplitOnDashDash(t *testing.T) {
	before, after := SplitOnDashDash([]string{"test", "web"})
	assert.Equal(t, []string{"test", "web"}, before)
	assert.Nil(t, after)

	before, after = SplitOnDashDash([]string{"test", "--", "-run", "TestX"})
	assert.Equal(t, []string{"test"}, before)
	assert.Equal(t, []string{"-run", "TestX"}, after)

	// A trailing separator forwards an empty argv, which is different from forwarding none.
	before, after = SplitOnDashDash([]string{"test", "--"})
	assert.Equal(t, []string{"test"}, before)
	assert.Equal(t, []string{}, after)
}
