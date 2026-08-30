package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestPeekSubRecognizesVersion pins `magus --version`: peekSub used to treat
// --version like any other dash-prefixed token (skip it, keep scanning for a
// subcommand), so `magus --version` alone found no subcommand, fell through to
// the main flag parse, and died on an unregistered flag ("flag parse failed")
// instead of printing the version - `magus version` worked, `magus --version`
// did not. -v is deliberately excluded: it is the verbosity flag (-v, -vv,
// -vvv), not a version request, so it must keep being skipped as an ordinary
// boolean flag here.
func TestPeekSubRecognizesVersion(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantSub     string
		wantSubArgs []string
	}{
		{"--version alone", []string{"--version"}, "version", nil},
		{"-version alone", []string{"-version"}, "version", nil},
		{"version the real subcommand still works", []string{"version"}, "version", nil},
		{"-v is verbosity, not version", []string{"-v"}, "", nil},
		{"--version after a real subcommand is that command's own flag", []string{"run", "build", "--version"}, "run", []string{"build", "--version"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub, subArgs := peekSub(tc.args)
			if sub != tc.wantSub || !slices.Equal(subArgs, tc.wantSubArgs) {
				t.Fatalf("peekSub(%v) = (%q, %v), want (%q, %v)", tc.args, sub, subArgs, tc.wantSub, tc.wantSubArgs)
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

// This file has no usage.go beside it, and that is deliberate: every subcommand writes its own
// usage printer in its own file, and what these tests check is the property they share. Pairing
// it with any one of them would name a single command for a sweep over all of them.

// TestUsagePrintersNameTheirSurface pins what a reader who typed `-h` is actually
// left with. The assertion is deliberately not byte equality: prose is meant to be
// rewritten, but a usage block that stops naming a subcommand or a flag has stopped
// being usage, and that is the regression worth catching.
//
// Every printer here writes to os.Stderr, which is the convention across the package:
// help is not the command's output, so it must not land in a pipe that expects data.
func TestUsagePrintersNameTheirSurface(t *testing.T) {
	tests := []struct {
		name  string
		print func()
		want  []string
	}{
		{
			name:  "affected",
			print: affectedUsage,
			want:  []string{"Usage: magus affected", "--explain", "--impact", "--plan", "--bisect", "--base"},
		},
		{
			name:  "buzz",
			print: buzzUsage,
			want:  []string{"Usage: magus buzz", "-e <code>", "-t, -test", "--embedded", "--no-autoload", "lsp"},
		},
		{
			name:  "chain",
			print: chainUsage,
			want:  []string{"--then", "outputs export --path <dir>", "file <path> history", "file <path> diff", "value"},
		},
		{
			name:  "completion",
			print: completionUsage,
			want:  []string{"Usage: magus completion", "bash", "zsh", "fish", "powershell"},
		},
		{
			name:  "describe",
			print: describeUsage,
			want:  []string{"Usage: magus describe", "spell", "charm", "target", "graph", "project", "workspace", "module", "mcp-tool", "file", "tool"},
		},
		{
			name:  "diff",
			print: func() { diffUsage(os.Stderr) },
			want:  []string{"Usage: magus diff", "--generated", "--no-tui", "magus graph build"},
		},
		{
			name:  "graph",
			print: graphUsage,
			want:  []string{"Usage: magus graph", "build", "deps", "export", "stats", "diff"},
		},
		{
			name:  "man",
			print: manUsage,
			want:  []string{"Usage: magus man install", "--dir", "--dry-run"},
		},
		{
			name:  "memory",
			print: memoryUsage,
			want:  []string{"Usage: magus memory", "ls", "get", "put", "delete", "verify", "magus_memory"},
		},
		{
			name:  "notes",
			print: notesUsage,
			want:  []string{"Usage: magus notes", "ls", "get", "edit", "verify", "capture", "promote", "knowledge.notes.shared"},
		},
		{
			name:  "self",
			print: selfCmdUsage,
			want:  []string{"Usage: magus self", "update", "refresh", "registry", "install-shorthand", "magus init"},
		},
		{
			name:  "install-shorthand",
			print: installShorthandUsage,
			want:  []string{"Usage: magus self install-shorthand", "--dir", "--force", shorthandName},
		},
		{
			name:  "server",
			print: serverUsage,
			want:  []string{"magus server", "start", "stop", "reload", "job", "MAGUS_DAEMON_ADDRESS", daemonDefaultAddr()},
		},
		{
			name:  "server job",
			print: serverJobUsage,
			want:  []string{"magus server job <name>", "Jobs:"},
		},
		{
			name:  "vcs",
			print: func() { vcsUsage(os.Stderr) },
			want:  []string{"Usage: magus vcs", "add", "resolve", "checkpoint", "merge-driver"},
		},
		{
			name:  "vcs add",
			print: func() { vcsAddUsage(os.Stderr) },
			want:  []string{"Usage: magus vcs add", "--dry-run", "--untracked", "git add -A"},
		},
		{
			name:  "vcs resolve",
			print: func() { vcsResolveUsage(os.Stderr) },
			want:  []string{"Usage: magus vcs resolve", "--against <ref>"},
		},
		{
			name:  "vcs checkpoint",
			print: func() { vcsCheckpointUsage(os.Stderr) },
			want:  []string{"Usage: magus vcs checkpoint", "-o name", "graph diff --rev"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStderr(t, tc.print)
			require.NotEmpty(t, out)
			for _, want := range tc.want {
				assert.Contains(t, out, want)
			}
			assertPlainASCII(t, out)
		})
	}
}

// TestUsagePrintersThatReturnAnExitPath covers the two printers that also decide the
// exit status. The status is the load-bearing half: `magus run -h` asking for help is
// not a failure, and merge-driver's usage is reached by a caller that must not be told
// the driver failed.
func TestUsagePrintersThatReturnAnExitPath(t *testing.T) {
	t.Run("run target usage asks for the help exit", func(t *testing.T) {
		var err error
		out := captureStderr(t, func() { err = targetUsage() })
		assert.ErrorIs(t, err, flag.ErrHelp)
		assert.Contains(t, out, "Usage: magus run <target>")
		assert.Contains(t, out, "<spell>::<target>")
		assert.Contains(t, out, "fmt -> format")
		assertPlainASCII(t, out)
	})

	t.Run("merge driver usage succeeds", func(t *testing.T) {
		var err error
		out := captureStderr(t, func() { err = mergeDriverUsage() })
		assert.NoError(t, err)
		assert.Contains(t, out, "magus vcs merge-driver %O %A %B %L %P")
		assert.Contains(t, out, "magus init")
		assert.Contains(t, out, "magus vcs resolve")
		assertPlainASCII(t, out)
	})
}

// assertPlainASCII enforces the workspace rule that user-facing message strings carry
// no em-dashes, curly quotes, or other non-ASCII. Help text is the surface most likely
// to acquire them, and a terminal that cannot render one prints a replacement glyph.
func assertPlainASCII(t *testing.T, s string) {
	t.Helper()
	for i, r := range s {
		if r > 127 {
			t.Errorf("non-ASCII %q at byte %d in: %s", r, i, strings.SplitN(s[i:], "\n", 2)[0])
			return
		}
	}
}

// No display_flags_parity.go exists, and none should: this reads the package's own SOURCE and
// asserts a property every command file must hold. Pairing it with any one of them would name
// a single command for a check over all of them.

// newFlagSetRe finds every command FlagSet construction and captures the
// variable it is bound to, so the check can require that same variable be
// handed to bindDisplayFlags.
var newFlagSetRe = regexp.MustCompile(`^\s*(\w+) := flag\.NewFlagSet\(`)

// TestEveryCommandBindsDisplayFlags is a source-level invariant: every command
// FlagSet gets the display flags (-o/--output, --tee, -v, -q/--quiet,
// -s/--silent).
//
// It is a TEXT scan rather than a behavioral test on purpose. The failure it
// prevents is a command forgetting to call bindDisplayFlags, which no runtime
// assertion can see without enumerating and invoking every subcommand - and an
// enumeration is exactly the thing that goes stale when someone adds a command.
//
// The rule exists because partial support is worse than none. `magus graph
// verify -s` died on "flag provided but not defined", and `magus agent install
// ... -s` did the same with stderr redirected, which looked precisely like a
// successful install - the skills were never written and nothing said so. An
// agent told to always pass -s meets one of those, concludes the flag is
// unreliable, and stops using it everywhere. One gap decays the whole
// convention, so the convention has to be total.
func TestEveryCommandBindsDisplayFlags(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	var missing []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		body, err := os.ReadFile(file)
		require.NoError(t, err, "read %s", file)
		lines := strings.Split(string(body), "\n")

		for i, line := range lines {
			m := newFlagSetRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			// Scan to the end of the enclosing function rather than a fixed
			// window: the bind is often separated from the construction by the
			// comment explaining why it is there, and a line budget would make
			// this test fail on formatting instead of on the thing it checks.
			// A `nodisplayflags:` comment in the preceding lines opts out, and the
			// text after the marker must say why. There is exactly one legitimate
			// case (main.go's "adopted" set, which redeclares the globals into
			// ignored variables precisely to swallow them), so the escape hatch is
			// deliberately ugly and has to be justified in place.
			if slicesContainsSubstr(lines[max(i-8, 0):i], "nodisplayflags:") {
				continue
			}
			want := "bindDisplayFlags(" + m[1] + ")"
			if !slicesContainsSubstr(lines[i+1:endOfFunc(lines, i)], want) {
				missing = append(missing, file+":"+itoa(i+1)+" ("+m[1]+")")
			}
		}
	}

	assert.Empty(t, missing,
		"every command FlagSet must call bindDisplayFlags right after construction, so -s/-q/-v/-o work on every command:\n  %s",
		strings.Join(missing, "\n  "))
}

// endOfFunc returns the index of the closing brace of the function containing
// line i, relying on gofmt putting a top-level "}" in column zero.
func endOfFunc(lines []string, i int) int {
	for j := i + 1; j < len(lines); j++ {
		if lines[j] == "}" {
			return j
		}
	}
	return len(lines)
}

func slicesContainsSubstr(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// TestMain lets the test binary act as the `magus` command inside testscript
// scripts: `exec magus ...` in a .txtar file runs the real CLI in process (via
// run), so behavior tests exercise the actual command, not a mock.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"magus": func() { os.Exit(runCLI()) },
	})
}

// TestScripts replays every testdata/script/*.txtar as a black-box CLI behavior
// test: readable command-plus-expected-output scenarios that catch any observable
// change to the CLI. Each script runs in its own temp dir with the daemon off, so
// tests are hermetic and never touch a real workspace or socket.
func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(e *testscript.Env) error {
			e.Setenv("MAGUS_DAEMON_ENABLED", "false")
			e.Setenv("MAGUS_HINTS_ENABLED", "false")
			// The shipped guard templates, by ABSOLUTE PATH to the real files.
			// A script that copied them into its own archive would be testing a
			// copy, and a copy of the artifact readers download is exactly what
			// dogfood_test.go exists to prevent.
			templates, err := filepath.Abs(filepath.Join("..", "..", "docs", "guides", "integrations", "agents"))
			if err != nil {
				return err
			}
			e.Setenv("GUARD_TEMPLATES", templates)
			return nil
		},
	})
}

// In-process benchmarks for magus.Open itself, separate from the cmd-level
// startup() pipeline benchmarks in startup_bench_test.go.
//
// covering a file, it is run by name (-bench) rather than with the suite, and the methodology
// notes below are the point of the file. Folding it into the tests of whichever source file it
// happens to call would bury them.
//
// Hypothesis B (cross-run magusfile parse-table cache) needs to score the
// residual cost of Open after the Teal→Lua bytecode cache in
// internal/runtime/compile.go is fully warm. BenchmarkStartupLs already
// measures whole-startup cost; the benchmarks here isolate the Open path
// so hypothesis-B work can be evaluated against a cleaner baseline.
// openBenchJSWorkspace creates nProjects JS project directories (package.json
// marker only, no magusfile.tl) so project.Discover auto-detects them as JS
// projects. Used to measure Open on a pure JS workspace where preloadMagusfiles
// is a no-op and all cost is borne by Inspect + cache replay.
func openBenchJSWorkspace(tb testing.TB, nProjects int) string {
	tb.Helper()
	root := tb.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module openbench\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < nProjects; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg-%03d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"pkg"}`), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return root
}

func openBenchWorkspace(tb testing.TB, numTargets int) string {
	tb.Helper()
	root := tb.TempDir()
	proj := filepath.Join(root, "svc")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		tb.Fatal(err)
	}
	var sb strings.Builder
	for i := 0; i < numTargets; i++ {
		fmt.Fprintf(&sb, "global function t_%d(args: {string}) end\n", i)
	}
	if err := os.WriteFile(filepath.Join(proj, "magusfile.tl"), []byte(sb.String()), 0o644); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module openbench\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	return root
}

// BenchmarkMagusOpenCold measures magus.Open on a fresh workspace each
// iteration. Every iteration pays full Teal compile + binding registration.
func BenchmarkMagusOpenCold(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		root := openBenchWorkspace(b, 10)
		b.StartTimer()

		m, err := magus.Open(ctx, root)
		if err != nil {
			b.Fatal(err)
		}
		_ = m.Close()
	}
}

// BenchmarkMagusOpenWarmGraphCache measures magus.Open on a 50-project pure JS
// workspace where the workspace.cache.json is already warm (disk cache valid).
// On a warm cache, project.Discover replays the graph from disk (no FS walk),
// preloadMagusfiles is a no-op (no .tl files), and registry.Apply resolves
// spell names. This is the cold-process / warm-disk hot path for JS monorepos.
//
// optimization: workspace.cache.json fast path (project.Discover restoreFromCache)
//
//	skips the full directory walk + autoDetect goroutine pool on warm disk.
//	For a 50-project JS workspace, the cache hit collapses Inspect from
//	O(D) WalkDir syscalls to a single ReadFile + N Lstat calls (one per dir
//	in DirMtimes). Combined with preloadMagusfiles being a no-op for
//	pure JS workspaces, total Open cost drops by ~80% vs cold.
//	measured: BenchmarkMagusOpenWarmGraphCache vs BenchmarkMagusOpenCold.
//	trade-off: cache is invalidated on any directory mtime change; over-
//	  invalidates on dir-only touches but never serves stale projects.
//	assumes: spell registry is process-deterministic — restoreFromCache
//	  binds spells by name from DefaultSpellRegistry().
func BenchmarkMagusOpenWarmGraphCache(b *testing.B) {
	ctx := context.Background()
	root := openBenchJSWorkspace(b, 50)

	// Warm the on-disk workspace cache with one full Open.
	warm, err := magus.Open(ctx, root)
	if err != nil {
		b.Fatal(err)
	}
	_ = warm.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, err := magus.Open(ctx, root)
		if err != nil {
			b.Fatal(err)
		}
		_ = m.Close()
	}
	b.Logf("target: <5 ms/op (informational; vs ~50 ms cold 50-project walk)")
}

// BenchmarkMagusOpenWarmAOT measures magus.Open after one previous Open
// against the same workspace — the in-process compile cache is fully warm
// so this isolates the per-Open residual that hypothesis B targets.
func BenchmarkMagusOpenWarmAOT(b *testing.B) {
	ctx := context.Background()
	root := openBenchWorkspace(b, 10)

	warm, err := magus.Open(ctx, root)
	if err != nil {
		b.Fatal(err)
	}
	_ = warm.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, err := magus.Open(ctx, root)
		if err != nil {
			b.Fatal(err)
		}
		_ = m.Close()
	}
}

// In-process benchmarks for the magus startup path. These exercise
// startup(ctx, args) without subprocess overhead so benchstat can compare
// before/after across the optimization roadmap.
//
// a path, is run by name, and carries methodology a fold would bury.
//
// Caveat on package-init costs: init() functions in dependent packages
// (e.g. internal/config.init's reflection walk, internal/interp/engine/lua/teal/spell
// .init's JSON unmarshal) fire ONCE per `go test` binary, not per b.N
// iteration. For those, see the spawn-based ground-truth measurement in
// hack/bench_startup.sh — it builds a fresh release binary and times
// real cold starts. The in-process benchmarks below still pick up the
// per-call cost of FindRoot, config decode, daemon-socket lookup, flag
// parse, and (when applicable) magus.Open.
//
// Caveat on singletons: cmd/magus uses sync.Once-backed singletons
// (magusOnce, inspectOnce) so a second loadMagus call short-circuits.
// resetStartupSingletons() restores them between iterations so each call
// measures fresh work.
//
// Capture baseline:
//
//	go test -run=^$ -bench=^BenchmarkStartup$ -benchmem -benchtime=2s \
//	  -count=10 ./cmd/magus > bench.before.txt
//
// Compare after a change:
//
//	go test -run=^$ -bench=^BenchmarkStartup$ -benchmem -benchtime=2s \
//	  -count=10 ./cmd/magus > bench.after.txt
//	benchstat bench.before.txt bench.after.txt
//
// resetStartupSingletons restores package-level state mutated by
// startup() so a benchmark loop can measure each iteration as a cold
// start. Not safe for concurrent use; benchmarks calling it MUST be
// serial.
func resetStartupSingletons() {
	magusOnce = sync.Once{}
	magusValue = nil
	magusErr = nil
	magusRootOverride = ""

	inspectOnce = sync.Once{}
	inspectValue = nil
	inspectErr = nil
	inspectRootOverride = ""

	globalCfg = config.Config{}
	global = globalFlags{}
}

// setupBenchWorkspace creates a synthetic workspace in b.TempDir() with
// the minimal markers FindRoot looks for (go.mod, empty magusfile.tl)
// and chdirs into it. Returns the workspace root.
func setupBenchWorkspace(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module benchstartup\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "magusfile.tl"), nil, 0o644); err != nil {
		b.Fatal(err)
	}
	b.Chdir(dir)
	return dir
}

// quarantineBenchEnv suppresses host state that would otherwise leak into
// startup(): a real daemon socket on the developer machine, an inherited
// trace log level that would print the startup table, etc. Restored by
// t.Setenv at bench end.
func quarantineBenchEnv(b *testing.B) {
	b.Helper()
	b.Setenv("MAGUS_DAEMON_SOCKET", "")
	b.Setenv("MAGUS_LOG_LEVEL", "")
	// startup() calls log.Print on errors; silence it during benches.
	prevOut := log.Writer()
	log.SetOutput(io.Discard)
	b.Cleanup(func() { log.SetOutput(prevOut) })
}

func benchStartup(b *testing.B, args []string) {
	b.Helper()
	setupBenchWorkspace(b)
	quarantineBenchEnv(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		resetStartupSingletons()
		b.StartTimer()
		res, _ := startup(context.Background(), args)
		if res.cleanup != nil {
			res.cleanup()
		}
	}
}

// BenchmarkStartupHelp measures the unconditional pre-dispatch path for
// `magus help` inside a workspace. Today this pays for a full magus.Open
// (cache + Teal magusfile parse) before the help switch fires in main();
// the fast-path skip lands in a follow-up PR.
func BenchmarkStartupHelp(b *testing.B) {
	benchStartup(b, []string{"help"})
}

// BenchmarkStartupVersion measures `magus version` startup — same waste
// as help, no workspace state is actually consulted.
func BenchmarkStartupVersion(b *testing.B) {
	benchStartup(b, []string{"version"})
}

// BenchmarkStartupCompletionBash measures `magus completion bash`. Needs
// config (the completion script embeds config-driven hints) but does not
// need a workspace.
func BenchmarkStartupCompletionBash(b *testing.B) {
	benchStartup(b, []string{"completion", "bash"})
}

// BenchmarkStartupLs is the workspace-aware fast case — exercises
// loadMagus + proc server start. Establishes the floor for any
// subcommand that actually needs the workspace open.
func BenchmarkStartupLs(b *testing.B) {
	benchStartup(b, []string{"ls"})
}
