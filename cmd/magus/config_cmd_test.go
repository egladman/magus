package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/generate/godecl"
)

func TestRunConfigView_Text(t *testing.T) {
	cfg := config.Defaults()
	require.NoError(t, runConfigView(cfg, nil))
}

func TestRunConfigView_JSON(t *testing.T) {
	cfg := config.Defaults()
	require.NoError(t, runConfigView(cfg, []string{"-o", "json"}))
}

func TestRunConfigView_YAML(t *testing.T) {
	cfg := config.Defaults()
	require.NoError(t, runConfigView(cfg, []string{"-o", "yaml"}))
}

func TestRunConfigView_Name(t *testing.T) {
	cfg := config.Defaults()

	// Capture stdout by redirecting within the test is complex; instead just
	// verify no error and that KnownKeys() is populated.
	_ = cfg
	keys := config.KnownKeys()
	assert.NotEmpty(t, keys)
	require.NoError(t, runConfigView(cfg, []string{"-o", "name"}))
}

func TestRunConfigSet_Local(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	require.NoError(t, runConfigSet([]string{"key=cache.dir,value=/tmp/mycache"}))

	path := filepath.Join(dir, config.Filename)
	_, err := os.Stat(path)
	assert.NoError(t, err, "expected %s to exist", path)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/mycache", cfg.Cache.Dir)
}

func TestRunConfigSet_Global(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	require.NoError(t, runConfigSet([]string{"--global", "key=log.format,value=json"}))

	path := filepath.Join(dir, "magus", config.Filename)
	_, err := os.Stat(path)
	assert.NoError(t, err, "expected %s to exist", path)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "json", cfg.Log.Format)
}

func TestRunConfigSet_UnknownKey(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	err := runConfigSet([]string{"key=not.a.real.key,value=v"})
	assert.Error(t, err, "expected error for unknown key")
}

func TestRunConfigSet_BadInt(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	err := runConfigSet([]string{"key=parallel,value=notanumber"})
	assert.Error(t, err, "expected error for bad int")
}

func TestRunConfigCmd_UnknownSubcommand(t *testing.T) {
	cfg := config.Defaults()
	err := configCmd(context.Background(), "", cfg, []string{"frobnicate"})
	require.Error(t, err, "expected error for unknown subcommand")
	assert.Contains(t, err.Error(), "frobnicate", "error should mention subcommand name")
}

// TestRunConfigCmd_NoArgs pins that a missing subcommand is a USAGE ERROR, not a
// quiet success. It previously returned nil, so `magus config` exited 0 and a script
// could not tell a bare invocation from one that did work - and it disagreed with
// `magus man`, which exited 1 for the same mistake. Usage errors exit 2 across the CLI.
func TestRunConfigCmd_NoArgs(t *testing.T) {
	cfg := config.Defaults()
	err := configCmd(context.Background(), "", cfg, nil)
	require.Error(t, err, "no args should be a usage error, not a quiet success")

	var usage errUsage
	require.ErrorAs(t, err, &usage, "should be errUsage so it exits %d", exitUsage)
	assert.Equal(t, exitUsage, exitCodeOf(err))
}

// TestUsageErrorsExitTwo pins the exit-code contract itself: an explicit help request
// is a satisfied request (0), a misuse is 2, and a genuine runtime failure stays 1.
func TestUsageErrorsExitTwo(t *testing.T) {
	assert.Equal(t, 0, exitCodeOf(nil), "success")
	assert.Equal(t, 0, exitCodeOf(flag.ErrHelp), "an explicit -h is a request that was satisfied")
	assert.Equal(t, exitUsage, exitCodeOf(usagef("bad invocation")), "misuse")
	assert.Equal(t, 1, exitCodeOf(errors.New("the work failed")), "runtime failure")
}

// runOnlyFlags lists flags that intentionally exist on `magus run` but not
// `magus affected`. Each entry must cite the reason; the corresponding
// declaration in run.go carries a matching "run-only:" comment.
var runOnlyFlags = map[string]string{
	"shard":               "CI matrix sharding targets an explicit project set; affected's scope is already minimal",
	"n-shards":            "pairs with --shard",
	"no-volatility-retry": "consumed by `magus ci bisect` which dispatches through run, not affected",
}

// affectedOnlyFlags lists flags that intentionally exist on `magus affected`
// but not `magus run`. Each entry must cite the reason; the corresponding
// declaration in affected.go carries a matching "affected-only:" comment.
var affectedOnlyFlags = map[string]string{
	"base":  "VCS diff base ref; `magus run` has no diff",
	"b":     "short for --base",
	"stdin": "reads changed paths from a pipe (watch loop); `magus run` takes explicit project paths",
	"null":  "pairs with --stdin",
}

// TestRunAffectedFlagParity ensures that `magus run` and `magus affected`
// expose the same flags, minus the documented exceptions above. When a new
// flag lands on one subcommand it must either also land on the other, or be
// added to the appropriate exception map here with a one-line rationale.
//
// To debug a failure:
//
//	go test ./cmd/magus/ -run TestRunAffectedFlagParity -v
func TestRunAffectedFlagParity(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	dir := filepath.Dir(thisFile)

	runFlags := collectFlagNames(t, filepath.Join(dir, "run.go"), "runTarget")
	affectedFlags := collectFlagNames(t, filepath.Join(dir, "affected.go"), "affected")

	// Stale exception check: every entry in an exception map must correspond
	// to a flag that actually exists in the owning file.
	for name := range runOnlyFlags {
		assert.Contains(t, runFlags, name,
			"runOnlyFlags entry %q no longer exists in run.go — remove it from the exception map", name)
	}
	for name := range affectedOnlyFlags {
		assert.Contains(t, affectedFlags, name,
			"affectedOnlyFlags entry %q no longer exists in affected.go — remove it from the exception map", name)
	}

	runShared := subtract(runFlags, runOnlyFlags)
	affectedShared := subtract(affectedFlags, affectedOnlyFlags)

	for name := range runShared {
		assert.Contains(t, affectedShared, name,
			"flag --%s exists in `magus run` (run.go) but not `magus affected` (affected.go)\n"+
				"\tAdd it to affected.go, or add an entry to affectedOnlyFlags in %s",
			name, filepath.Base(thisFile))
	}
	for name := range affectedShared {
		assert.Contains(t, runShared, name,
			"flag --%s exists in `magus affected` (affected.go) but not `magus run` (run.go)\n"+
				"\tAdd it to run.go, or add an entry to runOnlyFlags in %s",
			name, filepath.Base(thisFile))
	}
}

// collectFlagNames returns the flags bound inside funcName, as a set.
//
// The AST walking lives in internal/generate/godecl, shared with the generators. This
// file grew its own copy, and because that copy read only the named function while
// magus binds flags across several, anything built on it reported three false
// positives for every real finding.
//
// FlagNamesIn, not FlagNames: this test compares what each COMMAND binds, and
// affected.go also binds flags in affectedPlan and affectedImpact, which are sub-modes
// rather than part of affected's own surface.
func collectFlagNames(t *testing.T, file, funcName string) map[string]struct{} {
	t.Helper()
	parsed, err := godecl.Parse(file)
	require.NoError(t, err, "parse %s", file)
	names := godecl.FlagNamesIn(parsed, funcName)
	require.NotEmpty(t, names, "function %q bound no flags in %s", funcName, file)
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// subtract returns a copy of flags with all keys in exceptions removed.
func subtract(flags map[string]struct{}, exceptions map[string]string) map[string]struct{} {
	out := make(map[string]struct{}, len(flags))
	for k, v := range flags {
		out[k] = v
	}
	for k := range exceptions {
		delete(out, k)
	}
	return out
}
