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

func TestWorkspaceRunsDirDefaultsToTheWorkspaceCache(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	assert.Empty(t, workspaceRunsDir(""), "no cache dir yet is no run log")

	require.NoError(t, os.MkdirAll(filepath.Join(root, ".magus", "runs"), 0o755))
	assert.Equal(t, filepath.Join(".magus", "runs"), workspaceRunsDir(""))
}

// narrowLease is a delegated worker assigned one package's tests: the shape the
// multi-agent skill hands out, and the shape the gate deny is scoped to.
func narrowLease() types.Lease {
	return types.Lease{
		ID:         "harness/lease-scoped-deny",
		Goal:       "lease-scoped denies in the guard",
		OwnedPaths: []string{"cmd/magus/**"},
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

// TestLeaseFromTree pins the channel a worker in its own worktree reaches the hook
// through: a marker in the checkout's cache dir, honored only when it holds a lease id.
func TestLeaseFromTree(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())
	base := hookActivityTrail(ctx).base

	assert.Empty(t, leaseFromTree(ctx), "no marker, no lease")

	require.NoError(t, os.WriteFile(filepath.Join(base, leaseMarkerName), []byte(" harness/lease-scoped-deny \n"), 0o644))
	assert.Equal(t, "harness/lease-scoped-deny", leaseFromTree(ctx))

	require.NoError(t, os.WriteFile(filepath.Join(base, leaseMarkerName), []byte("not a lease id!\n"), 0o644))
	assert.Empty(t, leaseFromTree(ctx), "a malformed marker binds nothing rather than something")
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
		"git push origin main",
		"git stash push -u -m wip",
		"git reset --hard HEAD",
		"git clean -fd",
		"git worktree remove ../x",
		"git checkout .",
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
