package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/internal/trail"
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
		reason := denyLeaseScopedGate(ctx, "harness/lease-scoped-deny", command)
		require.NotEmpty(t, reason, "%q", command)
		assert.Contains(t, reason, "harness/lease-scoped-deny", "the denial must name the lease")
		assert.Contains(t, reason, command, "the denial must name what it refused")
		assert.Contains(t, reason, "validation", "the denial must name the field that decided it")
		assert.Contains(t, reason, "./internal/ledger/", "the denial must hand back the check to run instead")
	}
}

// TestDenyLeaseScopedGateStaysQuiet covers every silence. The rule is a seatbelt for
// harnesses that opt in: each of these is a case where nothing declared the gate to be
// out of scope, and a guard that cannot evaluate a rule must not block a tool call.
func TestDenyLeaseScopedGateStaysQuiet(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())

	t.Run("no lease", func(t *testing.T) {
		assert.Empty(t, denyLeaseScopedGate(ctx, "", "./magus affected ci"))
	})

	t.Run("a lease with no row", func(t *testing.T) {
		assert.Empty(t, denyLeaseScopedGate(ctx, "harness/absent", "./magus affected ci"))
	})

	t.Run("a terminal row", func(t *testing.T) {
		lease := narrowLease()
		lease.State = types.StatePass
		done, _ := fleetFixture(t, lease)
		assert.Empty(t, denyLeaseScopedGate(done, lease.ID, "./magus affected ci"))
	})

	t.Run("a row that declared no validation", func(t *testing.T) {
		lease := narrowLease()
		lease.Validation = ""
		undeclared, _ := fleetFixture(t, lease)
		assert.Empty(t, denyLeaseScopedGate(undeclared, lease.ID, "./magus affected ci"))
	})

	t.Run("a row whose validation names the gate", func(t *testing.T) {
		for _, validation := range []string{"ci", "magus affected ci", "magus run ci ."} {
			lease := narrowLease()
			lease.Validation = validation
			owns, _ := fleetFixture(t, lease)
			assert.Empty(t, denyLeaseScopedGate(owns, lease.ID, "./magus affected ci"), "%q", validation)
		}
	})

	t.Run("a command that is not the gate", func(t *testing.T) {
		assert.Empty(t, denyLeaseScopedGate(ctx, "harness/lease-scoped-deny", "./magus run go-build ."))
		assert.Empty(t, denyLeaseScopedGate(ctx, "harness/lease-scoped-deny", "./magus run go::go-test . -- -run Ci ./cmd/magus/"))
	})

	t.Run("no trail location", func(t *testing.T) {
		// Pinned EMPTY rather than left unpinned, so the case cannot reach the developer's
		// own ledger and grade against whatever plan they are really running.
		nowhere := context.WithValue(t.Context(), hookActivityLocationKey{}, hookActivityLocation{})
		assert.Empty(t, denyLeaseScopedGate(nowhere, "harness/lease-scoped-deny", "./magus affected ci"))
	})
}

// TestHookCmdDeniesTheGateUnderANarrowLease proves the WIRING. The rule itself is covered
// above; what this pins is that hookCmd reaches it, because a rule nothing calls never
// fires however well it is tested.
func TestHookCmdDeniesTheGateUnderANarrowLease(t *testing.T) {
	global = globalFlags{}
	// The hook falls back to the environment for the lease, so a developer or CI job that
	// exported one would decide the control case below.
	t.Setenv(trail.EnvBaggage, "")
	ctx, _ := fleetFixture(t, narrowLease())
	command := "./magus affected ci --no-default-charms"

	var denied bytes.Buffer
	err := hookCmd(ctx, strings.NewReader(command), &denied,
		[]string{"--lease", "harness/lease-scoped-deny", "-o", "name"})
	require.Error(t, err, "a deny that exits 0 blocks nothing: the host runs the command anyway")
	assert.Equal(t, "deny\n", denied.String())

	var unleased bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(command), &unleased, []string{"-o", "name"}))
	assert.Equal(t, "pass\n", unleased.String(), "a caller naming no lease is scoped by nobody's row")
}

// TestActingLeaseFromMarker pins the channel a worker in its own worktree reaches the
// hook through: a marker in the checkout's cache dir, honored only when it holds a lease
// id, and read by the same ledger.ActingLease the sandbox resolves through.
func TestActingLeaseFromMarker(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())
	base := hookActivityTrail(ctx).base

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
		reason := denyLeaseScopedVCS(ctx, worker.ID, command)
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
		assert.Empty(t, denyLeaseScopedVCS(ctx, worker.ID, command), "%q", command)
	}
}

// TestDenyLeaseScopedVCSStaysQuiet covers the silences: no lease, a root lease with
// no parent, and a lease nobody declared.
func TestDenyLeaseScopedVCSStaysQuiet(t *testing.T) {
	rootLease := narrowLease()
	ctx, _ := fleetFixture(t, rootLease)
	assert.Empty(t, denyLeaseScopedVCS(ctx, "", "git commit -m done"))
	assert.Empty(t, denyLeaseScopedVCS(ctx, rootLease.ID, "git commit -m done"), "a lease with no parent is the orchestrator's own")
	assert.Empty(t, denyLeaseScopedVCS(ctx, "harness/absent", "git commit -m done"))
}

// TestHookEnvelopeCwdLocatesTheWorkersCheckout pins the channel a host that runs its hooks
// somewhere else reaches the worker's marker through: the envelope's cwd names the worker's
// checkout, and the lease bound there scopes the verdict, whatever the hook process's own
// directory is.
func TestHookEnvelopeCwdLocatesTheWorkersCheckout(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	global = globalFlags{}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(""), 0o644))
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	worker := narrowLease()
	worker.Parent = "harness"
	_, err = ledger.NewStore(ledger.Location{CacheDir: cacheDir, Root: root}).Put(t.Context(), worker)
	require.NoError(t, err)
	require.NoError(t, ledger.BindLease(cacheDir, worker.ID))

	envelope := fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s1","cwd":%q,"tool_input":{"command":"git commit -m done"}}`, root)
	var out bytes.Buffer
	err = hookCmd(context.Background(), strings.NewReader(envelope), &out, []string{"-o", "name"})
	var silent errSilent
	require.ErrorAs(t, err, &silent, "the worker lease bound in the envelope's checkout must deny the commit")
	assert.Equal(t, "deny\n", out.String())

	events, err := trail.ReadRecent(cacheDir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1, "the observation lands in the worker's trail, not the hook's cwd")
	assert.Equal(t, worker.ID, events[0].Lease)
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
		reason := denyLeaseScopedRebind(ctx, me, command)
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
		assert.Empty(t, denyLeaseScopedRebind(ctx, me, command), name)
	}

	assert.Empty(t, denyLeaseScopedRebind(ctx, "", "magus session lease harness/other"),
		"an unbound caller is the orchestrator or the person, and they are who writes rows")
}

// TestLedgerToolRebindLetsALaneBeGivenBack pins the one put a bound caller may make:
// giving a declaration back. The direction is what the guard judges; whether a particular
// shrink is legitimate belongs to the store.
func TestLedgerToolRebindLetsALaneBeGivenBack(t *testing.T) {
	wide := narrowLease()
	wide.WritePaths = []string{"cmd/magus/**", "internal/hint/**"}
	ctx, _ := fleetFixture(t, wide)

	assert.Empty(t, denyLeaseScopedRebind(ctx, wide.ID,
		"magus_ledger op=put id="+wide.ID+" owned_paths=cmd/magus/**"),
		"dropping one of its own declarations cannot widen a role")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, wide.ID,
		"magus_ledger op=put id="+wide.ID+" owned_paths=cmd/magus/**,internal/hint/**,docs/**"),
		"adding a declaration is a widen however it is spelled")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, wide.ID,
		"magus_ledger op=put id="+wide.ID+" owned_paths=cmd/**"),
		"a pattern that happens to cover less is not a shrink this rule will try to prove")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, wide.ID,
		"magus_ledger op=put id="+wide.ID+" owned_paths=cmd/magus/** validation=magus affected ci"),
		"a shrink carrying another field is not a shrink")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, wide.ID,
		"magus_ledger op=put id="+wide.ID+" owned_paths=cmd/magus/** checkpoint=deadbeef"),
		"the checkpoint is the base this lease's work is graded against, and giving a lane back is not cover for moving it")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, wide.ID,
		"magus_ledger op=put id="+wide.ID+" owned_paths=cmd/magus/** write_paths=cmd/magus/**"),
		"both spellings at once leaves nothing saying which the store would apply")
}

// TestHookCmdJudgesTheMCPLedgerSurface is the decision table for the transport the CLI
// rules would otherwise miss: the same envelope a host forwards for an MCP tool call.
func TestHookCmdJudgesTheMCPLedgerSurface(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	wide := narrowLease()
	wide.WritePaths = []string{"cmd/magus/**", "internal/hint/**"}
	ctx, _ := fleetFixture(t, wide)

	for name, tc := range map[string]struct {
		toolInput string
		want      string
	}{
		"put on another row":    {`{"op":"put","id":"harness/other","owned_paths":"**"}`, "deny\n"},
		"put widening its own":  {`{"op":"put","id":"` + wide.ID + `","owned_paths":["**"]}`, "deny\n"},
		"clearing the board":    {`{"op":"clear"}`, "deny\n"},
		"register elsewhere":    {`{"op":"register","id":"harness/other"}`, "deny\n"},
		"register its own base": {`{"op":"register","id":"` + wide.ID + `","reported_base":"abc123"}`, "pass\n"},
		"listing the plan":      {`{"op":"list"}`, "pass\n"},
		"giving a lane back":    {`{"op":"put","id":"` + wide.ID + `","owned_paths":["cmd/magus/**"]}`, "pass\n"},
		// The rewrite the rendered line used to drop on the floor: judged only on the keys
		// the renderer carried, a shrink beside a forged checkpoint read as a plain shrink.
		"forging its own base": {`{"op":"put","id":"` + wide.ID + `","owned_paths":["cmd/magus/**"],"checkpoint":"deadbeef"}`, "deny\n"},
		"rewriting its goal":   {`{"op":"put","id":"` + wide.ID + `","owned_paths":["cmd/magus/**"],"goal":"something else"}`, "deny\n"},
	} {
		envelope := `{"hook_event_name":"PreToolUse","session_id":"mcp-` + name +
			`","tool_name":"mcp__magus__magus_ledger","tool_input":` + tc.toolInput + `}`
		var out bytes.Buffer
		err := hookCmd(ctx, strings.NewReader(envelope), &out, []string{"--lease", wide.ID, "-o", "name"})
		if tc.want == "deny\n" {
			require.Error(t, err, name)
		} else {
			require.NoError(t, err, name)
		}
		assert.Equal(t, tc.want, out.String(), name)
	}
}

// TestActingLeaseStandingSeparatesTheThreeAnswers is what the undeclared and terminal
// rules are built on: "not declared" and "could not be read" are different facts, and only
// the first is the caller's mistake.
func TestActingLeaseStandingSeparatesTheThreeAnswers(t *testing.T) {
	done := narrowLease()
	done.ID, done.State = "harness/finished", types.StatePass
	ctx, _ := fleetFixture(t, narrowLease(), done)

	live := actingLeaseStanding(ctx, narrowLease().ID)
	assert.True(t, live.readable)
	assert.True(t, live.declared)
	assert.False(t, live.terminal(), "a running row is not terminal")

	finished := actingLeaseStanding(ctx, done.ID)
	assert.True(t, finished.declared)
	assert.True(t, finished.terminal())

	absent := actingLeaseStanding(ctx, "harness/typo")
	assert.True(t, absent.readable, "the ledger answered; it just does not carry that id")
	assert.False(t, absent.declared)

	nowhere := context.WithValue(t.Context(), hookActivityLocationKey{}, hookActivityLocation{})
	assert.False(t, actingLeaseStanding(nowhere, narrowLease().ID).readable,
		"no workspace is a rule the guard cannot evaluate, not an undeclared id")
	assert.False(t, actingLeaseStanding(ctx, "").readable, "no lease is nothing to look up")
}

// TestHookCmdErrorsOnAnUndeclaredLease is C3: an id nobody declared bought SILENT
// un-enrolled treatment, so a worker whose orchestrator typo'd the id ran unguarded and
// looked exactly like a guarded one.
func TestHookCmdErrorsOnAnUndeclaredLease(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	ctx, _ := fleetFixture(t, narrowLease())

	var out bytes.Buffer
	err := hookCmd(ctx, strings.NewReader("ls"), &out, []string{"--lease", "harness/typo", "-o", "json"})
	var silent errSilent
	require.ErrorAs(t, err, &silent, "an assertion that does not resolve must not exit 0")
	assert.Equal(t, guardDenyExitCode, silent.exitCode)
	assert.Contains(t, out.String(), `"decision": "deny"`)
	assert.Contains(t, out.String(), "is not declared")
	assert.Contains(t, out.String(), `"lease": "harness/typo"`, "the verdict names the row it graded against")
}

// TestHookCmdNoticesATerminalLease covers the other half: a row that has finished still
// names a session, and every rule keyed on it has quietly stopped applying.
func TestHookCmdNoticesATerminalLease(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	done := narrowLease()
	done.State = types.StatePass
	ctx, _ := fleetFixture(t, done)

	var first bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("ls"), &first,
		[]string{"--lease", done.ID, "--session", "session-terminal"}))
	assert.Contains(t, first.String(), "is in state pass")
	assert.Contains(t, first.String(), "its rules are inert")

	var second bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("ls"), &second,
		[]string{"--lease", done.ID, "--session", "session-terminal"}))
	assert.Equal(t, "pass\n", second.String(), "a standing fact is said once per session")
}

// TestHookVerdictCarriesTheActingLease pins the field on the wire, which is what lets a
// person see WHICH row decided a verdict.
func TestHookVerdictCarriesTheActingLease(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	ctx, _ := fleetFixture(t, narrowLease())

	var leased bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("ls"), &leased,
		[]string{"--lease", narrowLease().ID, "-o", "json"}))
	assert.Contains(t, leased.String(), `"lease": "harness/lease-scoped-deny"`)

	var unbound bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("ls"), &unbound, []string{"-o", "json"}))
	assert.NotContains(t, unbound.String(), `"lease"`, "a session nobody leased names no row")
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

	assert.Empty(t, denyLeaseScopedRebind(ctx, done.ID,
		"magus_ledger op=put id="+done.ID+" owned_paths=**"),
		"a terminal row has no boundary left, so naming another lease's row would be false")

	nowhere := context.WithValue(t.Context(), hookActivityLocationKey{}, hookActivityLocation{})
	assert.Empty(t, denyLeaseScopedRebind(nowhere, done.ID, "magus_ledger op=put id="+done.ID),
		"a ledger the guard cannot read leaves nothing to judge against")
}

// TestHookCmdAdvisesAnInvalidLeaseOnEverySurface: the notice lived inside gradeLeasedWrite,
// which runs on the path surface only, so a COMMAND under a typo'd id ran fully un-enrolled
// with nothing said about it.
func TestHookCmdAdvisesAnInvalidLeaseOnEverySurface(t *testing.T) {
	for name, args := range map[string][]string{
		"the command surface": {"--lease", "has spaces", "--session", "invalid-command", "-o", "json"},
		"the path surface":    {"--path", "--lease", "has spaces", "--session", "invalid-path", "-o", "json"},
	} {
		t.Run(name, func(t *testing.T) {
			global = globalFlags{}
			t.Setenv(trail.EnvBaggage, "")
			ctx, _ := fleetFixture(t, narrowLease())

			var out bytes.Buffer
			require.NoError(t, hookCmd(ctx, strings.NewReader("README.md"), &out, args))
			assert.Contains(t, out.String(), "is not a valid lease id")
		})
	}
}

// TestHookCmdRanksTheCacheDirAboveTheUndeclaredLease: the undeclared refusal used to return
// from hookCmd before anything else ran, so it outranked the cache-dir rule against both
// files' stated order and skipped the stale-binary notice at the tail.
func TestHookCmdRanksTheCacheDirAboveTheUndeclaredLease(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	ctx, _ := fleetFixture(t, narrowLease())

	var out bytes.Buffer
	err := hookCmd(ctx, strings.NewReader(".magus/lease"), &out,
		[]string{"--path", "--lease", "harness/nobody-declared-this", "-o", "json"})
	require.Error(t, err)
	assert.Contains(t, out.String(), "magus cache dir")
	assert.NotContains(t, out.String(), "is not declared",
		"the cache dir is what the write is about; whose lane it is comes second")
}
