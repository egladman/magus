package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReorderFlagsFirst(t *testing.T) {
	// A flag set mirroring the shapes cmdParse sees: a value flag, a bool flag.
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.String("output", "", "value flag")
		fs.Bool("explain", false, "bool flag")
		var v verbosity
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
			assert.Equal(t, c.want, reorderFlagsFirst(newFS(), c.in))
		})
	}
}

// TestPartitionFlags locks the flag/positional split that both reorderFlagsFirst and
// splitTargetFromArgs depend on. A regression here silently breaks flag placement, so
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
			gotFlags, gotPos := partitionFlags(newFS(), c.args)
			assert.Equal(t, c.flags, gotFlags, "flags")
			assert.Equal(t, c.posargs, gotPos, "positionals")
			// reorderFlagsFirst must remain flags-then-positionals over the same split.
			assert.Equal(t, append(append([]string{}, c.flags...), c.posargs...),
				reorderFlagsFirst(newFS(), c.args), "reorderFlagsFirst")
		})
	}
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns everything
// written to it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	previous := os.Stderr
	read, write, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = write
	t.Cleanup(func() { os.Stderr = previous })

	fn()

	require.NoError(t, write.Close())
	content, err := io.ReadAll(read)
	require.NoError(t, err)
	return string(content)
}

// newRichUsageFS returns a FlagSet shaped like a real subcommand's: a local flag plus
// a rich fs.Usage (a couple of preamble lines, then fs.PrintDefaults()), so tests can
// tell the "compact" error path from the "full" help path by whether the -local-flag
// default line shows up.
func newRichUsageFS(fs *flag.FlagSet) {
	fs.Bool("local-flag", false, "a subcommand-local flag")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus fake <terms> [flags]")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
}

// TestCmdParseBadFlagCompactUsage locks the fix for a bad flag burying the usage that
// answers it under a full fs.PrintDefaults() dump of every config/display/local flag:
// the error must print first, the subcommand's own usage text must still be there
// (unabridged dumps aside), and the ~50 config flags (and this test's own -local-flag)
// must NOT appear, replaced by one pointer to `magus -h`.
func TestCmdParseBadFlagCompactUsage(t *testing.T) {
	var out string
	pos, err := func() ([]string, error) {
		var p []string
		var e error
		out = captureStderr(t, func() {
			p, e = cmdParse("fake", []string{"--nope"}, newRichUsageFS)
		})
		return p, e
	}()

	assert.Nil(t, pos)
	require.Error(t, err)
	var silent errSilent
	require.ErrorAs(t, err, &silent, "a bad flag must exit via errSilent so exitCodeOf does not re-log it")
	assert.Equal(t, exitUsage, silent.exitCode)

	assert.True(t, strings.HasPrefix(out, "[error] flag provided but not defined: -nope\n"),
		"error line must come first, got:\n%s", out)
	assert.Contains(t, out, "Usage: magus fake <terms> [flags]")
	assert.Contains(t, out, "Global flags are documented by `magus -h`.")
	assert.NotContains(t, out, "-local-flag", "the full PrintDefaults dump must not appear on a bad flag")
	assert.NotContains(t, out, "-concurrency", "the ~50 global config flags must not appear on a bad flag")
}

// TestCmdParseHelpFullDump locks that -h/--help is unabridged: current behavior (the
// full fs.PrintDefaults() dump, including this test's -local-flag) must be unchanged.
func TestCmdParseHelpFullDump(t *testing.T) {
	out := captureStderr(t, func() {
		_, err := cmdParse("fake", []string{"--help"}, newRichUsageFS)
		assert.ErrorIs(t, err, flag.ErrHelp)
	})

	assert.Contains(t, out, "Usage: magus fake <terms> [flags]")
	assert.Contains(t, out, "-local-flag", "an explicit --help must still show the full flag dump")
	assert.NotContains(t, out, "[error]")
}

// TestCmdParseHelpAliasForms locks the help spellings the pre-scan misses but stdlib
// flag still treats as help: -help, --h, and --help=<value> must exit through
// flag.ErrHelp (exit 0) with the usage printed, never through the bad-flag branch's
// "[error] flag: help requested" banner.
func TestCmdParseHelpAliasForms(t *testing.T) {
	for _, arg := range []string{"-help", "--h", "--help=true"} {
		out := captureStderr(t, func() {
			_, err := cmdParse("fake", []string{arg}, newRichUsageFS)
			assert.ErrorIs(t, err, flag.ErrHelp, "arg %q", arg)
		})
		assert.Contains(t, out, "Usage: magus fake <terms> [flags]", "arg %q", arg)
		assert.NotContains(t, out, "[error]", "arg %q must not take the bad-flag branch", arg)
	}
}

// TestRequestsHelp pins the token scan cmdParse uses to tell -h/--help apart from a
// bad flag before Parse runs: an exact -h/--help token counts, one consumed as
// another flag's value does not, and "--" halts the scan.
func TestRequestsHelp(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.String("val", "", "value flag")
	fs.Bool("b", false, "bool flag")

	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"bare -h", []string{"-h"}, true},
		{"bare --help", []string{"--help"}, true},
		{"help after positional", []string{"foo", "--help"}, true},
		{"no help", []string{"foo", "--nope"}, false},
		{"-h consumed as another flag's value", []string{"-val", "-h"}, false},
		{"halts at --", []string{"--", "-h"}, false},
		{"bool flag does not consume -h", []string{"-b", "-h"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, requestsHelp(fs, c.args))
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
			target, rest, ok := splitTargetFromArgs(c.args)
			assert.Equal(t, c.ok, ok, "ok")
			assert.Equal(t, c.target, target, "target")
			assert.Equal(t, c.rest, rest, "rest")
		})
	}
}
