package job

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// validateWorkspace is regeneratedWorkspace with the targets its root project exports and a
// ci chain that lints and tests, so every workspace check has something to read.
func validateWorkspace(t *testing.T) (*Store, []*types.Project) {
	t.Helper()
	root, projects := regeneratedWorkspace(t)
	top := projects[0]
	top.MagusfileTargets = []string{"ci", "lint", "test", "man-generate", "config-generate", "types-generate"}
	top.TargetInputs["lint"] = []types.InputRef{{Glob: "**/*.go"}}
	top.TargetChains = map[string][]types.ChainStep{"ci": {{Target: "lint"}, {Target: "test"}}}
	projects[1].MagusfileTargets = []string{"content-generate", "site-generate"}
	return NewStore(tempLocation(t, root)).InWorkspace(Workspace{Projects: projects}), projects
}

func codes(ps types.JobProblems) []types.DiagnosticCode {
	out := make([]types.DiagnosticCode, len(ps))
	for i, p := range ps {
		out[i] = p.Code
	}
	return out
}

func problemAt(t *testing.T, ps types.JobProblems, code types.DiagnosticCode) types.JobProblem {
	t.Helper()
	i := slices.IndexFunc(ps, func(p types.JobProblem) bool { return p.Code == code })
	require.GreaterOrEqual(t, i, 0, "no %s in %v", code, codes(ps))
	return ps[i]
}

func TestValidateMergeCollectsEveryProblem(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	v := validateMerge(t.Context(), s, nil, "w", func(u *types.Job) {
		u.WritePaths = []string{"cli", "cli/registry.go"}
		u.Check = &types.LeaseCheck{Target: "nosuch", Project: "."}
		u.Goals = []types.Goal{{ID: "elsewhere", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "test", Project: "nowhere"}}}
	}, config.Jobs{}, nil)

	got := codes(v.Problems)
	for _, want := range []types.DiagnosticCode{types.WritePathIsDirectory, types.JobCheckUnrunnable, types.OutputOutsideWritePaths, types.GateNotRun} {
		assert.Contains(t, got, want, "every problem at once")
	}
	assert.Equal(t, "write_paths[0]", problemAt(t, v.Problems, types.WritePathIsDirectory).Field)

	var target, project types.JobProblem
	for _, p := range v.Problems {
		switch p.Field {
		case "check.target":
			target = p
		case "goals[0].check.project":
			project = p
		}
	}
	assert.Contains(t, target.Message, `names target "nosuch", which project "." does not run`)
	assert.Contains(t, target.Fix, "config-generate, lint", "an unknown target names the project's targets")
	assert.Contains(t, project.Message, `names project "nowhere"`)
	assert.Equal(t, "name one of its projects: ., docs", project.Fix)

	regen := problemAt(t, v.Problems, types.OutputOutsideWritePaths)
	assert.NotEmpty(t, regen.Subject, "a waivable problem names what a waiver names")
	assert.Contains(t, regen.Fix, `"reason":"the root regenerates it"`)
	gate := problemAt(t, v.Problems, types.GateNotRun)
	assert.Equal(t, "lint .", gate.Subject)
	assert.Contains(t, gate.Fix, `"check":{"target":"lint","project":"."}`, "the fix is the goal that runs it")
	assert.Empty(t, v.Unchecked)
	assert.Equal(t, "w", v.Job.ID, "the result carries the row as it would be stored")
}

func TestValidateMergeWaivesOnlyWhatTheRecordNames(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	declare := func(waivers ...types.JobWaiver) func(*types.Job) {
		return func(u *types.Job) {
			u.WritePaths, u.Check, u.Waivers = []string{"cli/registry.go"}, forkCheck(), waivers
		}
	}
	v := validateMerge(t.Context(), s, nil, "w", declare(), config.Jobs{}, nil)
	require.NotEmpty(t, v.Problems)
	var waivers []types.JobWaiver
	for _, p := range v.Problems {
		require.True(t, types.Waivable(p.Code), "only waivable problems are left: %v", p)
		waivers = append(waivers, types.JobWaiver{Code: p.Code, Subject: p.Subject, Reason: "the root regenerates and runs it"})
	}

	waived := validateMerge(t.Context(), s, nil, "w", declare(waivers...), config.Jobs{}, nil)
	assert.Empty(t, waived.Problems)
	assert.Equal(t, v.Problems, waived.Waived)

	stored, err := ForkMerge(t.Context(), s, "w", declare(waivers...), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.NotZero(t, stored.Validated, "a validated row is stamped")
	assert.Equal(t, waivers, stored.Waivers)

	wrong := validateMerge(t.Context(), s, nil, "w2", func(u *types.Job) {
		u.WritePaths, u.Check = []string{"cli"}, forkCheck()
		u.Waivers = []types.JobWaiver{{Code: types.WritePathIsDirectory, Subject: "cli", Reason: "it is mine"}, {Code: types.GateNotRun, Subject: "lint ."}}
	}, config.Jobs{}, nil)
	fields := map[string]types.DiagnosticCode{}
	for _, p := range wrong.Problems {
		fields[p.Field] = p.Code
	}
	assert.Equal(t, types.JobRecordInvalid, fields["waivers[0].code"], "a waiver on a code that is not waivable is itself a problem")
	assert.Equal(t, types.JobRecordInvalid, fields["waivers[1].reason"], "and so is one with no reason")
	assert.Equal(t, types.WritePathIsDirectory, fields["write_paths[0]"], "and it waives nothing")
}

func TestValidateMergeNamesWhatItCouldNotCheck(t *testing.T) {
	t.Parallel()

	root, _ := regeneratedWorkspace(t)
	merge := func(u *types.Job) { u.WritePaths, u.Check = []string{"cli/registry.go"}, forkCheck() }

	v := validateMerge(t.Context(), NewStore(tempLocation(t, root)), nil, "w", merge, config.Jobs{}, nil)
	assert.Empty(t, v.Problems)
	assert.Len(t, v.Unchecked, 4, "the target, outputs, gates and breadth checks each say they did not run")
	for _, why := range v.Unchecked {
		assert.Contains(t, why, "without a workspace")
	}

	unavailable := NewStore(tempLocation(t, root)).InWorkspace(Workspace{Unavailable: "magusfile.buzz: line 3: unexpected }"})
	v = validateMerge(t.Context(), unavailable, nil, "w", merge, config.Jobs{}, nil)
	require.Len(t, v.Unchecked, 4)
	assert.Contains(t, v.Unchecked[0], "the workspace did not load: magusfile.buzz: line 3")

	v = validateMerge(t.Context(), unavailable, nil, "scout", func(u *types.Job) { u.ReadOnly = true }, config.Jobs{}, nil)
	assert.Empty(t, v.Unchecked, "a read-only row with no check skips nothing it would have needed")
}

func TestValidateForkWritesNothing(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	v, err := ValidateFork(t.Context(), s, "w", func(u *types.Job) {
		u.WritePaths, u.Check, u.Checkpoint = []string{"cli"}, forkCheck(), "abc+zz"
	}, config.Jobs{}, nil)
	require.NoError(t, err)
	got := codes(v.Problems)
	assert.Contains(t, got, types.JobCheckpointUnplaceable, "the checkpoint is judged in the same pass")
	assert.Contains(t, got, types.WritePathIsDirectory)
	assert.Equal(t, "checkpoint", problemAt(t, v.Problems, types.JobCheckpointUnplaceable).Field)

	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows, "a dry run writes nothing")

	_, err = ForkMerge(t.Context(), s, "w", func(u *types.Job) {
		u.WritePaths, u.Check, u.Checkpoint = []string{"cli"}, forkCheck(), "abc+zz"
	}, config.Jobs{}, nil)
	require.ErrorIs(t, err, types.JobCheckpointUnplaceable)
	require.ErrorIs(t, err, types.WritePathIsDirectory, "a fork reports both, not the first")
}

func TestValidateMergeJudgesAnUpdateByWhatItAdds(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	seed(t, s, types.Job{ID: "w", State: types.StateRunning, WritePaths: []string{"cli/**", "docs/**"}, Check: forkCheck()})
	rows, err := s.List()
	require.NoError(t, err)

	narrowed := validateMerge(t.Context(), s, rows, "w", func(u *types.Job) { u.WritePaths = []string{"cli/registry.go"} }, config.Jobs{}, nil)
	assert.Empty(t, narrowed.Problems, "narrowing a glob to a file it covers adds nothing")

	widened := validateMerge(t.Context(), s, rows, "w", func(u *types.Job) { u.WritePaths = []string{"cli/**", "docs/**", "cmd"} }, config.Jobs{}, nil)
	assert.Equal(t, "write_paths[2]", problemAt(t, widened.Problems, types.WritePathIsDirectory).Field,
		"a problem found on what the update adds names the field in the whole row")
	assert.NotContains(t, codes(widened.Problems), types.JobRecordInvalid, "a shape the row already had is not the update's")
}

func TestApplyCollectsEveryRecordsProblems(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	_, err := Apply(t.Context(), s, []types.Declaration{
		{ID: "a", WritePaths: []string{"cli"}, Check: forkCheck()},
		{ID: "b", WritePaths: []string{"cli/registry.go"}, State: types.StateRunning, Check: forkCheck()},
	}, config.Jobs{}, nil, nil, false)
	require.ErrorIs(t, err, types.WritePathIsDirectory)
	require.ErrorIs(t, err, types.JobRecordInvalid)
	assert.Contains(t, err.Error(), `a: "cli", inside project "." is a directory`, "each message names its record")
	assert.Contains(t, err.Error(), `b: the record for b carries state "running"`)

	rows, lerr := s.List()
	require.NoError(t, lerr)
	assert.Empty(t, rows, "nothing is written while any record has a problem")
}
