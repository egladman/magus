package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/queue"
	qtypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/rogpeppe/go-internal/txtar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A checkout whose magusfile this magus cannot load keeps one line naming the code, never
// the spell dump a load failure renders.
func TestUnloadableMagusfileIsOneLine(t *testing.T) {
	t.Parallel()
	coded := fmt.Errorf("loading magusfile: %w", types.DiagnosticErrorf(types.NoCITarget, "no ci target\n  at magusfile.buzz:1\n  spell dump"))
	assert.EqualError(t, unloadableMagusfile(coded), fmt.Sprintf("this magus cannot load its magusfile (%s)", types.NoCITarget))
	assert.EqualError(t, unloadableMagusfile(errors.New("parse failed\n  spell dump")), "parse failed")
}

func leaseRow(id, parent string) types.Job {
	return types.Job{ID: id, Parent: parent, State: types.StateRunning, Model: "standard"}
}

// A job verb, `ls jobs` and `describe job` read the job store first and open the workspace
// themselves where a section needs it, so startup neither preloads it nor forwards: the
// preload put a magusfile evaluation in front of every fork and exit. The other nouns of
// ls and describe still list the workspace.
func TestJobReadsSkipTheWorkspacePreload(t *testing.T) {
	t.Parallel()

	configOnly := dispatchProfile{needsConfig: true}
	preload := dispatchProfile{needsConfig: true, needsForward: true, needsWorkspace: true}
	for _, tc := range []struct {
		sub  string
		args []string
		want dispatchProfile
	}{
		{"job", []string{"fork", "a", "--write-paths", "x"}, configOnly},
		{"job", []string{"exit", "a"}, configOnly},
		{"job", []string{"wait", "a"}, configOnly},
		{"job", []string{"run", "sync-graph"}, configOnly},
		{"ls", []string{"jobs"}, configOnly},
		{"ls", []string{"-o", "json", "job"}, configOnly},
		{"ls", []string{"targets", "jobs-dir"}, preload},
		{"ls", nil, preload},
		{"describe", []string{"job", "a"}, configOnly},
		{"describe", []string{"jobs", "a"}, configOnly},
		{"describe", []string{"project", "."}, preload},
	} {
		assert.Equal(t, tc.want, resolveProfile(tc.sub, tc.args), "%s %v", tc.sub, tc.args)
	}
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
	w := testkit.NewWorkspace(t)
	w.Magusfile(`import "magus";
export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.writesFiles(["gen/*.go", "!gen/runtime.go"]);
}
`)
	m, err := magus.Open(t.Context(), w.Root(), magus.WithSpawnRule(magus.TierCeiling))
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

// A soft path is free until its holder writes it, so a sibling's terms leave it in reach.
func TestLeasedBoundarySkipsASoftPath(t *testing.T) {
	t.Parallel()

	held := types.Job{ID: "b", State: types.StateRunning, WritePaths: []string{"internal/job/plan.go", "internal/job/plan_test.go"}}
	soft := held
	soft.SoftPaths = []string{"internal/job/plan_test.go"}
	me := types.Job{ID: "a", State: types.StateRunning, WritePaths: []string{"internal/job/plan_test.go"}}
	owned := func(other types.Job) []string {
		var out []string
		for _, b := range leasedBoundary(me, []types.Job{me, other}) {
			out = append(out, b.Path)
		}
		return out
	}
	assert.Equal(t, []string{"internal/job/plan.go"}, owned(soft))
	assert.Equal(t, []string{"internal/job/plan.go", "internal/job/plan_test.go"}, owned(held), "a hardened path is owned")
}

func TestPrintLedgerTreeSaysWhereAnEmptyPlanComesFrom(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	printJobTree(&out, types.NewJobList(nil))
	assert.Contains(t, out.String(), fmt.Sprintf("`%s`", hint.JobFork.With("<job>")))
	assert.Contains(t, out.String(), fmt.Sprintf("`%s` MCP tool", hint.ToolClient.String()))
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
	flags.briefFormat = "buzz"
	// The version is READ from the constant, not typed as a digit: this case is about the
	// two doors agreeing on the FIELDS, and a fixture whose version fell behind would fail
	// it at the version check instead, naming nothing it came here to compare.
	piped, _, err := job.DecodeStdinDeclaration(strings.NewReader(fmt.Sprintf(`{
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
	  "brief_format": "buzz"
	}`))
	require.NoError(t, err)

	fromFlags := flags.row("adj/store")
	require.NoError(t, fromFlags.Validate())

	var a, b types.Job
	fromFlags.Apply(&a)
	piped.Apply(&b)
	assert.Equal(t, a, b)
	assert.Equal(t, types.BriefFormatBuzz, a.BriefFormat)
}

// A brief format is one of the two, refused at fork rather than read as markdown later.
func TestForkRefusesAnUnknownBriefFormat(t *testing.T) {
	t.Parallel()
	err := forkFlags{criteria: "c", briefFormat: "json"}.row("adj/store").Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, `brief_format "json"`)
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
	w := testkit.NewWorkspace(t)
	w.Write("magus.yaml", "")
	return seedJobStore(t, w.Root(), rows...)
}

// execRepoFixture is execFixture over a git repository holding the same magus.yaml.
func execRepoFixture(t *testing.T, rows ...types.Job) (repo *testkit.Repo, cacheDir string) {
	t.Helper()
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	repo = testkit.NewRepo(t, map[string]string{"magus.yaml": ""})
	_, cacheDir = seedJobStore(t, repo.Root(), rows...)
	return repo, cacheDir
}

func seedJobStore(t *testing.T, root string, rows ...types.Job) (string, string) {
	t.Helper()
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	for _, row := range rows {
		_, err := store.Update(t.Context(), row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
	}
	return root, cacheDir
}

// A deny path naming a declaration of a file no diff driver reads is refused at fork
// (MGS3031): the guard could only honor it by denying the whole file.
func TestJobForkRefusesAnUngradableDenyPath(t *testing.T) {
	resetWorkspaceMemo(t)
	repo, cacheDir := execRepoFixture(t)
	root := repo.Root()
	repo.WriteTree(map[string]string{".gitattributes": "*.go diff=golang\n", "run.go": "package run\n\nfunc A() {}\n", "notes.txt": "Intro\n"})
	fork := func(id, deny string) error {
		return jobFork(t.Context(), root, []string{id, "--check", "test .", "--write-paths", "run.go,notes.txt", "--deny-paths", deny})
	}

	err := fork("refused", "notes.txt#Intro")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MGS3031")
	assert.Contains(t, err.Error(), `deny path "notes.txt" has no diff driver`)

	require.NoError(t, fork("accepted", "run.go#A"))
	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).List()
	require.NoError(t, err)
	require.Len(t, rows, 1, "a refused fork writes no row")
	assert.Equal(t, []any{"accepted", []string{"run.go#A"}}, []any{rows[0].ID, rows[0].DenyPaths})
}

// A row naming no write paths bounds its holder by nothing unless it is read-only, so fork
// refuses it even with a check, and the refusal teaches the script check a scout passes by.
func TestJobForkRefusesARowScopedByNothing(t *testing.T) {
	resetWorkspaceMemo(t)
	root, cacheDir := execFixture(t)
	fork := func(args ...string) error { return jobFork(t.Context(), root, args) }

	err := fork("scout", "--criteria", "find the callers", "--check", "test .")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scout names no write paths and is not read-only")
	assert.Contains(t, err.Error(), "--read-only")
	assert.Contains(t, err.Error(), `{"check": {"script": "<probe>.buzz"}}`)
	assert.Contains(t, err.Error(), fmt.Sprintf("`%s`", hint.Buzz.With("--record", "<probe>.buzz")))

	require.NoError(t, fork("reader", "--read-only", "--criteria", "find the callers"))
	require.NoError(t, fork("writer", "--write-paths", "run.go", "--check", "test ."))
	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).List()
	require.NoError(t, err)
	got := make(map[string]bool, len(rows))
	for _, r := range rows {
		got[r.ID] = r.ReadOnly
	}
	assert.Equal(t, map[string]bool{"reader": true, "writer": false}, got, "a refused fork writes no row")
}

// A magusfile mid-edit still opens the store for model tiers, so the fork that fixes it can
// be declared, and the open names the tier it left unassigned: a tier declaration that
// failed to load must not read as one that declared nothing.
func TestOpenJobsOpensTheStoreWhenTheWorkspaceWillNotLoad(t *testing.T) {
	resetWorkspaceMemo(t)
	root, _ := execFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\nmagus\\project({\n"), 0o644))

	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	_, err := openJobs(t.Context(), root, jobOpen{DryRun: true})
	require.NoError(t, err)
	assert.Empty(t, logged.String(), "a store opened without model tiers never loads the workspace")
	_, err = openJobs(t.Context(), root, jobOpen{ModelTiers: true})
	require.NoError(t, err)
	assert.Contains(t, logged.String(), "no model tier was assigned")
	assert.Contains(t, logged.String(), "magusfile.buzz")
}

// A fork resolves the checkpoint it is handed: a full-length revision the repository
// never held is refused and writes no row, and a real abbreviated one is stored in full.
func TestJobForkResolvesItsCheckpoint(t *testing.T) {
	resetWorkspaceMemo(t)
	repo, cacheDir := execRepoFixture(t)
	root := repo.Root()
	head := repo.Head()
	fork := func(id, checkpoint string) error {
		return jobFork(t.Context(), root, []string{id, "--check", "test .", "--write-paths", "magus.yaml", "--checkpoint", checkpoint})
	}

	const madeUp = "34b41fd546ba8fa9c71a6a64e4ea0f3e93d5a7bc"
	err := fork("made-up", madeUp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), madeUp)
	assert.Contains(t, err.Error(), "`magus vcs checkpoint -o name`")

	captureStdout(t, func() { require.NoError(t, fork("real", head[:9])) })
	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).List()
	require.NoError(t, err)
	require.Len(t, rows, 1, "a refused fork writes no row")
	assert.Equal(t, []string{"real", head}, []string{rows[0].ID, rows[0].Checkpoint})
}

// A --stdin record and --checkout combine: the fork makes the checkout the flag names and
// judges the record against it, so a live job in the forking checkout refuses nothing.
// The same record without the flag is still refused, and a record naming another checkout
// is refused rather than one of the two being dropped.
func TestJobForkFromStdinMakesTheCheckoutTheFlagNames(t *testing.T) {
	resetWorkspaceMemo(t)
	repo, cacheDir := execRepoFixture(t, types.Job{
		ID: "live", State: types.StateRunning, WritePaths: []string{"a.go"},
		Check: &types.LeaseCheck{Target: "test", Project: "."},
	})
	root := repo.Root()
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	_, err := store.Exec(t.Context(), "live")
	require.NoError(t, err)
	record := func(id, checkout string) string {
		return fmt.Sprintf(`{"schema_version": %d, "id": %q, "checkout": %q, "write_paths": ["magus.yaml"],`+
			` "check": {"target": "test", "project": "."}}`, types.JobSchemaVersion, id, checkout)
	}
	fork := func(stdin string, args ...string) error {
		writeStdin(t, stdin)
		return jobFork(t.Context(), root, append([]string{"--stdin"}, args...))
	}

	err = fork(record("shared", ""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "live is live in this checkout")

	own := filepath.Join(t.TempDir(), "own")
	err = fork(record("elsewhere", filepath.Join(t.TempDir(), "other")), "--checkout", own)
	require.Error(t, err)
	assert.Contains(t, err.Error(), own)

	captureStdout(t, func() { require.NoError(t, fork(record("own", ""), "--checkout", own)) })
	rows, err := store.List()
	require.NoError(t, err)
	i := slices.IndexFunc(rows, func(row types.Job) bool { return row.ID == "own" })
	require.GreaterOrEqual(t, i, 0, "the fork wrote its row")
	require.NotNil(t, rows[i].Worktree)
	assert.Equal(t, own, rows[i].Worktree.Root)
	assert.DirExists(t, own)
}

// A row forked from inside a job's checkout got no worktree record, so the guard refused
// its holder's magus there as a sibling; an integration row forked from the forker's own
// linked checkout bound its integrator to the forker's tree.
func TestJobForkBindsTheCheckoutItRunsIn(t *testing.T) {
	resetWorkspaceMemo(t)
	repo, cacheDir := execRepoFixture(t)
	root := repo.Root()
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	fork := func(at, stdin string, args ...string) error {
		resetStartupSingletons()
		writeStdin(t, stdin)
		return jobFork(t.Context(), at, append([]string{"--stdin"}, args...))
	}
	record := func(id, path string) string {
		return fmt.Sprintf(`{"schema_version": %d, "id": %q, "write_paths": [%q], "check": {"target": "test", "project": "."}}`,
			types.JobSchemaVersion, id, path)
	}

	own := filepath.Join(t.TempDir(), "own")
	captureStdout(t, func() { require.NoError(t, fork(root, record("integrate", "magus.yaml"), "--checkout", own)) })
	_, err := store.Update(t.Context(), "integrate", func(u *types.Job) { u.State = types.StateExited })
	require.NoError(t, err)
	captureStdout(t, func() { require.NoError(t, fork(own, record("finish", "notes.txt"))) })
	rows, err := store.List()
	require.NoError(t, err)
	i := slices.IndexFunc(rows, func(row types.Job) bool { return row.ID == "finish" })
	require.GreaterOrEqual(t, i, 0)
	require.NotNil(t, rows[i].Worktree, "a fork run inside a job's checkout records it")
	assert.True(t, rows[i].Worktree.Standing())
	assert.Equal(t, own, rows[i].Worktree.Root)

	repo.Commit("raise prices", map[string]string{"pricing.txt": "apples 2\n"})
	repo.Git("branch", "done")
	repo.Git("reset", "-q", "--hard", "HEAD~1")
	session := repo.AddWorktree("session").Root()
	integration := fmt.Sprintf(`{"schema_version": %d, "id": "merge", "integrates": ["done"], "check": {"target": "test", "project": "."}}`, types.JobSchemaVersion)
	err = fork(session, integration)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the forker's own linked checkout")
	assert.Contains(t, err.Error(), "--checkout <dir>")
}

// The checkpoint of an integration fork into a standing checkout is that checkout's
// revision, not the forker's, so the integrator's merge is judged from where it starts.
func TestJobForkIntegrationIntoAStandingCheckoutRecordsTheCheckoutsRevision(t *testing.T) {
	resetWorkspaceMemo(t)
	repo, cacheDir := execRepoFixture(t)
	root := repo.Root()
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})

	standing := filepath.Join(t.TempDir(), "integrator")
	repo.Git("worktree", "add", "-q", "-b", "integrator", standing)
	checkoutHead := repo.Head()
	repo.Commit("raise prices", map[string]string{"pricing.txt": "apples 2\n"})
	repo.Git("branch", "done")
	repo.Git("reset", "-q", "--hard", "HEAD~1")
	repo.Commit("forker moves on", map[string]string{"forker.txt": "ahead\n"})
	require.NotEqual(t, checkoutHead, repo.Head())

	writeStdin(t, fmt.Sprintf(`{"schema_version": %d, "id": "merge", "integrates": ["done"], "check": {"target": "test", "project": "."}}`, types.JobSchemaVersion))
	captureStdout(t, func() {
		require.NoError(t, jobFork(t.Context(), root, []string{"--stdin", "--checkout", standing}))
	})
	rows, err := store.List()
	require.NoError(t, err)
	i := slices.IndexFunc(rows, func(row types.Job) bool { return row.ID == "merge" })
	require.GreaterOrEqual(t, i, 0)
	assert.Equal(t, []string{checkoutHead, "pricing.txt"}, []string{rows[i].Checkpoint, strings.Join(rows[i].WritePaths, ",")})
}

// An integration row that exited red was refused a same-id re-fork once its branch was
// merged ("nothing to merge"), so a small follow-up needed a second row.
func TestJobForkTakesAnUnfinishedIntegrationAgain(t *testing.T) {
	resetWorkspaceMemo(t)
	repo, cacheDir := execRepoFixture(t)
	root := repo.Root()
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	repo.Commit("raise prices", map[string]string{"pricing.txt": "apples 2\n"})
	repo.Git("branch", "done")
	repo.Git("reset", "-q", "--hard", "HEAD~1")
	integration := fmt.Sprintf(`{"schema_version": %d, "id": "merge", "integrates": ["done"], "check": {"target": "test", "project": "."}}`, types.JobSchemaVersion)
	fork := func() error {
		resetStartupSingletons()
		writeStdin(t, integration)
		return jobFork(t.Context(), root, []string{"--stdin"})
	}

	captureStdout(t, func() { require.NoError(t, fork()) })
	_, err := store.Update(t.Context(), "merge", func(u *types.Job) { u.State = types.StateExited })
	require.NoError(t, err)
	repo.Git("merge", "-q", "--ff-only", "done")

	captureStdout(t, func() { require.NoError(t, fork(), "its check never passed, so the row takes the follow-up") })
	rows, err := store.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, []string{"pricing.txt"}, rows[0].WritePaths, "the re-fork keeps the paths the row held")

	_, err = store.Delete(t.Context(), "merge", true)
	require.NoError(t, err)
	err = fork()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to merge", "with no row to finish, an empty merge is still refused")
}

// limitHostSpell is a harness whose host waits ten minutes at most on a foreground call.
const limitHostSpell = `import "magus/spell";

export fun mgs_getName() > str { return "limit-host"; }

export fun mgs_getHarnessConfig() > HarnessConfig {
    return HarnessConfig{path = "hooks.json"};
}

export fun mgs_getSkills() > HarnessSkills {
    return HarnessSkills{form = SkillForm.short};
}

export fun mgs_getLimits() > Limits {
    return Limits{foregroundDefaultMs = 120000, foregroundMaxMs = 600000};
}
`

// testTarget is the `test` target the fork's check names, which the workspace must have.
const testTarget = "\nexport fun test(ctx: magus\\Context, args: [str]) > void {\n}\n"

// A fork notes a check whose last real run took longer than a wired host lets a foreground
// call wait, naming both numbers, and says nothing when no wired host declares a limit.
func TestJobForkNotesACheckTooLongForTheForeground(t *testing.T) {
	// The spell's mgs_getLimits decode is the bindings' own test. Here the loader answers
	// for the wired id directly, since another test in this package may have cleared the
	// spell-backed one.
	fixture.WireHarness(t, agent.HarnessDescriptor{
		SchemaVersion: 2,
		ID:            "limit-host",
		Display:       agent.HarnessDisplay{Name: "Limit host"},
		Skills:        types.HarnessSkills{Form: types.SkillFormShort},
		Config:        types.HarnessConfig{Path: "hooks.json"},
		Limits:        types.Limits{ForegroundDefaultMs: 120000, ForegroundMaxMs: 600000},
	})
	for _, tt := range []struct {
		name, magusfile, want string
	}{
		{"a wired host with a limit", "import \"magus\";\nimport \"spells/limit-host\" as host;\nmagus\\harness.provider(host);\n" + testTarget,
			fmt.Sprintf("the check `%s` last ran 14m0s here, past the 10m0s a foreground call may wait", hint.Run.With("test", "."))},
		{"no wired host", "import \"magus\";\n" + testTarget, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resetWorkspaceMemo(t)
			root, _ := execFixture(t)
			require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(tt.magusfile), 0o644))
			require.NoError(t, os.MkdirAll(filepath.Join(root, "spells", "limit-host"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "spells", "limit-host", "spell.buzz"), []byte(limitHostSpell), 0o644))
			dir, err := sessions.Dir(root)
			require.NoError(t, err)
			w, err := sessions.Open(dir, "invmv00001", sessions.InvocationStart{Command: "run"})
			require.NoError(t, err)
			require.NoError(t, w.Append(sessions.KindTargetResult, sessions.TargetResult{
				Target: "test", Project: ".", Outcome: sessions.OutcomePass, DurationMs: (14 * time.Minute).Milliseconds(),
			}))

			out := captureStdout(t, func() {
				require.NoError(t, jobFork(t.Context(), root, []string{"long", "--check", "test .", "--write-paths", "magus.yaml"}))
			})
			if tt.want == "" {
				assert.NotContains(t, out, "foreground call may wait")
				return
			}
			assert.Contains(t, out, tt.want)
		})
	}
}

// A fork naming a model takes the tier of the harness row that names it: the row is what
// the wired harness publishes, and the host's own mapping of opus would give principal.
func TestJobForkTakesTheTierOfAWiredHarnessModelRow(t *testing.T) {
	fixture.WireHarness(t, agent.HarnessDescriptor{
		SchemaVersion: 2,
		ID:            "limit-host",
		Display:       agent.HarnessDisplay{Name: "Limit host"},
		Skills:        types.HarnessSkills{Form: types.SkillFormShort},
		Config:        types.HarnessConfig{Path: "hooks.json"},
		Models:        []types.ModelPricing{{ID: "claude-opus-test", Alias: "opus", Tier: "economy"}},
	})
	resetWorkspaceMemo(t)
	root, _ := execFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\nimport \"spells/limit-host\" as host;\nmagus\\harness.provider(host);\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "spells", "limit-host"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "spells", "limit-host", "spell.buzz"), []byte(limitHostSpell), 0o644))
	writeStdin(t, `{"schema_version": 12, "id": "wave/opus", "write_paths": ["a.go"], "model": "opus",`+
		` "goals": [{"id": "edited", "kind": "paths", "expect": "changed", "paths": ["a.go"]}]}`)

	out := captureStdout(t, func() {
		require.NoError(t, jobFork(t.Context(), root, []string{"--stdin"}))
	})
	assert.Contains(t, out, "model tier: economy (rank 0)")
}

// The tool rows a wired harness installs in a checkout are magus's, so ending its job
// removes the checkout; a harness that fails to load keeps it and names the failure,
// since its rows would otherwise read as work.
func TestEndWorktreesRestoresAWiredHarnessToolRows(t *testing.T) {
	harness := agent.HarnessDescriptor{
		SchemaVersion: 2,
		ID:            "limit-host",
		Display:       agent.HarnessDisplay{Name: "Limit host"},
		Skills:        types.HarnessSkills{Form: types.SkillFormShort},
		Config:        types.HarnessConfig{Path: "hooks.json"},
	}
	for _, tt := range []struct {
		name, toolsDir, want string
	}{
		{"a harness that loads", ".host-tools", "removed once job w ended"},
		{"a broken descriptor", "/host-tools", `kept after job w ended: restoring what magus wrote in it failed: loading wired harness limit-host for the tool rows it installs: invalid harness spell "limit-host": tools: dir must be a workspace-relative path`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wired := harness
			wired.Tools = &types.HarnessTools{Dir: tt.toolsDir}
			fixture.WireHarness(t, wired)
			resetWorkspaceMemo(t)
			t.Cleanup(snapshotGlobals())
			testkit.Isolate(t)
			global = globalFlags{}
			repo := testkit.NewRepo(t, map[string]string{
				"magus.yaml":                   "",
				"magusfile.buzz":               "import \"magus\";\nimport \"spells/limit-host\" as host;\nmagus\\harness.provider(host);\n" + testTarget,
				"spells/limit-host/spell.buzz": limitHostSpell,
				".host-tools/models":           "committed\n",
			})
			_, cacheDir := seedJobStore(t, repo.Root())
			store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: repo.Root()})
			wt, made, err := store.ForkWorktree(t.Context(), "w", filepath.Join(t.TempDir(), "w"), "", "")
			require.NoError(t, err)
			require.True(t, made)
			_, err = store.Update(t.Context(), "w", func(cur *types.Job) {
				*cur = types.Job{ID: "w", State: types.StatePass, WritePaths: []string{"a.go"}}
			})
			require.NoError(t, err)
			_, err = store.RecordWorktree(t.Context(), "w", wt)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(wt.Root, ".host-tools", "models"), []byte("reinstalled\n"), 0o644))

			var out bytes.Buffer
			require.NoError(t, endWorktrees(t.Context(), &out, store, "w"))
			assert.Contains(t, out.String(), tt.want)
		})
	}
}

// The store the CLI opens resolves a ref the worker recorded in its own checkout, so an
// orchestrator can exit a job on its worker's behalf.
func TestJobExitResolvesARefInTheJobsCheckout(t *testing.T) {
	worker := t.TempDir()
	recorded, err := cache.NewOutputStore(filepath.Join(worker, ".magus")).
		Persist(t.Context(), "key1", []byte("ok\n"), cache.OutputDescriptor{Project: ".", Target: "test", DurationMs: 1500, Revision: "abc123", Dirty: true})
	require.NoError(t, err)
	require.NotEmpty(t, recorded.Attempt)

	row := leaseRow("w", "")
	row.CheckoutRoot, row.Registered = worker, 1
	root, _ := execFixture(t, row)
	store, err := openJobs(t.Context(), root, jobOpen{})
	require.NoError(t, err)
	here := func(context.Context, string) (types.JobAttempt, error) { return types.JobAttempt{}, nil }
	result := types.JobResult{Job: row.ID, Validation: types.JobResultValidation{OutputRef: recorded.Ref}}

	stored, err := job.Exit(t.Context(), store, row.ID, &result, here)
	require.NoError(t, err)
	assert.Equal(t, &types.JobAttempt{
		Found: true, Ref: recorded.Ref, Project: ".", Target: "test",
		AttemptID: recorded.Attempt, DurationMs: 1500, Revision: "abc123", Dirty: true, ObservedGeneration: 1,
	}, stored.Attempt, "the attempt carries what freshness is judged by: the execution, its start and the tree it read")
}

// --evidence names a run the WAITER recorded. The holder's checkout is the primary one, the
// checkout `magus query output` falls back to, so a resolver that searched other checkouts
// would find the holder's own passing run there and certify it as the waiter's.
func TestJobWaitEvidenceResolvesOnlyInTheWaitersCheckout(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	holder := testkit.NewRepo(t, map[string]string{"magus.yaml": "", "a.go": "package a\n"})
	waiter := holder.AddWorktree("waiter")
	holderCache, err := magus.ResolveCacheDir(holder.Root())
	require.NoError(t, err)
	passed, err := cache.NewOutputStore(holderCache).
		Persist(t.Context(), "key1", []byte("ok\n"), cache.OutputDescriptor{Project: ".", Target: "go-test", TimestampMs: time.Now().UnixMilli()})
	require.NoError(t, err)

	found, err := storedAttempts(holder.Root())(t.Context(), passed.Ref)
	require.NoError(t, err)
	require.True(t, found.Found, "the control: the holder's checkout holds the run")
	missed, err := storedAttempts(waiter.Root())(t.Context(), passed.Ref)
	require.NoError(t, err)
	assert.False(t, missed.Found, "the waiter's resolver reads its own store alone")

	row := types.Job{
		ID: "w", Created: 1, State: types.StateExited, WritePaths: []string{"a.go"}, CheckoutRoot: holder.Root(),
		Check:   &types.LeaseCheck{Target: "go-test", Project: "."},
		Result:  &types.JobResult{Job: "w", ChangedPaths: []string{"a.go"}, Validation: types.JobResultValidation{OutputRef: "failed"}},
		Attempt: &types.JobAttempt{Found: true, Ref: "failed", Project: ".", Target: "go-test", Failed: true, TimestampMs: 2_000},
	}
	seedJobStore(t, waiter.Root(), row)
	store, err := openJobs(t.Context(), waiter.Root(), jobOpen{})
	require.NoError(t, err)
	observe := func(context.Context, types.Job) (job.Observed, error) {
		return job.Observed{Changed: []string{"a.go"}, ChangedKnown: true}, nil
	}

	_, err = job.Wait(t.Context(), store, row.ID, nil, storedAttempts(waiter.Root()), observe, map[string]string{types.PrimaryGoalID: passed.Ref})

	require.ErrorContains(t, err, "names no run this checkout recorded")
	rows, err := store.List()
	require.NoError(t, err)
	assert.Equal(t, types.StateExited, rows[0].State)
}

// Prune prints each job it ends with the reason, then the count; under --dry-run it says
// what would end and leaves the row live.
func TestJobPrunePrintsEachRowAndTheCount(t *testing.T) {
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
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
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
	row := leaseRow("w", "")
	row.State, row.WritePaths, row.Check = types.StateRunning, []string{"a.go", "b.go"}, &types.LeaseCheck{Target: "test", Project: "."}
	row.Validation = row.Check.String()
	root, cacheDir := execFixture(t, row)
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
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

func TestSpecChangeNamesWhatBreaksGainedAndLost(t *testing.T) {
	t.Parallel()

	prev := types.Job{Breaks: []string{"legacy_key", "api.Open"}}
	next := types.Job{Breaks: []string{"api.Open", "ledger.json"}}
	assert.Equal(t, "breaks +ledger.json -legacy_key", specChange("breaks", prev, next))
}

// describe job reads the graph as built and job wait may build it. On a current index the
// two grade a row alike; on a stale one describe builds nothing and leaves the symbol goal
// ungraded, naming the rebuild.
func TestDescribeAndWaitGradeSymbolsAlike(t *testing.T) {
	t.Parallel()

	g := deltaGraph(map[string]string{"Present": "code.go:3"})
	row := types.Job{ID: "wave/sym", WritePaths: []string{"code.go"}, Goals: []types.Goal{
		{ID: "here", Kind: types.GoalKindSymbol, Expect: types.ExpectPresent, Symbols: []string{"Present"}},
		{ID: "gone", Kind: types.GoalKindSymbol, Expect: types.ExpectAbsent, Symbols: []string{"Removed"}},
	}}
	read := func(rebuild bool, stale []types.KnowledgeStaleIndex) *jobGraph {
		return &jobGraph{
			rebuild: rebuild,
			stale:   func(context.Context) []types.KnowledgeStaleIndex { return stale },
			build: func(context.Context) (*knowledge.Graph, error) {
				if !rebuild && len(stale) > 0 {
					t.Error("describe built the graph over a stale index")
				}
				return g, nil
			},
		}
	}
	grade := func(graph *jobGraph) types.JobStatus {
		seen, err := jobObserver("", func(string) *jobGraph { return graph })(t.Context(), row)
		require.NoError(t, err)
		return job.VerifyGates(row, types.JobResult{Job: row.ID}, types.JobAttempt{}, nil, []types.Job{row}, seen)
	}

	described, waited := grade(read(false, nil)), grade(read(true, nil))
	assert.Equal(t, waited, described, "on a current index describe and wait agree")
	require.Len(t, described.Gates, 2)
	for _, gate := range described.Gates {
		assert.True(t, gate.Verified, "%+v", gate)
	}

	stale := []types.KnowledgeStaleIndex{{Project: "pkg", Language: "go"}}
	described = grade(read(false, stale))
	require.Len(t, described.Gates, 2)
	for _, gate := range described.Gates {
		assert.False(t, gate.Verified, gate.ID)
		assert.Contains(t, strings.Join(gate.Violations, " "), hint.GraphBuild.String(), gate.ID)
	}
	assert.Equal(t, waited, grade(read(true, stale)), "wait builds whatever the index says")
}

func TestJobObserverGradesSymbolsInTheJobsOwnTree(t *testing.T) {
	t.Parallel()

	root, checkout := t.TempDir(), t.TempDir()
	row := types.Job{ID: "wave/sym", CheckoutRoot: checkout, Goals: []types.Goal{
		{ID: "here", Kind: types.GoalKindSymbol, Expect: types.ExpectPresent, Symbols: []string{"Present"}},
	}}
	var trees []string
	graphIn := func(tree string) *jobGraph {
		trees = append(trees, tree)
		return &jobGraph{
			rebuild: true,
			stale:   func(context.Context) []types.KnowledgeStaleIndex { return nil },
			build: func(context.Context) (*knowledge.Graph, error) {
				return deltaGraph(map[string]string{"Present": "code.go:3"}), nil
			},
		}
	}

	seen, err := jobObserver(root, graphIn)(t.Context(), row)

	require.NoError(t, err)
	assert.Equal(t, []string{checkout}, trees, "the worker's checkout holds the symbols its branch defines, not the waiter's")
	assert.True(t, seen.SymbolsKnown)
}

// describe reads the graph evidence as it reads the symbol goals: from the graph as built,
// and from none while a symbol index is stale, which it reports cold.
func TestLeaseGraphEvidenceBuildsNothingOverAStaleIndex(t *testing.T) {
	t.Parallel()

	paths := []string{"code.go"}
	evidence := func(rebuild bool, stale []types.KnowledgeStaleIndex) (cold bool, loads int) {
		graph := &jobGraph{
			rebuild: rebuild,
			stale:   func(context.Context) []types.KnowledgeStaleIndex { return stale },
			domain: func(context.Context) (*knowledge.Graph, error) {
				loads++
				if !rebuild && len(stale) > 0 {
					t.Error("describe built the graph over a stale index")
				}
				return deltaGraph(nil), nil
			},
		}
		_, cold = leaseGraphEvidence(t.Context(), graph, paths)
		_, again := leaseGraphEvidence(t.Context(), graph, paths)
		assert.Equal(t, cold, again, "a second ask answers alike")
		return cold, loads
	}

	cold, loads := evidence(false, nil)
	assert.False(t, cold)
	assert.Equal(t, 1, loads, "one load serves every ask")

	stale := []types.KnowledgeStaleIndex{{Project: "pkg", Language: "go"}}
	cold, loads = evidence(false, stale)
	assert.True(t, cold, "a stale index is reported, not read as nothing depending on the paths")
	assert.Zero(t, loads)

	cold, loads = evidence(true, stale)
	assert.False(t, cold)
	assert.Equal(t, 1, loads)
}

func TestDeletionCandidatesAskNothingOfARowThatWritesNothing(t *testing.T) {
	t.Parallel()

	load := func(context.Context) *knowledge.Graph {
		t.Fatal("a row that writes nothing loaded the graph")
		return nil
	}
	assert.Nil(t, deletionCandidates(t.Context(), load, types.Job{ReadOnly: true, WritePaths: []string{"a.go"}}))
	assert.Nil(t, deletionCandidates(t.Context(), load, types.Job{}))
}

func TestForkOutputCarriesReferencedOutsideBesideTheRow(t *testing.T) {
	t.Parallel()

	out := forkOutput{
		Job:               types.Job{ID: "fork-names-refs-outside"},
		ReferencedOutside: []job.ReferencedFile{{File: "types/conformance_test.go", Count: 9, Uses: []string{"types/conformance.go"}}},
	}
	raw, err := json.Marshal(forkReport{forkOutput: out})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "fork-names-refs-outside", got["id"], "the row flattens")
	assert.Equal(t, []any{map[string]any{"file": "types/conformance_test.go", "count": float64(9), "uses": []any{"types/conformance.go"}}},
		got["referenced_outside"])
}

// A workspace with no symbol index cannot say what references the write paths, so a fork
// says the list is unknown instead of printing nothing.
func TestJobForkSaysReferencedOutsideIsUnknownWithoutASymbolIndex(t *testing.T) {
	resetWorkspaceMemo(t)
	root, _ := execFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\n"+testTarget), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644))

	out := captureStdout(t, func() {
		require.NoError(t, jobFork(t.Context(), root, []string{"unindexed", "--check", "test .", "--write-paths", "a.go"}))
	})
	assert.Contains(t, out, fmt.Sprintf("\n  unknown, no symbol index answered; `%s` builds one\n", hint.GraphBuild.String()))

	prev := global.output
	t.Cleanup(func() { global.output = prev })
	global.output = "json"
	raw := captureStdout(t, func() {
		require.NoError(t, jobFork(t.Context(), root, []string{"unindexed-json", "--check", "test .", "--write-paths", "a.go"}))
	})
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	assert.Equal(t, job.NoSymbolIndex(), got["referenced_outside_gap"], "-o json carries the gap beside the empty list")
}

// bindCheckout records id as the checkout's binding, as the guard does when a caller whose
// host names no session takes a job there.
func bindCheckout(t *testing.T, cacheDir, id string) {
	t.Helper()
	require.NoError(t, job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir}).Bind(job.Caller{}, id))
}

// TestJobExecWritesNoMarker pins the half of the 2026-09-26 fix that lives in the CLI: exec
// records the base and the checkout, and binds nobody. A CLI process cannot tell a subagent
// from its parent, so a binding it wrote was the checkout's, and the parent sharing that
// checkout was graded as the subagent from its next call.
func TestJobExecWritesNoMarker(t *testing.T) {
	row := leaseRow("lease-enforcement/wave4/docs", "")
	row.State = types.StateRunning
	repo, cacheDir := execRepoFixture(t, row)
	root := repo.Root()

	out := captureStdout(t, func() {
		require.NoError(t, jobExec(t.Context(), root, []string{row.ID}))
	})
	assert.Contains(t, out, "took "+row.ID)

	loc := job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}
	_, err := os.Stat(loc.MarkerPath())
	assert.True(t, os.IsNotExist(err), "exec writes no checkout record")
	lease, from := job.ActingLease(loc, "")
	assert.Equal(t, []any{"", types.LeaseSource("")}, []any{lease, from}, "the checkout acts under nothing")

	store := job.NewStore(loc)
	rows, err := store.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	abs, err := filepath.Abs(root)
	require.NoError(t, err)
	base, err := store.CheckoutToken(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []any{base, abs}, []any{rows[0].ReportedBase, rows[0].CheckoutRoot}, "the base read from the tree and the checkout are recorded")
	assert.True(t, strings.HasPrefix(base, repo.Head()), "the base is the checkout's own revision")
}

// Exec takes exactly one job: the read form it had printed a binding the CLI no longer
// holds, and a job that already ended has nothing left to take.
func TestJobPauseRefusesExecUntilResume(t *testing.T) {
	row := leaseRow("lease-enforcement/wave4/paused", "")
	row.State = types.StateDeclared
	repo, cacheDir := execRepoFixture(t, row)
	root := repo.Root()

	captureStdout(t, func() {
		require.NoError(t, jobPause(t.Context(), root, "pause", []string{"--reason", "release build", "--until", "1h", row.ID}))
	})
	err := jobExec(t.Context(), root, []string{row.ID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), row.ID+" is paused by ")
	assert.Contains(t, err.Error(), ": release build (until ")
	assert.Contains(t, err.Error(), "job resume "+row.ID)
	bindCheckout(t, cacheDir, row.ID)
	err = jobExec(t.Context(), root, []string{"--timeout", "1m", "--", "true"})
	require.Error(t, err, "a command under the paused lease is refused too")
	assert.Contains(t, err.Error(), row.ID+" is paused")

	assert.Error(t, jobPause(t.Context(), root, "resume", []string{row.ID}), "the holder does not lift its own pause")

	orchestrator := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root, Actor: &job.Actor{}})
	_, err = orchestrator.Resume(t.Context(), row.ID)
	require.NoError(t, err)
	captureStdout(t, func() {
		require.NoError(t, jobExec(t.Context(), root, []string{row.ID}))
	})
}

func TestJobExecRefusesAnythingButOneLiveJob(t *testing.T) {
	done := leaseRow("lease-enforcement/wave4/done", "")
	done.State = types.StatePass
	repo, _ := execRepoFixture(t, done)
	root := repo.Root()

	for name, args := range map[string][]string{
		"no job":        nil,
		"two jobs":      {"a", "b"},
		"an unknown id": {"harness/no-such-job"},
		"an ended job":  {done.ID},
	} {
		assert.Error(t, jobExec(t.Context(), root, args), name)
	}
}

// The base is read from the tree, so there is no flag to type one: --base is an unknown
// flag, refused before the row is touched.
func TestJobExecRefusesATypedBase(t *testing.T) {
	row := leaseRow("lease-enforcement/wave4/docs", "")
	row.State = types.StateDeclared
	repo, cacheDir := execRepoFixture(t, row)

	err := jobExec(t.Context(), repo.Root(), []string{row.ID, "--base", repo.Head()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "-base")

	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: repo.Root()}).List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, []any{types.StateDeclared, ""}, []any{rows[0].State, rows[0].ReportedBase}, "a refused exec takes nothing")
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

	assert.Equal(t, fmt.Sprintf(`%sneeds me (2)
%squeued, in plan order (5)
%s%sothers: 1 queued
%s`, head, kicked, queued, tail, refetch), render(false))
	assert.Equal(t, fmt.Sprintf(`%sneeds the author (2)
%squeued, in plan order (6)
%s  #457  their change                                        pos 6  job their-work
%s%s`, head, kicked, queued, tail, refetch), render(true))
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
	root, _ := execFixture(t, leaseRow("fleet", ""))
	withOutput(t, "")
	out := captureStdout(t, func() {
		require.NoError(t, lsJobs(root, nil))
	})
	assert.Contains(t, out, "in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes\n")
}

// Read end to end from a snapshot: a caller no job or login names sees everyone's.
func TestLsJobsReadsTheSnapshot(t *testing.T) {
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

// Ended rows pile up, so the board lists the live ones and counts the rest; --all is the
// way to ask for every row.
func TestLsJobsListsLiveRowsAndLeavesEndedOnesToAll(t *testing.T) {
	root, _ := execFixture(t,
		leaseRow("fleet", ""),
		types.Job{ID: "done", State: types.StatePass},
		types.Job{ID: "lost", State: types.StateNoReturn},
		types.Job{ID: "handed-in", State: types.StateExited},
	)
	withOutput(t, "name")
	useTextLogger(t)
	ls := func(args ...string) (ids, notice string) {
		notice = captureStderr(t, func() {
			ids = captureStdout(t, func() { require.NoError(t, lsJobs(root, args)) })
		})
		return ids, notice
	}

	ids, notice := ls()
	assert.ElementsMatch(t, []string{"fleet", "handed-in"}, strings.Fields(ids), "an exited row still holds its lease, so it is live")
	assert.Contains(t, notice, "2 ended jobs left out; `magus ls jobs --all` lists them")

	ids, notice = ls("--all")
	assert.ElementsMatch(t, []string{"fleet", "handed-in", "done", "lost"}, strings.Fields(ids))
	assert.NotContains(t, notice, "ended", "nothing was left out")
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
		assert.True(t, strings.HasPrefix(line, fmt.Sprintf("magus %s ", v.name)), "`magus %s -h` example runs something else: %q", v.name, line)
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
				return fmt.Sprintf(`%s <<'EOF'
%sEOF
%s`, command, body, golden)
			}
			command += " < " + in
		}
	}
	return fmt.Sprintf("%s\n%s", command, golden)
}

// A -run that leaves out a test of the write paths' package is refused at fork, listing
// the tests and a pattern that covers them, and a check running the whole package passes.
func TestJobForkRefusesACheckThatSkipsATestOfItsPackage(t *testing.T) {
	resetWorkspaceMemo(t)
	root, cacheDir := execFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("package pkg\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pkg", "a_test.go"),
		[]byte("package pkg\n\nimport \"testing\"\n\nfunc TestAlpha(t *testing.T) {}\nfunc TestBeta(t *testing.T) {}\n"), 0o644))
	fork := func(check string) error {
		return jobFork(t.Context(), root, []string{"w", "--write-paths", "pkg/a.go", "--check", check})
	}

	err := fork("go-test . -- -run Alpha")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pkg (TestBeta)")
	assert.Contains(t, err.Error(), "`Alpha|^(TestBeta)$`")

	require.NoError(t, fork("go-test . -- -run Alpha|^(TestBeta)$"))
	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).List()
	require.NoError(t, err)
	require.Len(t, rows, 1, "the refused fork wrote no row")
}

// goModule makes a fixture repository a Go module, which the go spell the deny-rule
// magusfiles bind needs for the checks they name to exist.
const goModule = "module example.com/w\n\ngo 1.25\n"

// denyWorkerBuildRule is a magusfile whose command rule denies a worker the go-build target.
const denyWorkerBuildRule = `import "magus";
import "magus/spell/go";

magus\project({"spells": [go]});

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    foreach (c in req.commands) {
        if (req.role == "worker" and c.args.indexOf("go-build") != null) {
            return magus\guard.deny("a worker does not build magus");
        }
    }
    return magus\guard.allow();
});
`

// The fork judges the check as its not-yet-stored worker would meet the guard: the rule
// that turns on the worker role denies it, the same line from the orchestrator would pass,
// and the refusal names the target that compiles without linking the binary.
func TestJobForkRefusesACheckTheGuardDeniesItsWorker(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := testkit.NewRepo(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyWorkerBuildRule, "go.mod": goModule, "run.go": "package run\n"}).Root()
	resetWorkspaceMemo(t)
	t.Chdir(root)
	installCheckGrader(t, newCheckGrader())
	fork := func(check string) error {
		return jobFork(t.Context(), root, []string{"w", "--write-paths", "run.go", "--check", check})
	}

	err := fork("go-build .")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "w's check `magus run go-build .` would be denied to its worker by the guard")
	assert.Contains(t, err.Error(), "a worker does not build magus")
	assert.Contains(t, err.Error(), "`go::go-vet .`")

	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	rows, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, rows, "grading dry-runs the guard and the refused fork wrote no row")

	require.NoError(t, fork("go::go-vet ."))
	rows, err = store.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

// denyBuildFromElsewhereRule is a magusfile whose command rule denies a worker the go-build
// target only when the call runs in a directory named "elsewhere": a stand-in for any answer
// that depends on where the guard is asked from.
const denyBuildFromElsewhereRule = `import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    foreach (c in req.commands) {
        if (req.role == "worker" and c.args.indexOf("go-build") != null and req.dir.indexOf("elsewhere") != null) {
            return magus\guard.deny("judged from the wrong directory");
        }
    }
    return magus\guard.allow();
});
`

// The grade follows the workspace the store was opened for, not the process's working
// directory: a daemon serving magus\job.put runs from somewhere else, and a command graded
// there would be judged for the wrong checkout. The rule answers differently from the
// "elsewhere" directory, so a grade that took the process cwd for the call's directory
// would deny the check.
func TestGradeCheckJudgesFromTheWorkspaceNotTheWorkingDirectory(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := testkit.NewRepo(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyBuildFromElsewhereRule, "run.go": "package run\n"}).Root()
	resetWorkspaceMemo(t)
	row := types.Job{ID: "w", WritePaths: []string{"run.go"}, Check: &types.LeaseCheck{Target: "go-build", Project: "."}}
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.MkdirAll(elsewhere, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(elsewhere, "magus.yaml"), []byte("{}\n"), 0o644))

	// The control: asked without a Dir, from elsewhere, the same request is denied. This is
	// the grade gradeCheck would reach with no Dir, so the cases below fail without it.
	t.Run("the rule answers differently from elsewhere", func(t *testing.T) {
		t.Chdir(elsewhere)
		unplaced := guard.Judge(t.Context(), guardDependencies(t.Context(), root), guard.Request{
			Input:      ownBinaryPrefix + row.Check.String(),
			DryRun:     true,
			Lease:      row.ID,
			PendingRow: &row,
		})
		assert.Equal(t, string(types.GuardDeny), unplaced.Decision)
		assert.Contains(t, unplaced.Reason, "judged from the wrong directory")
	})

	for name, cwd := range map[string]string{"the root": root, "elsewhere": elsewhere} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(cwd)
			verdict := gradeCheck(t.Context(), guardDependencies(t.Context(), root), root, row)
			assert.Empty(t, verdict.NotGraded)
			assert.False(t, verdict.Denied, "the check is judged from the workspace, wherever the process runs: %s", verdict.Reason)
		})
	}
}

// denyBuildRule is a magusfile whose command rule denies a worker the go-build target
// wherever the call runs.
const denyBuildRule = `import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    foreach (c in req.commands) {
        if (req.role == "worker" and c.args.indexOf("go-build") != null) {
            return magus\guard.deny("builds are refused here");
        }
    }
    return magus\guard.allow();
});
`

// A grader outlives one command in a daemon serving magus\job.put, so a rule edited after
// its first grade must apply to the next. The edit tightens the rules: an edit that only
// relaxed them would be held back by the approved sources and prove nothing.
func TestCheckGraderReadsAnEditedRuleAgain(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := testkit.NewRepo(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyBuildFromElsewhereRule, "run.go": "package run\n"}).Root()
	resetWorkspaceMemo(t)
	t.Chdir(root)
	row := types.Job{ID: "w", WritePaths: []string{"run.go"}, Check: &types.LeaseCheck{Target: "go-build", Project: "."}}
	grade := newCheckGrader()

	before := grade(t.Context(), root, row)
	require.Empty(t, before.NotGraded)
	require.False(t, before.Denied, "the rule allows the check from the root: %s", before.Reason)

	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(denyBuildRule), 0o644))

	after := grade(t.Context(), root, row)
	assert.Empty(t, after.NotGraded)
	assert.True(t, after.Denied, "the edited rule denies the check")
	assert.Contains(t, after.Reason, "builds are refused here")
}

// A workspace whose rules did not load defers the grade, and says so rather than refuse on a
// verdict nobody reached.
func TestGradeCheckSkipsAWorkspaceThatDidNotLoad(t *testing.T) {
	testkit.Isolate(t)
	row := types.Job{ID: "w", Check: &types.LeaseCheck{Target: "go-build", Project: "."}}

	verdict := gradeCheck(t.Context(), guard.Dependencies{LoadFailure: errors.New("magusfile.buzz:3: expected ';'")}, t.TempDir(), row)

	assert.Equal(t, job.CheckVerdict{Deferred: "the workspace's rules did not load: magusfile.buzz:3: expected ';'"}, verdict)
}

// One judge: the check a fork grades is the check `describe job` briefs and the guard lets
// the holder run. A row forked with --checkout gates in that checkout, so its `ci` check is
// accepted by all three; the same row with no checkout of its own is refused by all three.
func TestGradeCheckJudgesInTheJobsOwnCheckout(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	repo := testkit.NewRepo(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": "", "go.mod": goModule, "run.go": "package run\n"})
	root := repo.Root()
	own := filepath.Join(t.TempDir(), "own")
	repo.Git("worktree", "add", "-q", "--detach", own)
	resetWorkspaceMemo(t)
	t.Chdir(root)
	ci := &types.LeaseCheck{Target: types.TargetCI, Project: "."}
	held := types.Job{ID: "w", State: types.StateDeclared, WritePaths: []string{"run.go"}, Check: ci, Worktree: &types.JobWorktree{Root: own}}
	shared := held
	shared.Worktree = nil
	deps := guardDependencies(t.Context(), root)

	t.Run("its own checkout", func(t *testing.T) {
		verdict := gradeCheck(t.Context(), deps, root, held)
		assert.Empty(t, verdict.NotGraded)
		assert.False(t, verdict.Denied, "the holder gates in the checkout it holds: %s", verdict.Reason)
		require.NoError(t, jobRefusesTheGate(t.Context(), root, held), "describe briefs what the fork accepted")

		taken := held
		taken.State, taken.CheckoutRoot = types.StateRunning, own
		ran := guard.Judge(t.Context(), deps, guard.Request{Input: ownBinaryPrefix + ci.String(), DryRun: true, Lease: taken.ID, PendingRow: &taken, Dir: own})
		assert.NotEqual(t, string(types.GuardDeny), ran.Decision, "the guard lets the holder run it there: %s", ran.Reason)
	})

	t.Run("the forker's checkout", func(t *testing.T) {
		verdict := gradeCheck(t.Context(), deps, root, shared)
		assert.True(t, verdict.Denied, "a job's check is never the gate in a shared checkout")
		require.Error(t, jobRefusesTheGate(t.Context(), root, shared))
	})
}

// denyEveryWorkerRule is a magusfile whose command rule denies a worker every command, the
// way the stale-binary rule does: it describes the binary, not the check.
const denyEveryWorkerRule = `import "magus";
import "magus/spell/go";

magus\project({"spells": [go]});

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    if (req.role == "worker") {
        return magus\guard.deny("the hook binary is older than the tree\nrebuild it");
    }
    return magus\guard.allow();
});
`

// A deny that holds for the compiling check too is about the binary or the workspace, so
// it is no verdict on the check: the fork is refused as ungraded, never as a denied check,
// and never accepted.
func TestJobForkRefusesAsUngradedADenyThatHoldsForEveryCommand(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := testkit.NewRepo(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyEveryWorkerRule, "go.mod": goModule, "run.go": "package run\n"}).Root()
	resetWorkspaceMemo(t)
	t.Chdir(root)
	row := types.Job{ID: "w", WritePaths: []string{"run.go"}, Check: &types.LeaseCheck{Target: "go-build", Project: "."}}

	verdict := gradeCheck(t.Context(), guardDependencies(t.Context(), root), root, row)

	assert.False(t, verdict.Denied)
	assert.Contains(t, verdict.NotGraded, "denies this worker `magus run go::go-vet .` as well")
	assert.Contains(t, verdict.NotGraded, "the hook binary is older than the tree", "the first line of the verdict is quoted")
	assert.NotContains(t, verdict.NotGraded, "rebuild it")

	installCheckGrader(t, newCheckGrader())
	err := jobFork(t.Context(), root, []string{"w", "--write-paths", "run.go", "--check", "go-build ."})
	require.ErrorIs(t, err, types.JobUngraded)
	assert.NotErrorIs(t, err, types.JobCheckUnrunnable)
	assert.Equal(t, 1, strings.Count(err.Error(), string(types.JobUngraded)), "reported once: %s", err)
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	rows, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).List()
	require.NoError(t, err)
	assert.Empty(t, rows, "a fork nobody graded writes nothing")
}

// installCheckGrader installs g as runCLI installs newCheckGrader, and puts back what was
// installed. The grader is process-wide, so a test that uses it does not run in parallel.
func installCheckGrader(t *testing.T, g job.CheckGrader) {
	t.Helper()
	previous := job.SetCheckGrader(g)
	t.Cleanup(func() { job.SetCheckGrader(previous) })
}

// One grader judges every fork: runCLI installs it, and the Store `magus job fork` opens
// names none of its own, as the Stores magus\job.put opens over MCP and in the daemon do
// (std's TestMagusPutJobGradesTheCheckLikeFork).
func TestJobForkGradesWithTheInstalledGrader(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := testkit.NewRepo(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyWorkerBuildRule, "go.mod": goModule, "run.go": "package run\n"}).Root()
	resetWorkspaceMemo(t)
	t.Chdir(root)
	var graded []string
	installCheckGrader(t, func(_ context.Context, at string, _ types.Job) job.CheckVerdict {
		graded = append(graded, at)
		return job.CheckVerdict{}
	})

	require.NoError(t, jobFork(t.Context(), root, []string{"w", "--write-paths", "run.go", "--check", "go-build ."}))
	assert.Equal(t, []string{root}, graded)
}

func TestEvidenceFlagCollectsGoalRefPairs(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		values []string
		want   evidenceFlag
		err    string
	}{
		"one pair":       {values: []string{"check=abc123"}, want: evidenceFlag{"check": "abc123"}},
		"repeated":       {values: []string{"check=a", "docs=b"}, want: evidenceFlag{"check": "a", "docs": "b"}},
		"no separator":   {values: []string{"check"}, err: `want <goal>=<ref>, got "check"`},
		"no ref":         {values: []string{"check="}, err: `want <goal>=<ref>, got "check="`},
		"no goal":        {values: []string{"=abc"}, err: `want <goal>=<ref>, got "=abc"`},
		"a goal's twice": {values: []string{"check=a", "check=b"}, err: `goal "check" is named twice`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := evidenceFlag{}
			var err error
			for _, v := range tc.values {
				if err = got.Set(v); err != nil {
					break
				}
			}
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// The MODEL column names the model the row asks for and falls back to the tier the store
// assigned, so a row that names nothing still shows what it runs at.
func TestJobModelCellFallsBackToTheAssignedTier(t *testing.T) {
	t.Parallel()

	tier := &types.JobModelTier{Name: "economy"}
	tests := []struct {
		name string
		row  types.Job
		want string
	}{
		{"the model the row names wins", types.Job{Model: "opus", ModelTier: tier}, "opus"},
		{"the assigned tier stands in", types.Job{ModelTier: tier}, "economy"},
		{"neither is a dash", types.Job{}, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, jobModelCell(tt.row))
		})
	}
}

// dryRunFork runs a fork under the global --dry-run in a workspace whose go spell gives the
// checks they name something to resolve against.
func dryRunFork(t *testing.T, format string, args ...string) (stdout string, store *job.Store, err error) {
	t.Helper()
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	if format != "" {
		args = append([]string{"-o", format}, args...)
	}
	root := testkit.NewRepo(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyWorkerBuildRule, "go.mod": goModule, "run.go": "package run\n"}).Root()
	resetWorkspaceMemo(t)
	t.Chdir(root)
	installCheckGrader(t, newCheckGrader())
	globalCfg.DryRun = true
	t.Cleanup(func() { globalCfg.DryRun = false })
	cacheDir, cacheErr := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, cacheErr)
	store = job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	stdout = captureStdout(t, func() { err = jobFork(t.Context(), root, args) })
	return stdout, store, err
}

func TestJobForkDryRunListsEveryProblemAndWritesNothing(t *testing.T) {
	out, store, err := dryRunFork(t, "", "w", "--write-paths", "run.go", "--check", "nosuch .")

	require.Error(t, err)
	assert.Equal(t, exit.Failure, exit.Code(err))
	assert.True(t, exit.AlreadyReported(err), "the list is printed, not repeated as an error")
	assert.Contains(t, out, "1 problem:")
	assert.Contains(t, out, "[MGS3040]")
	assert.Contains(t, out, "     fix: name one of its targets")
	assert.Contains(t, out, "would store:")
	assert.Contains(t, out, "dry run: nothing written")
	rows, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestJobForkDryRunOfACleanRecordPrintsItAndExitsZero(t *testing.T) {
	out, store, err := dryRunFork(t, "", "w", "--write-paths", "run.go", "--check", "go::go-vet .")

	require.NoError(t, err)
	assert.Contains(t, out, "no problems\nwould store:\n")
	assert.Contains(t, out, `"id": "w"`)
	rows, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestJobForkDryRunEmitsTheValidationForAStructuredFormat(t *testing.T) {
	out, _, err := dryRunFork(t, "json", "w", "--write-paths", "run.go", "--check", "nosuch .")

	require.Error(t, err)
	var v types.JobValidation
	require.NoError(t, json.Unmarshal([]byte(out), &v), out)
	assert.Equal(t, "w", v.Job.ID)
	require.Len(t, v.Problems, 1)
	assert.Equal(t, types.JobCheckUnrunnable, v.Problems[0].Code)
}

// A fork made at a commit the forker's ./magus was not built at places no binary and names
// the one build its checkout needs.
func TestPrintJobWorktreeNamesTheCheckoutBootstrap(t *testing.T) {
	row := types.Job{ID: "w", Worktree: &types.JobWorktree{Root: "/w/c", Base: "0123456789abcdef"}}
	var out bytes.Buffer
	printJobWorktree(&out, row, job.CheckoutBootstrap("/w/c"))
	assert.Contains(t, out.String(), "placed no magus: the forking checkout's was built at another commit")
	assert.Contains(t, out.String(), "next: `./magus buzz hack/dev/bootstrap-checkout.buzz -- --checkout /w/c` builds one at 0123456789ab")

	out.Reset()
	printJobWorktree(&out, row, "")
	assert.Contains(t, out.String(), "runs magus from PATH", "a forker on PATH needs no build")
}

func TestParsePauseUntilReadsADurationOrAnInstant(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := map[string]int64{
		"":                     0,
		"2h":                   now.Add(2 * time.Hour).Unix(),
		"2027-01-15T09:00:00Z": 1_800_003_600,
	}
	for raw, want := range cases {
		got, err := types.ParsePauseUntil(raw, now)
		require.NoError(t, err, raw)
		assert.Equal(t, want, got, raw)
	}
	for _, raw := range []string{"-2h", "tomorrow"} {
		_, err := types.ParsePauseUntil(raw, now)
		assert.Error(t, err, raw)
	}
}
