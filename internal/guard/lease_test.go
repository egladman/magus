package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/gen/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
		{[]string{"run", "ci-lint", "."}, false},
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
	full, brief := adviseRepeatGate(dir, now)
	got := full.Say
	assert.Contains(t, got, "3 times")
	assert.Contains(t, got, "7m30s")
	// The cost, then the command that SIZES the decision. It names --plan rather than a
	// target because the plan is what says whether the gate is worth it; naming a target
	// would be magus choosing for the caller, and this tier cannot enforce that choice
	// anyway.
	assert.Contains(t, got, "--plan", "the advisory must hand back the command that sizes the risk")
	assert.NotContains(t, got, "magus ls targets", "the advisory must not name a target")

	// The repeat keeps the two facts that moved since the caller last read the full text,
	// and the command that acts on them. A repeat nobody can act on is noise.
	assert.Contains(t, brief, "3 times")
	assert.Contains(t, brief, "7m30s")
	assert.Contains(t, brief, "--plan")
	assert.Less(t, len(brief), len(got), "the repeat form must not be longer than the full text")
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
func narrowLease() types.Job {
	return types.Job{
		ID:         "harness/lease-scoped-deny",
		Criteria:   "lease-scoped denies in the guard",
		WritePaths: []string{"cmd/magus/**"},
		Validation: "magus run go::go-test . -- ./internal/ledger/",
		State:      types.StateRunning,
		Registered: 1,
	}
}

// TestDenyLeaseScopedGate pins the refusal and what it has to carry: the command and the
// check to run instead in its verdict, and the lease and the row field that decided it in
// its rationale, so a reader can repair the row instead of routing around the guard.
func TestDenyLeaseScopedGate(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())

	for _, command := range []string{
		"./magus affected ci --no-default-charms",
		"magus run ci .",
		"mise exec -- ./magus affected ci",
	} {
		reason := denyLeaseScopedGate(ctx, Dependencies{}, "harness/lease-scoped-deny", command)
		require.NotEmpty(t, reason, "%q", command)
		assert.Equal(t, "magus workspace: `"+command+"` is the orchestrator's gate; run your check: `magus run go::go-test . -- ./internal/ledger/`.", reason.Say,
			"the verdict names what it refused and hands back the check to run instead")
		assert.Contains(t, reason.Why, "harness/lease-scoped-deny", "the rationale must name the lease")
		assert.Contains(t, reason.Why, "the check on lease", "the rationale must name the field that decided it")
	}

	// A bound row that declares NO check is refused too: an empty field says nobody wrote
	// down what this unit should run, which is a reason to ask rather than a licence to
	// run everything. See the doc comment on denyLeaseScopedGate.
	t.Run("a row that declared no check at all", func(t *testing.T) {
		lease := narrowLease()
		lease.Validation = ""
		undeclared, _ := fleetFixture(t, lease)
		reason := denyLeaseScopedGate(undeclared, Dependencies{}, lease.ID, "./magus affected ci")
		require.NotEmpty(t, reason)
		assert.Contains(t, reason.Say, lease.ID, "the denial must name the lease")
		assert.Contains(t, reason.Say, "declares no check", "the denial must say why: nothing was recorded to run instead")
		assert.Contains(t, reason.Why, "record a check on this row", "the rationale must name who records one")
	})
}

// TestDenyLeaseScopedGateStaysQuiet covers every silence. The rule is a seatbelt for
// harnesses that opt in: each of these is a case where nothing declared the gate to be
// out of scope, and a guard that cannot evaluate a rule must not block a tool call.
func TestDenyLeaseScopedGateStaysQuiet(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())

	t.Run("no lease", func(t *testing.T) {
		assert.Empty(t, denyLeaseScopedGate(ctx, Dependencies{}, "", "./magus affected ci"))
	})

	t.Run("a lease with no row", func(t *testing.T) {
		assert.Empty(t, denyLeaseScopedGate(ctx, Dependencies{}, "harness/absent", "./magus affected ci"))
	})

	t.Run("a terminal row", func(t *testing.T) {
		lease := narrowLease()
		lease.State = types.StatePass
		done, _ := fleetFixture(t, lease)
		assert.Empty(t, denyLeaseScopedGate(done, Dependencies{}, lease.ID, "./magus affected ci"))
	})

	t.Run("a row whose validation names the gate", func(t *testing.T) {
		for _, validation := range []string{"ci", "magus affected ci", "magus run ci ."} {
			lease := narrowLease()
			lease.Validation = validation
			owns, _ := fleetFixture(t, lease)
			assert.Empty(t, denyLeaseScopedGate(owns, Dependencies{}, lease.ID, "./magus affected ci"), "%q", validation)
		}
	})

	// denyLeaseGate alone: every other target is worker-check-only's, graded below.
	t.Run("a command that is not the gate", func(t *testing.T) {
		assert.Empty(t, denyLeaseGate(ctx, Dependencies{}, "harness/lease-scoped-deny", "./magus run go-build ."))
		assert.Empty(t, denyLeaseGate(ctx, Dependencies{}, "harness/lease-scoped-deny", "./magus run go::go-test . -- -run Ci ./cmd/magus/"))
	})

	t.Run("no trail location", func(t *testing.T) {
		// Pinned EMPTY rather than left unpinned, so the case cannot reach the developer's
		// own job store and grade against whatever plan they are really running.
		nowhere := context.WithValue(t.Context(), locationKey{}, location{})
		assert.Empty(t, denyLeaseScopedGate(nowhere, Dependencies{}, "harness/lease-scoped-deny", "./magus affected ci"))
	})
}

// figuresLease is a figure worker whose one check regenerates diagrams: the row shape the
// 2026-09-29 incident ran under.
func figuresLease() types.Job {
	return types.Job{
		ID:         "figures/flow",
		Criteria:   "draw the flow figure",
		WritePaths: []string{"docs/figures/flow.md", "docs/gen/figures/flow.svg"},
		Check:      &types.LeaseCheck{Target: "diagrams_generate", Project: "docs"},
		State:      types.StateRunning,
		Registered: 1,
	}
}

// figuresWorkspace declares figures-generate as the producer of the row's figure. No other
// target the cases run declares an output among its write paths.
func figuresWorkspace(t *testing.T) Dependencies {
	t.Helper()
	ws := mocks.NewMockWorkspaceRepository(t)
	ws.EXPECT().ClassifyFiles(mock.Anything, mock.Anything).Return([]types.FileEntry{{
		Path: "docs/gen/figures/flow.svg", Project: "docs", Role: "output",
		Claims: []types.FileClaim{{Project: "docs", Target: "figures-generate", Role: "output", Glob: "gen/figures/*.svg"}},
	}}, nil).Maybe()
	return Dependencies{Inspect: func(context.Context, string) (types.WorkspaceRepository, error) { return ws, nil }}
}

// TestWorkerCheckOnly pins what a bound worker may run and what the refusal hands back: the
// check verbatim, the orchestrator as the one who runs the rest, and the check's command as
// the next.
func TestWorkerCheckOnly(t *testing.T) {
	row := figuresLease()
	ctx, _ := fleetFixture(t, row)
	deps := figuresWorkspace(t)

	for _, tt := range []struct {
		name, command string
		deny          bool
	}{
		{"the check", "./magus run diagrams_generate docs", false},
		{"the check with a charm and forwarded args", "./magus run diagrams-generate:rw docs --server-enabled=false -- --verbose", false},
		{"the check with a flag value before its project", "magus run diagrams-generate --timeout 5m docs", false},
		{"a target declaring an output in the write paths", "./magus run figures-generate:rw docs", false},
		{"a run that only reports", "./magus run lint docs --dry-run", false},
		{"a shard plan", "./magus affected ci --plan", false},
		{"a read", "./magus describe file docs/figures/flow.md", false},
		{"iteration in Buzz", "./magus buzz -t scratch.buzz", false},
		{"the job verbs", "./magus job exit figures/flow --stdin", false},
		{"lint", "./magus run lint docs", true},
		{"lint behind a wrapper", "mise exec -- ./magus run lint docs", true},
		{"the check's target on another project", "./magus run diagrams-generate .", true},
		{"a generator whose outputs are elsewhere", "./magus run generate:rw docs", true},
		{"affected", "./magus affected test", true},
		{"affected of the check's own target", "./magus affected diagrams-generate", true},
		{"a pipe whose second stage is not the check", "./magus run diagrams-generate docs | ./magus run lint docs", true},
		{"the binary rebuild, which the orchestrator owns", "./magus run go-build .", true},
	} {
		reason := denyWorkerCheckOnly(ctx, deps, row.ID, tt.command)
		if !tt.deny {
			assert.Empty(t, reason, tt.name)
			continue
		}
		require.NotEmpty(t, reason, tt.name)
		assert.NotContains(t, reason.Say, "\n", "%s: the verdict is one line", tt.name)
		assert.Contains(t, reason.Say, "run diagrams_generate docs`.", "%s: the verdict ends in the check's command", tt.name)
		assert.Contains(t, reason.Why, "The orchestrator runs every other target serially", tt.name)
	}

	assert.NotEmpty(t, denyLeaseScopedGate(ctx, deps, row.ID, "./magus run lint docs"), "the role-scoped entry reaches this rule")
	assert.Contains(t, denyLeaseScopedGate(ctx, deps, row.ID, "./magus affected ci").Why, "gates once", "the gate keeps its own refusal")
}

// TestWorkerCheckOnlyStaysQuiet covers every caller with nothing to hold a run to.
func TestWorkerCheckOnlyStaysQuiet(t *testing.T) {
	deps := figuresWorkspace(t)
	ctx, _ := fleetFixture(t, figuresLease())

	t.Run("an unbound caller", func(t *testing.T) {
		assert.Empty(t, denyWorkerCheckOnly(ctx, deps, "", "./magus run lint docs"))
	})
	t.Run("a lease with no row", func(t *testing.T) {
		assert.Empty(t, denyWorkerCheckOnly(ctx, deps, "figures/absent", "./magus run lint docs"))
	})
	for name, change := range map[string]func(*types.Job){
		"a terminal row":           func(j *types.Job) { j.State = types.StatePass },
		"a row that owns the gate": func(j *types.Job) { j.Check = &types.LeaseCheck{Target: "ci", Project: "."} },
		"a row declaring no check": func(j *types.Job) { j.Check = nil },
	} {
		t.Run(name, func(t *testing.T) {
			row := figuresLease()
			change(&row)
			ctx, _ := fleetFixture(t, row)
			assert.Empty(t, denyWorkerCheckOnly(ctx, deps, row.ID, "./magus run lint docs"))
		})
	}
	t.Run("a workspace that cannot load falls back to the producer spelling", func(t *testing.T) {
		row := figuresLease()
		assert.Empty(t, denyWorkerCheckOnly(ctx, Dependencies{}, row.ID, "./magus run generate:rw docs"))
		assert.Empty(t, denyWorkerCheckOnly(ctx, Dependencies{}, row.ID, "./magus run figures-generate docs"))
		assert.NotEmpty(t, denyWorkerCheckOnly(ctx, Dependencies{}, row.ID, "./magus run lint docs"))
	})
}

// TestWorkerCheckOnlyNextQuotesTheCheck: a forwarded pattern holding `|` is a pipe unless
// quoted, and the next is a command a reader runs as printed.
func TestWorkerCheckOnlyNextQuotesTheCheck(t *testing.T) {
	c := types.LeaseCheck{Target: "go::go-test", Project: ".", Args: []string{"-run", "Lease|CheckOnly", "./internal/guard/"}}
	assert.True(t, strings.HasSuffix(checkCommand(c), " run go::go-test . -- -run 'Lease|CheckOnly' ./internal/guard/"), checkCommand(c))

	c.NoDefaultCharms = true
	assert.True(t, strings.HasSuffix(checkCommand(c), " run go::go-test . --no-default-charms -- -run 'Lease|CheckOnly' ./internal/guard/"), checkCommand(c))
}

// TestIdentityLessCallersShareTheCheckoutRecord pins the one binding keyed on the checkout:
// a host that names no session (OpenCode's plugin hands the guard a bare command) has
// nothing else to key on, so every such caller in the checkout reads it, and no caller its
// host names ever does.
func TestIdentityLessCallersShareTheCheckoutRecord(t *testing.T) {
	t.Setenv("BAGGAGE", "")
	ctx, _ := fleetFixture(t, narrowLease())
	bindCaller(t, ctx, hookAttribution{Host: "opencode"}, narrowLease().ID)

	type graded struct {
		Lease string
		From  types.LeaseSource
	}
	got := map[string]graded{}
	for name, req := range map[string]Request{
		"an identity-less hook":      {Input: "ls", Host: "opencode"},
		"another identity-less hook": {Input: "ls"},
		"a session in the checkout":  {Input: "ls", Host: "claude-code", Session: "s1"},
		"a subagent in the checkout": {Input: "ls", Host: "claude-code", Session: "s1", Agent: "a1b2c3"},
	} {
		v := Judge(ctx, Dependencies{}, req)
		got[name] = graded{v.Lease, v.LeaseFrom}
	}
	assert.Equal(t, map[string]graded{
		"an identity-less hook":      {narrowLease().ID, types.LeaseSourceMarker},
		"another identity-less hook": {narrowLease().ID, types.LeaseSourceMarker},
		"a session in the checkout":  {},
		"a subagent in the checkout": {},
	}, got)

	base := hookLocation(ctx, Dependencies{}).cacheDir
	require.NoError(t, os.WriteFile(job.MarkerPath(base), []byte("not a lease id!\n"), 0o644))
	v := Judge(ctx, Dependencies{}, Request{Input: "ls"})
	assert.Equal(t, graded{}, graded{v.Lease, v.LeaseFrom}, "a record that does not read binds nobody")
	assert.Equal(t, "pass", v.Decision)
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
		"git --no-pager commit -m done",
		"git -P push",
		"git --git-dir=.git --work-tree=. stash",
		"git --config-env x=Y reset --hard",
		"git -c alias.x=commit x -m done",
		"hg --config alias.p=push p",
		"jj --config-toml 'x=1' --at-op @ git push",
		"hg --pag never push",
		"git push origin main",
		"hg push",
		"hg -R ../x push",
		"sl push --to main",
		"jj git push",
		"jj -R ../x git push -b main",
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
		"git -c core.pager=cat stash --help",
		"git stash --help | cat",
	} {
		reason := denyLeaseScopedVCS(ctx, Dependencies{}, worker.ID, command)
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
		"git stash --help",
		"git commit -h",
		"./magus run go::go-test . -- -run Guard ./cmd/magus/",
	} {
		assert.Empty(t, denyLeaseScopedVCS(ctx, Dependencies{}, worker.ID, command), "%q", command)
	}
}

// TestDenyLeaseScopedVCSStaysQuiet covers the silences: no lease, a parentless lease its
// root session holds, and a lease nobody declared.
func TestDenyLeaseScopedVCSStaysQuiet(t *testing.T) {
	rootLease := narrowLease()
	ctx, _ := fleetFixture(t, rootLease)
	root := Dependencies{caller: job.Caller{Host: "claude-code", Session: "s1"}}
	assert.Empty(t, denyLeaseScopedVCS(ctx, root, "", "git push"))
	assert.Empty(t, denyLeaseScopedVCS(ctx, root, rootLease.ID, "git push"), "a parentless lease the root session holds is the orchestrator's own")
	assert.Empty(t, denyLeaseScopedVCS(ctx, root, "harness/absent", "git push"))
}

// TestDenyLeaseScopedVCSGradesWorkersByHolder pins that a parentless row is no licence: a
// subagent holding it, or a caller that names no session, is a worker and may not push.
func TestDenyLeaseScopedVCSGradesWorkersByHolder(t *testing.T) {
	parentless := narrowLease()
	ctx, _ := fleetFixture(t, parentless)
	for holder, caller := range map[string]job.Caller{
		"a subagent":            {Host: "claude-code", Session: "s1", Agent: "a1b2c3"},
		"an identity-less hook": {},
	} {
		reason := denyLeaseScopedVCS(ctx, Dependencies{caller: caller}, parentless.ID, "git push origin HEAD")
		require.NotEmpty(t, reason, holder)
		assert.Contains(t, reason, "`git push`", holder)
	}
}

// leaseVCSRepo is a worker's checkout as `magus job exec` leaves it: a linked worktree on
// the job's own branch, its row a child of the orchestrator's and bound to that worktree.
func leaseVCSRepo(t *testing.T) (context.Context, worktreeRepo, types.Job, string) {
	t.Helper()
	r := newWorktreeRepo(t)
	wt := r.add("lease-job")
	worker := narrowLease()
	worker.Parent = "orchestrator"
	worker.CheckoutRoot = wt
	ctx, _ := fleetFixture(t, worker)
	return ctx, r, worker, wt
}

// TestDenyLeaseScopedVCSLetsAWorkerCommitInItsOwnCheckout pins every spelling of a commit
// that lands in the worker's own checkout, on its own branch, as allowed: from the hook's
// cwd, through git -C, after a cd, and by the git directory, from the environment or a flag.
func TestDenyLeaseScopedVCSLetsAWorkerCommitInItsOwnCheckout(t *testing.T) {
	ctx, r, worker, wt := leaseVCSRepo(t)
	adminDir := filepath.Join(r.main, ".git", "worktrees", "lease-job")
	from := func(dir string) Dependencies { return Dependencies{callDir: dir} }
	for command, deps := range map[string]Dependencies{
		"git commit -q -m done":                                     from(wt),
		"git add a.txt && git commit -m done":                       from(wt),
		"git -C " + wt + " commit -m done":                          from(r.main),
		"cd " + wt + " && git commit -m done":                       from(r.main),
		"cd " + wt + " && git commit -m \"$msg\"":                   from(r.main),
		"git -C " + r.main + " -C ../lease-job commit -m done":      from(t.TempDir()),
		"GIT_DIR=" + adminDir + " git commit -m done":               from(wt),
		"git --git-dir=" + adminDir + " commit -m done":             from(r.main),
		"env GIT_DIR=" + adminDir + " git commit --amend --no-edit": from(wt),
	} {
		assert.Empty(t, denyLeaseScopedVCS(ctx, deps, worker.ID, command), "%q", command)
	}
}

// TestDenyLeaseScopedVCSRefusesACommitAnywhereElse pins each commit a worker may not make,
// whichever way it is spelled, and that the refusal names where it was and where it may.
func TestDenyLeaseScopedVCSRefusesACommitAnywhereElse(t *testing.T) {
	ctx, r, worker, wt := leaseVCSRepo(t)
	sibling := r.add("sibling")
	from := func(dir string) Dependencies { return Dependencies{callDir: dir} }
	for command, deps := range map[string]Dependencies{
		"git commit -m done":                               from(r.main),
		"git -C " + r.main + " commit -m done":             from(wt),
		"cd " + sibling + " && git commit -m done":         from(wt),
		"git -C " + sibling + " commit -m done":            from(wt),
		"GIT_DIR=" + r.main + "/.git git commit -m done":   from(wt),
		"git --git-dir=" + r.main + "/.git commit -m done": from(wt),
		"git --work-tree " + wt + " commit -m done":        from(wt),
		"GIT_WORK_TREE=" + wt + " git commit -m done":      from(wt),
		"cd \"$dir\" && git commit -m done":                from(wt),
		"git -C \"$dir\" commit -m done":                   from(wt),
		"if true; then git commit -m done; fi":             from(wt),
		"git push":                                         from(wt),
		"git commit -m done && git push":                   from(wt),
		"git stash push -m wip":                            from(wt),
		"git reset --hard HEAD":                            from(wt),
		"git checkout .":                                   from(wt),
		"git revert HEAD":                                  from(wt),
		"git -c alias.x=commit x -m done":                  from(wt),
	} {
		reason := denyLeaseScopedVCS(ctx, deps, worker.ID, command)
		require.NotEmpty(t, reason, "%q", command)
		assert.Contains(t, reason, "A worker may commit, with any backend, on its own branch in "+wt, "%q", command)
		assert.Contains(t, reason, "a worker under orchestrator", "%q", command)
	}
	assert.Contains(t, denyLeaseScopedVCS(ctx, from(r.main), worker.ID, "git commit -m done"),
		"It commits in "+r.main+", and the lease was taken in "+wt+".")
}

// TestDenyLeaseScopedVCSRefusesTheWrongBranch pins the branch half: a worker's own
// checkout on the base branch, with no branch, or the primary checkout is not its own branch.
func TestDenyLeaseScopedVCSRefusesTheWrongBranch(t *testing.T) {
	ctx, r, worker, wt := leaseVCSRepo(t)
	onBase := Dependencies{callDir: wt, VCS: types.VCSOptions{BaseRef: "origin/lease-job"}}
	assert.Contains(t, denyLeaseScopedVCS(ctx, onBase, worker.ID, "git commit -m done"), "is on lease-job, the base branch")

	r.git(wt, "checkout", "-q", "--detach")
	assert.Contains(t, denyLeaseScopedVCS(ctx, Dependencies{callDir: wt}, worker.ID, "git commit -m done"), "has no branch checked out")

	primary := worker
	primary.ID = "harness/primary"
	primary.CheckoutRoot = r.main
	ctx, _ = fleetFixture(t, primary)
	assert.Contains(t, denyLeaseScopedVCS(ctx, Dependencies{callDir: r.main}, primary.ID, "git commit -m done"), "primary checkout")
}

// TestVCSCmdRefusalDecidesAsTheShellRuleDoes pins vcs.cmd to the shell rule's answers: a
// commit in the worker's own checkout runs, anything the shell rule refuses is refused,
// on every backend.
func TestVCSCmdRefusalDecidesAsTheShellRuleDoes(t *testing.T) {
	ctx, r, worker, wt := leaseVCSRepo(t)
	require.NoError(t, vcsCmdRefusal(ctx, worker, "git", []string{"commit", "-m", "done"}, wt))
	require.NoError(t, vcsCmdRefusal(ctx, worker, "git", []string{"-C", wt, "commit", "-m", "done"}, r.main))
	require.NoError(t, vcsCmdRefusal(ctx, worker, "git", []string{"status", "--short"}, r.main))
	for _, c := range []struct {
		backend string
		args    []string
		dir     string
	}{
		{"git", []string{"commit", "-m", "done"}, r.main},
		{"git", []string{"-C", r.main, "commit", "-m", "done"}, wt},
		{"git", []string{"push"}, wt},
		{"git", []string{"stash"}, wt},
		{"git", []string{"reset", "--hard"}, wt},
		{"hg", []string{"push"}, wt},
		{"hg", []string{"-R", r.main, "commit", "-m", "done"}, wt},
		{"sl", []string{"push", "--to", "main"}, wt},
		{"jj", []string{"git", "push"}, wt},
	} {
		err := vcsCmdRefusal(ctx, worker, c.backend, c.args, c.dir)
		require.Error(t, err, "%s %v", c.backend, c.args)
		assert.Contains(t, err.Error(), "`vcs\\cmd(", "%s %v", c.backend, c.args)
		assert.Contains(t, err.Error(), "A worker may commit, with any backend, on its own branch in "+wt)
	}
}

// TestUndeclaredLeaseRepairsReadsGitsSubcommand pins that an unenrolled session's git
// reads are recognized past git's global options, and that no option turns a write, or a
// word an inline alias defines, into one.
func TestUndeclaredLeaseRepairsReadsGitsSubcommand(t *testing.T) {
	t.Parallel()
	for command, want := range map[string]bool{
		"git status":                     true,
		"git -C ../other status --short": true,
		"git --no-pager log -3":          true,
		"git -c color.ui=never diff":     true,
		"git -C . commit -m x":           false,
		"git -c alias.st=commit st":      false,
		"git -c alias.status=x status":   false,
	} {
		assert.Equal(t, want, undeclaredLeaseRepairs(command), "%q", command)
	}
}

// The readers with a spelling that writes are read past it: sed only while it prints line
// ranges, find without an action that deletes or runs, and any reader whose output a
// redirect sends into a file is a writer.
func TestUndeclaredLeaseRepairsReadsPastWritingSpellings(t *testing.T) {
	t.Parallel()
	for command, want := range map[string]bool{
		"sed -n 1,5p x":                 true,
		"sed -n '12,40p;$p' x y":        true,
		"sed -ne 3q x":                  true,
		"sed -n -e 1,5p -e 9p x":        true,
		"sed -i s/a/b/ x":               false,
		"sed -n -i.bak 1p x":            false,
		"sed --in-place 1p x":           false,
		"sed -n '1w out' x":             false,
		"sed 's/a/b/e' x":               false,
		"sed -f script.sed x":           false,
		"find . -name '*.go'":           true,
		"find . -name '*.go' -delete":   false,
		"find . -exec rm {} +":          false,
		"cat x > y":                     false,
		"magus ls jobs 2>/dev/null":     true,
		"magus ls jobs > /tmp/jobs.txt": false,
	} {
		assert.Equal(t, want, undeclaredLeaseRepairs(command), "%q", command)
	}
}

// A worker whose checkout was removed while it ran reads as bound to the job the sweep
// ended, never as unbound: its work is refused with the reason and the one command that
// binds it again, and that command still runs and rebinds over the tombstone.
func TestATombstonedBindingIsRefusedUntilItRebinds(t *testing.T) {
	t.Setenv("BAGGAGE", "")
	gone := t.TempDir()
	held := types.Job{ID: "held-job", State: types.StateRunning, WritePaths: []string{"internal/held/**"}, CheckoutRoot: gone, Registered: 1, ReportedBase: "77aa01c"}
	next := types.Job{ID: "next-job", State: types.StateDeclared, WritePaths: []string{"internal/next/**"}}
	ctx, root := fleetFixture(t, held, next)
	at := hookLocation(ctx, Dependencies{})
	who := hookAttribution{Host: "claude-code", Session: "8f2c6a1e", Agent: "a1b2c3"}
	bindCaller(t, ctx, who, held.ID)
	require.NoError(t, os.RemoveAll(gone))
	_, err := job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace}).List()
	require.NoError(t, err)
	b := boundJob(who, at)
	require.Equal(t, []any{held.ID, true}, []any{b.Job, b.Gone}, "the sweep tombstoned the binding")

	type outcome struct {
		Decision, Rule, Lease string
	}
	var reason string
	judge := func(input string) outcome {
		v := Judge(ctx, Dependencies{}, Request{Input: input, Host: who.Host})
		reason = v.Reason
		return outcome{v.Decision, v.Rule, v.Lease}
	}
	refused := outcome{"deny", string(denyRuleLeaseUndeclared), held.ID}
	edit := map[string]any{"file_path": filepath.Join(root, "internal", "held", "a.go"), "old_string": "a", "new_string": "b"}
	write := hookJSON(t, map[string]any{"session_id": who.Session, "agent_id": who.Agent, "agent_type": "general-purpose", "cwd": root,
		"hook_event_name": "PreToolUse", "tool_name": "Edit", "tool_input": edit})

	assert.Equal(t, refused, judge(write), "a write")
	ref := verdictRef.FindString(reason)
	require.NotEmpty(t, ref, "the first firing cites its stored verdict")
	assert.Equal(t, "magus workspace: your binding to job held-job ended when its row or its checkout was removed; `magus job exec <job>` binds you again."+
		verdictRefLine+"magus query output "+ref, reason)
	stored, err := trail.ReadBlob(at.cacheDir, ref)
	require.NoError(t, err)
	assert.Contains(t, string(stored), "A caller whose binding ended is refused rather than read as unbound", "the rationale is in the stored verdict")
	assert.True(t, strings.HasSuffix(string(stored), "\nsee: https://eli.gladman.cc/magus/reference/rules/lease-undeclared/"), "with the rule's page")
	assert.Equal(t, refused, judge(bashCall(t, who.Session, who.Agent, "./build.sh")), "a command")
	assert.Equal(t, outcome{"advise", string(advisoryLeaseTerminal), held.ID}, judge(bashCall(t, who.Session, who.Agent, "ls")), "a reader")

	assert.NotEqual(t, "deny", judge(bashCall(t, who.Session, who.Agent, "magus job exec "+next.ID)).Decision, "the remedy")
	assert.Equal(t, job.Binding{Job: next.ID}, boundJob(who, at), "the exec rebinds over the tombstone")
	assert.Equal(t, outcome{"pass", "", next.ID}, judge(bashCall(t, who.Session, who.Agent, "ls")))
}

// A caller that ends its own job is unbound from then on: a binding nothing released
// graded it under a row it had finished with, so its next fork was refused as outside that
// row's tree, and once the row was removed every shell line it ran was refused as acting
// under an undeclared lease.
func TestEndingYourOwnJobReleasesTheBinding(t *testing.T) {
	t.Setenv("BAGGAGE", "")
	for _, tc := range []struct {
		name, command string
		ends          func(*testing.T, *job.Store, string)
	}{
		{"exit", "magus job exit held-job", func(t *testing.T, s *job.Store, id string) {
			_, err := s.Update(t.Context(), id, func(cur *types.Job) { cur.State = types.StateExited })
			require.NoError(t, err)
		}},
		{"rm", "magus job rm held-job --force", func(t *testing.T, s *job.Store, id string) {
			_, err := s.Delete(t.Context(), id, true)
			require.NoError(t, err)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := types.Job{ID: "held-job", State: types.StateRunning, WritePaths: []string{"internal/held/**"}, Registered: 1}
			ctx, _ := fleetFixture(t, held)
			at := hookLocation(ctx, Dependencies{})
			who := hookAttribution{Host: "claude-code", Session: "8f2c6a1e", Agent: "a1b2c3"}
			bindCaller(t, ctx, who, held.ID)
			judge := func(command string) Verdict {
				return Judge(ctx, Dependencies{}, Request{Input: bashCall(t, who.Session, who.Agent, command), Host: who.Host})
			}

			require.NotEqual(t, "deny", judge(tc.command).Decision, "ending its own job")
			tc.ends(t, job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace}), held.ID)

			assert.Equal(t, job.Binding{}, boundJob(who, at), "the caller is unbound")
			for _, command := range []string{"magus job fork next-job --parent other --read-only", "sed -n 1,5p x"} {
				v := judge(command)
				assert.NotEqual(t, "deny", v.Decision, "%q: %s", command, v.Reason)
			}
		})
	}
}

// A binding whose row someone else removed is a tombstone, never an unbound caller, and
// the caller can still read with sed while it is refused work.
func TestABindingToARemovedRowIsATombstone(t *testing.T) {
	t.Setenv("BAGGAGE", "")
	held := types.Job{ID: "held-job", State: types.StateRunning, WritePaths: []string{"internal/held/**"}, Registered: 1}
	ctx, _ := fleetFixture(t, held)
	at := hookLocation(ctx, Dependencies{})
	who := hookAttribution{Host: "claude-code", Session: "8f2c6a1e", Agent: "a1b2c3"}
	bindCaller(t, ctx, who, held.ID)
	store := job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace})
	_, err := store.Delete(t.Context(), held.ID, true)
	require.NoError(t, err)
	_, err = store.List()
	require.NoError(t, err)

	b := boundJob(who, at)
	assert.Equal(t, []any{held.ID, true}, []any{b.Job, b.Gone}, "the sweep tombstoned the binding")
	judge := func(command string) Verdict {
		return Judge(ctx, Dependencies{}, Request{Input: bashCall(t, who.Session, who.Agent, command), Host: who.Host})
	}
	assert.NotEqual(t, "deny", judge("sed -n 1,5p x").Decision, "a read")
	assert.Equal(t, string(denyRuleLeaseUndeclared), judge("sed -i s/a/b/ x").Rule, "sed in place is work")
	assert.Equal(t, string(denyRuleLeaseUndeclared), judge("./build.sh").Rule, "work")
}

// TestDenyLeaseScopedHarnessSeesPastGlobalFlags pins the two bypasses the harness rule had:
// a global flag's value read as the subcommand (`--root .`), and a single-dash word holding
// an h read as -h (`-o=template=hi`, `-root=/home/x`, `-cache-dir`), which exempted the call
// as a help request.
func TestDenyLeaseScopedHarnessSeesPastGlobalFlags(t *testing.T) {
	for _, command := range []string{
		"magus agent harness install",
		"magus --root . agent harness install",
		"magus -C /repo agent harness install",
		"magus -o=template=hi agent harness install",
		"magus -root=/home/x agent harness install",
		"magus -cache-dir /tmp/c agent harness install",
		"magus -j 4 --tee /tmp/out -s agent harness install --id x",
	} {
		assert.NotEmpty(t, denyLeaseScopedHarness(t.Context(), Dependencies{}, "wave/a", command), "%q", command)
	}
	for _, command := range []string{
		"magus agent harness install --help",
		"magus agent harness install -h",
		"magus -help agent harness install",
		"magus agent harness verify",
		"magus describe harness claude-code",
		"magus --root agent harness",
	} {
		assert.Empty(t, denyLeaseScopedHarness(t.Context(), Dependencies{}, "wave/a", command), "%q", command)
	}
}

// TestHasFlagReadsOnlyARealShortFlag pins the POSIX reading every rule shares: a short flag
// is bare or in a cluster of letters, a cluster's non-letter tail is a value, and a word
// holding `=` is one flag with its value.
func TestHasFlagReadsOnlyARealShortFlag(t *testing.T) {
	for args, want := range map[string]bool{
		"-h":             true,
		"-rh":            true,
		"-i.bak":         false, // asked for h; the i form is below
		"-o=template=hi": false,
		"-root=/home/x":  false,
		"--help":         true,
		"--help=true":    true,
		"-- -h":          false,
		"file -xh":       true,
	} {
		assert.Equal(t, want, hasFlag(strings.Fields(args), 'h', "help"), "%q", args)
	}
	assert.True(t, hasFlag([]string{"-i.bak"}, 'i', "in-place"), "a cluster's suffix is its value")
	assert.True(t, hasFlag([]string{"-pi.bak", "-e", "s/a/b/"}, 'i', "in-place"))
	assert.False(t, hasFlag([]string{"-e=i"}, 'i', ""), "a word with = carries no short flag")
}

// TestDenyLeaseScopedRebind pins the rebind rule: under a bound lease, the commands that
// rewrite who the caller is, or what its row says, are refused with the actor named.
func TestDenyLeaseScopedRebind(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())
	me := narrowLease().ID

	for command, what := range map[string]string{
		"magus job exec harness/other":                               "take the lease on another job",
		"./magus job exec harness/other":                             "take the lease on another job",
		"magus -s job exec harness/other":                            "take the lease on another job",
		"magus --root /tmp/x job exec harness/other":                 "take the lease on another job",
		"magus job exec --base rev1 harness/other":                   "take the lease on another job",
		"magus job wait harness/other":                               "verify a job",
		"magus job fork":                                             "declare a job",
		"client op=clear":                                            "drop every job",
		"client op=unread":                                           "cannot read",
		"client op=put id=harness/other write_paths=**":              "is outside lease harness/lease-scoped-deny's tree",
		"client op=register id=harness/other":                        "write another job",
		"client op=put id=harness/lease-scoped-deny write_paths=**":  "rewrite the job it holds",
		"client op=put id=harness/lease-scoped-deny read_only=false": "rewrite the job it holds",
	} {
		reason := denyLeaseScopedRebind(ctx, Dependencies{}, me, command)
		require.NotEmpty(t, reason, "%q", command)
		assert.Contains(t, reason.Say, what, "the verdict must say what the command would do")
		assert.NotContains(t, reason.Say, "\n", "the verdict is one line")
		assert.Contains(t, reason.full(), me, "the denial must name the bound lease")
		if command == "magus job fork" {
			assert.Contains(t, reason.Say, "give the child an id", "a fork the guard cannot read is the caller's to fix")
			continue
		}
		assert.Contains(t, reason.Say, "ask your orchestrator to", "the verdict must name the actor")
		assert.Contains(t, reason.Why, "Report it as an unresolved risk and stop", "the rationale ends the turn")
	}
}

// TestDenyLeaseScopedRebindLetsAHolderEnterBeneathIt pins the one put on another row a
// holder may make: an entry into a job forked beneath its own, which the store grades.
func TestDenyLeaseScopedRebindLetsAHolderEnterBeneathIt(t *testing.T) {
	me := narrowLease().ID
	child := types.Job{ID: me + "/child", Parent: me, WritePaths: []string{"cmd/magus/x/**"}, State: types.StateDeclared}
	ctx, _ := fleetFixture(t, narrowLease(), child)

	assert.Empty(t, denyLeaseScopedRebind(ctx, Dependencies{}, me, "client op=put id="+child.ID+" enter=cmd/magus/x/a.go"))
	for _, command := range []string{
		"client op=put id=harness/other enter=a.go",
		"client op=put id=" + me + " enter=cmd/magus/a.go",
	} {
		assert.Contains(t, denyLeaseScopedRebind(ctx, Dependencies{}, me, command).Say, "enter a job not forked beneath", "%q", command)
	}

	_, err := job.ParseMerge(map[string]any{"op": "fork", "id": child.ID, "enter": "cmd/magus/x/a.go", "write_paths": "**"})
	assert.ErrorContains(t, err, "write_paths does not belong beside it", "an entry carries nothing else for the store to apply")
}

// TestDenyLeaseScopedRebindStaysQuiet covers every silence. A read is not a rebind, an
// unbound caller is the party that writes rows, and `op=exec` is the worker's own
// procedure, demanded by the checkpoint denial on a file write.
func TestDenyLeaseScopedRebindStaysQuiet(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())
	me := narrowLease().ID

	for name, command := range map[string]string{
		"an exec naming no job":             "magus job exec",
		"reading the plan":                  "magus ls jobs",
		"reading one row":                   "magus describe job harness/lease-scoped-deny",
		"recording its own base":            "client op=register id=harness/lease-scoped-deny reported_base=abc123",
		"a client script with no job write": "client",
		"an unrelated magus verb":           "magus run go-build .",
		"a lease id in an argument":         "magus query \"job exec\"",
	} {
		assert.Empty(t, denyLeaseScopedRebind(ctx, Dependencies{}, me, command), name)
	}

	assert.Empty(t, denyLeaseScopedRebind(ctx, Dependencies{}, "", "magus job exec harness/other"),
		"an unbound caller is the orchestrator or the person, and they are who writes rows")
}

// TestJobToolRebindLetsAWritePathBeGivenBack pins the one fork a bound caller may make:
// giving a declaration back. The direction is what the guard judges; whether a particular
// shrink is legitimate belongs to the store.
func TestJobToolRebindLetsAWritePathBeGivenBack(t *testing.T) {
	wide := narrowLease()
	wide.WritePaths = []string{"cmd/magus/**", "internal/hint/**"}
	ctx, _ := fleetFixture(t, wide)

	assert.Empty(t, denyLeaseScopedRebind(ctx, Dependencies{}, wide.ID,
		"client op=put id="+wide.ID+" write_paths=cmd/magus/**"),
		"dropping one of its own declarations cannot widen a role")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, Dependencies{}, wide.ID,
		"client op=put id="+wide.ID+" write_paths=cmd/magus/**,internal/hint/**,docs/**"),
		"adding a declaration is a widen however it is spelled")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, Dependencies{}, wide.ID,
		"client op=put id="+wide.ID+" write_paths=cmd/**"),
		"a pattern that happens to cover less is not a shrink this rule will try to prove")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, Dependencies{}, wide.ID,
		"client op=put id="+wide.ID+" write_paths=cmd/magus/** validation=magus affected ci"),
		"a shrink carrying another field is not a shrink")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, Dependencies{}, wide.ID,
		"client op=put id="+wide.ID+" write_paths=cmd/magus/** checkpoint=deadbeef"),
		"the checkpoint is the base this lease's work is graded against, and giving a path back is not cover for moving it")

	assert.NotEmpty(t, denyLeaseScopedRebind(ctx, Dependencies{}, wide.ID,
		"client op=put id="+wide.ID+" write_paths=cmd/magus/** owned_paths=cmd/magus/**"),
		"a key the store does not know is not a shrink either")
}

// childFleet is a lease with a child already forked beneath it, beside another row, and a
// store that writes as the lease: the checkout a worker took its own job in.
func childFleet(t *testing.T, readOnly bool) (context.Context, types.Job, *job.Store) {
	t.Helper()
	me := narrowLease()
	me.ReadOnly = readOnly
	if readOnly {
		me.WritePaths = nil
	}
	scout := types.Job{ID: me.ID + "/scout", Parent: me.ID, ReadOnly: true, State: types.StateExited}
	other := types.Job{ID: "harness/other", WritePaths: []string{"docs/**"}, State: types.StateRunning}
	ctx, root := fleetFixture(t, me, scout, other)
	location, _ := ctx.Value(locationKey{}).(location)
	unbound := job.NewStore(job.Location{CacheDir: location.cacheDir, Root: root, Actor: &job.Actor{}})
	require.NoError(t, unbound.Bind(job.Caller{}, me.ID))
	bound := job.NewStore(job.Location{CacheDir: location.cacheDir, Root: root, Actor: &job.Actor{Lease: me.ID}})
	return ctx, me, bound
}

// TestDenyLeaseScopedRebindHandsAChildToTheStore pins U0 of the orchestration topology
// plan: a bound caller forks a child of its own lease and waits on its own descendant, and
// the store alone grades the child's boundary, so a widening child meets one refusal.
func TestDenyLeaseScopedRebindHandsAChildToTheStore(t *testing.T) {
	ctx, me, bound := childFleet(t, true)

	for _, command := range []string{
		"magus job fork " + me.ID + "/research --parent " + me.ID + " --read-only --criteria 'read the store'",
		"./magus job fork --parent=" + me.ID + " " + me.ID + "/research --read-only",
		"client op=put id=" + me.ID + "/research parent=" + me.ID + " read_only=true",
		"magus job fork --stdin <<'EOF'\n{\"schema_version\": " + strconv.Itoa(types.JobSchemaVersion) + ", \"id\": \"" + me.ID + "/research\", \"parent\": \"" + me.ID + "\", \"read_only\": true}\nEOF",
		"magus job wait " + me.ID + "/scout",
		"magus job fork " + me.ID + "/wide --parent " + me.ID + " --write-paths docs/**",
	} {
		assert.Empty(t, denyLeaseScopedRebind(ctx, Dependencies{}, me.ID, command), "%q", command)
	}

	_, err := bound.Update(t.Context(), me.ID+"/research", func(u *types.Job) {
		*u = types.Job{ID: me.ID + "/research", Parent: me.ID, ReadOnly: true, State: types.StateDeclared}
	})
	require.NoError(t, err, "the child the guard passed is one the store accepts")

	_, err = bound.Update(t.Context(), me.ID+"/wide", func(u *types.Job) {
		*u = types.Job{ID: me.ID + "/wide", Parent: me.ID, WritePaths: []string{"docs/**"}, State: types.StateDeclared}
	})
	var refused *job.RefusedError
	require.ErrorAs(t, err, &refused, "the store is the one refusal for a child reaching past its parent")
	assert.Contains(t, refused.Rule, "paths its parent owns")
}

// TestDenyLeaseScopedRebindRefusesWhatIsNotAChild: every fork that is not a new child of
// the caller's own lease, and every wait off its own tree, stays refused, and a refusal
// naming another parent carries the store's own sentence.
func TestDenyLeaseScopedRebindRefusesWhatIsNotAChild(t *testing.T) {
	ctx, me, bound := childFleet(t, false)
	other := "harness/other"

	for command, what := range map[string]string{
		"magus job fork " + me.ID + "/x --parent " + other + " --read-only":                   "must name " + me.ID + " as its parent",
		"magus job fork " + me.ID + "/x --read-only":                                          "must name " + me.ID + " as its parent",
		"client op=put id=" + me.ID + "/x parent=" + other + " read_only=true":                "must name " + me.ID + " as its parent",
		"magus job fork " + other + " --parent " + me.ID + " --read-only":                     "already exists",
		"magus job fork " + me.ID + " --parent " + me.ID:                                      "rewrite the job it holds",
		"magus job fork " + me.ID + "/x --parent " + me.ID:                                    "can write anywhere",
		"magus job fork --stdin < job.json":                                                   "cannot read",
		"magus job fork --stdin <<EOF\n{\"id\": \"$ID\", \"parent\": \"" + me.ID + "\"}\nEOF": "cannot read",
		"magus job wait " + other:                                                             "verify a job",
		"magus job wait " + me.ID:                                                             "verify a job",
	} {
		reason := denyLeaseScopedRebind(ctx, Dependencies{}, me.ID, command)
		require.NotEmpty(t, reason, "%q", command)
		assert.Contains(t, reason.full(), what, "%q", command)
	}

	_, err := bound.Update(t.Context(), me.ID+"/x", func(u *types.Job) {
		*u = types.Job{ID: me.ID + "/x", Parent: other, ReadOnly: true, State: types.StateDeclared}
	})
	var refused *job.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Rule, "must name "+me.ID+" as its parent", "the guard and the store say the same thing")
}

// TestDenyLeaseScopedRebindGradesTheChildWhereTheStoreCannot: a worker the hook identified
// in a checkout bound to nobody writes to a store that grades nothing, so only a child
// that can widen nothing passes.
func TestDenyLeaseScopedRebindGradesTheChildWhereTheStoreCannot(t *testing.T) {
	me := narrowLease()
	ctx, _ := fleetFixture(t, me)

	assert.Empty(t, denyLeaseScopedRebind(ctx, Dependencies{}, me.ID,
		"magus job fork "+me.ID+"/scout --parent "+me.ID+" --read-only --model opus"))
	assert.Empty(t, denyLeaseScopedRebind(ctx, Dependencies{}, me.ID,
		"magus job fork --stdin <<'EOF'\n{\"schema_version\": 11, \"id\": \""+me.ID+"/probe\", \"parent\": \""+me.ID+"\", \"read_only\": true, \"check\": {\"target\": \"\", \"script\": \"probe.buzz\"}}\nEOF"),
		"a script check grants nothing, and it is how a read-only scout passes job wait")
	for _, command := range []string{
		"magus job fork " + me.ID + "/wide --parent " + me.ID + " --write-paths cmd/magus/**",
		"magus job fork " + me.ID + "/scout --parent " + me.ID + " --read-only --read-paths **",
		"client op=put id=" + me.ID + "/scout parent=" + me.ID + " read_only=true state=pass",
		"magus job fork --stdin <<'EOF'\n{\"schema_version\": 11, \"id\": \"" + me.ID + "/probe\", \"parent\": \"" + me.ID + "\", \"read_only\": true, \"check\": {\"target\": \"go-test\", \"project\": \".\"}}\nEOF",
	} {
		assert.Contains(t, denyLeaseScopedRebind(ctx, Dependencies{}, me.ID, command).Say, "nothing would grade", "%q", command)
	}
}

// TestActingLeaseStandingSeparatesTheThreeAnswers is what the undeclared and terminal
// rules are built on: "not declared" and "could not be read" are different facts, and only
// the first is the caller's mistake.
func TestActingLeaseStandingSeparatesTheThreeAnswers(t *testing.T) {
	done := narrowLease()
	done.ID, done.State = "harness/finished", types.StatePass
	ctx, _ := fleetFixture(t, narrowLease(), done)

	live := actingLeaseStanding(ctx, Dependencies{}, narrowLease().ID)
	assert.True(t, live.readable)
	assert.True(t, live.declared)
	assert.False(t, live.terminal(), "a running row is not terminal")

	finished := actingLeaseStanding(ctx, Dependencies{}, done.ID)
	assert.True(t, finished.declared)
	assert.True(t, finished.terminal())

	absent := actingLeaseStanding(ctx, Dependencies{}, "harness/typo")
	assert.True(t, absent.readable, "the store answered; it just does not carry that id")
	assert.False(t, absent.declared)

	nowhere := context.WithValue(t.Context(), locationKey{}, location{})
	assert.False(t, actingLeaseStanding(nowhere, Dependencies{}, narrowLease().ID).readable,
		"no workspace is a rule the guard cannot evaluate, not an undeclared id")
	assert.False(t, actingLeaseStanding(ctx, Dependencies{}, "").readable, "no lease is nothing to look up")
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

// TestJobToolRebindIsSilentWhenTheStoreCannotAnswer: the rule read the acting lease's
// LIVE row, which is absent for a terminal row and for an unreadable store alike, so a
// session writing its OWN finished row was refused with a reason naming somebody else's,
// in the same verdict that says the lease-scoped denials are not running for it.
func TestJobToolRebindIsSilentWhenTheStoreCannotAnswer(t *testing.T) {
	done := narrowLease()
	done.State = types.StatePass
	ctx, _ := fleetFixture(t, done)

	assert.Empty(t, denyLeaseScopedRebind(ctx, Dependencies{}, done.ID,
		"client op=put id="+done.ID+" write_paths=**"),
		"a terminal row has no boundary left, so naming another lease's row would be false")

	nowhere := context.WithValue(t.Context(), locationKey{}, location{})
	assert.Empty(t, denyLeaseScopedRebind(nowhere, Dependencies{}, done.ID, "client op=put id="+done.ID),
		"a store the guard cannot read leaves nothing to judge against")
}

// TestDenyLeaseScopedRebindLetsAHolderFinishItsBootstrap pins two refusals that had no
// boundary behind them, both reported by workers that could not complete their own setup.
//
// Re-binding the id already bound is the bootstrap run twice, and the grading schema is a
// read: a holder denied it writes its result from memory, which is the failure the typed
// result exists to prevent. Binding a DIFFERENT lease is still refused, in the words it
// always used.
func TestDenyLeaseScopedRebindLetsAHolderFinishItsBootstrap(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())
	id := narrowLease().ID

	for _, command := range []string{
		"./magus job exec " + id,
		"./magus job wait --schema",
		"./magus job wait --help",
		"./magus job fork --schema",
	} {
		assert.Empty(t, denyLeaseScopedRebind(ctx, Dependencies{}, id, command),
			"%q asserts a binding the holder already has, or prints a contract; neither writes a row", command)
	}

	for _, command := range []string{
		"./magus job exec harness/other",
		"./magus job wait --state pass",
	} {
		reason := denyLeaseScopedRebind(ctx, Dependencies{}, id, command)
		require.NotEmpty(t, reason, "%q still moves a row", command)
		assert.Contains(t, reason.Say, id, "the verdict must say who magus thinks is calling")
	}
}

// writePathFleet is the plan the write-path rule is graded against: the acting worker, and
// a sibling whose tree every write below aims at.
func writePathFleet() []types.Job {
	return []types.Job{
		{
			ID:         "lease-a",
			Criteria:   "own the ledger store",
			WritePaths: []string{"internal/ledger"},
			State:      types.StateRunning,
			Registered: 1,
		},
		{
			ID:         "lease-b",
			Criteria:   "grade writes in the guard",
			WritePaths: []string{"cmd/magus"},
			DenyPaths:  []string{"cmd/magus/gen"},
			State:      types.StateRunning,
			Registered: 1,
		},
	}
}

// TestDenyWriteOutsideLeaseCatchesEveryWriterForm pins the gap this rule closed: the
// boundary was enforced only where a host reported a path, so every spelling below reached
// a sibling's tree unjudged while the identical editor write was refused.
//
// Each row asserts BOTH the shell line and the file write, and that they refuse in the SAME
// words. A rule that lands on one and not the other is exactly how the gap happened, and a reason that
// drifts between them teaches two different lessons for one mistake.
func TestDenyWriteOutsideLeaseCatchesEveryWriterForm(t *testing.T) {
	ctx, root := fleetFixture(t, writePathFleet()...)

	// Whether withoutLeaseAge blanked anything at all. Not every row renders an age (a
	// path the acting lease's own row DENIES names no owner), but if no row does, the
	// normalizer has silently become the identity function and this table has quietly
	// reverted to the clock-flaky comparison it was written to replace.
	normalized := false

	for _, tc := range []struct {
		command string
		path    string
	}{
		{`printf "\n" >> internal/ledger/store.go`, "internal/ledger/store.go"},
		{"echo x > internal/ledger/store.go", "internal/ledger/store.go"},
		{"echo x >| internal/ledger/store.go", "internal/ledger/store.go"},
		{"echo x &> internal/ledger/store.go", "internal/ledger/store.go"},
		{"echo x >& internal/ledger/store.go", "internal/ledger/store.go"},
		{"exec 3<> internal/ledger/store.go", "internal/ledger/store.go"},
		{"echo x | tee internal/ledger/store.go", "internal/ledger/store.go"},
		{"sed -i 's/a/b/' internal/ledger/store.go", "internal/ledger/store.go"},
		{"cp foo internal/ledger/store.go", "internal/ledger/store.go"},
		{"mv /tmp/x internal/ledger/store.go", "internal/ledger/store.go"},
		{"install -m 644 f internal/ledger/store.go", "internal/ledger/store.go"},
		{"dd of=internal/ledger/store.go", "internal/ledger/store.go"},
		{"ln -sf /dev/null internal/ledger/store.go", "internal/ledger/store.go"},
		{"touch internal/ledger/store.go", "internal/ledger/store.go"},
		{"truncate -s 0 internal/ledger/store.go", "internal/ledger/store.go"},
		{"chmod 600 internal/ledger/store.go", "internal/ledger/store.go"},
		{"chown me internal/ledger/store.go", "internal/ledger/store.go"},
		{"sort -o internal/ledger/store.go f", "internal/ledger/store.go"},
		{`awk '{print > "internal/ledger/store.go"}' f`, "internal/ledger/store.go"},
		{`python3 -c "open('internal/ledger/store.go','a').write(line)"`, "internal/ledger/store.go"},
		{"rm -rf internal/ledger", "internal/ledger"},
		{"find internal/ledger -delete", "internal/ledger"},
		{"sh -c 'echo x > internal/ledger/store.go'", "internal/ledger/store.go"},
		{"bash -c 'rm internal/ledger/store.go'", "internal/ledger/store.go"},
		{"eval 'echo x > internal/ledger/store.go'", "internal/ledger/store.go"},
		{"sudo sh -c 'echo x >| internal/ledger/store.go'", "internal/ledger/store.go"},

		// A path the acting lease's OWN row denies, which is the other half of a boundary.
		{"echo x > cmd/magus/gen/cli_flags.go", "cmd/magus/gen/cli_flags.go"},

		// An interpreter fed by a heredoc, which reached a sibling's tree unjudged: the
		// script is the heredoc body, so a walk of the arguments alone never saw the path.
		{"python3 - <<'PY'\nopen('internal/ledger/store.go','w').write(out)\nPY", "internal/ledger/store.go"},
		{"python3 -c \"open('internal/ledger/store.go','w').write(out)\"", "internal/ledger/store.go"},
	} {
		onCommand := denyWriteOutsideLease(ctx, Dependencies{}, "lease-b", tc.command)
		require.NotEmpty(t, onCommand,
			"the shell-command rules passed %q, which writes outside the acting lease's write paths.\n"+
				"A boundary enforced only where the host reports a PATH is one a shell line walks straight through, and that is the gap this table exists to catch. If you taught a rule a new writer spelling, teach writeTargetCandidates about it too.",
			tc.command)

		onPath := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, tc.path))
		require.Equal(t, "deny", onPath.Decision,
			"the file-write rules passed %s, so this row is no longer testing two rule sets against one another", tc.path)
		normalized = normalized || leaseAge.MatchString(onPath.Reason)
		assert.Equal(t, withoutLeaseAge(onPath.Reason), withoutLeaseAge(onCommand),
			"the two rule sets refused %q in DIFFERENT words.\n"+
				"It is one mistake however it is spelled, so it gets one explanation. The shell-command rules are meant to call gradeLeasedWrite, the file-write rules' own grader; a difference here means something re-decided the verdict instead of reusing it, and the two will drift from now on.",
			tc.command)
	}

	assert.True(t, normalized,
		"withoutLeaseAge blanked nothing across the whole table, so it is no longer normalizing the figure it exists for.\n"+
			"Either the refusal stopped naming the owner's age, or it names it in wording leaseAge no longer matches. Update the pattern; leaving it is how this table starts failing on the clock instead of on the wording.")
}

// leaseAge matches the owner's age in a refusal, which is read from the clock as the
// message is built. The unit stays outside the capture, so two rule sets that render the
// same elapsed time differently (0s against 0ms, 90s against 1m30s) still differ.
var leaseAge = regexp.MustCompile(`was last updated \d+s ago`)

// withoutLeaseAge blanks that figure. The two rule sets render their refusal at different
// moments, so an age that ticks over between them is a difference in the clock, not in
// the wording the comparison is about.
func withoutLeaseAge(reason string) string {
	return leaseAge.ReplaceAllString(reason, "was last updated <n>s ago")
}

// TestDenyWriteOutsideLeaseStaysQuiet covers the silences. The rule is a seatbelt for
// harnesses that opt in, so every case where nothing declared the write to be out of
// bounds has to pass: a guard that refuses reads is one agents learn to route around.
func TestDenyWriteOutsideLeaseStaysQuiet(t *testing.T) {
	ctx, _ := fleetFixture(t, writePathFleet()...)

	for _, command := range []string{
		// Reads of the very file the writes above are refused for.
		"cat internal/ledger/store.go",
		"head -n 5 internal/ledger/store.go",
		"grep -r lease internal/ledger",
		"sed 's/a/b/' internal/ledger/store.go",
		"sort internal/ledger/store.go",
		"find internal/ledger -name '*.go'",
		"cp internal/ledger/store.go /tmp/x",
		`echo "rm -rf internal/ledger"`,

		// A pure print: awk's range/comparison operators share a character with its
		// redirect operator, but neither follows a print/printf statement here, so
		// this is a read like any other.
		"awk 'NR>=1,NR<=20' internal/ledger/store.go",
		"awk '$1 > 5' internal/ledger/store.go",

		// magus's own argv is never a target: it writes in there by construction, and the
		// guard grades the agent's tool calls rather than magus's own processes.
		"./magus run test .",
		"./magus session lease lease-b",
		"./magus query output out123",

		// Inside the acting lease's own write paths.
		"echo x > cmd/magus/diff.go",

		// A word nobody declared is not a path. Without this the reader allowlist would
		// refuse an ordinary echo for writing outside the boundary.
		"echo hi",
		"printf 'done'",
	} {
		assert.Empty(t, denyWriteOutsideLease(ctx, Dependencies{}, "lease-b", command), "%q", command)
	}

	t.Run("flags and descriptors beside a lease owning the root", func(t *testing.T) {
		ctx, root := fleetFixture(t,
			types.Job{ID: "owns-root", WritePaths: []string{"**"}, State: types.StateRunning, Registered: 1},
			types.Job{ID: "reader", ReadOnly: true, State: types.StateRunning, Registered: 1},
		)
		require.NoError(t, os.WriteFile(filepath.Join(root, "README"), nil, 0o644))
		for _, command := range []string{
			"./magus ls jobs 2>&1",
			"mkdir -p /tmp/scratch",
			"python3 - < script.py",
			"find /tmp/scratch -type f -delete",
			"echo hi",
			"python3 -c 'print(1)'",
			`python3 -c 'print("hello")'`,
			"mkdir -m 755 /tmp/scratch",
		} {
			assert.Empty(t, denyWriteOutsideLease(ctx, Dependencies{}, "reader", command), "%q", command)
		}
		for _, command := range []string{
			"echo x > notes.txt",
			"echo x > out 2>&1",
			"touch internal/new.go",
			"rm README",
			`python3 -c 'open("out.txt", "w")'`,
		} {
			assert.NotEmpty(t, denyWriteOutsideLease(ctx, Dependencies{}, "reader", command), "%q", command)
		}
	})

	t.Run("no lease", func(t *testing.T) {
		assert.Empty(t, denyWriteOutsideLease(ctx, Dependencies{}, "", "echo x > internal/ledger/store.go"))
	})

	t.Run("a lease with no row", func(t *testing.T) {
		assert.Empty(t, denyWriteOutsideLease(ctx, Dependencies{}, "harness/absent", "echo x > internal/ledger/store.go"))
	})

	t.Run("no trail location", func(t *testing.T) {
		nowhere := context.WithValue(t.Context(), locationKey{}, location{})
		assert.Empty(t, denyWriteOutsideLease(nowhere, Dependencies{}, "lease-b", "echo x > internal/ledger/store.go"))
	})
}
