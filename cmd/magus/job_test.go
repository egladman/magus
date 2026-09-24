package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func leaseRow(id, parent string) types.Job {
	return types.Job{ID: id, Parent: parent, State: types.StateRunning, Model: "standard"}
}

func TestLedgerTreeOrderNestsChildrenUnderTheirParent(t *testing.T) {
	t.Parallel()

	got := jobTreeOrder([]types.Job{
		leaseRow("plan", ""),
		leaseRow("plan/core", "plan"),
		leaseRow("other", ""),
		leaseRow("plan/core/deep", "plan/core"),
	})

	assert.Equal(t, []jobTreeLine{
		{lease: leaseRow("plan", ""), depth: 0},
		{lease: leaseRow("plan/core", "plan"), depth: 1},
		{lease: leaseRow("plan/core/deep", "plan/core"), depth: 2},
		{lease: leaseRow("other", ""), depth: 0},
	}, got)
}

// Every row reaches the reader. A parent that was cleared, and a cycle, both cost their
// rows the indentation and nothing else.
func TestLedgerTreeOrderKeepsUnrootedRows(t *testing.T) {
	t.Parallel()

	rows := []types.Job{
		leaseRow("orphan", "cleared-parent"),
		leaseRow("a", "b"),
		leaseRow("b", "a"),
	}
	got := jobTreeOrder(rows)

	require.Len(t, got, len(rows))
	for _, r := range got {
		assert.Zero(t, r.depth)
	}
}

func TestPrintLedgerTreeRendersOverlaps(t *testing.T) {
	t.Parallel()

	parent := leaseRow("plan", "")
	parent.WritePaths = []string{"internal/ledger"}
	parent.Validation = "magus run test internal/ledger"
	child := leaseRow("plan/cli", "plan")
	child.WritePaths = []string{"internal/ledger/store.go"}

	var out strings.Builder
	printJobTree(&out, types.NewJobList([]types.Job{parent, child}))
	got := out.String()

	assert.Contains(t, got, "JOB")
	assert.Contains(t, got, "\n  plan/cli", "a child is indented under its parent")
	assert.Contains(t, got, "magus run test internal/ledger")
	assert.Contains(t, got, "overlaps")
	assert.Contains(t, got, "plan and plan/cli claim common ground")
}

func TestPrintJobTreeMarksJobsNobodyIsWaitingOn(t *testing.T) {
	t.Parallel()

	rows := []types.Job{
		{ID: "root", State: types.StatePass},
		{ID: "root/orphan", Parent: "root", State: types.StateRunning, Updated: 100},
		{ID: "late", State: types.StateRunning, Deadline: 50, Updated: 100},
	}
	var out strings.Builder
	printJobTree(&out, types.NewJobList(rows).Flag(200, time.Minute))
	text := out.String()
	assert.Contains(t, text, "overdue")
	assert.Contains(t, text, "orphan")
	assert.Contains(t, text, "stale")
	assert.Contains(t, text, hint.JobExit.With("root/orphan"))
}

func TestLeasedBoundarySkipsTheRowsAncestors(t *testing.T) {
	t.Parallel()

	rows := []types.Job{
		{ID: "root", State: types.StateRunning, WritePaths: []string{"internal"}},
		{ID: "root/a", Parent: "root", State: types.StateRunning, WritePaths: []string{"internal/job"}},
		{ID: "root/a/leaf", Parent: "root/a", State: types.StateRunning, WritePaths: []string{"internal/job/plan.go"}},
		{ID: "root/b", Parent: "root", State: types.StateRunning, WritePaths: []string{"internal/guard"}},
	}
	var owners []string
	for _, b := range leasedBoundary(rows[2], rows) {
		owners = append(owners, b.Path)
	}
	assert.Equal(t, []string{"internal/guard"}, owners, "a sibling's boundary is off limits, an ancestor's is where the leaf was forked")
}

func TestPrintLedgerTreeSaysWhereAnEmptyPlanComesFrom(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	printJobTree(&out, types.NewJobList(nil))
	assert.Contains(t, out.String(), "magus_job")
}

// TestPrintJobStatusFailedGateNamesHowToReadIt pins the completion-gates plan's
// step 3: a failed gate named an output ref but not how to read it, leaving the
// holder to reconstruct `magus query output <ref>` by hand. The line must be
// rendered through hint.QueryOutput (never a hardcoded string), and must appear
// only for a gate that actually failed and actually carries a ref.
func TestPrintJobStatusFailedGateNamesHowToReadIt(t *testing.T) {
	t.Parallel()

	status := job.Status{
		Job:        "plan",
		Violations: []string{`completion gate "ci": the run behind output ref "outdeadbeef" failed, so its check did not pass`},
		Gates: []types.GateStatus{
			{ID: "ci", Verified: false, OutputRef: "outdeadbeef"},
			{ID: "lint", Verified: true, OutputRef: "outfeedface"},
		},
	}

	var out strings.Builder
	printJobStatus(&out, status)
	got := out.String()

	assert.Contains(t, got, "completion gate ci: rejected (outdeadbeef)")
	assert.Contains(t, got, "magus query output outdeadbeef", "a failed gate must name how to read its ref")
	assert.NotContains(t, got, "magus query output outfeedface", "a verified gate needs no query-output line")
}

func TestPrintJobStatusRendersTheFootprint(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status job.Status
		want   string
	}{
		{
			name: "known",
			status: job.Status{Job: "plan", Verified: true, FootprintKnown: true, Footprint: []types.ChangedRegion{
				{Path: "a.go", Side: types.RegionNew, Lines: [2]int{3, 9}, Declaration: "func X() {"},
				{Path: "a.go", Side: types.RegionNew, Lines: [2]int{12, 14}, Declaration: "func X() {"},
				{Path: "a.go", Side: types.RegionOld, Lines: [2]int{20, 21}, Declaration: "func Y() {"},
				{Path: "notes.txt", Side: types.RegionNew, Lines: [2]int{1, 2}},
				{Path: "notes.txt", Side: types.RegionNew, Lines: [2]int{8, 8}},
			}},
			want: "verified plan, recorded pass\n" +
				"footprint, reported and never graded: where its diff since the checkpoint landed\n" +
				"  a.go#func X() {\n" +
				"  a.go#func Y() {\n" +
				"  notes.txt:1-2\n" +
				"  notes.txt:8-8\n",
		},
		{
			name:   "known and empty",
			status: job.Status{Job: "plan", Verified: true, FootprintKnown: true},
			want: "verified plan, recorded pass\n" +
				"footprint, reported and never graded: no line changed since the checkpoint\n",
		},
		{
			name:   "declined",
			status: job.Status{Job: "plan", Verified: true, FootprintReason: "git does not report changed regions (RegionReporter)"},
			want: "verified plan, recorded pass\n" +
				"footprint, reported and never graded: not known, git does not report changed regions (RegionReporter)\n",
		},
		{
			name:   "nobody looked",
			status: job.Status{Job: "plan", Verified: true},
			want: "verified plan, recorded pass\n" +
				"footprint, reported and never graded: not known, nothing observed the tree\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			printJobStatus(&out, tc.status)
			assert.Equal(t, tc.want, out.String())
		})
	}
}

func TestPrintJobTreeRendersEachFootprintVerdict(t *testing.T) {
	t.Parallel()

	pair := func(f *types.JobOverlapFootprint) types.JobOverlap {
		return types.JobOverlap{JobA: "a", JobB: "b", PathsA: []string{"api"}, PathsB: []string{"api/x.go"}, Footprint: f}
	}
	rows := []types.Job{
		{ID: "a", State: types.StateRunning, WritePaths: []string{"api"}},
		{ID: "b", State: types.StateRunning, WritePaths: []string{"api/x.go"}},
	}
	for _, tc := range []struct {
		name string
		f    *types.JobOverlapFootprint
		want string
	}{
		{"disjoint", &types.JobOverlapFootprint{Verdict: types.FootprintDisjoint}, "    footprints: disjoint\n"},
		{"shared", &types.JobOverlapFootprint{Verdict: types.FootprintShared, Shared: []string{"a.go#func X", "b.go#func Y"}},
			"    footprints: shared (a.go#func X, b.go#func Y)\n"},
		{"unknown", &types.JobOverlapFootprint{Verdict: types.FootprintUnknown, Reason: "b: no checkout of this repository is bound to b"},
			"    footprints: unknown (b: no checkout of this repository is bound to b)\n"},
		{"not computed", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			printJobTree(&out, types.JobList{Jobs: rows, Overlaps: []types.JobOverlap{pair(tc.f)}})
			_, after, ok := strings.Cut(out.String(), "    b: api/x.go\n")
			require.True(t, ok, "the pair's paths are rendered: %s", out.String())
			assert.Equal(t, tc.want, after)
		})
	}
}

// Explain resolves a bare name fuzzily, which is right for a person typing
// `magus explain build` and wrong for evidence: asked for "cmd/magus" it once answered
// target:.:release-sign, and a blast radius from an unrelated node is worse than silence.
func TestLeaseGraphEvidenceTakesOnlyAnExactNode(t *testing.T) {
	t.Parallel()

	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "dir:cmd/magus", Kind: types.KindDir, Label: "cmd/magus"})
	g.AddNode(types.KnowledgeNode{ID: "file:internal/ledger/brief.go", Kind: types.KindFile, Label: "brief.go"})

	got, ok := pathEvidence(g, "cmd/magus")
	require.True(t, ok)
	assert.Equal(t, job.TermsEvidence{Path: "cmd/magus", Node: "dir:cmd/magus"}, got)
	// "brief.go" resolves to file:internal/ledger/brief.go by name. It is a match and not
	// evidence: the lease declared a path at the workspace root, and this node is not it.
	_, ok = pathEvidence(g, "brief.go")
	assert.False(t, ok, "a fuzzy match is not evidence")
}

// The gate reached indirectly buys the same seven concurrent pipelines as the gate named
// outright, and it is the likelier mistake once the obvious spelling is refused.
func TestChainToGateFollowsComposites(t *testing.T) {
	t.Parallel()

	deps := map[string][]string{
		"preflight": {"format", "verify"},
		"verify":    {"ci"},
		"ci":        {"test", "lint"},
		"test":      nil,
		"format":    nil,
	}

	assert.Equal(t, []string{"preflight", "verify", "ci"}, chainToGate("preflight", deps))
	assert.Nil(t, chainToGate("test", deps))
	// Every bare word of a validation field reaches here, project paths included.
	assert.Nil(t, chainToGate("internal/ledger", deps))
}

// `magus describe graph` reports a cycle rather than rejecting it, so one reaches this
// walk. It has to terminate on its own rather than trust the graph to be acyclic.
func TestChainToGateTerminatesOnACycle(t *testing.T) {
	t.Parallel()

	assert.Nil(t, chainToGate("a", map[string][]string{"a": {"b"}, "b": {"a"}}))
}

// The person's two spellings are one declaration: a row typed as flags and the same row
// piped in as a record reach the store identically, or the CLI has quietly grown a
// second vocabulary.
func TestRegisterFromFlagsAndFromStdinAgree(t *testing.T) {
	t.Parallel()

	flags := forkFlags{
		criteria:   "the store is the enforcement point",
		parent:     "adjacency",
		checkpoint: "cf5509d09",
		writePaths: listFlag{"internal/ledger", "types/lease.go"},
		denyPaths:  listFlag{"MAGUS.md"},
		readPaths:  listFlag{"internal/trail"},
		dependsOn:  listFlag{"adj/guard"},
		check:      "test internal/ledger",
		model:      "principal",
	}
	// The version is READ from the constant, not typed as a digit: this case is about the
	// two doors agreeing on the FIELDS, and a fixture whose version fell behind would fail
	// it at the version check instead, naming nothing it came here to compare.
	piped, err := job.DecodeDeclaration(strings.NewReader(fmt.Sprintf(`{
	  "schema_version": %d,
	  "id": "adj/store",`, types.JobSchemaVersion) + `
	  "parent": "adjacency",
	  "criteria": "the store is the enforcement point",
	  "checkpoint": "cf5509d09",
	  "write_paths": ["internal/ledger", "types/lease.go"],
	  "deny_paths": ["MAGUS.md"],
	  "read_paths": ["internal/trail"],
	  "depends_on": ["adj/guard"],
	  "validation": "magus run test internal/ledger",
	  "model": "principal",
	  "state": "declared"
	}`))
	require.NoError(t, err)

	fromFlags := flags.row("adj/store")
	require.NoError(t, fromFlags.Validate())

	var a, b types.Job
	fromFlags.Apply(&a)
	piped.Apply(&b)
	assert.Equal(t, a, b)
}

// A path flag takes both spellings, and refuses the empty segment a trailing comma
// leaves: a boundary that silently shrank is the failure --skip already refuses.
func TestRegisterPathFlagsTakeRepeatsAndCommas(t *testing.T) {
	t.Parallel()

	var repeated, combined listFlag
	require.NoError(t, repeated.Set("internal/ledger"))
	require.NoError(t, repeated.Set("types/lease.go"))
	require.NoError(t, combined.Set("internal/ledger, types/lease.go"))
	assert.Equal(t, repeated, combined)

	var refused listFlag
	require.Error(t, refused.Set("internal/ledger,"))
	require.Error(t, refused.Set(""))
}

// execFixture pins XDG_STATE_HOME to a scratch dir (the job store lives there, not under
// t.TempDir() alone: see the tests-need-xdg-state-home-pinned lesson) and resolves the
// same cache dir jobExec itself will compute from root, so a marker or row seeded here is
// the one jobExec sees. Mirrors TestHookEnvelopeCwdLocatesTheWorkersCheckout's setup.
func execFixture(t *testing.T, rows ...types.Job) (root, cacheDir string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	global = globalFlags{}
	root = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(""), 0o644))
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	store := job.NewStore(job.Location{CacheDir: cacheDir, Root: root})
	for _, row := range rows {
		_, err := store.Update(t.Context(), row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
	}
	return root, cacheDir
}

// TestJobExecVacateIsANoOpWithNoBinding pins the ABSENT verdict: a checkout that never
// bound anything vacates cleanly, printing rather than failing, because a no-op must not
// read as an error.
func TestJobExecVacateIsANoOpWithNoBinding(t *testing.T) {
	root, _ := execFixture(t)
	out := captureStdout(t, func() {
		require.NoError(t, jobExec(t.Context(), root, []string{"--vacate"}))
	})
	assert.Contains(t, out, "holds no job")
}

// TestJobExecVacateRefusesAnInFlightJob pins the semantics this exists to fix without
// reopening the escape denyLeaseScopedRebind closes: a checkout may not walk away from a
// job the store still says is declared or running, because its next write would land
// ungraded from then on. The marker is left in place.
func TestJobExecVacateRefusesAnInFlightJob(t *testing.T) {
	for _, state := range []types.JobState{types.StateDeclared, types.StateRunning} {
		t.Run(string(state), func(t *testing.T) {
			row := leaseRow("lease-enforcement/wave4/docs", "")
			row.State = state
			root, cacheDir := execFixture(t, row)
			require.NoError(t, job.BindLease(cacheDir, row.ID))

			err := jobExec(t.Context(), root, []string{"--vacate"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), row.ID)
			assert.Contains(t, err.Error(), string(state))
			assert.Equal(t, row.ID, boundMarker(t, cacheDir), "a refused vacate changed nothing")
		})
	}
}

// TestJobExecVacateAllowsAJobThatAlreadyExited is the exact shape of the four-day bug
// this verb exists to fix: a checkout bound to a job that returned its result (exited)
// and that nobody will ever wait on. types.JobState.Live counts exited as live, on
// purpose, so a rejected wait can send work back to the same write paths; but that is a
// property of grading writes against a LIVE lease, not a reason to keep a checkout
// hostage to a lease its own holder is done with. Every later state (pass, fail,
// no_return) vacates the same way, and so does every state the store never declared at
// all: proof there is no lingering boundary for any of them to protect.
func TestJobExecVacateAllowsAJobThatAlreadyExited(t *testing.T) {
	for _, state := range []types.JobState{types.StateExited, types.StatePass, types.StateFail, types.StateNoReturn} {
		t.Run(string(state), func(t *testing.T) {
			row := leaseRow("lease-enforcement/wave4/docs", "")
			row.State = state
			root, cacheDir := execFixture(t, row)
			require.NoError(t, job.BindLease(cacheDir, row.ID))

			out := captureStdout(t, func() {
				require.NoError(t, jobExec(t.Context(), root, []string{"--vacate"}))
			})
			assert.Contains(t, out, row.ID)
			assert.Empty(t, boundMarker(t, cacheDir), "the marker is gone")
		})
	}
}

// A marker that does not read makes every lease resolution in the checkout an error, so
// vacating must clear it rather than fail on the same read.
func TestJobExecVacateClearsAMarkerThatDoesNotRead(t *testing.T) {
	root, cacheDir := execFixture(t)
	require.NoError(t, os.MkdirAll(cacheDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, job.LeaseMarkerName), []byte("not a lease id!\n"), 0o644))

	out := captureStdout(t, func() {
		require.NoError(t, jobExec(t.Context(), root, []string{"--vacate"}))
	})
	assert.Contains(t, out, "cleared a lease marker that did not read")
	assert.Empty(t, boundMarker(t, cacheDir))
}

// boundMarker reads the checkout-wide marker, failing the test on one that does not read.
func boundMarker(t *testing.T, cacheDir string) string {
	t.Helper()
	id, err := job.LeaseFromMarker(cacheDir)
	require.NoError(t, err)
	return id
}

// TestJobExecVacateAllowsAJobTheStoreDoesNotCarry covers the UNKNOWN case: a marker
// naming an id no row declares (a reset ledger, a store from before this one) has no
// boundary left to fail open against, so it vacates rather than wedging the checkout on
// an id nobody can even look up.
func TestJobExecVacateAllowsAJobTheStoreDoesNotCarry(t *testing.T) {
	root, cacheDir := execFixture(t)
	require.NoError(t, job.BindLease(cacheDir, "harness/no-such-job"))

	out := captureStdout(t, func() {
		require.NoError(t, jobExec(t.Context(), root, []string{"--vacate"}))
	})
	assert.Contains(t, out, "harness/no-such-job")
	assert.Empty(t, boundMarker(t, cacheDir))
}

// TestJobExecVacateRejectsBeingCombinedWithOtherArgs: --vacate gives up whichever job
// this checkout holds, so a positional job or a --base to record is nothing it can act on.
func TestJobExecVacateRejectsBeingCombinedWithOtherArgs(t *testing.T) {
	root, _ := execFixture(t)

	err := jobExec(t.Context(), root, []string{"--vacate", "some/job"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "takes no job")

	err = jobExec(t.Context(), root, []string{"--vacate", "--base", "rev1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--base")
}
