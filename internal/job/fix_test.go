package job

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func fixMerge(d types.Declaration) func(*types.Job) { return Declare(d, 0) }

func TestFixForkAppliesEveryMechanicalEditAndLeavesJudgment(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	row := types.Declaration{ID: "w", Paths: types.JobPaths{Write: []string{"cli", "cli/registry.go"}}, Check: forkCheck()}
	before, err := validateDryFork(t.Context(), s, row.ID, fixMerge(row))
	require.NoError(t, err)
	require.NotZero(t, before.Problems.Fixable(), "the record starts with mechanical problems: %v", codes(before.Problems))

	fixed, edits, err := FixFork(t.Context(), s, row, fixMerge, config.Jobs{}, nil)
	require.NoError(t, err)

	require.NotEmpty(t, edits)
	assert.Contains(t, edits, types.JobEdit{AddWritePath: "docs/manpage/*.md"}, "an output regenerated outside the write paths is added to them")
	after, err := validateDryFork(t.Context(), s, fixed.ID, fixMerge(fixed))
	require.NoError(t, err)
	assert.Zero(t, after.Problems.Fixable(), "nothing mechanical is left: %v", after.Problems)
	assert.Equal(t, types.WritePathIsDirectory, problemAt(t, after.Problems, types.WritePathIsDirectory).Code, "a directory write path is a judgment, left alone")
	assert.Equal(t, "cli", fixed.Paths.Write[0])

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

	repo := testkit.NewRepository(t, loadableFiles)
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
		fixed, _, err := FixFork(t.Context(), s, record(token), own, config.Jobs{}, nil)
		require.NoError(t, err)
		assert.Equal(t, token, fixed.Checkpoint, "%s names a revision the repository holds", token)
	}

	unknown := record(madeUpRevision)
	fixed, edits, err := FixFork(t.Context(), s, unknown, own, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Empty(t, edits, "a revision nobody holds is an error to the forker, never moved to HEAD")
	assert.Equal(t, unknown, fixed)
	after, err := validateDryFork(t.Context(), s, fixed.ID, own(fixed))
	require.NoError(t, err)
	assert.Equal(t, types.JobCheckpointUnplaceable, problemAt(t, after.Problems, types.JobCheckpointUnplaceable).Code)
}

func TestFixApplyFixesEachRecord(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	records := []types.Declaration{{ID: "a", Paths: types.JobPaths{Write: []string{"cli/registry.go"}}, Check: forkCheck()}}

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
	}, []string{"cli/api.lock"}, gateOwner{})

	assert.Equal(t, &types.JobEdit{AddWritePath: "manpage/*.1"}, ps[0].Edit)
	assert.Equal(t, &types.JobEdit{AddDeviation: &types.Deviation{Code: types.OutputOutsideWritePaths, Subject: "gen/*.go !gen/*_test.go", Reason: integratorRegenerates}},
		ps[1].Edit, "an output with an exclusion no write path can spell is permitted for the integrator")
	assert.Nil(t, ps[2].Edit, "an output the record denies is its own decision")
}

// A Go job's fork meets the ci chain's test and build. The worker may run the test only
// without default charms and never the build, so --fix adds the charmless test goal and the
// build raises nothing: no deviation is left to write.
func TestFixForkLeavesNoGateNeedingADeviationForAGoJob(t *testing.T) {
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
	row := types.Declaration{ID: "w", Paths: types.JobPaths{Write: []string{"cli/registry.go"}}, Check: &types.LeaseCheck{Target: "vet", Project: "."}}

	before, err := validateDryFork(t.Context(), s, row.ID, fixMerge(row))
	require.NoError(t, err)
	var gates []string
	for _, p := range before.Problems {
		if p.Code == types.GateNotRun {
			gates = append(gates, p.Subject)
		}
	}
	assert.Equal(t, []string{"unit ."}, gates, "lint is added at fork and the build is the integrator's and asks for no goal or deviation")

	fixed, _, err := FixFork(t.Context(), s, row, fixMerge, config.Jobs{}, nil)
	require.NoError(t, err)
	after, err := validateDryFork(t.Context(), s, fixed.ID, fixMerge(fixed))
	require.NoError(t, err)
	assert.NotContains(t, codes(after.Problems), types.GateNotRun)
	assert.NotContains(t, codes(after.Problems), types.JobCheckUnrunnable, "every goal --fix added is one the worker may run")
	for _, w := range fixed.Deviations {
		assert.NotEqual(t, types.GateNotRun, w.Code, "no gate needed a deviation")
	}
}

// declaredGateSuite pins what --fix records for a gate and an output
// a worker's row leaves out, by who the workspace says owns them.
type declaredGateSuite struct {
	suite.Suite
	store *Store
}

func TestDeclaredGate(t *testing.T) { suite.Run(t, new(declaredGateSuite)) }

func (s *declaredGateSuite) SetupTest() {
	store, projects := validateWorkspace(s.T())
	top := projects[0]
	top.MagusfileTargets = append(top.MagusfileTargets, "vet", "unit")
	top.TargetInputs["unit"] = []types.InputRef{{Glob: "**/*.go"}}
	top.TargetChains["ci"] = []types.ChainStep{{Target: "lint"}, {Target: "unit"}}
	s.store = store
}

func (s *declaredGateSuite) writeConfig(body string) {
	s.Require().NoError(os.WriteFile(filepath.Join(s.store.root, "magus.yaml"), []byte(body), 0o644))
}

func (s *declaredGateSuite) record() types.Declaration {
	return types.Declaration{ID: "w", Paths: types.JobPaths{Write: []string{"cli/registry.go"}}, Check: &types.LeaseCheck{Target: "vet", Project: "."}}
}

func (s *declaredGateSuite) fix(row types.Declaration) (types.Declaration, []types.JobEdit) {
	fixed, edits, err := FixFork(s.T().Context(), s.store, row, fixMerge, config.Jobs{}, nil)
	s.Require().NoError(err)
	return fixed, edits
}

func (s *declaredGateSuite) TestRemoteGateRecordsTheDeviationAndAddsNoGoal() {
	s.writeConfig("ci:\n  remote_gate: github-actions\n")

	fixed, _ := s.fix(s.record())

	s.Empty(fixed.Goals, "no ci goal is added for a worker")
	s.Equal([]string{"cli/registry.go"}, fixed.Paths.Write, "no output widens the write paths")
	s.Contains(fixed.Deviations, types.Deviation{Code: types.GateNotRun, Subject: "unit .", Reason: "github-actions runs it"})
	s.Contains(fixed.Deviations, types.Deviation{Code: types.OutputOutsideWritePaths, Subject: "docs/manpage/*.md", Reason: integratorRegenerates})
	after, err := validateDryFork(s.T().Context(), s.store, fixed.ID, fixMerge(fixed))
	s.Require().NoError(err)
	s.Empty(after.Problems)
}

func (s *declaredGateSuite) TestWithoutAnOwnerTheGoalIsAdded() {
	fixed, edits := s.fix(s.record())

	checks := make([]types.LeaseCheck, len(fixed.Goals))
	for i, g := range fixed.Goals {
		checks[i] = g.Check
	}
	s.Contains(checks, types.LeaseCheck{Target: "unit", Project: "."})
	s.Contains(edits, types.JobEdit{AddWritePath: "docs/manpage/*.md"})
	for _, w := range fixed.Deviations {
		s.NotEqual(types.GateNotRun, w.Code)
	}
}

func (s *declaredGateSuite) TestIntegratingParentRunsTheGate() {
	seed(s.T(), s.store, types.Job{ID: "integrate", State: types.StateRunning, Integrates: []string{"a-branch"}})
	row := s.record()
	row.Parent = "integrate"

	fixed, _ := s.fix(row)

	s.Empty(fixed.Goals)
	s.Contains(fixed.Deviations, types.Deviation{Code: types.GateNotRun, Subject: "unit .", Reason: integratorRuns})
}

func (s *declaredGateSuite) TestUnreadableConfigGuessesNoEdit() {
	s.writeConfig("ci: [\n")

	ps := unrunGateProblems([]UnrunGate{{Target: "unit", Project: ".", Source: "**/*.go"}}, declaredGateOwner(s.store.root, nil, types.Job{ID: "w"}))

	s.Require().Len(ps, 1)
	s.Nil(ps[0].Edit)
}

func TestUnrunGateProblemsAddTheGoalLintRunsByWithoutCharms(t *testing.T) {
	t.Parallel()

	goal := types.Goal{ID: "lint", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "lint", Project: "."}}
	ps := unrunGateProblems([]UnrunGate{{Target: "lint", Project: ".", Source: "**/*.go", Goal: goal}}, gateOwner{runs: "github-actions runs it"})

	require.NotNil(t, ps[0].Edit)
	want := goal
	want.Check.NoDefaultCharms = true
	assert.Equal(t, &want, ps[0].Edit.AddGoal, "lint added by --fix reports instead of rewriting the worker's tree, whoever owns the other gates")
	assert.False(t, goal.Check.NoDefaultCharms, "the gate's own goal is not changed")
}
