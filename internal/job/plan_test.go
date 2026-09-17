package job

import (
	"context"
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
	assert.Contains(t, out.String(), "1 of 1 completion gate(s) met")
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
		CompletionGates: []types.CompletionGate{
			{ID: "gone", Kind: types.GateKindSymbol, Expect: types.ExpectAbsent, Symbols: []string{"OldName"}},
			{ID: "tests", Check: types.LeaseCheck{Target: "test", Project: "."}},
		},
	}
	child := types.Job{
		ID: "rename/api", Parent: "rename", State: types.StateRunning, WritePaths: []string{"api"},
		CompletionGates: []types.CompletionGate{{ID: "moved", Kind: types.GateKindPaths, Expect: types.ExpectPresent, Paths: []string{"api"}}},
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

// TestForkMergeHoldsANewRowToTheLimits pins that the merge doors (the magus_job tool and
// magus\job.put) fork under the same limits and default timeout `magus job fork` applies,
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
