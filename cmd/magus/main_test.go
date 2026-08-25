package main

import (
	"errors"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/stretchr/testify/assert"
)

// TestResolveProfileRunAffectedUsageSkipsForward pins the fix for the silent
// `run -h` / `affected -h` / bare `affected` bug: a usage-only invocation of an
// adoptable subcommand must NOT forward to the daemon (which would print usage on the
// daemon's stderr and hand the caller a bare non-zero exit). It runs locally with
// only config loaded, so the per-subcommand usage reaches the caller's stderr.
func TestResolveProfileRunAffectedUsageSkipsForward(t *testing.T) {
	usageOnly := dispatchProfile{needsConfig: true}
	full := dispatchProfile{needsConfig: true, needsDaemonFwd: true, needsWorkspace: true}
	// server subcommands never forward and never host their own proc server: doing so let a
	// version-mismatched `server stop` shut down its own throwaway server instead of the real
	// daemon (a silent no-op). Config-only, like a usage-only invocation.
	serverProfile := dispatchProfile{needsConfig: true}

	cases := []struct {
		name    string
		sub     string
		subArgs []string
		want    dispatchProfile
	}{
		{"run bare", "run", nil, usageOnly},
		{"run -h", "run", []string{"-h"}, usageOnly},
		{"run --help", "run", []string{"--help"}, usageOnly},
		{"run help", "run", []string{"help"}, usageOnly},
		{"run target still forwards", "run", []string{"build"}, full},
		{"run flag-then-target still forwards", "run", []string{"-v", "build"}, full},
		// --detach SUBMITS to the daemon and reports the job id, so it is the client's
		// job for the same reason usage is. Forwarding it had the daemon submit to
		// itself and print the id onto its own log, leaving the caller silent at exit 0.
		{"run --detach stays local", "run", []string{"build", "--detach"}, usageOnly},
		{"run -detach stays local", "run", []string{"-detach", "build"}, usageOnly},
		{"run --detach=true stays local", "run", []string{"build", "--detach=true"}, usageOnly},
		{"a longer flag is a different flag", "run", []string{"build", "--detach-me"}, full},
		// Past "--" the tokens belong to the forwarded tool, not to magus.
		{"detach past the separator is the tool's", "run", []string{"go::go-test", ".", "--", "--detach"}, full},
		{"affected bare", "affected", nil, usageOnly},
		{"affected -h", "affected", []string{"-h"}, usageOnly},
		{"affected --help", "affected", []string{"--help"}, usageOnly},
		{"affected target still forwards", "affected", []string{"ci"}, full},
		// A forensic mode runs nothing, so a forward buys no pool and costs the report:
		// the daemon prints it on its own stdout and the client exits 0 with an empty one.
		// That is what made magus\affectedImpact - which forks `affected --impact -o json`
		// and decodes the child's stdout - fail with "decode report:" and an empty stderr
		// whenever the caller had a daemon to forward to.
		{"affected --impact stays local", "affected", []string{"--impact"}, usageOnly},
		{"affected --impact with a base stays local", "affected", []string{"--impact", "--base", "origin/main", "-o", "json"}, usageOnly},
		{"affected --explain stays local", "affected", []string{"--explain", "docs"}, usageOnly},
		{"affected --plan stays local", "affected", []string{"ci", "--plan"}, usageOnly},
		// --bisect runs the target once per candidate commit, so it still wants the pool.
		{"affected --bisect still forwards", "affected", []string{"--bisect", "docs"}, full},
		{"a longer mode flag is a different flag", "affected", []string{"ci", "--impactful"}, full},
		{"run is never a forensic mode", "run", []string{"build", "--impact"}, full},
		{"server stop never forwards", "server", []string{"stop"}, serverProfile},
		{"server start never forwards", "server", []string{"start"}, serverProfile},
		{"server job never forwards", "server", []string{"job", "sync-graph"}, serverProfile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveProfile(tc.sub, tc.subArgs)
			if got != tc.want {
				t.Fatalf("resolveProfile(%q, %v) = %+v, want %+v", tc.sub, tc.subArgs, got, tc.want)
			}
		})
	}
}

func TestIsUsageOnlyInvocation(t *testing.T) {
	cases := []struct {
		name    string
		subArgs []string
		want    bool
	}{
		{"empty", nil, true},
		{"-h", []string{"-h"}, true},
		{"--help", []string{"--help"}, true},
		{"help", []string{"help"}, true},
		{"target", []string{"build"}, false},
		{"help after target", []string{"build", "-h"}, false},
		{"leading global flag", []string{"-v"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUsageOnlyInvocation(tc.subArgs); got != tc.want {
				t.Fatalf("isUsageOnlyInvocation(%v) = %v, want %v", tc.subArgs, got, tc.want)
			}
		})
	}
}

// TestPreScanExtractorsStopAtSeparator pins the "--" guard on every pre-scan
// extractor that peeks argv before the main flag parse. Without it, a token meant
// verbatim for a forwarded tool (e.g. `go::go-test . -- -v`, where -v is the test
// binary's own flag) is misread as magus's own flag. The correct pattern already
// existed in this file (hasDetachFlag, bindGlobalsAfterSubcommand); these four had
// no test at all before this fix.
func TestPreScanExtractorsStopAtSeparator(t *testing.T) {
	t.Run("extractRootFlag", func(t *testing.T) {
		if got := extractRootFlag([]string{"run", "build", "--", "-root", "x"}); got != "" {
			t.Fatalf("extractRootFlag = %q, want empty", got)
		}
		if got := extractRootFlag([]string{"-root", "ws", "run", "build"}); got != "ws" {
			t.Fatalf("extractRootFlag = %q, want %q", got, "ws")
		}
	})

	t.Run("extractQuietFlag", func(t *testing.T) {
		if got := extractQuietFlag([]string{"run", "build", "--", "-q"}); got {
			t.Fatalf("extractQuietFlag = %v, want false", got)
		}
		if got := extractQuietFlag([]string{"-q", "run", "build"}); !got {
			t.Fatalf("extractQuietFlag = %v, want true", got)
		}
	})

	t.Run("extractSilentFlag", func(t *testing.T) {
		if got := extractSilentFlag([]string{"run", "build", "--", "-s"}); got {
			t.Fatalf("extractSilentFlag = %v, want false", got)
		}
		if got := extractSilentFlag([]string{"-s", "run", "build"}); !got {
			t.Fatalf("extractSilentFlag = %v, want true", got)
		}
	})

	t.Run("extractDaemonEnabledFlag", func(t *testing.T) {
		if val, set := extractDaemonEnabledFlag([]string{"run", "build", "--", "-daemon-enabled"}); val || set {
			t.Fatalf("extractDaemonEnabledFlag = (%v, %v), want (false, false)", val, set)
		}
		if val, set := extractDaemonEnabledFlag([]string{"-daemon-enabled", "run", "build"}); !val || !set {
			t.Fatalf("extractDaemonEnabledFlag = (%v, %v), want (true, true)", val, set)
		}
	})

	t.Run("extractVerbosityCount", func(t *testing.T) {
		if got := extractVerbosityCount([]string{"run", "build", "--", "-v", "-v"}); got != 0 {
			t.Fatalf("extractVerbosityCount = %d, want 0", got)
		}
		if got := extractVerbosityCount([]string{"-vv", "run", "build"}); got != 2 {
			t.Fatalf("extractVerbosityCount = %d, want 2", got)
		}
	})
}

// TestSnapshotGlobalsRestoresDryRun pins the fix for the daemon flag-bleed bug:
// dispatchAdopted defers snapshotGlobals()() so one adopted client's flags (e.g.
// --dry-run) cannot survive into the next dispatch on the same process. gen.BindFlags
// binds each generated flag with the CURRENT struct field as its default, so an unset
// flag on a later dispatch otherwise keeps whatever the previous one left in globalCfg.
//
// dispatchAdopted itself is not called directly here: its "run"/"affected" branches
// load a real workspace through loadMagus/inspectWorkspace, both memoized behind a
// process-wide sync.Once that panics if invoked twice with a different root - not
// something a unit test can safely drive. Instead this exercises the exact mechanism
// dispatchAdopted's callees use to set the field (cmdParse -> gen.BindFlags) and proves
// snapshotGlobals's restore undoes it, which is the narrowest reachable seam for the fix.
func TestSnapshotGlobalsRestoresDryRun(t *testing.T) {
	savedCfg, savedGlobal := globalCfg, global
	t.Cleanup(func() { globalCfg, global = savedCfg, savedGlobal })
	globalCfg, global = config.Config{}, globalFlags{}

	// First adopted dispatch: a client passes --dry-run.
	restore := snapshotGlobals()
	if _, err := cmdParse("run", []string{"--dry-run"}, nil); err != nil {
		t.Fatalf("cmdParse: %v", err)
	}
	if !globalCfg.DryRun {
		t.Fatalf("globalCfg.DryRun = false mid-dispatch, want true (cmdParse should have set it)")
	}
	restore()
	if globalCfg.DryRun {
		t.Fatalf("globalCfg.DryRun = true after restore, want false")
	}

	// Second adopted dispatch: a different client, no --dry-run. Before the fix,
	// gen.BindFlags binds the flag's default to the CURRENT (bled) field value, so
	// an unset flag here would silently inherit true from the first dispatch.
	restore = snapshotGlobals()
	if _, err := cmdParse("run", nil, nil); err != nil {
		t.Fatalf("cmdParse: %v", err)
	}
	if globalCfg.DryRun {
		t.Fatalf("globalCfg.DryRun = true on a dispatch that never asked for it: the bug this test pins")
	}
	restore()
}

// The exit-code contract magus does keep: 0 for success, 2 for a misuse the
// invocation never got past, 1 for work that ran and failed. There is deliberately
// no code for "the machine was busy" - magus no longer refuses work on that ground.
func TestExitCodeOf(t *testing.T) {
	assert.Equal(t, 0, exitCodeOf(nil))
	assert.Equal(t, 1, exitCodeOf(errors.New("go exited 1")))
	assert.Equal(t, 1, exitCodeOf(errors.Join(errors.New("go exited 1"), nil)))
	assert.Equal(t, exitUsage, exitCodeOf(usagef("no such target")))
}

// TestUsageNeedsNoWorkspace pins the rule that asking a command what it does must not do
// anything.
//
// Every subcommand's own parser prints usage and returns before it loads a workspace, but
// the pre-dispatch preload runs FIRST - so `magus diff -h` opened the workspace, and opening
// one refreshes the VCS merge-driver registration, which writes the tracked .gitattributes
// and a git config entry naming the running binary. A persona doing nothing but reading help
// left a dangling registration pointing at a throwaway binary path.
func TestUsageNeedsNoWorkspace(t *testing.T) {
	for _, sub := range []string{"diff", "run", "affected", "describe", "ls", "doctor", "graph"} {
		for _, help := range []string{"-h", "--help", "help"} {
			got := resolveProfile(sub, []string{help})
			if got.needsWorkspace {
				t.Errorf("resolveProfile(%q, [%q]) loads a workspace; reading help must write nothing", sub, help)
			}
			// The config tier stays: usage text reads it, and reading magus.yaml writes
			// nothing.
			if !got.needsConfig {
				t.Errorf("resolveProfile(%q, [%q]) skips config; usage text reads it", sub, help)
			}
		}
	}

	// A help flag AFTER a positional is still a help request: `magus diff --impact -h`.
	if resolveProfile("diff", []string{"--impact", "-h"}).needsWorkspace {
		t.Error("a trailing help flag must still skip the workspace")
	}
	// ...but a real invocation is unaffected.
	if !resolveProfile("diff", []string{"--impact"}).needsWorkspace {
		t.Error("a real diff still needs the workspace")
	}
}

func TestWantsUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"bare help flag", []string{"-h"}, true},
		{"long form", []string{"--help"}, true},
		{"help subverb", []string{"help"}, true},
		{"after a flag", []string{"--impact", "-h"}, true},
		{"no args is not a help request", nil, false},
		{"a real invocation", []string{"--impact"}, false},
		// Past "--" the tokens belong to a forwarded tool: this asks the test binary for
		// help, not magus.
		{"after a passthrough marker", []string{"go::go-test", ".", "--", "-h"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := wantsUsage(tc.args); got != tc.want {
				t.Fatalf("wantsUsage(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
