package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
)

const (
	// gateRepeatWindow is how far back a repeat is counted. Long enough to cover a
	// working session, short enough that yesterday's runs say nothing about today's.
	gateRepeatWindow = 2 * time.Hour

	// gateRepeatMinRuns is how many runs inside the window before saying anything.
	// The second is the first that could have been a narrower target.
	gateRepeatMinRuns = 2

	// gateRepeatMinSpent is how much those runs must have COST before it is worth
	// mentioning. Frequency alone is not waste: a repeat gate over an unchanged tree
	// is mostly cache hits and finishes in seconds, which is the cache working. What
	// is worth a word is a gate that keeps genuinely re-running.
	gateRepeatMinSpent = 5 * time.Minute
)

// adviseRepeatGate reports what the CI gate has already cost in this window, or ""
// when it has not run enough to be worth saying.
//
// The gate is the most expensive target by construction, because it runs everything
// the diff reaches, and it is the one an iterating caller reaches for out of habit.
//
// It reports rather than refuses, and deliberately does NOT judge whether a given
// run was justified. Nothing available separates a wasteful repeat from a legitimate
// re-run after a fix: the tree is dirty at the same revision either way. What is
// checkable is the accumulating cost, and stating it is what changes behavior.
//
// Read from the RUN LOG, not from forecast history. The history is user-global and
// keyed by workspace-relative project path, so every worktree records its root under
// "." in one file. This machine runs dozens, so a sibling checkout's gate would be
// counted and reported as having run "here". The run log is per-workspace, one file
// per invocation, carrying the argv and real wall clock, so the count is a fact
// rather than a reconstruction from per-spell samples.
func adviseRepeatGate(runsDir string, now time.Time) (full, brief string) {
	runs, spent := recentGateRuns(runsDir, now)
	if runs < gateRepeatMinRuns || spent < gateRepeatMinSpent {
		return "", ""
	}
	return gateRepeatAdvice(runs, spent), gateRepeatBrief(runs, spent)
}

// workspaceRunsDir is where this workspace logs its invocations, or "" when there is
// none to read. The hook runs with the workspace as its working directory.
func workspaceRunsDir(cacheDir string) string {
	if cacheDir == "" {
		cacheDir = ".magus"
	}
	dir := filepath.Join(cacheDir, cache.RunsDir)
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}

// recentGateRuns counts the gate's invocations inside the window and sums their wall
// clock.
//
// One log file is one invocation, so there is nothing to cluster and no way to count
// the same run twice. Candidates are filtered by file modification time before any
// file is opened, which keeps a directory of thousands cheap.
func recentGateRuns(runsDir string, now time.Time) (runs int, spent time.Duration) {
	if runsDir == "" {
		return 0, 0
	}
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return 0, 0
	}
	cutoff := now.Add(-gateRepeatWindow)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || info.ModTime().Before(cutoff) {
			continue
		}
		started, ok := readGateInvocation(filepath.Join(runsDir, e.Name()))
		if !ok || started.Before(cutoff) {
			continue
		}
		runs++
		// The log's last write is the run's end; a run still in flight measures as
		// however far it has got, which is the honest reading of "spent so far".
		if d := info.ModTime().Sub(started); d > 0 {
			spent += d
		}
	}
	return runs, spent
}

// readGateInvocation reports when an invocation started, and whether it was the CI
// gate, by reading only its first line: the started event, which carries the argv.
func readGateInvocation(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return time.Time{}, false
	}
	var ev journal.Event
	if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Kind != journal.KindStarted || ev.Command == nil {
		return time.Time{}, false
	}
	if !isGateCommand(ev.Command.Arguments) {
		return time.Time{}, false
	}
	return time.UnixMilli(ev.Ts), true
}

// commandRunsGate reports whether a shell line would invoke the CI gate.
//
// Through the parser rather than a pattern, for the reason parseGuardCommands exists:
// it resolves quoting, peels wrappers, and hands back each command a line would
// actually run, so `mise exec -- ./magus affected ci` reads the same as the bare form
// and a `ci` inside a quoted argument reads as neither.
func commandRunsGate(command string) bool {
	cmds, ok := parseGuardCommands(command)
	if !ok {
		return false
	}
	for _, c := range cmds {
		if c.Name == "magus" && isGateCommand(c.Args) {
			return true
		}
	}
	return false
}

// isGateCommand reports whether an argv invokes the CI gate.
//
// An argv, so there is no shell quoting left to peel and no way for a trailing
// `-run ci` argument to a test command to masquerade as the gate. The two callers
// both supply one: a recorded invocation's arguments, and a parsed command line.
func isGateCommand(args []string) bool {
	for i, a := range args {
		if a != "run" && a != "affected" {
			continue
		}
		for _, rest := range args[i+1:] {
			// Everything past `--` belongs to the underlying tool, where a bare `ci`
			// is a test filter rather than this workspace's gate.
			if rest == "--" {
				return false
			}
			if rest == "" || rest[0] == '-' {
				continue
			}
			// Every bare word, not just the first: magus accepts flags before the
			// target, and a flag's VALUE is a bare word too (`--timeout 5m ci`).
			// ParseTarget strips the charm suffix and normalizes, so `ci:rw` and `CI`
			// both resolve; a project path or a duration simply does not match.
			if t, err := types.ParseTarget(rest); err == nil && t.Name == types.TargetCI {
				return true
			}
		}
		return false
	}
	return false
}

// gateRepeatAdvice names no target but the gate itself: a workspace calls its
// narrower targets whatever it likes, so the advisory points at the command that
// lists them rather than guessing.
func gateRepeatAdvice(runs int, spent time.Duration) string {
	return fmt.Sprintf(
		"magus workspace: the `%s` gate has run %d times in this workspace in the last %s, about %s of wall clock. It runs everything the diff reaches, so it is the most expensive target here.\n",
		types.TargetCI, runs, gateRepeatWindow, spent.Round(time.Second)) +
		"While you iterate, a narrower target answers the same question faster. `" + hint.LsTargets.With("<project>") + "` lists what this workspace calls them, and `" + hint.Affected.With("--plan") + "` shows what the gate would run.\n" +
		"Save the full gate for the commit."
}

// gateRepeatBrief is the repeat form, and it keeps the running cost rather than going
// quiet: the count and the wall clock are the whole argument, and they are the part that
// has changed since the caller last read the full text.
func gateRepeatBrief(runs int, spent time.Duration) string {
	return fmt.Sprintf("magus workspace: the `%s` gate has run %d times here in the last %s, about %s of wall clock. `%s` lists narrower targets.\n",
		types.TargetCI, runs, gateRepeatWindow, spent.Round(time.Second), hint.LsTargets.With("<project>"))
}

// denyLeaseScopedGate refuses the gate to a lease that was handed a narrower check, and
// returns "" for everybody else.
//
// The gate runs ONCE per branch, in the orchestrator's tree, after every unit lands. A
// delegated worker's `validation` is the narrow target it was assigned, and until this
// rule the field declared that and enforced nothing: seven workers each ran the whole
// pipeline concurrently on one machine because every brief ended with it (2026-09-09).
// Gate redundancy cannot catch that, since it keys on identical tree content and seven
// worktrees are seven trees.
//
// Same seatbelt contract gradeLeasedWrite documents. A caller naming no lease, a lease
// with no live row, an unreadable ledger, and a row that declared no validation all pass:
// the last of those is a boundary nobody wrote, not a narrow one. A person running their
// own gate names no lease and never reaches this rule.
func denyLeaseScopedGate(ctx context.Context, actingLease, command string) string {
	if actingLease == "" || !commandRunsGate(command) {
		return ""
	}
	me, ok := actingLiveLease(ctx, actingLease)
	if !ok || me.Validation == "" || validationNamesGate(me.Validation) {
		return ""
	}
	return fmt.Sprintf(
		"magus workspace: run `%s` instead, which is the check lease %s was assigned. The orchestrator gates once, in its own tree, after every unit lands.\n"+
			"`%s` runs the `%s` gate, and the validation field on lease %s's ledger row reads %q, which does not name it. If this lease really owns the gate, widen that field with the "+hint.ToolLedger.String()+" tool and retry.",
		me.Validation, me.ID, command, types.TargetCI, me.ID, me.Validation)
}

// actingLiveLease reads the acting lease's own live row, reporting none whenever the
// ledger cannot answer. An unreadable ledger and an id nobody declared are one silence
// here: both leave nothing to judge against, and a rule the guard cannot evaluate must
// not block a tool call.
func actingLiveLease(ctx context.Context, actingLease string) (types.Lease, bool) {
	if !types.ValidLeaseID(actingLease) {
		return types.Lease{}, false
	}
	location := hookActivityTrail(ctx)
	if location.base == "" {
		return types.Lease{}, false
	}
	leases, err := ledger.NewStore(ledger.Location{CacheDir: location.base, Root: location.workspace}).List()
	if err != nil {
		return types.Lease{}, false
	}
	return liveLease(liveLeases(leases), actingLease)
}

// validationNamesGate reports whether a lease's declared validation IS the gate, in which
// case the lease owns it and nothing is refused.
//
// Read as words rather than through parseGuardCommands: the field is a declaration a
// person wrote, and `ci`, `magus run ci` and `affected ci` are all things they write. A
// stray `ci` elsewhere in the field reads as ownership and clears the deny, which is the
// direction this rule fails in on purpose.
func validationNamesGate(validation string) bool {
	for _, word := range strings.Fields(validation) {
		if t, err := types.ParseTarget(word); err == nil && t.Name == types.TargetCI {
			return true
		}
	}
	return false
}
