package job

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func fixMerge(d types.Declaration) func(*types.Job) { return Declare(d, 0) }

func TestFixForkAppliesEveryMechanicalEditAndLeavesJudgment(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	row := types.Declaration{ID: "w", WritePaths: []string{"cli", "cli/registry.go"}, Check: forkCheck()}
	before, err := ValidateFork(t.Context(), s, row.ID, fixMerge(row), config.Jobs{}, nil)
	require.NoError(t, err)
	require.NotZero(t, before.Problems.Fixable(), "the record starts with mechanical problems: %v", codes(before.Problems))

	fixed, edits, err := FixFork(t.Context(), s, row, fixMerge, config.Jobs{}, nil)
	require.NoError(t, err)

	require.NotEmpty(t, edits)
	assert.Contains(t, edits, types.JobEdit{AddWritePath: "docs/manpage/*.md"}, "an output regenerated outside the write paths is added to them")
	after, err := ValidateFork(t.Context(), s, fixed.ID, fixMerge(fixed), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Zero(t, after.Problems.Fixable(), "nothing mechanical is left: %v", after.Problems)
	assert.Equal(t, types.WritePathIsDirectory, problemAt(t, after.Problems, types.WritePathIsDirectory).Code, "a directory write path is a judgment, left alone")
	assert.Equal(t, "cli", fixed.WritePaths[0])

	again, more, err := FixFork(t.Context(), s, fixed, fixMerge, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Empty(t, more, "a fixed record has nothing left to fix")
	assert.Equal(t, fixed, again)
	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows, "fixing writes nothing")
}

// --fix never starts a job somewhere else than it was told. A sibling branch is placeable
// for a job in its own checkout, a digest of the wrong shape still names a real revision,
// and a revision nobody holds stays the MGS3041 error.
func TestFixForkNeverMovesACheckpointToHead(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, loadableFiles)
	repo.Git("switch", "-q", "-c", "elsewhere")
	elsewhere := repo.Commit("elsewhere", map[string]string{"elsewhere.txt": "x\n"})
	repo.Git("switch", "-q", "main")
	s := NewStore(tempLocation(t, repo.Root()))
	own := func(d types.Declaration) func(*types.Job) {
		return func(u *types.Job) {
			fixMerge(d)(u)
			u.Worktree = &types.JobWorktree{Root: d.Checkout}
		}
	}
	record := func(token string) types.Declaration {
		d := checkpointRecord("w", token)
		d.Checkout = filepath.Join(t.TempDir(), "own")
		return d
	}

	for _, token := range []string{elsewhere, elsewhere + "+not-a-digest"} {
		fixed, edits, err := FixFork(t.Context(), s, record(token), own, config.Jobs{}, nil)
		require.NoError(t, err)
		assert.NotContains(t, edits, types.JobEdit{RefreshCheckpoint: repo.Head()}, "%s names a revision the repository holds", token)
		assert.Equal(t, token, fixed.Checkpoint)
	}

	unknown := record(madeUpRevision)
	fixed, edits, err := FixFork(t.Context(), s, unknown, own, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Empty(t, edits, "a revision nobody holds is an error to the forker, never moved to HEAD")
	assert.Equal(t, unknown, fixed)
	after, err := ValidateFork(t.Context(), s, fixed.ID, own(fixed), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, types.JobCheckpointUnplaceable, problemAt(t, after.Problems, types.JobCheckpointUnplaceable).Code)
}

func TestFixApplyFixesEachRecord(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	records := []types.Declaration{{ID: "a", WritePaths: []string{"cli/registry.go"}, Check: forkCheck()}}

	fixed, edits := FixApply(t.Context(), s, records, config.Jobs{}, nil, nil)

	require.NotEmpty(t, edits["a"])
	_, err := Apply(t.Context(), s, fixed, config.Jobs{}, nil, nil, true)
	require.NoError(t, err, "every problem the record had was mechanical")
}

func TestRegeneratedProblemsLeaveADeniedOutputToJudgment(t *testing.T) {
	t.Parallel()

	ps := regeneratedProblems([]RegeneratedOutput{
		{Target: ".:man-generate", Output: "manpage/*.1", Source: "cli/registry.go"},
		{Target: ".:types-generate", Output: "gen/*.go !gen/*_test.go", Source: "types/job.go"},
		{Target: ".:config-generate", Output: "cli/api.lock", Source: "cli/registry.go"},
	}, []string{"cli/api.lock"})

	assert.Equal(t, &types.JobEdit{AddWritePath: "manpage/*.1"}, ps[0].Edit)
	assert.Equal(t, &types.JobEdit{AddWaiver: &types.JobWaiver{Code: types.OutputOutsideWritePaths, Subject: "gen/*.go !gen/*_test.go", Reason: integratorRegenerates}},
		ps[1].Edit, "an output with an exclusion no write path can spell is waived for the integrator")
	assert.Nil(t, ps[2].Edit, "an output the record denies is its own decision")
}

// A Go job's fork meets the ci chain's test and build. The worker may run the test only
// without default charms and never the build, so --fix adds the charmless test goal and the
// build raises nothing: no waiver is left to write.
func TestFixForkLeavesNoGateNeedingAWaiverForAGoJob(t *testing.T) {
	t.Parallel()

	s, projects := validateWorkspace(t)
	top := projects[0]
	top.MagusfileTargets = append(top.MagusfileTargets, "build", "unit", "vet")
	top.TargetInputs["unit"] = []types.InputRef{{Glob: "**/*.go"}}
	top.TargetInputs["build"] = []types.InputRef{{Glob: "**/*.go"}}
	top.TargetChains["ci"] = []types.ChainStep{{Target: "lint"}, {Target: "unit"}, {Target: "build"}}
	s.grader = func(_ context.Context, _ string, row types.Job) CheckVerdict {
		c := row.Check
		switch {
		case c.Target == "build":
			return CheckVerdict{Denied: true, Rule: "worker-builds-magus", Reason: "a worker does not build magus"}
		case c.Target == "unit" && !c.NoDefaultCharms:
			return CheckVerdict{Denied: true, Rule: "lease-gate", Reason: "test needs --no-default-charms"}
		}
		return CheckVerdict{}
	}
	row := types.Declaration{ID: "w", WritePaths: []string{"cli/registry.go"}, Check: &types.LeaseCheck{Target: "vet", Project: "."}}

	before, err := ValidateFork(t.Context(), s, row.ID, fixMerge(row), config.Jobs{}, nil)
	require.NoError(t, err)
	var gates []string
	for _, p := range before.Problems {
		if p.Code == types.GateNotRun {
			gates = append(gates, p.Subject)
		}
	}
	assert.Equal(t, []string{"unit ."}, gates, "lint is added at fork and the build is the integrator's and asks for no goal or waiver")

	fixed, _, err := FixFork(t.Context(), s, row, fixMerge, config.Jobs{}, nil)
	require.NoError(t, err)
	after, err := ValidateFork(t.Context(), s, fixed.ID, fixMerge(fixed), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.NotContains(t, codes(after.Problems), types.GateNotRun)
	assert.NotContains(t, codes(after.Problems), types.JobCheckUnrunnable, "every goal --fix added is one the worker may run")
	for _, w := range fixed.Waivers {
		assert.NotEqual(t, types.GateNotRun, w.Code, "no gate needed a waiver")
	}
}

func TestUnrunGateProblemsAddTheGoalLintRunsByWithoutCharms(t *testing.T) {
	t.Parallel()

	goal := types.Goal{ID: "lint", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "lint", Project: "."}}
	ps := unrunGateProblems([]UnrunGate{{Target: "lint", Project: ".", Source: "**/*.go", Goal: goal}})

	require.NotNil(t, ps[0].Edit)
	want := goal
	want.Check.NoDefaultCharms = true
	assert.Equal(t, &want, ps[0].Edit.AddGoal, "lint added by --fix reports instead of rewriting the worker's tree")
	assert.False(t, goal.Check.NoDefaultCharms, "the gate's own goal is not changed")
}
