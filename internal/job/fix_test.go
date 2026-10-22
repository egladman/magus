package job

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
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

	fixed, edits, err := FixFork(t.Context(), s, row, fixMerge, config.Jobs{}, nil, false)
	require.NoError(t, err)

	require.NotEmpty(t, edits)
	assert.Contains(t, edits, types.JobEdit{AddWritePath: "docs/manpage/*.md"}, "an output regenerated outside the write paths is added to them")
	after, err := ValidateFork(t.Context(), s, fixed.ID, fixMerge(fixed), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Zero(t, after.Problems.Fixable(), "nothing mechanical is left: %v", after.Problems)
	assert.Equal(t, types.WritePathIsDirectory, problemAt(t, after.Problems, types.WritePathIsDirectory).Code, "a directory write path is a judgment, left alone")
	assert.Equal(t, "cli", fixed.WritePaths[0])

	again, more, err := FixFork(t.Context(), s, fixed, fixMerge, config.Jobs{}, nil, false)
	require.NoError(t, err)
	assert.Empty(t, more, "a fixed record has nothing left to fix")
	assert.Equal(t, fixed, again)
	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows, "fixing writes nothing")
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
