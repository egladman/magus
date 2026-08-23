package main

import (
	"flag"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
)

// TestClampIndexStopsAtTheEnds pins the deliberate non-wrapping: a short list is read as a
// list, not a carousel, and wrapping from the last entry to the first reads as the
// selection having been lost.
func TestClampIndexStopsAtTheEnds(t *testing.T) {
	assert.Equal(t, 0, clampIndex(5, 0))
	assert.Equal(t, 0, clampIndex(-3, 4))
	assert.Equal(t, 3, clampIndex(9, 4))
	assert.Equal(t, 2, clampIndex(2, 4))
}

// TestIndexOfFailureMatchesOnIdentity pins what identifies a failure across a rerun: the
// project and target pair, not the log path or the output ref, which both change.
func TestIndexOfFailureMatchesOnIdentity(t *testing.T) {
	items := []cache.Failure{
		{Project: "web", Target: "build", OutputRef: "out1"},
		{Project: "api", Target: "test", OutputRef: "out2"},
	}

	assert.Equal(t, 1, indexOfFailure(items, cache.Failure{Project: "api", Target: "test", OutputRef: "different"}))
	assert.Equal(t, -1, indexOfFailure(items, cache.Failure{Project: "api", Target: "build"}))
	assert.Equal(t, -1, indexOfFailure(nil, cache.Failure{Project: "api", Target: "test"}))
}

// TestEffectiveLevelQuietWinsOverVerbose pins the precedence the flag help promises:
// --quiet beats any number of -v, so a script that passes both gets the quiet one.
func TestEffectiveLevelQuietWinsOverVerbose(t *testing.T) {
	assert.Equal(t, slog.LevelError, effectiveLevel(0, true))
	assert.Equal(t, slog.LevelError, effectiveLevel(3, true))
	assert.Equal(t, slog.LevelInfo, effectiveLevel(0, false))
	assert.Equal(t, slog.LevelDebug, effectiveLevel(1, false))
	assert.Equal(t, slog.LevelDebug, effectiveLevel(2, false))
	assert.Equal(t, config.LevelTrace, effectiveLevel(3, false))
}

// TestLevelNameNamesTraceItself covers the one level slog cannot name: LevelTrace sits
// below Debug, and slog's own String() renders it as "DEBUG-4".
func TestLevelNameNamesTraceItself(t *testing.T) {
	assert.Equal(t, "trace", levelName(config.LevelTrace))
	assert.Equal(t, "DEBUG", levelName(slog.LevelDebug))
	assert.Equal(t, "INFO", levelName(slog.LevelInfo))
	assert.Equal(t, "ERROR", levelName(slog.LevelError))
}

// TestEnvDefaultRewritesTheDefaultNotTheValue pins why this is not just a Set call: it
// also moves DefValue, so `-h` shows the env-supplied value as the default rather than
// advertising a default the command will not use.
func TestEnvDefaultRewritesTheDefaultNotTheValue(t *testing.T) {
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.String("base", "origin/main", "")
		fs.Int("max", 4, "")
		return fs
	}

	fs := newFS()
	envDefault(fs, "base", "release")
	assert.Equal(t, "release", fs.Lookup("base").Value.String())
	assert.Equal(t, "release", fs.Lookup("base").DefValue)

	// An empty value is "nothing was set in the environment", not "set it to empty".
	fs = newFS()
	envDefault(fs, "base", "")
	assert.Equal(t, "origin/main", fs.Lookup("base").DefValue)

	// An unknown flag and an unparseable value are both no-ops rather than failures:
	// the environment is not the caller's invocation, so it must not abort one.
	fs = newFS()
	envDefault(fs, "nosuchflag", "x")
	envDefault(fs, "max", "not-a-number")
	assert.Equal(t, "4", fs.Lookup("max").DefValue)
}

// TestIsServerStartHelpSkipsTheSubcommand pins the offset: the scan starts at subArgs[1]
// because subArgs[0] is "start" itself, and a subcommand literally named "help" is the
// case that would otherwise be misread.
func TestIsServerStartHelpSkipsTheSubcommand(t *testing.T) {
	assert.True(t, isServerStartHelp([]string{"start", "-h"}))
	assert.True(t, isServerStartHelp([]string{"start", "--foreground", "--help"}))
	assert.True(t, isServerStartHelp([]string{"start", "help"}))
	assert.False(t, isServerStartHelp([]string{"start"}))
	assert.False(t, isServerStartHelp([]string{"start", "--foreground"}))
	assert.False(t, isServerStartHelp([]string{"help"}), "the subcommand itself is not a help flag")
}

func TestWantsForeground(t *testing.T) {
	assert.True(t, wantsForeground([]string{"start", "--foreground"}))
	assert.True(t, wantsForeground([]string{"start", "-foreground"}))
	// A pre-parse scanner takes only the bare spellings; an =value form is not a bool.
	assert.False(t, wantsForeground([]string{"start", "--foreground=true"}))
	assert.False(t, wantsForeground([]string{"start"}))
}

// TestConsoleURLsDegradeToEmpty pins the contract both link builders share: a disabled
// console yields "" rather than a link that goes nowhere, decided before any token is read.
func TestConsoleURLsDegradeToEmpty(t *testing.T) {
	prev := globalCfg.Console.Enabled
	t.Cleanup(func() { globalCfg.Console.Enabled = prev })

	off := false
	globalCfg.Console.Enabled = &off
	assert.Equal(t, "", consoleWatchURL())
	assert.Equal(t, "", consoleDiffURL())
}

func TestFilterPathsIsCaseInsensitiveSubstring(t *testing.T) {
	items := []string{"cmd/magus/diff.go", "internal/CACHE/log.go", "types/diff.go"}

	// An empty filter returns the input itself rather than a copy: the picker shows
	// everything before a keystroke arrives.
	assert.Equal(t, items, filterPaths(items, ""))
	assert.Equal(t, items, filterPaths(items, "   "))

	assert.Equal(t, []string{"cmd/magus/diff.go", "types/diff.go"}, filterPaths(items, "DIFF"))
	assert.Equal(t, []string{"internal/CACHE/log.go"}, filterPaths(items, "cache"))
	assert.Nil(t, filterPaths(items, "nothing-matches"))
}

// TestMatchesScopedAcceptsAFingerprintPrefix pins the three spellings a person can type at
// a token: its name, its whole fingerprint, or the prefix they can actually see.
func TestMatchesScopedAcceptsAFingerprintPrefix(t *testing.T) {
	toks := []auth.ConnectorToken{{Name: "laptop", Fingerprint: "deadbeef"}}

	assert.True(t, matchesScoped(toks, "laptop"))
	assert.True(t, matchesScoped(toks, "deadbeef"))
	assert.True(t, matchesScoped(toks, "dead"))
	assert.True(t, matchesScoped(toks, "  laptop  "))

	assert.False(t, matchesScoped(toks, "beef"), "a fingerprint matches by prefix, not anywhere")
	assert.False(t, matchesScoped(toks, ""))
	assert.False(t, matchesScoped(nil, "laptop"))
}

func TestFirstNonEmptyTreatsBlankAsEmpty(t *testing.T) {
	assert.Equal(t, "a", firstNonEmpty("a", "b"))
	assert.Equal(t, "b", firstNonEmpty("", "b"))
	assert.Equal(t, "b", firstNonEmpty("   \n", "b"))
	assert.Equal(t, "", firstNonEmpty("", ""))
}

// TestRelativeToRootKeepsAnOutsidePathAbsolute pins the escape guard: a path outside the
// workspace is returned as given, because a "../.." spelling would name a file the reader
// cannot resolve against the workspace they are in.
func TestRelativeToRootKeepsAnOutsidePathAbsolute(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repos", "magus")

	assert.Equal(t, "cmd/magus/diff.go", relativeToRoot(root, filepath.Join(root, "cmd", "magus", "diff.go")))
	assert.Equal(t, ".", relativeToRoot(root, root))

	outside := filepath.Join(string(filepath.Separator), "repos", "other", "x.go")
	assert.Equal(t, outside, relativeToRoot(root, outside))
}

func TestFlagValueOfAndIsFlagNamedAgreeOnTheSameFlag(t *testing.T) {
	// The two halves must not disagree about what counts as the flag: `--explain` is the
	// bare form and only isFlagNamed sees it, `--explain=web` carries a value and only
	// flagValueOf sees it. A scanner that accepted both from one helper would read the
	// next positional as the value.
	require.True(t, isFlagNamed("--explain", "explain"))
	require.Empty(t, flagValueOf("--explain", "explain"))

	require.False(t, isFlagNamed("--explain=web", "explain"))
	require.Equal(t, "web", flagValueOf("--explain=web", "explain"))
}
