package job

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeclareStampsTheDeadlineFromTheTimeout(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	within := func(t *testing.T, got, want int64) {
		t.Helper()
		assert.InDelta(t, want, got, 5, "deadline %d, want about %d", got, want)
	}

	var timed types.Job
	Declare(types.Declaration{ID: "a", Timeout: "30m"}, time.Hour)(&timed)
	within(t, timed.Deadline, now+1800)

	var defaulted types.Job
	Declare(types.Declaration{ID: "a"}, time.Hour)(&defaulted)
	within(t, defaulted.Deadline, now+3600)

	unbound := types.Job{Deadline: now + 60}
	Declare(types.Declaration{ID: "a"}, 0)(&unbound)
	assert.Zero(t, unbound.Deadline, "a re-declaration with no timeout and no default carries no bound")
}

func TestMergeTimeoutStampsAndClearsTheDeadline(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	got := applyMerge(t, map[string]any{"timeout": "2h"}, types.Job{ID: "u1"})
	assert.InDelta(t, now+7200, got.Deadline, 5)

	kept := applyMerge(t, map[string]any{"state": "running"}, types.Job{ID: "u1", Deadline: 42})
	assert.Equal(t, int64(42), kept.Deadline, "a put naming no timeout keeps the bound")

	cleared := applyMerge(t, map[string]any{"timeout": ""}, types.Job{ID: "u1", Deadline: 42})
	assert.Zero(t, cleared.Deadline)

	_, err := ParseMerge(map[string]any{"timeout": "soon"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")

	_, err = ParseMerge(map[string]any{"deadline": 5})
	require.Error(t, err, "the instant is the store's to stamp")
}

func TestJobListFlagsOverdueOrphansAndStaleJobs(t *testing.T) {
	t.Parallel()

	const now = 10_000
	rows := []types.Job{
		{ID: "root", State: types.StatePass, Updated: now},
		{ID: "root/orphan", Parent: "root", State: types.StateRunning, Updated: now},
		{ID: "live", State: types.StateRunning, Updated: now},
		{ID: "live/overdue", Parent: "live", State: types.StateRunning, Deadline: now - 1, Updated: now},
		{ID: "live/stale", Parent: "live", State: types.StateDeclared, Updated: now - 3600},
		{ID: "done", State: types.StateFail, Deadline: 1, Updated: 1},
	}

	got := types.NewJobList(rows).Flag(now, 30*time.Minute)
	assert.Equal(t, []string{"live/overdue"}, got.Overdue)
	assert.Equal(t, []string{"root/orphan"}, got.Orphans)
	assert.Equal(t, []string{"live/stale"}, got.Stale)

	unset := types.NewJobList(rows).Flag(now, 0)
	assert.Empty(t, unset.Stale, "an unset stale_after flags nothing")
}

func TestRefuseForkLimitsNamesTheKey(t *testing.T) {
	t.Parallel()

	rows := []types.Job{
		{ID: "root", State: types.StateRunning},
		{ID: "root/a", Parent: "root", State: types.StateRunning},
		{ID: "root/b", Parent: "root", State: types.StatePass},
		{ID: "other", State: types.StateRunning},
	}

	err := RefuseForkLimits(rows, "root/a/deep", "root/a", config.Jobs{MaxDepth: 1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jobs.max_depth")
	assert.Contains(t, err.Error(), "root/a/deep")
	require.NoError(t, RefuseForkLimits(rows, "root/c", "root", config.Jobs{MaxDepth: 1}))

	err = RefuseForkLimits(rows, "root/c", "root", config.Jobs{MaxLive: 2})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jobs.max_live")
	require.NoError(t, RefuseForkLimits(rows, "root/c", "root", config.Jobs{MaxLive: 3}),
		"a finished job and another root's jobs do not count")
	require.NoError(t, RefuseForkLimits(rows, "root/a", "root", config.Jobs{MaxLive: 2}),
		"re-declaring a live job is not a new one")

	require.NoError(t, RefuseForkLimits(rows, "root/a/deep/deeper", "root/a/deep", config.Jobs{}), "unset is unlimited")
}

func TestRefuseAmbiguousSymbolsNamesEveryDefinition(t *testing.T) {
	t.Parallel()

	read := func(_ context.Context, name string) (SymbolFact, bool) {
		switch name {
		case "Validate":
			return SymbolFact{DefinedIn: []string{"types/job.go"}, SameNameDefinitions: []string{"internal/config/validate.go", "types/job.go"}}, true
		case "Cold":
			return SymbolFact{}, false
		}
		return SymbolFact{DefinedIn: []string{"internal/job/plan.go"}}, true
	}
	gate := func(names ...string) []types.CompletionGate {
		return []types.CompletionGate{
			{ID: "check", Check: types.LeaseCheck{Target: "test", Project: "."}},
			{ID: "sym", Kind: types.GateKindSymbol, Symbols: names},
		}
	}

	err := RefuseAmbiguousSymbols(t.Context(), gate("Declare", "Validate"), read)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"Validate"`)
	assert.Contains(t, err.Error(), "internal/config/validate.go")
	assert.Contains(t, err.Error(), "types/job.go")

	assert.NoError(t, RefuseAmbiguousSymbols(t.Context(), gate("Declare", "Cold"), read), "a name the graph cannot read fails open at declaration")
	assert.NoError(t, RefuseAmbiguousSymbols(t.Context(), gate("Declare"), nil))
}

type fakeSymbolGraph struct {
	nodes []types.KnowledgeMatch
	defs  map[string]string
}

func (g fakeSymbolGraph) Refs(ref string) (types.KnowledgeRefsOutput, bool) {
	for _, n := range g.nodes {
		if n.ID == ref || n.Label == ref {
			return types.KnowledgeRefsOutput{Symbol: n.ID, Label: n.Label, Defs: []types.KnowledgeRefSite{{File: g.defs[n.ID]}}}, true
		}
	}
	return types.KnowledgeRefsOutput{}, false
}

func (g fakeSymbolGraph) Resolve(input string, _ int) []types.KnowledgeMatch {
	var out []types.KnowledgeMatch
	for _, n := range g.nodes {
		if strings.Contains(n.Label, input) {
			out = append(out, n)
		}
	}
	return out
}

func TestGraphSymbolsListsEveryDefinitionOfABareName(t *testing.T) {
	t.Parallel()

	g := fakeSymbolGraph{
		nodes: []types.KnowledgeMatch{
			{ID: "symbol:config/Validate().", Kind: types.KindSymbol, Label: "Validate"},
			{ID: "symbol:types/Declaration#Validate().", Kind: types.KindSymbol, Label: "Validate"},
			{ID: "symbol:types/ValidateAll().", Kind: types.KindSymbol, Label: "ValidateAll"},
		},
		defs: map[string]string{
			"symbol:config/Validate().":            "internal/config/validate.go",
			"symbol:types/Declaration#Validate().": "types/job.go",
			"symbol:types/ValidateAll().":          "types/all.go",
		},
	}
	fact, ok := GraphSymbols(g)(t.Context(), "Validate")
	require.True(t, ok)
	assert.Equal(t, []string{"internal/config/validate.go", "types/job.go"}, fact.SameNameDefinitions)

	exact, ok := GraphSymbols(g)(t.Context(), "symbol:types/Declaration#Validate().")
	require.True(t, ok)
	assert.Empty(t, exact.SameNameDefinitions, "a full symbol id names one definition")
}

func TestRenderGatesSaysTheGraphWasStale(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	RenderGates(&out, types.JobStatus{
		Job:          "unit",
		Gates:        []types.GateStatus{{ID: "sym", Verified: true}},
		StaleIndexes: []string{".", "console"},
	})
	assert.Contains(t, out.String(), "1 of 1 goal(s) met")
	assert.Contains(t, out.String(), "stale")
	assert.Contains(t, out.String(), "., console")
	assert.Contains(t, out.String(), "graph build")
}

func TestWaitRefusesPassWhileADescendantIsLive(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	child := types.Job{ID: row.ID + "/child", Parent: row.ID, State: types.StateRunning}
	grandchild := types.Job{ID: child.ID + "/leaf", Parent: child.ID, State: types.StateDeclared}
	finished := types.Job{ID: row.ID + "/done", Parent: row.ID, State: types.StatePass}
	rep := passingResult()
	seen := Observed{Changed: rep.ChangedPaths, ChangedKnown: true}

	status := VerifyGates(row, rep, passingRun, nil, []types.Job{row, child, grandchild, finished}, seen)
	assert.False(t, status.Verified)
	joined := strings.Join(status.Violations, "\n")
	assert.Contains(t, joined, child.ID)
	assert.Contains(t, joined, grandchild.ID)
	assert.NotContains(t, joined, finished.ID)
}

func TestGradeGatesInheritsAncestorSymbolGates(t *testing.T) {
	t.Parallel()

	parent := types.Job{
		ID: "rename", State: types.StateRunning, WritePaths: []string{"."},
		Goals: []types.CompletionGate{
			{ID: "gone", Kind: types.GateKindSymbol, Expect: types.ExpectAbsent, Symbols: []string{"OldName"}},
			{ID: "tests", Check: types.LeaseCheck{Target: "test", Project: "."}},
		},
	}
	child := types.Job{
		ID: "rename/api", Parent: "rename", State: types.StateRunning, WritePaths: []string{"api"},
		Goals: []types.CompletionGate{{ID: "moved", Kind: types.GateKindPaths, Expect: types.ExpectPresent, Paths: []string{"api"}}},
	}
	loc := declared(t, parent, child)
	read := func(_ context.Context, name string) (SymbolFact, bool) {
		if name == "OldName" {
			return SymbolFact{DefinedIn: []string{"api/old.go"}}, true
		}
		return SymbolFact{}, true
	}

	status, err := GradeGates(t.Context(), NewStore(loc), child.ID, CheckpointObserver("", read))
	require.NoError(t, err)
	ids := make([]string, 0, len(status.Gates))
	for _, g := range status.Gates {
		ids = append(ids, g.ID)
	}
	assert.Contains(t, ids, "rename/gone", "a child is graded against its ancestors' symbol gates")
	assert.NotContains(t, ids, "rename/tests", "only symbol gates are inherited")
	assert.Contains(t, strings.Join(status.Violations, "\n"), `"OldName" is still defined in api/old.go`)
}

func TestVerifyHoldsTheClaimToTheObservedDiff(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	rep := passingResult()

	t.Run("a claimed path the diff does not show", func(t *testing.T) {
		t.Parallel()
		seen := Observed{Changed: []string{"internal/ledger/report.go"}, ChangedKnown: true, ChangedFrom: "abc"}
		status := VerifyGates(row, rep, passingRun, nil, nil, seen)
		assert.False(t, status.Verified)
		assert.Contains(t, strings.Join(status.Violations, "\n"), `"cmd/magus/ledger.go"`)
	})
	t.Run("no observed change inside the write paths", func(t *testing.T) {
		t.Parallel()
		claim := rep
		claim.ChangedPaths = []string{"internal/ledger/report.go"}
		seen := Observed{Changed: []string{"internal/ledger/report.go", "README.md"}, ChangedKnown: true}
		assert.True(t, VerifyGates(row, claim, passingRun, nil, nil, seen).Verified)

		outside := Observed{Changed: []string{"README.md"}, ChangedKnown: true}
		claim.ChangedPaths = nil
		status := VerifyGates(row, claim, passingRun, nil, nil, outside)
		assert.False(t, status.Verified)
		assert.Contains(t, strings.Join(status.Violations, "\n"), "inside its write paths")
	})
	t.Run("a diff nobody could read", func(t *testing.T) {
		t.Parallel()
		status := VerifyGates(row, rep, passingRun, nil, nil, Observed{})
		assert.False(t, status.Verified)
		assert.Contains(t, strings.Join(status.Violations, "\n"), "could not read")
	})
	t.Run("a read-only job needs no diff", func(t *testing.T) {
		t.Parallel()
		ro := row
		ro.ReadOnly, ro.WritePaths = true, nil
		claim := rep
		claim.ChangedPaths = nil
		assert.True(t, VerifyGates(ro, claim, passingRun, nil, nil, Observed{}).Verified)
	})
}

// TestForkMergeHoldsANewRowToTheLimits pins that magus\job.put forks under the same
// limits and default timeout `magus job fork` applies,
// while a merge onto an existing row stays an update nothing refuses.
func TestForkMergeHoldsANewRowToTheLimits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := NewStore(tmpLoc(t, t.TempDir()))
	limits := config.Jobs{MaxDepth: 1, DefaultTimeout: time.Hour}
	merge := func(parent string) func(*types.Job) {
		return func(u *types.Job) { u.Parent, u.State = parent, types.StateDeclared }
	}

	root, err := ForkMerge(ctx, s, "root", merge(""), limits, nil)
	require.NoError(t, err)
	assert.InDelta(t, time.Now().Add(time.Hour).Unix(), root.Deadline, 5, "a new row takes default_timeout")

	_, err = ForkMerge(ctx, s, "root/a", merge("root"), limits, nil)
	require.NoError(t, err)
	_, err = ForkMerge(ctx, s, "root/a/deep", merge("root/a"), limits, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jobs.max_depth")

	updated, err := ForkMerge(ctx, s, "root/a", func(u *types.Job) { u.Model = "opus" }, config.Jobs{MaxLive: 1}, nil)
	require.NoError(t, err, "an update is not a fork")
	assert.Equal(t, "opus", updated.Model)
}

// MGS3018 does not depend on which call wrote the path: a put that adds a directory to a
// row that exists is refused as a fork naming it would be, and writes nothing.
func TestForkMergeRefusesADirectoryAddedToARowThatExists(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := NewStore(tmpLoc(t, loadableRoot(t)))
	_, err := ForkMerge(ctx, s, "wave/job", func(u *types.Job) {
		u.Check, u.State, u.WritePaths = forkCheck(), types.StateDeclared, []string{"internal/job/store.go"}
	}, config.Jobs{}, nil)
	require.NoError(t, err)

	_, err = ForkMerge(ctx, s, "wave/job", func(u *types.Job) {
		u.WritePaths = append(u.WritePaths, "internal/**")
	}, config.Jobs{}, nil)
	require.ErrorIs(t, err, types.WritePathIsDirectory)
	rows, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/job/store.go"}, rows[0].WritePaths)
}

// A job that writes is held to something wait can grade; one that writes nothing is not.
func TestRefuseUngradedHoldsAWritingJobToACheckOrAGoal(t *testing.T) {
	t.Parallel()

	check := types.LeaseCheck{Target: "test", Project: "."}
	goal := types.CompletionGate{ID: "done", Kind: types.GateKindPaths, Paths: []string{"a.go"}}
	for name, tc := range map[string]struct {
		row     types.Job
		refused bool
	}{
		"a writing job with neither":    {row: types.Job{ID: "w", WritePaths: []string{"a.go"}}, refused: true},
		"a writing job with a check":    {row: types.Job{ID: "w", WritePaths: []string{"a.go"}, Check: &check}},
		"a writing job with a goal":     {row: types.Job{ID: "w", WritePaths: []string{"a.go"}, Goals: []types.CompletionGate{goal}}},
		"a read-only job":               {row: types.Job{ID: "r", ReadOnly: true}},
		"a job declaring no write path": {row: types.Job{ID: "r"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := RefuseUngraded(tc.row)
			if !tc.refused {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), `declares neither a check nor a goal`)
			assert.Contains(t, err.Error(), `"goals"`, "the refusal names what to add")
		})
	}

	_, err := ForkMerge(t.Context(), NewStore(tmpLoc(t, t.TempDir())), "w",
		func(u *types.Job) { u.WritePaths = []string{"a.go"} }, config.Jobs{}, nil)
	require.ErrorContains(t, err, "declares neither a check nor a goal", "the tool's fork and job.put hold a new row to it")
}

// appliedRow seeds w as a job a worker took, holding write and checked by forkCheck, in a
// root holding each path, the way `magus job apply` finds a live job.
func appliedRow(t *testing.T, write ...string) (*Store, Location) {
	t.Helper()
	root := loadableRoot(t)
	for _, p := range write {
		require.NoError(t, os.WriteFile(filepath.Join(root, p), []byte(p+"\n"), 0o644))
	}
	loc := tmpLoc(t, root)
	s := NewStore(loc)
	_, err := s.Update(t.Context(), "w", Declare(spec(write...), 0))
	require.NoError(t, err)
	_, err = s.Exec(t.Context(), "w", "abc123")
	require.NoError(t, err)
	_, err = s.Update(t.Context(), "w", func(u *types.Job) { u.State = types.StateRunning })
	require.NoError(t, err)
	return s, loc
}

func spec(write ...string) types.Declaration {
	return types.Declaration{ID: "w", Criteria: "goal", WritePaths: write, Check: forkCheck()}
}

func storeBytes(t *testing.T, s *Store) string {
	t.Helper()
	path, err := s.Path()
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

// Apply upserts the spec and leaves status alone: the holder keeps its job, its state and
// its registration, where a re-fork hands the job out again as declared.
func TestApplyWidensALiveJobAndKeepsItsState(t *testing.T) {
	t.Parallel()

	s, _ := appliedRow(t, "a.go")
	before, err := s.List()
	require.NoError(t, err)

	got, err := Apply(t.Context(), s, []types.Declaration{spec("a.go", "b.go")}, config.Jobs{}, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].Created)
	assert.Equal(t, []string{"write_paths"}, got[0].Changed)

	want := before[0].Clone()
	want.WritePaths = []string{"a.go", "b.go"}
	want.Updated = got[0].Next.Updated
	assert.Equal(t, want, got[0].Next)
	assert.Equal(t, types.StateRunning, got[0].Next.State)
}

// A worker's own row takes only the release: a spec dropping some of its paths, recorded as
// its own release rather than a revocation. Anything else is refused.
func TestApplyLetsAWorkerOnlyReleaseItsOwnPaths(t *testing.T) {
	t.Parallel()

	s, loc := appliedRow(t, "a.go", "b.go")
	worker := boundStore(loc, "w")

	shrunk, err := Apply(t.Context(), worker, []types.Declaration{spec("a.go")}, config.Jobs{}, nil, nil, false)
	require.NoError(t, err, "a worker may shrink its own row")
	assert.Equal(t, []string{"a.go"}, shrunk[0].Next.WritePaths)
	require.Len(t, shrunk[0].Next.Releases, 1)
	assert.False(t, shrunk[0].Next.Releases[0].Revoked, "giving up its own path is a release, not a revocation")

	var refused *RefusedError
	_, err = Apply(t.Context(), worker, []types.Declaration{spec("a.go", "c.go")}, config.Jobs{}, nil, nil, false)
	require.ErrorAs(t, err, &refused, "widening")
	criteria := spec("a.go")
	criteria.Criteria = "another goal"
	_, err = Apply(t.Context(), worker, []types.Declaration{criteria}, config.Jobs{}, nil, nil, true)
	require.ErrorAs(t, err, &refused, "a dry run answers what the write would")

	rows, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"a.go"}, rows[0].WritePaths)
	assert.Equal(t, "goal", rows[0].Criteria)
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	s, _ := appliedRow(t, "a.go", "b.go")
	before := storeBytes(t, s)

	got, err := Apply(t.Context(), s, []types.Declaration{spec("b.go")}, config.Jobs{}, nil, nil, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"b.go"}, got[0].Next.WritePaths, "the dry run returns the row the apply would write")
	require.Len(t, got[0].Next.Releases, 1)
	assert.Equal(t, "a.go", got[0].Next.Releases[0].Path)
	assert.True(t, got[0].Next.Releases[0].Revoked, "the orchestrator dropping a holder's path revokes it")
	assert.Equal(t, before, storeBytes(t, s))
}

// The stream is checked whole before any of it is written: a refusal at the second record
// (MGS3018) leaves the first unwritten too.
func TestApplyRefusalLeavesEveryRowUnchanged(t *testing.T) {
	t.Parallel()

	s, _ := appliedRow(t, "a.go")
	before := storeBytes(t, s)
	fresh := types.Declaration{ID: "x", WritePaths: []string{"internal/**"}, Check: forkCheck()}

	_, err := Apply(t.Context(), s, []types.Declaration{spec("a.go", "b.go"), fresh}, config.Jobs{}, nil, nil, false)
	require.ErrorIs(t, err, types.WritePathIsDirectory)
	assert.Equal(t, before, storeBytes(t, s))

	_, err = Apply(t.Context(), s, []types.Declaration{spec("a.go", "internal/**")}, config.Jobs{}, nil, nil, false)
	require.ErrorIs(t, err, types.WritePathIsDirectory, "a path added to a row that exists is held to the fork's rule")
	assert.Equal(t, before, storeBytes(t, s))
}

func TestApplyRefusesStatusAnEndedJobAndAnUnboundedOne(t *testing.T) {
	t.Parallel()

	s, _ := appliedRow(t, "a.go")
	withState := spec("a.go")
	withState.State = types.StatePass
	for name, tc := range map[string]struct {
		rec  types.Declaration
		want string
	}{
		"state":          {rec: withState, want: "which is status"},
		"no write paths": {rec: spec(), want: "`magus job exit w`"},
	} {
		_, err := Apply(t.Context(), s, []types.Declaration{tc.rec}, config.Jobs{}, nil, nil, false)
		require.ErrorContains(t, err, tc.want, name)
	}

	_, err := s.Update(t.Context(), "w", func(u *types.Job) { u.State = types.StateFail })
	require.NoError(t, err)
	_, err = Apply(t.Context(), s, []types.Declaration{spec("a.go", "b.go")}, config.Jobs{}, nil, nil, false)
	require.ErrorContains(t, err, "w already ended fail")
}

// A new id is a fork: declared, with this checkout's checkpoint when the record names none.
func TestApplyCreatesANewJob(t *testing.T) {
	t.Parallel()

	s := NewStore(tmpLoc(t, loadableRoot(t)))
	rec := types.Declaration{ID: "fresh", WritePaths: []string{"internal/job/store.go"}, Check: forkCheck()}
	got, err := Apply(t.Context(), s, []types.Declaration{rec}, config.Jobs{}, nil, func() string { return "rev-1" }, false)
	require.NoError(t, err)
	require.True(t, got[0].Created)
	assert.Equal(t, types.StateDeclared, got[0].Next.State)
	assert.Equal(t, "rev-1", got[0].Next.Checkpoint)
}
