package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRun plants one invocation log: a started event carrying argv, with the file's
// modification time standing in for when the run finished.
func writeRun(t *testing.T, dir, id string, started time.Time, dur time.Duration, args ...string) {
	t.Helper()
	quoted := ""
	for i, a := range args {
		if i > 0 {
			quoted += ","
		}
		quoted += fmt.Sprintf("%q", a)
	}
	line := fmt.Sprintf(`{"ts":%d,"inv":%q,"kind":"started","command":{"arguments":[%s],"trigger":"run"}}`,
		started.UnixMilli(), id, quoted)
	path := filepath.Join(dir, id+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte(line+"\n"), 0o644))
	finished := started.Add(dur)
	require.NoError(t, os.Chtimes(path, finished, finished))
}

func TestIsGateCommand(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"affected", "ci"}, true},
		{[]string{"affected", "ci", "--no-default-charms"}, true},
		{[]string{"run", "ci", "."}, true},
		{[]string{"run", "ci:rw", "."}, true},
		{[]string{"-v", "affected", "ci"}, true},
		{[]string{"affected", "--timeout", "5m", "ci"}, true},
		{[]string{"run", "ci-shard", "."}, false},
		{[]string{"run", "test", "."}, false},
		// The false positive a regex over the raw command line could not avoid: a
		// trailing test filter that happens to be the word ci.
		{[]string{"run", "go::go-test", ".", "--", "-run", "ci"}, false},
		{[]string{"ls", "targets", "."}, false},
	} {
		assert.Equal(t, tc.want, isGateCommand(tc.args), "%v", tc.args)
	}
}

// The rule fires on the GATE, not on anything that spawns work. The first version
// gated on the same "spawns work" pattern the contention rule uses, so it advised
// reaching for a narrower target while the caller was running one.
func TestCommandRunsGate(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{"magus affected ci", true},
		{"./magus affected ci --no-default-charms", true},
		{"magus run ci .", true},
		{"mise exec -- ./magus affected ci", true},
		{"git commit -m x && magus affected ci", true},
		// The narrow targets the advisory itself recommends.
		{"magus run test .", false},
		{"./magus run go::go-test . --silent -- ./cmd/magus/", false},
		{"magus run lint .", false},
		{"magus ls targets .", false},
		// Prose naming the gate is not an invocation of it.
		{`git commit -m "run ci before pushing"`, false},
		{"", false},
	} {
		assert.Equal(t, tc.want, commandRunsGate(tc.command), "%q", tc.command)
	}
}

// One log file is one invocation, so there is nothing to cluster. The old shape
// inferred invocations from per-spell timestamps and reported one gate as several.
func TestRecentGateRunsCountsOneInvocationPerLog(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// A real gate spans minutes, which is what made timestamp clustering wrong.
	writeRun(t, dir, "inv1", now.Add(-40*time.Minute), 158*time.Second, "affected", "ci")
	writeRun(t, dir, "inv2", now.Add(-10*time.Minute), 150*time.Second, "affected", "ci")

	runs, spent := recentGateRuns(dir, now)
	assert.Equal(t, 2, runs, "two logs, two invocations, however long each ran")
	// Rounded: the log stores milliseconds and the file's modification time keeps
	// nanoseconds, so the two disagree below the precision anyone reports.
	assert.Equal(t, 308*time.Second, spent.Round(time.Second),
		"wall clock, not a sum of per-project samples")
}

func TestRecentGateRunsIgnoresOtherCommands(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRun(t, dir, "inv1", now.Add(-5*time.Minute), time.Minute, "run", "test", ".")
	writeRun(t, dir, "inv2", now.Add(-4*time.Minute), time.Minute, "graph", "build")

	runs, _ := recentGateRuns(dir, now)
	assert.Zero(t, runs)
}

// Yesterday's runs say nothing about today's session.
func TestRecentGateRunsIgnoresRunsOutsideTheWindow(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRun(t, dir, "old", now.Add(-gateRepeatWindow-time.Hour), time.Minute, "affected", "ci")

	runs, _ := recentGateRuns(dir, now)
	assert.Zero(t, runs)
}

// Frequency alone is not waste. A repeat gate over an unchanged tree is mostly cache
// hits and finishes in seconds: the cache working, not something to advise about.
func TestAdviseRepeatGateIgnoresCheapRepeats(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i := range 6 {
		writeRun(t, dir, fmt.Sprintf("inv%d", i), now.Add(-time.Duration(i)*time.Minute), 2*time.Second, "affected", "ci")
	}
	full, brief := adviseRepeatGate(dir, now)
	assert.Empty(t, full, "six cached gates cost seconds")
	assert.Empty(t, brief)
}

func TestAdviseRepeatGateFiresOnceItHasCost(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i := range 3 {
		writeRun(t, dir, fmt.Sprintf("inv%d", i), now.Add(-time.Duration(i*10)*time.Minute), 150*time.Second, "affected", "ci")
	}
	got, brief := adviseRepeatGate(dir, now)
	assert.Contains(t, got, "3 times")
	assert.Contains(t, got, "7m30s")
	assert.Contains(t, got, "magus ls targets", "the advisory must not name a target")

	// The repeat keeps the two facts that moved since the caller last read the full text,
	// and the command that acts on them. A repeat nobody can act on is noise.
	assert.Contains(t, brief, "3 times")
	assert.Contains(t, brief, "7m30s")
	assert.Contains(t, brief, "magus ls targets")
	assert.Less(t, len(brief), len(got)/2, "the repeat form must be substantially shorter than the full text")
}

// A single run is the practice working, not something to interrupt.
func TestAdviseRepeatGateIsSilentForOneRun(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRun(t, dir, "inv1", now.Add(-time.Minute), 10*time.Minute, "affected", "ci")

	full, _ := adviseRepeatGate(dir, now)
	assert.Empty(t, full)
}

// No run log is no data, not zero runs.
func TestAdviseRepeatGateIsSilentWithoutARunLog(t *testing.T) {
	absent, _ := adviseRepeatGate("", time.Now())
	assert.Empty(t, absent)
	missing, _ := adviseRepeatGate(filepath.Join(t.TempDir(), "absent"), time.Now())
	assert.Empty(t, missing)
}

func TestWorkspaceRunsDirNeedsAResolvedCacheDir(t *testing.T) {
	root := t.TempDir()
	assert.Empty(t, workspaceRunsDir(""),
		"an unresolved cache dir is no run log: the literal name is relative, and this hook's own directory is the one thing it must not stand for")

	cacheDir := filepath.Join(root, ".magus")
	assert.Empty(t, workspaceRunsDir(cacheDir), "no runs dir yet is no run log")

	require.NoError(t, os.MkdirAll(filepath.Join(cacheDir, "runs"), 0o755))
	assert.Equal(t, filepath.Join(cacheDir, "runs"), workspaceRunsDir(cacheDir))
}

// narrowLease is a delegated worker assigned one package's tests: the shape the
// multi-agent skill hands out, and the shape the gate deny is scoped to.
func narrowLease() types.Lease {
	return types.Lease{
		ID:         "harness/lease-scoped-deny",
		Goal:       "lease-scoped denies in the guard",
		WritePaths: []string{"cmd/magus/**"},
		Validation: "magus run go::go-test . -- ./internal/ledger/",
		State:      types.StateRunning,
		Registered: 1,
	}
}

// TestDenyLeaseScopedGate pins the refusal and what it has to carry: the lease, the
// command, and the row field that decided it, so a reader can repair the row instead
// of routing around the guard.
func TestDenyLeaseScopedGate(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())

	for _, command := range []string{
		"./magus affected ci --no-default-charms",
		"magus run ci .",
		"mise exec -- ./magus affected ci",
	} {
		reason := denyLeaseScopedGate(ctx, Deps{}, "harness/lease-scoped-deny", command)
		require.NotEmpty(t, reason, "%q", command)
		assert.Contains(t, reason, "harness/lease-scoped-deny", "the denial must name the lease")
		assert.Contains(t, reason, command, "the denial must name what it refused")
		assert.Contains(t, reason, "validation", "the denial must name the field that decided it")
		assert.Contains(t, reason, "./internal/ledger/", "the denial must hand back the check to run instead")
	}

	// A bound row that declares NO check is refused too: an empty field says nobody wrote
	// down what this unit should run, which is a reason to ask rather than a licence to
	// run everything. See the doc comment on denyLeaseScopedGate.
	t.Run("a row that declared no check at all", func(t *testing.T) {
		lease := narrowLease()
		lease.Validation = ""
		undeclared, _ := fleetFixture(t, lease)
		reason := denyLeaseScopedGate(undeclared, Deps{}, lease.ID, "./magus affected ci")
		require.NotEmpty(t, reason)
		assert.Contains(t, reason, lease.ID, "the denial must name the lease")
		assert.Contains(t, reason, "declares no check", "the denial must say why: nothing was recorded to run instead")
		assert.Contains(t, reason, "record a check on this row", "the denial must name a next step")
	})
}

// TestDenyLeaseScopedGateStaysQuiet covers every silence. The rule is a seatbelt for
// harnesses that opt in: each of these is a case where nothing declared the gate to be
// out of scope, and a guard that cannot evaluate a rule must not block a tool call.
func TestDenyLeaseScopedGateStaysQuiet(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())

	t.Run("no lease", func(t *testing.T) {
		assert.Empty(t, denyLeaseScopedGate(ctx, Deps{}, "", "./magus affected ci"))
	})

	t.Run("a lease with no row", func(t *testing.T) {
		assert.Empty(t, denyLeaseScopedGate(ctx, Deps{}, "harness/absent", "./magus affected ci"))
	})

	t.Run("a terminal row", func(t *testing.T) {
		lease := narrowLease()
		lease.State = types.StatePass
		done, _ := fleetFixture(t, lease)
		assert.Empty(t, denyLeaseScopedGate(done, Deps{}, lease.ID, "./magus affected ci"))
	})

	t.Run("a row whose validation names the gate", func(t *testing.T) {
		for _, validation := range []string{"ci", "magus affected ci", "magus run ci ."} {
			lease := narrowLease()
			lease.Validation = validation
			owns, _ := fleetFixture(t, lease)
			assert.Empty(t, denyLeaseScopedGate(owns, Deps{}, lease.ID, "./magus affected ci"), "%q", validation)
		}
	})

	t.Run("a command that is not the gate", func(t *testing.T) {
		assert.Empty(t, denyLeaseScopedGate(ctx, Deps{}, "harness/lease-scoped-deny", "./magus run go-build ."))
		assert.Empty(t, denyLeaseScopedGate(ctx, Deps{}, "harness/lease-scoped-deny", "./magus run go::go-test . -- -run Ci ./cmd/magus/"))
	})

	t.Run("no trail location", func(t *testing.T) {
		// Pinned EMPTY rather than left unpinned, so the case cannot reach the developer's
		// own ledger and grade against whatever plan they are really running.
		nowhere := context.WithValue(t.Context(), hookActivityLocationKey{}, hookActivityLocation{})
		assert.Empty(t, denyLeaseScopedGate(nowhere, Deps{}, "harness/lease-scoped-deny", "./magus affected ci"))
	})
}

// TestActingLeaseFromMarker pins the channel a worker in its own worktree reaches the
// hook through: a marker in the checkout's cache dir, honored only when it holds a lease
// id, and read by the same ledger.ActingLease the sandbox resolves through.
func TestActingLeaseFromMarker(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())
	base := hookActivityTrail(ctx, Deps{}).base

	assert.Empty(t, ledger.ActingLease(base), "no marker, no lease")

	require.NoError(t, os.WriteFile(filepath.Join(base, ledger.LeaseMarkerName), []byte(" harness/lease-scoped-deny \n"), 0o644))
	assert.Equal(t, "harness/lease-scoped-deny", ledger.ActingLease(base))

	require.NoError(t, os.WriteFile(filepath.Join(base, ledger.LeaseMarkerName), []byte("not a lease id!\n"), 0o644))
	assert.Empty(t, ledger.ActingLease(base), "a malformed marker binds nothing rather than something")
}

// TestDenyLeaseScopedVCS pins that a WORKER lease, a row with a parent, is refused the
// version-control mutations the orchestrator owns, with the lease and its parent named.
func TestDenyLeaseScopedVCS(t *testing.T) {
	worker := narrowLease()
	worker.Parent = "harness"
	ctx, _ := fleetFixture(t, worker)

	for _, command := range []string{
		"git commit -q -m done",
		"git -C /tmp/elsewhere commit -m done",
		"git --work-tree /tmp/elsewhere commit -m done",
		"git push origin main",
		"git stash push -u -m wip",
		"git reset --hard HEAD",
		"git clean -fd",
		"git worktree remove ../x",
		"git checkout .",
		"git restore .",
		"git revert HEAD",
		"git rebase main",
		"git merge main",
		"git cherry-pick abc123",
		"cd sub && git commit -m done",
	} {
		reason := denyLeaseScopedVCS(ctx, Deps{}, worker.ID, command)
		require.NotEmpty(t, reason, "%q", command)
		assert.Contains(t, reason, worker.ID)
		assert.Contains(t, reason, "harness", "the denial names the parent the worker belongs to")
	}

	for _, command := range []string{
		"git status --short",
		"git diff --stat",
		"git checkout -- go.mod",
		"git restore -- go.mod",
		"git stash list",
		"git stash show -p",
		"git log --oneline -3",
		"./magus run go::go-test . -- -run Guard ./cmd/magus/",
	} {
		assert.Empty(t, denyLeaseScopedVCS(ctx, Deps{}, worker.ID, command), "%q", command)
	}
}

// TestDenyLeaseScopedVCSStaysQuiet covers the silences: no lease, a root lease with
// no parent, and a lease nobody declared.
func TestDenyLeaseScopedVCSStaysQuiet(t *testing.T) {
	rootLease := narrowLease()
	ctx, _ := fleetFixture(t, rootLease)
	assert.Empty(t, denyLeaseScopedVCS(ctx, Deps{}, "", "git commit -m done"))
	assert.Empty(t, denyLeaseScopedVCS(ctx, Deps{}, rootLease.ID, "git commit -m done"), "a lease with no parent is the orchestrator's own")
	assert.Empty(t, denyLeaseScopedVCS(ctx, Deps{}, "harness/absent", "git commit -m done"))
}

// TestDenyLeaseScopedRebind pins the rebind rule: under a bound lease, the commands that
// rewrite who the caller is, or what its row says, are refused with the actor named.
func TestDenyLeaseScopedRebind(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())
	me := narrowLease().ID

	for command, what := range map[string]string{
		"magus session lease harness/other":                                "bind this checkout to another lease",
		"./magus session lease harness/other":                              "bind this checkout to another lease",
		"magus -s session lease harness/other":                             "bind this checkout to another lease",
		"magus ledger accept harness/other":                                "grade a lease row",
		"magus ledger register":                                            "write a lease row",
		"magus_ledger op=clear":                                            "drop every ledger row",
		"magus_ledger op=put id=harness/other owned_paths=**":              "write another lease's ledger row",
		"magus_ledger op=register id=harness/other":                        "write another lease's ledger row",
		"magus_ledger op=put id=harness/lease-scoped-deny owned_paths=**":  "rewrite its own ledger row",
		"magus_ledger op=put id=harness/lease-scoped-deny read_only=false": "rewrite its own ledger row",
	} {
		reason := denyLeaseScopedRebind(ctx, Deps{}, me, command)
		require.NotEmpty(t, reason, "%q", command)
		assert.Contains(t, reason, what, "the denial must say what the command would do")
		assert.Contains(t, reason, me, "the denial must name the bound lease")
		assert.Contains(t, reason, "Your orchestrator can", "the denial must name the actor")
	}
}

// TestDenyLeaseScopedRebindStaysQuiet covers every silence. A read is not a rebind, an
// unbound caller is the party that writes rows, and `op=register` is the worker's own
// procedure, demanded by the checkpoint denial on the write surface.
func TestDenyLeaseScopedRebindStaysQuiet(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())
	me := narrowLease().ID

	for name, command := range map[string]string{
		"reading the binding":       "magus session lease",
		"reading the plan":          "magus ledger ls",
		"reading one row":           "magus ledger brief harness/lease-scoped-deny",
		"recording its own base":    "magus_ledger op=register id=harness/lease-scoped-deny reported_base=abc123",
		"listing rows over MCP":     "magus_ledger op=list",
		"an unrelated magus verb":   "magus run go-build .",
		"a lease id in an argument": "magus query \"session lease\"",
		// A flag's value is a bare word, so this reads as a subcommand token and matches
		// nothing. The rule fails to fire rather than firing on a path that happened to
		// end in a verb, which is the safe direction; see magusSubcommandWords.
		"a value-taking global flag": "magus --root /tmp/x session lease harness/other",
	} {
		assert.Empty(t, denyLeaseScopedRebind(ctx, Deps{}, me, command), name)
	}

	assert.Empty(t, denyLeaseScopedRebind(ctx, Deps{}, "", "magus session lease harness/other"),
		"an unbound caller is the orchestrator or the person, and they are who writes rows")
}

// TestLedgerToolRebindLetsALaneBeGivenBack pins the one put a bound caller may make:
// giving a declaration back. The direction is what the guard judges; whether a particular
// shrink is legitimate belongs to the store.
func TestLedgerToolRebindLetsALaneBeGivenBack(t *testing.T) {
	wide := narrowLease()
	wide.WritePaths = []string{"cmd/magus/**", "internal/hint/**"}
	ctx, _ := fleetFixture(t, wide)

	assert.Empty(t, denyLeaseScopedRebind(ctx, Deps{}, wide.ID,
		"magus_ledger op=put id="+wide.ID+" write_paths=cmd/magus/**"),
		"dropping one of its own declarations cannot widen a role")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, Deps{}, wide.ID,
		"magus_ledger op=put id="+wide.ID+" write_paths=cmd/magus/**,internal/hint/**,docs/**"),
		"adding a declaration is a widen however it is spelled")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, Deps{}, wide.ID,
		"magus_ledger op=put id="+wide.ID+" write_paths=cmd/**"),
		"a pattern that happens to cover less is not a shrink this rule will try to prove")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, Deps{}, wide.ID,
		"magus_ledger op=put id="+wide.ID+" write_paths=cmd/magus/** validation=magus affected ci"),
		"a shrink carrying another field is not a shrink")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, Deps{}, wide.ID,
		"magus_ledger op=put id="+wide.ID+" write_paths=cmd/magus/** checkpoint=deadbeef"),
		"the checkpoint is the base this lease's work is graded against, and giving a lane back is not cover for moving it")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, Deps{}, wide.ID,
		"magus_ledger op=put id="+wide.ID+" write_paths=cmd/magus/** owned_paths=cmd/magus/**"),
		"both spellings at once leaves nothing saying which the store would apply")
}

// TestActingLeaseStandingSeparatesTheThreeAnswers is what the undeclared and terminal
// rules are built on: "not declared" and "could not be read" are different facts, and only
// the first is the caller's mistake.
func TestActingLeaseStandingSeparatesTheThreeAnswers(t *testing.T) {
	done := narrowLease()
	done.ID, done.State = "harness/finished", types.StatePass
	ctx, _ := fleetFixture(t, narrowLease(), done)

	live := actingLeaseStanding(ctx, Deps{}, narrowLease().ID)
	assert.True(t, live.readable)
	assert.True(t, live.declared)
	assert.False(t, live.terminal(), "a running row is not terminal")

	finished := actingLeaseStanding(ctx, Deps{}, done.ID)
	assert.True(t, finished.declared)
	assert.True(t, finished.terminal())

	absent := actingLeaseStanding(ctx, Deps{}, "harness/typo")
	assert.True(t, absent.readable, "the ledger answered; it just does not carry that id")
	assert.False(t, absent.declared)

	nowhere := context.WithValue(t.Context(), hookActivityLocationKey{}, hookActivityLocation{})
	assert.False(t, actingLeaseStanding(nowhere, Deps{}, narrowLease().ID).readable,
		"no workspace is a rule the guard cannot evaluate, not an undeclared id")
	assert.False(t, actingLeaseStanding(ctx, Deps{}, "").readable, "no lease is nothing to look up")
}

// TestIsGateCommandIgnoresTheReportingForms pins the carve-out the structural breadcrumb
// test found: these print what the gate WOULD do and run no target, so neither the deny
// nor the cost advisory has anything to say about them. `magus affected ci --plan` is a
// command magus itself serves as a next step.
func TestIsGateCommandIgnoresTheReportingForms(t *testing.T) {
	for _, args := range [][]string{
		{"affected", "ci", "--plan"},
		{"affected", "ci", "--impact"},
		{"affected", "--explain", "docs", "ci"},
		{"run", "ci", ".", "--dry-run"},
	} {
		assert.False(t, isGateCommand(args), "%v prints a report and runs nothing", args)
	}
	assert.True(t, isGateCommand([]string{"affected", "ci", "--no-default-charms"}),
		"the gate itself still is the gate")
}

// TestLedgerToolRebindIsSilentWhenTheLedgerCannotAnswer: the rule read the acting lease's
// LIVE row, which is absent for a terminal row and for an unreadable ledger alike, so a
// session writing its OWN finished row was refused with a reason naming somebody else's,
// in the same verdict that says the lease-scoped denials are not running for it.
func TestLedgerToolRebindIsSilentWhenTheLedgerCannotAnswer(t *testing.T) {
	done := narrowLease()
	done.State = types.StatePass
	ctx, _ := fleetFixture(t, done)

	assert.Empty(t, denyLeaseScopedRebind(ctx, Deps{}, done.ID,
		"magus_ledger op=put id="+done.ID+" write_paths=**"),
		"a terminal row has no boundary left, so naming another lease's row would be false")

	nowhere := context.WithValue(t.Context(), hookActivityLocationKey{}, hookActivityLocation{})
	assert.Empty(t, denyLeaseScopedRebind(nowhere, Deps{}, done.ID, "magus_ledger op=put id="+done.ID),
		"a ledger the guard cannot read leaves nothing to judge against")
}
