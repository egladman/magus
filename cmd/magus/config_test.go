package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cli"
	"github.com/egladman/magus/internal/config"
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

// selfStatusErr is any failure that names its own process status, the shape a contended
// no-wait workspace lock arrives in. Declared here rather than imported because the
// package that returns it keeps the type unexported; what exitCodeOf reads is the method.
type selfStatusErr struct{ code int }

func (selfStatusErr) Error() string   { return "the machine is busy" }
func (e selfStatusErr) ExitCode() int { return e.code }

// TestSelfDescribedExitStatusSurvives pins that a failure carrying its own status keeps
// it locally, matching what the daemon already forwards for an adopted run. A contended
// MAGUS_NO_WAIT lock exits 75 (EX_TEMPFAIL); collapsing it to 1 is what made a busy
// machine read as a broken build.
func TestSelfDescribedExitStatusSurvives(t *testing.T) {
	assert.Equal(t, 75, exitCodeOf(selfStatusErr{code: 75}))
	assert.Equal(t, 75, exitCodeOf(fmt.Errorf("run: %w", selfStatusErr{code: 75})), "survives wrapping")
}

// runOnlyFlags lists flags that intentionally exist on `magus run` but not
// `magus affected`. Each entry must cite the reason; the corresponding
// declaration in run.go carries a matching "run-only:" comment.
var runOnlyFlags = map[string]string{
	"shard":               "CI matrix sharding targets an explicit project set; affected's scope is already minimal",
	"n-shards":            "pairs with --shard",
	"no-volatility-retry": "consumed by `magus ci bisect` which dispatches through run, not affected",
	"skip":                "subtracts from a selection the caller named; affected's set is derived from the diff, and dropping a project the diff put there un-gates exactly what affected exists to gate",
}

// affectedOnlyFlags lists flags that intentionally exist on `magus affected`
// but not `magus run`. Each entry must cite the reason; the corresponding
// declaration in affected.go carries a matching "affected-only:" comment.
var affectedOnlyFlags = map[string]string{
	"base":  "VCS diff base ref; `magus run` has no diff",
	"b":     "short for --base",
	"stdin": "reads changed paths from a pipe (watch loop); `magus run` takes explicit project paths",
	"null":  "pairs with --stdin",

	// The sub-modes. These select a different command path rather than configure
	// the run, and each is documented on affected's page because that is where a
	// reader meets it. They were absent from this map while the comparison read
	// run.go and affected.go as SOURCE: the scan looked only inside the affected
	// function, so everything bound by affectedPlan, affectedImpact and bisect was
	// invisible to it. Comparing the documented surfaces instead makes them visible,
	// which is the point - they are exceptions, not omissions.
	"explain":             "mode selector: reports why a project is in the set instead of running",
	"plan":                "mode selector: emits a CI shard plan for the affected set",
	"max-shards":          "pairs with --plan",
	"max-parallel-budget": "pairs with --plan",
	"detail":              "pairs with --plan",
	"bisect":              "mode selector: drives VCS bisect over the diff, which `magus run` has no notion of",
	"good":                "pairs with --bisect",
	"target":              "pairs with --bisect",

	"impact": "mode selector: reports the changeset's blast radius instead of running",
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

	runFlags := registryFlagNames(t, "run")
	affectedFlags := registryFlagNames(t, "affected")

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

// registryFlagNames returns the flags one command declares, as a set.
//
// Read from the command registry rather than parsed out of the Go source. The
// source scan this replaced walked run.go and affected.go for fs.Bool-style calls,
// which was the only way to ask "what does this command bind" while each command
// bound its own list by hand. Both now bind from the registry via a generated
// binder, so there is nothing to scrape - and a scan would find zero and pass
// vacuously if it were not for its own emptiness check.
func registryFlagNames(t *testing.T, command string) map[string]struct{} {
	t.Helper()
	for _, c := range cli.All {
		if c.Name != command {
			continue
		}
		require.NotEmpty(t, c.Flags, "command %q declares no flags", command)
		out := make(map[string]struct{}, len(c.Flags))
		for _, f := range c.Flags {
			out[f.Name] = struct{}{}
		}
		return out
	}
	t.Fatalf("command %q is not in the man-page registry", command)
	return nil
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
