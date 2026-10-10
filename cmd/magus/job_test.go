package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/queue"
	qtypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/rogpeppe/go-internal/txtar"
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

// HOLDER says who took each job: the checkout it was taken in, the server for its own,
// and nobody for one still waiting to be taken.
func TestPrintJobTreeNamesWhoHoldsEachJob(t *testing.T) {
	t.Parallel()

	rows := []types.Job{
		{ID: "taken", State: types.StateRunning, Holder: types.HolderSession, CheckoutRoot: "/src/ana"},
		{ID: "waiting", State: types.StateDeclared},
		{ID: "sweep", State: types.StateRunning, Holder: types.HolderServer},
	}
	var out strings.Builder
	printJobTree(&out, types.NewJobList(rows))
	holders := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n")[1:4] {
		f := strings.Fields(line)
		holders[f[0]] = f[1]
	}
	assert.Equal(t, map[string]string{"taken": "ana", "waiting": "-", "sweep": "server"}, holders)
}

// TestGeneratedBoundaryNamesWhatAnOutputCarvesOut: a brief fences a lease's generated
// globs, and the hand files an exclusion carves out of one must read as the exception
// rather than fenced with the rest.
func TestGeneratedBoundaryNamesWhatAnOutputCarvesOut(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(`import "magus";
export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.writesFiles("gen/*.go", "!gen/runtime.go");
}
`), 0o644))
	m, err := magus.Open(t.Context(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	got := generatedBoundary(m, []string{"."}, []string{"gen"})

	require.Len(t, got, 1)
	assert.Equal(t, "gen/*.go", got[0].Path)
	assert.Contains(t, got[0].Reason, "except gen/runtime.go")
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

// An exited holder returned, so a sibling's terms no longer put its paths out of reach.
func TestLeasedBoundarySkipsAnExitedRow(t *testing.T) {
	t.Parallel()

	rows := []types.Job{
		{ID: "a", State: types.StateRunning, WritePaths: []string{"internal/job"}},
		{ID: "b", State: types.StateExited, WritePaths: []string{"internal/guard"}},
		{ID: "c", State: types.StateDeclared, WritePaths: []string{"internal/hint"}},
	}
	var owners []string
	for _, b := range leasedBoundary(rows[0], rows) {
		owners = append(owners, b.Path)
	}
	assert.Equal(t, []string{"internal/hint"}, owners)
}

func TestPrintLedgerTreeSaysWhereAnEmptyPlanComesFrom(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	printJobTree(&out, types.NewJobList(nil))
	assert.Contains(t, out.String(), "`"+hint.JobFork.With("<job>")+"`")
	assert.Contains(t, out.String(), "`"+hint.ToolClient.String()+"` MCP tool")
}

// TestPrintJobStatusFailedGateNamesHowToReadIt pins step 3 of the plan that added goals:
// a failed goal named an output ref but not how to read it, leaving the holder to
// reconstruct `magus query output <ref>` by hand. The line must be rendered through
// hint.QueryOutput (never a hardcoded string), and must appear only for a goal that
// actually failed and actually carries a ref.
func TestPrintJobStatusFailedGateNamesHowToReadIt(t *testing.T) {
	t.Parallel()

	status := job.Status{
		Job:        "plan",
		Violations: []string{`goal "ci": the run behind output ref "outdeadbeef" failed, so its check did not pass`},
		Gates: []types.GateStatus{
			{ID: "ci", Verified: false, OutputRef: "outdeadbeef"},
			{ID: "lint", Verified: true, OutputRef: "outfeedface"},
		},
	}

	var out strings.Builder
	printJobStatus(&out, status)
	got := out.String()

	assert.Contains(t, got, "goal ci: rejected (outdeadbeef)")
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
			status: job.Status{Job: "plan", Verified: true, FootprintKnown: true, Footprint: []types.RegionChange{
				{File: types.FileChange{Path: "a.go"}, Side: types.RegionNew, Lines: [2]int{3, 9}, Declaration: "func X() {", Driver: "golang"},
				{File: types.FileChange{Path: "a.go"}, Side: types.RegionNew, Lines: [2]int{12, 14}, Declaration: "func X() {", Driver: "golang"},
				{File: types.FileChange{Path: "a.go"}, Side: types.RegionOld, Lines: [2]int{20, 21}, Declaration: "func Y() {", Driver: "golang"},
				{File: types.FileChange{Path: "notes.txt"}, Side: types.RegionNew, Lines: [2]int{1, 2}},
				{File: types.FileChange{Path: "notes.txt"}, Side: types.RegionNew, Lines: [2]int{8, 8}},
			}},
			want: "verified plan, recorded pass\n" +
				"footprint: where its diff since the checkpoint landed\n" +
				"  a.go#func X() {\n" +
				"  a.go#func Y() {\n" +
				"  notes.txt:1-2\n" +
				"  notes.txt:8-8\n",
		},
		{
			name:   "known and empty",
			status: job.Status{Job: "plan", Verified: true, FootprintKnown: true},
			want: "verified plan, recorded pass\n" +
				"footprint: no line changed since the checkpoint\n",
		},
		{
			name:   "declined",
			status: job.Status{Job: "plan", Verified: true, FootprintReason: "git does not report changed regions (RegionReporter)"},
			want: "verified plan, recorded pass\n" +
				"footprint: not known, git does not report changed regions (RegionReporter)\n",
		},
		{
			name:   "nobody looked",
			status: job.Status{Job: "plan", Verified: true},
			want: "verified plan, recorded pass\n" +
				"footprint: not known, nothing observed the tree\n",
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

func TestPrintJobTreeRendersTheClaimsVerdict(t *testing.T) {
	t.Parallel()

	rows := []types.Job{
		{ID: "a", State: types.StateRunning, WritePaths: []string{"run.go#executeStages"}},
		{ID: "b", State: types.StateRunning, WritePaths: []string{"run.go#RunCI"}},
	}
	var out strings.Builder
	printJobTree(&out, types.NewJobList(rows))
	_, after, ok := strings.Cut(out.String(), "\noverlaps\n")
	require.True(t, ok, "two claims on one file are listed: %s", out.String())
	assert.Equal(t, "  a and b claim common ground\n"+
		"    a: run.go#executeStages\n"+
		"    b: run.go#RunCI\n"+
		"    claims: disjoint (different declarations of one file: an integration order, not a wait)\n", after)
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
	  "model": "principal"
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
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
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

// A deny path naming a declaration of a file no diff driver reads is refused at fork
// (MGS3031): the guard could only honor it by denying the whole file.
func TestJobForkRefusesAnUngradableDenyPath(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	resetWorkspaceMemo(t)
	root, cacheDir := execFixture(t)
	for name, body := range map[string]string{".gitattributes": "*.go diff=golang\n", "run.go": "package run\n\nfunc A() {}\n", "notes.txt": "Intro\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	fork := func(id, deny string) error {
		return jobFork(t.Context(), root, []string{id, "--check", "test .", "--write-paths", "run.go,notes.txt", "--deny-paths", deny})
	}

	err = fork("refused", "notes.txt#Intro")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MGS3031")
	assert.Contains(t, err.Error(), `deny path "notes.txt" has no diff driver`)

	require.NoError(t, fork("accepted", "run.go#A"))
	rows, err := job.NewStore(job.Location{CacheDir: cacheDir, Root: root}).List()
	require.NoError(t, err)
	require.Len(t, rows, 1, "a refused fork writes no row")
	assert.Equal(t, []any{"accepted", []string{"run.go#A"}}, []any{rows[0].ID, rows[0].DenyPaths})
}

// The store the CLI opens resolves a ref the worker recorded in its own checkout, so an
// orchestrator can exit a job on its worker's behalf.
func TestJobExitResolvesARefInTheJobsCheckout(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	worker := t.TempDir()
	recorded, err := cache.NewOutputStore(filepath.Join(worker, ".magus")).
		Persist(t.Context(), "key1", []byte("ok\n"), cache.OutputDescriptor{Project: ".", Target: "test"})
	require.NoError(t, err)

	row := leaseRow("w", "")
	row.CheckoutRoot, row.Registered = worker, 1
	root, _ := execFixture(t, row)
	store, err := openJobs(root)
	require.NoError(t, err)
	here := func(context.Context, string) (types.JobAttempt, error) { return types.JobAttempt{}, nil }
	result := types.JobResult{Job: row.ID, Validation: types.JobResultValidation{OutputRef: recorded.Ref}}

	stored, err := job.Exit(t.Context(), store, row.ID, &result, here)
	require.NoError(t, err)
	assert.Equal(t, &types.JobAttempt{Found: true, Ref: recorded.Ref, Project: ".", Target: "test"}, stored.Attempt)
}

// Prune prints each job it ends with the reason, then the count; under --dry-run it says
// what would end and leaves the row live.
func TestJobPrunePrintsEachRowAndTheCount(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	exited := leaseRow("returned", "")
	exited.State = types.StateExited
	busy := leaseRow("busy", "")
	busy.Registered = time.Now().Unix()
	root, cacheDir := execFixture(t, exited, busy)
	prev := globalCfg.DryRun
	t.Cleanup(func() { globalCfg.DryRun = prev })

	globalCfg.DryRun = true
	out := captureStdout(t, func() { require.NoError(t, jobPrune(t.Context(), root, nil)) })
	assert.Regexp(t, "^would end returned: exited .* ago and nobody collected its result with `magus job wait`\n"+
		"would end 1 job\\(s\\); --dry-run ended nothing\n$", out)
	store := job.NewStore(job.Location{CacheDir: cacheDir, Root: root})
	rows, err := store.List()
	require.NoError(t, err)
	assert.Equal(t, types.StateExited, rows[0].State)

	globalCfg.DryRun = false
	out = captureStdout(t, func() { require.NoError(t, jobPrune(t.Context(), root, nil)) })
	assert.Regexp(t, "^ended returned: exited .* ago and nobody collected its result with `magus job wait`\nended 1 job\\(s\\)\n$", out)
	rows, err = store.List()
	require.NoError(t, err)
	assert.Equal(t, []types.JobState{types.StateNoReturn, types.StateRunning}, []types.JobState{rows[0].State, rows[1].State})
}

// Apply prints the spec diff, writes nothing under --dry-run, and keeps the job's state
// either way.
func TestJobApplyPrintsTheSpecDiffAndKeepsTheState(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	row := leaseRow("w", "")
	row.State, row.WritePaths, row.Check = types.StateRunning, []string{"a.go", "b.go"}, &types.LeaseCheck{Target: "test", Project: "."}
	row.Validation = row.Check.String()
	root, cacheDir := execFixture(t, row)
	store := job.NewStore(job.Location{CacheDir: cacheDir, Root: root})
	record := filepath.Join(t.TempDir(), "w.json")
	require.NoError(t, os.WriteFile(record, fmt.Appendf(nil,
		`{"schema_version":%d,"id":"w","model":"standard","write_paths":["b.go","c.go"],"check":{"target":"test","project":"."}}`,
		types.JobSchemaVersion), 0o644))
	args := []string{"-f", record}

	globalCfg.DryRun = true
	t.Cleanup(func() { globalCfg.DryRun = false })
	out := captureStdout(t, func() { require.NoError(t, jobApply(t.Context(), root, args)) })
	assert.Equal(t, "would update w, still running:\n"+
		"  write_paths +c.go -a.go\n"+
		"dry run: nothing written; rerun without --dry-run to write it\n", out)
	rows, err := store.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"a.go", "b.go"}, rows[0].WritePaths)

	globalCfg.DryRun = false
	out = captureStdout(t, func() { require.NoError(t, jobApply(t.Context(), root, args)) })
	assert.True(t, strings.HasPrefix(out, "updated w, still running:\n  write_paths +c.go -a.go\n"), out)
	rows, err = store.List()
	require.NoError(t, err)
	assert.Equal(t, []any{[]string{"b.go", "c.go"}, types.StateRunning}, []any{rows[0].WritePaths, rows[0].State})

	require.Error(t, jobApply(t.Context(), root, nil), "an apply naming no records is a usage error")
}

// bindCheckout records id as the checkout's binding, as the guard does when a caller whose
// host names no session takes a job there.
func bindCheckout(t *testing.T, cacheDir, id string) {
	t.Helper()
	require.NoError(t, job.NewStore(job.Location{CacheDir: cacheDir}).Bind(job.Caller{}, id))
}

// TestJobExecWritesNoMarker pins the half of the 2026-09-26 fix that lives in the CLI: exec
// records the base and the checkout, and binds nobody. A CLI process cannot tell a subagent
// from its parent, so a binding it wrote was the checkout's, and the parent sharing that
// checkout was graded as the subagent from its next call.
func TestJobExecWritesNoMarker(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	row := leaseRow("lease-enforcement/wave4/docs", "")
	row.State = types.StateRunning
	root, cacheDir := execFixture(t, row)

	out := captureStdout(t, func() {
		require.NoError(t, jobExec(t.Context(), root, []string{row.ID, "--base", "rev1"}))
	})
	assert.Contains(t, out, "took "+row.ID)

	_, err := os.Stat(job.MarkerPath(cacheDir))
	assert.True(t, os.IsNotExist(err), "exec writes no checkout record")
	lease, from := job.ActingLease(cacheDir, "")
	assert.Equal(t, []any{"", types.LeaseSource("")}, []any{lease, from}, "the checkout acts under nothing")

	rows, err := job.NewStore(job.Location{CacheDir: cacheDir, Root: root}).List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	abs, err := filepath.Abs(root)
	require.NoError(t, err)
	assert.Equal(t, []any{"rev1", abs}, []any{rows[0].ReportedBase, rows[0].CheckoutRoot}, "the base and the checkout are recorded")
}

// Exec takes exactly one job: the read form it had printed a binding the CLI no longer
// holds, and a job that already ended has nothing left to take.
func TestJobExecRefusesAnythingButOneLiveJob(t *testing.T) {
	done := leaseRow("lease-enforcement/wave4/done", "")
	done.State = types.StatePass
	root, _ := execFixture(t, done)

	for name, args := range map[string][]string{
		"no job":        nil,
		"two jobs":      {"a", "b"},
		"an unknown id": {"harness/no-such-job", "--base", "rev1"},
		"an ended job":  {done.ID, "--base", "rev1"},
	} {
		assert.Error(t, jobExec(t.Context(), root, args), name)
	}
}

// inflightFixture is the in-flight plan's fixture: two job trees, one checkout on
// fork-row-rebase, eight open changes on main listed out of plan order, one plan of one
// partition and one verdict.
func inflightFixture() (types.JobList, job.InflightInput) {
	rows := []types.Job{
		{ID: "fleet", State: types.StateRunning},
		{ID: "child-job-wait", Parent: "fleet", State: types.StatePass, CheckoutRoot: "/w/child-job-wait"},
		{ID: "status-wait", Parent: "fleet", State: types.StateRunning, CheckoutRoot: "/w/status-wait"},
		{ID: "fork-row-rebase", Parent: "fleet", State: types.StateRunning, CheckoutRoot: "/w/fork-row-rebase"},
		{ID: "inflight-view", Parent: "fleet", State: types.StateRunning, ReadOnly: true, CheckoutRoot: "/w/main"},
		{ID: "their-fleet", State: types.StateRunning},
		{ID: "their-work", Parent: "their-fleet", State: types.StateRunning, CheckoutRoot: "/w/theirs"},
	}
	change := func(id, branch, title, author string) qtypes.Change {
		return qtypes.Change{ID: id, Head: strings.Repeat(id, 14)[:40], Branch: branch, Base: "main", Title: title, Author: author, Method: qtypes.MethodSquash}
	}
	c438 := change("438", "guard-reads-and-searches", "guard reads and searches", "egladman")
	c443 := change("443", "fork-row-rebase", "rebase the fork row onto the job store before a worker forks its own child", "egladman")
	c445 := change("445", "trimpath-tests-more", "trimpath the remaining tests", "egladman")
	c450 := change("450", "merge-queue-dashboard", "merge queue dashboard", "egladman")
	c456 := change("456", "fix-444-review", "fix the 444 review", "egladman")
	c457 := change("457", "theirs", "their change", "priya")
	c459 := change("459", "status-wait", "status wait pipe", "egladman")
	in := job.InflightInput{
		Fetch: &types.InflightFetch{Provider: "github", Host: "github.com", Base: "main", At: 1790514000, ElapsedMS: 812},
		Changes: qtypes.Changes{
			Base:    "main",
			Changes: []qtypes.Change{c443, c438, c450, c445, c456, c457, c459},
			Unqueued: []qtypes.UnqueuedChange{
				{ID: "458", Head: strings.Repeat("8", 40), Branch: "child-job-wait", Base: "main", Title: "child job wait", Author: "egladman", Mark: qtypes.MarkKickedBack},
			},
		},
		Plan: &qtypes.Plan{
			Base: "main", BaseCommit: "f223bb1" + strings.Repeat("0", 33), Depth: 1,
			Partitions: [][]qtypes.Change{{c438, c443, c445, c450, c456, c457}},
			Verdicts:   []qtypes.Verdict{{Change: c459, Decision: qtypes.DecisionKick, Code: qtypes.CodeKickRed, Reason: "pr hygiene: changelog fragment missing"}},
		},
		Branches: map[string]string{
			"/w/child-job-wait": "child-job-wait", "/w/status-wait": "status-wait", "/w/fork-row-rebase": "fork-row-rebase",
			"/w/main": "main", "/w/theirs": "theirs",
		},
		Me: job.Identity{Lease: "fork-row-rebase", Login: "egladman"},
	}
	return types.JobList{Jobs: rows}, in
}

// The plan's proof: the reader's two kicked-back changes first, the five queued in plan
// order whatever order the provider listed them in, and everyone else as one count line.
func TestLsJobsPrintsTheChangesInFlight(t *testing.T) {
	t.Parallel()
	list, in := inflightFixture()
	render := func(all bool) string {
		var out strings.Builder
		printInflight(&out, job.Inflight(list, in), all)
		return out.String()
	}
	head := "in flight on main at f223bb100000, as of 2026-09-27 13:00 UTC (github, github.com, 812ms)\n"
	kicked := "" +
		"  #459  status wait pipe  KICK_RED: pr hygiene: changelog fragment missing  job status-wait\n" +
		"  #458  child job wait    kicked back                                       job child-job-wait\n"
	queued := "" +
		"  #438  guard reads and searches                            pos 1\n" +
		"  #443  rebase the fork row onto the job store before a...  pos 2  job fork-row-rebase\n" +
		"  #445  trimpath the remaining tests                        pos 3\n" +
		"  #450  merge queue dashboard                               pos 4\n" +
		"  #456  fix the 444 review                                  pos 5\n"
	tail := "jobs without a change (1)\n  inflight-view\n"
	refetch := "refetch: magus queue ls --provider github --base main\n"

	assert.Equal(t, head+"needs me (2)\n"+kicked+"queued, in plan order (5)\n"+queued+tail+"others: 1 queued\n"+refetch, render(false))
	assert.Equal(t, head+"needs the author (2)\n"+kicked+"queued, in plan order (6)\n"+queued+
		"  #457  their change                                        pos 6  job their-work\n"+tail+refetch, render(true))
	assert.Equal(t, render(false), render(false), "one store and one snapshot render the same bytes")
}

func TestLsJobsRendersTheRecordTheSameTwice(t *testing.T) {
	t.Parallel()
	list, in := inflightFixture()
	first, err := json.Marshal(job.Inflight(list, in))
	require.NoError(t, err)
	second, err := json.Marshal(job.Inflight(list, in))
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second))
}

func TestLsJobsSaysWhenTheQueueWasNeverFetched(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	root, _ := execFixture(t, leaseRow("fleet", ""))
	withOutput(t, "")
	out := captureStdout(t, func() {
		require.NoError(t, lsJobs(root, nil))
	})
	assert.Contains(t, out, "in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes\n")
}

// Read end to end from a snapshot: a caller no job or login names sees everyone's.
func TestLsJobsReadsTheSnapshot(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	root, _ := execFixture(t, leaseRow("fleet", ""))
	_, in := inflightFixture()
	require.NoError(t, queue.WriteSnapshot(root, queue.Snapshot{Fetched: *in.Fetch, Changes: in.Changes}))

	withOutput(t, "json")
	out := captureStdout(t, func() {
		require.NoError(t, lsJobs(root, nil))
	})
	var got types.JobList
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	var ids []string
	for _, c := range got.Changes {
		ids = append(ids, c.ID)
	}
	assert.Equal(t, []string{"443", "438", "450", "445", "456", "457", "459", "458"}, ids)
}

func TestDescribeJobPrintsItsChange(t *testing.T) {
	t.Parallel()
	list, in := inflightFixture()
	joined := job.Inflight(list, in)
	var out strings.Builder
	printJobChange(&out, joined.Changes, "fork-row-rebase")
	printJobChange(&out, joined.Changes, "child-job-wait")
	printJobChange(&out, joined.Changes, "inflight-view")
	assert.Equal(t, "\nchange: #443 rebase the fork row onto the job store before a..., the queue's turn, pos 2\n"+
		"\nchange: #458 child job wait, its author's turn, kicked back\n", out.String())
}

func TestLsJobsClipsATitleAtAWord(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "short", clipTitle("short", 48))
	assert.Equal(t, "one two...", clipTitle("one two three", 9))
	assert.Equal(t, "abcdefgh...", clipTitle("abcdefghijk", 8), "no space to cut at")
}

// Every job verb's help ends its prose with one command a person types, spelled with the
// verb itself, so `-h` answers "what does a use of this look like" without a docs page.
func TestJobHelpCarriesAPersonRunExample(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	ctx := t.Context()
	verbs := []struct {
		name string
		run  func(args []string) error
	}{
		{"job fork", func(a []string) error { return jobFork(ctx, "", a) }},
		{"job apply", func(a []string) error { return jobApply(ctx, "", a) }},
		{"job exec", func(a []string) error { return jobExec(ctx, "", a) }},
		{"job exit", func(a []string) error { return jobExit(ctx, "", a) }},
		{"job wait", func(a []string) error { return jobWait(ctx, "", a) }},
		{"job watch", func(a []string) error { return jobWatch(ctx, "", a) }},
		{"job rm", func(a []string) error { return jobDelete(ctx, "", a) }},
		{"job prune", func(a []string) error { return jobPrune(ctx, "", a) }},
		{"ls jobs", func(a []string) error { return lsJobs("", a) }},
		{"describe job", func(a []string) error { return describeJob(ctx, "", a) }},
	}
	for _, v := range verbs {
		var err error
		help := captureStderr(t, func() { err = v.run([]string{"-h"}) })
		require.ErrorIs(t, err, flag.ErrHelp, v.name)
		_, example, found := strings.Cut(help, "\nExample:\n  ")
		if !found {
			_, example, found = strings.Cut(help, "\nExamples:\n  ")
		}
		require.True(t, found, "`magus %s -h` carries no Example:\n%s", v.name, help)
		line, _, _ := strings.Cut(example, "\n")
		assert.True(t, strings.HasPrefix(line, "magus "+v.name+" "), "`magus %s -h` example runs something else: %q", v.name, line)
	}
}

// guideMarker pins the fenced block on the next line to a section of a job_people script.
var guideMarker = regexp.MustCompile(`^<!-- golden: (job_people_[a-z]+\.txtar) (\S+) -->$`)

// The job guides show people running the CLI, and their transcripts are the outputs the
// job_people scripts compare byte for byte: a block marked with guideMarker must equal
// "$ <the command>" plus the golden that command's stdout was compared against, or, for a
// section no command's stdout is compared against (an input file), the section itself.
// Every job_people script is shown somewhere, so a script nobody reads cannot pass for
// documentation.
func TestJobGuideMatchesItsScripts(t *testing.T) {
	t.Parallel()
	guides := filepath.Join("..", "..", "docs", "guides")
	pages, err := filepath.Glob(filepath.Join(guides, "jobs*.md"))
	require.NoError(t, err)
	more, err := filepath.Glob(filepath.Join(guides, "jobs", "*.md"))
	require.NoError(t, err)
	pages = append(pages, more...)
	require.NotEmpty(t, pages)

	shown := map[string]bool{}
	for _, page := range pages {
		data, err := os.ReadFile(page)
		require.NoError(t, err)
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			m := guideMarker.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			// One blank line may separate the two, as the formatter leaves it.
			fence := i + 1
			if fence < len(lines) && lines[fence] == "" {
				fence++
			}
			require.Less(t, fence, len(lines), "%s:%d: a marker with no block under it", page, i+1)
			require.True(t, strings.HasPrefix(lines[fence], "```"), "%s:%d: the marker must sit above a fence", page, i+1)
			end := slices.Index(lines[fence+1:], "```")
			require.GreaterOrEqual(t, end, 0, "%s:%d: the fenced block never closes", page, fence+1)
			got := strings.Join(lines[fence+1:fence+1+end], "\n") + "\n"
			assert.Equal(t, guideBlock(t, m[1], m[2]), got, "%s:%d shows something %s does not pin", page, i+1, m[1])
			shown[m[1]] = true
		}
	}
	scripts, err := filepath.Glob(filepath.Join("testdata", "script", "job_people_*.txtar"))
	require.NoError(t, err)
	require.NotEmpty(t, scripts)
	for _, script := range scripts {
		assert.True(t, shown[filepath.Base(script)], "no job guide shows %s", script)
	}
}

// guideBlock renders what a guide must show for section of script: the section as is, or,
// when a `cmp stdout $WORK/<section>` compares a command's output against it, that command
// as a transcript line followed by the section. The command is the nearest `exec magus`
// above the cmp, since the steps between only normalize its output. A stdin the archive
// carries is shown as the heredoc a person would type; one the script generated, as a
// redirect from the file of that name.
func guideBlock(t *testing.T, script, section string) string {
	t.Helper()
	archive, err := txtar.ParseFile(filepath.Join("testdata", "script", script))
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range archive.Files {
		files[f.Name] = string(f.Data)
	}
	golden, ok := files[section]
	require.True(t, ok, "%s has no section %s", script, section)

	steps := strings.Split(string(archive.Comment), "\n")
	cmp := slices.Index(steps, "cmp stdout $WORK/"+section)
	if cmp < 0 {
		return golden
	}
	run := cmp - 1
	for run >= 0 && !strings.HasPrefix(strings.TrimPrefix(steps[run], "! "), "exec magus ") {
		run--
	}
	require.GreaterOrEqual(t, run, 0, "%s compares %s against no magus command", script, section)
	command := "$ " + strings.TrimPrefix(strings.TrimPrefix(steps[run], "! "), "exec ")
	if run > 0 {
		if in, ok := strings.CutPrefix(steps[run-1], "stdin $WORK/"); ok {
			if body, archived := files[in]; archived {
				return command + " <<'EOF'\n" + body + "EOF\n" + golden
			}
			command += " < " + in
		}
	}
	return command + "\n" + golden
}
